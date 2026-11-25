package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopTaskAction struct {
	process monitor.TaskProcess
	kind    monitor.TaskAction
	memory  monitor.MemoryAction
	nice    string
	busy    bool
	err     error
	cancel  context.CancelFunc
}

func desktopTaskActionName(kind monitor.TaskAction) string {
	switch kind {
	case monitor.TaskTerminate:
		return locales.DesktopTaskTerminate()
	case monitor.TaskKill:
		return locales.DesktopTaskKill()
	case monitor.TaskStop:
		return locales.DesktopTaskStop()
	case monitor.TaskContinue:
		return locales.DesktopTaskContinue()
	case monitor.TaskRenice:
		return locales.DesktopTaskRenice()
	default:
		return ""
	}
}

func (action *desktopTaskAction) title() string {
	if action.memory != 0 {
		return memoryActionName(action.memory)
	}
	return locales.DesktopTaskActionTitle(desktopTaskActionName(action.kind), action.process.Name, strconv.Itoa(action.process.PID))
}

func (action *desktopTaskAction) warning() string {
	if action.memory != 0 {
		return memoryActionWarning(action.memory)
	}
	switch action.kind {
	case monitor.TaskTerminate:
		return "SIGTERM · " + locales.SigTERM()
	case monitor.TaskKill:
		return "SIGKILL · " + locales.SigKILL()
	case monitor.TaskStop:
		return "SIGSTOP · " + locales.SigSTOP()
	case monitor.TaskContinue:
		return "SIGCONT · " + locales.SigCONT()
	case monitor.TaskRenice:
		return locales.DesktopTaskNiceHint()
	default:
		return ""
	}
}

func (a *App) desktopTaskAsk(state *desktopTaskState, proc monitor.TaskProcess, kind monitor.TaskAction) {
	if state.action != nil || !taskProcessCan(proc, kind) {
		return
	}
	state.succeeded = false
	state.action = &desktopTaskAction{process: proc, kind: kind, nice: strconv.Itoa(proc.Nice)}
}

func (a *App) desktopTaskAskMemory(state *desktopTaskState, kind monitor.MemoryAction) {
	if state.action != nil {
		return
	}
	state.succeeded = false
	state.action = &desktopTaskAction{memory: kind}
}

func (a *App) desktopTaskRoot(session *Session) monitor.MemoryRunner {
	privilege := a.sessionPrivilege(session)
	return func(ctx context.Context, script string) (string, string, error) {
		return privilege.runner.Run(ctx, script, monitor.PrivilegeOptions{ScriptStdin: true})
	}
}

func desktopTaskContext(session *Session) context.Context {
	if session.ctx != nil {
		return session.ctx
	}
	return context.Background()
}

func (a *App) desktopTaskRunAction(session *Session, state *desktopTaskState, action *desktopTaskAction, nice int) {
	if action.busy || !desktopTaskConnected(session, state) {
		return
	}
	if action.memory != 0 && memoryActionBlock(session.stats.Memory, action.memory) != nil {
		return
	}
	if action.memory == 0 && !taskProcessCan(action.process, action.kind) {
		return
	}
	if action.kind == monitor.TaskRenice && (nice < -20 || nice > 19) {
		return
	}
	ctx, cancel := context.WithTimeout(desktopTaskContext(session), 2*time.Minute)
	action.busy, action.err, action.cancel = true, nil, cancel
	run := desktopTaskRunner(state.run, session.client)
	root := a.desktopTaskRoot(session)
	proc, kind, mem := action.process, action.kind, action.memory
	desktop := a.desktopOf(session)
	log := action.title()
	if kind == monitor.TaskRenice {
		log = locales.DesktopTaskNiceLog(desktopTaskActionName(kind), proc.Name, strconv.Itoa(proc.PID), strconv.Itoa(nice))
	}
	if mem != 0 {
		log = fmt.Sprintf("%s · %s", log, session.Host.Name)
	}
	go func() {
		defer cancel()
		var err error
		if mem != 0 {
			_, err = monitor.RunMemoryAction(ctx, root, mem)
		} else {
			err = monitor.RunTaskAction(ctx, run, root, proc.PID, proc.Start, kind, nice)
		}
		a.post(func() {
			if desktop != nil && a.desktopOf(session) == desktop {
				a.desktopLog(session, appTaskMgr, log, err)
			}
			if state.action != action {
				return
			}
			action.busy, action.err, action.cancel = false, err, nil
			state.poll.pollNow()
			if err == nil {
				state.action = nil
				state.succeeded = true
			}
		})
	}()
}

func (a *App) desktopTaskActionDialog(c *ui.Context, session *Session, state *desktopTaskState) {
	action := state.action
	if action == nil {
		return
	}
	open := true
	ui.Modal(c.Key(action), &open, func() {
		ui.Column(c).Width(400).MinWidth(0).Gap(11).Children(func() {
			ui.Text(c, action.title()).FontSize(17).Bold().MaxLines(3)
			ui.Text(c, action.warning()).FontSize(13)
			if action.memory != 0 && session.Host != nil {
				ui.Text(c, session.Host.Name+" · "+session.Host.Addr).TextColor(theme.TextMuted).FontSize(11).SingleLine()
			}
			if action.kind == monitor.TaskRenice {
				ui.TextInput(c, &action.nice).Label(locales.DesktopTaskNice()).Width(98).Disabled(action.busy)
			}
			if action.err != nil {
				reason := desktopTaskError(action.err)
				if action.memory != 0 {
					reason = memoryErrorText(action.err)
				}
				taskNotice(c, reason, true)
			}
			nice, err := strconv.Atoi(strings.TrimSpace(action.nice))
			invalid := action.kind == monitor.TaskRenice && (err != nil || nice < -20 || nice > 19)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if action.busy {
					ui.Spinner(c).Label(locales.MemoryWorking())
					ui.Text(c, locales.MemoryWorking()).TextColor(theme.TextMuted).FontSize(11).MinWidth(0).Shrink(1).SingleLine()
				}
				ui.Spacer(c)
				if ui.Button(c, locales.Cancel()).Clicked() {
					if action.cancel != nil {
						action.cancel()
					} else {
						open = false
					}
				}
				if ui.PrimaryButton(c, locales.DesktopTaskConfirm()).Disabled(action.busy || invalid || !desktopTaskConnected(session, state)).Clicked() {
					a.desktopTaskRunAction(session, state, action, nice)
				}
			})
		})
	})
	if !open {
		if action.cancel != nil {
			action.cancel()
		}
		state.action = nil
	}
}
