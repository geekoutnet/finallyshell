package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type serverUpdatesPage struct {
	opsState
	manager, os, kernel, last, reboot string
	filter                            string
	securityKnown                     bool
	rows                              []monitor.PackageUpdate
	selected                          ui.Selection[string]
	sort                              ui.SortOrder
	table                             ui.ListState
}

func (a *App) refreshServerUpdates(s *Session) {
	page := &s.server.updates
	if page.changed(s) {
		*page = serverUpdatesPage{opsState: page.reset(s)}
	}
	conn, ok := a.opsConnection(s, &page.opsState)
	if !ok || opsBlocked(&page.opsState) != "" {
		return
	}
	page.loaded, page.reading, page.err = true, true, nil
	login := a.passwords[s.Host.ID]
	go func() {
		ctx, cancel := context.WithTimeout(conn.ctx, 40*time.Second)
		defer cancel()
		var diagnostic strings.Builder
		run := func(script string) (string, error) {
			if script == "" {
				return "", nil
			}
			out, stderr, err := conn.normal(ctx, script, nil)
			if err != nil {
				diagnostic.WriteString(stderr + "\n")
			}
			return out, err
		}
		info, err := run(monitor.PackageInfoScript)
		parts := strings.SplitN(info, "\n", 4)
		parts = append(parts, "", "", "")
		manager, release, kernel := strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), `"'`), strings.TrimSpace(parts[2])
		read := run
		if manager == "dnf" || manager == "yum" {
			read = func(script string) (string, error) {
				out, stderr, err := conn.root(ctx, script, nil)
				if err != nil {
					diagnostic.WriteString(stderr + "\n")
				}
				return out, err
			}
		}
		output, listErr := read(monitor.PackageListScript(manager))
		var details string
		var detailsErr error
		if listErr == nil {
			details, detailsErr = read(monitor.PackageDetailsScript(manager))
		}
		last, _ := run(monitor.PackageLastUpdateScript(manager))
		reboot, _ := run(monitor.PackageRebootCheckScript(manager))
		rows := monitor.ParsePackageUpdates(manager, output, details)
		a.post(func() {
			if !conn.current() {
				return
			}
			page.reading, page.err, page.output = false, opsReadError(errors.Join(err, listErr, detailsErr), login), opsSafeOutput(diagnostic.String(), login)
			page.manager, page.os, page.kernel, page.last, page.reboot = manager, release, kernel, strings.TrimSpace(last), strings.TrimSpace(reboot)
			page.securityKnown = monitor.PackageCanUpgrade(manager) && detailsErr == nil && listErr == nil
			page.rows = rows
			for name := range page.selected.All() {
				if !slices.ContainsFunc(rows, func(row monitor.PackageUpdate) bool { return row.Name == name }) {
					page.selected.Remove(name)
				}
			}
		})
	}()
}

func (page *serverUpdatesPage) view() []monitor.PackageUpdate {
	var result []monitor.PackageUpdate
	filter := strings.ToLower(strings.TrimSpace(page.filter))
	for _, entry := range page.rows {
		if strings.Contains(strings.ToLower(entry.Name+" "+entry.Repository), filter) {
			result = append(result, entry)
		}
	}
	slices.SortStableFunc(result, func(left, right monitor.PackageUpdate) int {
		field := func(pkg monitor.PackageUpdate) string {
			switch page.sort.Column {
			case "current":
				return pkg.Current
			case "new":
				return pkg.New
			case "repo":
				return pkg.Repository
			case "security":
				if pkg.Security {
					return "0"
				}
				return "1"
			default:
				return pkg.Name
			}
		}
		order := strings.Compare(field(left), field(right))
		if page.sort.Descending {
			return -order
		}
		return order
	})
	return result
}

func (page *serverUpdatesPage) names(scope string, view []monitor.PackageUpdate) []string {
	var names []string
	for _, row := range view {
		if scope == "security" && row.Security || scope == "selected" && page.selected.Has(row.Name) || scope == "all" {
			name := row.Name
			if page.manager == "apt" && scope == "security" {
				name += "=" + row.New
			}
			names = append(names, name)
		}
	}
	return names
}

func (a *App) serverUpdatesSection(c *ui.Context, s *Session) {
	page := &s.server.updates
	if !page.loaded || page.changed(s) {
		a.refreshServerUpdates(s)
	}
	view := page.view()
	opsToolbar(c, func() {
		ui.SearchField(c, &page.filter).Label(locales.UpdFilter()).Placeholder(locales.UpdFilter()).Width(160).MinWidth(100).Shrink(1)
		refreshWhy := page.block(false)
		if opsButton(c, locales.UpdRefresh(), refreshWhy) {
			a.updatesConfirm(s, "refresh", nil)
		}
		ui.Button(c.Key("upgrade"), locales.UpdUpgrade()).Disabled(page.block(true) != "").Tooltip(page.block(true)).PopoverMenuButton(func(menu *ui.Menu) { a.updatesMenu(menu, s, view, "") })
		if monitor.ParseRebootRequired(page.reboot) && opsButton(c, locales.UpdReboot(), opsBlocked(&page.opsState)) {
			a.updatesConfirm(s, "reboot", nil)
		}
		page.note(c)
	})
	opsToolbar(c, func() {
		for _, value := range []string{page.os, page.kernel, page.manager} {
			if value != "" {
				widgets.Muted(c.Key(value), value).FontSize(12).SingleLine().MaxWidth(220).Tooltip(value)
			}
		}
		if page.last != "" {
			widgets.Muted(c, locales.UpdLast()+": "+page.last).FontSize(12).SingleLine().Tooltip(page.last)
		}
		if page.reboot == "required" {
			ui.Text(c, locales.UpdRebootRequired()).TextColor(theme.Warning).FontSize(12)
		}
		if page.manager != "" && !page.reading && page.err == nil {
			widgets.Muted(c, locales.UpdCount(len(page.rows))).FontSize(12)
			if page.securityKnown {
				count := len(page.names("security", page.rows))
				ui.Text(c, locales.UpdSecurityCount(count)).FontSize(12).TextColor(theme.Warning).Background(theme.Warning.Alpha(.12)).Padding(2, 6).Radius(4)
			}
		}
	})
	if len(view) == 0 && !page.reading {
		message := locales.UpdEmpty()
		if !monitor.PackageManager(page.manager) {
			message = locales.UpdUnsupported()
		}
		opsTextView(c, page.output, message)
	} else {
		a.updateTable(c, s, view)
	}
	a.opsWorkDialog(c, s, &page.opsState)
}

func (page *serverUpdatesPage) block(upgrade bool) string {
	if reason := opsBlocked(&page.opsState); reason != "" {
		return reason
	}
	if !monitor.PackageCanUpgrade(page.manager) {
		return locales.UpdReadOnly()
	}
	if upgrade && len(page.rows) == 0 {
		return locales.UpdEmpty()
	}
	return ""
}

func (a *App) updateTable(c *ui.Context, s *Session, rows []monitor.PackageUpdate) {
	page := &s.server.updates
	page.table.Key = func(index int) any { return rows[index].Name }
	page.table.Label = func(index int) string { return rows[index].Name }
	page.table.Sort, page.table.Selection = &page.sort, &page.selected
	page.table.CellMenu = func(menu *ui.Menu, index, _ int) { a.updatesMenu(menu, s, rows, rows[index].Name) }
	versionWidth := float32(96)
	if a.opsNarrow() {
		versionWidth = 82
	}
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.UpdPackage(), MinWidth: 100, Sortable: true},
		{ID: "current", Title: locales.UpdInstalled(), Width: versionWidth, Sortable: true},
		{ID: "new", Title: locales.UpdCandidate(), Width: versionWidth, Sortable: true},
		{ID: "repo", Title: locales.UpdRepository(), Width: 150, Sortable: true},
	}
	ui.Table(c, &page.table, columns, len(rows), func(index, column int) {
		row := rows[index]
		if column == 0 && row.Security {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(5).Children(func() {
				ui.Text(c, row.Name).SingleLine().Grow(1).MinWidth(0).Tooltip(row.Name)
				ui.Box(c).Size(7, 7).Radius(4).Background(theme.Warning).Tooltip(locales.UpdSecurity()).Shrink(0)
			})
			return
		}
		value := orDash([4]string{row.Name, row.Current, row.New, row.Repository}[column])
		ui.Text(c, value).SingleLine().Tooltip(value)
	}).Grow(1).MinHeight(0).Label(locales.ServerUpdates())
}

func (a *App) updatesMenu(menu *ui.Menu, s *Session, rows []monitor.PackageUpdate, clicked string) {
	page := &s.server.updates
	for _, action := range []struct{ key, title string }{{"all", locales.UpdAll()}, {"security", locales.UpdSecurity()}, {"selected", locales.UpdSelected()}} {
		names := page.names(action.key, rows)
		if action.key != "selected" {
			names = page.names(action.key, page.rows)
		}
		if action.key == "selected" && len(names) == 0 && clicked != "" {
			names = []string{clicked}
		}
		why := page.block(true)
		if action.key == "security" && !page.securityKnown && why == "" {
			why = locales.UpdUnknownSecurity()
		}
		if len(names) == 0 && why == "" {
			why = locales.UpdNoTargets()
		}
		if opsMenuItem(menu, action.title, why) {
			a.updatesConfirm(s, action.key, names)
		}
	}
}

func (a *App) updatesConfirm(s *Session, action string, names []string) {
	page := &s.server.updates
	var title, message, script string
	var err error
	switch action {
	case "refresh":
		title, message = locales.UpdRefresh(), locales.UpdRefreshWarning()
		script, err = monitor.PackageRefreshScript(page.manager)
	case "reboot":
		title, message, script = locales.UpdReboot(), locales.UpdRebootWarning(), monitor.PackageRebootScript
	default:
		title = map[string]string{"all": locales.UpdAll(), "security": locales.UpdSecurity(), "selected": locales.UpdSelected()}[action]
		message = locales.UpdUpgradeWarning() + "\n\n" + strings.Join(names, ", ")
		if action == "all" || action == "security" && page.manager != "apt" {
			names = nil
		}
		script, err = monitor.PackageUpgradeScript(page.manager, action, names)
	}
	if err != nil {
		return
	}
	page.dialog = &opsDialog{title: title, description: message, action: title, build: opsStatic(script), refresh: func() { a.refreshServerUpdates(s) }}
}
