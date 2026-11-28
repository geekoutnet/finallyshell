package sshx

import (
	"io"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
)

func TestTunnelResolve(t *testing.T) {
	client := sshtest.Start(t).Dial(t)
	echo := startEcho(t, "127.0.0.1:0")
	tun, err := StartTunnelWithOptions(client, config.Tunnel{Kind: config.TunnelDynamic, ListenHost: "127.0.0.1"}, TunnelOptions{
		Resolve: func(host string) (string, bool) { return "127.0.0.1", host == "site.example.org" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	conn, code := socksDial(t, tun.Addr(), 1, 3, "site.example.org", echo.Port)
	defer conn.Close()
	if code != 0 {
		t.Fatalf("SOCKS reply %d", code)
	}
	conn.Write([]byte("ping"))
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "ping" {
		t.Fatalf("%q: %v", reply, err)
	}
	other, code := socksDial(t, tun.Addr(), 1, 3, "unmapped.invalid", echo.Port)
	other.Close()
	if code == 0 {
		t.Fatal("reached a name that was not mapped")
	}
}
