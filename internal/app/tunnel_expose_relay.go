package app

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

type tunnelRelayRunner func(context.Context, string, io.Reader, func(string)) (string, string, error)

type exposureForward func(*ssh.Client, config.Tunnel, func()) (*sshx.Tunnel, error)

func startExposureRelay(ctx context.Context, client *ssh.Client, spec config.Tunnel, tool string, normal, root tunnelRelayRunner, startForward exposureForward, notify func()) (*sshx.Tunnel, <-chan error, error) {
	if !spec.Kind.IsRemote() || len(spec.AllowFrom) != 0 {
		return nil, nil, errors.New(locales.TexpRelayUnsupported())
	}
	if _, err := monitor.TunnelRelayScript(tool, spec.ListenHost, spec.ListenPort, 1+spec.ListenPort%65535); err != nil {
		return nil, nil, err
	}
	inner := spec
	inner.ListenHost, inner.ListenPort, inner.ServerRelay = config.LoopbackHost, 0, false
	forward, err := startForward(client, inner, notify)
	if err != nil {
		return nil, nil, err
	}
	port := forward.Addr().(*net.TCPAddr).Port
	script, err := monitor.TunnelRelayScript(tool, spec.ListenHost, spec.ListenPort, port)
	if err != nil {
		forward.Stop()
		return nil, nil, err
	}
	run := normal
	if spec.ListenPort < 1024 {
		run = root
	}
	if run == nil {
		forward.Stop()
		return nil, nil, monitor.ErrPrivilegeDenied
	}
	if err := ctx.Err(); err != nil {
		forward.Stop()
		return nil, nil, err
	}
	start, stop := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	ready := make(chan struct{})
	done := make(chan error, 1)
	var signal sync.Once
	go func() {
		defer stop()
		var progress string
		_, stderr, err := run(start, script, reader, func(chunk string) {
			progress += chunk
			for strings.Contains(progress, "\n") {
				var line string
				line, progress, _ = strings.Cut(progress, "\n")
				if strings.TrimSpace(line) == "FINALLYSHELL_RELAY_READY" {
					signal.Do(func() { close(ready) })
				}
			}
			if len(progress) > 4096 {
				progress = ""
			}
		})
		writer.Close()
		if err == nil {
			err = errors.New(locales.TexpRelayExited())
		} else if detail := strings.TrimSpace(opsTail(stderr)); detail != "" {
			err = errors.New(detail)
		}
		done <- err
		forward.Stop()
	}()
	go func() {
		select {
		case <-start.Done():
			forward.Stop()
		case <-forward.Done():
		}
		writer.Close()
		timer := time.AfterFunc(2*time.Second, stop)
		<-start.Done()
		timer.Stop()
		reader.Close()
	}()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case <-ready:
		return forward, done, nil
	case err := <-done:
		forward.Stop()
		return nil, nil, err
	case <-timer.C:
		stop()
		forward.Stop()
		return nil, nil, context.DeadlineExceeded
	case <-ctx.Done():
		stop()
		forward.Stop()
		return nil, nil, ctx.Err()
	}
}

func (a *App) runExposureRelay(session *Session, run *tunnelRun, spec config.Tunnel, tool string) {
	if session == nil || session.client == nil || !execAllowed(session.Host) {
		return
	}
	client, privilege := session.client, a.sessionPrivilege(session)
	login := a.passwords[session.Host.ID]
	run.stop()
	run.spec, run.starting = spec, true
	generation := run.gen
	normal := func(ctx context.Context, script string, input io.Reader, report func(string)) (string, string, error) {
		return monitor.RunSystemInput(ctx, client, script, input, report)
	}
	root := func(ctx context.Context, script string, input io.Reader, report func(string)) (string, string, error) {
		if privilege.runner.CachedMethod() == 0 {
			if _, _, err := privilege.runner.Run(ctx, "'true'", monitor.PrivilegeOptions{}); err != nil {
				return "", "", err
			}
		}
		return privilege.runner.RunCached(ctx, script, monitor.PrivilegeOptions{Stdin: input, Progress: report, Interrupt: true})
	}
	go func() {
		start := func(_ *ssh.Client, inner config.Tunnel, _ func()) (*sshx.Tunnel, error) {
			return a.startRelayInner(privilege.life, session, client, run, generation, inner)
		}
		ctx, cancel := context.WithTimeout(privilege.life, 45*time.Second)
		out, _, err := privilege.runner.Run(ctx, monitor.GatewayPortsReadScript, monitor.PrivilegeOptions{})
		cancel()
		var forward *sshx.Tunnel
		var exited <-chan error
		if err == nil {
			check, parseErr := monitor.ParseGatewayPorts(out)
			if parseErr != nil || check.Match || check.Mode == "yes" {
				err = errors.New(locales.TexpRelayUnsupported())
			} else {
				forward, exited, err = startExposureRelay(privilege.life, client, spec, tool, normal, root, start, nil)
			}
		}
		a.post(func() {
			if run.gen != generation || session.client != client {
				if forward != nil {
					forward.Stop()
				}
				return
			}
			run.starting, run.fwd, run.err = false, forward, opsReadError(err, login)
		})
		if exited != nil {
			failure := <-exited
			if forwardErr := forward.Err(); forwardErr != nil {
				failure = forwardErr
			}
			a.post(func() {
				if run.gen == generation && session.client == client {
					run.fwd, run.err = nil, opsReadError(failure, login)
				}
			})
		}
	}()
}

func (a *App) startRelayInner(life context.Context, session *Session, client *ssh.Client, run *tunnelRun, generation int, spec config.Tunnel) (*sshx.Tunnel, error) {
	type result struct {
		forward *sshx.Tunnel
		err     error
	}
	ctx, cancel := context.WithTimeout(life, 20*time.Second)
	defer cancel()
	done := make(chan result, 1)
	inner := &tunnelRun{spec: spec}
	var started bool
	var read func()
	read = func() {
		if ctx.Err() != nil || session.client != client || run.gen != generation {
			inner.stop()
			done <- result{err: context.Canceled}
			return
		}
		if !started {
			a.startTunnel(session, inner)
			started = true
		}
		if !inner.starting {
			done <- result{inner.fwd, inner.err}
			return
		}
		time.AfterFunc(35*time.Millisecond, func() { a.post(read) })
	}
	a.post(read)
	select {
	case found := <-done:
		if found.forward == nil && found.err == nil {
			found.err = errors.New(locales.TexpRelayExited())
		}
		return found.forward, found.err
	case <-ctx.Done():
		a.post(inner.stop)
		return nil, ctx.Err()
	}
}
