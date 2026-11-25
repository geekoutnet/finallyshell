package sysproxy

import (
	"errors"
	"reflect"
	"testing"
)

func TestSystemProxyGNOMEBackend(t *testing.T) {
	var calls [][]string
	backend := gnomeBackend{func(name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		switch args[2] {
		case "mode":
			return "'none'\n", nil
		case "autoconfig-url", "host":
			return "''\n", nil
		case "port":
			return "0\n", nil
		default:
			return "false\n", nil
		}
	}, true}
	backup, err := backend.Snapshot()
	if err != nil || len(calls) != len(gnomeKeys) {
		t.Fatalf("snapshot: %s %v %v", backup, calls, err)
	}
	calls = nil
	if err := backend.Set(Settings{Address: "127.0.0.1:3128"}, backup); err != nil {
		t.Fatal(err)
	}
	for index, want := range map[int][]string{
		2:  {"gsettings", "set", "org.gnome.system.proxy.http", "host", "'127.0.0.1'"},
		6:  {"gsettings", "set", "org.gnome.system.proxy.https", "port", "3128"},
		11: {"gsettings", "set", "org.gnome.system.proxy", "mode", "'manual'"},
	} {
		if !reflect.DeepEqual(calls[index], want) {
			t.Errorf("%v != %v", calls[index], want)
		}
	}
	calls = nil
	if err := backend.Restore(backup); err != nil || !reflect.DeepEqual(calls[11], []string{"gsettings", "set", "org.gnome.system.proxy", "mode", "'none'"}) {
		t.Fatalf("restored %v: %v", calls, err)
	}
	backend.supported = false
	if _, err := backend.Snapshot(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported desktop: %v", err)
	}
}
