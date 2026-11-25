package app

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type taskProcessID struct {
	pid   int
	start uint64
}

func taskProcessIDOf(proc monitor.TaskProcess) taskProcessID {
	return taskProcessID{proc.PID, proc.Start}
}

type taskProcessRow struct {
	proc  monitor.TaskProcess
	depth int
}

type desktopTaskProcessView struct {
	rows   []taskProcessRow
	filter string
	sort   ui.SortOrder
	tree   bool
}

func (state *desktopTaskState) shownProcesses() []taskProcessRow {
	view := &state.processView
	if view.rows == nil || view.filter != state.filter || view.sort != state.sort || view.tree != state.tree {
		*view = desktopTaskProcessView{rows: taskProcessRows(state.processes, state.filter, state.sort, state.tree), filter: state.filter, sort: state.sort, tree: state.tree}
	}
	return view.rows
}

func taskProcessRows(list []monitor.TaskProcess, filter string, sort ui.SortOrder, tree bool) []taskProcessRow {
	query := strings.ToLower(strings.TrimSpace(filter))
	visible := make([]monitor.TaskProcess, 0, len(list))
	for _, proc := range list {
		if query == "" || strings.Contains(strings.ToLower(proc.Name), query) || strings.Contains(strconv.Itoa(proc.PID), query) ||
			strings.Contains(strings.ToLower(proc.User), query) || strings.Contains(strings.ToLower(proc.Args), query) {
			visible = append(visible, proc)
		}
	}
	slices.SortStableFunc(visible, func(left, right monitor.TaskProcess) int {
		order := 0
		switch sort.Column {
		case "name":
			order = cmp.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
		case "pid":
			order = cmp.Compare(left.PID, right.PID)
		case "user":
			order = cmp.Compare(left.User, right.User)
		case "state":
			order = cmp.Compare(left.State, right.State)
		case "cpu":
			order = cmp.Compare(left.CPU, right.CPU)
		case "rss", "memory":
			order = cmp.Compare(left.RSS, right.RSS)
		case "threads":
			order = cmp.Compare(left.Threads, right.Threads)
		case "started":
			order = left.Started.Compare(right.Started)
		case "command":
			order = cmp.Compare(left.Args, right.Args)
		}
		if sort.Descending {
			order = -order
		}
		if order == 0 {
			order = cmp.Compare(left.PID, right.PID)
		}
		return order
	})
	rows := make([]taskProcessRow, 0, len(visible))
	if !tree {
		for _, proc := range visible {
			rows = append(rows, taskProcessRow{proc: proc})
		}
		return rows
	}
	ids := make(map[int]bool, len(visible))
	children := make(map[int][]monitor.TaskProcess)
	for _, proc := range visible {
		ids[proc.PID] = true
		children[proc.PPID] = append(children[proc.PPID], proc)
	}
	visited := make(map[int]bool, len(visible))
	var visit func(monitor.TaskProcess, int)
	visit = func(proc monitor.TaskProcess, depth int) {
		if visited[proc.PID] {
			return
		}
		visited[proc.PID] = true
		rows = append(rows, taskProcessRow{proc: proc, depth: depth})
		for _, child := range children[proc.PID] {
			visit(child, min(depth+1, 8))
		}
	}
	for _, proc := range visible {
		if !ids[proc.PPID] {
			visit(proc, 0)
		}
	}
	for _, proc := range visible {
		visit(proc, 0)
	}
	return rows
}

func taskProcessColumns(compact bool) []ui.TableColumn {
	cols := []ui.TableColumn{
		{ID: "name", Title: locales.ColProcName(), MinWidth: 120, Sortable: true},
		{ID: "pid", Title: locales.ColPID(), Width: 54, Sortable: true, Align: ui.End},
		{ID: "user", Title: locales.ColProcUser(), Width: 68, Sortable: true},
		{ID: "state", Title: locales.DesktopTaskState(), Width: 59, Sortable: true},
		{ID: "cpu", Title: locales.DesktopTaskCPU(), Width: 76, Sortable: true, Align: ui.End},
		{ID: "rss", Title: locales.DesktopTaskRSS(), Width: 80, Sortable: true, Align: ui.End},
		{ID: "memory", Title: locales.DesktopTaskMemoryPercent(), Width: 91, Sortable: true, Align: ui.End},
		{ID: "threads", Title: locales.DesktopTaskThreads(), Width: 68, Sortable: true, Align: ui.End},
		{ID: "started", Title: locales.DesktopTaskStarted(), Width: 158, Sortable: true},
		{ID: "command", Title: locales.ColProcCommand(), MinWidth: 138, Sortable: true},
	}
	if compact {
		cols[0].MinWidth, cols[1].Width, cols[2].Width = 105, 49, 57
		cols[3].Width, cols[4].Width, cols[5].Width = 51, 71, 75
	}
	return cols
}

func taskTableText(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).SingleLine().MinWidth(0).Tooltip(text).FontFeatures("tnum")
}

func taskStarted(proc monitor.TaskProcess) string {
	if proc.Started.IsZero() {
		return "—"
	}
	return proc.Started.Local().Format("2006-01-02 15:04:05")
}

func taskProcessState(state byte) string {
	if state == 0 {
		return "—"
	}
	return string(state)
}

func (a *App) desktopTaskProcesses(c *ui.Context, session *Session, state *desktopTaskState) {
	ready := desktopTaskConnected(session, state)
	page := ui.Column(c).Grow(1).Padding(12).Gap(9).MinHeight(0)
	compact := page.Bounds().W > 0 && page.Bounds().W < 550
	page.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.DesktopTaskProcesses()).FontSize(20).Bold().Grow(1).MinWidth(0)
			if ready {
				taskLiveBadge(c, state.paused)
			}
		})
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c, &state.filter).Placeholder(locales.DesktopTaskSearch()).Label(locales.DesktopTaskSearch()).Grow(1).MinWidth(90)
			label, icon := locales.DesktopTaskPause(), "pause"
			if state.paused {
				label, icon = locales.DesktopTaskResume(), "play"
			}
			if widgets.ToggleIconButton(c, icon, label, state.paused).Disabled(!ready).Clicked() {
				state.paused = !state.paused
			}
			if widgets.ToggleIconButton(c, "list-tree", locales.DesktopTaskTree(), state.tree).Clicked() {
				state.tree = !state.tree
			}
		})
		rows := state.shownProcesses()
		state.row = slices.IndexFunc(rows, func(row taskProcessRow) bool { return taskProcessIDOf(row.proc) == state.selected })
		var proc monitor.TaskProcess
		if state.row >= 0 {
			proc = rows[state.row].proc
		}
		ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
			mutate := ready && taskProcessCan(proc, monitor.TaskTerminate)
			if ui.Button(c, locales.DesktopTaskTerminate()).Disabled(!mutate).Clicked() {
				a.desktopTaskAsk(state, proc, monitor.TaskTerminate)
			}
			if ui.Button(c, locales.DesktopTaskProperties()).Disabled(!ready || proc.Start == 0).Clicked() {
				a.desktopTaskOpenDetails(state, proc)
			}
			menu := ui.Box(c).Size(25, 25).Center().Radius(5).Role(ui.RoleButton).Label(locales.DesktopTaskActions()).Tooltip(locales.DesktopTaskActions()).Disabled(proc.PID == 0)
			if menu.Hovered() {
				menu.Background(theme.Text.Alpha(0.08))
			}
			menu.Children(func() { ui.Icon(c, icons.Get("ellipsis")).FontSize(16) })
			menu.Menu(func(menu *ui.Menu) { a.desktopTaskProcessMenu(c, menu, state, proc, ready) })
		})
		if state.succeeded {
			taskNotice(c, locales.DesktopTaskSuccess(), false)
		}
		taskReadStatus(c, state.processReady, ready && !state.paused, state.processErr)
		if state.processReady && len(rows) == 0 {
			taskNotice(c, locales.DesktopTaskNoProcesses(), false)
		}
		state.rows.Selected, state.rows.Sort = &state.row, &state.sort
		state.rows.Label = func(index int) string { return rows[index].proc.Name }
		state.rows.CellMenu = func(menu *ui.Menu, row, _ int) {
			if row >= 0 && row < len(rows) {
				a.desktopTaskProcessMenu(c, menu, state, rows[row].proc, ready)
			}
		}
		cols := taskProcessColumns(compact)
		table := ui.Table(c, &state.rows, cols, len(rows), func(row, column int) {
			display := rows[row]
			proc := display.proc
			if column == 0 {
				ui.Row(c).PaddingX(float32(display.depth) * 8).MinWidth(0).Children(func() { taskTableText(c, proc.Name).Bold() })
				return
			}
			fraction := "—"
			if session.stats.MemTotal > 0 {
				fraction = fmt.Sprintf("%.1f%%", float64(proc.RSS)*100/float64(session.stats.MemTotal))
			}
			taskTableText(c, [...]string{"", strconv.Itoa(proc.PID), taskValue(proc.User), taskProcessState(proc.State), fmt.Sprintf("%.1f%%", proc.CPU),
				memorySize(proc.RSS), fraction, taskCount(proc.Threads), taskStarted(proc), taskValue(proc.Args)}[column])
		}).Grow(1).MinHeight(0).Background(colors.card).Border(1, colors.line).Radius(7).Label(locales.DesktopTaskProcesses())
		if table.Changed() || table.Submitted() {
			if state.row >= 0 && state.row < len(rows) {
				proc := rows[state.row].proc
				state.selected = taskProcessIDOf(proc)
				if table.Submitted() && ready && proc.Start > 0 {
					a.desktopTaskOpenDetails(state, proc)
				}
			}
		}
		widgets.Muted(c, locales.ProcCountFiltered(len(rows), len(state.processes))).FontSize(11).SingleLine()
	})
}

func taskProcessCan(proc monitor.TaskProcess, action monitor.TaskAction) bool {
	if proc.PID <= 1 || proc.Start == 0 || proc.State == 'Z' || proc.State == 'X' || proc.State == 'x' {
		return false
	}
	stopped := proc.State == 'T'
	switch action {
	case monitor.TaskContinue:
		return stopped
	case monitor.TaskStop:
		return !stopped && proc.State != 't'
	default:
		return true
	}
}

func (a *App) desktopTaskProcessMenu(c *ui.Context, menu *ui.Menu, state *desktopTaskState, proc monitor.TaskProcess, ready bool) {
	for _, action := range []monitor.TaskAction{monitor.TaskTerminate, monitor.TaskKill, monitor.TaskStop, monitor.TaskContinue, monitor.TaskRenice} {
		if menu.Item(desktopTaskActionName(action)).Disabled(!ready || !taskProcessCan(proc, action)).Chosen() {
			a.desktopTaskAsk(state, proc, action)
		}
	}
	menu.Separator()
	if menu.Item(locales.ProcCopyPID()).Chosen() {
		c.WriteClipboard(strconv.Itoa(proc.PID))
	}
	if menu.Item(locales.ProcCopyCommand()).Disabled(proc.Args == "").Chosen() {
		c.WriteClipboard(proc.Args)
	}
	menu.Separator()
	if menu.Item(locales.DesktopTaskProperties()).Disabled(!ready || proc.Start == 0).Chosen() {
		a.desktopTaskOpenDetails(state, proc)
	}
}
