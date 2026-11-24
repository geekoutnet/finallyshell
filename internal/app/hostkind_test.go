package app

import (
	"slices"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestKindLook(t *testing.T) {
	ssh, rdp := kindLook(&config.Host{}), kindLook(&config.Host{Kind: config.KindRDP})
	if ssh.icon != "square-terminal" || rdp.icon != "monitor" {
		t.Errorf("icons %q, %q", ssh.icon, rdp.icon)
	}
	if ssh.label != locales.KindSSH() || rdp.label != locales.KindRDPShort() || ssh.color == rdp.color {
		t.Errorf("looks %+v, %+v", ssh, rdp)
	}
}

// A search finds a host by its kind's name, as well as by its fields.
func TestHostMatchesKind(t *testing.T) {
	ssh := &config.Host{Name: "web-1", Addr: "203.0.113.10"}
	rdp := &config.Host{Name: "win-1", Addr: "10.0.0.5", Kind: config.KindRDP}
	for _, c := range []struct {
		query    string
		ssh, rdp bool
	}{
		{"rdp", false, true},
		{"远程桌面", false, true},
		{"ssh", true, false},
		{"rdp win", false, true},
		{"rdp web", false, false},
		{"1", true, true},
	} {
		words := strings.Fields(c.query)
		if hostMatches(ssh, words) != c.ssh || hostMatches(rdp, words) != c.rdp {
			t.Errorf("%q: ssh %v, rdp %v", c.query, hostMatches(ssh, words), hostMatches(rdp, words))
		}
	}
}

// The start page names each host's kind, and its search finds the
// Remote Desktop hosts by "rdp".
func TestStartPageHostKinds(t *testing.T) {
	a, _, tt, _ := startApp(t)
	rdp := rdpHost(a)
	client := fakeRDPClient(a)
	a.passwords[rdp.ID] = "saved-password"
	tt.Frame()
	checkHostKindIcon(t, tt, "db-1", locales.KindSSH())
	checkHostKindIcon(t, tt, "win-1", locales.KindRDPShort())
	for _, s := range []string{"win-1", "web-1", locales.KindRDPShort(), locales.KindSSH()} {
		if !tt.HasText(s) {
			t.Errorf("missing %q; texts %q", s, tt.Texts())
		}
	}
	tt.Type("rdp")
	tt.Frame()
	if !tt.HasText("win-1") || tt.HasText("web-1") {
		t.Errorf("search for rdp; texts %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if requests := client.requests(); len(requests) != 1 || requests[0].Host.ID != rdp.ID || !a.current().isNewTab() {
		t.Errorf("opening RDP from search: %d requests, current tab is new: %v", len(requests), a.current().isNewTab())
	}
}

// The manager has a Type column, and its type filter shows one kind,
// with the search.
func TestManagerHostKinds(t *testing.T) {
	a, _, tt := openTestManager(t)
	rdp := rdpHost(a)
	rdp.Folder = "lab"
	tt.SetScale(1)
	tt.Frame()
	checkTreeKindIcon(t, tt, a.hosts.Host("pg"), 44)
	checkTreeKindIcon(t, tt, rdp, 32)
	for _, s := range []string{locales.FieldHostKind(), locales.KindRDPShort(), locales.KindSSH(), locales.AllTypes()} {
		if !tt.HasText(s) {
			t.Errorf("missing %q; texts %q", s, tt.Texts())
		}
	}
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.RemoteDesktop())
	if got := shown(a); a.manager.kind != kindsRDP || !slices.Equal(got, []string{"Lab", "  win-1"}) {
		t.Errorf("RDP only: %q", got)
	}
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.KindSSH())
	if got := shown(a); a.manager.kind != kindsSSH || slices.Contains(got, "win-1") || !slices.Contains(got, "    pg-main") {
		t.Errorf("SSH only: %q", got)
	}
	chooseKindOption(t, tt, locales.FolderFilter(), "Lab")
	if got := shown(a); !slices.Equal(got, []string{"raspberry"}) {
		t.Errorf("SSH in Lab: %q", got)
	}
	clickKindControl(t, tt, ui.RoleTextField, locales.SearchHosts())
	tt.Type("rdp")
	if got := shown(a); len(got) != 0 || !tt.HasText(locales.NoMatchingHosts()) {
		t.Errorf("SSH and rdp: %q", got)
	}
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.AllTypes())
	if got := shown(a); !slices.Equal(got, []string{"win-1"}) {
		t.Errorf("rdp: %q", got)
	}
}

func clickKindControl(t *testing.T, tt *ui.Tester, role ui.Role, label string) {
	t.Helper()
	control, ok := tt.AccessByRole(role, label)
	if !ok {
		t.Fatalf("no %q control with role %v", label, role)
	}
	bounds := control.Bounds
	tt.ClickAt(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
	tt.Frame()
}

func chooseKindOption(t *testing.T, tt *ui.Tester, label, option string) {
	t.Helper()
	clickKindControl(t, tt, ui.RolePopUpButton, label)
	clickKindControl(t, tt, ui.RoleOption, option)
}

func checkTreeKindIcon(t *testing.T, tt *ui.Tester, host *config.Host, offset int) {
	t.Helper()
	row, ok := tt.Find(host.Title())
	if !ok {
		t.Fatalf("no row for %q", host.Title())
	}
	image := tt.Image()
	color := kindLook(host).color
	// Anywhere in the row, the icon or the kind's pill: fonts, and so where
	// the icon is drawn, differ between systems.
	_ = offset
	left := int(row.X)
	top := int(row.Y + row.H/2 - 8)
	for posY := top; posY < top+16; posY++ {
		for posX := left; posX < int(row.X+row.W); posX++ {
			pixel := image.RGBAAt(posX, posY)
			if pixel.R == color.R && pixel.G == color.G && pixel.B == color.B {
				return
			}
		}
	}
	t.Errorf("no %s icon of %q at (%d, %d)", kindLook(host).label, host.Title(), left, top)
}

func checkHostKindIcon(t *testing.T, tt *ui.Tester, host, label string) {
	t.Helper()
	text, found := tt.Find(host)
	icon, ok := tt.AccessByRole(ui.RoleImage, label)
	iconY := icon.Bounds.Y + icon.Bounds.H/2
	if !found || !ok || icon.Bounds.W <= 0 || iconY < text.Y || iconY > text.Y+text.H || icon.Bounds.X > text.X+text.W {
		t.Errorf("%s: expected %s icon by name at %+v, got %+v", host, label, text, icon.Bounds)
	}
}

// The Connections page marks the Remote Desktop hosts, and its own type
// filter shows one kind.
func TestConnTreeHostKinds(t *testing.T) {
	a, p := testApp(t)
	managerHosts(a)
	rdp := rdpHost(a)
	a.restoreLeftPanel()
	a.setLeftPage(leftConnections)
	// Tall enough for every row whole: fonts, and so row heights, differ
	// between systems, and a row cut by the window's edge shows no icon.
	tt := ui.NewTester(a.View, 1280, 1400)
	p.tt = tt
	tt.Frame()
	checkTreeKindIcon(t, tt, a.hosts.Host("pg"), 53)
	checkTreeKindIcon(t, tt, rdp, 24)
	if !tt.HasText("win-1") || !tt.HasText("web-1") || !tt.HasText(locales.KindRDPShort()) {
		t.Fatalf("texts %q", tt.Texts())
	}
	clickKindControl(t, tt, ui.RoleRadio, locales.KindSSH())
	if got := connShown(a); a.connTree.kind != kindsSSH || slices.Contains(got, "win-1") || !slices.Contains(got, "  web-1") {
		t.Errorf("SSH only: %q", got)
	}
	clickKindControl(t, tt, ui.RoleRadio, locales.KindRDPShort())
	if got := connShown(a); a.connTree.kind != kindsRDP || !slices.Equal(got, []string{"win-1"}) {
		t.Errorf("RDP only: %q", got)
	}
	if a.manager.kind != kindsAll {
		t.Error("the manager's filter changed too")
	}
	a.setLeftPage(leftSessions)
	tt.Frame()
	a.setLeftPage(leftConnections)
	tt.Frame()
	if got := connShown(a); !slices.Equal(got, []string{"win-1"}) {
		t.Errorf("filter after switching pages: %q", got)
	}
	clickKindControl(t, tt, ui.RoleRadio, locales.AllTypes())
	clickKindControl(t, tt, ui.RoleTextField, locales.SearchConnections())
	tt.Type("rdp")
	if got := connShown(a); !slices.Equal(got, []string{"win-1"}) {
		t.Errorf("search for rdp: %q", got)
	}
}

// Screenshots of the start page, the manager and the Connections page
// with SSH and Remote Desktop hosts, light and dark, for SHOT_DIR.
func TestHostKindShots(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			a, p, tt, _ := startApp(t)
			p.SetLocale(lang)
			tt.SetLocale(lang)
			win := rdpHost(a)
			win.LastConnectedAt = a.hosts.Recent(1)[0].LastConnectedAt
			a.hosts.Add(&config.Host{ID: "r2", Name: "win-build-server-with-a-long-name", Addr: "10.0.0.6", User: "builder", Kind: config.KindRDP})
			tt.Frame()
			both := func(tt *ui.Tester, name string) {
				saveShot(t, tt, name+"-"+lang+".png")
				tt.SetDark(true)
				tt.Frame()
				saveShot(t, tt, name+"-dark-"+lang+".png")
				tt.SetDark(false)
				tt.Frame()
			}
			both(tt, "kinds-start")

			managerHosts(a)
			a.openManager()
			m := p.show(t, WindowManager)
			m.SetLocale(lang)
			m.Frame()
			both(m, "kinds-manager")

			m.SetSize(managerSpec.MinWidth, managerSpec.Height)
			m.Frame()
			both(m, "kinds-manager-min")
			a.closeWindow(WindowManager)

			a.setLeftPage(leftConnections)
			tt.SetSize(1280, 1050)
			a.connTree.tree.Open.Add(managerItem{folder: "lab"})
			win.Folder = "lab"
			tt.Frame()
			both(tt, "kinds-conntree")

			// At the sidebar's least width, the filter's choices and the
			// pills keep inside it.
			a.sideWidth = sidebarLimits.first
			tt.Frame()
			for _, s := range kindFilterLabels(true) {
				box, ok := tt.Find(s)
				if !ok || box.W <= 0 || box.X+box.W > sidebarLimits.first {
					t.Errorf("%q at %+v in a sidebar of %v", s, box, sidebarLimits.first)
				}
			}
			both(tt, "kinds-conntree-min")
		})
	}
}
