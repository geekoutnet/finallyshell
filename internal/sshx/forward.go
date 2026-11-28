package sshx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"finallyshell/internal/config"

	"golang.org/x/crypto/ssh"
)

// ErrUnknownTunnel is a tunnel of a kind this build does not know, as a
// newer one may write.
var ErrUnknownTunnel = errors.New("unknown kind of tunnel")

// ErrRemoteListenHost is a remote tunnel whose listen address is a host
// name: the address is sent to the server as an IP address, and a name
// would be looked up here, not on the server.
var ErrRemoteListenHost = errors.New("a remote tunnel listens on an IP address or localhost, not a host name")

// RemoteListenAddr is host:port that a remote tunnel asks the server to
// listen on: its ListenHost, an IP address, or the loopback for "" and
// localhost. It fails with ErrRemoteListenHost for a host name.
func RemoteListenAddr(t config.Tunnel) (string, error) {
	host := t.ListenHost
	if host == "" || strings.EqualFold(host, "localhost") {
		host = config.LoopbackHost
	}
	if net.ParseIP(host) == nil {
		return "", ErrRemoteListenHost
	}
	return net.JoinHostPort(host, strconv.Itoa(t.ListenPort)), nil
}

// TunnelError is a tunnel that could not start listening.
type TunnelError struct {
	// Addr is where it was to listen.
	Addr string
	// Remote is set when the server was to listen, and refused or failed.
	Remote bool
	// InUse is set when the port is taken on this computer.
	InUse bool
	Err   error
}

func (e *TunnelError) Error() string { return "listen on " + e.Addr + ": " + e.Err.Error() }
func (e *TunnelError) Unwrap() error { return e.Err }

// Tunnel is a port forwarded through an SSH connection, as ssh -L, -R or
// -D: a listener, and the connections it has open. Its methods are safe
// to call from any goroutine.
type Tunnel struct {
	spec               config.Tunnel
	client             *ssh.Client
	ln                 net.Listener
	notify             func()
	opts               TunnelOptions
	ctx                context.Context
	cancel             context.CancelFunc
	allow              []netip.Prefix
	upstream           *config.ProxyConfig
	limitUp, limitDown *tunnelLimiter
	idle               time.Duration

	active   atomic.Int64
	total    atomic.Int64
	rejected atomic.Int64
	up, down atomic.Int64

	wg   sync.WaitGroup // the accept loop and every connection
	done chan struct{}

	mu          sync.Mutex
	stopped     bool
	err         error // why it stopped by itself; nil once closed
	dialErr     error // why the last connection could not reach the target
	conns       map[io.Closer]struct{}
	timer       *time.Timer // a notify that is due
	history     tunnelHistory
	lastErr     error
	rates       [TunnelRateSamples]tunnelRateSlot
	lastTraffic time.Time
}

// TunnelStats are what a tunnel has carried.
type TunnelStats struct {
	// Active are the connections open now; Total all of them.
	Active, Total int64
	// Up are the bytes from the side that connected to the target, Down
	// the bytes back.
	Up, Down               int64
	Rejected               int64
	UpRate, DownRate       float64
	UpHistory, DownHistory [TunnelRateSamples]float64
	LastError              error
}

// TunnelOptions supplies runtime-only credentials and notifications. Never persist it.
type TunnelOptions struct {
	ProxyPassword    string
	UpstreamPassword string
	Notify           func()
	// Resolve, if not nil, gives the address to connect to instead of a
	// target's host, keeping its port, as a hosts file on the server
	// would: the desktop browser opens a site by its name at the address
	// nginx listens on. It is called on the tunnel's goroutines.
	Resolve func(host string) (string, bool)
}

// notifyEvery is how often a tunnel calls notify at most.
const notifyEvery = 250 * time.Millisecond

// StartTunnel starts listening as t says, on this computer or, for a
// remote tunnel, on the server client is connected to, and forwards
// what connects. notify, if not nil, is called on a goroutine of the
// tunnel's after its stats or state change, at most every quarter of a
// second; it must not block. The tunnel stops on Stop or Close, or by
// itself when the listener fails, as when the connection is lost.
func StartTunnel(client *ssh.Client, t config.Tunnel, notify func()) (*Tunnel, error) {
	return StartTunnelWithOptions(client, t, TunnelOptions{Notify: notify})
}

// StartTunnelWithOptions starts a tunnel with runtime-only options.
func StartTunnelWithOptions(client *ssh.Client, t config.Tunnel, opts TunnelOptions) (*Tunnel, error) {
	if !t.Kind.Known() {
		return nil, ErrUnknownTunnel
	}
	allow, upstream, err := tunnelPolicy(t, opts)
	if err != nil {
		return nil, err
	}
	addr := t.ListenAddr()
	var ln net.Listener
	switch t.Kind {
	case config.TunnelLocal, config.TunnelDynamic, config.TunnelHTTP:
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			return nil, &TunnelError{Addr: addr, InUse: addrInUse(err), Err: err}
		}
	case config.TunnelRemote, config.TunnelRemoteDynamic, config.TunnelRemoteHTTP:
		// x/crypto resolves the address on this computer: only an IP
		// address means the same on the server.
		var raddr string
		if raddr, err = RemoteListenAddr(t); err != nil {
			return nil, &TunnelError{Addr: addr, Remote: true, Err: err}
		}
		addr = raddr
		ln, err = client.Listen("tcp", addr)
		if err != nil {
			return nil, &TunnelError{Addr: addr, Remote: true, Err: err}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	tun := &Tunnel{spec: t, client: client, ln: ln, notify: opts.Notify, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		conns: map[io.Closer]struct{}{}, allow: allow, upstream: upstream, idle: time.Duration(t.IdleSeconds) * time.Second,
		limitUp: newTunnelLimiter(t.RateKBps), limitDown: newTunnelLimiter(t.RateKBps)}
	tun.wg.Add(1)
	go tun.accept()
	return tun, nil
}

// Addr is the address the tunnel listens on, the port chosen when it
// asked for 0.
func (t *Tunnel) Addr() net.Addr { return t.ln.Addr() }

// Stats are what the tunnel has carried so far.
func (t *Tunnel) Stats() TunnelStats {
	st := TunnelStats{Active: t.active.Load(), Total: t.total.Load(), Up: t.up.Load(), Down: t.down.Load(), Rejected: t.rejected.Load()}
	t.mu.Lock()
	defer t.mu.Unlock()
	st.LastError = t.lastErr
	if t.err != nil {
		st.LastError = t.err
	}
	st.readRates(t.rates, time.Now())
	return st
}

// Done is closed once the tunnel stops listening.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Err is why the tunnel stopped by itself, as ErrConnectionClosed; nil
// while it listens, and after Stop.
func (t *Tunnel) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// DialErr is why the last connection that failed could not reach its
// target; nil once one reaches it.
func (t *Tunnel) DialErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dialErr
}

// Stop stops listening and closes the tunnel's connections. It does not
// wait: a port on this computer is free once it returns, while a server
// is told to stop listening on a goroutine. Stopping again does nothing.
func (t *Tunnel) Stop() { t.stop(nil) }

// Wait waits until the tunnel has stopped and its goroutines are gone.
func (t *Tunnel) Wait() { t.wg.Wait() }

// Close stops the tunnel, as Stop, and waits until its connections are
// gone, as Wait. Closing again does nothing.
func (t *Tunnel) Close() error {
	t.Stop()
	t.Wait()
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
	return nil
}

// stop closes the listener and the connections; err is why, nil for
// Stop.
func (t *Tunnel) stop(err error) {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped, t.err = true, err
	conns := t.conns
	t.conns = nil
	if t.spec.Kind.IsRemote() || len(conns) != 0 {
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			for conn := range conns {
				conn.Close()
			}
		}()
	}
	if t.spec.Kind.IsRemote() {
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.ln.Close()
		}()
	}
	t.mu.Unlock()
	t.cancel()
	if !t.spec.Kind.IsRemote() {
		t.ln.Close()
	}
	close(t.done)
	t.changed()
}

// track keeps c to close as the tunnel stops; false, closing c, when it
// has stopped.
func (t *Tunnel) track(c io.Closer) bool {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		c.Close()
		return false
	}
	t.conns[c] = struct{}{}
	t.mu.Unlock()
	return true
}

func (t *Tunnel) untrack(c io.Closer) {
	c.Close()
	t.mu.Lock()
	delete(t.conns, c)
	t.mu.Unlock()
}

// changed calls notify soon, once for the changes until then.
func (t *Tunnel) changed() {
	if t.notify == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer == nil {
		t.timer = time.AfterFunc(notifyEvery, func() {
			t.mu.Lock()
			t.timer = nil
			ticking := !t.stopped && time.Since(t.lastTraffic) < (TunnelRateSamples+1)*time.Second
			t.mu.Unlock()
			t.notify()
			if ticking {
				t.changed()
			}
		})
	}
}

func (t *Tunnel) noteDial(err error) {
	t.mu.Lock()
	t.dialErr = err
	t.mu.Unlock()
}

func (t *Tunnel) accept() {
	defer t.wg.Done()
	for {
		c, err := t.ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				t.stop(err)
			} else {
				t.stop(ErrConnectionClosed)
			}
			return
		}
		peer := c.RemoteAddr().String()
		t.mu.Lock()
		if t.stopped {
			t.mu.Unlock()
			c.Close()
			return
		}
		t.total.Add(1)
		var reject error
		switch {
		case !allowsTunnelPeer(t.allow, peer):
			reject = ErrTunnelPeerDenied
		case t.spec.MaxConns > 0 && t.active.Load() >= int64(t.spec.MaxConns):
			reject = ErrTunnelCapacity
		}
		if reject != nil {
			t.mu.Unlock()
			c.Close()
			t.rejected.Add(1)
			t.record(TunnelConn{Started: time.Now(), Client: peer, Err: reject})
			t.changed()
			continue
		}
		t.wg.Add(1)
		t.conns[c] = struct{}{}
		t.active.Add(1)
		t.mu.Unlock()
		t.changed()
		go t.handle(c)
	}
}

// wsaeAddrInUse is Windows' error for a port taken, which syscall does
// not name there.
const wsaeAddrInUse = 10048

// addrInUse tells whether err, from listening, is a port taken.
func addrInUse(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == syscall.EADDRINUSE || runtime.GOOS == "windows" && errno == wsaeAddrInUse)
}

// ErrConnectionClosed is a tunnel whose listener ended without Stop: the
// connection to the server is gone.
var ErrConnectionClosed = errors.New("the connection to the server closed")

func (t *Tunnel) handle(c net.Conn) {
	flow := newTunnelFlow(t, c)
	defer func() {
		flow.finish()
		if t.ctx.Err() != nil && (errors.Is(flow.entry.Err, context.Canceled) || errors.Is(flow.entry.Err, net.ErrClosed) || errors.Is(flow.entry.Err, io.EOF)) {
			flow.entry.Err = nil
		}
		t.untrack(c)
		t.record(flow.entry)
		t.active.Add(-1)
		t.changed()
		t.wg.Done()
	}()
	if t.spec.Kind.IsProxy() {
		flow.entry.Err = t.serveProxy(flow, c)
		return
	}
	network, target := "tcp", t.spec.TargetAddr()
	if t.spec.TargetSocket != "" {
		network, target = "unix", t.spec.TargetSocket
	}
	flow.entry.Target = target
	out, err := t.dialTarget(flow.ctx, network, target)
	if err != nil {
		flow.entry.Err = err
		return
	}
	defer t.untrack(out)
	flow.entry.Err = t.pipe(c, out, flow)
}

// pipe copies between in, the connection that came in, and out, the one
// to the target, until both directions end. As one direction ends, the
// other side is told no more is coming, if it can be, as ssh does.
func (t *Tunnel) pipe(in, out net.Conn, flow *tunnelFlow) error {
	stop := context.AfterFunc(flow.ctx, func() { out.Close() })
	defer stop()
	var wg sync.WaitGroup
	var upErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, upErr = io.CopyBuffer(flow.writer(out, true), tunnelReader{Reader: in}, make([]byte, 32*1024))
		endTunnelCopy(out, in, upErr)
	}()
	_, downErr := io.CopyBuffer(flow.writer(in, false), tunnelReader{Reader: out}, make([]byte, 32*1024))
	endTunnelCopy(in, out, downErr)
	wg.Wait()
	for _, err := range []error{upErr, downErr} {
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) && t.ctx.Err() == nil {
			return errors.New("tunnel traffic interrupted")
		}
	}
	return nil
}

type closeWriter interface{ CloseWrite() error }
type tunnelReader struct{ io.Reader }

func endTunnelCopy(dst, src net.Conn, err error) {
	if cw, ok := dst.(closeWriter); ok && err == nil {
		cw.CloseWrite()
		return
	}
	dst.Close()
	src.Close()
}

func (t *Tunnel) dialTarget(ctx context.Context, network, target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, tunnelDialTimeout)
	defer cancel()
	if resolve := t.opts.Resolve; resolve != nil {
		if host, port, err := net.SplitHostPort(target); err == nil {
			if addr, ok := resolve(host); ok {
				target = net.JoinHostPort(addr, port)
			}
		}
	}
	var conn net.Conn
	var err error
	switch {
	case t.upstream != nil:
		conn, err = t.dialUpstream(ctx, target)
	case t.spec.Kind.IsRemote():
		conn, err = (&net.Dialer{}).DialContext(ctx, network, target)
	default:
		conn, err = t.client.DialContext(ctx, network, target)
	}
	if err != nil {
		err = safeTunnelDialError(err)
		if !errors.Is(err, context.Canceled) {
			t.noteDial(fmt.Errorf("%s: %w", target, err))
		}
		return nil, err
	}
	if !t.track(conn) {
		return nil, net.ErrClosed
	}
	t.noteDial(nil)
	return conn, nil
}
