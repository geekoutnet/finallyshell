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
	t := c.Theme()
	head := widths.Size(ui.Row(c.Key("head:"+id)).AlignItems(ui.Center), id, def)
	head.Children(func() {
		content()
		// The grip straddles the gap after the cell.
		grip := ui.Box(c.Key("grip:" + id)).Absolute().Top(0).Bottom(0).End(-6).Width(8).Cursor(ui.CursorResizeEW).Role(ui.RoleNone)
		if grip.DoubleClicked() {
			delete(widths, id)
		} else if dx, _, held := grip.Dragged(); held && dx != 0 {
			w := widths.Width(id, def)
			if w <= 0 {
				w = head.Bounds().W
			}
			widths[id] = max(min(w+dx, columnMaxWidth), least, 16)
		}
		grip.Draw(func(p *ui.Painter, r ui.Rect) {
			color := t.Border
			if grip.Hovered() || grip.Pressed() {
				color = t.Accent
			}
			p.Fill(ui.Rect{X: r.X + r.W/2 - 0.5, Y: r.Y + r.H/4, W: 1, H: r.H / 2}, color, 0)
		})
	})
	return head
}
