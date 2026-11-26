package app

import (
	"slices"
	"strconv"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// TestSessionTabs switches servers with their tabs, back to the row of
// each the user was on, closes a disconnected server at once and a
// connected one after asking.
func TestSessionTabs(t *testing.T) {
	a, p := testApp(t)
	web := demoSession(a, "web-1", "10.0.0.11")
	db := demoSession(a, "db-1", "10.0.0.12")
	a.openDesktop(web)
	desktop := a.desktopRowOf(web)
	// With the left column away, the names are on the tabs alone.
	a.sidebarHidden = true
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	tt.Frame()
	if a.current() != desktop {
		t.Fatalf("opening the desktop selected %q", a.current().label())
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("click %q: %v; %q", label, err, tt.Texts())
		}
		tt.Frame()
	}
	saveShot(t, tt, "session-tabs-sidebar-hidden.png")
	// The db-1 tab, then back to web-1's, on its desktop.
	click("db-1")
	if a.current() != db {
		t.Fatalf("db-1's tab selected %q", a.current().label())
	}
	click("web-1")
	if a.current() != desktop {
		t.Fatalf("web-1's tab went to %q, not its desktop", a.current().label())
	}
	// Selecting in the list moves the tabs along.
	a.active = slices.Index(a.sessions, db)
	tt.Frame()
	if got, ok := tt.Access("db-1"); !ok || !got.Selected {
		t.Fatalf("db-1's tab is not the chosen one: %+v", got)
	}
	// A connected server asks before closing; Cancel keeps it.
	a.requestCloseSession(db)
	tt.Frame()
	if a.closeAsk != db || !tt.HasText(locales.CloseSessionTitle("db-1")) {
		t.Fatal("closing a connected server did not ask")
	}
	saveShot(t, tt, "session-tabs-close.png")
	click(locales.Cancel())
	if a.closeAsk != nil || !slices.Contains(a.sessions, db) {
		t.Fatal("cancel closed the server")
	}
	a.requestCloseSession(web)
	tt.Frame()
	click(locales.CloseSessionConfirm())
	if slices.Contains(a.sessions, web) || slices.Contains(a.sessions, desktop) {
		t.Fatalf("closing web-1 left %q", rowNames(a))
	}
	// A disconnected one closes at once.
	db.status = statusClosed
	a.requestCloseSession(db)
	if a.closeAsk != nil || slices.Contains(a.sessions, db) {
		t.Fatal("a disconnected server asked, or stayed")
	}
}

// TestSessionTabMenu closes tabs from a tab's menu: the disconnected ones
// at once, others after asking once, and renames a tab in place.
func TestSessionTabMenu(t *testing.T) {
	a, p := testApp(t)
	one := demoSession(a, "one", "10.0.0.1")
	two := demoSession(a, "two", "10.0.0.2")
	three := demoSession(a, "three", "10.0.0.3")
	four := demoSession(a, "four", "10.0.0.4")
	two.status = statusClosed
	a.sidebarHidden = true // the names are on the tabs alone
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	tt.Frame()
	menu := func(tab, item string) {
		t.Helper()
		if err := tt.RightClick(tab); err != nil {
			t.Fatalf("right-click %q: %v", tab, err)
		}
		tt.Frame()
		if err := tt.ChooseMenuItem(item); err != nil {
			t.Fatalf("%q in %q: %v", item, tt.Menu(), err)
		}
		tt.Frame()
	}
	if err := tt.RightClick("three"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{locales.Reconnect(), locales.DuplicateSession(), locales.CopyConnInfo(), locales.Rename(),
		locales.CloseOtherTabs(), locales.CloseTabsRight(), locales.CloseTabsLeft(), locales.CloseDisconnectedTabs(), locales.CloseAllTabs()} {
		if !slices.Contains(tt.Menu(), want) {
			t.Fatalf("no %q in the tab's menu %q", want, tt.Menu())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	menu("three", locales.CloseDisconnectedTabs())
	if slices.Contains(a.sessions, two) || a.closeAskMany != nil || a.closeAsk != nil {
		t.Fatalf("closing the disconnected tabs: %q, asked %v", rowNames(a), a.closeAskMany)
	}
	// Two connected tabs: one question for both, which Cancel answers.
	menu("three", locales.CloseOtherTabs())
	if len(a.closeAskMany) != 2 || !tt.HasText(locales.CloseTabsTitle(2)) {
		t.Fatalf("closing the other tabs asked %v", a.closeAskMany)
	}
	saveShot(t, tt, "session-tabs-close-others.png")
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.closeAskMany != nil || len(a.sessions) != 3 {
		t.Fatalf("cancel closed tabs: %q", rowNames(a))
	}
	menu("three", locales.CloseTabsRight())
	if a.closeAsk != four {
		t.Fatalf("closing the tab to the right asked about %v", a.closeAsk)
	}
	if err := tt.Click(locales.CloseSessionConfirm()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if slices.Contains(a.sessions, four) || !slices.Contains(a.sessions, one) {
		t.Fatalf("closing the tab to the right left %q", rowNames(a))
	}
	// Renaming from the menu shows the field in the tab.
	menu("three", locales.Rename())
	if !a.rename.inTab {
		t.Fatal("renaming from the tab did not rename in it")
	}
	tt.Frame()
	tt.Type("db")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if three.label() != "db" || a.rename.s != nil {
		t.Fatalf("the tab is named %q", three.label())
	}
}

// TestSidebarHidden folds the left column away and back, with the button
// and the shortcut, and keeps the choice.
func TestSidebarHidden(t *testing.T) {
	a, p := testApp(t)
	demoSession(a, "web-1", "10.0.0.11")
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	tt.Frame()
	if !tt.HasText(locales.SessionsTitle(1)) {
		t.Fatalf("no session list: %q", tt.Texts())
	}
	if err := tt.Click(locales.HideSidebar()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.sidebarHidden || tt.HasText(locales.SessionsTitle(1)) {
		t.Fatal("the left column still shows")
	}
	if !config.LoadSettings().SidebarHidden {
		t.Fatal("the choice was not kept")
	}
	key := a.shortcut(scSidebar)
	tt.Key(key.Mods, key.Key)
	tt.Frame()
	if a.sidebarHidden || !tt.HasText(locales.SessionsTitle(1)) || config.LoadSettings().SidebarHidden {
		t.Fatal("the shortcut did not bring the left column back")
	}
}

// TestBottomTabPerSession keeps each session's tab of the bottom panel as
// the user switches between them; a session showing the first time keeps
// the tab the user was on.
func TestBottomTabPerSession(t *testing.T) {
	a, p := testApp(t)
	demoSession(a, "web-1", "10.0.0.11")
	demoSession(a, "db-1", "10.0.0.12")
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	show := func(i int) {
		a.active = i
		tt.Frame()
	}
	show(0)
	a.openBottomTab(tabOverview)
	tt.Frame()
	show(1)
	if a.bottomTab != tabOverview {
		t.Fatalf("db-1 first shows tab %d, not the one the user was on", a.bottomTab)
	}
	a.openBottomTab(tabFiles)
	tt.Frame()
	show(0)
	if a.bottomTab != tabOverview {
		t.Fatalf("web-1 shows tab %d, want Overview", a.bottomTab)
	}
	show(1)
	if a.bottomTab != tabFiles {
		t.Fatalf("db-1 shows tab %d, want Files", a.bottomTab)
	}
}

// TestSessionTabsOverflow: a few tabs take their widest, many share the
// row down to their narrowest and then scroll, with a menu of them all,
// and the chosen one scrolls into view.
func TestSessionTabsOverflow(t *testing.T) {
	a, p := testApp(t)
	for i := range 3 {
		demoSession(a, "server-"+strconv.Itoa(i), "10.0.0."+strconv.Itoa(10+i))
	}
	a.active = 0
	a.sidebarHidden = true // the names on the tabs alone
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	tt.Frame()
	tt.Frame()
	if r, ok := tt.Find("server-1"); !ok || r.W > sessionTabMax {
		t.Fatalf("a tab of three is %+v", r)
	}
	if tt.HasText(locales.AllTabs()) {
		t.Fatal("three tabs have a menu of tabs")
	}
	saveShot(t, tt, "session-tabs-few.png")
	for i := 3; i < 14; i++ {
		demoSession(a, "server-"+strconv.Itoa(i), "10.0.0."+strconv.Itoa(10+i))
	}
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find(locales.AllTabs()); !ok {
		t.Fatalf("no menu of tabs for 14: %q", tt.Texts())
	}
	// The last server, chosen from the menu, shows, and its tab is in view.
	if err := tt.Click(locales.AllTabs()); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("server-13"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Frame()
	tt.Frame()
	if a.current().label() != "server-13" {
		t.Fatalf("the menu chose %q", a.current().label())
	}
	tab, ok := tt.Access("server-13")
	if !ok || !tab.Selected || tab.Bounds.X+tab.Bounds.W > 1100 || tab.Bounds.W < sessionTabMin-1 {
		t.Fatalf("the chosen tab is at %+v", tab.Bounds)
	}
	saveShot(t, tt, "session-tabs-many.png")
}

// TestSessionTabsJustFit: tabs that just fit share the row whole, with
// nothing to scroll and no menu, the first in view while the last is
// chosen.
func TestSessionTabsJustFit(t *testing.T) {
	a, p := testApp(t)
	for i := range 6 {
		demoSession(a, "server-"+strconv.Itoa(i), "10.0.0."+strconv.Itoa(10+i))
	}
	a.active, a.sidebarHidden = 5, true
	tt := ui.NewTester(a.View, 840, 600)
	p.tt = tt
	for range 4 {
		tt.Frame()
	}
	if share := (a.tabsRoom - 2 - sessionTabGap*5) / 6; share < sessionTabMin {
		t.Fatalf("the test's window is too narrow: %v a tab", share)
	}
	first, _ := tt.Access("server-0")
	last, _ := tt.Access("server-5")
	if a.tabsScroll.X != 0 || a.tabsScroll.MaxX > 0.5 || tt.HasText(locales.AllTabs()) {
		t.Fatalf("scrolled %v of %v, menu %v", a.tabsScroll.X, a.tabsScroll.MaxX, tt.HasText(locales.AllTabs()))
	}
	if first.Bounds.X < 0 || last.Bounds.X+last.Bounds.W > 840 {
		t.Fatalf("first at %+v, last at %+v", first.Bounds, last.Bounds)
	}
}
