package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type opsRunner func(context.Context, string, func(string)) (string, string, error)

func (a *App) opsNarrow() bool { return a.panelOnRight() && !a.current().isPanelTab() }

func serverCompactSections(c *ui.Context, section *int) {
	names := serverSectionNames()
	ui.Button(c, names[*section]+" ▾").Label(locales.TabSystem()).Grow(1).MinWidth(0).PopoverMenuButton(func(menu *ui.Menu) {
		for index, name := range names {
			if menu.Item(name).Radio(index == *section).Chosen() {
				*section = index
			}
		}
	})
}

type opsConnection struct {
	ctx     context.Context
	normal  opsRunner
	root    opsRunner
	current func() bool
}

type opsState struct {
	client  *ssh.Client
	life    context.Context
	runner  opsRunner
	loaded  bool
	reading bool
	err     error
	output  string
	dialog  *opsDialog
}

func (state *opsState) changed(s *Session) bool {
	return state.client != s.client || state.life != s.ctx
}

func (state *opsState) reset(s *Session) opsState {
	return opsState{client: s.client, life: s.ctx, runner: state.runner}
}

func (a *App) opsConnection(s *Session, state *opsState) (opsConnection, bool) {
	if s.status != statusConnected || s.Host == nil || !execAllowed(s.Host) || s.client == nil && state.runner == nil {
		return opsConnection{}, false
	}
	return a.opsConnectionFor(s, state), true
}

func (a *App) opsConnectionFor(s *Session, state *opsState) opsConnection {
	client, life := s.client, s.ctx
	ctx := life
	if ctx == nil {
		ctx = context.Background()
	}
	run := state.runner
	if run == nil {
		run = func(ctx context.Context, script string, progress func(string)) (string, string, error) {
			return monitor.RunOpsStream(ctx, client, script, progress)
		}
	}
	priv := a.sessionPrivilege(s)
	return opsConnection{
		ctx: ctx, normal: run,
		root: func(ctx context.Context, script string, progress func(string)) (string, string, error) {
			return priv.runner.Run(ctx, script, monitor.PrivilegeOptions{Progress: progress})
		},
		current: func() bool { return s.client == client && s.ctx == life && s.status == statusConnected },
	}
}

type opsDialog struct {
	title, description, action string
	unprivileged               bool
	build                      func(*ui.Context) (string, string)
	prepare                    func(context.Context, opsConnection, string) (string, error)
	refresh                    func()
	work                       *opsWork
}

type opsWork struct {
	output string
	err    error
	done   bool
}

func opsTail(text string) string {
	const limit = 24 << 10
	if len(text) > limit {
		text = text[len(text)-limit:]
	}
	return strings.Map(func(char rune) rune {
		if char != '\n' && char != '\t' && char != '\r' && unicode.IsControl(char) {
			return -1
		}
		return char
	}, text)
}

var opsURLCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/@]+@`)
var opsSecretField = regexp.MustCompile(`(?i)(\b(?:password|passwd|passphrase|token|api[_-]?key|private[_-]?key)\s*[:=])\s*\S+`)

func opsSafeOutput(text, login string) string {
	if login != "" {
		text = strings.ReplaceAll(text, login, "[redacted]")
	}
	text = opsURLCredentials.ReplaceAllString(text, `${1}[redacted]@`)
	text = opsSecretField.ReplaceAllString(text, `${1} [redacted]`)
	if strings.Contains(text, "PRIVATE KEY-----") {
		return "[redacted]"
	}
	return opsTail(text)
}

func opsReadError(err error, login string) error {
	if err == nil {
		return nil
	}
	if cleaned := opsSafeOutput(err.Error(), login); cleaned != err.Error() {
		return errors.New(cleaned)
	}
	return err
}

func opsError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, monitor.ErrDiskTimeout):
		return locales.SysInfoTimeout()
	case errors.Is(err, context.Canceled):
		return locales.DuCanceled()
	case errors.Is(err, monitor.ErrOpsInvalid):
		return locales.DuInvalid()
	case errors.Is(err, monitor.ErrPrivilegeDenied), errors.Is(err, monitor.ErrPrivilegeCancelled), errors.Is(err, monitor.ErrPrivilegeWrongPassword):
		return locales.PrivNeedsRoot()
	default:
		return opsTail(err.Error())
	}
}

func opsButton(c *ui.Context, label, why string) bool {
	button := ui.Button(c.Key(label), label).Disabled(why != "")
	if why != "" {
		button.Tooltip(why)
	}
	return button.Clicked() && why == ""
}

func opsMenuItem(menu *ui.Menu, label, why string) bool {
	item := menu.Item(label).Disabled(why != "")
	if why != "" {
		item.Subtitle(why)
	}
	return item.Chosen() && why == ""
}

func opsToolbar(c *ui.Context, items func()) {
	ui.Row(c).FillWidth().Wrap().Padding(5, 10).Gap(6).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(items)
}

func (state *opsState) note(c *ui.Context) {
	if state.reading {
		ui.Spinner(c).Size(14, 14).Label(locales.DuReading())
	}
	if state.err != nil {
		message := opsError(state.err)
		ui.Text(c, message).TextColor(theme.Danger).SingleLine().Shrink(1).MinWidth(40).Tooltip(strings.TrimSpace(message + "\n" + state.output))
	}
}

func (state *opsState) readonlyNote(c *ui.Context, s *Session) {
	if state.err == nil {
		return
	}
	if errors.Is(state.err, monitor.ErrPrivilegeDenied) || errors.Is(state.err, monitor.ErrPrivilegeCancelled) || errors.Is(state.err, monitor.ErrPrivilegeWrongPassword) {
		ui.Scroll(c).FillWidth().MaxHeight(150).Padding(8, 12).Children(func() { privilegeHelp(c, s.Host.User) })
	}
}

func (a *App) opsWorkDialog(c *ui.Context, s *Session, state *opsState) {
	dialog := state.dialog
	if dialog == nil {
		return
	}
	open := true
	ui.Modal(c.Key("ops-confirm"), &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-48, 220), 560)).Gap(10).Children(func() {
			ui.Text(c, dialog.title).FontSize(16).Bold()
			widgets.Muted(c, s.Host.Title()).SingleLine()
			if dialog.work != nil {
				a.opsWorkResult(c, s, dialog.work, height, &open)
				return
			}
			if dialog.description != "" {
				ui.Scroll(c).FillWidth().MaxHeight(max(height/3, 74)).Children(func() {
					ui.Text(c, dialog.description).Selectable()
				})
			}
			script, reason := dialog.build(c)
			if reason != "" {
				widgets.Muted(c, reason).FontSize(12)
			}
			ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, dialog.action).Disabled(script == "" || reason != "").Clicked() && open && script != "" && reason == "" {
					a.opsStart(s, state, dialog, script)
				}
			})
		})
	})
	if !open && (dialog.work == nil || dialog.work.done) {
		state.dialog = nil
	}
}

func (a *App) opsWorkResult(c *ui.Context, s *Session, work *opsWork, height float32, open *bool) {
	switch {
	case !work.done:
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c).Size(16, 16).Label(locales.DuWorking())
			ui.Text(c, locales.DuWorking())
		})
	case work.err != nil:
		if errors.Is(work.err, monitor.ErrPrivilegeDenied) || errors.Is(work.err, monitor.ErrPrivilegeCancelled) || errors.Is(work.err, monitor.ErrPrivilegeWrongPassword) {
			privilegeHelp(c, s.Host.User)
		} else {
			ui.Text(c, opsError(work.err)).TextColor(theme.Danger)
		}
	default:
		ui.Text(c, locales.DuFinished()).TextColor(theme.Success)
	}
	if work.output != "" {
		ui.Scroll(c).FillWidth().MaxHeight(max(height/2, 90)).Padding(7, 9).Radius(5).Background(colors.note).Children(func() {
			ui.Text(c, work.output).Selectable().Font("monospace").FontSize(12)
		})
	}
	if work.done && ui.Button(c, locales.Close()).AlignSelf(ui.End).Clicked() {
		*open = false
	}
}

func opsStatic(script string) func(*ui.Context) (string, string) {
	return func(*ui.Context) (string, string) { return script, "" }
}

func (a *App) opsStart(s *Session, state *opsState, dialog *opsDialog, script string) {
	conn, ok := a.opsConnection(s, state)
	if !ok || state.dialog != dialog || dialog.work != nil || state.reading {
		return
	}
	work := &opsWork{}
	dialog.work = work
	login := a.passwords[s.Host.ID]
	go func() {
		var working, pending, stdout, stderr string
		var err error
		var last time.Time
		var suppress bool
		progress := func(chunk string) {
			if suppress {
				return
			}
			pending += chunk
			if strings.Contains(pending, "PRIVATE KEY-----") {
				pending, working, suppress = "", "[redacted]", true
				return
			}
			end := strings.LastIndexByte(pending, '\n')
			if end < 0 {
				if len(pending) > 64<<10 {
					pending = ""
				}
				return
			}
			working = opsTail(working + opsSafeOutput(pending[:end+1], login))
			pending = pending[end+1:]
			if time.Since(last) < 110*time.Millisecond {
				return
			}
			last = time.Now()
			text := working
			a.post(func() {
				if state.dialog == dialog && !work.done {
					work.output = text
				}
			})
		}
		if dialog.prepare != nil {
			ctx, cancel := context.WithTimeout(conn.ctx, 2*time.Minute)
			script, err = dialog.prepare(ctx, conn, script)
			cancel()
		}
		if err == nil {
			run := conn.root
			if dialog.unprivileged {
				run = conn.normal
			}
			stdout, stderr, err = run(conn.ctx, script, progress)
		}
		if stdout == "" {
			stdout = working + pending
		}
		output := strings.TrimSpace(opsSafeOutput(stdout+"\n"+stderr, login))
		err = opsReadError(err, login)
		a.post(func() {
			if state.dialog != dialog {
				return
			}
			work.output, work.err, work.done = output, err, true
			if conn.current() && dialog.refresh != nil {
				dialog.refresh()
			}
		})
	}()
}

func opsBlocked(state *opsState) string {
	if state.reading || state.dialog != nil && state.dialog.work != nil && !state.dialog.work.done {
		return locales.DuWorking()
	}
	return ""
}

func opsTextView(c *ui.Context, text, empty string) {
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(10, 14).Children(func() {
		if strings.TrimSpace(text) == "" {
			widgets.Muted(c, empty)
			return
		}
		ui.Text(c, text).Font("monospace").FontSize(12).Selectable()
	})
}
