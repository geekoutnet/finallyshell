package sshx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

func TestTunnelUnixSocketChannel(t *testing.T) {
	const path = "/var/run/docker.sock"
	got := make(chan string, 1)
	addr := managedSSHServer(t, &ssh.ServerConfig{NoClientAuth: true}, func(_ *ssh.ServerConn, channels <-chan ssh.NewChannel) {
		for request := range channels {
			var payload struct {
				Path         string
				Reserved     string
				ReservedPort uint32
			}
			if request.ChannelType() != "direct-streamlocal@openssh.com" || ssh.Unmarshal(request.ExtraData(), &payload) != nil || payload.Reserved != "" || payload.ReservedPort != 0 {
				request.Reject(ssh.UnknownChannelType, "unexpected channel")
				continue
			}
			got <- payload.Path
			channel, requests, err := request.Accept()
			if err == nil {
				go ssh.DiscardRequests(requests)
				io.Copy(channel, channel)
				channel.CloseWrite()
				channel.Close()
			}
		}
	})
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, bad := range []string{"relative.sock", "/nul\x00x", "/line\nend"} {
		if _, err := StartTunnel(client, config.Tunnel{Kind: config.TunnelLocal, TargetSocket: bad}, nil); !errors.Is(err, ErrTunnelSocket) {
			t.Fatalf("invalid Unix path: %v", err)
		}
	}
	tun, err := StartTunnel(client, config.Tunnel{Kind: config.TunnelLocal, TargetSocket: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	echoThrough(t, dial(t, tun.Addr()), "streamlocal")
	if <-got != path {
		t.Fatal("wrong socket path")
	}
}

func TestTunnelUpstreamRejectsEmbeddedPassword(t *testing.T) {
	for _, value := range []string{
		"https://127.0.0.1:80", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:80/", "http://127.0.0.1:80?",
		"socks5://user" + ":" + "not-a-real-password" + "@127.0.0.1:1080", "http://user%3Aname@127.0.0.1:80",
	} {
		if _, err := ParseTunnelUpstream(value); !errors.Is(err, ErrTunnelUpstream) {
			t.Fatalf("expected sanitized upstream validation error: %v", err)
		}
	}
	if proxy, err := ParseTunnelUpstream("socks5://user@[::1]:1080"); err != nil || ProxyAddr(proxy) != "[::1]:1080" || proxy.User != "user" {
		t.Fatalf("wrong IPv6 upstream: %v, %v", proxy, err)
	}
}

func TestTunnelProxyConnectAndSOCKSAuth(t *testing.T) {
	srv := sshtest.Start(t)
	client := srv.Dial(t)
	echo := startEcho(t, "127.0.0.1:0")
	if echo == nil {
		t.Fatal("no echo listener")
	}
	for _, kind := range []config.TunnelKind{config.TunnelHTTP, config.TunnelRemoteDynamic, config.TunnelRemoteHTTP} {
		t.Run(string(kind), func(t *testing.T) {
			tun, err := StartTunnelWithOptions(client, config.Tunnel{Kind: kind, Mixed: !kind.IsRemote(), ProxyUser: "name"}, TunnelOptions{ProxyPassword: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			defer tun.Close()
			if kind != config.TunnelRemoteDynamic {
				c := dial(t, tun.Addr())
				defer c.Close()
				c.SetDeadline(time.Now().Add(4 * time.Second))
				fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic bmFtZTpwYXNz\r\n\r\nearly", echo, echo)
				const reply = "HTTP/1.1 200 Connection Established\r\n\r\nearly"
				got := make([]byte, len(reply))
				if _, err := io.ReadFull(c, got); err != nil || string(got) != reply {
					t.Fatalf("CONNECT reply %q: %v", got, err)
				}
			}
			if kind == config.TunnelRemoteHTTP {
				return
			}
			for _, pair := range []struct {
				password string
				status   byte
			}{{"fail", 1}, {"pass", 0}} {
				c := dial(t, tun.Addr())
				defer c.Close()
				c.SetDeadline(time.Now().Add(4 * time.Second))
				check := func(sent, want []byte) {
					t.Helper()
					c.Write(sent)
					got := make([]byte, len(want))
					if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("SOCKS reply %v, want %v: %v", got, want, err)
					}
				}
				check([]byte{5, 2, 0, 2}, []byte{5, 2})
				check(append([]byte{1, 4, 'n', 'a', 'm', 'e', 4}, pair.password...), []byte{1, pair.status})
				if pair.status != 0 {
					continue
				}
				check(binary.BigEndian.AppendUint16([]byte{5, 1, 0, 1, 127, 0, 0, 1}, uint16(echo.Port)), []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				echoThrough(t, c, "authenticated")
			}
		})
	}
}

func TestTunnelRemoteUpstream(t *testing.T) {
	srv := sshtest.Start(t)
	client := srv.Dial(t)
	echo := startEcho(t, "127.0.0.1:0")
	if echo == nil {
		t.Fatal("no echo listener")
	}
	for _, scheme := range []string{"http", "socks5"} {
		t.Run(scheme, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			answered := make(chan error, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					answered <- err
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(4 * time.Second))
				if scheme == "http" {
					want := "CONNECT " + echo.String() + " HTTP/1.1\r\nHost: " + echo.String() + "\r\nProxy-Authorization: Basic dXNlcjpwYXNz\r\n\r\n"
					got := make([]byte, len(want))
					if _, err := io.ReadFull(conn, got); err != nil || string(got) != want {
						answered <- fmt.Errorf("invalid upstream CONNECT: %q: %w", got, err)
						return
					}
					io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				} else {
					target, err := socksRequest(conn, "user", "pass")
					if err != nil || target != echo.String() {
						answered <- fmt.Errorf("invalid upstream SOCKS target: %q: %w", target, err)
						return
					}
					_ = socksReply(conn, socksOK)
				}
				_, err = io.Copy(conn, conn)
				answered <- err
			}()
			kind := config.TunnelRemoteDynamic
			if scheme == "http" {
				kind = config.TunnelRemote
			}
			tun, err := StartTunnelWithOptions(client, config.Tunnel{Kind: kind, TargetHost: "127.0.0.1", TargetPort: echo.Port,
				Upstream: scheme + "://user@" + ln.Addr().String()}, TunnelOptions{UpstreamPassword: "pass"})
			if err != nil {
				t.Fatal(err)
			}
			defer tun.Close()
			var c net.Conn
			if kind == config.TunnelRemoteDynamic {
				var code byte
				c, code = socksDial(t, tun.Addr(), 1, 1, "127.0.0.1", echo.Port)
				if code != 0 {
					t.Fatalf("SOCKS reply %d", code)
				}
			} else {
				c = dial(t, tun.Addr())
			}
			echoThrough(t, c, "through upstream")
			if err := <-answered; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTunnelRejectionAndIdle(t *testing.T) {
	srv := sshtest.Start(t)
	client := srv.Dial(t)
	echo := startEcho(t, "127.0.0.1:0")
	if echo == nil {
		t.Fatal("no echo listener")
	}
	spec := config.Tunnel{Kind: config.TunnelRemote, TargetHost: "127.0.0.1", TargetPort: echo.Port, MaxConns: 1, IdleSeconds: 1, AllowFrom: []string{"192.0.2.0/24"}}
	blocked, err := StartTunnel(client, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	c := dial(t, blocked.Addr())
	c.Close()
	waitFor(t, "origin denied", func() bool { return len(blocked.Conns()) == 1 })
	if record := blocked.Conns()[0]; !errors.Is(record.Err, ErrTunnelPeerDenied) || !strings.HasPrefix(record.Client, "127.0.0.1:") {
		t.Fatalf("wrong origin or rejection: %+v", record)
	}
	spec.AllowFrom = []string{"127.0.0.0/8"}
	tun, err := StartTunnel(client, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	first := dial(t, tun.Addr())
	defer first.Close()
	first.SetDeadline(time.Now().Add(4 * time.Second))
	waitFor(t, "first connection active", func() bool { return tun.Stats().Active == 1 })
	second := dial(t, tun.Addr())
	second.Close()
	waitFor(t, "second connection refused", func() bool { return len(tun.Conns()) == 1 })
	if !errors.Is(tun.Conns()[0].Err, ErrTunnelCapacity) {
		t.Fatalf("wrong refusal: %+v", tun.Conns()[0])
	}
	if _, err := first.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("idle connection not closed: %v", err)
	}
	waitFor(t, "idle logged", func() bool { return len(tun.Conns()) == 2 && tun.Stats().Active == 0 })
	if st := tun.Stats(); st.Active != 0 || st.Total != 2 || st.Rejected != 1 || !errors.Is(st.LastError, ErrTunnelIdle) {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestTunnelRateSharedPerDirection(t *testing.T) {
	srv := sshtest.Start(t)
	client := srv.Dial(t)
	echo := startEcho(t, "127.0.0.1:0")
	if echo == nil {
		t.Fatal("no echo listener")
	}
	tun, err := StartTunnel(client, config.Tunnel{Kind: config.TunnelLocal, TargetHost: "127.0.0.1", TargetPort: echo.Port, RateKBps: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	started := time.Now()
	var group sync.WaitGroup
	for range 2 {
		c := dial(t, tun.Addr())
		group.Go(func() {
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := c.Write(bytes.Repeat([]byte{1}, 2048)); err != nil {
				t.Error(err)
				return
			}
			got := make([]byte, 2048)
			if _, err := io.ReadFull(c, got); err != nil || !bytes.Equal(got, bytes.Repeat([]byte{1}, 2048)) {
				t.Errorf("echo failed: %v", err)
			}
		})
	}
	group.Wait()
	if elapsed := time.Since(started); elapsed < 680*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("shared rate took %v", elapsed)
	}
	if st := tun.Stats(); st.Up != 4096 || st.Down != 4096 || st.UpRate <= 0 || st.DownRate <= 0 {
		t.Fatalf("unexpected rate: %+v", st)
	}
}

func TestTunnelHTTPFailures(t *testing.T) {
	client := sshtest.Start(t).Dial(t)
	tun, err := StartTunnel(client, config.Tunnel{Kind: config.TunnelHTTP}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	for _, fixture := range []struct {
		request string
		code    int
	}{
		{"GET /relative HTTP/1.1\r\nHost: localhost\r\n\r\n", 400},
		{"GET http://user:never-log-this@127.0.0.1/ HTTP/1.1\r\nHost: localhost\r\n\r\n", 400},
		{fmt.Sprintf("CONNECT 127.0.0.1:%d HTTP/1.1\r\nHost: localhost\r\n\r\n", freePort(t)), 502},
	} {
		conn := dial(t, tun.Addr())
		conn.SetDeadline(time.Now().Add(4 * time.Second))
		io.WriteString(conn, fixture.request)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil || resp.StatusCode != fixture.code {
			t.Fatalf("want %d: %v, %v", fixture.code, resp, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if len(body) == 0 || strings.Contains(string(body), "never-log-this") {
			t.Fatalf("unexpected response body %q", body)
		}
	}
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	go writeTunnelHTTPDialError(server, safeTunnelDialError(context.DeadlineExceeded))
	peer.SetDeadline(time.Now().Add(time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(peer), nil)
	if err != nil || resp.StatusCode != 504 {
		t.Fatalf("want 504: %v, %v", resp, err)
	}
	resp.Body.Close()
}

func TestTunnelHTTPAndMixed(t *testing.T) {
	srv := sshtest.Start(t)
	client := srv.Dial(t)
	defer client.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		fmt.Fprintf(w, "%s:%s:%s:%s", req.RequestURI, body, req.Header.Get("Proxy-Authorization"), req.Header.Get("X-Private"))
	})}
	go origin.Serve(ln)
	defer origin.Close()
	for _, kind := range []config.TunnelKind{config.TunnelHTTP, config.TunnelRemoteHTTP} {
		t.Run(string(kind), func(t *testing.T) {
			tun, err := StartTunnelWithOptions(client, config.Tunnel{Kind: kind, Mixed: !kind.IsRemote(), ProxyUser: "user"}, TunnelOptions{ProxyPassword: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			defer tun.Close()
			c := dial(t, tun.Addr())
			defer c.Close()
			c.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(c)
			for idx := range 2 {
				fmt.Fprintf(c, "POST http://%s/path?q=%d HTTP/1.1\r\nHost: %s\r\nTransfer-Encoding: chunked\r\nConnection: X-Private\r\nX-Private: hidden\r\nProxy-Authorization: Basic %s\r\n\r\n3\r\nabc\r\n0\r\n\r\n", ln.Addr(), idx, ln.Addr(), base64.StdEncoding.EncodeToString([]byte("user:secret")))
				resp, err := http.ReadResponse(reader, nil)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || string(got) != fmt.Sprintf("/path?q=%d:abc::", idx) || err != nil {
					t.Fatalf("status %d, body %q, err %v", resp.StatusCode, got, err)
				}
			}
			fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", ln.Addr(), ln.Addr())
			resp, err := http.ReadResponse(reader, nil)
			if err != nil || resp.StatusCode != 407 || !strings.HasPrefix(resp.Header.Get("Proxy-Authenticate"), "Basic") {
				t.Fatalf("missing basic challenge: %v, %v", resp, err)
			}
			resp.Body.Close()
		})
	}
}

func TestTunnelAccessPolicy(t *testing.T) {
	list, err := ParseTunnelAllowList([]string{"127.0.0.1", "2001:db8::/32", "::ffff:192.0.2.0/120"})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]bool{"127.0.0.1:80": true, "192.0.2.4:80": true, "[2001:db8::2]:80": true, "[::1]:80": false, "192.0.3.4:80": false} {
		if allowsTunnelPeer(list, addr) != want {
			t.Errorf("%s: want %t", addr, want)
		}
	}
	if _, err := ParseTunnelAllowList([]string{"127.0.0.1/99"}); err == nil {
		t.Fatal("invalid prefix accepted")
	}
}

func TestTunnelConnectionHistory(t *testing.T) {
	tun := &Tunnel{}
	for idx := range 205 {
		tun.record(TunnelConn{Started: time.Unix(int64(idx), 0), Up: int64(idx)})
	}
	entries := tun.Conns()
	if len(entries) != 200 || entries[0].Up != 204 || entries[199].Up != 5 {
		t.Fatalf("invalid connection history: %v", entries)
	}
	entries[0].Up = 0
	if tun.Conns()[0].Up != 204 {
		t.Fatal("history shares its backing array")
	}
}
