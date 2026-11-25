package sshx

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"finallyshell/internal/config"
)

var (
	ErrTunnelAllowList  = errors.New("invalid tunnel allowlist: use IP addresses or CIDRs")
	ErrTunnelPeerDenied = errors.New("client address not on the tunnel allowlist")
	ErrTunnelCapacity   = errors.New("tunnel connection limit reached")
	ErrTunnelIdle       = errors.New("tunnel connection idle timeout")
	ErrTunnelSocket     = errors.New("invalid Unix socket path")
	ErrTunnelUpstream   = errors.New("invalid upstream proxy: use socks5:// or http:// with a host and port, without a password")
	ErrTunnelCredential = errors.New("missing or invalid tunnel proxy credential")
	ErrTunnelLimit      = errors.New("invalid tunnel limit or port")
	ErrTunnelTarget     = errors.New("invalid tunnel target address")
)

const tunnelDialTimeout = 15 * time.Second

// ParseTunnelAllowList parses IPs and CIDRs without DNS; mapped IPv4 is normalized.
func ParseTunnelAllowList(entries []string) ([]netip.Prefix, error) {
	var list []netip.Prefix
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if addr, err := netip.ParseAddr(entry); err == nil && addr.Zone() == "" {
			addr = addr.Unmap()
			list = append(list, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return nil, ErrTunnelAllowList
		}
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		list = append(list, prefix.Masked())
	}
	return list, nil
}

func allowsTunnelPeer(list []netip.Prefix, peer string) bool {
	if len(list) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.WithZone("").Unmap()
	for _, prefix := range list {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// ValidTunnelSocket validates a Unix path relative to the server's filesystem.
func ValidTunnelSocket(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\x00\r\n")
}

func validTunnelTarget(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return false
	}
	num, err := strconv.ParseUint(port, 10, 16)
	if err != nil || num == 0 {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.Zone() == "" {
		return true
	}
	return !strings.ContainsFunc(host, func(value rune) bool {
		return !(value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '.' || value == '-' || value == '_')
	})
}

// ParseTunnelUpstream accepts a URL with an optional username, never a password.
func ParseTunnelUpstream(raw string) (*config.ProxyConfig, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") || !validTunnelTarget(parsed.Host) {
		return nil, ErrTunnelUpstream
	}
	p := &config.ProxyConfig{Addr: parsed.Hostname()}
	p.Port, _ = strconv.Atoi(parsed.Port())
	switch parsed.Scheme {
	case "socks5":
		p.Kind = config.ProxySOCKS5
	case "http":
		p.Kind = config.ProxyHTTP
	default:
		return nil, ErrTunnelUpstream
	}
	if parsed.User != nil {
		p.User = parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword || p.User == "" || len(p.User) > 255 || strings.ContainsFunc(p.User, unicode.IsControl) || p.Kind == config.ProxyHTTP && strings.Contains(p.User, ":") {
			return nil, ErrTunnelUpstream
		}
	}
	return p, nil
}

func tunnelPolicy(t config.Tunnel, opts TunnelOptions) ([]netip.Prefix, *config.ProxyConfig, error) {
	if t.ListenPort < 0 || t.ListenPort > 65535 || t.MaxConns < 0 || t.RateKBps < 0 || t.IdleSeconds < 0 || int64(t.IdleSeconds) > math.MaxInt64/int64(time.Second) {
		return nil, nil, ErrTunnelLimit
	}
	if t.TargetSocket != "" {
		if t.Kind != config.TunnelLocal || !ValidTunnelSocket(t.TargetSocket) {
			return nil, nil, ErrTunnelSocket
		}
	} else if !t.Kind.IsProxy() && !validTunnelTarget(t.TargetAddr()) {
		return nil, nil, ErrTunnelTarget
	}
	if t.Kind.IsProxy() && t.ProxyUser != "" && (opts.ProxyPassword == "" || len(opts.ProxyPassword) > 255 || len(t.ProxyUser) > 255 || strings.ContainsFunc(t.ProxyUser, unicode.IsControl) || strings.Contains(t.ProxyUser, ":")) {
		return nil, nil, ErrTunnelCredential
	}
	allow, err := ParseTunnelAllowList(t.AllowFrom)
	if err != nil {
		return nil, nil, err
	}
	upstream, err := ParseTunnelUpstream(t.Upstream)
	if err == nil && upstream != nil {
		switch {
		case !t.Kind.IsRemote():
			err = ErrTunnelUpstream
		case upstream.User != "" && (opts.UpstreamPassword == "" || len(opts.UpstreamPassword) > 255):
			err = ErrTunnelCredential
		}
	}
	return allow, upstream, err
}

type tunnelLimiter struct {
	mu           sync.Mutex
	rate, tokens float64
	last         time.Time
	burst        int
}

func newTunnelLimiter(rateKBps int) *tunnelLimiter {
	if rateKBps == 0 {
		return nil
	}
	rate := float64(rateKBps) * 1024
	burst := int(min(rate/4, 32*1024))
	return &tunnelLimiter{rate: rate, burst: burst, tokens: float64(burst)}
}

func (lim *tunnelLimiter) wait(ctx context.Context, bytes int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lim.mu.Lock()
	now := time.Now()
	lim.tokens = min(float64(lim.burst), lim.tokens+now.Sub(lim.last).Seconds()*lim.rate) - float64(bytes)
	lim.last = now
	delay := time.Duration(max(0, -lim.tokens/lim.rate) * float64(time.Second))
	lim.mu.Unlock()
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		lim.mu.Lock()
		lim.tokens = min(float64(lim.burst), lim.tokens+float64(bytes))
		lim.mu.Unlock()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type tunnelFlow struct {
	t        *Tunnel
	in       net.Conn
	ctx      context.Context
	cancel   context.CancelCauseFunc
	last     atomic.Int64
	up, down atomic.Int64
	idle     *time.Timer
	idleMu   sync.Mutex
	entry    TunnelConn
}

func newTunnelFlow(t *Tunnel, in net.Conn) *tunnelFlow {
	ctx, cancel := context.WithCancelCause(t.ctx)
	f := &tunnelFlow{t: t, in: in, ctx: ctx, cancel: cancel, entry: TunnelConn{Started: time.Now(), Client: in.RemoteAddr().String()}}
	if t.idle > 0 {
		f.idleMu.Lock()
		f.idle = time.AfterFunc(t.idle, f.checkIdle)
		f.idleMu.Unlock()
	}
	return f
}

func (f *tunnelFlow) checkIdle() {
	f.idleMu.Lock()
	if f.ctx.Err() != nil {
		f.idleMu.Unlock()
		return
	}
	remaining := f.t.idle - (time.Since(f.entry.Started) - time.Duration(f.last.Load()))
	if remaining > 0 {
		f.idle.Reset(remaining)
		f.idleMu.Unlock()
		return
	}
	f.cancel(ErrTunnelIdle)
	f.idleMu.Unlock()
	f.in.Close()
}

func (f *tunnelFlow) finish() {
	f.cancel(nil)
	if f.idle != nil {
		f.idleMu.Lock()
		f.idle.Stop()
		f.idleMu.Unlock()
	}
	if errors.Is(context.Cause(f.ctx), ErrTunnelIdle) {
		f.entry.Err = ErrTunnelIdle
	}
	f.entry.Duration = time.Since(f.entry.Started)
	f.entry.Up, f.entry.Down = f.up.Load(), f.down.Load()
}

func (f *tunnelFlow) writer(dst io.Writer, up bool) io.Writer {
	lim := f.t.limitDown
	if up {
		lim = f.t.limitUp
	}
	return tunnelWriter{f, dst, lim, up}
}

type tunnelWriter struct {
	flow *tunnelFlow
	dst  io.Writer
	lim  *tunnelLimiter
	up   bool
}

func (w tunnelWriter) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		chunk := len(data)
		if w.lim != nil {
			chunk = min(chunk, w.lim.burst)
			if err := w.lim.wait(w.flow.ctx, chunk); err != nil {
				return total, err
			}
		}
		n, err := w.dst.Write(data[:chunk])
		if n > 0 {
			w.flow.last.Store(int64(time.Since(w.flow.entry.Started)))
			if w.up {
				w.flow.up.Add(int64(n))
			} else {
				w.flow.down.Add(int64(n))
			}
			w.flow.t.traffic(w.up, int64(n))
		}
		total += n
		if err != nil {
			return total, err
		}
		if n != chunk {
			return total, io.ErrShortWrite
		}
		data = data[n:]
	}
	return total, nil
}
