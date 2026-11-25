package app

import (
	"slices"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) desktopToggleLauncher(d *desktopState) {
	sh := &d.shell
	sh.launcher = !sh.launcher
	sh.launcherFocus = sh.launcher
	sh.query = ""
	sh.dockPopup, sh.hostPopup, sh.dockOverflow = "", false, false
}

func (a *App) desktopLauncher(c *ui.Context, d *desktopState, s *Session) {
	sh := &d.shell
	if !sh.launcher {
		return
	}
	width := min(float32(486), d.area.W-24)
	height := min(float32(460), d.area.H-desktopDockHeight-16)
	if width <= 0 || height <= 0 {
		return
	}
	panel := ui.Column(c.Key("launcher")).Absolute().Left((d.area.W-width)/2).Bottom(desktopDockHeight+4).Size(width, height).ZIndex(6).
		Padding(14).Gap(12).Radius(16).Border(1, theme.Border).Shadow(0, 12, 36, 0, ui.RGBA(0, 0, 0, .42)).Background(theme.Background).
		Label(locales.DesktopShellLauncher())
	if panel.PressedOutside() && !desktopContains(sh.launcherButton, sh.pointer) {
		sh.launcher = false
		c.Invalidate()
	}
	panel.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			desktopTile(c, "layout-grid", ui.Hex("#6354e8"), 26, false)
			ui.Text(c, locales.DesktopShellLauncher()).FontSize(17).Bold().MinWidth(0).Grow(1).SingleLine()
			if desktopFrameButton(c, "x", locales.Close()).Clicked() {
				sh.launcher = false
				c.Invalidate()
			}
		})
		query := ui.SearchField(c, &sh.query).FillWidth().Shrink(0).Placeholder(locales.DesktopShellSearch()).Label(locales.DesktopShellSearch())
		if sh.launcherFocus {
			query.Focus()
			sh.launcherFocus = false
		}
		apps := desktopAppsFor(s)
		if term := strings.ToLower(strings.TrimSpace(sh.query)); term != "" {
			apps = slices.DeleteFunc(apps, func(app *desktopApp) bool { return !strings.Contains(strings.ToLower(app.name()), term) })
		}
		if query.Submitted() && len(apps) == 1 {
			a.desktopOpen(s, apps[0].id, nil)
			sh.launcher = false
			c.Invalidate()
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if len(apps) == 0 {
				ui.Text(c, locales.DesktopShellNoApps()).TextColor(theme.TextMuted).PaddingY(12)
				return
			}
			for _, group := range []struct {
				id   string
				name func() string
			}{
				{desktopCatSystem, locales.DesktopShellSystem}, {desktopCatTools, locales.DesktopShellTools}, {desktopCatServer, locales.DesktopShellServer},
			} {
				if !slices.ContainsFunc(apps, func(app *desktopApp) bool { return app.category == group.id }) {
					continue
				}
				ui.Text(c, group.name()).FontSize(11).Bold().TextColor(theme.TextMuted).Margin(7, 3, 3, 3).Shrink(0)
				ui.Row(c.Key(group.id)).Wrap().FillWidth().Gap(5).Children(func() {
					for _, app := range apps {
						if app.category != group.id {
							continue
						}
						a.desktopLauncherApp(c, d, s, app, max(84, (width-52)/4))
					}
				})
			}
		})
	})
}

func (a *App) desktopLauncherApp(c *ui.Context, d *desktopState, s *Session, app *desktopApp, width float32) {
	label := app.name()
	tile := ui.Column(c.Key(app.id)).Size(width, 70).Shrink(0).Center().Gap(6).Radius(8).Role(ui.RoleButton).Label(label).Tooltip(label)
	if tile.Hovered() {
		tile.Background(theme.SurfaceHover)
	}
	if tile.Clicked() {
		a.desktopOpen(s, app.id, nil)
		d.shell.launcher = false
		c.Invalidate()
	}
	tile.ContextMenu(func(m *ui.Menu) {
		if app.id != appAI {
			a.desktopPinMenu(m, app.id)
		}
		h := a.desktopHost(s)
		onDesktop := slices.ContainsFunc(a.desktopHostIcons(s, h), func(i config.DesktopIcon) bool { return i.App == string(app.id) && i.Folder == "" })
		if m.Item(locales.DesktopShellAddIcon()).Disabled(onDesktop).Chosen() {
			a.desktopAddIcon(s, string(app.id))
		}
	})
	tile.Children(func() {
		desktopTile(c, app.icon, app.tint, 34, false)
		ui.Text(c, label).FontSize(11).SingleLine().MaxWidth(width - 8)
	})
}
