package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type caddyState struct {
	client       *ssh.Client
	backend      caddyBackend
	run          monitor.CaddyRunner
	install      monitor.CaddyInstall
	generation   uint64
	loaded       bool
	loading      bool
	failure      string
	dialog       *systemDialog
	detectCancel context.CancelFunc
}

func (a *App) caddyAccess(s *Session) (caddyBackend, monitor.CaddyRunner) {
	state := &s.caddy.state
	backend, run := state.backend, state.run
	if backend == nil {
		backend = caddyMonitorBackend{}
	}
	if run == nil {
		privilege := a.sessionPrivilege(s).runner
		run = func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
			return privilege.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input, Progress: progress, ScriptStdin: true, Interrupt: progress != nil})
		}
	}
	return backend, run
}

func (a *App) caddyConnection(s *Session) {
	state := &s.caddy.state
	if state.client != s.client {
		if dialog := state.dialog; dialog != nil && dialog.cancel != nil {
			dialog.cancel()
		}
		state.client, state.dialog = s.client, nil
		a.invalidateCaddy(s)
	}
}

func (a *App) invalidateCaddy(s *Session) {
	state := &s.caddy.state
	if state.detectCancel != nil {
		state.detectCancel()
		state.detectCancel = nil
	}
	state.generation++
	state.loaded, state.loading, state.failure = false, false, ""
	state.install = monitor.CaddyInstall{}
	a.stopPoll(&s.caddy.logs.poll)
	s.caddy.overview = caddyOverviewPage{}
	s.caddy.config = caddyConfigPage{}
	s.caddy.certs = caddyCertsPage{}
	s.caddy.logs = caddyLogsPage{}
	s.caddy.install = caddyInstallPage{}
}

func (a *App) detectCaddyPanel(s *Session) {
	state := &s.caddy.state
	if state.loaded || state.loading || !systemReady(s, s.client) || s.client == nil && state.backend == nil {
		return
	}
	state.loading = true
	state.detectCancel = caddyRead(a, s, func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, _ monitor.CaddyInstall) (monitor.CaddyInstall, error) {
		return backend.detect(ctx, run)
	}, func(install monitor.CaddyInstall, err error) {
		state.detectCancel = nil
		state.loading, state.loaded = false, true
		state.install, state.failure = install, caddyError(err)
	})
}

func caddyRead[T any](a *App, s *Session, read func(caddyBackend, context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (T, error), finish func(T, error)) context.CancelFunc {
	state := &s.caddy.state
	client, generation, install := s.client, state.generation, state.install
	backend, run := a.caddyAccess(s)
	ctx, cancel := context.WithTimeout(systemContext(s), time.Minute)
	go func() {
		defer cancel()
		result, err := read(backend, ctx, run, install)
		a.post(func() {
			if systemReady(s, client) && generation == state.generation {
				finish(result, err)
			}
		})
	}()
	return cancel
}

func caddyError(err error) string {
	switch {
	case errors.Is(err, monitor.ErrCaddyNotImplemented):
		return locales.CpanelUnavailable()
	case errors.Is(err, monitor.ErrCaddyChanged):
		return locales.CpanelChanged()
	case errors.Is(err, errCaddyPanelTarget):
		return locales.CpanelNoTarget()
	case errors.Is(err, monitor.ErrCaddyAdminOff):
		return locales.CpanelAdminOff()
	default:
		return systemError("", err)
	}
}

func (a *App) caddyDetected(c *ui.Context, s *Session, allowMissing bool) bool {
	a.caddyConnection(s)
	a.detectCaddyPanel(s)
	state := &s.caddy.state
	if !state.loaded {
		ui.Spinner(c).Label(locales.CpanelDetecting()).Padding(8)
		return false
	}
	if state.failure != "" {
		caddyNotice(c, state.failure)
		return false
	}
	if !allowMissing && state.install.Method == monitor.CaddyNotInstalled {
		overviewMessage(c, "globe", locales.CpanelNotInstalled(), func() {
			if ui.PrimaryButton(c, locales.CpanelInstall()).Clicked() {
				a.openCaddyPage(s, caddySecInstall)
			}
		})
		return false
	}
	return true
}

type caddyOperation func(context.Context, caddyBackend, monitor.CaddyRunner, monitor.CaddyInstall, func(string)) (string, error)

func (a *App) caddyConfirm(s *Session, title, target, note string, work caddyOperation) *systemDialog {
	dialog := &systemDialog{client: s.client, title: title, target: target, consequence: note}
	dialog.confirm = func() { a.caddyWork(s, dialog, work) }
	s.caddy.state.dialog = dialog
	return dialog
}

func (a *App) caddyWork(s *Session, dialog *systemDialog, work caddyOperation) {
	backend, run := a.caddyAccess(s)
	install := s.caddy.state.install
	wrapped := func(ctx context.Context, _ string, _ io.Reader, progress func(string)) (string, string, error) {
		buffer := newSystemChunks(200)
		flush := func(final bool) {
			for _, line := range buffer.take(final) {
				if !strings.HasPrefix(line, "CADDY_TX ") {
					progress(line + "\n")
				}
			}
		}
		out, err := work(ctx, backend, run, install, func(chunk string) {
			buffer.add(chunk)
			flush(false)
		})
		flush(true)
		if out != "" {
			progress(systemRedact(out) + "\n")
		}
		return "", caddyError(err), err
	}
	a.systemWork(s, dialog, wrapped, "", nil, func(s *Session) {
		a.invalidateCaddy(s)
		a.detectCaddyPanel(s)
	})
}
