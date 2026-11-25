package app

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func exposureTestDialog() *tunnelExposure {
	return &tunnelExposure{
		ctx: context.Background(), host: &config.Host{ID: "publish", Name: "Edge gateway", Addr: "gateway.example.com", User: "operator"},
		spec:    config.Tunnel{Kind: config.TunnelRemote, ListenHost: "0.0.0.0", ListenPort: 8080},
		outside: "gateway.example.com", checked: true, checkedPort: 8080,
		checks:     exposeChecks{gateway: monitor.GatewayPorts{Mode: "no"}, tool: "socat", firewall: exposeFirewall{backend: "ufw"}},
		connection: opsConnection{ctx: context.Background(), current: func() bool { return true }},
	}
}

func exposureTestView(a *App, dialog *tunnelExposure, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) { useTheme(c); a.exposureView(c, dialog) }, width, height)
}

func TestTunnelExposeGatewayConfirm(t *testing.T) {
	a, platform := testApp(t)
	dialog := exposureTestDialog()
	commands := make(chan string, 2)
	dialog.connection.root = func(_ context.Context, script string, _ func(string)) (string, string, error) {
		commands <- script
		return "", "configuration rolled back", errors.New("reload rejected")
	}
	tester := exposureTestView(a, dialog, 690, 690)
	tester.Frame()
	clickButton(t, tester, locales.TexpAllowGateway())
	tester.Frame()
	if !tester.HasText(locales.TexpGatewayConfirm(dialog.host.Title())) {
		t.Fatalf("missing confirmation: %q", tester.Texts())
	}
	clickButton(t, tester, locales.Cancel())
	if len(commands) != 0 || dialog.confirm != nil {
		t.Fatal("cancel unexpectedly ran a command")
	}
	clickButton(t, tester, locales.TexpAllowGateway())
	tester.Frame()
	clickButton(t, tester, locales.SvcConfirm())
	platform.until(t, "gateway error", func() bool { return dialog.problem != "" })
	if cmd := <-commands; cmd != monitor.GatewayPortsChangeScript {
		t.Fatalf("wrong script: %s", cmd)
	}
	tester.Frame()
	if !tester.HasText(locales.TexpError("configuration rolled back")) || dialog.needsReconnect {
		t.Fatalf("failure not visible: %q", tester.Texts())
	}
}

func TestTunnelLANConfirm(t *testing.T) {
	a, platform := testApp(t)
	commands := make(chan int, 2)
	dialog := &tunnelLANDialog{windows: true, port: 8443, addresses: []netip.Prefix{netip.MustParsePrefix("192.168.10.42/24")}, add: func(port int) error {
		commands <- port
		return errors.New("permission refused")
	}}
	tester := ui.NewTester(func(c *ui.Context) { useTheme(c); a.tunnelLANView(c, dialog) }, 610, 485)
	tester.Frame()
	clickButton(t, tester, locales.TexpLANFirewallAdd())
	tester.Frame()
	if !tester.HasText(locales.TexpLANFirewallConfirm("8443")) {
		t.Fatalf("missing confirmation: %q", tester.Texts())
	}
	clickButton(t, tester, locales.Cancel())
	if len(commands) != 0 || dialog.confirm != nil {
		t.Fatal("cancel unexpectedly ran a command")
	}
	clickButton(t, tester, locales.TexpLANFirewallAdd())
	tester.Frame()
	clickButton(t, tester, locales.SvcConfirm())
	platform.until(t, "firewall error", func() bool { return dialog.errorMsg != "" })
	if port := <-commands; port != 8443 {
		t.Fatalf("wrong port: %d", port)
	}
}

func TestTunnelExposeFirewallConfirm(t *testing.T) {
	a, platform := testApp(t)
	dialog := exposureTestDialog()
	changes := make(chan string, 4)
	dialog.connection.normal = func(_ context.Context, script string, _ func(string)) (string, string, error) {
		switch script {
		case monitor.FirewallSessionScript:
			return "127.0.0.1 42000 127.0.0.1 22\n", "", nil
		case monitor.FirewallDetectScript:
			return "ufw", "", nil
		case monitor.TunnelRelayDetectScript:
			return "socat", "", nil
		}
		return "", "", errors.New("unexpected command")
	}
	dialog.connection.root = func(_ context.Context, script string, _ func(string)) (string, string, error) {
		switch script {
		case monitor.FirewallConnectionsScript:
			return "port 22", "", nil
		case monitor.UFWReadScript:
			return "Status: active\nDefault: deny (incoming)\n", "", nil
		case monitor.GatewayPortsReadScript:
			return "gatewayports no", "", nil
		}
		changes <- script
		return "", "fixture: rule refused", errors.New("exit 1")
	}
	tester := exposureTestView(a, dialog, 690, 620)
	tester.Frame()
	clickButton(t, tester, locales.TexpAllowPort("8080"))
	tester.Frame()
	clickButton(t, tester, locales.Cancel())
	if len(changes) != 0 || dialog.confirm != nil {
		t.Fatal("cancel changed firewall")
	}
	clickButton(t, tester, locales.TexpAllowPort("8080"))
	tester.Frame()
	clickButton(t, tester, locales.SvcConfirm())
	platform.until(t, "firewall result", func() bool { return dialog.problem != "" })
	select {
	case cmd := <-changes:
		if !strings.Contains(cmd, `'ufw' 'allow' 'proto' 'tcp' 'from' 'any' 'to' 'any' 'port' '8080' 'comment' 'Finallyshell t`) {
			t.Errorf("wrong firewall command: %s", cmd)
		}
	default:
		t.Errorf("did not issue the firewall command: %s", dialog.problem)
	}
	if dialog.problem != locales.TexpError("fixture: rule refused") || len(dialog.records) != 0 {
		t.Errorf("error %q, records %v", dialog.problem, dialog.records)
	}
}

func TestTunnelExposeRowMenus(t *testing.T) {
	a, platform := testApp(t)
	host := &config.Host{ID: "test-tunnels", Name: "Tunnel host", Addr: "server.example.com", User: "tester",
		Tunnels: []config.Tunnel{{Kind: config.TunnelRemote, ListenPort: 8080, TargetHost: "localhost", TargetPort: 3000},
			{Kind: config.TunnelLocal, ListenPort: 9090, TargetHost: "localhost", TargetPort: 80}}}
	a.hosts.Add(host)
	session := newSession(host)
	session.status = statusClosed
	session.tunnels.runs = []*tunnelRun{{spec: host.Tunnels[0]}, {spec: host.Tunnels[1]}}
	a.sessions, a.active = []*Session{session}, 0
	tester := ui.NewTester(func(c *ui.Context) { useTheme(c); ui.Column(c).Fill().Children(func() { a.tunnelPanel(c) }) }, 460, 310)
	tester.Frame()
	tester.RightClickAt(rowCenter(t, tester, "127.0.0.1:8080"))
	tester.Frame()
	if err := tester.ChooseMenuItem(locales.TexpMakePublic()); err != nil {
		t.Fatal(err)
	}
	dialog := platform.show(t, windowTunnelExpose)
	dialog.Frame()
	if !dialog.HasText(locales.TexpOffline()) {
		t.Errorf("missing offline explanation: %q", dialog.Texts())
	}
	if !tester.HasText(locales.TunnelStart()) {
		t.Error("short panel hides the tunnel actions")
	}
	a.closeWindow(windowTunnelExpose)
	if a.windowOpen(windowTunnelLAN) {
		t.Fatal("unexpected LAN window")
	}
	a.editTunnels(host)
	editor := platform.show(t, WindowEditor)
	editor.Frame()
	editor.RightClickAt(rowCenter(t, editor, "127.0.0.1:8080"))
	editor.Frame()
	if err := editor.ChooseMenuItem(locales.TexpMakePublic()); err != nil {
		t.Fatal(err)
	}
	if !a.windowOpen(windowTunnelExpose) {
		t.Fatal("editor menu did not open assistant")
	}
}

func TestTunnelExposeRelayOnlyStartsFromAssistant(t *testing.T) {
	a, _ := testApp(t)
	dialog := exposureTestDialog()
	dialog.spec.Background = true
	var saved *config.Tunnel
	dialog.save = func(spec config.Tunnel) error { saved = &spec; return nil }
	tester := exposureTestView(a, dialog, 690, 620)
	tester.Frame()
	clickButton(t, tester, locales.TexpRelayAction())
	tester.Frame()
	if dialog.confirm == nil || dialog.confirm.message != locales.TexpRelayConfirm("Edge gateway", "0.0.0.0:8080") {
		t.Fatalf("wrong relay target in confirmation: %+v", dialog.confirm)
	}
	clickButton(t, tester, locales.Cancel())
	if saved != nil {
		t.Fatal("cancel saved a relay configuration")
	}
	clickButton(t, tester, locales.TexpRelayAction())
	tester.Frame()
	dialog.connection.current = func() bool { return false }
	clickButton(t, tester, locales.SvcConfirm())
	if saved == nil || !saved.ServerRelay || !saved.Disabled || saved.Background {
		t.Fatalf("relay must not start as a normal direct forward: %+v", saved)
	}
}

func TestTunnelExposeScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				a, platform := testApp(t)
				platform.SetLocale(language)
				shot := func(tester *ui.Tester, name string) {
					tester.SetDark(mode == "dark")
					tester.SetLocale(language)
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "texp-"+name+"-"+language+"-"+mode+".png")
				}
				state := exposureTestDialog()
				full := exposureTestView(a, state, 690, 620)
				shot(full, "public")
				clickButton(t, full, locales.In(language).TexpRelayAction())
				shot(full, "relay-confirm")
				lan := &tunnelLANDialog{
					spec: config.Tunnel{Kind: config.TunnelHTTP}, windows: true, port: 8080,
					addresses: []netip.Prefix{netip.MustParsePrefix("192.168.10.42/24"), netip.MustParsePrefix("fd00::a/64")},
					status:    locales.In(language).TexpLANFirewallUnknown(),
				}
				shot(ui.NewTester(func(c *ui.Context) { useTheme(c); a.tunnelLANView(c, lan) }, 610, 435), "lan")
				sshHost := &config.Host{ID: "desktop-jump", Name: "Office jump host", Addr: "office.example.com", User: "staff"}
				a.hosts.Add(sshHost)
				desktop := &config.Host{ID: "desktop", Name: "Editing desktop", Addr: "desktop.internal", Kind: config.KindRDP,
					RDP: &config.RDPConfig{SSHHostID: sshHost.ID, SSHTarget: "desktop.internal:3389"}}
				a.hosts.Add(desktop)
				a.editConnection(desktop)
				a.editor.nav.Page = editorRDPConnection
				shot(platform.show(t, WindowEditor), "rdp-editor")
				a.closeWindow(WindowEditor)
				session := newSession(sshHost)
				session.status = statusConnected
				state.spec.TargetHost, state.spec.TargetPort = "127.0.0.1", 3000
				session.tunnels.runs = []*tunnelRun{{spec: state.spec}, {spec: config.Tunnel{Kind: config.TunnelLocal, ListenHost: "0.0.0.0", ListenPort: 9090, TargetHost: "127.0.0.1", TargetPort: 80}}}
				a.sessions = []*Session{session}
				a.active = 0
				a.openPanelTab(session, tabTunnels)
				shot(ui.NewTester(a.View, 1280, 820), "panel-full")
				a.active = 0
				short := ui.NewTester(func(c *ui.Context) { useTheme(c); ui.Column(c).Fill().Children(func() { a.tunnelPanel(c) }) }, 460, 310)
				shot(short, "panel-short")
				if got := strings.Join(short.Texts(), " "); !strings.Contains(got, "8080") {
					t.Errorf("short panel did not show tunnel: %s", got)
				}
			})
		}
	}
}
