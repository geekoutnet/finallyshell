package app

import (
	"context"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type desktopTaskDetails struct {
	process monitor.TaskProcess
	tab     monitor.TaskProperty
	poll    sessionPoll
	load    bool
	busy    bool
	root    bool
	reveal  bool
	err     error
	result  monitor.TaskDetails
	limited []string
	list    ui.ListState
}

var taskPropertyTabs = [...]func() string{
	locales.DesktopTaskBasic, locales.DesktopTaskMaps, locales.DesktopTaskFiles, locales.DesktopTaskEnvironment, locales.DesktopTaskConnections,
}

func (a *App) desktopTaskOpenDetails(state *desktopTaskState, process monitor.TaskProcess) {
	if process.Start == 0 {
		return
	}
	a.desktopTaskCloseDetails(state)
	state.details = &desktopTaskDetails{process: process, load: true}
}

func (a *App) desktopTaskCloseDetails(state *desktopTaskState) {
	if detail := state.details; detail != nil {
		a.stopPoll(&detail.poll)
		state.details = nil
	}
}

func (a *App) desktopTaskSelectProperty(detail *desktopTaskDetails, tab monitor.TaskProperty, root bool) {
	a.stopPoll(&detail.poll)
	if !root {
		detail.limited = nil
	}
	detail.tab, detail.load, detail.busy, detail.root, detail.reveal, detail.err = tab, true, false, root, false, nil
	detail.result, detail.list = monitor.TaskDetails{}, ui.ListState{}
}

func (a *App) desktopTaskReadProperty(session *Session, state *desktopTaskState, detail *desktopTaskDetails) {
	if !desktopTaskConnected(session, state) || !detail.load {
		return
	}
	if detail.poll.run != nil {
		detail.poll.run.seen = true
		return
	}
	detail.reveal, detail.busy = false, true
	run, tab := desktopTaskRunner(state.run, session.client), detail.tab
	pid, start := detail.process.PID, detail.process.Start
	root := detail.root
	if root {
		run = a.desktopTaskRoot(session)
	}
	a.keepPolling(&detail.poll, session, time.Duration(1<<63-1), func() pollRead {
		return func(ctx context.Context, _ *ssh.Client) func() {
			timeout := procTimeout
			if root {
				timeout = 2 * time.Minute
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			result, err := monitor.ReadTaskDetails(ctx, run, pid, start, tab)
			return func() {
				a.stopPoll(&detail.poll)
				detail.result, detail.err, detail.busy, detail.load, detail.root = result, err, false, false, false
				if err == nil {
					detail.limited = result.Missing
				}
			}
		}
	})
}

func (a *App) desktopTaskProperties(c *ui.Context, session *Session, state *desktopTaskState) {
	detail := state.details
	ui.Column(c.Key(detail)).Grow(1).Padding(12).Gap(9).MinHeight(0).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if widgets.IconButton(c, "arrow-left", locales.DesktopTaskBack()).Clicked() {
				a.desktopTaskCloseDetails(state)
			}
			title := locales.DesktopTaskActionTitle(locales.DesktopTaskProperties(), detail.process.Name, strconv.Itoa(detail.process.PID))
			ui.Text(c, title).FontSize(17).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(title)
			if widgets.IconButton(c, "refresh-cw", locales.Refresh()).Disabled(!desktopTaskConnected(session, state)).Clicked() {
				a.desktopTaskSelectProperty(detail, detail.tab, false)
			}
		})
		ui.Row(c).Wrap().Gap(5).Children(func() {
			for index, label := range taskPropertyTabs {
				tab := monitor.TaskProperty(index)
				button := ui.Button(c.Key(tab), label()).FontSize(11).AccessSelected(tab == detail.tab)
				if tab == detail.tab {
					button.Background(theme.Accent.Alpha(0.15)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.45))
				}
				if button.Clicked() && tab != detail.tab {
					a.desktopTaskSelectProperty(detail, tab, false)
				}
			}
		})
		if detail.busy || detail.load {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label(locales.ReadingServerInfo())
				widgets.Muted(c, locales.ReadingServerInfo()).Grow(1).MinWidth(0)
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.stopPoll(&detail.poll)
					detail.busy, detail.load, detail.root = false, false, false
				}
			})
		}
		if detail.err != nil {
			taskNotice(c, desktopTaskError(detail.err), true)
		}
		if len(detail.limited) > 0 && !detail.busy && !detail.load {
			ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
				label := locales.DesktopTaskLimited()
				ui.Text(c, label).FontSize(11).TextColor(theme.Warning).Grow(1).MinWidth(190).Tooltip(strings.Join(detail.limited, ", "))
				if ui.Button(c, locales.DesktopTaskAsRoot()).FontSize(11).Disabled(!desktopTaskConnected(session, state)).Clicked() {
					a.desktopTaskSelectProperty(detail, detail.tab, true)
				}
			})
		}
		if detail.result.Truncated {
			taskNotice(c, locales.DesktopTaskTruncated(), false)
		}
		if !detail.busy && !detail.load {
			taskPropertyContent(c, detail)
		}
	})
	if state.details == detail {
		a.desktopTaskReadProperty(session, state, detail)
	}
}

func taskPropertyContent(c *ui.Context, detail *desktopTaskDetails) {
	result := detail.result
	switch detail.tab {
	case monitor.TaskBasic:
		taskPropertyBasic(c, detail)
	case monitor.TaskMaps:
		cols := []ui.TableColumn{{Title: locales.DesktopTaskMappedRange(), Width: 178}, {Title: locales.DesktopTaskAccess(), Width: 86},
			{Title: locales.ColSize(), Width: 75, Align: ui.End}, {Title: locales.DesktopTaskMappedPath(), MinWidth: 170}}
		taskPropertyTable(c, detail, cols, len(result.Maps), func(index, column int) string {
			entry := result.Maps[index]
			return [...]string{entry.Range, entry.Access, memorySize(entry.Bytes), taskValue(entry.Path)}[column]
		})
	case monitor.TaskFiles:
		cols := []ui.TableColumn{{Title: locales.DesktopTaskFileDescriptor(), Width: 95}, {Title: locales.DesktopTaskTarget(), MinWidth: 240}}
		taskPropertyTable(c, detail, cols, len(result.Files), func(index, column int) string {
			entry := result.Files[index]
			return [...]string{strconv.Itoa(entry.Number), entry.Target}[column]
		})
	case monitor.TaskEnv:
		if len(result.Env) > 0 {
			label := locales.DesktopTaskReveal()
			if detail.reveal {
				label = locales.DesktopTaskHide()
			}
			if ui.Button(c, label).FontSize(11).AlignSelf(ui.Start).Clicked() {
				detail.reveal = !detail.reveal
			}
		}
		taskPropertyTable(c, detail, envColumns(), len(result.Env), func(index, column int) string {
			entry := result.Env[index]
			return [...]string{entry.Name, entry.Display(detail.reveal)}[column]
		})
	case monitor.TaskSockets:
		cols := []ui.TableColumn{{Title: locales.DesktopTaskProtocol(), Width: 76}, {Title: locales.DesktopTaskLocal(), MinWidth: 136},
			{Title: locales.DesktopTaskRemote(), MinWidth: 136}, {Title: locales.DesktopTaskState(), Width: 105}}
		taskPropertyTable(c, detail, cols, len(result.Sockets), func(index, column int) string {
			socket := result.Sockets[index]
			remote := "—"
			if socket.Remote.IsValid() {
				remote = socket.Remote.String()
			}
			return [...]string{strings.ToUpper(socket.Proto), socket.Local.String(), remote, socket.State}[column]
		})
	}
}

func taskPropertyBasic(c *ui.Context, detail *desktopTaskDetails) {
	data, process := detail.result, detail.process
	basic := data.Basic
	cols := []taskFact{{locales.ColPID(), strconv.Itoa(process.PID)}, {locales.DesktopTaskParent(), strconv.Itoa(process.PPID)},
		{locales.ColProcUser(), taskValue(process.User)}, {locales.DesktopTaskState(), taskProcessState(process.State)},
		{locales.DesktopTaskNice(), strconv.Itoa(process.Nice)}, {locales.DesktopTaskThreads(), taskCount(process.Threads)},
		{locales.DesktopTaskStarted(), taskStarted(process)}, {locales.DesktopTaskRSS(), memorySize(process.RSS)}}
	if basic.PID != 0 {
		cols[1].value, cols[3].value = strconv.Itoa(data.Status.PPID), taskProcessState(basic.State)
		if basic.User != "" {
			cols[2].value = basic.User
		}
		cols[4].value = strconv.Itoa(data.Nice)
		if data.Status.Threads > 0 {
			cols[5].value, cols[7].value = taskCount(data.Status.Threads), memorySize(data.Status.RSS)
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).KeepScrollbars(true).Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Padding(10).Radius(7).Background(colors.card).Border(1, colors.line).Gap(8).Children(func() {
			taskFacts(c, 500, cols...)
			for _, row := range []taskFact{{locales.ProcCwd(), taskValue(basic.Cwd)}, {locales.ColProcPath(), taskValue(basic.Path)}, {locales.ColProcCommand(), taskValue(strings.Join(basic.Args, " "))}} {
				ui.Column(c.Key(row.label)).Gap(3).Children(func() {
					widgets.Muted(c, row.label).FontSize(11)
					ui.Text(c, row.value).Selectable().FontSize(12).MinWidth(0)
				})
			}
		})
	})
}

func taskPropertyTable(c *ui.Context, detail *desktopTaskDetails, cols []ui.TableColumn, count int, cell func(int, int) string) {
	if count == 0 && len(detail.result.Missing) == 0 && detail.err == nil {
		taskNotice(c, locales.DesktopTaskUnavailable(), false)
	}
	ui.Table(c.Key(detail.tab), &detail.list, cols, count, func(row, column int) { taskTableText(c, cell(row, column)).Selectable() }).
		Grow(1).MinHeight(0).Background(colors.card).Border(1, colors.line).Radius(7).Label(taskPropertyTabs[detail.tab]())
}
