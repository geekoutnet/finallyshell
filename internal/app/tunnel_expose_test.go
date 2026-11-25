package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/internal/sshx"
)

func TestTunnelExposeOutsideProbe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("probe %s %s", r.Method, r.URL)
		}
		w.Header().Set("Location", "http://127.0.0.1:1/")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	for _, httpProbe := range []bool{false, true} {
		status, err := tunnelOutsideProbe(context.Background(), "127.0.0.1", port, httpProbe)
		if err != nil || httpProbe && status != http.StatusFound || !httpProbe && status != 0 {
			t.Errorf("HTTP=%v: %d, %v", httpProbe, status, err)
		}
	}
	if _, err := tunnelOutsideProbe(context.Background(), "127.0.0.1/user", port, false); err == nil {
		t.Error("accepted a URL instead of a host")
	}
}

func TestTunnelExposeFirewallCommands(t *testing.T) {
	ctx := context.Background()
	state := "Status: active\nDefault: deny (incoming)\n[ 1] 22/tcp ALLOW IN   Anywhere\n[ 2] 8000:8100/udp ALLOW IN   Anywhere\n"
	conn := opsConnection{
		normal: func(_ context.Context, script string, _ func(string)) (string, string, error) {
			if script != monitor.FirewallDetectScript {
				t.Errorf("detect ran %s", script)
			}
			return "ufw\n", "", nil
		},
		root: func(_ context.Context, script string, _ func(string)) (string, string, error) {
			if script != monitor.UFWReadScript {
				t.Errorf("read ran %s", script)
			}
			return state, "", nil
		},
	}
	record := tunnelFirewallRecord{Backend: "ufw", Port: 8080, Tag: "Finallyshell test"}
	before, err := tunnelFirewallStatus(ctx, conn, record.Port)
	if err != nil || before.backend != "ufw" || before.allowed {
		t.Fatalf("before = %+v, %v", before, err)
	}
	cmd, err := tunnelFirewallScript(record, before, []int{22}, true)
	if err != nil || !strings.HasSuffix(cmd, `'ufw' 'allow' 'proto' 'tcp' 'from' 'any' 'to' 'any' 'port' '8080' 'comment' 'Finallyshell test'`) {
		t.Errorf("allow: %s, %v", cmd, err)
	}
	state += "[ 3] 8080/tcp ALLOW IN   Anywhere # Finallyshell test\n[ 4] 8080/tcp (v6) ALLOW IN   Anywhere (v6) # Finallyshell test\n"
	after, err := tunnelFirewallStatus(ctx, conn, record.Port)
	if err != nil || !after.allowed {
		t.Fatalf("after = %+v, %v", after, err)
	}
	cmd, err = tunnelFirewallScript(record, after, []int{22}, false)
	if err != nil || !strings.Contains(cmd, "'ufw' '--force' 'delete' '4'") || !strings.HasSuffix(cmd, "'ufw' '--force' 'delete' '3'") {
		t.Errorf("remove: %s, %v", cmd, err)
	}
	if _, err := tunnelFirewallScript(record, after, []int{8080}, false); err == nil {
		t.Error("accepted removing SSH port")
	}
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	if err := keepTunnelFirewallRecord(record, true); err != nil {
		t.Fatal(err)
	}
	if records, err := tunnelFirewallRecords(); err != nil || len(records) != 1 || records[0] != record {
		t.Fatalf("records: %v, %v", records, err)
	}
	if err := keepTunnelFirewallRecord(record, false); err != nil {
		t.Fatal(err)
	}
	if records, err := tunnelFirewallRecords(); err != nil || len(records) != 0 {
		t.Fatalf("records after removal: %v, %v", records, err)
	}
}

func TestTunnelExposeFirewallFirewalld(t *testing.T) {
	record := tunnelFirewallRecord{Backend: "firewall-cmd", Zone: "public", Port: 8080}
	before := exposeFirewall{backend: record.Backend, zone: record.Zone}
	cmd, err := tunnelFirewallScript(record, before, []int{22}, true)
	for _, want := range []string{
		`'firewall-cmd' '--permanent' '--zone=public' '--query-port=8080/tcp'`,
		`'firewall-cmd' '--zone=public' '--add-port=8080/tcp'`,
		`'firewall-cmd' '--permanent' '--zone=public' '--add-port=8080/tcp'`,
		`'firewall-cmd' '--zone=public' '--remove-port=8080/tcp'`,
	} {
		if err != nil || !strings.Contains(cmd, want) {
			t.Errorf("missing %s: %v", want, err)
		}
	}
}

func TestTunnelExposeRelayLifetime(t *testing.T) {
	server := sshtest.Start(t)
	client := server.Dial(t)
	spec := config.Tunnel{Kind: config.TunnelRemote, ListenHost: "0.0.0.0", ListenPort: 8080,
		TargetHost: "127.0.0.1", TargetPort: echoServer(t), ServerRelay: true}
	done := make(chan error, 1)
	runner := func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		if !strings.Contains(script, `'socat' 'TCP4-LISTEN:8080,bind=0.0.0.0,fork,reuseaddr'`) {
			t.Errorf("runner received %s", script)
		}
		progress("FINALLYSHELL_RELAY_")
		progress("READY\n")
		_, err := io.Copy(io.Discard, input)
		done <- err
		return "", "", err
	}
	forward, _, err := startExposureRelay(context.Background(), client, spec, "socat", runner, nil, sshx.StartTunnel, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(forward.Stop)
	addr := forward.Addr().(*net.TCPAddr)
	if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) || addr.Port == spec.ListenPort {
		t.Fatalf("inner listener: %s", addr)
	}
	echoes(t, addr.Port, 0, "test")
	forward.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("relay stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay stdin did not close")
	}
	forward.Wait()
	if stillOpen, err := net.DialTimeout("tcp", addr.String(), time.Second); err == nil {
		stillOpen.Close()
		t.Fatal("inner listener is still reachable after stop")
	}
	startErr := errors.New("cannot bind")
	fail := func(context.Context, string, io.Reader, func(string)) (string, string, error) {
		return "", "", startErr
	}
	if _, _, err := startExposureRelay(context.Background(), client, spec, "socat", fail, nil, sshx.StartTunnel, nil); !errors.Is(err, startErr) {
		t.Errorf("startup error: %v", err)
	}
}

func TestTunnelExposeRelayStopsForwardWhenRelayExits(t *testing.T) {
	client := sshtest.Start(t).Dial(t)
	spec := config.Tunnel{Kind: config.TunnelRemote, ListenHost: "0.0.0.0", ListenPort: 443,
		TargetHost: "127.0.0.1", TargetPort: echoServer(t), ServerRelay: true}
	exit := make(chan struct{})
	root := func(ctx context.Context, _ string, _ io.Reader, progress func(string)) (string, string, error) {
		progress("FINALLYSHELL_RELAY_READY\n")
		select {
		case <-exit:
			return "", "lost listener", errors.New("relay exited")
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	cleanup, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	forward, exited, err := startExposureRelay(cleanup, client, spec, "socat", nil, root, sshx.StartTunnel, nil)
	if err != nil {
		t.Fatal(err)
	}
	close(exit)
	select {
	case err := <-exited:
		if err == nil || err.Error() != "lost listener" {
			t.Fatalf("relay error: %v", err)
		}
	case <-cleanup.Done():
		t.Fatal("the relay exit was not reported")
	}
	select {
	case <-forward.Done():
	case <-cleanup.Done():
		t.Fatal("the remote forward did not stop after the relay exited")
	}
}

func TestTunnelLANFirewallMatchesProgramPortAndDirection(t *testing.T) {
	exe := `C:\Apps\Finallyshell.exe`
	rule := func(name, direction, program, ports, action string) string {
		return fmt.Sprintf("Rule Name: %s\nEnabled: Yes\nDirection: %s\nProgram: %s\nProtocol: TCP\nLocalPort: %s\nAction: %s\n", name, direction, program, ports, action)
	}
	allow := rule("Allow local", "In", exe, "443, 8000-8100", "Allow")
	blockOut := rule("Block outgoing", "Out", exe, "Any", "Block")
	blockOther := rule("Block other", "In", "C:\\other.exe", "Any", "Block")
	if got := tunnelLANFirewallMatch(allow+blockOut+blockOther, exe, 8080); got != "allow" {
		t.Errorf("got %q", got)
	}
	if got := tunnelLANFirewallMatch(allow+rule("Block", "In", exe, "8080", "Block"), exe, 8080); got != "block" {
		t.Errorf("got %q", got)
	}
}

func TestTunnelLANAddressesAndRule(t *testing.T) {
	input := []tunnelLANInterface{
		{name: "Wi-Fi", flags: net.FlagUp, addresses: []string{"192.168.0.3/24", "fe80::1/64", "fd00::1/64", "127.1.1.1/8"}},
		{name: "vEthernet (Default Switch)", flags: net.FlagUp, addresses: []string{"192.168.10.5/24"}},
		{name: "utun2", flags: net.FlagUp, addresses: []string{"192.168.99.1/24"}},
		{name: "Ethernet", addresses: []string{"10.0.0.9/16"}},
	}
	for bind, want := range map[string]string{"0.0.0.0": "[192.168.0.3/24]", "::": "[192.168.0.3/24 fd00::1/64]", "192.168.0.3": "[192.168.0.3/24]", "127.0.0.1": "[]"} {
		if got := fmt.Sprint(tunnelLANAddresses(input, netip.MustParseAddr(bind))); got != want {
			t.Errorf("bind %s: %s, want %s", bind, got, want)
		}
	}
	if cmd := tunnelLANRule(8080); cmd != `netsh advfirewall firewall add rule name="Finallyshell tunnel 8080" dir=in action=allow protocol=TCP localport=8080` {
		t.Errorf("rule %q", cmd)
	}
	for _, port := range []int{0, -1, 65536} {
		if got := tunnelLANRule(port); got != "" {
			t.Error("accepted port " + strconv.Itoa(port))
		}
	}
}
