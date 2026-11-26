package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSessionTreeGuides joins each server's child rows to it.
func TestSessionTreeGuides(t *testing.T) {
	a, p := testApp(t)
	web := demoSession(a, "web-1", "10.0.0.11")
	db := demoSession(a, "db-1", "10.0.0.12")
	a.openDesktop(web)
	a.openPanelTab(web, tabFiles)
	a.openDesktop(db)
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	tt.Frame()
	// web-1, its files and desktop rows, db-1, its desktop row.
	if len(a.sessions) != 5 || a.sessions[0] != web || a.sessions[3] != db {
		t.Fatalf("rows %q", rowNames(a))
	}
	want := []sessionGuide{
		{children: true},
		{},
		{lastChild: true},
		{children: true},
		{lastChild: true},
	}
	for i, w := range want {
		if got := a.sessionGuideOf(i); got != w {
			t.Errorf("row %d (%s): guide %+v, want %+v", i, rowNames(a)[i], got, w)
		}
	}
	a.active = 0
	tt.Frame()
	saveShot(t, tt, "session-tree.png")
}
