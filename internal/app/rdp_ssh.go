package app

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/rdp"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

type rdpSSHContext struct{ context.Context }

func (a *App) connectedTunnelSession(h *config.Host) *Session {
	for _, session := range a.sessions {
		_, desktopOnly := session.ctx.(rdpSSHContext)
		if !desktopOnly && session.status == statusConnected && session.client != nil && hostMatchesPrivilege(session.Host, h) {
			return session
		}
	}
	return nil
}

func (a *App) openRDPThroughSSH(desktop *config.Host) {
	opts := config.RDPOf(desktop)
	host := a.hosts.Host(opts.SSHHostID)
	if !host.IsSSH() || host.Deleted() {
		a.rdpFailed(desktop, errors.New(locales.RdpsshMissing()))
		return
	}
	if opts.GatewayUsage != config.GatewayOff {
		a.rdpFailed(desktop, errors.New(locales.RdpsshNoGateway()))
		return
	}
	target, err := config.RDPSSHTarget(desktop)
	if err != nil {
		a.rdpFailed(desktop, errors.New(locales.RdpsshBadTarget()))
		return
	}
	if session := a.connectedTunnelSession(host); session != nil {
		a.startRDPForward(desktop, target, session, false)
		return
	}
	if a.readChainSecrets(host, func() { a.openRDPThroughSSH(desktop) }) {
		return
	}
	if need := a.missingSecret(host); need != nil {
		a.askSecret(need, func() { a.openRDPThroughSSH(desktop) })
		a.pwCancel = func() { a.rdpFailed(desktop, errors.New(locales.RdpsshCancelled())) }
		return
	}
	a.dialRDPSSH(desktop, target, host)
}

func (a *App) dialRDPSSH(desktop *config.Host, target string, host *config.Host) {
	session := newSession(host)
	session.name = locales.RdpsshSession(desktop.Title())
	session.ctx, session.cancel = context.WithCancel(context.Background())
	session.ctx = rdpSSHContext{session.ctx}
	life := session.ctx
	options, err := a.dialOptionsFor(life, host, a.passwords[host.ID], func(question *sshx.HostKeyQuestion) {
		a.post(func() {
			if life.Err() != nil {
				question.Answer <- false
				return
			}
			session.hostKeyQ = question
		})
	}, true)
	if err != nil {
		session.cancel()
		a.rdpFailed(desktop, errors.New(locales.RdpsshDialFailed(host.Title(), describeError(err))))
		return
	}
	options.KeepAlive = a.keepAlive()
	passphrase := a.storedPassphrase(host)
	copyHost := host.Clone()
	a.sessions = append(a.sessions, session)
	a.notify(locales.RdpsshConnecting(host.Title()))
	go func() {
		ctx, cancel := context.WithTimeout(life, 90*time.Second)
		defer cancel()
		if passphrase != nil {
			options.Passphrase = passphrase(ctx)
		}
		remote, err := connectKeysRemote(ctx, copyHost, options)
		a.post(func() {
			if life.Err() != nil {
				if remote.client != nil {
					remote.close()
				}
				return
			}
			if err != nil {
				a.closeSession(session)
				if need := a.refusedSecret(host, err); need != nil && !a.pwOpen && !errors.Is(err, sshx.ErrVerificationCode) {
					delete(a.secrets.untried, need.key)
					a.forgetSecret(need)
					a.secrets.markRead(need.store)
					a.askSecret(need, func() { a.openRDPThroughSSH(desktop) })
					a.pwCancel = func() { a.rdpFailed(desktop, errors.New(locales.RdpsshCancelled())) }
					a.pwErr = err
					return
				}
				a.rdpFailed(desktop, errors.New(locales.RdpsshDialFailed(copyHost.Title(), describeError(err))))
				return
			}
			session.client, session.status = remote.client, statusConnected
			a.loggedIn(host)
			a.startRDPForward(desktop, target, session, true)
		})
	}()
}

func (a *App) startRDPForward(desktop *config.Host, target string, session *Session, owned bool) {
	client, life := session.client, session.ctx
	if life == nil {
		life = context.Background()
	}
	name, port, _ := net.SplitHostPort(target)
	number, _ := strconv.Atoi(port)
	spec := config.Tunnel{ID: config.NewTunnelID(), Kind: config.TunnelLocal, Name: desktop.Title(), TargetHost: name, TargetPort: number, Disabled: true}
	request := rdp.Request{Host: desktop.Clone(), Password: a.passwords[desktop.ID]}
	a.notify(locales.RdpsshPreparing(target))
	go func() {
		ctx, cancel := context.WithTimeout(life, 8*time.Second)
		probe, err := client.DialContext(ctx, "tcp", target)
		cancel()
		if err != nil {
			a.post(func() {
				if session.client == client {
					if owned {
						a.closeSession(session)
					}
					a.rdpFailed(desktop, errors.New(locales.RdpsshTargetFailed(target, err.Error())))
				}
			})
			return
		}
		probe.Close()
		forward, err := sshx.StartTunnel(client, spec, nil)
		a.post(func() {
			if session.client != client || life.Err() != nil {
				if forward != nil {
					forward.Stop()
				}
				return
			}
			if err != nil {
				if owned {
					a.closeSession(session)
				}
				a.rdpFailed(desktop, errors.New(locales.RdpsshTunnelFailed(describeTunnelError(err))))
				return
			}
			a.attachRDPForward(desktop, request, session, client, spec, forward, owned)
		})
	}()
}

func (a *App) attachRDPForward(desktop *config.Host, req rdp.Request, session *Session, client *ssh.Client, spec config.Tunnel, forward *sshx.Tunnel, owned bool) {
	port := forward.Addr().(*net.TCPAddr).Port
	run := &tunnelRun{spec: spec, fwd: forward}
	session.tunnels.runs = append(session.tunnels.runs, run)
	stop := func() {
		forward.Stop()
		if index := slices.Index(session.tunnels.runs, run); index >= 0 {
			session.tunnels.runs = slices.Delete(session.tunnels.runs, index, index+1)
			if owned && session.client == client {
				a.closeSession(session)
			}
		}
	}
	go func() { client.Wait(); forward.Stop() }()
	go func() { <-forward.Done(); a.post(stop) }()
	if owned || !rdp.TracksClient() {
		a.openPanelTab(session, tabTunnels)
		if panel := a.panelRowOf(session); panel != nil {
			panel.panelTab = tabTunnels
		}
	}
	a.launchRDP(desktop, rdp.WithSSHForward(req, port), stop)
}
