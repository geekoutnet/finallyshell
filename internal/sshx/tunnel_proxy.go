package sshx

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"finallyshell/internal/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

var (
	ErrTunnelDialDenied  = errors.New("target connection denied")
	ErrTunnelDialRefused = errors.New("target connection refused")
	ErrTunnelDialNetwork = errors.New("target network unreachable")
	ErrTunnelDialFailed  = errors.New("could not reach target")
	ErrTunnelDialTimeout = errors.New("target connection timed out")
	errTunnelHeaderSize  = errors.New("proxy headers too large")
)

const tunnelHeaderLimit = 64 * 1024

func timeTunnelHandshake(conn net.Conn) func() {
	timer := time.AfterFunc(30*time.Second, func() { conn.Close() })
	return func() { timer.Stop() }
}

type tunnelBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (conn *tunnelBufferedConn) Read(data []byte) (int, error) { return conn.reader.Read(data) }

func (conn *tunnelBufferedConn) CloseWrite() error {
	if half, ok := conn.Conn.(closeWriter); ok {
		return half.CloseWrite()
	}
	return conn.Close()
}

func (t *Tunnel) serveProxy(flow *tunnelFlow, conn net.Conn) error {
	if t.spec.Mixed && !t.spec.Kind.IsRemote() {
		reader := bufio.NewReader(conn)
		stop := timeTunnelHandshake(conn)
		first, err := reader.Peek(1)
		stop()
		if err != nil {
			return err
		}
		conn = &tunnelBufferedConn{conn, reader}
		if first[0] == socksVersion {
			return t.serveSOCKS(flow, conn)
		}
		return t.serveHTTP(flow, conn)
	}
	if t.spec.Kind == config.TunnelDynamic || t.spec.Kind == config.TunnelRemoteDynamic {
		return t.serveSOCKS(flow, conn)
	}
	return t.serveHTTP(flow, conn)
}

func safeTunnelDialError(err error) error {
	var netErr net.Error
	var open *ssh.OpenChannelError
	for _, safe := range []error{ErrTunnelDialTimeout, ErrTunnelDialDenied, ErrTunnelDialRefused, ErrTunnelDialNetwork} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return ErrTunnelDialTimeout
	case errors.Is(err, ErrProxyAuth):
		return ErrProxyAuth
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.Errno(10061)):
		return ErrTunnelDialRefused
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.Errno(10051)), errors.Is(err, syscall.Errno(10065)):
		return ErrTunnelDialNetwork
	case errors.As(err, &open):
		if open.Reason == ssh.Prohibited {
			return ErrTunnelDialDenied
		}
		msg := strings.ToLower(open.Message)
		if strings.Contains(msg, "refused") {
			return ErrTunnelDialRefused
		}
		if strings.Contains(msg, "unreachable") {
			return ErrTunnelDialNetwork
		}
	}
	return ErrTunnelDialFailed
}

func (t *Tunnel) dialUpstream(ctx context.Context, target string) (net.Conn, error) {
	p := t.upstream
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", ProxyAddr(p))
	if err != nil {
		return nil, err
	}
	if p.Kind == config.ProxySOCKS5 {
		var auth *proxy.Auth
		if p.User != "" {
			auth = &proxy.Auth{User: p.User, Password: t.opts.UpstreamPassword}
		}
		dial, err := proxy.SOCKS5("tcp", ProxyAddr(p), auth, dialed{conn})
		if err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := dial.(proxy.ContextDialer).DialContext(ctx, "tcp", target); err != nil {
			conn.Close()
			return nil, socksError(err, target)
		}
		return conn, nil
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	fail := func(err error) (net.Conn, error) {
		stop()
		conn.Close()
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, err
	}
	req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if p.User != "" {
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(p.User+":"+t.opts.UpstreamPassword)) + "\r\n"
	}
	if _, err := io.WriteString(conn, req+"\r\n"); err != nil {
		return fail(err)
	}
	limited := &tunnelHeaderReader{Reader: conn, remaining: tunnelHeaderLimit}
	reader := bufio.NewReader(limited)
	for {
		resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
		if err != nil {
			return fail(err)
		}
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return fail(ErrProxyAuth)
		}
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			break
		}
		if resp.StatusCode < 100 || resp.StatusCode >= 200 || resp.StatusCode == http.StatusSwitchingProtocols {
			return fail(ErrTunnelDialFailed)
		}
	}
	if !stop() {
		return fail(ctx.Err())
	}
	limited.remaining = -1
	return &tunnelBufferedConn{conn, reader}, nil
}

type tunnelHeaderReader struct {
	io.Reader
	remaining int
}

func (r *tunnelHeaderReader) Read(data []byte) (int, error) {
	if r.remaining < 0 {
		return r.Reader.Read(data)
	}
	if r.remaining == 0 {
		return 0, errTunnelHeaderSize
	}
	n, err := r.Reader.Read(data[:min(r.remaining, len(data))])
	r.remaining -= n
	return n, err
}
