package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestSSHForwardParsing(t *testing.T) {
	tests := []struct {
		input string
		want  Tunnel
	}{
		{`ssh -Nf -L 13306:localhost:3306 alice@server`, Tunnel{Kind: TunnelLocal, ListenPort: 13306, TargetHost: "localhost", TargetPort: 3306}},
		{`ssh -L '[::1]:1234:[2001:db8::1]:443' server`, Tunnel{Kind: TunnelLocal, ListenHost: "::1", ListenPort: 1234, TargetHost: "2001:db8::1", TargetPort: 443}},
		{`ssh -L '2375:/var/run/docker sock' server`, Tunnel{Kind: TunnelLocal, ListenPort: 2375, TargetSocket: "/var/run/docker sock"}},
		{`ssh -D*:1080 server`, Tunnel{Kind: TunnelDynamic, ListenHost: "0.0.0.0", ListenPort: 1080}},
		{`RemoteForward [::1]:1080`, Tunnel{Kind: TunnelRemoteDynamic, ListenHost: "::1", ListenPort: 1080}},
		{`RemoteForward = 0 localhost:3000 # comment`, Tunnel{Kind: TunnelRemote, TargetHost: "localhost", TargetPort: 3000}},
		{`ssh -R 0 server`, Tunnel{Kind: TunnelRemoteDynamic}},
	}
	for _, test := range tests {
		got, err := ParseSSHForwards(test.input)
		if err != nil || !reflect.DeepEqual(got.Tunnels, []Tunnel{test.want}) {
			t.Errorf("%q: %+v, %v", test.input, got, err)
		}
	}
	got, err := ParseSSHForwards("ssh -o 'ExitOnForwardFailure=yes' -L 8000:localhost:80 -R 1080 -D1081 host")
	if err != nil || len(got.Tunnels) != 3 || got.ExitOnForwardFailure == nil || !*got.ExitOnForwardFailure {
		t.Fatalf("combined: %+v, %v", got, err)
	}
	for _, bad := range []string{
		"ssh -L 80:host:0 server", "ssh -L 0:host:80 server", "ssh -L 8000:host server", "ssh -R 1234:/tmp/sock server",
		"ssh -D 99999 host", "ssh -L 80:2001:db8::1:80 host", "ssh -L '8000:host:80", "ssh host -D 1234",
		"ssh -D 1234 host; whoami", "ssh -D 1234", "LocalForward /tmp/local.sock /tmp/remote.sock", "ExitOnForwardFailure maybe",
		"ssh -o 'ClearAllForwardings=yes' -D 1080 host", "DynamicForward [+::1]:1080", "DynamicForward +1080",
	} {
		if _, err := ParseSSHForwards(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSSHForwardFormatting(t *testing.T) {
	host := Host{Addr: "2001:db8::1", User: "alice", Port: 2222, Tunnels: []Tunnel{
		{Kind: TunnelLocal, ListenHost: "::1", ListenPort: 2375, TargetSocket: "/var/run/docker's sock"},
		{Kind: TunnelRemoteDynamic, ListenPort: 1080},
		{Kind: TunnelHTTP, ListenPort: 3128, ProxyUser: "never-export", Upstream: "http://never-export"},
		{Kind: TunnelDynamic, ListenHost: "0.0.0.0", ListenPort: 1081, AllowFrom: []string{"192.168.0.0/24"}},
	}}
	command := FormatSSHCommand(&host)
	if !strings.Contains(command, `ssh -N -o 'ExitOnForwardFailure=yes' -p '2222' -l 'alice' -L '[::1]:2375:/var/run/docker'\''s sock' -R '127.0.0.1:1080' '2001:db8::1'`) {
		t.Fatal(command)
	}
	for _, output := range []string{command, FormatSSHConfig(host.Tunnels)} {
		if strings.Contains(output, "never-export") || strings.Count(output, "# Tunnel") != 2 {
			t.Fatal(output)
		}
		got, err := ParseSSHForwards(output)
		if err != nil || len(got.Tunnels) != 2 || got.Tunnels[0].TargetSocket != host.Tunnels[0].TargetSocket || got.Tunnels[1].Kind != TunnelRemoteDynamic {
			t.Errorf("round trip: %+v, %v", got, err)
		}
	}
}
