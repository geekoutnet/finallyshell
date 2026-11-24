package app

import (
	"cmp"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const alertWindow WindowKind = "alerts"

var alertBell = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 10a7 7 0 0 1 14 0c0 5 2 6 2 7H3c0-1 2-2 2-7zM10 21h4"/></svg>`))

type alertsState struct {
	engine   alertEngine
	sessions map[*Session]*alertSession
	peers    map[*Session]alertPeer
	events   []alertEvent
	sequence int64
	read     monitor.MemoryRunner
	notify   alertNotifier

	query     string
	unread    bool
	table     ui.ListState
	sort      ui.SortOrder
	selection ui.Selection[any]
	scope     string
}

func alertLabel(kind alertKind) string {
	switch kind {
	case alertUnreachable:
		return locales.AlertUnreachable()
	case alertCPU:
		return locales.AlertCPU()
	case alertMemory:
		return locales.AlertMemory()
	case alertDisk:
		return locales.AlertDisk()
	case alertLoad:
		return locales.AlertLoad()
	case alertService:
		return locales.AlertService()
	}
	return string(kind)
}

func (a *App) alertButton(c *ui.Context) {
	count := 0
	for _, event := range a.alerts.events {
		if !event.acked {
			count++
		}
	}
	if count == 0 {
		return
	}
	label := locales.AlertUnreadCount(count)
	button := ui.Box(c.Key("alert-bell")).Width(23).Height(24).Center().Shrink(0).Radius(5).Role(ui.RoleButton).Label(label).Tooltip(label)
	if button.Hovered() {
		button.Background(theme.Text.Alpha(0.07))
	}
	button.Children(func() {
		ui.Icon(c, alertBell).FontSize(15).TextColor(theme.TextMuted)
		if count > 0 {
			ui.Text(c, locales.AlertBadge(min(count, 100))).Absolute().Top(-2).Right(-3).ZIndex(1).FontSize(9).Bold().PaddingX(3).
				Radius(6).Background(theme.Accent).TextColor(theme.AccentText).AccessHidden(true)
		}
	})
	if button.Clicked() {
		a.openAlerts()
	}
}

func (a *App) openAlerts() {
	a.openWindow(WindowSpec{Kind: alertWindow, Title: locales.AlertTitle, Width: 820, Height: 460, MinWidth: 470, MinHeight: 270}, a.alertList)
}

func (state *alertsState) visible() []alertEvent {
	query := strings.ToLower(strings.TrimSpace(state.query))
	var rows []alertEvent
	for _, event := range slices.Backward(state.events) {
		if state.unread && event.acked || query != "" && !strings.Contains(strings.ToLower(event.host+" "+alertLabel(event.kind)+" "+event.value), query) {
			continue
		}
		rows = append(rows, event)
	}
	slices.SortStableFunc(rows, func(left, right alertEvent) int {
		var order int
		switch state.sort.Column {
		case "host":
			order = strings.Compare(strings.ToLower(left.host), strings.ToLower(right.host))
		case "rule":
			order = strings.Compare(alertLabel(left.kind), alertLabel(right.kind))
		case "state":
			order = cmp.Compare(alertEventState(left), alertEventState(right))
		default:
			order = left.at.Compare(right.at)
		}
		if state.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func alertEventState(event alertEvent) string {
	if event.acked {
		return locales.AlertAcknowledged()
	}
	return locales.AlertNew()
}

func (state *alertsState) acknowledge(ids []int64) {
	for index := range state.events {
		event := &state.events[index]
		if slices.Contains(ids, event.id) {
			event.acked = true
		}
	}
}

func (a *App) alertList(c *ui.Context) {
	state := &a.alerts
	if state.sort.Column == "" {
		state.sort = ui.SortOrder{Column: "time", Descending: true}
	}
	rows := state.visible()
	state.table.Key = func(index int) any { return rows[index].id }
	state.table.Label = func(index int) string { return rows[index].host + " · " + alertLabel(rows[index].kind) }
	state.table.Sort = &state.sort
	state.table.Selection = &state.selection
	state.table.CellMenu = func(menu *ui.Menu, row, _ int) {
		event := rows[row]
		reason := ""
		if event.acked {
			reason = locales.AlertAlreadyAcknowledged()
		}
		if menu.Item(locales.AlertAcknowledge()).Disabled(event.acked).Subtitle(reason).Chosen() && !event.acked {
			state.acknowledge([]int64{event.id})
		}
		if menu.Item(locales.AlertCopyValue()).Chosen() {
			c.WriteClipboard(event.value)
		}
	}
	ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
		ui.Text(c, locales.AlertTitle()).Bold().FontSize(17)
		widgets.Muted(c, locales.AlertWhileRunning()).FontSize(12)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &state.query).Placeholder(locales.AlertFilter()).Label(locales.AlertFilter()).Grow(1).MinWidth(0)
			ui.Checkbox(c, &state.unread, locales.AlertOnlyUnread())
		})
		columns := []ui.TableColumn{
			{ID: "time", Title: locales.AlertTime(), Width: 116, MinWidth: 105, Sortable: true},
			{ID: "host", Title: locales.AlertHost(), Width: 125, MinWidth: 65, Sortable: true},
			{ID: "rule", Title: locales.AlertRule(), Width: 168, MinWidth: 100, Sortable: true},
			{ID: "value", Title: locales.AlertValue(), MinWidth: 75},
			{ID: "state", Title: locales.AlertState(), Width: 122, MinWidth: 85, Sortable: true},
		}
		if width, _ := c.Size(); width <= 650 {
			columns[0].Width, columns[1].Width, columns[2].Width, columns[4].Width = 105, 65, 100, 104
		}
		ui.Table(c, &state.table, columns, len(rows), func(row, col int) {
			event := rows[row]
			text := []string{event.at.Local().Format("01-02 15:04:05"), event.host, alertLabel(event.kind), event.value, alertEventState(event)}[col]
			ui.Text(c, text).SingleLine().Tooltip(text)
		}).Grow(1).MinHeight(0).Label(locales.AlertTitle())
		if len(rows) == 0 {
			widgets.Muted(c, locales.AlertEmpty()).FontSize(12)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			var selected []int64
			for _, event := range rows {
				if !event.acked && state.selection.Has(event.id) {
					selected = append(selected, event.id)
				}
			}
			if ui.Button(c, locales.AlertAcknowledgeSelected()).Disabled(len(selected) == 0).Clicked() {
				state.acknowledge(selected)
			}
			if ui.PrimaryButton(c, locales.Done()).Clicked() {
				a.closeWindow(alertWindow)
			}
		})
	})
}
