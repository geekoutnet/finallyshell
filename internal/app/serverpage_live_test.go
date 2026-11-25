package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func livePanel(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session) {
	t.Helper()
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE not set")
	}
	a, platform, session := connectTestServer(t)
	t.Cleanup(a.CloseAll)
	tester := ui.NewTester(a.View, 1440, 900)
	platform.tt = tester
	return a, platform, tester, session
}

func livePanelRun(t *testing.T, platform *fakePlatform, run systemRunner, script, text string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var out, detail string
	var err error
	ready := make(chan struct{})
	go func() {
		if text != "" {
			out, detail, err = run(ctx, script, strings.NewReader(text), nil)
		} else {
			out, detail, err = run(ctx, script, nil, nil)
		}
		close(ready)
	}()
	platform.until(t, "remote operation", func() bool {
		select {
		case <-ready:
			return true
		default:
			return false
		}
	})
	if err != nil {
		t.Fatalf("remote operation: %v: %s", err, detail)
	}
	return out
}

func TestSystemLivePanels(t *testing.T) {
	a, platform, tester, session := livePanel(t)
	a.openPanelTab(session, tabSystem)
	services := &session.server.services
	platform.until(t, "service list", func() bool {
		return slices.ContainsFunc(services.shown(), func(unit monitor.ServiceUnit) bool { return unit.Name == "ssh.service" })
	})
	services.filter = "ssh.service"
	tester.Frame()
	if rows := services.shown(); len(rows) != 1 || rows[0].Name != "ssh.service" {
		t.Errorf("service filter returned %d rows", len(rows))
	}
	a.serviceDetails(session, "ssh.service")
	platform.until(t, "service details", func() bool { return services.dialog != nil && !services.dialog.busy })
	systemClick(t, tester, locales.Close())

	root := a.systemRoot(session)
	path := fmt.Sprintf("/var/log/fslive-panel-%d.log", time.Now().UnixNano())
	defer livePanelRun(t, platform, root, "rm -f "+path, "")
	livePanelRun(t, platform, root, "umask 077; cat >"+path, "fslive-before\n")
	a.openServerPage(session, serverSecLogs)
	page := &session.server.logs
	tester.Frame()
	page.sourceMode, page.path = 2, path
	tester.Frame()
	systemClick(t, tester, locales.LogvFollow())
	platform.until(t, "protected log via elevation", func() bool { return strings.Contains(page.logText(false), "fslive-before") })
	systemClick(t, tester, locales.LogvPause())
	livePanelRun(t, platform, rootWithSeparatePrivilege(a, session), "cat >>"+path, "fslive-after\n")
	platform.until(t, "new line from the remote stream", func() bool { return len(page.lines) >= 2 })
	if strings.Contains(page.logText(false), "fslive-after") {
		t.Error("pause did not freeze log view")
	}
	systemClick(t, tester, locales.LogvResume())
	systemType(t, tester, locales.LogvFilter(), "fslive-after")
	if text := page.logText(false); !strings.Contains(text, "fslive-after") || strings.Contains(text, "fslive-before") {
		t.Error("log filter did not apply")
	}
	systemClick(t, tester, locales.LogvOptions())
	chooseDockerAction(t, tester, locales.LogvRegex())
	if !page.regex {
		t.Error("regular expression option did not apply")
	}
	a.openServerPage(session, serverSecCron)
	tester.Frame()
	cron := &session.server.cron
	platform.until(t, "cron source ready", func() bool { return cron.usable })
	cron.selected = 1
	a.refreshServerCron(session)
	platform.until(t, "root crontab", func() bool { return !cron.loading })
	if !cron.usable || cron.failure != "" || page.poll.run != nil {
		t.Errorf("root crontab or log cancellation: %q, stream=%v", cron.failure, page.poll.run != nil)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if out := livePanelRun(t, platform, rootWithSeparatePrivilege(a, session), "ps -C tail -o args= || :", ""); !strings.Contains(out, path) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote log follower survived leaving the tab")
		}
	}
	for _, section := range []struct {
		id    int
		ready func() bool
	}{
		{serverSecDisk, func() bool { return session.server.disk.loaded && !session.server.disk.reading }},
		{serverSecUpdates, func() bool { return session.server.updates.loaded && !session.server.updates.reading }},
		{serverSecFirewall, func() bool { return session.server.firewall.loaded && !session.server.firewall.reading }},
		{serverSecSites, func() bool { return session.server.sites.loaded && !session.server.sites.reading }},
	} {
		a.openServerPage(session, section.id)
		tester.Frame()
		platform.until(t, "System section", section.ready)
	}
	if len(session.server.disk.mounts) == 0 || session.server.updates.manager != "apt" || !session.server.firewall.ufw.Known || session.server.firewall.protected.actual != 22 || !session.server.sites.status.Installed || session.server.sites.readErr != "" {
		t.Errorf("System readback: mounts=%d, package manager=%q, firewall=%q, actual SSH port=%d, websites error=%q", len(session.server.disk.mounts), session.server.updates.manager, session.server.firewall.backend, session.server.firewall.protected.actual, session.server.sites.readErr)
	}
}

func rootWithSeparatePrivilege(a *App, session *Session) systemRunner {
	if systemUser(session) == "root" {
		return systemReader(session)
	}
	priv := monitor.NewPrivilege(systemContext(session), monitor.SSHPrivilegeRunner(session.client), monitor.PrivilegeCredentials{
		Login: func(context.Context) []byte { return []byte(a.passwords[session.Host.ID]) },
	})
	return func(ctx context.Context, script string, input io.Reader, _ func(string)) (string, string, error) {
		return priv.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input})
	}
}

func TestSystemLiveServiceActions(t *testing.T) {
	a, platform, tester, session := livePanel(t)
	name := fmt.Sprintf("fslive-ui-%d.service", time.Now().UnixNano())
	file := "/run/systemd/system/" + name
	override, err := monitor.ServiceOverridePath(name)
	if err != nil {
		t.Fatal(err)
	}
	root := a.systemRoot(session)
	defer livePanelRun(t, platform, root, "systemctl stop "+name+"; rm -f "+file+" "+override+"; rmdir "+strings.TrimSuffix(override, "/override.conf")+" 2>/dev/null; systemctl daemon-reload", "")
	livePanelRun(t, platform, root, "set -e; cat >"+file+"; systemctl daemon-reload", "[Service]\nExecStart=/bin/sleep infinity\n")
	a.openPanelTab(session, tabSystem)
	page := &session.server.services
	page.filter = name
	platform.until(t, "synthetic unit shown", func() bool { return len(page.shown()) == 1 })
	a.serviceOverride(session, name)
	platform.until(t, "override editor ready", func() bool { return page.dialog != nil && page.dialog.body != nil })
	systemType(t, tester, locales.SvcEdit(), "[Service]\nEnvironment=FSLIVE_UI=yes\n")
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "override write complete", func() bool { return page.dialog.done })
	if page.dialog.failure != "" {
		t.Fatal(page.dialog.failure)
	}
	systemClick(t, tester, locales.Close())
	for _, step := range []struct{ action, state string }{{"start", "active"}, {"stop", "inactive"}} {
		rows := page.shown()
		if len(rows) != 1 || rows[0].Name != name {
			t.Fatal("unrelated service selected")
		}
		action := slices.IndexFunc(serviceActions, func(action serviceAction) bool { return action.id == step.action })
		a.confirmService(session, rows, serviceActions[action])
		tester.Frame()
		systemClick(t, tester, locales.SvcConfirm())
		platform.until(t, "action and readback", func() bool {
			return page.dialog.done && (page.dialog.failure != "" || len(page.shown()) == 1 && page.shown()[0].Active == step.state)
		})
		if page.dialog.failure != "" {
			t.Fatal(page.dialog.failure)
		}
		systemClick(t, tester, locales.Close())
	}
	if out := livePanelRun(t, platform, systemReader(session), monitor.SystemCommand("systemctl", "show", "-p", "Environment", "--value", name), ""); strings.TrimSpace(out) != "FSLIVE_UI=yes" {
		t.Fatalf("systemd did not load the override: %q", out)
	}
}

func TestSystemLiveProcessSignals(t *testing.T) {
	a, platform, tester, session := livePanel(t)
	a.bottomTab = tabProcesses
	for _, root := range []bool{false, true} {
		if root && systemUser(session) == "root" {
			break
		}
		run := systemReader(session)
		if root {
			run = rootWithSeparatePrivilege(a, session)
		}
		marker := fmt.Sprintf("fslive-proc-%d", time.Now().UnixNano())
		pidText := strings.TrimSpace(livePanelRun(t, platform, run, `exec bash -c 'exec -a "$1" /bin/sleep 120' bash `+marker+` </dev/null >/dev/null 2>&1 & echo $!`, ""))
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid < 2 {
			t.Fatalf("unexpected fixture PID: %q", pidText)
		}
		defer livePanelRun(t, platform, run, "if [ -r /proc/"+pidText+"/cmdline ] && grep -zFxq "+marker+" /proc/"+pidText+"/cmdline; then kill -KILL "+pidText+"; fi", "")
		var row monitor.Process
		platform.until(t, "fixture process listed", func() bool {
			for _, candidate := range session.procs.list {
				if candidate.PID == pid && strings.Contains(candidate.Args, marker) {
					row = candidate
					return candidate.Start != 0
				}
			}
			return false
		})
		session.procs.filter = marker
		tester.Frame()
		if shown := session.procs.shown(); len(shown) != 1 || shown[0].PID != pid {
			t.Fatalf("unexpected process filter result: %d rows", len(shown))
		}
		a.askSignal(session, row, "TERM", true)
		tester.Frame()
		systemClick(t, tester, locales.ProcKillButton())
		platform.until(t, "initial signal result", func() bool { return a.processes.dlg == nil || !a.processes.dlg.busy })
		if root {
			if dlg := a.processes.dlg; dlg == nil || dlg.err == nil {
				t.Fatal("expected a refusal before explicit elevation")
			}
			secret := a.passwords[session.Host.ID]
			delete(a.passwords, session.Host.ID)
			systemClick(t, tester, locales.PrivRun())
			kind := privilegeWindow(session)
			platform.until(t, "explicit sudo prompt", func() bool { return a.windowOpen(kind) })
			prompt := platform.show(t, kind)
			typeInto(t, prompt, locales.PrivPassword(), secret)
			clickButton(t, prompt, locales.PrivRun())
			a.passwords[session.Host.ID] = secret
			platform.until(t, "privileged signal result", func() bool { return a.processes.dlg == nil || !a.processes.dlg.busy })
		}
		if dlg := a.processes.dlg; dlg != nil {
			t.Fatalf("signal did not finish: %v", dlg.err)
		}
		if running, err := monitor.ProcessRunning(t.Context(), session.client, pid, row.Start); err != nil || running {
			t.Fatalf("process after signal: running=%v, error=%v", running, err)
		}
	}
}
