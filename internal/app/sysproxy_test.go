package app

import (
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/internal/sshx"
	"finallyshell/internal/sysproxy"
	"finallyshell/locales"
)

type appProxyBackend struct {
	change  chan sysproxy.Settings
	restore chan struct{}
}

func (backend appProxyBackend) Snapshot() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
func (backend appProxyBackend) Set(settings sysproxy.Settings, _ json.RawMessage) error {
	backend.change <- settings
	return nil
}
func (backend appProxyBackend) Restore(json.RawMessage) error {
	backend.restore <- struct{}{}
	return nil
}

func testSystemProxy(t *testing.T, a *App) appProxyBackend {
	t.Helper()
	if a.sysProxy.tail != nil {
		<-a.sysProxy.tail
	}
	backend := appProxyBackend{make(chan sysproxy.Settings, 4), make(chan struct{}, 4)}
	a.sysProxy.manager = sysproxy.New(filepath.Join(t.TempDir(), sysproxy.BackupName), backend)
	t.Cleanup(a.closeSystemProxy)
	return backend
}

func TestSystemProxyTunnelLifecycle(t *testing.T) {
	a, platform := testApp(t)
	backend := testSystemProxy(t, a)
	server := sshtest.Start(t)
	client := server.Dial(t)
	tunnel := config.Tunnel{Kind: config.TunnelDynamic, SystemProxy: true, PAC: []string{"all"}}
	start := func() *sshx.Tunnel {
		run, err := sshx.StartTunnel(client, tunnel, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(run.Stop)
		a.systemProxyTunnelStarted(nil, tunnel, run)
		select {
		case setting := <-backend.change:
			if setting.Address != run.Addr().String() || !setting.SOCKS || setting.PACURL == "" {
				t.Fatalf("wrong setting: %+v", setting)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("system proxy was not applied")
		}
		return run
	}
	first := start()
	second := start()
	first.Stop()
	select {
	case fn := <-platform.posts:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("stop was not posted")
	}
	<-a.sysProxy.tail
	if len(backend.restore) != 0 {
		t.Fatal("non-owning stop restored the settings")
	}
	second.Stop()
	select {
	case fn := <-platform.posts:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("active stop was not posted")
	}
	<-a.sysProxy.tail
	if len(backend.restore) != 1 {
		t.Fatal("active stop did not restore the settings")
	}
	start()
	a.CloseAll()
	if len(backend.restore) != 2 {
		t.Fatal("closing app did not restore the settings")
	}
}

func TestSystemProxyRestorePaletteConfirmation(t *testing.T) {
	a, platform := testApp(t)
	backend := testSystemProxy(t, a)
	a.applySystemProxy(nil, config.Tunnel{Kind: config.TunnelDynamic, SystemProxy: true}, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1080})
	<-a.sysProxy.tail
	a.openBulkPalette(nil)
	palette := platform.show(t, "command-palette")
	clickNamed(t, palette, locales.SysproxyRestore())
	dialog := platform.show(t, "restore-system-proxy")
	clickNamed(t, dialog, locales.Cancel())
	if len(backend.restore) != 0 {
		t.Fatal("cancel restored system settings")
	}
	a.confirmRestoreSystemProxy()
	dialog = platform.show(t, "restore-system-proxy")
	clickNamed(t, dialog, locales.SysproxyRestore())
	<-a.sysProxy.tail
	if len(backend.restore) != 1 {
		t.Fatal("confirm did not restore system settings")
	}
}

func TestTunnelRecipeOpenSSHLoopbackEcho(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	server := sshtest.Start(t)
	parsed, err := config.ParseSSHForwards("LocalForward 127.0.0.1:13306 " + listener.Addr().String())
	if err != nil || len(parsed.Tunnels) != 1 {
		t.Fatalf("parse %+v %v", parsed, err)
	}
	draft := parsed.Tunnels[0]
	draft.ListenPort = 0
	tunnel, err := sshx.StartTunnel(server.Dial(t), draft, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Stop()
	conn, err := net.DialTimeout("tcp4", tunnel.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	const sent = "loopback echo"
	if _, err := io.WriteString(conn, sent); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(sent))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != sent {
		t.Fatalf("echo %q: %v", got, err)
	}
}
