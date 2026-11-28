package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"
)

func TestDesktopToolsBrowserArgs(t *testing.T) {
	profile, addr, target := "isolated profile", "127.0.0.1:9123", "http://127.0.0.1:8080/"
	chromium := []string{"--user-data-dir=" + profile, "--proxy-server=socks5://" + addr,
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE 127.0.0.1", "--proxy-bypass-list=<-loopback>",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp", "--disable-quic", "--no-first-run", "--no-default-browser-check"}
	for _, test := range []struct {
		name            string
		firefox, newTab bool
		want            []string
	}{
		{"chromium window", false, false, append(slices.Clone(chromium), "--new-window", target)},
		{"chromium tab", false, true, append(slices.Clone(chromium), target)},
		{"firefox window", true, false, []string{"--new-instance", "--profile", profile, "--new-window", target}},
		{"firefox tab", true, true, []string{"--profile", profile, "--new-tab", target}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := desktopBrowserArgs(desktopBrowserChoice{firefox: test.firefox}, profile, addr, target, test.newTab); !slices.Equal(got, test.want) {
				t.Fatalf("arguments = %q, want %q", got, test.want)
			}
		})
	}
	for _, bad := range []string{"file:///tmp/a", "javascript:alert(1)", "https://name:password@example.org", "http://", "https://example.org/\na"} {
		if _, err := desktopBrowserURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestDesktopToolsBrowserDiscovery(t *testing.T) {
	chrome := filepath.Join("program", "Google", "Chrome", "Application", "chrome.exe")
	registry := filepath.Join("registry", "msedge.exe")
	got := desktopFindBrowsers("windows", func(key string) string {
		if key == "ProgramFiles" {
			return "program"
		}
		return ""
	}, func(name string) []string {
		if name == "msedge.exe" {
			return []string{registry}
		}
		return nil
	}, func(string) (string, error) { return "", io.EOF }, func(path string) bool { return path == chrome || path == registry })
	if len(got) != 2 || got[0].path != chrome || got[1].path != registry {
		t.Fatalf("browsers = %+v", got)
	}
}

func TestDesktopToolsBrowserProxy(t *testing.T) {
	server := sshtest.Start(t)
	client := server.Dial(t)
	tunnel, err := desktopBrowserProxy(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tunnel.Close() })
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, err := target.Accept()
		if err == nil {
			defer conn.Close()
			io.WriteString(conn, "through SSH")
		}
	}()
	conn, err := desktopBrowserDialer(tunnel.Addr().String()).DialContext(t.Context(), "tcp", target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if reply, err := io.ReadAll(conn); err != nil || string(reply) != "through SSH" {
		t.Fatalf("reply %q, %v", reply, err)
	}
	tunnel.Stop()
	if conn, err := net.Dial("tcp", tunnel.Addr().String()); err == nil {
		conn.Close()
		t.Fatal("proxy still listening")
	}
}

type desktopTestBrowserProcess struct {
	done chan struct{}
	end  sync.Once
}

func (process *desktopTestBrowserProcess) Wait() error { <-process.done; return nil }
func (process *desktopTestBrowserProcess) Quit()       { process.end.Do(func() { close(process.done) }) }
func (process *desktopTestBrowserProcess) Kill()       { process.Quit() }

func TestDesktopToolsBrowserWindow(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	server := sshtest.Start(t)
	session.client, session.Host.NoMonitor = server.Dial(t), true
	window := a.desktopOpen(session, appBrowser, nil)
	window.x, window.y = 130, 24
	if a.desktopOpen(session, appBrowser, nil) != window {
		t.Fatal("browser window not single")
	}
	state := window.state.(*desktopBrowserState)
	state.discovering = false
	state.choice = desktopBrowserChoice{name: "Test Chromium", path: "fake"}
	state.choices = []desktopBrowserChoice{state.choice}
	state.launch = func(*desktopBrowserRun, string) (desktopBrowserProcess, error) {
		return nil, errors.New("test secret URL")
	}
	tester.Frame()
	systemClick(t, tester, locales.DesktopBrowserStart())
	platform.until(t, "generic launch error", func() bool { return !state.starting && tester.HasText(locales.DesktopBrowserLaunchFailed()) })
	if tester.HasText("test secret URL") {
		t.Fatal("raw process error was exposed")
	}
	type tabCall struct {
		target string
		run    *desktopBrowserRun
	}
	tabs := make(chan tabCall, 1)
	state.openTab = func(_ context.Context, run *desktopBrowserRun, target string) error {
		tabs <- tabCall{target, run}
		return nil
	}
	for _, stopWith := range []string{"button", "disconnect"} {
		process := &desktopTestBrowserProcess{done: make(chan struct{})}
		state.launch = func(_ *desktopBrowserRun, target string) (desktopBrowserProcess, error) {
			if target != "about:blank" {
				return nil, errors.New("unexpected URL")
			}
			return process, nil
		}
		systemClick(t, tester, locales.DesktopBrowserStart())
		platform.until(t, "browser running", func() bool { return state.run != nil })
		addr := state.run.addr
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
		if stopWith == "button" {
			state.url = "http://127.0.0.1:8080/"
			tester.Frame()
			systemClick(t, tester, locales.OpenFile())
			platform.until(t, "new tab requested", func() bool { return !state.opening })
			select {
			case call := <-tabs:
				if call.target != state.url || call.run != state.run {
					t.Fatalf("wrong target or browser instance: %+v", call)
				}
			default:
				t.Fatal("no new tab requested")
			}
			systemClick(t, tester, locales.DesktopBrowserStop())
			state.url = ""
		} else {
			server.DropConnections()
		}
		platform.until(t, "browser and proxy stopped", func() bool { return state.run == nil })
		select {
		case <-process.done:
		default:
			t.Fatal("browser process not told to quit")
		}
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close()
			t.Fatal("browser proxy kept running")
		}
	}
}

func TestDesktopToolsBrowserProfilesAndPorts(t *testing.T) {
	root := t.TempDir()
	choice := desktopBrowserChoice{firefox: true}
	profile, cleanup, err := desktopBrowserProfile(choice, root, "server", "127.0.0.1:49888")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	data, err := os.ReadFile(filepath.Join(profile, "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range []string{`"network.proxy.type", 1`, `"network.proxy.socks_port", 49888`, `"network.proxy.socks_remote_dns", true`, `"network.proxy.allow_hijacking_localhost", true`} {
		if !strings.Contains(string(data), option) {
			t.Errorf("Firefox setting missing: %s", option)
		}
	}
	cleanup()
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary profile still present: %v", err)
	}
	choice.firefox = false
	profile, cleanup, err = desktopBrowserProfile(choice, root, "../outside", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if filepath.Dir(profile) != root {
		t.Fatalf("profile escaped root: %q", profile)
	}
	if _, _, err := desktopBrowserProfile(choice, root, "../outside", ""); !errors.Is(err, errDesktopBrowserProfileInUse) {
		t.Fatalf("duplicate profile: %v", err)
	}
	var snapshot monitor.NetSnapshot
	for _, address := range []string{"0.0.0.0:80", "[::]:8443", "127.0.0.1:80", "192.0.2.1:8088", "0.0.0.0:22"} {
		snapshot.Groups = append(snapshot.Groups, monitor.NetGroup{Listen: true, Proto: "tcp", Local: netip.MustParseAddrPort(address)})
	}
	if got := desktopBrowserPorts(snapshot); !slices.Equal(got, []int{80, 8443}) {
		t.Fatalf("ports = %v", got)
	}
}
