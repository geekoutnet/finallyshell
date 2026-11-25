package app

import (
	"context"
	"errors"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerInputRunner func(context.Context, *ssh.Client, monitor.DockerInput, func(string)) (string, error)

type dockerExec struct {
	command dockerRunner
	input   dockerInputRunner
	events  dockerRunner
}

func (a *App) dockerExec(s *Session, direct dockerRunner, input dockerInputRunner, forceRoot bool) dockerExec {
	state := &s.overview.docker
	if state.run != nil {
		direct = state.run
	}
	if input == nil {
		input = func(ctx context.Context, client *ssh.Client, input monitor.DockerInput, report func(string)) (string, error) {
			return input.Run(ctx, client, report)
		}
	}
	if !state.d.Root && !forceRoot {
		return dockerExec{command: direct, input: input, events: direct}
	}
	priv := a.sessionPrivilege(s)
	run := priv.runner.RunCached
	if forceRoot {
		run = priv.runner.Run
	}
	client, connection := s.client, s.ctx
	failed := func(err error) {
		if !errors.Is(err, monitor.ErrPrivilegeNotCached) {
			return
		}
		a.post(func() {
			if s.client == client && s.ctx == connection && state.d.Root && !state.loading {
				state.d.Root, state.d.State = false, monitor.DockerNeedsRoot
				state.autoRoot = false
				state.err = nil
			}
		})
	}
	return dockerExec{
		command: func(ctx context.Context, _ *ssh.Client, script string, report func(string)) (string, error) {
			out, err := monitor.RunDockerRoot(ctx, run, script, report)
			failed(err)
			return out, err
		},
		input: func(ctx context.Context, _ *ssh.Client, input monitor.DockerInput, report func(string)) (string, error) {
			out, err := input.RunRoot(ctx, run, report)
			failed(err)
			return out, err
		},
		events: func(ctx context.Context, _ *ssh.Client, script string, report func(string)) (string, error) {
			out, err := monitor.StreamDockerEventsRoot(ctx, run, script, report)
			failed(err)
			return out, err
		},
	}
}

func dockerPrivilegeFailed(err error) bool {
	return errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword)
}

func dockerDeniedText(d *monitor.Docker) string {
	if d.Tool == "podman" {
		return locales.DockerPrivDenied(dockerName(d))
	}
	return locales.DockerDenied()
}

func (a *App) dockerPrivilegeButton(c *ui.Context, s *Session) {
	if ui.PrimaryButton(c, locales.DockerPrivUseRoot()).AlignSelf(ui.Start).Disabled(s.overview.docker.loading).Clicked() {
		a.readDockerAccess(s, true)
	}
}

func dockerPrivilegeHelp(c *ui.Context, s *Session) {
	user := s.Host.User
	if s.client != nil {
		user = s.client.User()
	}
	privilegeHelp(c, user)
	if s.overview.docker.d.Tool == "docker" {
		ui.Text(c, locales.DockerPrivGroupHint()).FontSize(12)
		command := "usermod -aG docker " + user
		if !monitor.ValidName(user) || strings.HasPrefix(user, "-") {
			command = "usermod -aG docker -- " + composeShellArg(user)
		}
		ui.Text(c, command).Font("monospace").Selectable().FontSize(12)
	}
}
