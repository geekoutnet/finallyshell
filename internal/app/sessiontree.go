package app

import "github.com/egoist/mygo/ui"

// The session list's tree: dashed lines join a server's child rows (its
// panel row and its desktop) to it, and a little room separates one server
// from the next, so that the rows of each server read as one group.

// sessionGuide is where a row of the session list is in its server's
// group.
type sessionGuide struct {
	// children holds for a server with child rows below it.
	children bool
	// lastChild holds for the last child row of its server.
	lastChild bool
}

// sessionGuideOf is the guide of the row at i.
func (a *App) sessionGuideOf(i int) sessionGuide {
	s := a.sessions[i]
	next := (*Session)(nil)
	if i+1 < len(a.sessions) {
		next = a.sessions[i+1]
	}
	if s.isPanelTab() {
		return sessionGuide{lastChild: next == nil || next.panelOf != s.panelOf}
	}
	return sessionGuide{children: next != nil && next.panelOf == s}
}

// treeX is where the tree's vertical line runs, under a server's status
// dot: the row's left padding and half the dot.
const treeX = 8 + 4

func treeColor() ui.Color { return theme.TextMuted.Alpha(0.55) }

// dashV draws a vertical dashed line at x from y0 to y1.
func dashV(p *ui.Painter, x, y0, y1 float32, color ui.Color) {
	for y := y0; y < y1; y += 5 {
		p.Fill(ui.Rect{X: x - 0.5, Y: y, W: 1, H: min(3, y1-y)}, color, 0)
	}
}

// dashH draws a horizontal dashed line at y from x0 to x1.
func dashH(p *ui.Painter, x0, x1, y float32, color ui.Color) {
	for x := x0; x < x1; x += 5 {
		p.Fill(ui.Rect{X: x, Y: y - 0.5, W: min(3, x1-x), H: 1}, color, 0)
	}
}

// drawServerGuide draws, on a server's row, the start of the line down to
// its children.
func drawServerGuide(row ui.Element, g sessionGuide) {
	row.Draw(func(p *ui.Painter, r ui.Rect) {
		if g.children {
			dashV(p, r.X+treeX, r.Y+r.H/2+7, r.Y+r.H, treeColor())
		}
	})
}

// drawChildGuide draws, on a child row, the line down from its server and
// the branch to its icon: down to the next child, or to the branch alone
// for the last.
func drawChildGuide(row ui.Element, g sessionGuide) {
	row.Draw(func(p *ui.Painter, r ui.Rect) {
		mid := r.Y + r.H/2
		end := r.Y + r.H
		if g.lastChild {
			end = mid
		}
		dashV(p, r.X+treeX, r.Y, end, treeColor())
		dashH(p, r.X+treeX+2, r.X+8+panelIndent-3, mid, treeColor())
	})
}
