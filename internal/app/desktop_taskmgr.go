package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func init() {
	registerDesktopApp(desktopApp{
		id: appTaskMgr, icon: "activity", tint: ui.Hex("#22a06b"), name: locales.DesktopAppTaskManager,
		category: desktopCatSystem, order: 60, onDesktop: true, pinned: true,
		size: desktopSize{W: 1010, H: 640}, minSize: desktopSize{W: 590, H: 390}, single: true,
		view: (*App).desktopTaskView, opened: desktopTaskOpened, closing: (*App).desktopTaskClosing,
	})
}

type desktopTaskPage int

const (
	taskPerformance desktopTaskPage = iota
	taskProcesses
	taskUsers
	taskServices
)

type taskMetric int

const (
	taskLoad taskMetric = iota
	taskCPU
	taskMemory
	taskDisk
	taskNetwork
)

type desktopTaskState struct {
	page           desktopTaskPage
	metric         taskMetric
	disk, iface    string
	paused         bool
	poll           sessionPoll
	client         *ssh.Client
	sessionContext context.Context
	run            monitor.MemoryRunner

	perf          monitor.TaskPerformance
	perfReady     bool
	perfErr       error
	history       map[string][]float64
	statsAt       time.Time
	shownFrame    uint64
	processes     []monitor.TaskProcess
	processReady  bool
	processErr    error
	filter        string
	tree          bool
	sort          ui.SortOrder
	rows          ui.ListState
	row           int
	selected      taskProcessID
	processView   desktopTaskProcessView
	users         []monitor.TaskLogin
	usersReady    bool
	usersErr      error
	userRows      ui.ListState
	services      []monitor.ServiceUnit
	servicesReady bool
	servicesErr   error
	serviceRows   ui.ListState

	details   *desktopTaskDetails
	action    *desktopTaskAction
	succeeded bool
}

func desktopTaskOpened(_ *App, window *desktopWindow, _ any) {
	if window.state == nil {
		window.state = &desktopTaskState{client: window.session.client, sessionContext: window.session.ctx, history: make(map[string][]float64), sort: ui.SortOrder{Column: "cpu", Descending: true}}
	}
}

func (a *App) desktopTaskClosing(window *desktopWindow) bool {
	state := window.state.(*desktopTaskState)
	a.stopPoll(&state.poll)
	a.desktopTaskCloseDetails(state)
	if state.action != nil && state.action.cancel != nil {
		state.action.cancel()
	}
	state.action = nil
	return true
}

func desktopTaskRunner(run monitor.MemoryRunner, client *ssh.Client) monitor.MemoryRunner {
	if run != nil {
		return run
	}
	return func(ctx context.Context, script string) (string, string, error) {
		return monitor.RunScript(ctx, client, script)
	}
}

func desktopTaskConnected(session *Session, state *desktopTaskState) bool {
	return session.status == statusConnected && execAllowed(session.Host) && (session.client != nil || state.run != nil)
}

func (a *App) desktopTaskPoll(session *Session, state *desktopTaskState) {
	if !desktopTaskConnected(session, state) || state.page == taskProcesses && (state.paused || state.details != nil) {
		a.stopPoll(&state.poll)
		return
	}
	run := desktopTaskRunner(state.run, session.client)
	interval := procRefresh
	if state.page == taskUsers || state.page == taskServices {
		interval = 15 * time.Second
	}
	a.keepPolling(&state.poll, session, interval, func() pollRead {
		var sampler monitor.TaskSampler
		var lister monitor.TaskProcessLister
		page := state.page
		return func(ctx context.Context, _ *ssh.Client) func() {
			ctx, cancel := context.WithTimeout(ctx, sysInfoTimeout)
			defer cancel()
			switch page {
			case taskProcesses:
				list, err := lister.List(ctx, run)
				return func() {
					state.processErr = err
					if err == nil {
						state.processes, state.processReady = list, true
						state.processView = desktopTaskProcessView{}
					}
				}
			case taskUsers:
				list, err := monitor.ReadTaskLogins(ctx, run)
				return func() {
					state.usersErr = err
					if err == nil {
						state.users, state.usersReady = list, true
					}
				}
			case taskServices:
				list, _, err := monitor.ReadServices(ctx, monitor.SystemRead(run))
				return func() {
					state.servicesErr = err
					if err == nil || len(list) > 0 {
						state.services, state.servicesReady = list, true
					}
				}
			default:
				performance, err := sampler.Sample(ctx, run)
				return func() {
					state.perfErr = err
					if err == nil {
						state.sample(performance, &session.stats)
					}
				}
			}
		}
	})
}

func (state *desktopTaskState) sample(read monitor.TaskPerformance, stats *monitor.Stats) {
	state.perf, state.perfReady = read, true
	for index, value := range read.Load {
		state.push(taskLoadSeries[index], value)
	}
	for _, cpu := range read.CPUs {
		state.push(cpu.Name, cpu.Usage())
	}
	for _, disk := range read.Disks {
		if disk.Measured {
			state.push("read/"+disk.Name, disk.Read)
			state.push("write/"+disk.Name, disk.Write)
		}
	}
	state.sampleSessionStats(stats)
}

func (state *desktopTaskState) sampleSessionStats(stats *monitor.Stats) {
	if stats.Memory.SampledAt == state.statsAt {
		return
	}
	state.statsAt = stats.Memory.SampledAt
	if stats.MemTotal > 0 {
		state.push("mem", ratio(stats.MemUsed, stats.MemTotal)*100)
	}
	if stats.SwapTotal > 0 {
		state.push("swap", ratio(stats.SwapUsed, stats.SwapTotal)*100)
	}
	for _, iface := range stats.Ifaces {
		if len(iface.RxHist) > 0 {
			state.push("rx/"+iface.Name, iface.RxRate)
		}
		if len(iface.TxHist) > 0 {
			state.push("tx/"+iface.Name, iface.TxRate)
		}
	}
}

func (state *desktopTaskState) push(key string, sample float64) {
	values := state.history[key]
	if len(values) == monitor.HistoryLen {
		copy(values, values[1:])
		values[len(values)-1] = sample
		return
	}
	state.history[key] = append(values, sample)
}

func (a *App) desktopTaskView(c *ui.Context, window *desktopWindow) {
	state, session := window.state.(*desktopTaskState), window.session
	if state.client != session.client || state.sessionContext != session.ctx {
		a.desktopTaskClosing(window)
		*state = desktopTaskState{page: state.page, metric: state.metric, client: session.client, sessionContext: session.ctx,
			run: state.run, history: make(map[string][]float64), sort: state.sort}
	}
	if state.shownFrame+1 < a.frame && state.details != nil {
		state.details.reveal = false
	}
	state.shownFrame = a.frame
	ui.Row(c.Key("task-manager")).Absolute().Top(0).Bottom(0).Left(0).Right(0).AlignItems(ui.Start).Clip().Background(colors.overviewPage).Children(func() {
		a.desktopTaskNav(c, state)
		ui.Column(c).Grow(1).FillHeight().MinWidth(0).MinHeight(0).Clip().Children(func() {
			switch {
			case session.status != statusConnected:
				a.statusBanner(c, session)
			case !execAllowed(session.Host):
				taskNotice(c, locales.MonitorOff(), false)
			}
			switch state.page {
			case taskProcesses:
				if state.details != nil {
					a.desktopTaskProperties(c, session, state)
				} else {
					a.desktopTaskProcesses(c, session, state)
				}
			case taskUsers:
				a.desktopTaskUsers(c, session, state)
			case taskServices:
				a.desktopTaskServices(c, session, state)
			default:
				a.desktopTaskPerformance(c, session, state)
			}
		})
		a.desktopTaskActionDialog(c, session, state)
	})
	if !window.minimized {
		a.desktopTaskPoll(session, state)
	}
}

func (a *App) desktopTaskNav(c *ui.Context, state *desktopTaskState) {
	ui.Column(c).Width(140).FillHeight().Shrink(0).Padding(12, 8).Gap(5).Background(colors.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(colors.line).Children(func() {
		for _, item := range []struct {
			page        desktopTaskPage
			title, icon string
		}{
			{taskProcesses, locales.DesktopTaskProcesses(), "list-tree"},
			{taskPerformance, locales.DesktopTaskPerformance(), "activity"},
			{taskUsers, locales.DesktopTaskUsers(), "users"},
			{taskServices, locales.DesktopTaskServices(), "server"},
		} {
			selected := state.page == item.page
			row := ui.Row(c.Key(item.page)).FillWidth().Height(34).Gap(7).PaddingX(8).Radius(7).AlignItems(ui.Center).
				Role(ui.RoleButton).Focusable().Label(item.title).AccessSelected(selected).Tooltip(item.title)
			ink := theme.TextMuted
			if selected {
				row.Background(theme.Accent.Alpha(0.14))
				ink = theme.Accent
			} else if row.Hovered() {
				row.Background(theme.Text.Alpha(0.06))
			}
			if row.Clicked() && !selected {
				a.stopPoll(&state.poll)
				a.desktopTaskCloseDetails(state)
				state.page, state.succeeded = item.page, false
			}
			row.Children(func() {
				ui.Icon(c, icons.Get(item.icon)).FontSize(15).TextColor(ink).Shrink(0)
				ui.Text(c, item.title).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
			})
		}
	})
}

func taskNotice(c *ui.Context, text string, bad bool) {
	ink, background := theme.TextMuted, colors.note
	if bad {
		ink, background = theme.Danger, colors.dangerBanner
	}
	ui.Text(c, text).FontSize(12).TextColor(ink).Background(background).Padding(7, 10).Radius(6).FillWidth()
}

func taskReadStatus(c *ui.Context, ready, connected bool, err error) {
	switch {
	case err != nil:
		taskNotice(c, describeProcError(err), true)
	case !ready && connected:
		ui.Row(c).Gap(8).Padding(7, 9).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(locales.ReadingServerInfo())
			widgets.Muted(c, locales.ReadingServerInfo())
		})
	case !ready:
		taskNotice(c, locales.DesktopTaskUnavailable(), false)
	}
}

func desktopTaskError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return locales.DesktopTaskCancelled()
	case errors.Is(err, monitor.ErrNoProcess):
		return locales.ProcAlreadyEnded()
	case errors.Is(err, monitor.ErrPrivilegeCancelled):
		return locales.DesktopTaskCancelled()
	case errors.Is(err, monitor.ErrPrivilegeDenied):
		return locales.ProcNeedsRoot()
	default:
		return describeProcError(err)
	}
}

func (a *App) desktopTaskUsers(c *ui.Context, session *Session, state *desktopTaskState) {
	ui.Column(c).Grow(1).MinHeight(0).Padding(12).Gap(10).Children(func() {
		ui.Text(c, locales.DesktopTaskUsers()).FontSize(20).Bold()
		taskReadStatus(c, state.usersReady, desktopTaskConnected(session, state), state.usersErr)
		if state.usersReady && len(state.users) == 0 {
			taskNotice(c, locales.DesktopTaskNoUsers(), false)
		}
		if len(state.users) > 0 {
			cols := []ui.TableColumn{{Title: locales.ColProcUser(), Width: 105}, {Title: locales.DesktopTaskTTY(), Width: 90},
				{Title: locales.DesktopTaskStarted(), MinWidth: 145}, {Title: locales.DesktopTaskSource(), MinWidth: 100}}
			ui.Table(c, &state.userRows, cols, len(state.users), func(row, column int) {
				login := state.users[row]
				taskTableText(c, [...]string{login.User, login.TTY, login.Started, taskValue(login.Source)}[column])
			}).Grow(1).MinHeight(0).Background(colors.card).Border(1, colors.line).Radius(7)
		}
	})
}

func (a *App) desktopTaskServices(c *ui.Context, session *Session, state *desktopTaskState) {
	ui.Column(c).Grow(1).MinHeight(0).Padding(12).Gap(10).Children(func() {
		ui.Text(c, locales.DesktopTaskServices()).FontSize(20).Bold()
		taskReadStatus(c, state.servicesReady, desktopTaskConnected(session, state), state.servicesErr)
		if state.servicesReady && len(state.services) == 0 {
			taskNotice(c, locales.DesktopTaskUnavailable(), false)
		}
		if len(state.services) == 0 {
			return
		}
		cols := []ui.TableColumn{{Title: locales.SvcName(), MinWidth: 150}, {Title: locales.SvcState(), Width: 80},
			{Title: locales.SvcEnabled(), Width: 80}, {Title: locales.ColPID(), Width: 65, Align: ui.End}, {Title: locales.SvcDescription(), MinWidth: 160}}
		ui.Table(c, &state.serviceRows, cols, len(state.services), func(row, column int) {
			service := state.services[row]
			pid := "—"
			if service.PID > 0 {
				pid = strconv.FormatInt(service.PID, 10)
			}
			taskTableText(c, [...]string{service.Name, taskValue(service.Sub), taskValue(service.Enabled), pid, taskValue(service.Description)}[column])
		}).Grow(1).MinHeight(0).Background(colors.card).Border(1, colors.line).Radius(7)
	})
}
