package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type systemRunner func(context.Context, string, io.Reader, func(string)) (string, string, error)

func systemReader(s *Session) systemRunner {
	if s.server.services.run != nil {
		return s.server.services.run
	}
	client := s.client
	return func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		if input != nil {
			return monitor.RunSystemInput(ctx, client, script, input, progress)
		}
		if progress != nil {
			return monitor.StreamSystem(ctx, client, script, progress)
		}
		return monitor.RunScript(ctx, client, script)
	}
}

func (a *App) systemRoot(s *Session) systemRunner {
	return a.systemRootRunner(s, false)
}

func (a *App) systemRootRunner(s *Session, interrupt bool) systemRunner {
	priv := a.sessionPrivilege(s).runner
	return func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		return priv.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input, Progress: progress, Interrupt: interrupt})
	}
}

func systemReady(s *Session, client *ssh.Client) bool {
	return s.status == statusConnected && s.client == client && execAllowed(s.Host)
}

func systemContext(s *Session) context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func systemUser(s *Session) string {
	if s.client != nil {
		return s.client.User()
	}
	return s.Host.User
}

func systemSelect(c *ui.Context, index *int, label string, choices []string) bool {
	*index = min(max(*index, 0), len(choices)-1)
	value := choices[*index]
	ui.Select(c, &value, choices).Label(label).MinWidth(0).MaxWidth(220)
	for next, choice := range choices {
		if choice == value && next != *index {
			*index = next
			return true
		}
	}
	return false
}

func systemError(output string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword) {
		return locales.PrivNeedsRoot()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, monitor.ErrPrivilegeCancelled) {
		return locales.SvcCancelled()
	}
	if detail := strings.TrimSpace(output); detail != "" {
		return systemRedact(detail)
	}
	return systemRedact(err.Error())
}

type systemDialog struct {
	client                     *ssh.Client
	title, target, consequence string
	output, failure            string
	busy, done, rootDenied     bool
	cancel                     context.CancelFunc
	confirm                    func()
	body                       func(*ui.Context, float32)
	back                       func()
}

func (a *App) systemDialogView(c *ui.Context, s *Session, slot **systemDialog) {
	dialog := *slot
	if dialog == nil {
		return
	}
	open := systemReady(s, dialog.client)
	if open {
		ui.Modal(c, &open, func() {
			width, height := c.Size()
			ui.Column(c).Width(min(max(width-56, 260), 680)).Gap(9).Children(func() {
				ui.Text(c, dialog.title).FontSize(17).Bold()
				if dialog.target != "" {
					ui.Scroll(c).FillWidth().MaxHeight(110).Children(func() {
						ui.Text(c, dialog.target).Selectable().FillWidth()
					})
				}
				if dialog.consequence != "" && !dialog.done && !dialog.busy {
					ui.Text(c, dialog.consequence)
				}
				if dialog.body != nil && !dialog.done {
					dialog.body(c, height)
				}
				if dialog.busy {
					ui.Spinner(c).Label(locales.SvcWorking())
				}
				if dialog.output != "" || dialog.failure != "" {
					output := strings.TrimSpace(strings.Join([]string{dialog.output, dialog.failure}, "\n"))
					ui.TextArea(c, &output).ReadOnly(true).Font("monospace").FillWidth().Height(min(220, height/4)).Label(locales.SvcOutput())
				}
				if dialog.rootDenied {
					privilegeHelp(c, systemUser(s))
				}
				ui.Row(c).Justify(ui.End).Gap(8).Wrap().Children(func() {
					label := locales.Close()
					if dialog.confirm != nil || dialog.busy {
						label = locales.Cancel()
					}
					if ui.Button(c, label).Clicked() {
						if dialog.busy && dialog.body == nil && dialog.cancel != nil {
							dialog.cancel()
						} else if dialog.back != nil && dialog.confirm != nil {
							dialog.back()
						} else {
							open = false
						}
					}
					if dialog.confirm != nil && !dialog.busy && ui.PrimaryButton(c, locales.SvcConfirm()).Clicked() {
						confirm := dialog.confirm
						dialog.confirm = nil
						confirm()
					}
				})
			})
		})
	}
	if !open && *slot == dialog {
		if dialog.cancel != nil {
			dialog.cancel()
		}
		*slot = nil
	}
}

func (a *App) systemWork(s *Session, dialog *systemDialog, run systemRunner, script string, input io.Reader, refresh func(*Session)) {
	ctx, cancel := context.WithTimeout(systemContext(s), 5*time.Minute)
	dialog.busy, dialog.cancel, dialog.body, dialog.consequence = true, cancel, nil, ""
	go func() {
		defer cancel()
		pending := newSystemChunks(1000)
		flush := func() {
			lines := pending.take(false)
			if len(lines) != 0 {
				a.post(func() { dialog.output = systemTail(dialog.output+strings.Join(lines, "\n")+"\n", 65536) })
			}
		}
		finished := make(chan struct{})
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			tick := time.NewTicker(150 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					flush()
				case <-finished:
					return
				}
			}
		}()
		out, stderr, err := run(ctx, script, input, pending.add)
		close(finished)
		<-drained
		last := strings.Join(pending.take(true), "\n")
		a.post(func() {
			dialog.busy, dialog.done = false, true
			if dialog.output == "" && last == "" {
				dialog.output = systemTail(systemRedact(out), 65536)
			} else {
				dialog.output = systemTail(dialog.output+last, 65536)
			}
			if err == nil && strings.TrimSpace(dialog.output) == "" {
				dialog.output = locales.SvcDone()
			}
			dialog.failure = systemError(stderr, err)
			if err == nil && strings.TrimSpace(stderr) != "" {
				dialog.output = systemTail(strings.TrimSpace(dialog.output)+"\n"+systemRedact(stderr), 65536)
			}
			dialog.rootDenied = errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword)
			if refresh != nil && systemReady(s, dialog.client) && !errors.Is(err, monitor.ErrPrivilegeDenied) && !errors.Is(err, monitor.ErrPrivilegeWrongPassword) && !errors.Is(err, monitor.ErrPrivilegeCancelled) {
				refresh(s)
			}
		})
	}()
}

func systemTail(text string, size int) string {
	if len(text) > size {
		text = strings.ToValidUTF8(text[len(text)-size:], "�")
	}
	return text
}

func (a *App) systemFileEditor(s *Session, slot **systemDialog, file, title, consequence string, refresh func(*Session)) {
	script, err := monitor.SystemFileReadCommand(file)
	if err != nil {
		return
	}
	dialog := &systemDialog{client: s.client, title: title, target: file, busy: true}
	*slot = dialog
	ctx, cancel := context.WithTimeout(systemContext(s), time.Minute)
	dialog.cancel = cancel
	run := a.systemRoot(s)
	go func() {
		defer cancel()
		out, stderr, err := run(ctx, script, nil, nil)
		a.post(func() {
			if *slot != dialog {
				return
			}
			dialog.busy = false
			state, original, _ := strings.Cut(out, "\n")
			state = strings.TrimSuffix(state, "\r")
			if err != nil || state != "present" && state != "missing" || len(original) > monitor.SystemEditLimit || strings.ContainsRune(original, 0) || !utf8.ValidString(original) || systemRedact(original) != original {
				dialog.failure = systemError(stderr, err)
				if dialog.failure == "" {
					dialog.failure = locales.SvcCannotEdit()
				}
				dialog.rootDenied = errors.Is(err, monitor.ErrPrivilegeDenied)
				return
			}
			text := original
			var editor func(*ui.Context, float32)
			editor = func(c *ui.Context, height float32) {
				ui.TextArea(c, &text).Font("monospace").FillWidth().Height(min(350, height/2)).Label(title)
				valid := len(text) <= monitor.SystemEditLimit && !strings.ContainsRune(text, 0) && systemRedact(text) == text
				if !valid {
					ui.Text(c, locales.SvcCannotEdit()).TextColor(theme.Warning)
				}
				if ui.PrimaryButton(c, locales.Save()).Disabled(text == original || !valid).AlignSelf(ui.End).Clicked() {
					write, _ := monitor.SystemFileWriteCommand(file, original, state == "present")
					dialog.body, dialog.consequence = nil, consequence
					dialog.back = func() { dialog.body, dialog.consequence, dialog.confirm = editor, "", nil }
					dialog.confirm = func() { a.systemWork(s, dialog, run, write, strings.NewReader(text), refresh) }
				}
			}
			dialog.body = editor
		})
	}()
}
