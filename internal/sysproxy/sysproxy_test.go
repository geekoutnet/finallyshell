package sysproxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeBackend struct {
	current     json.RawMessage
	changes     []Settings
	failSet     bool
	failRestore bool
}

func (backend *fakeBackend) Snapshot() (json.RawMessage, error) { return backend.current, nil }
func (backend *fakeBackend) Set(settings Settings, _ json.RawMessage) error {
	backend.changes = append(backend.changes, settings)
	backend.current = json.RawMessage(`{"mode":"changed"}`)
	if backend.failSet {
		return errors.New("set failed")
	}
	return nil
}
func (backend *fakeBackend) Restore(data json.RawMessage) error {
	if backend.failRestore {
		return errors.New("restore failed")
	}
	backend.current = data
	return nil
}

func TestSystemProxyApplyRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), BackupName)
	initial := json.RawMessage(`{"mode":"original"}`)
	backend := &fakeBackend{current: initial}
	manager := New(path, backend)
	if err := manager.Apply(Settings{Address: "127.0.0.1:1080", SOCKS: true, PAC: []string{"*.example.org"}}); err != nil {
		t.Fatal(err)
	}
	url := backend.changes[0].PACURL
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `shExpMatch(host, "*.example.org")`) {
		t.Fatalf("response %v %s %v", response.StatusCode, data, err)
	}
	if err := manager.Apply(Settings{Address: "127.0.0.1:3128"}); err != nil {
		t.Fatal(err)
	}
	if err := New(path, backend).Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.current, initial) {
		t.Fatalf("restored %s", backend.current)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup: %v", err)
	}
	backend.failSet, backend.failRestore = true, true
	if err := manager.Apply(Settings{Address: "127.0.0.1:3128"}); err == nil {
		t.Fatal("no error for partial write")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	backend.failRestore = false
	if err := manager.Restore(); err != nil || !reflect.DeepEqual(backend.current, initial) {
		t.Fatalf("recovery: %s %v", backend.current, err)
	}
}

func TestSystemProxyPAC(t *testing.T) {
	text, err := BuildPAC(Settings{Address: "127.0.0.1:1080", SOCKS: true, PAC: []string{"EXAMPLE.org", "*.example.org", "10.2.3.4/8", "fd00::/8"}})
	for _, want := range []string{`host === "example.org"`, `shExpMatch(host, "*.example.org")`, `isInNet(host, "10.0.0.0", "255.0.0.0")`, `isInNetEx(host, "fd00::/8")`, `"SOCKS5 127.0.0.1:1080"`, `return "DIRECT";`} {
		if err != nil || !strings.Contains(text, want) {
			t.Errorf("missing %s: %v\n%s", want, err, text)
		}
	}
	for _, rule := range []string{`foo"); return "DIRECT`, "10.0.0.0/33", "https://example.org", "", "*"} {
		if _, err := BuildPAC(Settings{Address: "127.0.0.1:1080", PAC: []string{rule}}); err == nil {
			t.Errorf("accepted %q", rule)
		}
	}
	if _, err := BuildPAC(Settings{Address: "0.0.0.0:1080", PAC: []string{"all"}}); err == nil {
		t.Fatal("accepted non-loopback address")
	}
	global, err := BuildPAC(Settings{Address: "127.0.0.1:3128", PAC: []string{"all"}})
	if err != nil || !strings.Contains(global, `return "PROXY 127.0.0.1:3128";`) || strings.Contains(global, "DIRECT") {
		t.Fatalf("global PAC: %s, %v", global, err)
	}
}
