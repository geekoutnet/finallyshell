package app

import (
	"maps"
	"slices"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopCenterState struct {
	query, category string
}

var desktopCenterDescriptions = map[desktopAppID]func() string{
	appThisPC:       locales.DesktopCenterThisPC,
	appFiles:        locales.DesktopCenterFiles,
	appTerminal:     locales.DesktopCenterTerminal,
	appTrash:        locales.DesktopCenterTrash,
	appSettings:     locales.DesktopCenterSettings,
	appTaskMgr:      locales.DesktopCenterTaskMgr,
	appBrowser:      locales.DesktopCenterBrowser,
	appAppCenter:    locales.DesktopCenterCenter,
	appAI:           locales.DesktopCenterAI,
	"srv-docker":    locales.DesktopCenterDocker,
	"srv-db":        locales.DesktopCenterDB,
	"srv-sites":     locales.DesktopCenterSites,
	"srv-caddy":     locales.DesktopCenterCaddy,
	"srv-services":  locales.DesktopCenterServices,
	"srv-logs":      locales.DesktopCenterLogs,
	"srv-cron":      locales.DesktopCenterCron,
	"srv-disk":      locales.DesktopCenterDisk,
	"srv-updates":   locales.DesktopCenterUpdates,
	"srv-security":  locales.DesktopCenterSecurity,
	"srv-firewall":  locales.DesktopCenterFirewall,
	"srv-processes": locales.DesktopCenterProcesses,
	"srv-network":   locales.DesktopCenterNetwork,
	"srv-tunnels":   locales.DesktopCenterTunnels,
	"srv-transfers": locales.DesktopCenterTransfers,
	"srv-commands":  locales.DesktopCenterCommands,
	"srv-overview":  locales.DesktopCenterOverview,
}

func desktopCenterDescription(app *desktopApp) string {
	if description := desktopCenterDescriptions[app.id]; description != nil {
		return description()
	}
	return app.name()
}

func (a *App) desktopCenterOpened(w *desktopWindow, _ any) {
	if w.state == nil {
		w.state = &desktopCenterState{}
	}
	desktop := a.desktopOf(w.session)
	for _, open := range desktop.windows {
		if !slices.Contains(w.session.desktopToolsRecent, open.app.id) {
			a.desktopToolsNoteOpened(open)
		}
	}
}

func desktopToolsIcon(c *ui.Context, app *desktopApp, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(size/4).Center().
		Gradient(app.tint, app.tint.Alpha(0.76), 140).Shadow(0, 2, 5, 0, app.tint.Alpha(0.22)).Children(func() {
		ui.Icon(c, icons.Get(app.icon)).FontSize(size * 0.53).TextColor(ui.RGB(255, 255, 255))
	})
}

func (a *App) desktopCenterView(c *ui.Context, w *desktopWindow) {
	state := w.state.(*desktopCenterState)
	s := w.session
	ui.Column(c).Fill().MinHeight(0).Background(colors.overviewPage).Children(func() {
		a.desktopCenterHeader(c, w, state)
		ui.Scroll(c.Key(state.query + "|" + state.category)).Grow(1).MinHeight(0).FillWidth().Children(func() {
			content := ui.Column(c).FillWidth().Padding(13).Gap(12)
			width := content.LayoutBounds().W
			if width <= 0 {
				width = w.w
			}
			content.Children(func() {
				apps := desktopCenterMatches(s, state)
				ui.Text(c, locales.DesktopCenterCount(len(apps))).Bold().FontSize(13).TextColor(theme.TextMuted)
				if len(apps) == 0 {
					ui.Text(c, locales.DesktopCenterEmpty()).Padding(12).TextColor(theme.TextMuted)
					return
				}
				if state.query == "" && state.category == "" && len(s.desktopToolsRecent) != 0 && width < 490 {
					a.desktopCenterRecent(c, s)
				}
				a.desktopCenterGrid(c, s, apps, width)
			})
		})
	})
}

func desktopCenterMatches(s *Session, state *desktopCenterState) []*desktopApp {
	query := strings.Fields(strings.ToLower(state.query))
	return slices.DeleteFunc(desktopAppsFor(s), func(app *desktopApp) bool {
		if state.category != "" && state.category != app.category {
			return true
		}
		text := strings.ToLower(app.name() + " " + desktopCenterDescription(app) + " " + string(app.id))
		return slices.ContainsFunc(query, func(word string) bool { return !strings.Contains(text, word) })
	})
}

func (a *App) desktopCenterHeader(c *ui.Context, w *desktopWindow, state *desktopCenterState) {
	header := ui.Column(c).FillWidth().Shrink(0).Padding(12, 14).Gap(9).Background(theme.Background).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border)
	width := header.LayoutBounds().W
	if width == 0 {
		width = w.w
	}
	header.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			desktopToolsIcon(c, w.app, 38)
			ui.Column(c).MinWidth(0).Grow(1).Gap(2).Children(func() {
				ui.Text(c, locales.DesktopCenterHeading()).FontSize(17).Bold()
				ui.Text(c, locales.DesktopCenterSubtitle()).TextColor(theme.TextMuted).FontSize(12)
			})
		})
		ui.SearchField(c, &state.query).FillWidth().Placeholder(locales.DesktopCenterSearch()).Label(locales.DesktopCenterSearch())
		ui.Row(c).Gap(6).Wrap().Children(func() {
			for _, category := range []struct{ id, name string }{
				{"", locales.DesktopCenterAll()}, {desktopCatSystem, locales.DesktopCenterSystem()},
				{desktopCatTools, locales.DesktopCenterTools()}, {desktopCatServer, locales.DesktopCenterServer()},
			} {
				selected := state.category == category.id
				button := ui.Button(c.Key(category.id), category.name).Height(26).Radius(12).Checked(selected)
				if selected {
					button.Background(theme.Accent).TextColor(ui.RGB(255, 255, 255))
				}
				if button.Clicked() {
					state.category = category.id
				}
			}
		})
		if state.query == "" && state.category == "" && len(w.session.desktopToolsRecent) != 0 && width >= 490 {
			a.desktopCenterRecent(c, w.session)
		}
	})
}

func (a *App) desktopCenterRecent(c *ui.Context, s *Session) {
	ui.Text(c, locales.DesktopCenterRecent()).Bold().FontSize(13).TextColor(theme.TextMuted)
	ui.ScrollHorizontal(c).Gap(6).FillWidth().Height(30).Scrollbars(false).Children(func() {
		for _, id := range slices.Clone(s.desktopToolsRecent) {
			app := desktopAppByID(id)
			if app == nil || app.available != nil && !app.available(s) {
				continue
			}
			button := ui.Button(c.Key(string(id)), "").Label(app.name()).Radius(14).Height(27).Shrink(0).PaddingX(8)
			button.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.Get(app.icon)).FontSize(14).TextColor(app.tint)
					ui.Text(c, app.name()).FontSize(12).SingleLine()
				})
			})
			if button.Clicked() {
				a.desktopCenterOpen(s, app)
			}
		}
	})
}

func (a *App) desktopCenterGrid(c *ui.Context, s *Session, apps []*desktopApp, width float32) {
	columns := max(1, int((width-20)/244))
	for row := range slices.Chunk(apps, columns) {
		ui.Row(c.Key(string(row[0].id))).Gap(9).FillWidth().Children(func() {
			for _, app := range row {
				a.desktopCenterCard(c.Key(string(app.id)), s, app)
			}
			for range columns - len(row) {
				ui.Box(c).Basis(0).Grow(1).MinWidth(0)
			}
		})
	}
}

func (a *App) desktopCenterCard(c *ui.Context, s *Session, app *desktopApp) {
	menu := func(menu *ui.Menu) { a.desktopCenterMenu(menu, s, app) }
	card := ui.Column(c).Basis(0).Grow(1).MinWidth(0).Padding(10).Gap(8).Radius(10).Background(theme.Background).Border(1, theme.Border)
	card.ContextMenu(menu)
	card.Children(func() {
		ui.Row(c).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
			desktopToolsIcon(c, app, 39)
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, app.name()).FontSize(14).Bold().SingleLine().Ellipsis("…").Tooltip(app.name())
				description := desktopCenterDescription(app)
				ui.Text(c, description).FontSize(12).TextColor(theme.TextMuted).SingleLine().Ellipsis("…").Tooltip(description)
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			if ui.Button(c, locales.OpenFile()).Height(26).Clicked() {
				a.desktopCenterOpen(s, app)
			}
			widgets.IconButton(c, "ellipsis", locales.DesktopCenterActions()).PopoverMenuButton(menu)
			ui.Box(c).Grow(1)
			if a.desktopCenterPinned(app) {
				ui.Icon(c, icons.Get("pin")).TextColor(theme.AccentText).FontSize(13).Tooltip(locales.DesktopCenterPinned())
			}
		})
	})
}

func (a *App) desktopCenterOpen(s *Session, app *desktopApp) {
	if w := a.desktopOpen(s, app.id, nil); w != nil {
		a.desktopToolsNoteOpened(w)
	}
}

func (a *App) desktopCenterMenu(menu *ui.Menu, s *Session, app *desktopApp) {
	if menu.Item(locales.OpenFile()).Chosen() {
		a.desktopCenterOpen(s, app)
	}
	menu.Separator()
	label := locales.DesktopCenterAdd()
	if a.desktopCenterOnDesktop(s, app) {
		label = locales.DesktopCenterRemove()
	}
	if menu.Item(label).Chosen() {
		a.desktopCenterToggleOnDesktop(s, app)
	}
	label = locales.DesktopCenterPin()
	if a.desktopCenterPinned(app) {
		label = locales.DesktopCenterUnpin()
	}
	if menu.Item(label).Chosen() {
		a.desktopCenterTogglePinned(app)
	}
}

func (a *App) desktopCenterPreferences() config.DesktopSettings {
	if settings := a.settings.Desktop; settings != nil {
		return *settings
	}
	return config.DesktopSettings{}
}

func (a *App) desktopCenterPinned(app *desktopApp) bool {
	settings := a.desktopCenterPreferences()
	if settings.Pinned == nil {
		return app.pinned
	}
	return slices.Contains(*settings.Pinned, string(app.id))
}

func (a *App) desktopCenterTogglePinned(app *desktopApp) {
	settings := a.desktopCenterPreferences()
	var pinned []string
	if settings.Pinned != nil {
		pinned = append([]string{}, (*settings.Pinned)...)
	} else {
		for _, candidate := range desktopApps {
			if candidate.pinned {
				pinned = append(pinned, string(candidate.id))
			}
		}
	}
	if index := slices.Index(pinned, string(app.id)); index >= 0 {
		pinned = slices.Delete(pinned, index, index+1)
	} else {
		pinned = append(pinned, string(app.id))
	}
	settings.Pinned = &pinned
	a.changeSettings(func() { a.settings.Desktop = &settings })
}

func (a *App) desktopCenterOnDesktop(s *Session, app *desktopApp) bool {
	icons := a.desktopCenterPreferences().Hosts[s.Host.ID].Icons
	if icons == nil {
		return app.onDesktop
	}
	return slices.ContainsFunc(icons, func(icon config.DesktopIcon) bool { return icon.App == string(app.id) && icon.Folder == "" })
}

func (a *App) desktopCenterToggleOnDesktop(s *Session, app *desktopApp) {
	settings := a.desktopCenterPreferences()
	settings.Hosts = maps.Clone(settings.Hosts)
	if settings.Hosts == nil {
		settings.Hosts = make(map[string]config.DesktopHost)
	}
	host := settings.Hosts[s.Host.ID]
	icons := slices.Clone(host.Icons)
	if icons == nil {
		for _, candidate := range desktopAppsFor(s) {
			if candidate.onDesktop {
				icons = append(icons, config.DesktopIcon{App: string(candidate.id), Col: -1, Row: -1})
			}
		}
	}
	if a.desktopCenterOnDesktop(s, app) {
		icons = slices.DeleteFunc(icons, func(icon config.DesktopIcon) bool { return icon.App == string(app.id) && icon.Folder == "" })
	} else if index := slices.IndexFunc(icons, func(icon config.DesktopIcon) bool { return icon.App == string(app.id) }); index >= 0 {
		icons[index] = config.DesktopIcon{App: string(app.id), Col: -1, Row: -1}
	} else {
		icons = slices.DeleteFunc(icons, func(icon config.DesktopIcon) bool { return icon.App == "" })
		icons = append(icons, config.DesktopIcon{App: string(app.id), Col: -1, Row: -1})
	}
	if len(icons) == 0 {
		icons = []config.DesktopIcon{{App: "", Col: -1, Row: -1}}
	}
	host.Icons = icons
	settings.Hosts[s.Host.ID] = host
	a.changeSettings(func() { a.settings.Desktop = &settings })
}
