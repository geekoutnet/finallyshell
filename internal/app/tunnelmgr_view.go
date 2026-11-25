package app

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) tunnelManagerButton(c *ui.Context) {
	if widgets.IconButton(c, "network", locales.TmgrTitle()).Shrink(0).Clicked() {
		a.OpenTunnelManager()
	}
}

func tunnelStateLabel(state tunnelViewState) string {
	switch state {
	case tunnelViewStopped:
		return locales.TunnelStopped()
	case tunnelViewConnecting:
		return locales.TunnelStarting()
	case tunnelViewListening:
		return locales.TunnelListening()
	case tunnelViewRetry:
		return locales.TmgrReconnecting()
	case tunnelViewError:
		return locales.TmgrError()
	default:
		return locales.TmgrAnyState()
	}
}

func (a *App) tunnelManagerWindow(c *ui.Context) {
	view := &a.tmgr.view
	all := a.tunnelManagerRows()
	rows := view.filtered(all)
	visible := make(map[tunnelKey]bool, len(rows))
	for _, row := range rows {
		visible[row.key] = true
	}
	for key := range view.selection.All() {
		if !visible[key] {
			view.selection.Remove(key)
		}
	}
	ui.Column(c).Fill().Padding(12).Gap(9).Background(theme.Background).Children(func() {
		a.tunnelManagerFilters(c)
		a.tunnelManagerToolbar(c, rows, all)
		if view.err != nil {
			message := locales.ErrSaveFailed(view.err.Error())
			if view.err == errBulkTargetChanged {
				message = locales.BulkTargetChanged()
			}
			ui.Text(c, message).TextColor(theme.Danger).MaxLines(2).Tooltip(message)
		}
		a.tunnelManagerTable(c, rows)
		a.tunnelManagerFooter(c, all)
	})
	a.tunnelManagerDialogs(c)
}

func (a *App) tunnelManagerFilters(c *ui.Context) {
	view := &a.tmgr.view
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &view.query).Placeholder(locales.TmgrSearch()).Label(locales.TmgrSearch()).Width(240).Grow(1)
		hosts := a.hosts.Live()
		hosts = slices.DeleteFunc(hosts, func(host *config.Host) bool { return !tunnelManagerHost(host) })
		hostOptions := []string{locales.TmgrAnyHost()}
		for _, host := range hosts {
			hostOptions = append(hostOptions, tunnelHostLabel(host))
		}
		hostIndex := 1 + slices.IndexFunc(hosts, func(host *config.Host) bool { return host.ID == view.hostID })
		hostLabel := hostOptions[hostIndex]
		if ui.Select(c.Key("host"), &hostLabel, hostOptions).Label(locales.TmgrHost()).Width(150).Changed() {
			view.hostID = ""
			if chosen := slices.Index(hostOptions, hostLabel); chosen > 0 {
				view.hostID = hosts[chosen-1].ID
			}
		}
		kinds := slices.Clone(config.TunnelKinds)
		kindOptions := []string{locales.TmgrAnyKind()}
		for _, kind := range kinds {
			kindOptions = append(kindOptions, tunnelManagerKind(kind))
		}
		kindLabel := kindOptions[1+slices.Index(kinds, view.kind)]
		if ui.Select(c.Key("kind"), &kindLabel, kindOptions).Label(locales.ColTunnelType()).Width(145).Changed() {
			view.kind = ""
			if chosen := slices.Index(kindOptions, kindLabel); chosen > 0 {
				view.kind = kinds[chosen-1]
			}
		}
		states := []string{locales.TmgrAnyState(), locales.TunnelStopped(), locales.TunnelStarting(), locales.TunnelListening(), locales.TmgrReconnecting(), locales.TmgrError()}
		stateLabel := states[view.state]
		if ui.Select(c.Key("state"), &stateLabel, states).Label(locales.ColTunnelStatus()).Width(130).Changed() {
			view.state = tunnelViewState(max(0, slices.Index(states, stateLabel)))
		}
	})
}

func tunnelHostLabel(host *config.Host) string {
	if host.Title() == host.Addr {
		return host.Title()
	}
	return host.Title() + " · " + host.Target()
}

func (a *App) tunnelManagerToolbar(c *ui.Context, rows, all []tunnelManagerRow) {
	view := &a.tmgr.view
	selected := tunnelSelection(rows, &view.selection)
	background := slices.DeleteFunc(slices.Clone(all), func(row tunnelManagerRow) bool { return !row.spec.Background })
	ui.Row(c).Gap(7).AlignItems(ui.Center).Wrap().Children(func() {
		button := func(action tunnelAction, label string, targets []tunnelManagerRow) {
			reason := tunnelActionReason(action, targets)
			if ui.Button(c.Key(label), label).Disabled(reason != "").Tooltip(reason).Clicked() {
				a.askTunnelManager(action, targets)
			}
		}
		button(tunnelActionStart, locales.TunnelStart(), selected)
		button(tunnelActionStop, locales.TunnelStop(), selected)
		button(tunnelActionRestart, locales.TmgrRestart(), selected)
		button(tunnelActionStartBackground, locales.TmgrStartAll(), background)
		button(tunnelActionStop, locales.TmgrStopAll(), all)
		if ui.Button(c, locales.TunnelNew()).Disabled(!slices.ContainsFunc(a.hosts.Live(), tunnelManagerHost)).Clicked() {
			hostID := view.hostID
			if len(selected) == 1 {
				hostID = selected[0].key.host
			}
			if tunnelManagerHost(a.hosts.Host(hostID)) {
				a.editTunnelFromManager(hostID, "")
			} else {
				view.chooseHost = true
			}
		}
		reason := ""
		if len(selected) != 1 {
			reason = locales.TmgrSelectOne()
		}
		if ui.Button(c, locales.Edit()).Disabled(reason != "").Tooltip(reason).Clicked() && len(selected) == 1 {
			a.editTunnelFromManager(selected[0].key.host, selected[0].key.id)
		}
	})
}

func tunnelActionReason(action tunnelAction, rows []tunnelManagerRow) string {
	if len(rows) == 0 {
		return locales.TmgrSelectRows()
	}
	for _, row := range rows {
		if row.available(action) == "" {
			return ""
		}
	}
	return rows[0].available(action)
}

func tunnelSelection(rows []tunnelManagerRow, selection *ui.Selection[tunnelKey]) []tunnelManagerRow {
	var selected []tunnelManagerRow
	for _, row := range rows {
		if selection.Has(row.key) {
			selected = append(selected, row)
		}
	}
	return selected
}

func tunnelManagerColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "selection", Width: 32, Fixed: true},
		{ID: "name", Title: locales.TmgrName(), MinWidth: 150},
		{ID: "host", Title: locales.TmgrHost(), Width: 145},
		{ID: "listen", Title: locales.ColTunnelListen(), Width: 141},
		{ID: "target", Title: locales.TmgrTarget(), Width: 149},
		{ID: "mode", Title: locales.TmgrMode(), Width: 98},
		{ID: "state", Title: locales.ColTunnelStatus(), Width: 193},
		{ID: "connections", Title: locales.ColTunnelConnections(), Width: 105},
		{ID: "traffic", Title: locales.ColTunnelTraffic(), Width: 112},
		{ID: "uptime", Title: locales.TmgrUptime(), Width: 82},
	}
}

func (a *App) tunnelManagerTable(c *ui.Context, rows []tunnelManagerRow) {
	view := &a.tmgr.view
	view.table.Key = func(index int) any { return rows[index].key }
	view.table.Label = func(index int) string { return rows[index].name() }
	view.table.Selection = &view.selection
	view.table.HeaderCell = func(_ ui.Element, col int) {
		if col != 0 {
			return
		}
		on := len(rows) > 0 && view.selection.Len() == len(rows)
		if ui.Checkbox(c, &on, "").Label(locales.TmgrSelectAll()).Disabled(len(rows) == 0).Changed() {
			view.selection.Clear()
			if on {
				for _, row := range rows {
					view.selection.Add(row.key)
				}
			}
			c.Invalidate()
		}
	}
	view.table.CellMenu = func(menu *ui.Menu, index, _ int) {
		row := rows[index]
		selected := []tunnelManagerRow{row}
		if view.selection.Has(row.key) && view.selection.Len() > 1 {
			selected = tunnelSelection(rows, &view.selection)
		}
		a.tunnelManagerMenu(c, menu, selected, row)
	}
	columns := tunnelManagerColumns()
	order := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if width, _ := c.Size(); width < 900 {
		order = []int{0, 1, 2, 6, 3, 4, 5, 7, 8, 9}
	}
	visibleColumns := make([]ui.TableColumn, len(order))
	for index, col := range order {
		visibleColumns[index] = columns[col]
	}
	table := ui.Table(c, &view.table, visibleColumns, len(rows), func(index, col int) {
		row := rows[index]
		if col == 0 {
			on := view.selection.Has(row.key)
			if ui.Checkbox(c, &on, "").Label(row.name()).Changed() {
				if on {
					view.selection.Add(row.key)
				} else {
					view.selection.Remove(row.key)
				}
				c.Invalidate()
			}
			return
		}
		a.tunnelManagerCell(c, row, order[col], index == 0 || rows[index-1].key.host != row.key.host)
	}).Grow(1).MinHeight(0).KeepScrollbars(true).Border(1, theme.Border).Radius(6)
	table.Children(func() {
		if len(rows) == 0 {
			widgets.Muted(c, locales.TmgrEmpty()).Padding(14)
		}
	})
}

func (a *App) tunnelManagerMenu(c *ui.Context, menu *ui.Menu, selected []tunnelManagerRow, row tunnelManagerRow) {
	item := func(action tunnelAction, rows []tunnelManagerRow) {
		reason := tunnelActionReason(action, rows)
		if menu.Item(action.label()).Disabled(reason != "").Subtitle(reason).Chosen() {
			a.askTunnelManager(action, rows)
		}
	}
	item(tunnelActionStart, selected)
	item(tunnelActionStop, selected)
	item(tunnelActionRestart, selected)
	if len(selected) != 1 {
		return
	}
	item(tunnelActionStartBackground, selected)
	menu.Separator()
	toggle := tunnelActionEnableBackground
	if row.spec.Background {
		toggle = tunnelActionDisableBackground
	}
	editorOpen := a.tunnelEditorOpen(row.key.host)
	reason := ""
	if editorOpen {
		reason = locales.TmgrEditorOpen()
	}
	if menu.Item(toggle.label()).Disabled(editorOpen).Subtitle(reason).Chosen() {
		a.askTunnelManager(toggle, selected)
	}
	reason = ""
	if row.address == "" {
		reason = locales.TmgrNotListening()
	}
	if menu.Item(locales.TmgrCopyAddress()).Disabled(reason != "").Subtitle(reason).Chosen() {
		c.WriteClipboard(row.address)
	}
	if menu.Item(locales.TmgrLog()).Chosen() {
		a.tmgr.view.log = &row
	}
	if menu.Item(locales.TmgrOpenTerminal()).Chosen() {
		if host := a.hosts.Host(row.key.host); tunnelManagerHost(host) {
			a.openSession(host)
			a.plat.FocusWindow(WindowMain)
		}
	}
}

func (a *App) tunnelManagerCell(c *ui.Context, row tunnelManagerRow, col int, firstHost bool) {
	text := func(value string) ui.Element {
		return ui.Text(c, value).SingleLine().Ellipsis("…").Tooltip(value).MinWidth(0)
	}
	subtext := func(main, sub string) {
		ui.Column(c).MinWidth(0).Children(func() {
			text(main)
			if sub != "" {
				text(sub).FontSize(11).TextColor(theme.TextMuted)
			}
		})
	}
	switch col {
	case 1:
		subtext(row.name(), tunnelManagerKind(row.spec.Kind))
	case 2:
		if firstHost {
			hostAddr := row.hostAddr
			if row.host == hostAddr {
				hostAddr = ""
			}
			subtext(row.host, hostAddr)
		} else {
			text(row.host).TextColor(theme.TextMuted)
		}
	case 3:
		text(row.bind())
	case 4:
		text(row.target())
	case 5:
		mode := locales.TmgrWithSession()
		if row.background {
			mode = locales.TmgrBackground()
		}
		text(mode)
	case 6:
		a.tunnelManagerStateCell(c, row)
	case 7:
		if row.state == tunnelViewListening || row.stats.Total > 0 {
			text(locales.TmgrConnectionCount(strconv.FormatInt(row.stats.Active, 10), strconv.FormatInt(row.stats.Total, 10))).Tooltip(locales.TmgrConnectionsHint())
		} else {
			text("—").TextColor(theme.TextMuted)
		}
	case 8:
		if row.state == tunnelViewListening || row.stats.Up+row.stats.Down > 0 {
			ui.Column(c).FillWidth().MinWidth(0).Gap(2).Children(func() {
				text(formatSize(row.stats.Up + row.stats.Down))
				tunnelManagerSparkline(c, row.traffic)
			})
		} else {
			text("—").TextColor(theme.TextMuted)
		}
	case 9:
		if row.since.IsZero() {
			text("—").TextColor(theme.TextMuted)
		} else {
			text(uptimeText(max(0, a.tmgr.time().Sub(row.since))))
		}
	}
}

func (a *App) tunnelManagerStateCell(c *ui.Context, row tunnelManagerRow) {
	status, color := tunnelStateLabel(row.state), theme.TextMuted
	switch row.state {
	case tunnelViewConnecting:
		color = theme.Warning
	case tunnelViewListening:
		color = theme.Success
	case tunnelViewRetry:
		color = theme.Warning
		remaining := max(0, row.retryAt.Sub(a.tmgr.time()))
		status = locales.TmgrRetryIn(int((remaining + time.Second - 1) / time.Second))
	case tunnelViewError:
		color = theme.Danger
	}
	detail := row.errorText()
	if row.attempt > 0 {
		detail = locales.TmgrAttempt(strconv.Itoa(row.attempt)) + " · " + detail
	}
	ui.Column(c).MinWidth(0).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
			widgets.StatusDot(c, color)
			ui.Text(c, status).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(status)
		})
		if detail != "" {
			ui.Text(c, detail).FontSize(11).TextColor(color).MaxLines(2).Ellipsis("…").MinWidth(0).Tooltip(detail)
		}
	})
}

func tunnelManagerSparkline(c *ui.Context, samples []float64) {
	peak := 0.0
	for _, sample := range samples {
		peak = max(peak, sample)
	}
	color := theme.Accent
	ui.Box(c).FillWidth().Height(12).Tooltip(locales.TmgrRate(formatSize(int64(peak)))).Draw(func(p *ui.Painter, bounds ui.Rect) {
		if peak == 0 {
			return
		}
		step := bounds.W / tunnelTrafficSamples
		for index, sample := range samples {
			height := float32(sample/peak) * bounds.H
			p.Fill(ui.Rect{X: bounds.X + float32(tunnelTrafficSamples-len(samples)+index)*step, Y: bounds.Y + bounds.H - height, W: step * .75, H: height}, color, 0)
		}
	})
}

func (a *App) tunnelManagerFooter(c *ui.Context, rows []tunnelManagerRow) {
	listening, connections, traffic := 0, int64(0), int64(0)
	for _, row := range rows {
		if row.state == tunnelViewListening {
			listening++
		}
		connections += row.stats.Active
		traffic += row.stats.Up + row.stats.Down
	}
	on := a.settings.TunnelManager.Autostart()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		widgets.Muted(c, strings.Join([]string{locales.TmgrListeningTotal(listening), locales.TmgrActiveTotal(int(connections)), locales.TmgrTrafficTotal(formatSize(traffic))}, "  ·  "))
		ui.Spacer(c)
		if ui.Checkbox(c, &on, locales.TmgrAutostart()).Changed() {
			a.tmgr.view.autostart = &on
		}
	})
}
