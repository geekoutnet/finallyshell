package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

type aiTerminalTarget struct {
	window *desktopWindow
	term   *terminal.Terminal
}

type aiRunConfirmation struct {
	target   aiTerminalTarget
	command  string
	highRisk bool
	explicit string
}

type aiCommandState struct {
	session    *Session
	target     aiTerminalTarget
	prompt     string
	attach     bool
	os         string
	result     string
	status     string
	failed     bool
	open       bool
	focus      bool
	cancel     context.CancelFunc
	seen       uint64
	requestFor config.AISettings
}

func (a *App) desktopAITarget(session *Session) aiTerminalTarget {
	if target := a.ai.targets[session]; target.term != nil && a.aiTargetLive(session, target) {
		return target
	}
	delete(a.ai.targets, session)
	if barLive(session) {
		return aiTerminalTarget{term: session.term()}
	}
	return aiTerminalTarget{}
}

func (a *App) aiTargetLive(session *Session, target aiTerminalTarget) bool {
	if session == nil || target.term == nil || session.status != statusConnected || !slices.Contains(a.sessions, session) {
		return false
	}
	if target.window == nil {
		return session.term() == target.term
	}
	desktop := a.desktopOf(session)
	return desktop != nil && slices.Contains(desktop.windows, target.window) && a.ai.targets[session] == target
}

func (a *App) aiDeliver(session *Session, target aiTerminalTarget, command string, run bool) bool {
	if !ai.CommandLine(command) || a.Locked() || !a.aiTargetLive(session, target) {
		return false
	}
	if run {
		target.term.Send([]byte(command + "\r"))
		if target.window == nil {
			session.shellCommandSent()
		}
		a.desktopLog(session, appAI, locales.DesktopAIRunLogged(), nil)
	} else {
		target.term.Send([]byte(command))
	}
	if target.window != nil {
		a.desktopFocus(target.window)
	} else {
		a.active = slices.Index(a.sessions, session)
		session.panes.refocus = true
	}
	return true
}

func (a *App) desktopAITerminal(c *ui.Context, session *Session, window *desktopWindow, term *terminal.Terminal, view ui.Element) {
	if session == nil || session.isDeviceSession() || session.isLocal() {
		return
	}
	if term == nil || session.status != statusConnected {
		if previous := a.ai.targets[session]; previous.window == window {
			delete(a.ai.targets, session)
		}
		return
	}
	target := aiTerminalTarget{window: window, term: term}
	if window != nil {
		if desktop := a.desktopOf(session); desktop != nil && (desktop.focused == window || a.ai.targets[session].window == window) {
			if a.ai.targets == nil {
				a.ai.targets = make(map[*Session]aiTerminalTarget)
			}
			for other, old := range a.ai.targets {
				if !a.aiTargetLive(other, old) {
					delete(a.ai.targets, other)
				}
			}
			a.ai.targets[session] = target
		}
	} else if previous := a.ai.targets[session]; previous.window != nil {
		delete(a.ai.targets, session)
	}
	key := a.shortcut(scGenerateAI)
	if !key.none() && view.Shortcut(key.Mods, key.Key) {
		a.aiOpenCommand(session, target)
	}
	state := a.ai.command
	if state == nil || state.session != session || state.target != target {
		return
	}
	if state.cancel != nil && state.requestFor != a.aiSettings() {
		a.aiCloseCommand()
		return
	}
	a.aiCheckReady()
	state.seen = a.frame
	anchor := ui.Box(c).Absolute().Left(11).Top(46).Size(1, 1)
	ui.Popover(c, anchor, &state.open, func() { a.aiCommandView(c, state, view) })
	if !state.open {
		a.aiCloseCommand()
	}
}

func (a *App) aiOpenCommand(session *Session, target aiTerminalTarget) {
	a.aiCloseCommand()
	a.ai.command = &aiCommandState{session: session, target: target, os: ai.Mask(aiOS(session)), attach: a.aiSettings().SendContext, open: true, focus: true}
	a.aiCheckReady()
}

func (a *App) aiCloseCommand() {
	if a.ai.command != nil && a.ai.command.cancel != nil {
		a.ai.command.cancel()
	}
	a.ai.command = nil
}

func (a *App) aiStopUnseenCommand() {
	if state := a.ai.command; state != nil && (!state.open || state.seen != a.frame) {
		a.aiCloseCommand()
	}
}

func (a *App) aiExplainTerminalText(session *Session, text string) {
	if strings.TrimSpace(text) == "" || a.Locked() || !canOpenDesktop(session) {
		return
	}
	text = ai.Limit(ai.Mask(text), 16<<10)
	a.openDesktop(session)
	a.desktopOpen(session, appAI, desktopAISelection{text: text})
}

func (a *App) aiCommandView(c *ui.Context, state *aiCommandState, view ui.Element) {
	width, _ := c.Size()
	ui.Column(c).Width(min(410, width-32)).Padding(11).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			desktopAITile(c, 29)
			ui.Text(c, locales.AiGenerate()).Bold().FontSize(15).Grow(1).MinWidth(0)
			widgets.Muted(c, a.shortcutLabel(scGenerateAI)).FontSize(11)
		})
		if !a.ai.ready {
			widgets.Muted(c, locales.DesktopAISetupBody()).MaxWidthPercent(100)
			ui.Row(c).Gap(7).Children(func() {
				if ui.PrimaryButton(c, locales.DesktopAISettings()).Clicked() {
					state.open = false
					a.openAIOptions()
				}
				if ui.Button(c, locales.Cancel()).Clicked() {
					state.open = false
				}
			})
			return
		}
		input := ui.TextInput(c, &state.prompt).FillWidth().MinWidth(0).Label(locales.AiCommandHint()).Placeholder(locales.AiCommandHint()).Disabled(state.cancel != nil)
		if input.Changed() {
			state.result, state.status = "", ""
		}
		if state.focus {
			input.Focus()
			state.focus = false
		}
		if state.os != "" && state.result == "" && state.cancel == nil {
			ui.Checkbox(c, &state.attach, locales.AiAttachOS())
			if state.attach {
				widgets.Muted(c, state.os).FontSize(12)
			}
		}
		if state.status != "" {
			ui.Scroll(c).MaxHeight(70).FillWidth().Children(func() { aiStatus(c, state.status, state.failed) })
		}
		if state.result != "" {
			ui.Text(c, locales.AiReview()).Bold().FontSize(12)
			ui.Scroll(c).MaxHeight(90).FillWidth().Background(desktopAISoft()).Radius(6).Children(func() {
				ui.Text(c, state.result).Font("monospace").Padding(8).MaxWidthPercent(100)
			})
		}
		ui.Row(c).Gap(6).Wrap().Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				state.open = false
			}
			if state.result != "" {
				if ui.Button(c, locales.Copy()).Clicked() {
					c.WriteClipboard(state.result)
				}
				if ui.PrimaryButton(c, locales.AiInsertCommand()).Clicked() {
					if a.aiDeliver(state.session, state.target, state.result, false) {
						state.open = false
						view.Focus()
					} else {
						state.status, state.failed = locales.DesktopAITargetChanged(), true
					}
				}
				return
			}
			ready := state.cancel == nil && strings.TrimSpace(state.prompt) != ""
			if ui.PrimaryButton(c, locales.AiGenerateButton()).Disabled(!ready).Clicked() || ready && input.Shortcut(0, ui.KeyEnter) {
				a.aiGenerateCommand(state)
			}
		})
	})
}

func (a *App) aiGenerateCommand(state *aiCommandState) {
	settings := a.aiSettings()
	question := strings.TrimSpace(state.prompt)
	if state.cancel != nil || a.Locked() || question == "" || !a.aiTargetLive(state.session, state.target) {
		return
	}
	if len(question) > aiQuestionLimit {
		state.status, state.failed = locales.DesktopAIQuestionTooLong(), true
		return
	}
	if err := aiValidate(settings, true); err != nil {
		state.status, state.failed = err.Error(), true
		return
	}
	if state.attach && state.os != "" {
		question += "\n\n[Untrusted server context]\nOS: " + state.os
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel, state.status, state.failed = cancel, locales.DesktopAIReceiving(), false
	state.requestFor = settings
	finish := func(text string, err error) {
		if a.ai.command != state || !state.open || a.Locked() {
			cancel()
			return
		}
		cancel()
		state.cancel, state.status = nil, ""
		if err != nil {
			state.status, state.failed = locales.AiRequestError()+"\n"+err.Error(), true
			return
		}
		state.result = ai.Command(text)
		if state.result == "" {
			state.status, state.failed = locales.AiInvalidCommand(), true
		}
	}
	a.aiCredential(ctx, settings, "", false, func(client ai.Client, err error) {
		if err == nil && (a.aiSettings() != settings || a.Locked() || a.ai.command != state || !state.open) {
			err = errors.New(locales.AiSettingsChanged())
		}
		if err == nil && client.Key != "" && strings.Contains(question, client.Key) {
			err = errors.New(locales.AiKeyFieldOnly())
		}
		if err != nil {
			finish("", err)
			return
		}
		messages := []ai.Message{{Role: "system", Content: ai.CommandPrompt}, {Role: "user", Content: question}}
		a.aiStream(ctx, client, messages, 12<<10, func(text string, done bool, err error) {
			if done {
				finish(text, err)
			}
		})
	})
}
