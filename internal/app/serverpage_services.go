package app

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type serverServicesPage struct {
	run             systemRunner
	poll            sessionPoll
	rows            []monitor.ServiceUnit
	loaded, systemd bool
	client          *ssh.Client
	err             error
	filter          string
	state           int
	table           ui.ListState
	row             int
	selected        ui.Selection[string]
	sort            ui.SortOrder
	dialog          *systemDialog
}

func (a *App) refreshServerServices(s *Session) { s.server.services.poll.pollNow() }

func (a *App) serverServicesSection(c *ui.Context, s *Session) {
	box := ui.Column(c.Key("services-layout")).Grow(1).MinHeight(0).FillWidth()
	width := box.LayoutBounds().W
	box.Children(func() { a.serviceView(c, s, width > 0 && width < 785) })
}

func (a *App) serviceView(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.services
	if page.client != s.client {
		page.client, page.loaded, page.rows, page.err = s.client, false, nil, nil
		page.selected.Clear()
	}
	read := systemReader(s)
	if s.client != nil || page.run != nil {
		a.keepPolling(&page.poll, s, 20*time.Second, func() pollRead {
			return func(ctx context.Context, _ *ssh.Client) func() {
				ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				rows, systemd, err := monitor.ReadServices(ctx, func(ctx context.Context, script string) (string, string, error) {
					return read(ctx, script, nil, nil)
				})
				return func() { page.rows, page.loaded, page.systemd, page.err = rows, true, systemd, err }
			}
		})
	}
	rows := page.shown()
	failed := 0
	for _, row := range page.rows {
		if row.Active == "failed" {
			failed++
		}
	}
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &page.filter).Placeholder(locales.SvcFilter()).Label(locales.SvcFilter()).Width(180).MinWidth(90).Shrink(1)
		systemSelect(c, &page.state, locales.SvcState(), []string{locales.SvcAll(), locales.SvcRunning(), locales.SvcFailed(), locales.SvcInactive(), locales.SvcEnabled()})
		badge := ui.Text(c, locales.SvcFailedCount(failed)).FontSize(12).Padding(3, 7).Radius(5)
		if failed > 0 {
			badge.TextColor(theme.Danger).Background(theme.Danger.Alpha(.1))
		}
		picked := page.picked(rows)
		if !narrow && ui.Button(c, locales.SvcDetails()).Disabled(!page.systemd || len(picked) != 1).Clicked() {
			a.serviceDetails(s, picked[0].Name)
		}
		ui.MenuButton(c, locales.SvcActions(), func(menu *ui.Menu) { a.serviceMenu(menu, s, picked) })
	})
	switch {
	case !page.loaded:
		ui.Text(c, locales.SvcReading()).Padding(6, 8).TextColor(theme.TextMuted)
	case page.err != nil:
		message := locales.SvcReadFailed(systemRedact(page.err.Error()))
		if !page.systemd && len(page.rows) == 0 {
			message = locales.SvcUnavailable()
		}
		ui.Text(c, message).SingleLine().Tooltip(message).Padding(4, 8).TextColor(theme.Warning)
	case !page.systemd:
		ui.Text(c, locales.SvcLegacy()).SingleLine().Padding(4, 8).TextColor(theme.TextMuted)
	}
	if page.loaded && len(rows) == 0 && page.err == nil {
		ui.Text(c, locales.SvcEmpty()).Padding(6, 8).TextColor(theme.TextMuted)
	}
	page.table.Key = func(index int) any { return rows[index].Name }
	page.table.Label = func(index int) string { return rows[index].Name }
	page.table.Sort, page.table.Selection, page.table.KeepOffset = &page.sort, &page.selected, true
	page.table.Selected = &page.row
	page.table.CellMenu = func(menu *ui.Menu, index, _ int) {
		row := rows[index]
		if !page.selected.Has(row.Name) {
			page.selected.Clear()
			page.selected.Add(row.Name)
		}
		a.serviceMenu(menu, s, page.picked(rows))
	}
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.SvcName(), Width: 175, Sortable: true},
		{ID: "state", Title: locales.SvcState(), Width: 130, Sortable: true},
		{ID: "enabled", Title: locales.SvcStartup(), Width: 105, Sortable: true},
		{ID: "description", Title: locales.SvcDescription(), MinWidth: 180, Sortable: true},
		{ID: "load", Title: locales.SvcLoad(), Width: 90, Sortable: true},
		{ID: "pid", Title: locales.ColPID(), Width: 70, Align: ui.End, Sortable: true},
		{ID: "memory", Title: locales.SvcMemory(), Width: 95, Align: ui.End, Sortable: true},
	}
	if narrow {
		columns = columns[:3]
		columns[0].Width, columns[0].MinWidth = 0, 100
		columns[1].Width, columns[2].Width = 125, 100
	}
	table := ui.Table(c.Key(narrow), &page.table, columns, len(rows), func(index, column int) {
		row := rows[index]
		state := row.Sub
		if row.Active != "" {
			state = row.Active + " / " + row.Sub
		}
		pid, memory := "", ""
		if row.PID > 0 {
			pid = strconv.FormatInt(row.PID, 10)
		}
		if row.Memory >= 0 {
			memory = formatSize(row.Memory)
		}
		text := orDash([]string{row.Name, state, row.Enabled, systemRedact(row.Description), row.Load, pid, memory}[column])
		tip := text
		if narrow && column == 0 {
			tip = strings.Join([]string{text, systemRedact(row.Description), locales.SvcLoad() + ": " + orDash(row.Load), locales.ColPID() + ": " + orDash(pid), locales.SvcMemory() + ": " + orDash(memory)}, "\n")
		}
		cell := ui.Text(c, text).SingleLine().Tooltip(strings.TrimSpace(tip)).MinWidth(0).Shrink(1)
		if column == 1 {
			switch {
			case row.Active == "failed":
				cell.TextColor(theme.Danger)
			case row.Sub == "running":
				cell.TextColor(theme.Success)
			case row.Sub == "dead" || row.Sub == "exited":
				cell.TextColor(theme.TextMuted)
			}
		}
	}).Grow(1).MinHeight(0).Label(locales.ServerServices())
	if table.Submitted() {
		picked := page.picked(rows)
		if page.systemd && len(picked) == 1 {
			a.serviceDetails(s, picked[0].Name)
		}
	}
	a.systemDialogView(c.Key("service-dialog"), s, &page.dialog)
}

func (page *serverServicesPage) shown() []monitor.ServiceUnit {
	query := strings.ToLower(strings.TrimSpace(page.filter))
	var rows []monitor.ServiceUnit
	for _, row := range page.rows {
		if !strings.Contains(strings.ToLower(row.Name+" "+row.Description), query) {
			continue
		}
		match := [...]bool{true, row.Sub == "running", row.Active == "failed", row.Active == "inactive" || row.Sub == "dead", strings.HasPrefix(row.Enabled, "enabled")}
		if match[page.state] {
			rows = append(rows, row)
		}
	}
	slices.SortStableFunc(rows, func(left, right monitor.ServiceUnit) int {
		order := 0
		switch page.sort.Column {
		case "state":
			order = cmp.Compare(left.Active+left.Sub, right.Active+right.Sub)
		case "enabled":
			order = cmp.Compare(left.Enabled, right.Enabled)
		case "description":
			order = cmp.Compare(left.Description, right.Description)
		case "load":
			order = cmp.Compare(left.Load, right.Load)
		case "pid":
			order = cmp.Compare(left.PID, right.PID)
		case "memory":
			order = cmp.Compare(left.Memory, right.Memory)
		}
		if order == 0 {
			order = cmp.Compare(left.Name, right.Name)
		}
		if page.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func (page *serverServicesPage) picked(rows []monitor.ServiceUnit) []monitor.ServiceUnit {
	var out []monitor.ServiceUnit
	for _, row := range rows {
		if page.selected.Has(row.Name) {
			out = append(out, row)
		}
	}
	return out
}
