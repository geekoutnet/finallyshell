package app

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/rdp"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"
)

func TestRDPSSHReusesConnectionAndStops(t *testing.T) {
	a, platform := testApp(t)
	server := sshtest.Start(t)
	sshHost := &config.Host{ID: "ssh-rdp", Name: "Desk gateway", Addr: server.Host, Port: server.Port, User: "test"}
	a.hosts.Add(sshHost)
	session := newSession(sshHost)
	session.client, session.status = server.Dial(t), statusConnected
	a.sessions = append(a.sessions, session)
	target, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close(); session.disconnect() })
	go func() {
		for {
			conn, err := target.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("rdp"))
			conn.Close()
		}
	}()
	finished := make(chan struct{})
	var launched string
	a.rdpStart = func(req rdp.Request) (func() error, error) {
		launched = net.JoinHostPort(req.Host.Addr, strconv.Itoa(req.Host.Port))
		file := rdp.File(req.Host)
		if req.Host.Addr != "127.0.0.1" || !strings.Contains(file, "full address:s:"+launched+"\r\n") || strings.Contains(file, "override.example") {
			t.Errorf("unexpected launch file: %q", file)
		}
		return func() error { <-finished; return nil }, nil
	}
	desktop := &config.Host{ID: "desktop", Kind: config.KindRDP, Addr: "desktop.example", RDP: &config.RDPConfig{
		SSHHostID: sshHost.ID, SSHTarget: target.Addr().String(), Custom: "Full Address:s:override.example\nredirectclipboard:i:1",
	}}
	a.openRDP(desktop)
	platform.until(t, "RDP client", func() bool { return launched != "" })
	if len(session.tunnels.runs) != 1 || session.tunnels.runs[0].fwd == nil {
		t.Fatal("forward is not tracked")
	}
	conn, err := net.DialTimeout("tcp", launched, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	got, err := io.ReadAll(conn)
	conn.Close()
	if string(got) != "rdp" || err != nil {
		t.Fatalf("forward got %q, %v", got, err)
	}
	close(finished)
	platform.until(t, "tunnel stopped", func() bool { return len(session.tunnels.runs) == 0 })
	if conn, err := net.DialTimeout("tcp", launched, time.Second); err == nil {
		conn.Close()
		t.Fatal("tunnel still accepts connections")
	}
}

func TestRDPSSHDialsWithHostKeyAndClosesOwnedSession(t *testing.T) {
	a, platform := testApp(t)
	server := sshtest.Start(t)
	sshHost := &config.Host{ID: "rdp-jump", Addr: server.Host, Port: server.Port, User: "tester", Auth: config.AuthPassword}
	a.hosts.Add(sshHost)
	a.passwords[sshHost.ID] = sshtest.Password
	finished := make(chan struct{})
	launched := false
	a.rdpStart = func(request rdp.Request) (func() error, error) {
		launched = request.Host.Addr == "127.0.0.1"
		return func() error { <-finished; return nil }, nil
	}
	desktop := &config.Host{ID: "desktop", Addr: "private-desktop", Kind: config.KindRDP,
		RDP: &config.RDPConfig{SSHHostID: sshHost.ID, SSHTarget: net.JoinHostPort("127.0.0.1", strconv.Itoa(echoServer(t)))}}
	a.openRDP(desktop)
	platform.until(t, "host key", func() bool { return a.pendingHostKey() != nil })
	session := a.pendingHostKey()
	session.hostKeyQ.Answer <- true
	session.hostKeyQ = nil
	platform.until(t, "RDP launched", func() bool { return launched })
	if session.term() != nil || len(session.tunnels.runs) != 1 || session.client == nil {
		t.Fatal("dedicated SSH connection should have one tunnel and no shell")
	}
	close(finished)
	platform.until(t, "owned connection stopped", func() bool { return session.client == nil })
}

func TestRDPSSHTargetRefused(t *testing.T) {
	a, platform := testApp(t)
	server := sshtest.Start(t)
	sshHost := &config.Host{ID: "rdp-jump", Addr: server.Host, Port: server.Port, User: "tester"}
	a.hosts.Add(sshHost)
	session := newSession(sshHost)
	session.client, session.status = server.Dial(t), statusConnected
	a.sessions = append(a.sessions, session)
	launched := false
	a.rdpStart = func(rdp.Request) (func() error, error) {
		launched = true
		return func() error { return nil }, nil
	}
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(freeLocalPort(t)))
	desktop := &config.Host{ID: "desktop", Addr: "private-desktop", Kind: config.KindRDP,
		RDP: &config.RDPConfig{SSHHostID: sshHost.ID, SSHTarget: target}}
	a.openRDP(desktop)
	platform.until(t, "target failure", func() bool { return a.rdpErr != nil })
	want := strings.Split(locales.RdpsshTargetFailed(target, "_"), "_")[0]
	if launched || !strings.Contains(a.rdpErr.err.Error(), want) || len(session.tunnels.runs) != 0 || session.client == nil {
		t.Fatalf("launch=%v, error=%v, runs=%d", launched, a.rdpErr.err, len(session.tunnels.runs))
	}
}
