package app

import (
	"testing"

	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The session panel's header has a button that opens the connection
// manager, on both pages, and it fits the narrowest sidebar.
func TestManagerButton(t *testing.T) {
	for _, page := range []int{leftSessions, leftConnections} {
		a, p, tt := splitApp(t)
		a.setLeftPage(page)
		tt.Frame()
		clickButton(t, tt, locales.ConnectionManager())
		if !a.windowOpen(WindowManager) {
			t.Fatalf("page %d: the button opened no manager", page)
		}
		p.show(t, WindowManager)
	}

	a, _, tt := splitApp(t)
	tip := locales.WithShortcut(locales.ConnectionManager(), a.shortcutLabel(scManager))
	b, ok := tt.AccessByRole(ui.RoleButton, locales.ConnectionManager())
	if !ok {
		t.Fatal("no button")
	}
	tt.Move(b.Bounds.X+b.Bounds.W/2, b.Bounds.Y+b.Bounds.H/2)
	tt.Advance(2e9)
	tt.Frame()
	if !tt.HasText(tip) {
		t.Errorf("no tooltip %q; texts %q", tip, tt.Texts())
	}
	saveShot(t, tt, "manager-button-en.png")
	for _, w := range []float32{defaultSideWidth, sidebarLimits.first} {
		a.sideWidth = w
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			tt.Frame()
			// The header's four buttons keep their size, side by side,
			// within the sidebar.
			var last float32
			for _, name := range []string{locales.ConnectionManager(), locales.Options(), locales.LocalTerminal(), locales.NewTab()} {
				b, ok := tt.AccessByRole(ui.RoleButton, name)
				if !ok {
					t.Fatalf("width %v: no %q button", w, name)
				}
				if b.Bounds.W < 19 || b.Bounds.X < last || b.Bounds.X+b.Bounds.W > w {
					t.Errorf("width %v: %q at %+v, after %v", w, name, b.Bounds, last)
				}
				last = b.Bounds.X + b.Bounds.W
			}
			name := "manager-button-light"
			if dark {
				name = "manager-button-dark"
			}
			saveShot(t, tt, name+"-"+map[float32]string{defaultSideWidth: "default", sidebarLimits.first: "min"}[w]+".png")
		}
	}
}
