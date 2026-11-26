package widgets

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestColumnHead drags the grips of a fixed and of a sharing column, and
// gives one its default back with a double click; neither press clicks
// the header, which sorts.
func TestColumnHead(t *testing.T) {
	widths := ColumnWidths{}
	var fixed, shared ui.Rect
	clicks := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).FillWidth().Gap(10).Padding(0, 20).Children(func() {
			h := ColumnHead(c, widths, "size", 100, 40, func() { ui.Text(c, "Size") })
			if h.Clicked() {
				clicks++
			}
			fixed = h.Bounds()
			shared = ColumnHead(c, widths, "name", 0, 40, func() { ui.Text(c, "Name") }).Bounds()
			ui.Text(c, "Rest").Grow(1).Basis(0)
		})
	}, 600, 100)
	tt.Frame()
	if fixed.W != 100 {
		t.Fatalf("default width %v, want 100", fixed.W)
	}
	drag := func(r ui.Rect, dx float32) {
		x, y := r.X+r.W+1, r.Y+r.H/2
		tt.Press(x, y)
		tt.Frame()
		tt.Move(x+dx, y)
		tt.Frame()
		tt.Release(x+dx, y)
		tt.Frame()
	}
	drag(fixed, 40)
	if got := widths["size"]; got != 140 || fixed.W != 140 {
		t.Fatalf("dragged width %v (laid out %v), want 140", got, fixed.W)
	}
	before := shared.W
	drag(shared, -30)
	if got := widths["name"]; got != before-30 {
		t.Fatalf("sharing column dragged to %v, want %v", got, before-30)
	}
	drag(fixed, -500)
	if got := widths["size"]; got != 40 {
		t.Fatalf("width %v under the least, want 40", got)
	}
	if clicks != 0 {
		t.Fatalf("dragging a grip clicked the header %d times", clicks)
	}
	x, y := fixed.X+fixed.W+1, fixed.Y+fixed.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	if _, ok := widths["size"]; ok || fixed.W != 100 {
		t.Fatalf("double click left width %v, want the default 100", fixed.W)
	}
	if err := tt.Click("Size"); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Fatalf("a click on the title clicked the header %d times, want 1", clicks)
	}
}
