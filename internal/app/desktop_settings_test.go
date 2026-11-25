package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const desktopSetDemoInfo = `---FINALLYSHELL:os-release---
ID=ubuntu
---FINALLYSHELL:virtualization---
kvm
---FINALLYSHELL:zone---
Asia/Shanghai
---FINALLYSHELL:addr---
2: eth0 inet 10.0.0.11/24 scope global eth0
2: eth0 inet6 fd00::11/64 scope global eth0
---FINALLYSHELL:mounts---
/dev/vda1 40894464 17196646 22020096 46% /
/dev/vdb1 83886080 4194304 79691776 5% /data
---FINALLYSHELL:end---`

const desktopSetDemoDNS = `---FINALLYSHELL:manager---
MODE=plain
NM=no
BACKUP=yes
---FINALLYSHELL:file---
nameserver 1.1.1.1
nameserver 1.0.0.1
search internal.example
options edns0
---FINALLYSHELL:end---`

const desktopSetDemoTime = `---FINALLYSHELL:clock---
1795571622
+0800
---FINALLYSHELL:timedatectl---
Timezone=Asia/Shanghai
LocalRTC=no
CanNTP=yes
NTP=yes
NTPSynchronized=yes
RTCTimeUSec=Wed 2026-11-25 02:21:00 UTC
---FINALLYSHELL:sync---
chronyc
---FINALLYSHELL:end---`

type desktopSetFake struct {
	reads    chan string
	root     chan monitor.PrivilegeRequest
	probes   atomic.Int32
	response atomic.Int32
}

func (fake *desktopSetFake) read(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
	fake.reads <- script
	switch script {
	case monitor.SysConfInfoCommand:
		return desktopSetDemoInfo, "", nil
	case monitor.SysConfDNSCommand:
		return desktopSetDemoDNS, "", nil
	case monitor.SysConfDNSProbe:
		return "192.0.2.80 example.com\n", "", nil
	case monitor.SysConfTimeCommand:
		return desktopSetDemoTime, "", nil
	case monitor.SysConfZonesCommand:
		return "America/Chicago\nAmerica/New_York\nAsia/Shanghai\nEtc/UTC\nEurope/Paris\n", "", nil
	case monitor.SysConfLastCommand:
		return "operator pts/0 192.0.2.35 Wed Nov 25 09:30:00 2026 still logged in\n", "", nil
	case monitor.SysConfPublicIPCommand:
		return "203.0.113.37", "", nil
	default:
		return "", "unexpected read", errors.New("unexpected read")
	}
}

func (fake *desktopSetFake) privilege(ctx context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
	if request.Probe {
		fake.probes.Add(1)
		return monitor.PrivilegeResult{Stdout: "sudo-n"}, nil
	}
	fake.root <- request
	if request.Action == monitor.SysConfLastbCommand {
		return monitor.PrivilegeResult{Started: true, Stdout: "unknown ssh:notty 198.51.100.24 Wed Nov 25 09:23:00 2026 - 09:23  (00:00)\n"}, nil
	}
	switch fake.response.Load() {
	case 1:
		return monitor.PrivilegeResult{Started: true, Stderr: "simulated remote failure"}, errors.New("exit status 1")
	case 2:
		request.ReportProgress("fake operation is running\n")
		<-ctx.Done()
		return monitor.PrivilegeResult{Started: true}, ctx.Err()
	}
	request.ReportProgress("fake operation complete\n")
	return monitor.PrivilegeResult{Started: true}, nil
}

func desktopSettingsFixture(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *desktopSetFake) {
	t.Helper()
	a, platform, tester, session := desktopTestApp(t)
	fake := &desktopSetFake{reads: make(chan string, 128), root: make(chan monitor.PrivilegeRequest, 32)}
	session.server.services.run = fake.read
	session.overview.memory.privilegeRunner = fake.privilege
	a.readSysInfo = func(context.Context, *ssh.Client) (monitor.SysInfo, error) {
		return monitor.SysInfo{Hostname: "atlas-node-07", OS: "Ubuntu 24.04.2 LTS", Arch: "x86_64", Kernel: "6.8.0-60-generic", CPUModel: "AMD EPYC 7B13", Cores: 4, CPUs: 8, MemTotal: 8 << 30, Uptime: 83*24*time.Hour + 22*time.Hour}, nil
	}
	return a, platform, tester, session, fake
}

func desktopSetOpenPage(a *App, tester *ui.Tester, session *Session, name string) (*desktopWindow, *desktopSettingsState) {
	window := a.desktopOpen(session, appSettings, name)
	tester.Frame()
	return window, window.state.(*desktopSettingsState)
}

func TestDesktopSettingsPages(t *testing.T) {
	a, platform, tester, session, fake := desktopSettingsFixture(t)
	window, state := desktopSetOpenPage(a, tester, session, "info")
	platform.until(t, "system information", func() bool { return state.info.load.loaded })
	wantTexts(t, tester, "system information", "atlas-node-07", "AMD EPYC 7B13", locales.DesktopSetOverview())
	systemClick(t, tester, locales.DesktopSetCopyAll())
	if clipboard := tester.Clipboard(); !strings.Contains(clipboard, "/dev/vda1") || strings.Contains(clipboard, "10.0.0.11") {
		t.Fatalf("clipboard: %q", clipboard)
	}
	if len(fake.reads) != 1 || <-fake.reads != monitor.SysConfInfoCommand {
		t.Fatal("opening system information unexpectedly looked up a public IP")
	}
	systemClick(t, tester, locales.DesktopSetDetectIP())
	platform.until(t, "detected IP", func() bool { return state.info.detect.loaded })
	wantTexts(t, tester, "IP information", "203.0.113.37")
	if <-fake.reads != monitor.SysConfPublicIPCommand {
		t.Fatal("wrong public IP command")
	}
	systemClick(t, tester, locales.RenameConnection())
	tester.Type("-renamed")
	systemClick(t, tester, locales.Save())
	if !strings.Contains(session.Host.Name, "-renamed") {
		t.Fatalf("alias was not saved: %q", session.Host.Name)
	}
	for _, item := range desktopSettingsPages {
		reopen, state := desktopSetOpenPage(a, tester, session, item.id)
		if reopen != window || state.nav.Page != item.id || !tester.HasText(item.title()) {
			t.Fatalf("cannot show %s in the single window", item.id)
		}
	}
	systemClick(t, tester, locales.DesktopSetOpen(locales.DesktopSetAppLock()))
	if a.optionsNav.Page != pageSecurity || !a.windowOpen(WindowOptions) {
		t.Fatal("app lock link did not open the Options page")
	}
	a.closeWindow(WindowOptions)
	systemClick(t, tester, locales.DesktopSetOpen(locales.KeysTitle()))
	if !a.windowOpen(keysWindow) {
		t.Fatal("SSH keys link did not open the key manager")
	}
	a.closeWindow(keysWindow)
	systemClick(t, tester, locales.DesktopSetOpen(locales.DesktopSetServerSecurity()))
	if desktopAppByID("srv-security") != nil {
		if len(a.desktopOf(session).windowsOf("srv-security")) != 1 {
			t.Fatal("server security link did not open its desktop app")
		}
	} else if row := a.panelRowOf(session); row == nil || row.panelTab != tabSystem || session.server.section != serverSecSecurity || a.desktopRowOf(session) == nil {
		t.Fatal("server security link did not open the System panel and keep the desktop")
	}
}

func TestDesktopSettingsLog(t *testing.T) {
	a, platform, tester, session, fake := desktopSettingsFixture(t)
	a.desktopLog(session, appFiles, "Created reports", nil)
	a.desktopLog(session, appSettings, "Applied DNS", errors.New("lookup failed"))
	_, state := desktopSetOpenPage(a, tester, session, "log")
	wantTexts(t, tester, "operation log", "Created reports", "lookup failed")
	state.log.app, state.log.search = appSettings, "FAILED"
	tester.Frame()
	if tester.HasText("Created reports") || !tester.HasText("Applied DNS") {
		t.Fatal("log filters did not apply")
	}
	path := filepath.Join(t.TempDir(), "operations.log")
	platform.pickSave(path, nil)
	systemClick(t, tester, locales.DesktopSetExport())
	platform.until(t, "export", func() bool { return !state.log.exporting })
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "Applied DNS") || strings.Contains(string(contents), "Created reports") {
		t.Fatalf("export: %q, %v", contents, err)
	}
	systemClick(t, tester, locales.DesktopSetClear())
	systemClick(t, tester, locales.Cancel())
	if len(a.desktopOf(session).log) != 2 {
		t.Fatal("cancel cleared the log")
	}
	systemClick(t, tester, locales.DesktopSetClear())
	systemClick(t, tester, locales.SvcConfirm())
	if len(a.desktopOf(session).log) != 0 {
		t.Fatal("confirm did not clear the log")
	}
	state.log.tab = 1
	tester.Frame()
	platform.until(t, "successful logins", func() bool { return state.log.load.loaded })
	wantTexts(t, tester, "successful logins", "operator", "192.0.2.35")
	if fake.probes.Load() != 0 {
		t.Fatal("reading successful logins unexpectedly escalated privileges")
	}
	systemClick(t, tester, locales.DesktopSetLoadFailed())
	platform.until(t, "failed logins", func() bool { return state.log.failedLoad })
	wantTexts(t, tester, "failed logins", "198.51.100.24", locales.DesktopSetFailedLogin())
	if request := <-fake.root; request.Action != monitor.SysConfLastbCommand || !strings.Contains(request.Command, "sudo -n -u root") {
		t.Fatalf("failed login command: %q / %q", request.Action, request.Command)
	}
}

func TestDesktopSettingsChanges(t *testing.T) {
	a, platform, tester, session, fake := desktopSettingsFixture(t)
	_, state := desktopSetOpenPage(a, tester, session, "dns")
	platform.until(t, "DNS state", func() bool { return state.dns.load.loaded })
	wantTexts(t, tester, "DNS", locales.DesktopSetDNSStatus(), "/etc/resolv.conf", "edns0")
	state.dns.servers = "9.9.9.9, 149.112.112.112"
	tester.Frame()
	check := func(label, target string, change monitor.SysConfChange, verify bool) {
		t.Helper()
		systemClick(t, tester, label)
		if state.dialog == nil || !strings.Contains(state.dialog.target, target) {
			t.Fatalf("confirmation does not name %q", target)
		}
		systemClick(t, tester, locales.Cancel())
		if state.dialog != nil || len(fake.root) != 0 {
			t.Fatal("cancel ran the change")
		}
		systemClick(t, tester, label)
		systemClick(t, tester, locales.SvcConfirm())
		platform.until(t, "change result", func() bool { return state.dialog != nil && state.dialog.done })
		request := <-fake.root
		if request.Action != change.Script || !strings.Contains(request.Command, "sudo -n -u root") {
			t.Fatalf("unexpected privileged script: %q", request.Action)
		}
		if verify && !strings.Contains(state.dialog.output, "192.0.2.80 example.com") {
			t.Fatalf("missing DNS verification: %q", state.dialog.output)
		}
		systemClick(t, tester, locales.Close())
	}
	values, _ := monitor.ValidateSysConfDNS(state.dns.servers, state.dns.search)
	dnsApply, _ := monitor.BuildSysConfDNS(state.dns.data, values, false)
	check(locales.DesktopSetDNSApply(), monitor.SysConfResolvPath, dnsApply, true)
	platform.until(t, "DNS refresh", func() bool { return state.dns.load.cancel == nil })
	dnsRestore, _ := monitor.BuildSysConfDNS(state.dns.data, monitor.DNSValues{}, true)
	check(locales.DesktopSetDNSRestore(), monitor.SysConfResolvBackup, dnsRestore, true)
	_, state = desktopSetOpenPage(a, tester, session, "time")
	platform.until(t, "server clock", func() bool { return state.clock.load.loaded })
	wantTexts(t, tester, "server clock", "Asia/Shanghai", locales.DesktopSetNTP())
	state.clock.zone = "Etc/UTC"
	tester.Frame()
	zone, _ := monitor.BuildSysConfZone("Etc/UTC", true)
	check(locales.DesktopSetSetZone(), "'set-timezone' 'Etc/UTC'", zone, false)
	platform.until(t, "time refresh", func() bool { return state.clock.load.cancel == nil })
	check(locales.DesktopSetDisableNTP(), "'set-ntp' 'false'", monitor.BuildSysConfNTP(false), false)
	platform.until(t, "NTP refresh", func() bool { return state.clock.load.cancel == nil })
	fake.response.Store(1)
	systemClick(t, tester, locales.DesktopSetSyncNow())
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "failed sync", func() bool { return state.dialog != nil && state.dialog.done })
	if request := <-fake.root; request.Action != "'chronyc' 'makestep'\n" || !strings.Contains(state.dialog.failure, "simulated remote failure") {
		t.Fatalf("failed sync: %q / %q", request.Action, state.dialog.failure)
	}
	log := a.desktopOf(session).log
	if len(log) != 5 || !strings.Contains(log[4].err, "simulated remote failure") || fake.probes.Load() != 1 {
		t.Fatalf("operation log: %+v; probes: %d", log, fake.probes.Load())
	}
	systemClick(t, tester, locales.Close())
	platform.until(t, "error refresh", func() bool { return state.clock.load.cancel == nil })
	fake.response.Store(2)
	systemClick(t, tester, locales.DesktopSetSyncNow())
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "running sync", func() bool { return strings.Contains(state.dialog.output, "fake operation is running") })
	systemClick(t, tester, locales.Cancel())
	platform.until(t, "cancelled running sync", func() bool { return state.dialog.done })
	if state.dialog.failure != locales.SvcCancelled() {
		t.Fatalf("running cancellation: %q", state.dialog.failure)
	}
}

func TestDesktopSettingsScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	a, platform, tester, session, _ := desktopSettingsFixture(t)
	window, state := desktopSetOpenPage(a, tester, session, "info")
	platform.until(t, "system overview", func() bool { return state.info.load.loaded })
	state.info.showIPs = true
	a.desktopLog(session, appFiles, "/var/www/releases", nil)
	a.desktopLog(session, appSettings, "example.com", errors.New("SERVFAIL"))
	_, state = desktopSetOpenPage(a, tester, session, "dns")
	platform.until(t, "DNS overview", func() bool { return state.dns.load.loaded })
	_, state = desktopSetOpenPage(a, tester, session, "time")
	platform.until(t, "live time", func() bool { return state.clock.load.loaded })
	_, state = desktopSetOpenPage(a, tester, session, "log")
	state.log.tab = 1
	tester.Frame()
	platform.until(t, "login history", func() bool { return state.log.load.loaded })
	state.log.tab = 0
	wide, tall := window.w, window.h
	for _, language := range []string{"en", "zh-Hans"} {
		platform.SetLocale(language)
		for _, appearance := range []string{"light", "dark"} {
			tester.SetDark(appearance == "dark")
			for _, size := range []string{"wide", "small"} {
				window.w, window.h = wide, tall
				if size == "small" {
					window.w, window.h = window.app.minSize.W, window.app.minSize.H+desktopTitleHeight
				}
				for _, page := range []string{"info", "personalize", "log", "logins", "dns", "time", "security"} {
					state.log.tab = 0
					id := page
					if page == "logins" {
						id, state.log.tab = "log", 1
					}
					desktopSetOpenPage(a, tester, session, id)
					tester.Frame()
					saveShot(t, tester, "desktop-settings-"+page+"-"+language+"-"+appearance+"-"+size+".png")
				}
			}
		}
	}
}

func TestDesktopSettingsCompactScroll(t *testing.T) {
	a, platform, tester, session, _ := desktopSettingsFixture(t)
	window, state := desktopSetOpenPage(a, tester, session, "dns")
	platform.until(t, "DNS state", func() bool { return state.dns.load.loaded })
	window.w, window.h = window.app.minSize.W, window.app.minSize.H+desktopTitleHeight
	tester.Frame()
	tester.Frame()
	status, ok := tester.Find(locales.DesktopSetDNSStatus())
	if !ok {
		t.Fatal("DNS status is not at the start of the compact page")
	}
	tester.Scroll(status.X+20, status.Y+24, 0, 480)
	tester.Frame()
	_, ok = tester.AccessByRole(ui.RoleButton, locales.DesktopSetDNSTest())
	if !ok {
		t.Fatal("DNS test button is unreachable in the compact window")
	}
	systemClick(t, tester, locales.DesktopSetDNSTest())
	platform.until(t, "DNS probe", func() bool { return state.dns.probe.loaded })
	if !strings.Contains(state.dns.result, "192.0.2.80") {
		t.Fatalf("DNS probe result: %q", state.dns.result)
	}
}
