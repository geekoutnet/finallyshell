package app

import (
	"slices"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type desktopDockDrag struct {
	id    desktopAppID
	start float32
	moved bool
}

func (a *App) desktopPinned() []string {
	if pinned := a.desktopPreferences().Pinned; pinned != nil {
		return *pinned
	}
	var list []string
	for _, app := range desktopApps {
		if app.pinned && app.id != appAI {
			list = append(list, string(app.id))
		}
	}
	return list
}

func (a *App) desktopSetPinned(pinned []string) {
	if pinned == nil {
		pinned = []string{}
	}
	a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.Pinned = &pinned })
}

func (a *App) desktopPinMenu(m *ui.Menu, id desktopAppID) {
	pinned := a.desktopPinned()
	idx := slices.Index(pinned, string(id))
	if idx >= 0 {
		if m.Item(locales.DesktopDockUnpin()).Chosen() {
			a.desktopSetPinned(slices.Delete(slices.Clone(pinned), idx, idx+1))
		}
	} else if m.Item(locales.DesktopDockPin()).Chosen() {
		a.desktopSetPinned(append(slices.Clone(pinned), string(id)))
	}
}

func (a *App) desktopDockApps(d *desktopState, s *Session) []*desktopApp {
	var list []*desktopApp
	add := func(app *desktopApp) {
		if app != nil && app.id != appAI && !slices.ContainsFunc(list, func(existing *desktopApp) bool { return existing.id == app.id }) {
			list = append(list, app)
		}
	}
	for _, id := range a.desktopPinned() {
		app := desktopAppByID(desktopAppID(id))
		if app != nil && (app.available == nil || app.available(s)) {
			add(app)
		}
	}
	for _, w := range d.windows {
		add(w.app)
	}
	return list
}

func desktopDockGroup(c *ui.Context) ui.Element {
	from, to, border := ui.RGBA(249, 251, 255, .85), ui.RGBA(219, 232, 255, .71), ui.RGBA(255, 255, 255, .62)
	if theme.Dark {
		from, to, border = ui.RGBA(30, 41, 62, .91), ui.RGBA(18, 25, 41, .81), ui.RGBA(211, 227, 255, .24)
	}
	return ui.Row(c).Height(49).Shrink(0).Padding(3, 8).Gap(5).AlignItems(ui.Center).Radius(14).
		Gradient(from, to, 155).Border(1, border).Shadow(0, 4, 13, 0, ui.RGBA(7, 12, 32, .28))
}

func desktopDockButton(c *ui.Context, label string) ui.Element {
	return ui.Column(c.Key(label)).Size(37, 42).Shrink(0).Radius(8).Center().Gap(3).Role(ui.RoleButton).Label(label).Tooltip(label)
}

func (a *App) desktopDock(c *ui.Context, d *desktopState, s *Session) {
	sh := &d.shell
	now := c.Now()
	c.After(time.Minute - time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond()))
	if sh.covered && !sh.reveal && !sh.launcher && sh.dockPopup == "" && !sh.hostPopup && !sh.dockOverflow && sh.dockDrag == nil &&
		!sh.trayPopup && !sh.clockPopup && !sh.powerPopup && sh.power.dialog == nil {
		return
	}
	apps := a.desktopDockApps(d, s)
	hostWidth := float32(25) // the host's icon; its name is in the tooltip and the popover
	availableSlots := max(1, int((d.area.W-hostWidth-210-desktopTrayWidth(d.area.W)+46)/42))
	visible := len(apps)
	if visible > availableSlots {
		visible = availableSlots - 1
	}
	sh.dockPositions = sh.dockPositions[:0]
	ui.Row(c.Key("dock")).Absolute().Left(0).Right(0).Bottom(8).Justify(ui.Center).AlignItems(ui.Center).Gap(8).ZIndex(5).Children(func() {
		launcher := desktopDockGroup(c.Key("start"))
		launcher.Children(func() {
			button := desktopDockButton(c, locales.DesktopShellLauncher())
			b := button.Bounds()
			sh.launcherButton = ui.Rect{X: b.X - sh.root.X, Y: b.Y - sh.root.Y, W: b.W, H: b.H}
			if button.Clicked() {
				a.desktopToggleLauncher(d)
				c.Invalidate()
			}
			button.Children(func() { desktopTile(c, "layout-grid", ui.Hex("#6354e8"), 30, button.Hovered()) })
			if ai := desktopAppByID(appAI); ai != nil && (ai.available == nil || ai.available(s) || len(d.windowsOf(appAI)) > 0) {
				a.desktopDockApp(c, d, s, ai, false)
			}
		})
		if len(apps) > 0 {
			desktopDockGroup(c.Key("app-dock")).Children(func() {
				for _, app := range apps[:visible] {
					a.desktopDockApp(c, d, s, app, true)
				}
				if visible < len(apps) {
					a.desktopDockOverflow(c, d, s, apps[visible:])
				}
			})
		}
		desktopDockGroup(c.Key("system-dock")).Children(func() {
			button := desktopDockButton(c, locales.DesktopDockShowDesktop())
			if button.Clicked() {
				a.desktopShow(d)
				c.Invalidate()
			}
			button.Children(func() { ui.Icon(c, icons.Get("panels-top-left")).FontSize(17).TextColor(theme.Text) })
			ui.Box(c).Size(1, 24).Background(theme.Text.Alpha(.22))
			a.desktopDockTray(c, d, s)
			a.desktopDockHost(c, d, s, hostWidth)
			a.desktopDockPower(c, d, s)
		})
	})
	if drag := sh.dockDrag; drag != nil && sh.dockRelease {
		sh.dockDrag = nil
		sh.dockRelease = false
		if drag.moved {
			a.desktopReorderDock(d, drag.id)
		}
	}
}

func (a *App) desktopDockApp(c *ui.Context, d *desktopState, s *Session, app *desktopApp, dragAllowed bool) {
	sh := &d.shell
	windows := d.windowsOf(app.id)
	button := desktopDockButton(c.Key(app.id), app.name())
	if button.Clicked() {
		a.desktopDockActivate(d, s, app, windows)
		c.Invalidate()
	}
	button.ContextMenu(func(m *ui.Menu) { a.desktopDockMenu(m, d, s, app, windows) })
	if dragAllowed && slices.Contains(a.desktopPinned(), string(app.id)) {
		b := button.Bounds()
		sh.dockPositions = append(sh.dockPositions, desktopDockPosition{app.id, b.X + b.W/2 - sh.root.X})
		_, _, pressed := button.Dragged()
		if pressed {
			if sh.dockDrag == nil {
				sh.dockDrag = &desktopDockDrag{id: app.id, start: sh.pointer.X}
			}
			if drag := sh.dockDrag; drag.id == app.id && (sh.pointer.X-drag.start > 5 || drag.start-sh.pointer.X > 5) {
				drag.moved = true
				button.Opacity(.55)
			}
		} else if drag := sh.dockDrag; drag != nil && drag.id == app.id {
			if _, _, ok := button.DragEnded(); ok {
				sh.dockRelease = true
			} else {
				sh.dockDrag = nil
			}
		}
	}
	button.Children(func() {
		desktopTile(c, app.icon, app.tint, 30, button.Hovered())
		width, color := float32(4), theme.TextMuted
		if d.focused != nil && d.focused.app.id == app.id {
			width, color = 13, theme.Accent
		}
		if len(windows) == 0 {
			color = ui.Transparent
		}
		ui.Box(c).Size(width, 3).Radius(2).Background(color)
	})
	open := sh.dockPopup == app.id && len(windows) > 1
	if sh.dockPopup == app.id && !open {
		sh.dockPopup = ""
	}
	if open {
		a.desktopDockPopover(c.Key("dock-windows"), button, &open, func() {
			ui.Text(c, app.name()).Bold().SingleLine().FillWidth()
			ui.Scroll(c).MaxHeight(220).FillWidth().Children(func() {
				for _, w := range windows {
					if desktopPanelRow(c.Key(w.id), app.icon, w.displayTitle(), d.focused == w).Clicked() {
						a.desktopFocus(w)
						open = false
					}
				}
			})
		})
		if !open {
			sh.dockPopup = ""
		}
	}
}

type desktopDockPosition struct {
	id     desktopAppID
	center float32
}

func (a *App) desktopReorderDock(d *desktopState, id desktopAppID) {
	positions := d.shell.dockPositions
	if len(positions) < 2 {
		return
	}
	dest := positions[len(positions)-1].id
	for _, pos := range positions {
		if d.shell.pointer.X <= pos.center+20 {
			dest = pos.id
			break
		}
	}
	pinned := slices.Clone(a.desktopPinned())
	from, to := slices.Index(pinned, string(id)), slices.Index(pinned, string(dest))
	if from < 0 || to < 0 || from == to {
		return
	}
	pinned = slices.Delete(pinned, from, from+1)
	a.desktopSetPinned(slices.Insert(pinned, to, string(id)))
}

func (a *App) desktopDockActivate(d *desktopState, s *Session, app *desktopApp, windows []*desktopWindow) {
	sh := &d.shell
	sh.launcher, sh.hostPopup, sh.dockOverflow = false, false, false
	switch len(windows) {
	case 0:
		sh.dockPopup = ""
		a.desktopOpen(s, app.id, nil)
	case 1:
		sh.dockPopup = ""
		w := windows[0]
		if d.focused == w && !w.minimized {
			a.desktopMinimize(d, w)
		} else {
			a.desktopFocus(w)
		}
	default:
		if sh.dockPopup == app.id {
			sh.dockPopup = ""
		} else {
			sh.dockPopup = app.id
		}
	}
}

func (a *App) desktopDockMenu(m *ui.Menu, d *desktopState, s *Session, app *desktopApp, windows []*desktopWindow) {
	for _, w := range windows {
		if m.Item(w.displayTitle()).Checked(d.focused == w).Chosen() {
			a.desktopFocus(w)
		}
	}
	if len(windows) > 0 {
		m.Separator()
	}
	if m.Item(locales.DesktopDockNewWindow()).Disabled(app.single && len(windows) > 0 || app.available != nil && !app.available(s)).Chosen() {
		a.desktopOpen(s, app.id, nil)
	}
	if app.id != appAI {
		a.desktopPinMenu(m, app.id)
	}
	if m.Item(locales.DesktopDockCloseAll()).Disabled(len(windows) == 0).Chosen() {
		for _, w := range windows {
			a.desktopRequestClose(w)
		}
	}
}

func (a *App) desktopDockOverflow(c *ui.Context, d *desktopState, s *Session, apps []*desktopApp) {
	sh := &d.shell
	button := desktopDockButton(c, locales.DesktopDockMore())
	if button.Clicked() {
		sh.dockOverflow = !sh.dockOverflow
		sh.launcher, sh.hostPopup, sh.dockPopup = false, false, ""
		c.Invalidate()
	}
	button.Children(func() { ui.Icon(c, icons.Get("ellipsis")).FontSize(18).TextColor(theme.Text) })
	a.desktopDockPopover(c.Key("dock-overflow"), button, &sh.dockOverflow, func() {
		ui.Text(c, locales.DesktopDockMore()).Bold()
		ui.Scroll(c).MaxHeight(250).FillWidth().Children(func() {
			for _, app := range apps {
				windows := d.windowsOf(app.id)
				row := desktopPanelRow(c.Key(app.id), app.icon, app.name(), false)
				row.ContextMenu(func(m *ui.Menu) { a.desktopDockMenu(m, d, s, app, windows) })
				if len(windows) > 1 {
					row.PopoverMenuButton(func(m *ui.Menu) {
						for _, w := range windows {
							if m.Item(w.displayTitle()).Chosen() {
								a.desktopFocus(w)
								sh.dockOverflow = false
							}
						}
					})
				} else if row.Clicked() {
					a.desktopDockActivate(d, s, app, windows)
					c.Invalidate()
				}
			}
		})
	})
}

func (a *App) desktopDockHost(c *ui.Context, d *desktopState, s *Session, width float32) {
	sh := &d.shell
	button := ui.Row(c.Key("dock-host")).Height(36).MaxWidth(width).MinWidth(0).PaddingX(5).Gap(5).Radius(7).
		AlignItems(ui.Center).Role(ui.RoleButton).Label(s.label()).Tooltip(s.label())
	b := button.Bounds()
	sh.hostButton = ui.Point{X: b.X + b.W/2 - sh.root.X, Y: b.Y + b.H/2 - sh.root.Y}
	if button.Hovered() {
		button.Background(theme.Text.Alpha(.09))
	}
	if button.Clicked() {
		sh.hostPopup = !sh.hostPopup
		sh.launcher, sh.dockOverflow, sh.dockPopup = false, false, ""
		c.Invalidate()
	}
	button.Children(func() {
		ui.Icon(c, icons.Get("monitor")).FontSize(15).Shrink(0).TextColor(theme.Text)
		if width > 25 {
			ui.Text(c, s.label()).FontSize(12).SingleLine().MinWidth(0).TextColor(theme.Text)
		}
	})
	a.desktopDockPopover(c.Key("desktop-host"), button, &sh.hostPopup, func() {
		ui.Text(c, s.label()).Bold().FontSize(16).SingleLine().FillWidth()
		address := s.ip
		if address == "" {
			address = s.Host.Addr
		}
		if s.Host.User != "" {
			address = s.Host.User + "@" + address
		}
		ui.Text(c, address).TextColor(theme.TextMuted).SingleLine().FillWidth()
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			status := ui.Hex("#b68a58")
			if s.status == statusConnected {
				status = ui.Hex("#2bb78e")
			}
			ui.Box(c).Size(7, 7).Radius(4).Background(status)
			ui.Text(c, s.statusText()).FontSize(12).TextColor(theme.TextMuted)
		})
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		if desktopPanelRow(c, "terminal", locales.SwitchToTerminalMode(), false).Clicked() {
			sh.hostPopup = false
			a.backToTerminal(a.desktopRowOf(s))
		}
		var others []*Session
		for _, other := range a.sessions {
			if other != s && canOpenDesktop(other) && other.status == statusConnected {
				others = append(others, other)
			}
		}
		row := desktopPanelRow(c, "monitor", locales.DesktopDockSwitch(), false).Disabled(len(others) == 0)
		row.PopoverMenuButton(func(m *ui.Menu) {
			for _, other := range others {
				if m.Item(other.label()).Chosen() {
					sh.hostPopup = false
					a.openDesktop(other)
				}
			}
		})
	})
}

func (a *App) desktopDockPopover(c *ui.Context, anchor ui.Element, open *bool, content func()) {
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.Width(232).Padding(12).Gap(7).Radius(12).Background(theme.Background).Border(1, theme.Border).
			Shadow(0, 8, 25, 0, ui.RGBA(0, 0, 0, .37)).Children(content)
	})
}

func desktopPanelRow(c *ui.Context, icon, label string, active bool) ui.Element {
	row := ui.Row(c).FillWidth().Height(32).Shrink(0).PaddingX(8).Gap(8).Radius(6).AlignItems(ui.Center).Role(ui.RoleButton).Label(label)
	if active || row.Hovered() {
		row.Background(theme.SurfaceHover)
	}
	row.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(15).TextColor(theme.TextMuted).Shrink(0)
		ui.Text(c, label).SingleLine().MinWidth(0).Grow(1)
		if active {
			ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(theme.Accent).Shrink(0)
		}
	})
	return row
}
