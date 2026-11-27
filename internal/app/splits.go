package app

import (
	"time"

	"finallyshell/internal/config"

	"github.com/egoist/mygo/ui"
)

// The pane sizes when none are saved, and where a double click on a
// divider puts it back.
const (
	defaultSideWidth   = 240 // DIPs
	defaultMonitorPct  = 62
	defaultTerminalPct = 64
	defaultTreeWidth   = 200 // DIPs
	// The monitor's share of the right column above the bottom panel,
	// when both are there.
	defaultRightMonitorPct = 45
)

// splitLimits are the smallest sizes, in DIPs, a divider's drag leaves to
// the pane before it (first) and the one after it (second). MyGo's own
// limit, Space(10), is 30 DIPs with this app's theme: too little for a
// sidebar or a terminal to show anything.
type splitLimits struct {
	first, second float32
	vertical      bool // the panes are stacked, the size their height
	percent       bool // the size is first's percentage of the room
}

var (
	// The sidebar keeps the monitor's tables readable; the main area keeps
	// the file table's columns.
	sidebarLimits = splitLimits{first: 180, second: 420}
	// The monitor keeps its IP line; the session list its title and a row,
	// over the account's bar.
	monitorLimits = splitLimits{first: 60, second: 90 + accountBarHeight, vertical: true, percent: true}
	// The terminal keeps a few rows above its command bar; the bottom
	// panel its tabs, path bar and a row of the table.
	terminalLimits = splitLimits{first: 140, second: 120, vertical: true, percent: true}
	// The tree keeps a short folder name; the table its name column.
	treeLimits = splitLimits{first: 100, second: 240}
)

// maxSavedWidth is the widest a saved sidebar, tree or right column may
// be: more is not a size a divider was dragged to, and is ignored.
const maxSavedWidth = 1600

// layoutSaveDelay is how long the dividers rest before their sizes are
// saved, so that a drag writes the settings once, not at each move.
const layoutSaveDelay = 400 * time.Millisecond

// restoreLayout takes the pane sizes saved in the settings, keeping the
// defaults for those missing or out of range.
func (a *App) restoreLayout() {
	s := a.settings
	if s.SidebarWidth >= sidebarLimits.first && s.SidebarWidth <= maxSavedWidth {
		a.sideWidth = s.SidebarWidth
	}
	if s.TreeWidth >= treeLimits.first && s.TreeWidth <= maxSavedWidth {
		a.treeWidth = s.TreeWidth
	}
	if s.MonitorPercent > 0 && s.MonitorPercent < 100 {
		a.monitorPct = s.MonitorPercent
	}
	if s.TerminalPercent > 0 && s.TerminalPercent < 100 {
		a.terminalPct = s.TerminalPercent
	}
	a.bottomHidden = s.BottomPanelHidden
	a.sidebarHidden = s.SidebarHidden
	if s.BottomPanelSide == config.PanelRight {
		a.bottomSide = config.PanelRight
	}
	if s.RightPanelWidth >= rightLimits.first && s.RightPanelWidth <= maxSavedWidth {
		a.rightWidth = s.RightPanelWidth
	}
	if s.MonitorSide == config.PanelRight {
		a.monitorSide = config.PanelRight
	}
	if s.RightMonitorPercent > 0 && s.RightMonitorPercent < 100 {
		a.rightMonitorPct = s.RightMonitorPercent
	}
	a.restoreCmdEditor()
	// The Processes and Network tabs' sizes, which the sessions share.
	if s.ProcListPercent > 0 && s.ProcListPercent < 100 {
		a.processes.listPct = s.ProcListPercent
	}
	if s.ProcFieldsPercent > 0 && s.ProcFieldsPercent < 100 {
		a.processes.fieldsPct = s.ProcFieldsPercent
	}
	if s.NetListPercent > 0 && s.NetListPercent < 100 {
		a.network.listPct = s.NetListPercent
	}
}

// splitMoved keeps the panes of split at least l's sizes when the user
// moved its divider, and saves the sizes once it rests. Only a move is
// clamped: a window too small for the size the user chose leaves it to
// MyGo, which gives the size back when there is room again.
func (a *App) splitMoved(c *ui.Context, split ui.Element, size *float32, l splitLimits) {
	if !clampSplit(split, size, l) {
		return
	}
	a.layoutDirty, a.layoutAt = true, c.Now()
	c.After(layoutSaveDelay)
}

// clampSplit keeps the panes of split at least l's sizes when the user
// moved its divider, and reports the move.
func clampSplit(split ui.Element, size *float32, l splitLimits) bool {
	if !split.Changed() {
		return false
	}
	clampSize(size, splitRoom(split, l), l)
	return true
}

// splitRoom is the room split's panes share, in DIPs, as the last frame
// laid it out: its width or height, less the divider's line.
func splitRoom(split ui.Element, l splitLimits) float32 {
	b := split.Bounds()
	if l.vertical {
		return b.H - 1
	}
	return b.W - 1
}

// clampSize keeps *size, the first pane's, to leave both panes at least
// l's sizes in room.
func clampSize(size *float32, room float32, l splitLimits) {
	if room <= 0 {
		return
	}
	v := *size
	if l.percent {
		v = v / 100 * room
	}
	// With no room for both, the first pane keeps its size.
	// A size within the limits stays as it is, not converted back
	// and forth: a double click saves the default itself.
	if clamped := max(min(v, room-l.second), l.first); clamped != v {
		if l.percent {
			clamped = clamped * 100 / room
		}
		*size = clamped
	}
}

// splitGripW is how wide the strip is, in DIPs, that a press drags a
// layout divider by: MyGo's own handle is Space(1.5) wide, 4.5 DIPs with
// this app's theme, which a pointer a little off the line misses.
const splitGripW = 12

// gripSplit lays out the split that build makes, filling the room, with
// wider grips over its divider (splitGrip), and sizes kept to l.
func (a *App) gripSplit(c *ui.Context, size *float32, reset float32, l splitLimits, build func() ui.Element) {
	ui.Column(c).Fill().Children(func() {
		split := build().SplitReset(reset).Fill()
		a.splitMoved(c, split, size, l)
		a.splitGrip(c, split, size, reset, l)
	})
}

// splitGrip widens the handle of split's divider to splitGripW, and marks
// the line's middle with a bar that shows it drags. MyGo's handle, in the
// middle of the strip, keeps its presses, which focus the divider for the
// arrow keys; the strips on either side drag it as that handle does,
// kept to l, and a double click puts it back at reset. split must fill
// the element the grips are put in, which they are laid out in.
func (a *App) splitGrip(c *ui.Context, split ui.Element, size *float32, reset float32, l splitLimits) {
	room := splitRoom(split, l)
	minPane := theme.Space(10) // MyGo's least pane
	if room < 2*minPane+1 {
		return // not laid out yet, or too small to share
	}
	line := *size
	if l.percent {
		line = line / 100 * room
	}
	// Where MyGo lays the line, also when the room is too small for size.
	line = max(min(line, room-minPane), minPane)
	moved := func() {
		a.layoutDirty, a.layoutAt = true, c.Now()
		c.After(layoutSaveDelay)
		c.Invalidate()
	}
	inner := theme.Space(1.5) / 2 // half MyGo's handle
	w := splitGripW/2 - inner
	active := false
	for i, from := range [2]float32{line + 0.5 - splitGripW/2, line + 0.5 + inner} {
		g := ui.Box(c.Key(i)).Absolute()
		if l.vertical {
			g.Left(0).Right(0).Top(from).Height(w).Cursor(ui.CursorResizeNS)
		} else {
			g.Top(0).Bottom(0).Start(from).Width(w).Cursor(ui.CursorResizeEW)
		}
		if g.DoubleClicked() {
			*size = reset
			moved()
			continue
		}
		dx, dy, held := g.Dragged()
		active = active || held || g.Hovered()
		d := dy
		if !l.vertical {
			d = dx
			if c.Direction() == ui.RightToLeft {
				d = -dx
			}
		}
		if !held || d == 0 {
			continue
		}
		if l.percent {
			d = d * 100 / room
		}
		*size += d
		clampSize(size, room, l)
		moved()
	}
	// The bar, which takes no input: MyGo's handle under it keeps the
	// press. Accent while the strips are hovered or held.
	bar := theme.Border
	if active {
		bar = theme.Accent
	}
	const barLen, barW = 32, 4
	m := ui.Box(c).Absolute().PassThrough()
	if l.vertical {
		m.Top(line + 0.5 - barW/2).Height(barW).Left(0).Right(0)
	} else {
		m.Start(line + 0.5 - barW/2).Width(barW).Top(0).Bottom(0)
	}
	m.Draw(func(p *ui.Painter, r ui.Rect) {
		if l.vertical {
			r = ui.Rect{X: r.X + (r.W-barLen)/2, Y: r.Y, W: barLen, H: r.H}
		} else {
			r = ui.Rect{X: r.X, Y: r.Y + (r.H-barLen)/2, W: r.W, H: barLen}
		}
		p.Fill(r, bar, barW/2)
	})
}

// endSplit lays out start and end side by side, end at the end, with a
// divider between them that sizes end: its width, *size, is kept from
// the end edge, so that more room goes to start. It returns the row
// that holds them, to size.
//
// ui.Split sizes its first pane, so the split is laid out in the other
// direction, where its first pane is the end one, and the panes are put
// back in the window's own direction.
func (a *App) endSplit(c *ui.Context, size *float32, reset float32, l splitLimits, start, end func()) ui.Element {
	dir := c.Direction()
	flipped := ui.RightToLeft
	if dir == ui.RightToLeft {
		flipped = ui.LeftToRight
	}
	pane := func(build func()) func() {
		return func() { ui.Column(c).Direction(dir).Fill().Children(build) }
	}
	return ui.Row(c).Direction(flipped).Children(func() {
		split := ui.Split(c, size, pane(end), pane(start)).SplitReset(reset).Fill()
		a.splitMoved(c, split, size, l)
		a.splitGrip(c, split, size, reset, l)
	})
}

// saveLayoutAtRest saves the pane sizes once the dividers stopped moving,
// asking for the frame that does while they wait: a frame asks only for
// the next one it needs.
func (a *App) saveLayoutAtRest(c *ui.Context) {
	if !a.layoutDirty {
		return
	}
	if wait := layoutSaveDelay - c.Now().Sub(a.layoutAt); wait > 0 {
		c.After(wait)
		return
	}
	a.saveLayout()
}

func (a *App) saveLayout() {
	a.layoutDirty = false
	a.settings.SidebarWidth = a.sideWidth
	a.settings.MonitorPercent = a.monitorPct
	a.settings.TerminalPercent = a.terminalPct
	a.settings.BottomPanelHidden = a.bottomHidden
	a.settings.SidebarHidden = a.sidebarHidden
	a.settings.BottomPanelSide = a.bottomSide
	a.settings.RightPanelWidth = a.rightWidth
	a.settings.MonitorSide = a.monitorSide
	a.settings.RightMonitorPercent = a.rightMonitorPct
	a.settings.TreeWidth = a.treeWidth
	a.settings.ProcListPercent = a.processes.listPct
	a.settings.ProcFieldsPercent = a.processes.fieldsPct
	a.settings.NetListPercent = a.network.listPct
	a.keepCmdEditorLayout()
	a.saveSettings(a.settings)
}
