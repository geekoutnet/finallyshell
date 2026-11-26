package app

import (
	"slices"
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
