package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopSetLogState struct {
	tab        int
	app        desktopAppID
	search     string
	load       desktopSetLoad
	logins     []monitor.SysConfLogin
	loginErr   string
	failedLoad bool
	exporting  bool
	exportErr  string
}

func (a *App) desktopSetLog(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	page := &state.log
	ui.Segmented(c, &page.tab, locales.DesktopSetLog(), locales.DesktopSetLogins()).AlignSelf(ui.Start)
	if page.tab == 1 {
		a.desktopSetLogins(c.Key("logins"), window, state)
		return
	}
	a.desktopSetOperations(c.Key("operations"), window, state)
}

func desktopSetLogApp(id desktopAppID) string {
	if app := desktopAppByID(id); app != nil {
		return app.name()
	}
	return string(id)
}

func desktopSetLogEntries(log []desktopLogEntry, app desktopAppID, search string) []desktopLogEntry {
	var entries []desktopLogEntry
	search = strings.ToLower(strings.TrimSpace(search))
	for _, entry := range slices.Backward(log) {
		if app != "" && entry.app != app {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.text+"\n"+entry.err+"\n"+desktopSetLogApp(entry.app)), search) {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func (a *App) desktopSetOperations(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	page, desktop := &state.log, a.desktopOf(window.session)
	entries := desktopSetLogEntries(desktop.log, page.app, page.search)
	desktopSetCard(c).ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.DesktopSetExport()).Disabled(len(entries) == 0 || page.exporting).Chosen() {
			a.desktopSetExport(page, entries)
		}
		if menu.Item(locales.DesktopSetClear()).Disabled(len(desktop.log) == 0).Chosen() {
			state.clear = true
		}
	}).Children(func() {
		ui.TextInput(c, &page.search).FillWidth().Label(locales.DesktopSetFilterLog()).Placeholder(locales.DesktopSetFilterLog())
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			label := locales.DesktopSetAllApps()
			if page.app != "" {
				label = desktopSetLogApp(page.app)
			}
			ui.Button(c, label+" ▾").PopoverMenuButton(func(menu *ui.Menu) {
				if menu.Item(locales.DesktopSetAllApps()).Radio(page.app == "").Chosen() {
					page.app = ""
				}
				var apps []desktopAppID
				for _, entry := range desktop.log {
					if !slices.Contains(apps, entry.app) {
						apps = append(apps, entry.app)
					}
				}
				slices.Sort(apps)
				for _, app := range apps {
					if menu.Item(desktopSetLogApp(app)).Radio(page.app == app).Chosen() {
						page.app = app
					}
				}
			})
			if ui.Button(c, locales.DesktopSetExport()).Disabled(len(entries) == 0 || page.exporting).Clicked() {
				a.desktopSetExport(page, entries)
			}
			if ui.Button(c, locales.DesktopSetClear()).Disabled(len(desktop.log) == 0).Clicked() {
				state.clear = true
			}
			if page.exporting {
				ui.Spinner(c).Label(locales.DesktopSetExport())
			}
		})
		if page.exportErr != "" {
			ui.Text(c, page.exportErr).TextColor(theme.Danger)
		}
	})
	if len(entries) == 0 {
		widgets.Muted(c, locales.DesktopSetNoOperations()).Padding(6, 3)
	}
	for index, entry := range entries {
		card := desktopSetCard(c.Key(index))
		if entry.err != "" {
			card.Background(colors.dangerBanner)
		}
		card.Children(func() {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				ui.Text(c, desktopSetLogApp(entry.app)).Bold()
				widgets.Muted(c, entry.at.Local().Format("2006-01-02 15:04:05")).FontSize(11)
			})
			ui.Text(c, entry.text).FillWidth().Selectable()
			if entry.err != "" {
				ui.Text(c, entry.err).TextColor(theme.Danger).FillWidth().Selectable()
			}
		})
	}
}

func (a *App) desktopSetExport(page *desktopSetLogState, entries []desktopLogEntry) {
	page.exporting, page.exportErr = true, ""
	title := locales.DesktopSetExport()
	var text strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&text, "%s  [%s]  %s\n", entry.at.Format("2006-01-02 15:04:05 -0700"), desktopSetLogApp(entry.app), entry.text)
		if entry.err != "" {
			fmt.Fprintln(&text, entry.err)
		}
	}
	go func() {
		file, err := a.plat.SaveFile(WindowMain, title, "desktop-operations.log", title, []string{"log", "txt"})
		if err == nil && file != "" {
			var handle *os.File
			handle, err = os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				_, err = io.WriteString(handle, text.String())
				err = errors.Join(err, handle.Close())
			}
		}
		a.post(func() {
			page.exporting = false
			if err != nil {
				page.exportErr = locales.ErrSaveFailed(err.Error())
			}
		})
	}()
}

func (a *App) desktopSetLogins(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	if !state.online {
		desktopSetOffline(c, window.session)
		return
	}
	page := &state.log
	if !page.load.loaded {
		a.desktopSetReadLogins(window, state, false)
	}
	desktopSetCard(c).Children(func() {
		desktopSetHeading(c, "list", locales.DesktopSetLogins())
		widgets.Muted(c, locales.DesktopSetLoginsNote()).FillWidth()
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c, locales.ServerRefresh()).Disabled(page.load.cancel != nil).Clicked() {
				a.desktopSetReadLogins(window, state, page.failedLoad)
			}
			if ui.Button(c, locales.DesktopSetLoadFailed()).Disabled(page.load.cancel != nil || page.failedLoad).Clicked() {
				a.desktopSetReadLogins(window, state, true)
			}
		})
		desktopSetReadNote(c, &page.load)
		if page.loginErr != "" {
			ui.Text(c, page.loginErr).TextColor(theme.Danger).Selectable()
		}
	})
	if page.load.loaded && len(page.logins) == 0 {
		widgets.Muted(c, locales.DesktopSetNoLogins()).Padding(6, 3)
	}
	for index, login := range page.logins {
		card := desktopSetCard(c.Key(index))
		if login.Failed {
			card.Background(colors.dangerBanner)
		}
		card.Children(func() {
			ui.Row(c).Gap(7).Wrap().Children(func() {
				ui.Text(c, login.User).Bold()
				widgets.Muted(c, login.Terminal)
				if login.From != "" {
					widgets.Muted(c, login.From).Selectable()
				}
				if login.Failed {
					ui.Text(c, locales.DesktopSetFailedLogin()).TextColor(theme.Danger)
				}
			})
			widgets.Muted(c, login.When).FillWidth().Selectable()
		})
	}
}

func (a *App) desktopSetReadLogins(window *desktopWindow, state *desktopSettingsState, failed bool) {
	page, session := &state.log, window.session
	read := systemReader(session)
	var root systemRunner
	if failed {
		root = a.desktopSetRoot(session)
	}
	type result struct {
		logins []monitor.SysConfLogin
		stderr string
		err    error
	}
	desktopSetRead(a, window, state, &page.load, func(ctx context.Context) (result, string, error) {
		output, stderr, err := read(ctx, monitor.SysConfLastCommand, nil, nil)
		if err != nil {
			return result{}, stderr, err
		}
		value := result{logins: monitor.ParseSysConfLogins(output, false)}
		if root != nil {
			output, value.stderr, value.err = root(ctx, monitor.SysConfLastbCommand, nil, nil)
			value.logins = append(value.logins, monitor.ParseSysConfLogins(output, true)...)
		}
		return value, "", nil
	}, func(value result) {
		page.logins, page.loginErr = value.logins, systemError(value.stderr, value.err)
		page.failedLoad = failed && value.err == nil
	})
}
