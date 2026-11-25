package app

import (
	"cmp"
	"math"
	"slices"

	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopFrameDrag struct {
	window *desktopWindow
	start  ui.Point
	frame  ui.Rect
	active bool
}

func desktopWindowOrder(first, second *desktopWindow) int {
	if first.keepOnTop != second.keepOnTop {
		if first.keepOnTop {
			return 1
		}
		return -1
	}
	return cmp.Compare(first.z, second.z)
}

func (a *App) desktopWindowsByZ(d *desktopState) []*desktopWindow {
	out := slices.DeleteFunc(slices.Clone(d.windows), func(w *desktopWindow) bool { return w.minimized })
	slices.SortFunc(out, desktopWindowOrder)
	return out
}

func desktopWorkArea(d *desktopState, cover bool) desktopSize {
	area := d.area
	if area.W <= 0 || area.H <= 0 {
		area = desktopSize{1200, 760}
	}
	if !cover {
		area.H = max(1, area.H-desktopDockHeight)
	}
	return area
}

func desktopClamp(value, low, high float32) float32 { return min(max(value, low), high) }

func desktopWindowRect(w *desktopWindow) ui.Rect { return ui.Rect{X: w.x, Y: w.y, W: w.w, H: w.h} }

func desktopSetWindowRect(w *desktopWindow, r ui.Rect) { w.x, w.y, w.w, w.h = r.X, r.Y, r.W, r.H }

func desktopFitWindow(r ui.Rect, w *desktopWindow, area desktopSize) ui.Rect {
	r.W = desktopClamp(r.W, min(w.app.minSize.W, area.W), area.W)
	r.H = desktopClamp(r.H, min(w.app.minSize.H+desktopTitleHeight+desktopTitleDivider, area.H), area.H)
	r.X = desktopClamp(r.X, 0, area.W-r.W)
	r.Y = desktopClamp(r.Y, 0, area.H-r.H)
	return r
}

func (a *App) placeDesktopWindow(d *desktopState, w *desktopWindow) {
	area := desktopWorkArea(d, false)
	if last, ok := a.desktopWindowSizes[w.app.id]; ok {
		w.w, w.h = last.W, last.H
	}
	r := desktopFitWindow(desktopWindowRect(w), w, area)
	r.X, r.Y = (area.W-r.W)/2, (area.H-r.H)/2
	if a.desktopPreferences().StackWindows {
		if related := d.windowsOf(w.app.id); len(related) > 0 {
			r.X, r.Y = related[len(related)-1].x, related[len(related)-1].y
		}
	} else if top := d.topWindow(); top != nil {
		r.X, r.Y = top.x+28, top.y+28
		if r.X+r.W > area.W {
			r.X = min(24, area.W-r.W)
		}
		if r.Y+r.H > area.H {
			r.Y = min(16, area.H-r.H)
		}
	}
	desktopSetWindowRect(w, desktopFitWindow(r, w, area))
}

func (a *App) desktopSnapRect(d *desktopState, w *desktopWindow, mode string) ui.Rect {
	area := desktopWorkArea(d, mode == "max" && a.desktopPreferences().CoverDock)
	r := ui.Rect{W: area.W, H: area.H}
	if mode != "max" {
		r.W = min(area.W, max(area.W/2, w.app.minSize.W))
		if mode == "right" {
			r.X = area.W - r.W
		}
	}
	return r
}

func (a *App) desktopSetWindowMode(d *desktopState, w *desktopWindow, mode string) {
	if w.maximized || w.snapped != "" {
		if mode == "" {
			r := ui.Rect{X: w.restore[0], Y: w.restore[1], W: w.restore[2], H: w.restore[3]}
			desktopSetWindowRect(w, desktopFitWindow(r, w, desktopWorkArea(d, false)))
		}
	} else if mode != "" {
		w.restore = [4]float32{w.x, w.y, w.w, w.h}
	}
	w.maximized, w.snapped = mode == "max", ""
	if mode == "left" || mode == "right" {
		w.snapped = mode
	}
}

func (a *App) desktopToggleMaximize(d *desktopState, w *desktopWindow) {
	mode := "max"
	if w.maximized || w.snapped != "" {
		mode = ""
	}
	a.desktopSetWindowMode(d, w, mode)
}

func (a *App) desktopMinimize(d *desktopState, w *desktopWindow) {
	w.minimized = true
	if drag := d.shell.drag; drag != nil && drag.window == w {
		d.shell.drag, d.shell.preview = nil, ""
	}
	if d.focused == w {
		d.focused = d.topWindow()
	}
}

func (a *App) desktopShow(d *desktopState) {
	if d.shell.hidden != nil {
		for _, w := range d.windows {
			if d.shell.hidden[w] {
				w.minimized = false
			}
		}
		d.focused = d.topWindow()
		if w := d.shell.hiddenFocus; w != nil && d.shell.hidden[w] {
			d.focused = w
		}
		d.shell.hidden, d.shell.hiddenFocus = nil, nil
		return
	}
	d.shell.hidden = make(map[*desktopWindow]bool)
	d.shell.hiddenFocus = d.focused
	for _, w := range d.windows {
		if !w.minimized {
			d.shell.hidden[w] = true
			a.desktopMinimize(d, w)
		}
	}
	if len(d.shell.hidden) == 0 {
		d.shell.hidden = nil
	}
	d.focused = nil
}

func (a *App) desktopWindowFrame(c *ui.Context, d *desktopState, w *desktopWindow) {
	r := desktopWindowRect(w)
	switch {
	case w.maximized:
		r = a.desktopSnapRect(d, w, "max")
	case w.snapped != "":
		r = a.desktopSnapRect(d, w, w.snapped)
	default:
		r = desktopFitWindow(r, w, desktopWorkArea(d, false))
		desktopSetWindowRect(w, r)
	}
	frame := ui.Column(c.Key(w.id)).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).
		Radius(10).Clip().Background(theme.Background).Border(1, theme.Border).
		Shadow(0, 10, 29, 0, ui.RGBA(8, 11, 28, 0.38)).Label(w.displayTitle())
	if w.maximized {
		frame.Radius(0)
	}
	if frame.PressedInside() && d.focused != w {
		a.desktopFocus(w)
		c.Invalidate()
	}
	focused := d.focused == w
	frame.Children(func() {
		barColor := colors.sidebar
		if focused {
			barColor = ui.Hex("#eef1fb")
			if theme.Dark {
				barColor = ui.Hex("#242c41")
			}
		}
		ui.Row(c).Height(desktopTitleHeight).Shrink(0).AlignItems(ui.Center).Background(barColor).Children(func() {
			title := ui.Row(c.Key("title")).Grow(1).MinWidth(0).FillHeight().PaddingX(10).Gap(8).
				AlignItems(ui.Center).Cursor(ui.CursorMove).Label(w.displayTitle())
			a.desktopMove(c, d, w, title, r)
			title.ContextMenu(func(m *ui.Menu) { a.desktopTitleMenu(m, d, w) })
			title.Children(func() {
				ui.Icon(c, icons.Get(w.app.icon)).FontSize(15).TextColor(w.app.tint).Shrink(0)
				text := ui.Text(c, w.displayTitle()).SingleLine().MinWidth(0)
				if focused {
					text.Bold()
				} else {
					text.TextColor(theme.TextMuted)
				}
				if w.keepOnTop {
					ui.Icon(c, icons.Get("pin")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
				}
			})
			if desktopFrameButton(c, "minus", locales.DesktopMinimize()).Clicked() {
				a.desktopMinimize(d, w)
				c.Invalidate()
			}
			icon, label := "square", locales.DesktopMaximize()
			if w.maximized || w.snapped != "" {
				icon, label = "copy", locales.DesktopWinRestore()
			}
			if desktopFrameButton(c, icon, label).Clicked() {
				a.desktopToggleMaximize(d, w)
				c.Invalidate()
			}
			if desktopFrameButton(c, "x", locales.Close()).Clicked() {
				a.desktopRequestClose(w)
				c.Invalidate()
			}
			ui.Box(c).Width(5).Shrink(0)
		})
		ui.Box(c).Height(desktopTitleDivider).Shrink(0).Background(theme.Border)
		contentHeight := max(0, r.H-desktopTitleHeight-desktopTitleDivider)
		if r.W < w.app.minSize.W || contentHeight < w.app.minSize.H {
			ui.ScrollBoth(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				ui.Box(c).Size(max(r.W, w.app.minSize.W), max(contentHeight, w.app.minSize.H)).Children(func() { w.app.view(a, c, w) })
			})
		} else {
			ui.Box(c).Grow(1).MinHeight(0).MinWidth(0).Clip().Children(func() { w.app.view(a, c, w) })
		}
		if !w.maximized && w.snapped == "" {
			a.desktopResizeHandles(c, d, w)
		}
	})
}

func desktopFrameButton(c *ui.Context, icon, label string) ui.Element {
	button := ui.Box(c.Key(label)).Size(29, 26).Shrink(0).Radius(6).Center().Role(ui.RoleButton).Label(label).Tooltip(label)
	if button.Hovered() {
		if icon == "x" {
			button.Background(ui.RGBA(230, 62, 83, .24))
		} else {
			button.Background(theme.Border)
		}
	}
	button.Children(func() { ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(theme.Text) })
	return button
}

func (a *App) desktopTitleMenu(m *ui.Menu, d *desktopState, w *desktopWindow) {
	if m.Item(locales.DesktopWinRestore()).Disabled(!w.maximized && w.snapped == "").Chosen() {
		a.desktopSetWindowMode(d, w, "")
	}
	if m.Item(locales.DesktopMinimize()).Chosen() {
		a.desktopMinimize(d, w)
	}
	if m.Item(locales.DesktopMaximize()).Disabled(w.maximized).Chosen() {
		a.desktopSetWindowMode(d, w, "max")
	}
	if m.Item(locales.DesktopWinKeepOnTop()).Checked(w.keepOnTop).Chosen() {
		w.keepOnTop = !w.keepOnTop
	}
	m.Separator()
	if m.Item(locales.Close()).Chosen() {
		a.desktopRequestClose(w)
	}
}

func (a *App) desktopMove(c *ui.Context, d *desktopState, w *desktopWindow, title ui.Element, r ui.Rect) {
	_, _, pressed := title.Dragged()
	p := d.shell.pointer
	if pressed {
		g := d.shell.drag
		if g == nil || g.window != w {
			g = &desktopFrameDrag{window: w, start: p, frame: r}
			d.shell.drag = g
		}
		dx, dy := p.X-g.start.X, p.Y-g.start.Y
		if !g.active && math.Hypot(float64(dx), float64(dy)) >= 4 {
			g.active = true
			if w.maximized || w.snapped != "" {
				anchor := desktopClamp((g.start.X-r.X)/max(r.W, 1), 0, 1)
				a.desktopSetWindowMode(d, w, "")
				g.frame = desktopWindowRect(w)
				g.frame.X = g.start.X - g.frame.W*anchor
				g.frame.Y = g.start.Y - desktopClamp(g.start.Y-r.Y, 0, desktopTitleHeight-4)
			}
		}
		if g.active {
			next := g.frame
			next.X += dx
			next.Y += dy
			desktopSetWindowRect(w, desktopFitWindow(next, w, desktopWorkArea(d, false)))
			d.shell.preview = ""
			switch {
			case p.Y <= 12:
				d.shell.preview = "max"
			case p.X <= 12:
				d.shell.preview = "left"
			case p.X >= d.area.W-12:
				d.shell.preview = "right"
			}
			if r != desktopWindowRect(w) {
				c.Invalidate()
			}
		}
	} else if g := d.shell.drag; g != nil && g.window == w {
		if _, _, ended := title.DragEnded(); ended && g.active && d.shell.preview != "" {
			a.desktopSetWindowMode(d, w, d.shell.preview)
		}
		d.shell.drag, d.shell.preview = nil, ""
		c.Invalidate()
	}
	if title.DoubleClicked() {
		a.desktopToggleMaximize(d, w)
		c.Invalidate()
	}
}

func (a *App) desktopSnapPreview(c *ui.Context, d *desktopState) {
	if d.shell.preview == "" || d.shell.drag == nil {
		return
	}
	r := a.desktopSnapRect(d, d.shell.drag.window, d.shell.preview)
	ui.Box(c).Absolute().Left(r.X+4).Top(r.Y+4).Size(max(0, r.W-8), max(0, r.H-8)).Radius(10).IgnoreInput(true).
		Background(ui.RGBA(137, 170, 255, .26)).Border(2, ui.RGBA(203, 220, 255, .76))
}

func (a *App) desktopResizeHandles(c *ui.Context, d *desktopState, w *desktopWindow) {
	const edge, corner = float32(5), float32(11)
	for _, side := range []struct {
		x, y   int
		cursor ui.Cursor
	}{
		{-1, 0, ui.CursorResizeEW}, {1, 0, ui.CursorResizeEW}, {0, -1, ui.CursorResizeNS}, {0, 1, ui.CursorResizeNS},
		{-1, -1, ui.CursorResizeNWSE}, {1, 1, ui.CursorResizeNWSE}, {1, -1, ui.CursorResizeNESW}, {-1, 1, ui.CursorResizeNESW},
	} {
		handle := ui.Box(c.Key(side.x*3 + side.y)).Absolute().ZIndex(2).Cursor(side.cursor)
		size := edge
		if side.x != 0 && side.y != 0 {
			size = corner
		}
		switch side.x {
		case -1:
			handle.Left(0).Width(size)
		case 1:
			handle.Right(0).Width(size)
		default:
			handle.Left(corner).Right(corner)
		}
		switch side.y {
		case -1:
			handle.Top(0).Height(size)
		case 1:
			handle.Bottom(0).Height(size)
		default:
			handle.Top(corner).Bottom(corner)
		}
		if dx, dy, ok := handle.Dragged(); ok && (dx != 0 || dy != 0) {
			r := desktopWindowRect(w)
			area := desktopWorkArea(d, false)
			minW, minH := min(w.app.minSize.W, area.W), min(w.app.minSize.H+desktopTitleHeight+desktopTitleDivider, area.H)
			if side.x < 0 {
				delta := desktopClamp(dx, -r.X, r.W-minW)
				r.X += delta
				r.W -= delta
			}
			if side.x > 0 {
				r.W = desktopClamp(r.W+dx, minW, area.W-r.X)
			}
			if side.y < 0 {
				delta := desktopClamp(dy, -r.Y, r.H-minH)
				r.Y += delta
				r.H -= delta
			}
			if side.y > 0 {
				r.H = desktopClamp(r.H+dy, minH, area.H-r.Y)
			}
			desktopSetWindowRect(w, r)
			if a.desktopWindowSizes == nil {
				a.desktopWindowSizes = make(map[desktopAppID]desktopSize)
			}
			a.desktopWindowSizes[w.app.id] = desktopSize{r.W, r.H}
			c.Invalidate()
		}
	}
}
