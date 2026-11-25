package app

import (
	"net"
	"reflect"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func TestTunnelRecipeDrafts(t *testing.T) {
	for _, recipe := range tunnelRecipes {
		t.Run(recipe.id, func(t *testing.T) {
			existing := []config.Tunnel{{Kind: recipe.kind, ListenPort: recipe.listen}}
			probes := 0
			draft := recipe.draft(existing, func(string) bool { probes++; return probes > 1 }, []string{"192.168.2.0/24"})
			want := recipe.listen + 2
			if recipe.kind.IsRemote() {
				want--
				if probes != 0 {
					t.Fatal("remote port probed locally")
				}
			}
			if draft.Kind != recipe.kind || draft.ListenPort != want || draft.TargetPort != recipe.target || draft.TargetSocket != recipe.socket || draft.ListenHost != recipe.bind || (len(draft.AllowFrom) != 0) != recipe.lan {
				t.Fatalf("%+v, want port %d", draft, want)
			}
			if draft.Mixed != (recipe.id == "mixed" || recipe.id == "lan-proxy") || draft.SystemProxy || draft.ProxyUser != "" || draft.ServerRelay {
				t.Fatalf("unexpected defaults: %+v", draft)
			}
		})
	}
}

func TestTunnelRecipeListenProbe(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	recipe := tunnelRecipes[0]
	recipe.listen = listener.Addr().(*net.TCPAddr).Port
	if recipeCanListen(listener.Addr().String()) {
		t.Fatal("an occupied loopback port was reported free")
	}
	tunnel := recipe.draft(nil, recipeCanListen, nil)
	if tunnel.ListenPort == recipe.listen || tunnel.ListenPort == 0 && recipe.listen < 65408 {
		t.Fatalf("did not find the next port: %+v", tunnel)
	}
}

func TestTunnelRecipeDiscovery(t *testing.T) {
	got := recipeLAN([]recipeInterface{
		{net.FlagUp, []string{"192.168.2.9/24", "fd00:abcd::2/64", "192.168.2.10/24", "8.8.8.8/24", "169.254.1.2/16", "10.0.0.1/32", "bad"}},
		{net.FlagUp | net.FlagLoopback, []string{"127.0.0.1/8"}},
		{net.FlagUp | net.FlagPointToPoint, []string{"10.8.1.1/24"}},
		{net.FlagBroadcast, []string{"192.168.3.4/24"}},
	})
	if !reflect.DeepEqual(got, []string{"192.168.2.0/24", "fd00:abcd::/64"}) {
		t.Fatal(got)
	}
	got = recipeUpstreams(func(address string) bool { return address == "127.0.0.1:7890" || address == "127.0.0.1:1080" })
	if !reflect.DeepEqual(got, []string{"http://127.0.0.1:7890", "socks5://127.0.0.1:1080"}) {
		t.Fatal(got)
	}
	for _, invalid := range []string{"http://user@localhost:1", "https://localhost:1", "http://localhost:1/path", "http://localhost", "socks5://localhost:0"} {
		if recipeValidUpstream(invalid) {
			t.Errorf("accepted %q", invalid)
		}
	}
}

func TestTunnelRecipeProxyCommands(t *testing.T) {
	exports, apt, docker := recipeProxyCommands(config.Tunnel{Kind: config.TunnelRemoteHTTP, ListenPort: 3128})
	if exports != "export http_proxy='http://127.0.0.1:3128' https_proxy='http://127.0.0.1:3128' all_proxy='http://127.0.0.1:3128'" ||
		apt != "Acquire::http::Proxy \"http://127.0.0.1:3128\";\nAcquire::https::Proxy \"http://127.0.0.1:3128\";" ||
		docker != "[Service]\nEnvironment=\"HTTP_PROXY=http://127.0.0.1:3128\"\nEnvironment=\"HTTPS_PROXY=http://127.0.0.1:3128\"" {
		t.Fatalf("%q %q %q", exports, apt, docker)
	}
	exports, apt, docker = recipeProxyCommands(config.Tunnel{Kind: config.TunnelRemoteDynamic, ListenHost: "::1", ListenPort: 1080})
	if exports != "export all_proxy='socks5h://[::1]:1080'" || apt != "" || docker != "" {
		t.Fatalf("%q %q %q", exports, apt, docker)
	}
}

func TestTunnelRecipeSendShellExports(t *testing.T) {
	a, platform, session, connection, _ := barTerm(t)
	platform.SetLocale("en")
	listener := &config.Tunnel{Kind: config.TunnelRemoteHTTP, ListenPort: 3128}
	view := &tunnelRecipeView{editor: &connEditor{id: session.Host.ID}, send: true}
	server := sshtest.Start(t)
	session.client = server.Dial(t)
	tester := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		a.recipeSendDialog(c, view, *listener)
	}, 450, 240)
	clickNamed(t, tester, locales.Cancel())
	if input := connection.sent(t, session.term()); view.send || input != "" {
		t.Fatalf("cancel sent %q", input)
	}
	view.send = true
	clickNamed(t, tester, locales.TrecSend())
	command, _, _ := recipeProxyCommands(*listener)
	if input := connection.sent(t, session.term()); view.send || input != command {
		t.Fatalf("confirm sent %q; want %q without Enter", input, command)
	}
}

func TestTunnelRecipeConfirmAndImport(t *testing.T) {
	a, platform := testApp(t)
	platform.SetLocale("en")
	host := &config.Host{ID: "test-host", Name: "dev", Addr: "203.0.113.40", User: "alice"}
	a.hosts.Add(host)
	editor := openTunnelsPage(t, a, platform, host)
	clickNamed(t, editor, locales.TrecTitle())
	tester := platform.show(t, windowTunnelRecipes)
	clickNamed(t, tester, locales.TrecGroupPublish())
	clickNamed(t, tester, locales.TrecPublishWeb())
	if !tester.HasText(locales.TrecRemoteWarn("0.0.0.0:8080")) {
		t.Fatalf("no exposure warning: %q", tester.Texts())
	}
	_ = tester.Click(locales.TrecAdd())
	if len(a.editor.tunnels.list) != 0 {
		t.Fatal("added before acknowledgement")
	}
	clickNamed(t, tester, locales.TrecAcceptAccess())
	clickNamed(t, tester, locales.Cancel())
	if len(a.editor.tunnels.list) != 0 || len(host.Tunnels) != 0 {
		t.Fatal("cancel edited tunnels")
	}
	clickNamed(t, editor, locales.TrecTitle())
	tester = platform.show(t, windowTunnelRecipes)
	clickNamed(t, tester, locales.TrecImport())
	tester = platform.show(t, windowTunnelImport)
	typeInto(t, tester, locales.TrecSSHText(), "ssh -L broken host")
	clickNamed(t, tester, locales.TrecPreview())
	if !tester.HasText(locales.TrecInvalidSSH()) {
		t.Fatalf("no invalid syntax: %q", tester.Texts())
	}
	typeInto(t, tester, locales.TrecSSHText(), "ssh -N -L 13306:localhost:3306 -D 0.0.0.0:1080 host")
	clickNamed(t, tester, locales.TrecPreview())
	if !tester.HasText(locales.TrecNoAuthWarn()) && !strings.Contains(strings.Join(tester.Texts(), " "), locales.TrecNoAuthWarn()) {
		t.Fatalf("no public proxy warning: %q", tester.Texts())
	}
	tester.Scroll(580, 330, 0, 280)
	clickNamed(t, tester, locales.TrecAcceptAccess())
	clickNamed(t, tester, locales.TrecAdd())
	if len(a.editor.tunnels.list) != 2 || len(host.Tunnels) != 0 || a.editor.tunnels.list[0].ID == "" {
		t.Fatalf("draft %+v, saved %+v", a.editor.tunnels.list, host.Tunnels)
	}
	clickNamed(t, editor, locales.Ok())
	if len(host.Tunnels) != 2 || host.Tunnels[1].ListenHost != "0.0.0.0" {
		t.Fatalf("saved %+v", host.Tunnels)
	}
	a.openBulkPalette([]*config.Host{host})
	palette := platform.show(t, "command-palette")
	clickNamed(t, palette, locales.TrecTitle())
	if !a.windowOpen(windowTunnelRecipes) || a.editor.id != host.ID || a.editor.nav.Page != editorTunnels {
		t.Fatal("palette did not open the host's recipe gallery")
	}
}

func TestTunnelRecipeScreens(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(lang+"/"+appearance, func(t *testing.T) {
				a, platform := testApp(t)
				platform.SetLocale(lang)
				view := &tunnelRecipeView{group: "0", editor: &connEditor{name: "dev-gateway", addr: "203.0.113.40", port: "22", user: "alice", tunnels: newTunnelEditor(&config.Host{Tunnels: demoTunnels()})},
					canListen: func(string) bool { return true }, detect: func() []string { return []string{"http://127.0.0.1:7890"} }}
				tester := ui.NewTester(func(c *ui.Context) {
					useTheme(c)
					a.tunnelRecipeWindow(c, view)
				}, 1280, 820)
				platform.tt = tester
				tester.SetLocale(lang)
				tester.SetDark(appearance == "dark")
				tester.Frame()
				shot := func(name string) { saveShot(t, tester, "trec-"+name+"-"+lang+"-"+appearance+".png") }
				shot("gallery")
				tester.SetSize(460, 320)
				tester.Frame()
				shot("short-gallery")
				tester.SetSize(770, 720)
				clickNamed(t, tester, locales.TrecGroupReverse())
				clickNamed(t, tester, locales.TrecReverseHTTP())
				platform.until(t, "upstream suggestion", func() bool { return len(view.upstream) == 1 })
				clickNamed(t, tester, locales.TrecUseProxy("http://127.0.0.1:7890"))
				if view.form.upstream != "http://127.0.0.1:7890" {
					t.Fatal("upstream not selected")
				}
				shot("review")
				tester.SetSize(460, 320)
				tester.Frame()
				shot("short-review")
				tester.SetSize(770, 720)
				clickNamed(t, tester, locales.TrecBack())
				clickNamed(t, tester, locales.TrecImport())
				importer := platform.show(t, windowTunnelImport)
				importer.SetScale(1)
				importer.SetDark(appearance == "dark")
				typeInto(t, importer, locales.TrecSSHText(), "ssh -N -L 13306:localhost:3306 -R 127.0.0.1:3128:localhost:8080 dev-gateway")
				clickNamed(t, importer, locales.TrecPreview())
				if !importer.HasText("localhost:8080") {
					t.Fatal("no preview")
				}
				saveShot(t, importer, "trec-import-"+lang+"-"+appearance+".png")
				importer.SetSize(460, 320)
				importer.Frame()
				saveShot(t, importer, "trec-short-import-"+lang+"-"+appearance+".png")
			})
		}
	}
}
