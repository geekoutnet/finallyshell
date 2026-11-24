package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

const opsTestDataAuth = `2026-04-21T01:02:03+0000 test-box sshd[22]: Failed password for invalid user guest from 192.0.2.12 port 43112 ssh2
2026-04-21T01:02:19+0000 test-box sshd[23]: Failed password for invalid user guest from 192.0.2.12 port 43113 ssh2
2026-04-21T01:03:03+0000 test-box sshd[24]: Invalid user deploy from 198.51.100.22 port 43114
2026-04-21T01:04:03+0000 test-box sshd[25]: Failed password for admin from 203.0.113.11 port 43115 ssh2
2026-04-21T01:08:03+0000 test-box sshd[27]: Accepted publickey for ops from 192.0.2.44 port 43116 ssh2`

const opsTestUFW = `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)
[ 1] 2200/tcp                  ALLOW IN    Anywhere                    # SSH
[ 2] 443/tcp                   ALLOW IN    Anywhere                    # HTTPS
[ 3] 5353/udp                  DENY IN     192.0.2.0/24`

type opsTestRunner struct {
	actions chan string
	reads   chan string
	root    chan string
}

func (fake *opsTestRunner) run(_ context.Context, script string, progress func(string)) (string, string, error) {
	var data string
	switch script {
	case monitor.DiskMountScript:
		data = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/example1 65536000 41943040 23592960 64% /\n/dev/example2 419430400 62914560 356515840 15% /srv\n/dev/example3 1048576 125829 922747 12% /boot\n"
	case monitor.DiskFreeRootScript:
		data = "yes\n"
	case monitor.DiskCleanupEstimatesScript:
		data = "apt-get\njournalctl\n344060\t/var/cache/apt/archives\n375411\t/var/log/journal\n82100\t/var/log/app.log.1.gz\n"
	case monitor.PackageInfoScript:
		data = "apt\n\"Debian GNU/Linux 12 (bookworm)\"\n6.1.0-32-amd64\n"
	case monitor.PackageListScript("apt"):
		data = "Listing...\nopenssl/bookworm-security 3.0.15-1 amd64 [upgradable from: 3.0.14-1]\ncurl/bookworm-security 7.88.1-10 amd64 [upgradable from: 7.88.1-9]\nbash/bookworm 5.2.16-4 amd64 [upgradable from: 5.2.16-3]\ntzdata/bookworm 2025b all [upgradable from: 2025a]\n"
	case monitor.PackageLastUpdateScript("apt"):
		data = "2026-04-19  07:25:31\n"
	case monitor.PackageRebootCheckScript("apt"):
		data = "required\n"
	case monitor.SecurityAuthScript:
		data = opsTestDataAuth
	case monitor.SecurityUsersScript:
		data = "ops pts/0 2026-04-21 07:17 (192.0.2.44)\n---last---\nops pts/0 192.0.2.44 Tue Apr 21 07:17:01 2026\n"
	case monitor.SecuritySettingsScript:
		data = "port 2200\npermitrootlogin prohibit-password\npasswordauthentication no\n"
	case monitor.SecuritySudoScript:
		data = "2026-04-21T01:09:03+0000 test-box sudo: ops\n"
	case monitor.Fail2banStatusScript:
		data = "installed\nStatus\n`- Jail list: sshd, web-auth\n"
	case "'fail2ban-client' 'status' 'sshd'":
		data = "Status for the jail: sshd\n`- Banned IP list: 192.0.2.12 2001:db8::1\n"
	case "'fail2ban-client' 'status' 'web-auth'":
		data = "Status for the jail: web-auth\n`- Banned IP list:\n"
	case monitor.FirewallDetectScript:
		data = "ufw\nnft\niptables\n"
	case monitor.UFWReadScript:
		data = opsTestUFW
	case monitor.FirewallSessionScript:
		data = "192.0.2.44 51123 192.0.2.9 2200\n"
	case monitor.FirewallConnectionsScript:
		data = "ESTAB 0 0 192.0.2.9:2200 192.0.2.44:51123 users:((\"sshd\",pid=438,fd=4))\n"
	case monitor.FirewallListeningScript:
		data = "Netid State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process\ntcp   LISTEN 0      128    0.0.0.0:2200       0.0.0.0:*        users:((\"sshd\",pid=438,fd=4))\n"
	default:
		if strings.HasPrefix(script, "if command -v 'timeout'") && strings.Contains(script, "export LC_ALL=C\n[ -d ") {
			if strings.Contains(script, "'-size' '+50M'") {
				data = "958879510\t1776698801.0\t/srv/log/nginx.log\x00320789911\t1776647841.0\t/srv/db/archive.tar\x00"
			} else {
				data = "F\t/srv/backup.log\x0061579050\t1776698801\t/srv/backups\x0037256810\t1776659841\t/srv/db\x008351100\t1776695800\t/srv/log\x001320500\t1776698822\t/srv/backup.log\x00112655000\t1776699841\t/srv\x00"
			}
		} else {
			fake.actions <- script
			if progress != nil {
				progress("Processing example target…\n")
			}
			if strings.Contains(script, "'unbanip'") {
				return "", "test: daemon is busy", errors.New("exit status 1")
			}
			return "test: complete", "", nil
		}
	}
	select {
	case fake.reads <- script:
	default:
	}
	if progress != nil {
		progress(data)
	}
	return data, "", nil
}

func opsTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *opsTestRunner) {
	t.Helper()
	a, platform := testApp(t)
	s := demoSession(a, "staging-example", "192.0.2.9")
	a.active = 0
	fake := &opsTestRunner{make(chan string, 24), make(chan string, 160), make(chan string, 160)}
	s.server.disk.runner, s.server.updates.runner, s.server.security.runner, s.server.firewall.runner = fake.run, fake.run, fake.run, fake.run
	s.overview.memory.privilegeRunner = func(ctx context.Context, req monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if req.Probe {
			return monitor.PrivilegeResult{Stdout: "root"}, nil
		}
		select {
		case fake.root <- req.Action:
		default:
		}
		stdout, stderr, err := fake.run(ctx, req.Action, nil)
		return monitor.PrivilegeResult{Stdout: stdout, Stderr: stderr, Started: true}, err
	}
	a.openPanelTab(s, tabSystem)
	tester := ui.NewTester(a.View, 1280, 820)
	platform.tt = tester
	return a, platform, tester, s, fake
}

func openOpsTestSection(t *testing.T, p *fakePlatform, s *Session, section int, state *opsState) {
	t.Helper()
	s.server.section = section
	p.tt.Frame()
	p.until(t, "ops section data", func() bool { return state.loaded && !state.reading })
	p.tt.Frame()
}

func opsClick(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	button, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok || button.Disabled {
		t.Fatalf("no enabled button %q; texts %q", label, tester.Texts())
	}
	tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	tester.Frame()
}

func assertOpsNoAction(t *testing.T, fake *opsTestRunner) {
	t.Helper()
	select {
	case action := <-fake.actions:
		t.Fatalf("unexpected mutation: %s", action)
	default:
	}
}

func takeOpsAction(t *testing.T, fake *opsTestRunner, expected string) {
	t.Helper()
	select {
	case actual := <-fake.actions:
		if actual != expected {
			t.Fatalf("unexpected mutation: %s", actual)
		}
	default:
		t.Fatal("no confirmed operation was run")
	}
	for {
		select {
		case script := <-fake.root:
			if script == expected {
				return
			}
		default:
			t.Fatal("confirmed operation did not go through escalation")
		}
	}
}

func TestOpsSections(t *testing.T) {
	a, platform, tester, s, fake := opsTestApp(t)
	disk := &s.server.disk
	openOpsTestSection(t, platform, s, serverSecDisk, &disk.opsState)
	wantTexts(t, tester, "mounts", "/srv", "/boot", "64%")
	disk.pathInput = "/srv"
	tester.Frame()
	opsClick(t, tester, locales.DuScan())
	platform.until(t, "scan", func() bool { return !disk.reading && len(disk.scan.Entries) == 4 })
	if !disk.root || disk.rows()[0].Path != "/srv/backups" {
		t.Fatal("missing root scan or size ordering")
	}
	disk.filter = "back"
	disk.sort = ui.SortOrder{Column: "size"}
	tester.Frame()
	if rows := disk.rows(); len(rows) != 2 || rows[0].Path != "/srv/backup.log" {
		t.Fatalf("filtered rows: %+v", rows)
	}
	assertOpsNoAction(t, fake)

	updates := &s.server.updates
	openOpsTestSection(t, platform, s, serverSecUpdates, &updates.opsState)
	wantTexts(t, tester, "updates", "openssl:amd64", locales.UpdRebootRequired(), locales.UpdSecurityCount(2))
	updates.filter = "curl"
	tester.Frame()
	if len(updates.view()) != 1 {
		t.Fatal("package filter")
	}
	opsClick(t, tester, locales.UpdRefresh())
	opsClick(t, tester, locales.Cancel())
	assertOpsNoAction(t, fake)
	opsClick(t, tester, locales.UpdRefresh())
	platform.until(t, "confirm button", func() bool { return updates.dialog != nil })
	opsClick(t, tester, locales.UpdRefresh())
	platform.until(t, "refresh completion", func() bool {
		return updates.dialog != nil && updates.dialog.work != nil && updates.dialog.work.done && !updates.reading
	})
	select {
	case action := <-fake.actions:
		if action != "LC_ALL=C DEBIAN_FRONTEND=noninteractive 'apt-get' 'update'" {
			t.Fatalf("refresh action: %s", action)
		}
	default:
		t.Fatal("missing elevated package refresh")
	}
	wantTexts(t, tester, "result", "test: complete")
	opsClick(t, tester, locales.Close())

	security := &s.server.security
	openOpsTestSection(t, platform, s, serverSecSecurity, &security.opsState)
	wantTexts(t, tester, "SSH summaries", "192.0.2.12", "198.51.100.22")
	if rows := a.securityLoginRows(security); len(rows) != 3 || rows[0].count != 2 {
		t.Fatalf("SSH rows: %+v", rows)
	}
	security.view = 4
	tester.Frame()
	wantTexts(t, tester, "bans", "2001:db8::1")
	clickNamed(t, tester, "192.0.2.12")
	opsClick(t, tester, locales.SecUnban())
	opsClick(t, tester, locales.SecUnban())
	platform.until(t, "unban error", func() bool { return security.dialog != nil && security.dialog.work != nil && security.dialog.work.done })
	wantTexts(t, tester, "unban error", "test: daemon is busy")
	select {
	case action := <-fake.actions:
		if action != "'fail2ban-client' 'set' 'sshd' 'unbanip' '192.0.2.12'" {
			t.Fatalf("unban action: %s", action)
		}
	default:
		t.Fatal("missing elevated unban")
	}
	opsClick(t, tester, locales.Close())

	firewall := &s.server.firewall
	openOpsTestSection(t, platform, s, serverSecFirewall, &firewall.opsState)
	wantTexts(t, tester, "firewall", "2200/tcp", "443/tcp", locales.FwSSHPort("2200"))
	if why := firewall.ufwDeleteReason(firewall.ufw.Rules[0]); why != locales.FwProtected() {
		t.Fatalf("SSH guard: %q", why)
	}
	if why := firewall.ufwDeleteReason(firewall.ufw.Rules[1]); why != "" {
		t.Fatalf("HTTPS blocked: %q", why)
	}
	clickNamed(t, tester, "2200/tcp")
	if button, ok := tester.AccessByRole(ui.RoleButton, locales.Delete()); !ok || !button.Disabled {
		t.Fatal("SSH rule delete must be disabled")
	}
	clickNamed(t, tester, "443/tcp")
	opsClick(t, tester, locales.Delete())
	opsClick(t, tester, locales.Cancel())
	assertOpsNoAction(t, fake)
	opsClick(t, tester, locales.Delete())
	opsClick(t, tester, locales.Delete())
	platform.until(t, "delete firewall rule and reread", func() bool {
		return firewall.dialog != nil && firewall.dialog.work != nil && firewall.dialog.work.done && !firewall.reading
	})
	cmd, _ := monitor.UFWDeleteScript(firewall.ufw.Rules[1], firewall.protected.ports)
	takeOpsAction(t, fake, cmd)
}

func TestOpsConfirmActions(t *testing.T) {
	a, p, tt, s, fake := opsTestApp(t)
	disk, upd, sec, fw := &s.server.disk, &s.server.updates, &s.server.security, &s.server.firewall
	openOpsTestSection(t, p, s, serverSecDisk, &disk.opsState)
	openOpsTestSection(t, p, s, serverSecUpdates, &upd.opsState)
	openOpsTestSection(t, p, s, serverSecSecurity, &sec.opsState)
	openOpsTestSection(t, p, s, serverSecFirewall, &fw.opsState)
	for _, item := range []struct {
		name    string
		section int
		state   *opsState
		open    func()
		want    string
	}{
		{"disk-delete", serverSecDisk, &disk.opsState, func() {
			cmd, _ := monitor.DiskDeleteScript([]string{"/srv/backup"})
			a.diskConfirm(s, locales.Delete(), locales.DuDeleteWarning()+"\n/srv/backup (4 GB)", opsStatic(cmd))
		}, opsTestCommand(monitor.DiskDeleteScript([]string{"/srv/backup"}))},
		{"disk-truncate", serverSecDisk, &disk.opsState, func() {
			cmd, _ := monitor.DiskTruncateScript("/srv/service.log")
			a.diskConfirm(s, locales.DuTruncate(), locales.DuTruncateWarning()+"\n/srv/service.log", opsStatic(cmd))
		}, opsTestCommand(monitor.DiskTruncateScript("/srv/service.log"))},
		{"upgrade-all", serverSecUpdates, &upd.opsState, func() { a.updatesConfirm(s, "all", upd.names("all", upd.rows)) }, "LC_ALL=C DEBIAN_FRONTEND=noninteractive 'apt-get' 'upgrade' '-y' '--no-remove'"},
		{"upgrade-security", serverSecUpdates, &upd.opsState, func() { a.updatesConfirm(s, "security", upd.names("security", upd.rows)) }, "LC_ALL=C DEBIAN_FRONTEND=noninteractive 'apt-get' 'install' '--only-upgrade' '--no-remove' '-y' '--' 'openssl:amd64=3.0.15-1' 'curl:amd64=7.88.1-10'"},
		{"reboot", serverSecUpdates, &upd.opsState, func() { a.updatesConfirm(s, "reboot", nil) }, monitor.PackageRebootScript},
		{"ufw-enable", serverSecFirewall, &fw.opsState, func() { a.ufwToggleDialog(s, true) }, opsTestCommand(monitor.UFWToggleScript(true, fw.protected.actual, fw.protected.peer))},
		{"ufw-disable", serverSecFirewall, &fw.opsState, func() { a.ufwToggleDialog(s, false) }, opsTestCommand(monitor.UFWToggleScript(false, fw.protected.actual, fw.protected.peer))},
	} {
		t.Run(item.name, func(t *testing.T) {
			s.server.section = item.section
			item.open()
			tt.Frame()
			label := item.state.dialog.action
			opsClick(t, tt, locales.Cancel())
			assertOpsNoAction(t, fake)
			item.open()
			tt.Frame()
			opsClick(t, tt, label)
			p.until(t, "confirmed result", func() bool {
				return item.state.dialog != nil && item.state.dialog.work != nil && item.state.dialog.work.done && !item.state.reading
			})
			takeOpsAction(t, fake, item.want)
			opsClick(t, tt, locales.Close())
		})
	}
}

func opsTestCommand(script string, err error) string {
	if err != nil {
		panic(err)
	}
	return script
}

func TestOpsRedactsCommandOutput(t *testing.T) {
	const input = "failed https://example-user:example-password@example.invalid; token=example-token; echo example-login"
	clean := opsSafeOutput(input, "example-login")
	if strings.Count(clean, "[redacted]") != 3 || strings.Contains(clean, "example-login") {
		t.Fatal("a credential was not redacted")
	}
}

func TestOpsScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, platform, tester, s, fake := opsTestApp(t)
			platform.SetLocale(language)
			done := make(chan struct{})
			term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
			if err != nil {
				close(done)
				t.Fatal(err)
			}
			t.Cleanup(func() { close(done); term.Close() })
			s.setTerm(term)
			term.Feed([]byte("\x1b[32mops@staging-example\x1b[0m:~$ "))
			for _, mode := range []string{"light", "dark"} {
				tester.SetDark(mode == "dark")
				shot := func(scene string) {
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "ops-"+scene+"-"+language+"-"+mode+".png")
				}
				for _, item := range []struct {
					section int
					name    string
					state   *opsState
				}{
					{serverSecDisk, "disk", &s.server.disk.opsState},
					{serverSecUpdates, "updates", &s.server.updates.opsState},
					{serverSecSecurity, "security", &s.server.security.opsState},
					{serverSecFirewall, "firewall", &s.server.firewall.opsState},
				} {
					a.openPanelTab(s, tabSystem)
					openOpsTestSection(t, platform, s, item.section, item.state)
					shot(item.name + "-full")
					a.active = 0
					a.openServerPage(s, item.section)
					shot(item.name + "-short")
					a.setPanelOnRight(true)
					shot(item.name + "-right")
					a.setPanelOnRight(false)
					a.openPanelTab(s, tabSystem)
					switch item.section {
					case serverSecDisk:
						s.server.disk.mode = 1
						a.scanServerDisk(s, "/srv", false)
						platform.until(t, "folder scan", func() bool { return !s.server.disk.reading })
						shot("disk-folders")
						script, _ := monitor.DiskDeleteScript([]string{"/srv/backups"})
						a.diskConfirm(s, locales.Delete(), locales.DuDeleteWarning()+"\n\n/srv/backups (58.7 GB)", opsStatic(script))
					case serverSecUpdates:
						a.updatesConfirm(s, "security", s.server.updates.names("security", s.server.updates.rows))
					case serverSecSecurity:
						s.server.security.view = 4
						shot("security-fail2ban")
						a.securityBanDialog(s, "sshd", s.server.security.jails[0].ips[0], false)
					case serverSecFirewall:
						a.ufwAddDialog(s)
						tester.Frame()
						clickNamed(t, tester, locales.FwPort())
						tester.Type("8443")
					}
					shot(item.name + "-dialog")
					item.state.dialog = nil
					s.server.disk.mode, s.server.security.view = 0, 0
				}
			}
			assertOpsNoAction(t, fake)
		})
	}
}
