package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/internal/sshx"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestTunnelManagerBackoff(t *testing.T) {
	for attempt, seconds := range []int{2, 4, 8, 16, 32, 60, 60, 60} {
		if got := tunnelRetryDelay(attempt+1, 0.5); got != time.Duration(seconds)*time.Second {
			t.Errorf("attempt %d: %v", attempt+1, got)
		}
	}
	if low, high := tunnelRetryDelay(1, 0), tunnelRetryDelay(1, 1); low >= 2*time.Second || high <= 2*time.Second || tunnelRetryDelay(100, 1) > time.Minute {
		t.Fatalf("jitter: %v, %v", low, high)
	}
}

func TestTunnelManagerDialErrorScrubsSecrets(t *testing.T) {
	proxyPassword := "test-only-proxy-value"
	jumpPassword := "test-only-jump-value"
	opts := sshx.Options{ProxyPassword: proxyPassword, Jump: &sshx.Hop{Options: sshx.Options{Password: jumpPassword}}}
	err, retry := tunnelDialFailure(&net.OpError{Op: "dial", Err: errors.New(proxyPassword + "/" + jumpPassword)}, opts)
	if !retry || err.Error() != "dial: [redacted]/[redacted]" {
		t.Fatal("background dial errors did not redact credentials")
	}
	var failure *sshx.TunnelError
	err = tunnelForwardError(&sshx.TunnelError{Addr: "127.0.0.1:8080", Remote: true, Err: errors.New(proxyPassword)}, bulkSecrets(opts))
	if !errors.As(err, &failure) || !failure.Remote || failure.Err.Error() != "[redacted]" {
		t.Fatal("listener diagnostics did not redact credentials or lost the listener error type")
	}
}

func TestTunnelManagerReconnect(t *testing.T) {
	server := sshtest.Start(t)
	application, platform := testApp(t)
	t.Cleanup(application.CloseAll)
	clock := time.Now()
	application.tmgr.now = func() time.Time { return clock }
	application.tmgr.jitter = func() float64 { return 0.5 }
	host := tunnelManagerTestHost(application, server, echoServer(t))
	host.Tunnels = append(host.Tunnels, config.Tunnel{ID: "second", Kind: config.TunnelDynamic, Background: true})
	writeTunnelManagerHostKey(t, server, server)
	application.StartBackgroundTunnels()
	first := application.tmgr.runs[tunnelKey{host.ID, "first"}]
	second := application.tmgr.runs[tunnelKey{host.ID, "second"}]
	platform.until(t, "both listeners", func() bool { return first.handle != nil && second.handle != nil })
	if server.Logins() != 1 || len(application.sessions) != 0 {
		t.Fatalf("expected one SSH login and no terminal, got %d logins, %d sessions", server.Logins(), len(application.sessions))
	}
	if server.Shells() != 0 {
		t.Fatal("background connection opened a shell")
	}
	listener := first.handle
	echoes(t, listener.Addr().(*net.TCPAddr).Port, 0, "before drop")
	server.DropConnections()
	platform.until(t, "scheduled retry", func() bool { return first.handle == nil && !application.tmgr.hosts[host.ID].retryAt.IsZero() })
	if got := application.tmgr.hosts[host.ID].retryAt.Sub(clock); got != 2*time.Second {
		t.Fatalf("retry delay %v", got)
	}
	clock = clock.Add(2 * time.Second)
	application.tunnelManagerTick()
	platform.until(t, "both listeners restarted", func() bool { return first.handle != nil && second.handle != nil })
	if first.handle == listener || server.Logins() != 2 {
		t.Fatalf("expected a fresh shared transport, got %d logins", server.Logins())
	}
	echoes(t, first.handle.Addr().(*net.TCPAddr).Port, 0, "after drop")
	previousFirst, previousSecond := first.handle, second.handle
	application.restartBackgroundTunnel(tunnelKey{host.ID, "first"})
	platform.until(t, "one listener restarted", func() bool { return first.handle != nil && first.handle != previousFirst })
	if second.handle != previousSecond || server.Logins() != 2 {
		t.Fatal("restarting one listener disturbed another on the same transport")
	}
	server.DropConnections()
	platform.until(t, "second scheduled retry", func() bool { return !application.tmgr.hosts[host.ID].retryAt.IsZero() })
	application.BackgroundTunnelNetworkChanged()
	platform.until(t, "early network recovery", func() bool { return first.handle != nil && second.handle != nil })
	if server.Logins() != 3 {
		t.Fatal("network recovery did not reconnect ahead of the retry deadline")
	}
}

func TestTunnelManagerSessionMode(t *testing.T) {
	server := sshtest.Start(t)
	application, platform := testApp(t)
	t.Cleanup(application.CloseAll)
	host := tunnelManagerTestHost(application, server, echoServer(t))
	host.Tunnels[0].Background = false
	host.Tunnels = append(host.Tunnels, config.Tunnel{Kind: config.TunnelDynamic, Background: true})
	session := newSession(host)
	session.client, session.status = server.Dial(t), statusConnected
	application.sessions = append(application.sessions, session)
	specs := application.sessionTunnelSpecs(host.ID, host.Tunnels)
	if len(specs) != 1 || specs[0].ID != "first" {
		t.Fatalf("session inherited background tunnels: %+v", specs)
	}
	application.startTunnels(session, specs)
	platform.until(t, "session tunnel", func() bool { return session.tunnels.runs[0].fwd != nil })
	application.tunnelManagerTick()
	row := application.tunnelManagerRow(host, host.Tunnels[0])
	if row.background || row.state != tunnelViewListening || row.since.IsZero() {
		t.Fatalf("session tunnel not reflected in manager: %+v", row)
	}
	application.stopManagerRow(row)
	if session.tunnels.runs[0].fwd != nil || session.client == nil {
		t.Fatal("manager stopped the SSH session or left the listener running")
	}
}

func TestTunnelManagerHostKey(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unknown"
		if changed {
			name = "changed"
		}
		t.Run(name, func(t *testing.T) {
			server := sshtest.Start(t)
			application, platform := testApp(t)
			t.Cleanup(application.CloseAll)
			host := tunnelManagerTestHost(application, server, 1)
			if changed {
				writeTunnelManagerHostKey(t, server, sshtest.Start(t))
			}
			application.StartBackgroundTunnels()
			platform.until(t, "untrusted host key", func() bool { return application.tmgr.hosts[host.ID].err != nil })
			state := application.tmgr.hosts[host.ID]
			if !errors.Is(state.err, sshx.ErrBulkHostKey) || !state.retryAt.IsZero() || server.Logins() != 0 || len(application.sessions) != 0 || len(platform.questions()) != 0 {
				t.Fatalf("unexpected result: %v, retry %v, logins %d", state.err, state.retryAt, server.Logins())
			}
		})
	}
}

func writeTunnelManagerHostKey(t *testing.T, target, keySource *sshtest.Server) {
	t.Helper()
	var key ssh.PublicKey
	_, _ = ssh.Dial("tcp", keySource.Addr(), &ssh.ClientConfig{User: "tester", HostKeyCallback: func(_ string, _ net.Addr, observed ssh.PublicKey) error {
		key = observed
		return errors.New("capture public key only")
	}})
	if key == nil {
		t.Fatal("no test host key")
	}
	line := knownhosts.Line([]string{target.Addr()}, key) + "\n"
	if err := os.WriteFile(filepath.Join(config.Dir(), "known_hosts"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTunnelManagerAutostartTrayAndQuit(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "enabled"
		if disabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("FINALLYSHELL_DIR", t.TempDir())
			store := config.NewStore()
			host := &config.Host{ID: "saved", Addr: "127.0.0.1", User: "saved", Auth: config.AuthPassword, Tunnels: []config.Tunnel{
				{ID: "background", Kind: config.TunnelDynamic, Background: true, Disabled: true},
				{ID: "session", Kind: config.TunnelDynamic},
			}}
			store.Add(host)
			if err := config.SaveHosts(store); err != nil {
				t.Fatal(err)
			}
			settings := config.Settings{MinimizeToTray: true}
			if disabled {
				settings.TunnelManager = &config.TunnelManagerSettings{NoAutostart: true}
			}
			if err := config.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			platform := newFakePlatform(t)
			application := New(platform)
			t.Cleanup(application.CloseAll)
			calls := make(chan sshx.Options, 1)
			if err := platform.SecretSet(loginSecretKey(host), sshtest.Password); err != nil {
				t.Fatal(err)
			}
			application.tmgr.dial = func(_ context.Context, _ *config.Host, opts sshx.Options) (*ssh.Client, error) {
				calls <- opts
				return nil, &net.OpError{Op: "dial", Err: errors.New("offline")}
			}
			application.StartBackgroundTunnels()
			if disabled {
				if len(application.tmgr.runs) != 0 {
					t.Fatal("autostart opt-out ignored")
				}
				return
			}
			platform.until(t, "initial background attempt", func() bool { return len(calls) == 1 })
			if opts := <-calls; opts.Password != sshtest.Password || opts.KeepAlive.Interval != 30*time.Second {
				t.Fatal("background dial did not reuse saved credentials and keepalive")
			}
			if application.CloseRequested() || !platform.inTray || len(application.tmgr.runs) != 1 {
				t.Fatal("closing to tray interrupted autostart")
			}
			application.CloseAll()
			if len(application.tmgr.runs) != 0 || len(application.tmgr.hosts) != 0 || application.tmgr.timer != nil {
				t.Fatal("quit left background work alive")
			}
		})
	}
}

func TestTunnelManagerPortInUse(t *testing.T) {
	server := sshtest.Start(t)
	application, platform := testApp(t)
	t.Cleanup(application.CloseAll)
	clock := time.Now()
	application.tmgr.now = func() time.Time { return clock }
	application.tmgr.jitter = func() float64 { return 0.5 }
	host := tunnelManagerTestHost(application, server, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	host.Tunnels[0].ListenPort = listener.Addr().(*net.TCPAddr).Port
	application.tmgr.dial = func(_ context.Context, _ *config.Host, _ sshx.Options) (*ssh.Client, error) {
		return server.Dial(t), nil
	}
	application.StartBackgroundTunnels()
	run := application.tmgr.runs[tunnelKey{host.ID, "first"}]
	platform.until(t, "bind failure", func() bool { return run.err != nil })
	var listenErr *sshx.TunnelError
	if !errors.As(run.err, &listenErr) || !listenErr.InUse || run.retryAt.Sub(clock) != 2*time.Second {
		t.Fatalf("listen failure: %v, retry %v", run.err, run.retryAt.Sub(clock))
	}
	application.tunnelManagerTick()
	if run.attempt != 1 || run.starting {
		t.Fatal("retried before backoff elapsed")
	}
	clock = clock.Add(2 * time.Second)
	application.tunnelManagerTick()
	platform.until(t, "second bind failure", func() bool { return run.retryAt.Sub(clock) == 4*time.Second })
	if server.Logins() != 1 {
		t.Fatalf("bind failure needlessly reconnected SSH %d times", server.Logins())
	}
}

func tunnelManagerTestHost(application *App, server *sshtest.Server, target int) *config.Host {
	host := &config.Host{ID: "tunnel-host", Addr: server.Host, Port: server.Port, User: "tester", Auth: config.AuthPassword,
		Tunnels: []config.Tunnel{{ID: "first", Kind: config.TunnelLocal, TargetHost: "127.0.0.1", TargetPort: target, Background: true}}}
	application.hosts.Add(host)
	application.passwords[host.ID] = sshtest.Password
	return host
}
