package app

import (
	"slices"
	"testing"

	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// desktopApp is a connected session with its desktop open, on a 1280×820
// window.
func desktopTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session) {
	t.Helper()
	a, p := testApp(t)
	s := demoSession(a, "web-1", "10.0.0.11")
	a.active = 0
	a.openDesktop(s)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	tt.Frame()
	return a, p, tt, s
}

func TestDesktopRow(t *testing.T) {
	a, _, tt, s := desktopTestApp(t)
	d := a.desktopRowOf(s)
	if d == nil || a.current() != d || a.panelRowOf(s) != nil {
		t.Fatalf("desktop row = %v, current = %v", d, a.current())
	}
	if got := rowNames(a); !slices.Equal(got, []string{"web-1", "web-1 | " + locales.DesktopTab()}) {
		t.Errorf("rows = %q", got)
	}
	// A panel row as well: the desktop stays its own row.
	a.openPanelTab(s, tabFiles)
	if a.panelRowOf(s) == nil || a.desktopRowOf(s) != d || len(a.sessions) != 3 {
		t.Errorf("rows = %q", rowNames(a))
	}
	a.active = slices.Index(a.sessions, d)
	tt.Frame()
	if _, ok := tt.Find(locales.DesktopAppThisPC()); !ok {
		t.Error("no This PC icon on the desktop")
	}
	w := a.desktopOpen(s, appTrash, nil)
	if w == nil || a.desktopOpen(s, appTrash, nil) != w || len(d.desktop.windows) != 1 {
		t.Fatalf("single app opened twice: %v", d.desktop.windows)
	}
	a.desktopOpen(s, appFiles, nil)
	a.desktopOpen(s, appFiles, nil)
	if n := len(d.desktop.windowsOf(appFiles)); n != 2 {
		t.Errorf("%d file windows", n)
	}
	tt.Frame()
	saveShot(t, tt, "desktop-skeleton.png")
	// Closing the session closes its rows.
	a.closeSession(s)
	if len(a.sessions) != 0 {
		t.Errorf("rows left: %q", rowNames(a))
	}
}

// TestDesktopModeSwitch switches a server between its terminal and its
// desktop from its tab's menu, the desktop's own menu and the shortcut,
// each naming the mode it goes to.
func TestDesktopModeSwitch(t *testing.T) {
	a, _, tt, s := desktopTestApp(t)
	d := a.desktopRowOf(s)
	a.sidebarHidden = true // the server's name is on its tab alone
	tt.Frame()
	menu := func(x, y float32, item string) {
		t.Helper()
		tt.RightClickAt(x, y)
		tt.Frame()
		if err := tt.ChooseMenuItem(item); err != nil {
			t.Fatalf("%q in %q: %v", item, tt.Menu(), err)
		}
		tt.Frame()
	}
	tab, ok := tt.Find("web-1")
	if !ok {
		t.Fatalf("no tab: %q", tt.Texts())
	}
	tx, ty := tab.X+tab.W/2, tab.Y+tab.H/2
	menu(tx, ty, locales.SwitchToTerminalMode())
	if a.current() != s || a.inDesktopMode(s) {
		t.Fatalf("the tab's menu went to %q", a.current().label())
	}
	menu(tx, ty, locales.SwitchToDesktopMode())
	if a.current() != d || !a.inDesktopMode(s) {
		t.Fatalf("the tab's menu went to %q", a.current().label())
	}
	// The desktop's menu, on its wallpaper.
	menu(640, 560, locales.SwitchToTerminalMode())
	if a.current() != s {
		t.Fatalf("the desktop's menu went to %q", a.current().label())
	}
	saveShot(t, tt, "desktop-mode-terminal.png")
	// The shortcut, both ways.
	k := a.shortcut(scOpenDesktop)
	tt.Key(k.Mods, k.Key)
	tt.Frame()
	if a.current() != d {
		t.Fatalf("the shortcut went to %q, not the desktop", a.current().label())
	}
	tt.Key(k.Mods, k.Key)
	tt.Frame()
	if a.current() != s {
		t.Fatalf("the shortcut went to %q, not the terminal", a.current().label())
	}
}

// TestTabModeMark marks each server's tab with what it shows: its
// terminal, its desktop or a panel row.
func TestTabModeMark(t *testing.T) {
	a, _, tt, web := desktopTestApp(t) // web-1, on its desktop
	db := demoSession(a, "db-1", "10.0.0.12")
	cache := demoSession(a, "cache-1", "10.0.0.13")
	a.openPanelTab(cache, tabFiles)
	tt.Frame() // shown, so its tab goes back to it
	a.active = slices.Index(a.sessions, db)
	tt.Frame()
	for _, tc := range []struct {
		s    *Session
		icon string
	}{{web, desktopRowIcon}, {db, "terminal"}, {cache, "folder"}} {
		if got := tabModeIcon(tc.s, a.tabRow(tc.s)); got != tc.icon {
			t.Errorf("%s's tab: %q, want %q", tc.s.label(), got, tc.icon)
		}
	}
	saveShot(t, tt, "session-tabs-modes.png")
}
