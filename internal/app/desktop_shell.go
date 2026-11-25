package app

import (
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The desktop's shell: the wallpaper, the icons, the windows and the dock.
// This is a first, plain version that the shell's files replace: the
// window manager (moving, resizing, snapping, keys), the icon grid
// (selecting, moving, folders, menus), the dock, the launcher and the
// wallpapers.

// desktopShellState is what the shell keeps for a desktop besides its
// windows.
type desktopShellState struct {
	selectedIcon desktopAppID
}

// desktopDockHeight is the dock's height, its margin below included.
const desktopDockHeight = 64

// placeDesktopWindow places a new window w on d: cascading from the
// frontmost window, within the area.
func (a *App) placeDesktopWindow(d *desktopState, w *desktopWindow) {
	area := d.area
	if area.W == 0 {
		area = desktopSize{W: 1200, H: 760}
	}
	w.w, w.h = min(w.w, area.W-40), min(w.h, area.H-desktopDockHeight-20)
	w.x, w.y = max(20, (area.W-w.w)/2), max(10, (area.H-desktopDockHeight-w.h)/2)
	if top := d.topWindow(); top != nil {
		w.x, w.y = top.x+28, top.y+28
		if w.x+w.w > area.W || w.y+w.h > area.H-desktopDockHeight {
			w.x, w.y = 20, 10
		}
	}
}

// desktopArea is the desktop row p's main area: the desktop.
func (a *App) desktopArea(c *ui.Context, p *Session) {
	d := p.desktop
	s := p.panelOf
	root := ui.Box(c).Fill().Clip().Gradient(ui.Hex("#5b6cf0"), ui.Hex("#a5c8ff"), 135)
	b := root.Bounds()
	if b.W > 0 {
		d.area = desktopSize{W: b.W, H: b.H}
	}
	root.Children(func() {
		a.desktopIcons(c, d, s)
		for _, w := range a.desktopWindowsByZ(d) {
			a.desktopWindowFrame(c, d, w)
		}
		a.desktopDock(c, d, s)
	})
}

// desktopWindowsByZ are d's windows not minimized, back to front.
func (a *App) desktopWindowsByZ(d *desktopState) []*desktopWindow {
	var out []*desktopWindow
	for _, w := range d.windows {
		if !w.minimized {
			out = append(out, w)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].z < out[j-1].z; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// desktopIcons are the desktop's icons, in columns from the top left.
func (a *App) desktopIcons(c *ui.Context, d *desktopState, s *Session) {
	ui.Column(c).Absolute().Top(12).Left(12).Bottom(desktopDockHeight).Gap(6).Wrap().Children(func() {
		for _, app := range desktopAppsFor(s) {
			if !app.onDesktop {
				continue
			}
			tile := ui.Column(c.Key(string(app.id))).Width(84).Padding(6).Gap(4).AlignItems(ui.Center).Radius(6).Role(ui.RoleButton).Label(app.name())
			if d.shell.selectedIcon == app.id {
				tile.Background(ui.RGBA(255, 255, 255, 0.25))
			}
			if tile.Clicked() {
				d.shell.selectedIcon = app.id
			}
			if tile.DoubleClicked() {
				a.desktopOpen(s, app.id, nil)
			}
			tile.Children(func() {
				ui.Box(c).Size(44, 44).Radius(10).Center().Background(app.tint).Children(func() {
					ui.Icon(c, icons.Get(app.icon)).FontSize(24).TextColor(ui.RGB(255, 255, 255))
				})
				ui.Text(c, app.name()).FontSize(12).TextColor(ui.RGB(255, 255, 255)).SingleLine()
			})
		}
	})
}

// desktopWindowFrame is the window w: its title bar and its app's view.
func (a *App) desktopWindowFrame(c *ui.Context, d *desktopState, w *desktopWindow) {
	x, y, ww, hh := w.x, w.y, w.w, w.h
	if w.maximized {
		x, y, ww, hh = 0, 0, d.area.W, d.area.H-desktopDockHeight
	}
	focused := d.focused == w
	frame := ui.Column(c.Key(w.id)).Absolute().Left(x).Top(y).Size(ww, hh).ZIndex(w.z+1).
		Radius(8).Clip().Background(theme.Background).Border(1, theme.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.3))
	if frame.PressedInside() && !focused {
		a.desktopFocus(w)
	}
	frame.Children(func() {
		bar := ui.Row(c).Height(desktopTitleHeight).Shrink(0).PaddingX(10).Gap(8).AlignItems(ui.Center).Background(colors.sidebar)
		if dx, dy, ok := bar.Dragged(); ok && !w.maximized {
			w.x, w.y = w.x+dx, max(0, w.y+dy)
		}
		if bar.DoubleClicked() {
			w.maximized = !w.maximized
		}
		bar.Children(func() {
			ui.Icon(c, icons.Get(w.app.icon)).FontSize(14).TextColor(w.app.tint)
			t := ui.Text(c, w.displayTitle()).SingleLine().Grow(1).MinWidth(0)
			if focused {
				t.Bold()
			}
			if widgets.IconButton(c, "minus", locales.DesktopMinimize()).Clicked() {
				w.minimized = true
				if d.focused == w {
					d.focused = d.topWindow()
				}
			}
			if widgets.IconButton(c, "square", locales.DesktopMaximize()).Clicked() {
				w.maximized = !w.maximized
			}
			if widgets.IconButton(c, "x", locales.Close()).Clicked() {
				a.desktopRequestClose(w)
			}
		})
		ui.Box(c).Grow(1).MinHeight(0).Children(func() { w.app.view(a, c, w) })
	})
}

// desktopDock is the dock at the bottom: the pinned and running apps, the
// clock and the server's name.
func (a *App) desktopDock(c *ui.Context, d *desktopState, s *Session) {
	ui.Row(c).Absolute().Bottom(8).Left(0).Right(0).Height(desktopDockHeight - 12).Justify(ui.Center).ZIndex(1 << 20).Children(func() {
		ui.Row(c).Padding(6, 10, 6, 10).Gap(6).Radius(12).AlignItems(ui.Center).Background(ui.RGBA(30, 30, 40, 0.55)).Children(func() {
			for _, app := range desktopAppsFor(s) {
				running := d.windowsOf(app.id)
				if !app.pinned && len(running) == 0 {
					continue
				}
				btn := ui.Column(c.Key("dock|" + string(app.id))).Gap(2).AlignItems(ui.Center).Role(ui.RoleButton).Label(app.name()).Tooltip(app.name())
				if btn.Clicked() {
					if len(running) > 0 {
						a.desktopFocus(running[len(running)-1])
					} else {
						a.desktopOpen(s, app.id, nil)
					}
				}
				btn.Children(func() {
					ui.Box(c).Size(34, 34).Radius(8).Center().Background(app.tint).Children(func() {
						ui.Icon(c, icons.Get(app.icon)).FontSize(18).TextColor(ui.RGB(255, 255, 255))
					})
					dot := ui.Box(c).Size(4, 4).Radius(2)
					if len(running) > 0 {
						dot.Background(ui.RGB(255, 255, 255))
					}
				})
			}
			ui.Box(c).Width(1).Height(28).Background(ui.RGBA(255, 255, 255, 0.3))
			ui.Text(c, time.Now().Format("15:04")).TextColor(ui.RGB(255, 255, 255))
			ui.Text(c, s.label()).TextColor(ui.RGB(255, 255, 255)).SingleLine().MaxWidth(160)
		})
	})
}

// desktopPersonalizePage is the settings app's Personalization page: the
// wallpaper, opening icons with one or two clicks, how new windows are
// placed, whether maximized windows cover the dock, the icons' size.
func (a *App) desktopPersonalizePage(c *ui.Context) {
	ui.Text(c, locales.DesktopComingSoon()).TextColor(theme.TextMuted)
}
