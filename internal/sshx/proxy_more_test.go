package sshx

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"finallyshell/internal/config"
)

// TestDialHTTPSProxy reaches a host through an HTTP proxy over TLS, whose
// certificate is checked, and refuses one it cannot trust.
func TestDialHTTPSProxy(t *testing.T) {
	srv := startServer(t, false)
	plain := startHTTPProxy(t, "bob", "pw")
	// A TLS listener in front of the HTTP proxy, with httptest's
	// certificate for 127.0.0.1.
	certSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	certSrv.StartTLS()
	cert := certSrv.TLS.Certificates[0]
	pool := certSrv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	certSrv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go plain.serve(c)
		}
	}()
	host, port := splitAddr(t, ln.Addr().String())
	p := &config.ProxyConfig{Kind: config.ProxyHTTPS, Addr: host, Port: port, User: "bob"}

	// Not trusted: the system's authorities do not know it.
	if conn, err := DialProxy(p, "pw", srv.addr, 3*time.Second); err == nil {
		conn.Close()
		t.Fatal("an untrusted certificate was accepted")
	}
	old := proxyRootCAs
	proxyRootCAs = pool
	t.Cleanup(func() { proxyRootCAs = old })
	var asked []string
	opts := testOptions(t, "secret", &asked)
	opts.ProxyPassword = "pw"
	client, err := Dial(target(srv, p), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ping(t, client)
	if !slices.Equal(asked, []string{srv.addr}) {
		t.Errorf("asked about %v", asked)
	}
	// The wrong password is the proxy's refusal.
	if _, err := DialProxy(p, "wrong", srv.addr, 3*time.Second); !errors.Is(err, ErrProxyAuth) {
		t.Fatalf("wrong password: %v", err)
	}
}

// socks4Server is a SOCKS4/4a proxy in the test; it records the user IDs
// and the names it was asked to resolve.
type socks4Server struct {
	addr  string
	mu    sync.Mutex
	users []string
	names []string
}

func (s *socks4Server) seen() (users, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.users), slices.Clone(s.names)
}

func startSOCKS4(t *testing.T) *socks4Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &socks4Server{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *socks4Server) serve(c net.Conn) {
	var head [8]byte
	if _, err := io.ReadFull(c, head[:]); err != nil || head[0] != 4 || head[1] != 1 {
		c.Close()
		return
	}
	readString := func() string {
		var b []byte
		var one [1]byte
		for {
			if _, err := c.Read(one[:]); err != nil || one[0] == 0 {
				return string(b)
			}
			b = append(b, one[0])
		}
	}
	user := readString()
	s.mu.Lock()
	s.users = append(s.users, user)
	s.mu.Unlock()
	port := binary.BigEndian.Uint16(head[2:4])
	host := net.IP(head[4:8]).String()
	if head[4] == 0 && head[5] == 0 && head[6] == 0 && head[7] != 0 {
		host = readString() // SOCKS4a
		s.mu.Lock()
		s.names = append(s.names, host)
		s.mu.Unlock()
	}
	out, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		c.Write([]byte{0, 91, 0, 0, 0, 0, 0, 0})
		c.Close()
		return
	}
	c.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
	pipe(c, out)
}

// TestDialSOCKS4 reaches a host through SOCKS4 by its address, and as
// SOCKS4a by its name, with the user as the user ID.
func TestDialSOCKS4(t *testing.T) {
	srv := startServer(t, false)
	proxy := startSOCKS4(t)
	host, port := splitAddr(t, proxy.addr)
	p := &config.ProxyConfig{Kind: config.ProxySOCKS4, Addr: host, Port: port, User: "alice"}
	var asked []string
	client, err := Dial(target(srv, p), testOptions(t, "secret", &asked))
	if err != nil {
		t.Fatal(err)
	}
	ping(t, client)
	client.Close()
	_, srvPort := splitAddr(t, srv.addr)
	conn, err := DialProxy(p, "", net.JoinHostPort("localhost", strconv.Itoa(srvPort)), 3*time.Second)
	if err != nil {
		t.Fatalf("SOCKS4a: %v", err)
	}
	conn.Close()
	if users, names := proxy.seen(); !slices.Equal(users, []string{"alice", "alice"}) || !slices.Equal(names, []string{"localhost"}) {
		t.Fatalf("users %q, names %q", users, names)
	}
	// A refusal names the target; IPv6 is not for SOCKS4.
	var target *ProxyTargetError
	if _, err := DialProxy(p, "", "127.0.0.1:"+strconv.Itoa(closedPort(t)), 3*time.Second); !errors.As(err, &target) {
		t.Fatalf("refused: %v", err)
	}
	if _, err := DialProxy(p, "", "[::1]:22", 3*time.Second); err == nil {
		t.Fatal("SOCKS4 took an IPv6 address")
	}
}
