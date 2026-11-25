package sshx

import (
	"testing"
	"time"
)

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
