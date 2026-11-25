package app

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"reflect"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"

	"golang.org/x/crypto/ssh"
)

type tunnelKey struct{ host, id string }

type tunnelHandle interface {
	Addr() net.Addr
	Stats() sshx.TunnelStats
	Done() <-chan struct{}
	Err() error
	Stop()
}

type tunnelBackground struct {
	spec     config.Tunnel
	handle   tunnelHandle
	cancel   context.CancelFunc
	starting bool
	err      error
	lastErr  error
	blocked  bool
	attempt  int
	retryAt  time.Time
	since    time.Time
	accum    sshx.TunnelStats
	traffic  tunnelTraffic
}

type tunnelBackgroundHost struct {
	route       []*config.Host
	client      *ssh.Client
	life        context.Context
	cancel      context.CancelFunc
	connecting  bool
	probing     bool
	err         error
	attempt     int
	retryAt     time.Time
	connectedAt time.Time
	forwardGate chan struct{}
	secrets     []string
}

type tunnelManager struct {
	runs   map[tunnelKey]*tunnelBackground
	hosts  map[string]*tunnelBackgroundHost
	view   tunnelManagerView
	timer  *time.Timer
	ended  bool
	now    func() time.Time
	jitter func() float64
	dial   func(context.Context, *config.Host, sshx.Options) (*ssh.Client, error)
	start  func(*ssh.Client, config.Tunnel) (tunnelHandle, error)
}

var errTunnelManagerEngine = errors.New("this build cannot apply the tunnel options")
var errTunnelManagerLogin = errors.New("background SSH authentication needs attention")

func (manager *tunnelManager) time() time.Time {
	if manager.now != nil {
		return manager.now()
	}
	return time.Now()
}

func tunnelRetryDelay(attempt int, jitter float64) time.Duration {
	base := min(time.Minute, (2*time.Second)<<uint(min(5, max(0, attempt-1))))
	return min(time.Minute, time.Duration(float64(base)*(0.85+0.30*jitter)))
}

func (manager *tunnelManager) nextRetry(attempt int) time.Time {
	jitter := rand.Float64
	if manager.jitter != nil {
		jitter = manager.jitter
	}
	return manager.time().Add(tunnelRetryDelay(attempt, jitter()))
}

func tunnelManagerHost(host *config.Host) bool { return host.IsSSH() && !host.Deleted() }

func (a *App) tunnelSpec(key tunnelKey) (*config.Host, *config.Tunnel) {
	host := a.hosts.Host(key.host)
	if tunnelManagerHost(host) {
		for index := range host.Tunnels {
			if host.Tunnels[index].ID == key.id {
				return host, &host.Tunnels[index]
			}
		}
	}
	return nil, nil
}

func (a *App) StartBackgroundTunnels() {
	if a.settings.TunnelManager.Autostart() {
		a.ensureTunnelManagerIDs()
		for _, host := range a.hosts.Live() {
			if !tunnelManagerHost(host) {
				continue
			}
			for _, spec := range host.Tunnels {
				if spec.Background {
					a.startBackgroundTunnel(tunnelKey{host.ID, spec.ID})
				}
			}
		}
	}
}

func (a *App) startBackgroundTunnel(key tunnelKey) {
	manager := &a.tmgr
	host, spec := a.tunnelSpec(key)
	if spec == nil || manager.ended || manager.runs[key] != nil {
		return
	}
	if manager.runs == nil {
		manager.runs = make(map[tunnelKey]*tunnelBackground)
		manager.hosts = make(map[string]*tunnelBackgroundHost)
	}
	run := &tunnelBackground{spec: *spec}
	manager.runs[key] = run
	if !tunnelManagerEngineSupports(*spec) {
		run.err, run.lastErr, run.blocked = errTunnelManagerEngine, errTunnelManagerEngine, true
		a.scheduleTunnelManagerTick()
		return
	}
	state := manager.hosts[key.host]
	if state != nil && !a.tunnelRouteMatches(key.host, state.route) {
		a.stopBackgroundHost(key.host)
		manager.runs[key] = run
		state = nil
	}
	if state == nil {
		state = &tunnelBackgroundHost{route: a.tunnelRoute(host), forwardGate: make(chan struct{}, 1)}
		manager.hosts[key.host] = state
		a.dialTunnelHost(key.host, state)
	} else if state.client != nil {
		a.listenBackgroundTunnel(key, run, state)
	}
	a.scheduleTunnelManagerTick()
}

func (a *App) retryBackgroundHost(id string, state *tunnelBackgroundHost) {
	if a.tunnelRouteMatches(id, state.route) {
		state.attempt = 0
		a.dialTunnelHost(id, state)
		return
	}
	a.stopBackgroundHost(id)
}

func (a *App) restartBackgroundTunnel(key tunnelKey) {
	run, state := a.tmgr.runs[key], a.tmgr.hosts[key.host]
	if run == nil || state == nil || state.client == nil || run.handle == nil {
		a.stopBackgroundTunnel(key)
		a.startBackgroundTunnel(key)
		return
	}
	handle := run.handle
	run.release()
	run.starting, run.err = true, nil
	ctx, cancel := context.WithCancel(state.life)
	run.cancel = cancel
	go func() {
		if waiter, ok := handle.(interface{ Wait() }); ok {
			waiter.Wait()
		}
		if ctx.Err() == nil {
			a.post(func() {
				if ctx.Err() == nil {
					cancel()
					run.cancel, run.starting = nil, false
					a.listenBackgroundTunnel(key, run, state)
				}
			})
		}
	}()
}

func tunnelManagerEngineSupports(spec config.Tunnel) bool {
	return (spec.Kind == config.TunnelLocal || spec.Kind == config.TunnelRemote || spec.Kind == config.TunnelDynamic) &&
		!spec.ServerRelay && spec.ProxyUser == "" && len(spec.AllowFrom) == 0 && spec.MaxConns == 0 && spec.RateKBps == 0 &&
		spec.IdleSeconds == 0 && !spec.Mixed && !spec.SystemProxy && len(spec.PAC) == 0 && spec.TargetSocket == "" && spec.Upstream == ""
}

func (a *App) tunnelRoute(host *config.Host) []*config.Host {
	jumps, _ := a.hosts.JumpHosts(host)
	route := []*config.Host{host.Clone()}
	for _, hop := range jumps {
		route = append(route, hop.Clone())
	}
	return route
}

func (a *App) tunnelRouteMatches(id string, route []*config.Host) bool {
	host := a.hosts.Host(id)
	if !tunnelManagerHost(host) {
		return false
	}
	jumps, _ := a.hosts.JumpHosts(host)
	current := slices.Concat([]*config.Host{host}, jumps)
	return slices.EqualFunc(current, route, func(left, right *config.Host) bool {
		return hostMatchesPrivilege(left, right) && left.Kind == right.Kind && left.KeyPath == right.KeyPath && reflect.DeepEqual(config.ProxyOf(left), config.ProxyOf(right))
	})
}

func (a *App) dialTunnelHost(id string, state *tunnelBackgroundHost) {
	host := a.hosts.Host(id)
	if !a.tunnelRouteMatches(id, state.route) || state.connecting || state.client != nil {
		return
	}
	state.life, state.cancel = context.WithCancel(context.Background())
	life := state.life
	state.connecting, state.retryAt = true, time.Time{}
	finish := func() {
		if life.Err() != nil || !a.tunnelRouteMatches(id, state.route) {
			return
		}
		opts, err := a.dialOptionsFor(life, host, a.passwords[id], nil, false)
		if err != nil {
			a.tunnelHostFailed(state, err, false)
			return
		}
		opts.KeepAlive = a.keepAlive()
		dial := a.tmgr.dial
		if dial == nil {
			dial = sshx.DialBulk
		}
		original := host.Clone()
		go func() {
			ctx, cancel := context.WithTimeout(life, 20*time.Second)
			client, err := dial(ctx, original, opts)
			if client != nil {
				context.AfterFunc(life, func() { _ = client.Close() })
			}
			cancel()
			if life.Err() != nil {
				return
			}
			err, retry := tunnelDialFailure(err, opts)
			secrets := bulkSecrets(opts)
			a.post(func() {
				if life.Err() != nil {
					return
				}
				if !a.tunnelRouteMatches(id, state.route) {
					a.stopBackgroundHost(id)
					return
				}
				if err != nil {
					a.tunnelHostFailed(state, err, retry)
					return
				}
				a.tunnelHostConnected(id, state, client, secrets)
			})
		}()
	}
	pending := 0
	for _, need := range a.chainSecrets(host) {
		if a.readSecret(need, func() {
			pending--
			if pending == 0 {
				finish()
			}
		}) {
			pending++
		}
	}
	if pending == 0 {
		finish()
	}
}

func tunnelDialFailure(err error, opts sshx.Options) (error, bool) {
	if err == nil {
		return nil, false
	}
	if errors.Is(err, sshx.ErrBulkHostKey) {
		return sshx.ErrBulkHostKey, false
	}
	if errors.Is(err, sshx.ErrNoAuth) || errors.Is(err, sshx.ErrProxyAuth) || errors.Is(err, sshx.ErrNeedPassphrase) || errors.Is(err, sshx.ErrVerificationCode) || strings.Contains(err.Error(), "unable to authenticate") {
		return errTunnelManagerLogin, false
	}
	var pathErr *net.OpError
	var routeErr *ssh.OpenChannelError
	retry := errors.As(err, &pathErr) || errors.As(err, &routeErr) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "ssh: handshake failed")
	return errors.New(bulkScrub(err.Error(), bulkSecrets(opts))), retry
}

func (a *App) tunnelHostFailed(state *tunnelBackgroundHost, err error, retry bool) {
	state.cancel()
	state.client, state.connecting, state.probing, state.err = nil, false, false, err
	state.secrets = nil
	for key, run := range a.tmgr.runs {
		if key.host == state.route[0].ID && !run.blocked {
			run.lastErr = err
		}
	}
	if retry {
		state.attempt++
		state.retryAt = a.tmgr.nextRetry(state.attempt)
	}
}

func (a *App) tunnelHostConnected(id string, state *tunnelBackgroundHost, client *ssh.Client, secrets []string) {
	state.client, state.connecting, state.err, state.connectedAt = client, false, nil, a.tmgr.time()
	state.secrets = secrets
	for key, run := range a.tmgr.runs {
		if key.host == id && !run.blocked && (run.retryAt.IsZero() || !a.tmgr.time().Before(run.retryAt)) {
			a.listenBackgroundTunnel(key, run, state)
		}
	}
	life := state.life
	go func() {
		_ = client.Wait()
		if life.Err() == nil {
			a.post(func() {
				if life.Err() != nil {
					return
				}
				for key, run := range a.tmgr.runs {
					if key.host == id && !run.blocked {
						run.release()
						if errors.Is(run.err, sshx.ErrConnectionClosed) || errors.Is(run.err, io.EOF) || errors.Is(run.err, net.ErrClosed) {
							run.err, run.retryAt, run.attempt = nil, time.Time{}, 0
						}
					}
				}
				if a.tmgr.time().Sub(state.connectedAt) >= 30*time.Second {
					state.attempt = 0
				}
				a.tunnelHostFailed(state, sshx.ErrConnectionClosed, true)
			})
		}
	}()
}

func (a *App) listenBackgroundTunnel(key tunnelKey, run *tunnelBackground, state *tunnelBackgroundHost) {
	_, spec := a.tunnelSpec(key)
	if spec == nil || !sameBackgroundTunnel(run.spec, *spec) || run.starting || run.handle != nil {
		return
	}
	run.starting, run.retryAt = true, time.Time{}
	ctx, cancel := context.WithCancel(state.life)
	run.cancel = cancel
	client, settings := state.client, run.spec
	gate := state.forwardGate
	secrets := state.secrets
	start := a.tmgr.start
	if start == nil {
		start = startTunnelManagerForward
	}
	go func() {
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return
		}
		if ctx.Err() != nil {
			<-gate
			return
		}
		var deadline *time.Timer
		if settings.Kind.IsRemote() {
			deadline = time.AfterFunc(15*time.Second, func() { _ = client.Close() })
		}
		handle, err := start(client, settings)
		if deadline != nil {
			deadline.Stop()
		}
		if handle != nil && ctx.Err() != nil {
			handle.Stop()
			if waiter, ok := handle.(interface{ Wait() }); ok {
				waiter.Wait()
			}
			<-gate
			return
		}
		<-gate
		if handle != nil {
			context.AfterFunc(ctx, handle.Stop)
		}
		if ctx.Err() != nil {
			return
		}
		err = tunnelForwardError(err, secrets)
		a.post(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.tunnelListenFailed(run, err)
				return
			}
			run.handle, run.starting, run.err, run.since, run.attempt = handle, false, nil, a.tmgr.time(), 0
		})
		if handle != nil {
			select {
			case <-ctx.Done():
			case <-handle.Done():
				err := tunnelForwardError(handle.Err(), secrets)
				a.post(func() {
					if ctx.Err() == nil {
						a.tunnelListenFailed(run, err)
					}
				})
			}
		}
	}()
}

func tunnelForwardError(err error, secrets []string) error {
	if err == nil || !bulkContainsSecret(err.Error(), secrets) {
		return err
	}
	var failure *sshx.TunnelError
	if errors.As(err, &failure) {
		return &sshx.TunnelError{Addr: bulkScrub(failure.Addr, secrets), Remote: failure.Remote, InUse: failure.InUse,
			Err: errors.New(bulkScrub(failure.Err.Error(), secrets))}
	}
	return errors.New(bulkScrub(err.Error(), secrets))
}

func startTunnelManagerForward(client *ssh.Client, spec config.Tunnel) (tunnelHandle, error) {
	forward, err := sshx.StartTunnel(client, spec, nil)
	if err != nil {
		return nil, err
	}
	return forward, nil
}

func (a *App) tunnelListenFailed(run *tunnelBackground, err error) {
	run.release()
	if err == nil {
		err = sshx.ErrConnectionClosed
	}
	run.err, run.lastErr = err, err
	switch {
	case errors.Is(err, sshx.ErrUnknownTunnel), errors.Is(err, sshx.ErrRemoteListenHost):
		run.blocked = true
	default:
		run.attempt++
		run.retryAt = a.tmgr.nextRetry(run.attempt)
	}
}

func (run *tunnelBackground) release() {
	if run.cancel != nil {
		run.cancel()
		run.cancel = nil
	}
	if handle := run.handle; handle != nil {
		handle.Stop()
		stats := handle.Stats()
		run.accum.Total += stats.Total
		run.accum.Up += stats.Up
		run.accum.Down += stats.Down
	}
	run.handle, run.starting, run.since = nil, false, time.Time{}
}

func (run *tunnelBackground) stats() sshx.TunnelStats {
	stats := run.accum
	if run.handle != nil {
		live := run.handle.Stats()
		stats.Active, stats.Total = live.Active, stats.Total+live.Total
		stats.Up, stats.Down = stats.Up+live.Up, stats.Down+live.Down
	}
	return stats
}

func (a *App) stopBackgroundTunnel(key tunnelKey) {
	run := a.tmgr.runs[key]
	if run == nil {
		return
	}
	run.release()
	delete(a.tmgr.runs, key)
	for other, run := range a.tmgr.runs {
		if other.host == key.host && !run.blocked {
			return
		}
	}
	a.disconnectBackgroundHost(key.host)
}

func (a *App) stopBackgroundHost(id string) {
	for key, run := range a.tmgr.runs {
		if key.host == id {
			run.release()
			delete(a.tmgr.runs, key)
		}
	}
	a.disconnectBackgroundHost(id)
}

func (a *App) disconnectBackgroundHost(id string) {
	if state := a.tmgr.hosts[id]; state != nil {
		if state.cancel != nil {
			state.cancel()
		}
		delete(a.tmgr.hosts, id)
	}
}

func (a *App) closeTunnelManager() {
	a.tmgr.ended = true
	if a.tmgr.timer != nil {
		a.tmgr.timer.Stop()
		a.tmgr.timer = nil
	}
	for id := range a.tmgr.hosts {
		a.stopBackgroundHost(id)
	}
	clear(a.tmgr.runs)
}

func sameBackgroundTunnel(before, after config.Tunnel) bool {
	before.Name, before.Description, before.Disabled = after.Name, after.Description, after.Disabled
	return reflect.DeepEqual(before, after)
}

func (a *App) scheduleTunnelManagerTick() {
	manager := &a.tmgr
	if manager.ended || manager.timer != nil || len(manager.runs) == 0 && !a.windowOpen(windowTunnelManager) && !a.hasSessionTunnels() {
		return
	}
	manager.timer = time.AfterFunc(time.Second, func() {
		a.post(func() {
			manager.timer = nil
			a.tunnelManagerTick()
		})
	})
}

func (a *App) hasSessionTunnels() bool {
	return slices.ContainsFunc(a.sessions, func(session *Session) bool {
		return session.client != nil && slices.ContainsFunc(session.tunnels.runs, func(run *tunnelRun) bool { return run.starting || run.fwd != nil })
	})
}

func (a *App) tunnelManagerTick() {
	manager := &a.tmgr
	if manager.ended {
		return
	}
	now := manager.time()
	if a.windowOpen(windowTunnelManager) {
		a.ensureTunnelManagerIDs()
	}
	for key, run := range manager.runs {
		_, spec := a.tunnelSpec(key)
		if spec == nil || !sameBackgroundTunnel(run.spec, *spec) {
			a.stopBackgroundTunnel(key)
		}
	}
	for id, state := range manager.hosts {
		if !a.tunnelRouteMatches(id, state.route) {
			a.stopBackgroundHost(id)
		} else if !state.retryAt.IsZero() && !now.Before(state.retryAt) {
			a.dialTunnelHost(id, state)
		}
	}
	for key, run := range manager.runs {
		if state := manager.hosts[key.host]; state != nil && state.client != nil && !run.retryAt.IsZero() && !now.Before(run.retryAt) {
			a.listenBackgroundTunnel(key, run, state)
		}
		run.traffic.sample(now, run.stats())
	}
	a.sampleManagerSessions(now)
	a.scheduleTunnelManagerTick()
}

func (a *App) BackgroundTunnelNetworkChanged() {
	if a.tmgr.ended {
		return
	}
	for id, state := range a.tmgr.hosts {
		if !state.retryAt.IsZero() {
			a.dialTunnelHost(id, state)
		} else if client := state.client; client != nil && !state.probing {
			state.probing = true
			life := state.life
			go func() {
				deadline := time.AfterFunc(3*time.Second, func() { _ = client.Close() })
				_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
				if !deadline.Stop() || err != nil {
					return
				}
				if life.Err() == nil {
					a.post(func() {
						if life.Err() == nil {
							state.probing = false
						}
					})
				}
			}()
		}
	}
}
