package app

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func TestTunnelManagerBatchAndFilter(t *testing.T) {
	application, platform := testApp(t)
	application.tmgr.dial = func(context.Context, *config.Host, sshx.Options) (*ssh.Client, error) {
		return nil, sshx.ErrBulkHostKey
	}
	t.Cleanup(application.CloseAll)
	for _, name := range []string{"Alpha", "Beta"} {
		application.hosts.Add(&config.Host{ID: name, Name: name, Addr: "127.0.0.1", Auth: config.AuthPassword, Tunnels: []config.Tunnel{
			{ID: "web", Name: "Web " + name, Kind: config.TunnelLocal, ListenPort: 8443, TargetHost: "127.0.0.1", TargetPort: 443},
		}})
	}
	view := &application.tmgr.view
	tester := ui.NewTester(func(c *ui.Context) { useTheme(c); application.tunnelManagerWindow(c) }, 1280, 820)
	platform.tt = tester
	tester.Frame()
	wantTexts(t, tester, "manager", "Web Alpha", "Web Beta")
	view.query = "alpha"
	tester.Frame()
	if !tester.HasText("Web Alpha") || tester.HasText("Web Beta") {
		t.Fatalf("host/name filtering: %v", tester.Texts())
	}
	view.query = ""
	view.selection.Add(tunnelKey{"Alpha", "web"})
	view.selection.Add(tunnelKey{"Beta", "web"})
	tester.Frame()
	clickNamed(t, tester, locales.TunnelStart())
	wantTexts(t, tester, "start confirmation", "Alpha · Web Alpha · 127.0.0.1:8443", "Beta · Web Beta · 127.0.0.1:8443")
	clickNamed(t, tester, locales.Cancel())
	if len(application.tmgr.runs) != 0 {
		t.Fatal("cancel started tunnels")
	}
	view.selection.Clear()
	tester.Frame()
	tester.RightClickAt(rowCenter(t, tester, "Web Alpha"))
	if err := tester.ChooseMenuItem(locales.TunnelStop()); err == nil {
		t.Fatal("stopped tunnel offered Stop")
	}
	if err := tester.ChooseMenuItem(locales.TmgrCopyAddress()); err == nil {
		t.Fatal("stopped tunnel offered Copy listen address")
	}
	tester.CloseMenu()
	view.selection.Add(tunnelKey{"Alpha", "web"})
	view.selection.Add(tunnelKey{"Beta", "web"})
	tester.Frame()
	clickNamed(t, tester, locales.TunnelStart())
	clickNamed(t, tester, locales.TmgrConfirm())
	platform.until(t, "both errors", func() bool {
		return errors.Is(application.tmgr.hosts["Alpha"].err, sshx.ErrBulkHostKey) && errors.Is(application.tmgr.hosts["Beta"].err, sshx.ErrBulkHostKey)
	})
	view.state = tunnelViewError
	view.selection.Clear()
	tester.Frame()
	wantTexts(t, tester, "error", locales.TmgrError(), "Web Alpha", "Web Beta")
	rowMenu(t, tester, "Web Alpha", locales.TmgrLog())
	wantTexts(t, tester, "connection log", locales.TmgrUntrusted())
	clickNamed(t, tester, locales.Close())
	clickNamed(t, tester, locales.TmgrStopAll())
	clickNamed(t, tester, locales.TmgrConfirm())
	if len(application.tmgr.runs) != 0 || len(application.tmgr.hosts) != 0 {
		t.Fatal("stop all left a background tunnel")
	}
}

func TestTunnelManagerSettingsAndEdit(t *testing.T) {
	application, platform := testApp(t)
	t.Cleanup(application.CloseAll)
	host := &config.Host{ID: "alpha", Name: "Alpha", Addr: "127.0.0.1", User: "test", Tunnels: []config.Tunnel{
		{ID: "web", Name: "Web", Kind: config.TunnelLocal, ListenPort: 8443, TargetHost: "127.0.0.1", TargetPort: 443},
	}}
	application.hosts.Add(host)
	application.openTunnelManager(host.ID)
	tester := platform.show(t, windowTunnelManager)
	clickNamed(t, tester, locales.TmgrAutostart())
	wantTexts(t, tester, "autostart confirmation", locales.TmgrAutostartOff())
	clickNamed(t, tester, locales.Cancel())
	if !application.settings.TunnelManager.Autostart() {
		t.Fatal("cancel changed the startup setting")
	}
	clickNamed(t, tester, locales.TmgrAutostart())
	clickNamed(t, tester, locales.TmgrConfirm())
	if application.settings.TunnelManager.Autostart() {
		t.Fatal("confirmation did not change the startup setting")
	}
	rowMenu(t, tester, "Web", locales.TmgrEnableBackground())
	clickNamed(t, tester, locales.Cancel())
	if host.Tunnels[0].Background {
		t.Fatal("cancel changed the background flag")
	}
	rowMenu(t, tester, "Web", locales.TmgrEnableBackground())
	clickNamed(t, tester, locales.TmgrConfirm())
	if !host.Tunnels[0].Background || len(application.tmgr.runs) != 0 {
		t.Fatal("background autostart did not save without starting a listener")
	}
	application.tmgr.view.selection.Add(tunnelKey{host.ID, "web"})
	tester.Frame()
	clickNamed(t, tester, locales.Edit())
	if editor := application.editor; editor == nil || editor.nav.Page != editorTunnels || editor.tunnels.editing != 0 || !editor.tunnels.dialog {
		t.Fatal("edit did not open the selected tunnel")
	}
}

func TestTunnelManagerScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(lang+"/"+appearance, func(t *testing.T) {
				application, platform := testApp(t)
				platform.SetLocale(lang)
				now := time.Now()
				application.tmgr.now = func() time.Time { return now }
				tunnelManagerDemo(application, now)
				view := func(c *ui.Context) { useTheme(c); application.tunnelManagerWindow(c) }
				shot := func(tester *ui.Tester, scene string) {
					tester.SetDark(appearance == "dark")
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "tmgr-"+scene+"-"+lang+"-"+appearance+".png")
				}
				full := ui.NewTester(view, 1280, 820)
				shot(full, "full")
				shot(ui.NewTester(view, 680, 470), "compact")
				rows := application.tunnelManagerRows()
				application.askTunnelManager(tunnelActionStop, rows[:2])
				shot(full, "confirmation")
				application.tmgr.view.ask = nil
				application.tmgr.view.log = &rows[2]
				shot(full, "log")
				application.tmgr.view.log = nil
				session := demoSession(application, "Studio gateway", "gateway.example.invalid")
				application.active = len(application.sessions) - 1
				session.tunnels.runs = []*tunnelRun{{spec: config.Tunnel{Kind: config.TunnelLocal, ListenPort: 33890, TargetHost: "127.0.0.1", TargetPort: 3389}}}
				application.openBottomTab(tabTunnels)
				main := ui.NewTester(application.View, 1280, 820)
				shot(main, "panel-short")
				application.setPanelOnRight(true)
				shot(main, "panel-right")
				application.openPanelTab(session, tabTunnels)
				shot(main, "panel-full")
				application.OpenTunnelManager()
				application.editTunnelFromManager("studio", "desktop")
				editor := platform.windows[WindowEditor]
				shot(ui.NewTester(editor.view, editor.spec.Width, editor.spec.Height), "editor")
				application.CloseAll()
			})
		}
	}
}

type tunnelManagerDemoHandle struct {
	addr  net.Addr
	stats sshx.TunnelStats
}

func (handle tunnelManagerDemoHandle) Addr() net.Addr          { return handle.addr }
func (handle tunnelManagerDemoHandle) Stats() sshx.TunnelStats { return handle.stats }
func (tunnelManagerDemoHandle) Done() <-chan struct{}          { return nil }
func (tunnelManagerDemoHandle) Conns() []sshx.TunnelConn       { return nil }
func (tunnelManagerDemoHandle) Err() error                     { return nil }
func (tunnelManagerDemoHandle) Stop()                          {}

func tunnelManagerDemo(application *App, now time.Time) {
	application.hosts.Add(&config.Host{ID: "dev", Name: "Development", Addr: "dev.example.invalid", Tunnels: []config.Tunnel{
		{ID: "db", Name: "PostgreSQL", Kind: config.TunnelLocal, ListenPort: 15432, TargetHost: "db.internal", TargetPort: 5432, Background: true},
		{ID: "egress", Name: "Staging egress", Kind: config.TunnelDynamic, ListenPort: 1080, Background: true},
	}})
	application.hosts.Add(&config.Host{ID: "prod", Name: "Production", Addr: "prod.example.invalid", Tunnels: []config.Tunnel{
		{ID: "preview", Name: "Webhook preview", Kind: config.TunnelRemote, ListenPort: 9000, TargetHost: "127.0.0.1", TargetPort: 8000, Background: true},
	}})
	application.hosts.Add(&config.Host{ID: "studio", Name: "Studio", Addr: "studio.example.invalid", Tunnels: []config.Tunnel{
		{ID: "desktop", Name: "Remote desktop", Kind: config.TunnelLocal, ListenPort: 33389, TargetHost: "desktop.lan", TargetPort: 3389},
	}})
	application.tmgr.hosts = map[string]*tunnelBackgroundHost{
		"prod": {err: sshx.ErrBulkHostKey},
	}
	application.tmgr.runs = map[tunnelKey]*tunnelBackground{
		{"dev", "db"}: {since: now.Add(-34 * time.Minute), handle: tunnelManagerDemoHandle{
			addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 15432}, stats: sshx.TunnelStats{Active: 2, Total: 15, Up: 1_500_000, Down: 9_800_000},
		}, traffic: tunnelTraffic{samples: []float64{0, 20, 34, 14, 11, 28, 40, 30, 17, 2, 1, 3, 6, 49, 41, 23, 17, 12, 19, 34, 22, 18, 16, 6}}},
		{"dev", "egress"}:   {attempt: 3, retryAt: now.Add(8 * time.Second), err: &sshx.TunnelError{Addr: "127.0.0.1:1080", InUse: true, Err: errors.New("address in use")}},
		{"prod", "preview"}: {},
	}
}
