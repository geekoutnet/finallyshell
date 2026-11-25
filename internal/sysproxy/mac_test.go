package sysproxy

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSystemProxyMacBackend(t *testing.T) {
	var calls [][]string
	backend := macBackend{func(name string, args ...string) (string, error) {
		if name != "networksetup" {
			t.Fatal(name)
		}
		calls = append(calls, slices.Clone(args))
		switch args[0] {
		case "-listallnetworkservices":
			return "An asterisk denotes a disabled service.\nWi-Fi\n*Old Wire\nUSB LAN\n", nil
		case "-getautoproxyurl":
			return "URL: (null)\nEnabled: No\n", nil
		case "-getproxyautodiscovery":
			return "Auto Proxy Discovery: On\n", nil
		default:
			return "Enabled: No\nServer: \nPort: 0\nAuthenticated Proxy Enabled: 0\n", nil
		}
	}}
	backup, err := backend.Snapshot()
	if err != nil || strings.Contains(string(backup), "Old Wire") || len(calls) != 11 {
		t.Fatalf("snapshot: %s %v %v", backup, calls, err)
	}
	calls = nil
	if err := backend.Set(Settings{Address: "127.0.0.1:1080", SOCKS: true}, backup); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"-setproxyautodiscovery", "Wi-Fi", "off"}, {"-setsocksfirewallproxy", "USB LAN", "127.0.0.1", "1080", "off"},
		{"-setsecurewebproxystate", "Wi-Fi", "off"}, {"-setsocksfirewallproxystate", "USB LAN", "on"},
	} {
		if !slices.ContainsFunc(calls, func(args []string) bool { return reflect.DeepEqual(args, want) }) {
			t.Errorf("missing %v in %v", want, calls)
		}
	}
	calls = nil
	if err := backend.Restore(backup); err != nil || !reflect.DeepEqual(calls[0], []string{"-setproxyautodiscovery", "Wi-Fi", "on"}) {
		t.Fatalf("restore: %v %v", calls, err)
	}
}
