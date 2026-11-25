package app

import (
	"net"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestTunnelCredentialStore(t *testing.T) {
	a, p := testApp(t)
	h := &config.Host{ID: "secret-host", Addr: "127.0.0.1", User: "tester"}
	a.hosts.Add(h)
	tt := openTunnelsPage(t, a, p, h)
	te := &a.editor.tunnels
	te.open(-1)
	te.form.kind, te.form.proxyUser, te.form.proxyPassword = config.TunnelRemoteHTTP, "tester", "fixture-tunnel-proxy"
	te.form.upstreamKind, te.form.upstreamAddr, te.form.upstreamUser, te.form.upstreamPassword = config.ProxyHTTP, "127.0.0.1:8080", "upstream", "fixture-tunnel-upstream"
	te.saveDialog()
	if te.dialog || len(te.list) != 1 {
		t.Fatal("valid proxy form was rejected")
	}
	spec := te.list[0]
	pk, uk := tunnelSecretKey(h.ID, spec.ID, false), tunnelSecretKey(h.ID, spec.ID, true)
	if pk == "" || uk == "" || p.secrets()[pk] != "" || p.secrets()[uk] != "" {
		t.Fatal("credentials saved before host confirmation, or ID missing")
	}
	saved := savedHosts(a)
	tt.Frame()
	clickButton(t, tt, locales.Ok())
	p.until(t, "tunnel credentials stored", func() bool { return p.secrets()[pk] != "" && p.secrets()[uk] != "" })
	if strings.Contains(string(*saved), "fixture-tunnel-") || !strings.Contains(string(*saved), "http://upstream@127.0.0.1:8080") || h.Tunnels[0].ID != spec.ID {
		t.Fatal("unexpected persisted tunnel definition")
	}
	srv := sshtest.Start(t)
	s := newSession(h)
	s.client = srv.Dial(t)
	a.startTunnels(s, h.Tunnels)
	p.until(t, "authenticated listener starts", func() bool { return tunnelsSettled(s, 1) })
	if s.tunnels.runs[0].fwd == nil {
		t.Fatal(s.tunnels.runs[0].err)
	}
	s.stopTunnels()
	s.client.Close()
	tt = openTunnelsPage(t, a, p, h)
	a.editor.tunnels.row = 0
	tt.Frame()
	clickButton(t, tt, locales.TunnelRemove())
	clickButton(t, tt, locales.TunnelRemove())
	clickButton(t, tt, locales.Ok())
	p.until(t, "credentials deleted", func() bool { return p.secrets()[pk] == "" && p.secrets()[uk] == "" })
}

func TestTunnelProfessionalViews(t *testing.T) {
	srv := sshtest.Start(t)
	echo := echoServer(t)
	for _, language := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			variant := language + "-" + appearance
			t.Run(variant, func(t *testing.T) {
				a, p := testApp(t)
				p.SetLocale(language)
				s := demoSession(a, "tunnel-lab", srv.Host)
				s.client = srv.Dial(t)
				defer s.client.Close()
				defer s.stopTunnels()
				s.Host.Tunnels = []config.Tunnel{
					{Kind: config.TunnelLocal, Name: "Development database", TargetHost: "127.0.0.1", TargetPort: echo, AllowFrom: []string{"127.0.0.1"}, MaxConns: 8},
					{Kind: config.TunnelRemote, Name: "Preview server", TargetHost: "127.0.0.1", TargetPort: echo},
					{Kind: config.TunnelDynamic, Name: "Browser", Mixed: true},
					{Kind: config.TunnelHTTP, Name: "Build network", ProxyUser: "builder", Disabled: true},
					{Kind: config.TunnelRemoteDynamic, Name: "Workstation network", Disabled: true},
					{Kind: config.TunnelRemoteHTTP, Name: "Package downloads", Upstream: "http://127.0.0.1:8080", Disabled: true},
				}
				s.Host.EnsureTunnelIDs()
				a.hosts.Add(s.Host)
				a.startTunnels(s, s.Host.Tunnels)
				p.until(t, "listeners", func() bool { return tunnelsSettled(s, 6) && s.listening() == 3 })
				run := s.tunnels.runs[0]
				echoes(t, run.fwd.Addr().(*net.TCPAddr).Port, 0, strings.Repeat("data", 2048))
				p.until(t, "traffic and connection log", func() bool { return len(run.fwd.Conns()) == 1 })
				newTester := func(view func(*ui.Context), width, height int) *ui.Tester {
					viewer := ui.NewTesterWith(view, width, height, ui.TesterOptions{Dark: dark})
					viewer.SetLocale(language)
					viewer.Frame()
					return viewer
				}
				a.openPanelTab(s, tabTunnels)
				tt := newTester(a.View, 1280, 820)
				wantTexts(t, tt, "tunnel status", run.spec.Name, run.address(), locales.TunnelListening())
				saveShot(t, tt, "tengine-panel-full-"+variant+".png")
				rowMenu(t, tt, run.spec.Name+" "+run.address(), locales.TunCopyAddress())
				if tt.Clipboard() != run.address() {
					t.Fatal("wrong clipboard address")
				}
				socks := s.tunnels.runs[2]
				rowMenu(t, tt, socks.spec.Name+" "+socks.address(), locales.TunCopyCommand())
				if want := "export all_proxy='socks5h://" + socks.address() + "'"; tt.Clipboard() != want {
					t.Fatal("wrong proxy command")
				}
				rowMenu(t, tt, run.spec.Name+" "+run.address(), locales.TunConnectionLog())
				wantTexts(t, tt, "connection log", locales.TunLogClient(), locales.TunLogOK(), run.spec.TargetAddr())
				saveShot(t, tt, "tengine-log-"+variant+".png")
				clickButton(t, tt, locales.Copy())
				if !strings.Contains(tt.Clipboard(), run.spec.TargetAddr()) {
					t.Fatal("log copy omitted target")
				}
				typeInto(t, tt, locales.TunLogFilter(), "no match")
				wantTexts(t, tt, "filtered log", locales.TunNoLog())
				clickButton(t, tt, locales.Close())
				rowMenu(t, tt, run.spec.Name+" "+run.address(), locales.TunnelStop())
				if run.fwd == nil || s.tunnels.confirm == nil {
					t.Fatal("stop did not wait for confirmation")
				}
				saveShot(t, tt, "tengine-stop-confirm-"+variant+".png")
				clickButton(t, tt, locales.Cancel())
				if run.fwd == nil || s.tunnels.confirm != nil {
					t.Fatal("cancel stopped tunnel")
				}
				a.active, a.bottomTab, a.terminalPct = 0, tabTunnels, 64
				tt.Frame()
				saveShot(t, tt, "tengine-panel-short-"+variant+".png")
				a.bottomSide, a.rightWidth = config.PanelRight, 460
				tt.Frame()
				wantTexts(t, tt, "narrow tunnel status", run.spec.Name, run.address(), locales.TunnelListening())
				saveShot(t, tt, "tengine-panel-narrow-"+variant+".png")
				a.editTunnels(s.Host)
				editor := newTester(func(c *ui.Context) { useTheme(c); a.editorView(c) }, 680, 700)
				saveShot(t, editor, "tengine-editor-table-"+variant+".png")
				for idx, spec := range s.Host.Tunnels {
					a.editor.tunnels.open(idx)
					editor.Frame()
					wantTexts(t, editor, "editor kind", locales.TunnelChange(), kindHint(spec.Kind))
					saveShot(t, editor, "tengine-editor-"+string(spec.Kind)+"-"+variant+".png")
				}
				a.editor.tunnels.open(5)
				a.editor.tunnels.form.public, a.editor.tunnels.form.listenHost = true, "0.0.0.0"
				editor.Frame()
				wantTexts(t, editor, "public warning", locales.TunOpenProxy(), locales.TunGatewayPorts(), locales.TunPublicConfirm())
				if fieldValue(t, editor, locales.TunUser()) != "" || fieldValue(t, editor, locales.TunUpstreamAddress()) != "127.0.0.1:8080" {
					t.Fatal("public fields changed another field's value")
				}
				saveShot(t, editor, "tengine-editor-public-"+variant+".png")
				a.editor.tunnels.open(0)
				a.editor.tunnels.form.socketMode, a.editor.tunnels.form.socket = true, "/var/run/docker.sock"
				editor.Frame()
				saveShot(t, editor, "tengine-editor-socket-"+variant+".png")
				if language == "en" && !dark {
					a.active = 1
					tt.Frame()
					rowMenu(t, tt, run.spec.Name+" "+run.address(), locales.TunnelStop())
					fwd := run.fwd
					clickButton(t, tt, locales.TunnelStop())
					if run.fwd != nil || !portFree(fwd.Addr().(*net.TCPAddr).Port) {
						t.Fatal("confirmed stop did not release its listener")
					}
				}
			})
		}
	}
}
