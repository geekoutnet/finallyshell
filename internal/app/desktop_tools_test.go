package app

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

func TestDesktopToolsTerminalShells(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	server := sshtest.Start(t)
	session.client = server.Dial(t)
	session.Host.NoMonitor = true
	first := a.desktopOpen(session, appTerminal, "/var/log/it's here")
	platform.until(t, "first desktop shell", func() bool { return first.state.(*desktopTerminalState).pane != nil })
	second := a.desktopOpen(session, appTerminal, nil)
	platform.until(t, "second desktop shell", func() bool { return server.Shells() == 2 })
	if first == second || server.Logins() != 1 {
		t.Fatalf("windows are not independent channels on the same login")
	}
	platform.until(t, "quoted initial directory", func() bool {
		return strings.Contains(server.ShellInput(0), `cd '--' '/var/log/it'"'"'s here'`)
	})
	platform.until(t, "second terminal view", func() bool {
		return second.state.(*desktopTerminalState).pane != nil
	})
	tester.Frame()
	tester.Type("second-only")
	platform.until(t, "focused shell input", func() bool { return strings.Contains(server.ShellInput(1), "second-only") })
	if strings.Contains(server.ShellInput(0), "second-only") {
		t.Fatal("keyboard input reached the inactive terminal")
	}
	state := second.state.(*desktopTerminalState)
	find := a.shortcut(scFind)
	tester.Key(find.Mods, find.Key)
	tester.Frame()
	if !state.pane.find.open {
		t.Fatal("desktop terminal search shortcut did not open find")
	}
	state.pane.find.open = false
	tester.Frame()
	term := state.pane.term
	second.minimized = true
	tester.Frame()
	select {
	case <-term.Done():
		t.Fatal("minimizing ended the shell")
	default:
	}
	a.desktopFocus(second)
	a.desktopRequestClose(second)
	tester.Frame()
	systemClick(t, tester, locales.Cancel())
	if state.confirming || state.pane == nil || !a.desktopToolsLive(second) {
		t.Fatal("cancel closed the shell")
	}
	a.desktopRequestClose(second)
	tester.Frame()
	systemClick(t, tester, locales.DesktopToolsCloseShell())
	platform.until(t, "closed shell", func() bool { return state.pane == nil })
	select {
	case <-term.Done():
	default:
		t.Fatal("closing the window kept its shell alive")
	}
	tester.Frame()
	tester.Type("first-only")
	platform.until(t, "focus returned to first shell", func() bool { return strings.Contains(server.ShellInput(0), "first-only") })
	server.EndShell(0)
	platform.until(t, "remote channel exit", func() bool { return first.state.(*desktopTerminalState).pane == nil })
	a.desktopTerminalStart(first, first.state.(*desktopTerminalState))
	platform.until(t, "reconnected shell", func() bool { return server.Shells() == 3 && first.state.(*desktopTerminalState).pane != nil })
	if server.Logins() != 1 {
		t.Fatal("reconnect required a new login")
	}
	session.disconnect()
	platform.until(t, "cleanup on disconnect", func() bool { return first.state.(*desktopTerminalState).pane == nil })
}

func TestDesktopToolsServerRegistry(t *testing.T) {
	for _, name := range strings.Fields("docker db sites caddy services logs cron disk updates security firewall processes network tunnels transfers commands overview") {
		app := desktopAppByID(desktopAppID("srv-" + name))
		if app == nil || !app.single || app.pinned || app.category != desktopCatServer {
			t.Errorf("server app missing or misconfigured: %s", name)
		}
	}
}

func TestDesktopToolsTabRouting(t *testing.T) {
	a, _, _, session := desktopTestApp(t)
	row := a.current()
	for tab, app := range desktopToolsTabApps {
		if !a.desktopToolsOpenTab(session, tab, nil) || !a.desktopToolsViewing(session, app) {
			t.Fatalf("tab %d did not route to %s", tab, app)
		}
	}
	for section, app := range desktopToolsSectionApps {
		session.server.section = section
		if !a.desktopToolsOpenTab(session, tabSystem, nil) || !a.desktopToolsViewing(session, app) {
			t.Fatalf("section %d did not route to %s", section, app)
		}
	}
	if a.current() != row || !row.isDesktopRow() || a.desktopToolsOpenTab(session, tabDesktop, nil) {
		t.Fatal("routing changed the desktop row or swallowed an unmapped tab")
	}
	a.backToTerminal(row)
	if a.desktopToolsViewing(session, "srv-docker") || a.desktopToolsOpenTab(session, tabDocker, nil) {
		t.Fatal("routing intercepted the ordinary terminal row")
	}
}

func TestDesktopToolsServiceAction(t *testing.T) {
	a, platform, tester, session, fake := systemTestApp(t)
	a.openDesktop(session)
	window := a.desktopOpen(session, "srv-services", nil)
	window.x, window.y = 110, 22
	tester.Frame()
	wantTexts(t, tester, "service window", "queue.service", "report.service")
	page := &session.server.services
	page.selected.Add("queue.service")
	perform := func() {
		tester.Frame()
		systemClick(t, tester, locales.SvcActions())
		chooseDockerAction(t, tester, locales.SvcReset())
		wantTexts(t, tester, "targeted confirmation", "queue.service", locales.SvcResetNote())
	}
	perform()
	systemClick(t, tester, locales.Cancel())
	if len(fake.root) != 0 {
		t.Fatal("cancel ran a privileged action")
	}
	perform()
	systemClick(t, tester, locales.SvcConfirm())
	if call := systemNext(t, platform, fake.root); call.command != monitor.SystemCommand("env", "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=cat", "systemctl", "reset-failed", "--no-ask-password", "--", "queue.service") {
		t.Fatalf("privileged action = %q", call.command)
	}
	platform.until(t, "finished operation", func() bool { return page.dialog.done })
	if !strings.Contains(page.dialog.failure, "simulated unit failure") {
		t.Fatalf("failure = %q", page.dialog.failure)
	}
}

func TestDesktopToolsTerminalDirectory(t *testing.T) {
	for _, invalid := range []string{"", "../var/log", "C:\\Windows", "/tmp/hello\nexit", "/tmp/\x1b[2J", "/tmp/s\x00"} {
		if got := desktopTerminalCD(invalid); got != "" {
			t.Errorf("accepted %q: %q", invalid, got)
		}
	}
	if got := desktopTerminalCD("/var/log"); got != "cd '--' '/var/log' && clear\r" {
		t.Fatalf("startup command = %q", got)
	}
}

func TestDesktopToolsScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("set SHOT_DIR to capture desktop screenshots")
	}
	a, platform, tester, session := desktopTestApp(t)
	session.desktopToolsRecent = []desktopAppID{"srv-docker", appBrowser, appTerminal, "srv-network"}
	for _, address := range []string{"0.0.0.0:80", "127.0.0.1:8080", "127.0.0.1:8443", "127.0.0.1:22"} {
		session.net.snap.Groups = append(session.net.snap.Groups, monitor.NetGroup{Listen: true, Proto: "tcp", Local: netip.MustParseAddrPort(address)})
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			suffix := appearance + "-" + language
			platform.SetLocale(language)
			tester.SetDark(dark)
			center := a.desktopOpen(session, appAppCenter, nil)
			center.x, center.y = 107, 26
			for _, size := range []string{"normal", "small"} {
				if size == "small" {
					center.w, center.h = center.app.minSize.W, center.app.minSize.H+desktopTitleHeight
				}
				tester.Frame()
				tester.Frame()
				saveShot(t, tester, "desktop-center-"+size+"-"+suffix+".png")
			}
			a.desktopClose(center)
			browser := a.desktopOpen(session, appBrowser, nil)
			browser.x, browser.y = 122, 22
			state := browser.state.(*desktopBrowserState)
			state.discovering, state.stop = false, func() {}
			state.choice, state.run = desktopBrowserChoice{name: "Chromium", path: "test"}, &desktopBrowserRun{addr: "127.0.0.1:48163", ctx: t.Context()}
			state.url, state.exitIP = "http://127.0.0.1:8080/", "203.0.113.12"
			for _, size := range []string{"normal", "small"} {
				if size == "small" {
					browser.w, browser.h = browser.app.minSize.W, browser.app.minSize.H+desktopTitleHeight
				}
				tester.Frame()
				saveShot(t, tester, "desktop-browser-"+size+"-"+suffix+".png")
			}
			a.desktopClose(browser)
		}
	}
	platform.SetLocale("en")
	server := sshtest.Start(t)
	session.client, session.Host.NoMonitor = server.Dial(t), true
	first := a.desktopOpen(session, appTerminal, "/var/log")
	platform.until(t, "first window", func() bool { return first.state.(*desktopTerminalState).pane != nil })
	second := a.desktopOpen(session, appTerminal, "/srv/apps")
	platform.until(t, "second window", func() bool { return second.state.(*desktopTerminalState).pane != nil })
	for index, window := range []*desktopWindow{first, second} {
		window.x, window.y, window.w, window.h = 56+float32(index)*455, 84, 440, 386
		pane := window.state.(*desktopTerminalState).pane
		pane.term.Feed(fmt.Appendf(nil, "\x1b[32mroot@web-1\x1b[0m:\x1b[34m%s\x1b[0m# ls\r\naccess.log  app.log  audit.log\r\n\x1b[32mroot@web-1\x1b[0m# ", window.state.(*desktopTerminalState).dir))
	}
	state := second.state.(*desktopTerminalState)
	for _, language := range []string{"en", "zh-Hans"} {
		platform.SetLocale(language)
		for _, appearance := range []string{"light", "dark"} {
			tester.SetDark(appearance == "dark")
			suffix := appearance + "-" + language
			second.w, second.h = 440, 386
			state.pane.find.open = false
			tester.Frame()
			saveShot(t, tester, "desktop-terminal-two-"+suffix+".png")
			second.w, second.h = second.app.minSize.W, second.app.minSize.H+desktopTitleHeight
			state.pane.find.open = true
			tester.Frame()
			saveShot(t, tester, "desktop-terminal-small-"+suffix+".png")
		}
	}
	session.disconnect()
	platform.until(t, "shells stopped", func() bool { return first.state.(*desktopTerminalState).pane == nil && state.pane == nil })
}

func TestDesktopToolsServerWindows(t *testing.T) {
	a, _, tester, session, _ := systemTestApp(t)
	a.openDesktop(session)
	t.Cleanup(func() {
		for poll := range a.polls {
			a.stopPoll(poll)
		}
	})
	docker := demoDocker()
	a.readDocker = func(context.Context, *ssh.Client) (monitor.Docker, error) { return docker, nil }
	session.overview.docker.d, session.overview.docker.read = docker, true
	known := map[desktopAppID]string{
		"srv-docker": locales.DockerContainers(), "srv-db": locales.DbNew(), "srv-sites": locales.ServerSites(),
		"srv-services": "report.service", "srv-logs": locales.LogvFollow(), "srv-cron": locales.CronAdd(),
		"srv-disk": locales.DuMounts(), "srv-updates": locales.UpdRefresh(), "srv-security": locales.SecFailures(),
		"srv-firewall": locales.FwRules(), "srv-processes": locales.ProcFilter(), "srv-network": locales.NetFilter(),
		"srv-tunnels": locales.TunnelsConfigure(), "srv-transfers": locales.TransfersEmpty(), "srv-commands": locales.QuickSearch(),
		"srv-overview": session.label(),
	}
	for _, tool := range desktopServerTools() {
		t.Run(string(tool.id), func(t *testing.T) {
			window := a.desktopOpen(session, tool.id, nil)
			window.x, window.y = 124, 22
			for _, size := range []string{"normal", "small"} {
				window.w, window.h = 860, 560
				if size == "small" {
					window.w, window.h = window.app.minSize.W, window.app.minSize.H+desktopTitleHeight
				}
				tester.Frame()
				tester.Frame()
				if tester.HasText(locales.DesktopComingSoon()) || !tester.HasText(tool.name()) {
					t.Fatalf("%s did not draw: %v", tool.id, tester.Texts())
				}
				if label := known[tool.id]; label != "" && !tester.HasText(label) {
					if _, ok := tester.Access(label); !ok {
						t.Errorf("%s content missing %q: %v", tool.id, label, tester.Texts())
					}
				}
				saveShot(t, tester, "desktop-"+string(tool.id)+"-"+size+".png")
			}
			a.desktopClose(window)
			tester.Frame()
		})
	}
}
