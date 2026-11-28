package widgets

import "github.com/egoist/mygo/ui"

// ColumnWidths are the widths the user gave the columns of a table built
// by hand, by column ID, for as long as the app runs. ui.Table keeps its
// own; this is for tables of rows the views lay out themselves.
type ColumnWidths map[string]float32

// Width is the width of column id: the user's, or def.
func (w ColumnWidths) Width(id string, def float32) float32 {
	if v, ok := w[id]; ok {
		return v
	}
	return def
}

// Size makes cell as wide as column id: the user's width, or def, or for
// a def of 0 a share of the room the other columns leave.
func (w ColumnWidths) Size(cell ui.Element, id string, def float32) ui.Element {
	if v := w.Width(id, def); v > 0 {
		return cell.Width(v).Shrink(0)
	}
	return cell.Grow(1).Basis(0).MinWidth(0)
}

// columnMaxWidth is the widest a column of a hand-built table grows.
const columnMaxWidth = 640

// ColumnHead is the header cell of column id, sized as Size does, content
// building what it shows. A grip on its end edge resizes the column
// between least and 640 DIPs, as ui.Table's do, a column sharing the room
// left keeping the width it had then; a double click there gives it its
// default back. The rows' cells take their widths from the same
// ColumnWidths with Size. widths must not be nil. Ask Clicked on what it
// returns, for a header that sorts.
func ColumnHead(c *ui.Context, widths ColumnWidths, id string, def, least float32, content func()) ui.Element {
	head := widths.Size(ui.Row(c.Key("head:"+id)).AlignItems(ui.Center), id, def)
	head.Children(func() {
		content()
		if dx, reset := grip(c, id); reset {
			delete(widths, id)
		} else {
			widths.Resize(id, head, def, least, dx)
		}
	})
	return head
}

// FlexHead is the header cell of column id, which takes the room the
// other columns leave, at grow, content building what it shows. A grip on
// its end edge, like ColumnHead's, moves the border with the column after
// it: FlexHead returns how far it was dragged in this frame, which the
// caller gives that column the other way with Resize, so that the border
// follows the pointer; and whether it was double-clicked, to give that
// column its default back.
func FlexHead(c *ui.Context, id string, grow float32, content func()) (head ui.Element, dx float32, reset bool) {
	head = ui.Row(c.Key("head:" + id)).AlignItems(ui.Center).Grow(grow).Basis(0).MinWidth(0)
	head.Children(func() {
		content()
		dx, reset = grip(c, id)
	})
	return head, dx, reset
}

// Resize gives column id, whose header is head, dx more width, between
// least and columnMaxWidth DIPs; a column sharing the room left keeps the
// width it had then.
func (w ColumnWidths) Resize(id string, head ui.Element, def, least, dx float32) {
	if dx == 0 {
		return
	}
	v := w.Width(id, def)
	if v <= 0 {
		v = head.Bounds().W
	}
	w[id] = max(min(v+dx, columnMaxWidth), least, 16)
}

// grip is the grip on the end edge of a header cell, built in it: how far
// it was dragged in this frame, and whether it was double-clicked.
func grip(c *ui.Context, id string) (dx float32, reset bool) {
	t := c.Theme()
	// The grip straddles the gap after the cell.
	g := ui.Box(c.Key("grip:" + id)).Absolute().Top(0).Bottom(0).End(-6).Width(8).Cursor(ui.CursorResizeEW).Role(ui.RoleNone)
	if g.DoubleClicked() {
		reset = true
	} else if d, _, held := g.Dragged(); held {
		dx = d
	}
	g.Draw(func(p *ui.Painter, r ui.Rect) {
		color := t.Border
		if g.Hovered() || g.Pressed() {
			color = t.Accent
		}
		p.Fill(ui.Rect{X: r.X + r.W/2 - 0.5, Y: r.Y + r.H/4, W: 1, H: r.H / 2}, color, 0)
	})
	return dx, reset
}
