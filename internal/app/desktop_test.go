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
