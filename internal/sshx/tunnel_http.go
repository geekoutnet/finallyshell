package sshx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

var errTunnelHTTP = errors.New("invalid or unsupported HTTP proxy request")

func (t *Tunnel) serveHTTP(flow *tunnelFlow, conn net.Conn) error {
	limited := &tunnelHeaderReader{Reader: conn}
	reader := bufio.NewReader(limited)
	for {
		limited.remaining = tunnelHeaderLimit
		stop := timeTunnelHandshake(conn)
		req, err := http.ReadRequest(reader)
		stop()
		if err != nil {
			if errors.Is(err, io.EOF) || flow.ctx.Err() != nil {
				return nil
			}
			writeTunnelHTTPError(conn, 400, "Invalid proxy request.")
			return errTunnelHTTP
		}
		limited.remaining = -1
		if t.spec.ProxyUser != "" && !t.acceptHTTPAuth(req.Header.Values("Proxy-Authorization")) {
			writeTunnelHTTPError(conn, 407, "Proxy authentication required.")
			return ErrProxyAuth
		}
		target, err := httpTunnelTarget(req)
		if err != nil {
			writeTunnelHTTPError(conn, 400, "Expected a CONNECT authority or absolute HTTP URL.")
			return err
		}
		flow.entry.Target = target
		if req.Method == http.MethodConnect {
			return t.serveHTTPConnect(flow, &tunnelBufferedConn{conn, reader}, target)
		}
		if strings.EqualFold(req.Header.Get("Expect"), "100-continue") {
			if _, err := io.WriteString(conn, "HTTP/1.1 100 Continue\r\n\r\n"); err != nil {
				return err
			}
			req.Header.Del("Expect")
		} else if req.Header.Get("Expect") != "" {
			writeTunnelHTTPError(conn, 417, "Unsupported Expect header.")
			return errTunnelHTTP
		}
		keepAlive, err := t.forwardHTTPRequest(flow, conn, req)
		if err != nil || !keepAlive {
			return err
		}
	}
}

func httpTunnelTarget(req *http.Request) (string, error) {
	if req.ProtoMajor != 1 || req.ProtoMinor > 1 {
		return "", errTunnelHTTP
	}
	addr := req.URL.Host
	if req.Method == http.MethodConnect {
		if req.RequestURI != addr || req.ContentLength != 0 || len(req.TransferEncoding) != 0 {
			return "", errTunnelHTTP
		}
	} else {
		if req.URL.Scheme != "http" || req.URL.User != nil || addr == "" || req.URL.Fragment != "" {
			return "", errTunnelHTTP
		}
		if req.URL.Port() == "" && !strings.HasSuffix(addr, ":") {
			addr = net.JoinHostPort(req.URL.Hostname(), "80")
		}
	}
	if !validTunnelTarget(addr) {
		return "", errTunnelHTTP
	}
	return addr, nil
}

func (t *Tunnel) acceptHTTPAuth(values []string) bool {
	if len(values) != 1 {
		return false
	}
	scheme, value, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	user, password, found := bytes.Cut(decoded, []byte{':'})
	return err == nil && found && tunnelCredentialsMatch(user, password, t.spec.ProxyUser, t.opts.ProxyPassword)
}

func (t *Tunnel) serveHTTPConnect(flow *tunnelFlow, conn net.Conn, target string) error {
	out, err := t.dialTarget(flow.ctx, "tcp", target)
	if err != nil {
		writeTunnelHTTPDialError(conn, err)
		return err
	}
	defer t.untrack(out)
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return err
	}
	return t.pipe(conn, out, flow)
}

func (t *Tunnel) forwardHTTPRequest(flow *tunnelFlow, conn net.Conn, req *http.Request) (bool, error) {
	keepAlive := !req.Close && req.ProtoAtLeast(1, 1)
	req = req.WithContext(flow.ctx)
	req.RequestURI, req.Host, req.Close = "", req.URL.Host, false
	stripTunnelHopHeaders(req.Header)
	req.Trailer = nil
	var incoming *tunnelHTTPBody
	if req.Body != nil && req.Body != http.NoBody {
		incoming = &tunnelHTTPBody{ReadCloser: req.Body}
		req.Body = incoming
	}
	transport := &http.Transport{
		DisableCompression:     true,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: tunnelHeaderLimit,
		ResponseHeaderTimeout:  tunnelDialTimeout,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			out, err := t.dialTarget(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &tunnelHTTPOut{Conn: out, writer: flow.writer(out, true), tunnel: t}, nil
		},
	}
	defer transport.CloseIdleConnections()
	resp, err := transport.RoundTrip(req)
	if err != nil {
		err = safeTunnelDialError(err)
		writeTunnelHTTPDialError(conn, err)
		return false, err
	}
	defer resp.Body.Close()
	keepAlive = keepAlive && (incoming == nil || incoming.consumed.Load())
	if resp.StatusCode == http.StatusSwitchingProtocols {
		writeTunnelHTTPError(conn, 502, "Protocol upgrade requires CONNECT.")
		return false, errTunnelHTTP
	}
	forward := *resp
	stripTunnelHopHeaders(forward.Header)
	forward.Trailer = nil
	forward.Close = !keepAlive
	if forward.ContentLength < 0 && keepAlive && req.Method != http.MethodHead {
		forward.TransferEncoding = []string{"chunked"}
	}
	forward.ProtoMajor, forward.ProtoMinor = 1, req.ProtoMinor
	if !req.ProtoAtLeast(1, 1) {
		forward.TransferEncoding = nil
	}
	if err := forward.Write(flow.writer(conn, false)); err != nil {
		return false, errors.New("proxy response interrupted")
	}
	return keepAlive, nil
}

type tunnelHTTPBody struct {
	io.ReadCloser
	consumed atomic.Bool
}

func (body *tunnelHTTPBody) Read(data []byte) (int, error) {
	n, err := body.ReadCloser.Read(data)
	if err == io.EOF {
		body.consumed.Store(true)
	}
	return n, err
}

func (body *tunnelHTTPBody) Close() error { return nil }

type tunnelHTTPOut struct {
	net.Conn
	writer io.Writer
	tunnel *Tunnel
}

func (out *tunnelHTTPOut) Write(data []byte) (int, error) { return out.writer.Write(data) }

func (out *tunnelHTTPOut) Close() error {
	out.tunnel.untrack(out.Conn)
	return nil
}

func stripTunnelHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for name := range strings.SplitSeq(value, ",") {
			header.Del(strings.TrimSpace(name))
		}
	}
	for name := range header {
		if strings.HasPrefix(strings.ToLower(name), "proxy-") {
			delete(header, name)
		}
	}
	for _, name := range []string{"Connection", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func writeTunnelHTTPDialError(conn net.Conn, err error) {
	if errors.Is(err, ErrTunnelDialTimeout) {
		writeTunnelHTTPError(conn, 504, "Target connection timed out.")
		return
	}
	writeTunnelHTTPError(conn, 502, "Could not reach target.")
}

func writeTunnelHTTPError(conn net.Conn, status int, message string) {
	challenge := ""
	if status == http.StatusProxyAuthRequired {
		challenge = "Proxy-Authenticate: Basic realm=\"SSH tunnel\"\r\n"
	}
	body := message + "\n"
	_, _ = fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nConnection: close\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\n%s\r\n%s", status, http.StatusText(status), len(body), challenge, body)
}
