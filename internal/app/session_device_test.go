package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestDeviceQuickConnect(t *testing.T) {
	for _, tc := range []struct {
		text, addr string
		port       int
		matched    bool
	}{
		{"telnet://console.example", "console.example", 23, true},
		{"Telnet://[2001:db8::1]:2323", "2001:db8::1", 2323, true},
		{"telnet://console.example:0", "", 0, true},
		{"telnet://console.example:", "", 0, true},
		{"telnet://console.example/path", "", 0, true},
		{"telnet://operator@console.example", "", 0, true},
		{"telnet://console.example?", "", 0, true},
		{"telnet://2001:db8::1", "", 0, true},
		{"ssh", "", 0, false},
	} {
		host, matched := parseTelnetQuick(tc.text)
		if matched != tc.matched || tc.addr == "" && host != nil || tc.addr != "" &&
			(host == nil || host.Addr != tc.addr || host.Port != tc.port || host.Kind != config.KindTelnet || host.ID != "") {
			t.Errorf("%q: %+v, %t", tc.text, host, matched)
		}
	}
	a, p := testApp(t)
	a.NewTab()
	calls := make(chan string, 1)
	a.telnetDial = func(_ context.Context, network, address string) (net.Conn, error) {
		calls <- network + " " + address
		return nil, errors.New("fake dial result")
	}
	tt := ui.NewTester(a.View, 960, 720)
	tt.Frame()
	clickKindControl(t, tt, ui.RoleTextField, locales.QuickConnect())
	tt.Type("telnet://console.example:2323")
	tt.Key(0, ui.KeyEnter)
	p.until(t, "quick Telnet connection", func() bool { return len(calls) == 1 })
	if got := <-calls; got != "tcp console.example:2323" || len(a.hosts.Live()) != 0 {
		t.Fatalf("quick connect dialed %q or saved a transient host", got)
	}
}

func deviceTestPort() string {
	switch runtime.GOOS {
	case "windows":
		return "COM4"
	case "darwin":
		return "/dev/cu.console"
	default:
		return "/dev/ttyUSB0"
	}
}

func TestDeviceEditor(t *testing.T) {
	a, p := testApp(t)
	a.serialPorts = func() ([]string, error) { return []string{deviceTestPort()}, nil }
	a.openEditor(nil, WindowMain)
	ed := a.editor
	ed.setKind(config.KindTelnet)
	ed.addr, ed.port, ed.user = "console.example", "23", "operator"
	ed.telnet.AutoLogin, ed.telnet.LoginPrompt = true, "["
	ed.nav.Page = editorTerminal
	a.saveEditor()
	if ed.nav.Page != editorTelnetLogin || a.editor != ed || ed.problems()[fieldTelnetUserPrompt] == "" {
		t.Fatal("invalid Telnet prompt did not block saving")
	}
	tt := p.show(t, WindowEditor)
	tt.Frame()
	wantTexts(t, tt, "Telnet editor", locales.TelnetAutoLogin(), locales.TelnetPromptInvalid())
	ed.telnet.LoginPrompt = ""
	a.saveEditor()
	if a.windowOpen(WindowEditor) || len(a.hosts.Live()) != 1 || a.hosts.Live()[0].Kind != config.KindTelnet {
		t.Fatal("valid Telnet host was not saved")
	}
	a.openEditor(nil, WindowMain)
	ed = a.editor
	ed.setKind(config.KindSerial)
	ed.serial.device, ed.serial.baud = deviceTestPort(), "invalid"
	a.saveEditor()
	if a.editor != ed || ed.problems()[fieldSerialBaud] == "" || len(ed.serial.ports) != 1 {
		t.Fatal("serial list or validation failed")
	}
	ed.serial.baud = "12345"
	a.saveEditor()
	if h := a.hosts.Host(ed.id); a.windowOpen(WindowEditor) || h == nil || h.Kind != config.KindSerial || h.Addr != deviceTestPort() || h.Serial.Baud != 12345 {
		t.Fatalf("valid serial host was not saved: %+v", h)
	}
}

func TestDeviceTelnetConfirmation(t *testing.T) {
	a, p := testApp(t)
	h := &config.Host{Addr: "console.example", User: "operator", Auth: config.AuthPassword, Kind: config.KindTelnet, Telnet: &config.TelnetConfig{AutoLogin: true}}
	a.hosts.Add(h)
	secret := "fake-" + "credential"
	if err := p.SecretSet(hostSecretKey(h.ID, secretTelnetPassword), secret); err != nil {
		t.Fatal(err)
	}
	calls := make(chan string, 2)
	a.telnetDial = func(_ context.Context, network, address string) (net.Conn, error) {
		calls <- network + " " + address
		return nil, errors.New("fake connection failure")
	}
	a.openSessionIn(nil, h)
	p.until(t, "security confirmation", func() bool { return len(p.questions()) == 1 })
	question := p.questions()[0]
	if strings.Contains(question.Detail, secret) || !strings.Contains(question.Detail, h.Target()) || len(calls) != 0 {
		t.Fatalf("unsafe confirmation or cancel result")
	}
	p.fakeSystem.mu.Lock()
	p.answer = true
	p.fakeSystem.mu.Unlock()
	a.openSessionIn(nil, h)
	p.until(t, "fake connection failure", func() bool {
		return a.current() != nil && !a.current().isNewTab() && a.current().status == statusFailed
	})
	if got := <-calls; got != "tcp console.example:23" {
		t.Errorf("dialed %q", got)
	}
	tt := ui.NewTester(a.View, 960, 720)
	tt.Frame()
	wantTexts(t, tt, "connection error", "fake connection failure", locales.TelnetUnavailable())
	text := connInfoText(a.connInfoFields(h, false))
	if strings.Contains(text, secret) || strings.Contains(text, "ssh://") {
		t.Fatal("connection info exposed a password or SSH command")
	}
	for _, key := range a.syncSecretKeys() {
		if key == hostSecretKey(h.ID, secretTelnetPassword) {
			t.Fatal("Telnet passwords must not be synced")
		}
	}
}

func TestDeviceFilterAndCredentialIsolation(t *testing.T) {
	a, p := testApp(t)
	ssh := &config.Host{Name: "Build server", Addr: "build.example", User: "dev", Auth: config.AuthPassword}
	network := &config.Host{Name: "Lab router", Addr: "router.example", Kind: config.KindTelnet}
	device := &config.Host{Name: "Bench console", Addr: deviceTestPort(), Kind: config.KindSerial}
	for _, host := range []*config.Host{ssh, network, device} {
		a.hosts.Add(host)
	}
	a.restoreLeftPanel()
	a.setLeftPage(leftConnections)
	tt := ui.NewTester(a.View, 1280, 820)
	tt.Frame()
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.TelnetKind())
	if a.connTree.kind != kindsTelnet || strings.Join(connShown(a), "|") != network.Name {
		t.Fatal("Telnet filter did not isolate Telnet hosts")
	}
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.SerialKind())
	if a.connTree.kind != kindsSerial || strings.Join(connShown(a), "|") != device.Name {
		t.Fatal("serial filter did not isolate serial hosts")
	}
	a.passwords[ssh.ID] = "fake-" + "ssh-credential"
	a.openEditor(ssh, WindowMain)
	ed := a.editor
	if ed.password == "" {
		t.Fatal("fixture did not load the SSH password")
	}
	ed.setKind(config.KindTelnet)
	if ed.password != "" || ed.passphrase != "" {
		t.Fatal("changing the kind carried an SSH credential into Telnet")
	}
	ed.telnet.AutoLogin = true
	a.saveEditor()
	p.until(t, "secrets queue", a.secrets.queue.idle)
	if ssh.Kind != config.KindTelnet || a.passwords[telnetPasswordKey(ssh.ID)] != "" || len(p.questions()) != 0 {
		t.Fatal("changing the kind stored or used an SSH password for Telnet")
	}
}

func TestDeviceSerialSession(t *testing.T) {
	a, p := testApp(t)
	h := &config.Host{Addr: deviceTestPort(), Kind: config.KindSerial, Serial: &config.SerialConfig{Baud: 9600, Flow: "xonxoff"}}
	client, server := net.Pipe()
	t.Cleanup(func() { server.Close() })
	a.serialOpen = func(name string, options config.SerialConfig) (io.ReadWriteCloser, error) {
		if name != h.Addr || options != *h.Serial {
			return nil, fmt.Errorf("wrong serial configuration")
		}
		return client, nil
	}
	a.openSessionIn(a.current(), h)
	session := a.current()
	t.Cleanup(session.disconnect)
	p.until(t, "serial connection", func() bool { return session.status != statusConnecting })
	if session.status != statusConnected || session.client != nil || canSplit(session) {
		t.Fatalf("serial session did not open correctly: %v", session.err)
	}
	a.openPanelTab(session, tabFiles)
	tt := ui.NewTester(a.View, 1280, 820)
	for _, tab := range []int{tabFiles, tabTransfers, tabTunnels, tabProcesses, tabNetwork, tabOverview, tabDocker, tabSystem} {
		a.current().panelTab = tab
		tt.Frame()
		wantTexts(t, tt, strconv.Itoa(tab), locales.SerialUnavailable())
	}
}

func TestDeviceScreenshots(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(lang+"/"+appearance, func(t *testing.T) {
				a, p := testApp(t)
				p.SetLocale(lang)
				dark := appearance == "dark"
				suffix := lang + "-" + appearance + ".png"
				a.serialPorts = func() ([]string, error) { return []string{deviceTestPort()}, nil }
				saved := &config.Host{Kind: config.KindTelnet, Name: "Lab router", Addr: "router.example", User: "operator", Auth: config.AuthPassword, Telnet: &config.TelnetConfig{AutoLogin: true}}
				device := &config.Host{Kind: config.KindSerial, Name: "Bench console", Addr: deviceTestPort(), Serial: &config.SerialConfig{Baud: 115200}}
				for _, h := range []*config.Host{saved, device, {Name: "Build server", Addr: "build.example"}, {Kind: config.KindRDP, Name: "Workstation", Addr: "desk.example"}} {
					a.hosts.Add(h)
				}
				tt := ui.NewTester(a.View, 1280, 820)
				tt.SetDark(dark)
				tt.SetLocale(lang)
				p.tt = tt
				tt.Frame()
				wantTexts(t, tt, "hosts", locales.TelnetKind(), locales.SerialKind(), saved.Name, device.Name)
				if !kindsTelnet.keeps(saved) || kindsSSH.keeps(saved) || !kindsSerial.keeps(device) || kindsRDP.keeps(device) || kindLook(saved).icon == kindLook(device).icon || !hostMatches(device, []string{"串口"}) {
					t.Fatal("wrong icons or filters")
				}
				saveShot(t, tt, "device-hosts-"+suffix)
				for name, host := range map[string]*config.Host{"telnet": saved, "serial": device} {
					a.openEditor(host, WindowMain)
					editor := p.show(t, WindowEditor)
					editor.SetDark(dark)
					editor.Frame()
					saveShot(t, editor, name+"-editor-"+suffix)
					if host.Kind == config.KindTelnet {
						a.editor.nav.Page = editorTelnetLogin
						editor.Frame()
						saveShot(t, editor, "telnet-login-"+suffix)
					}
					a.closeWindow(WindowEditor)
				}
				client, server := net.Pipe()
				defer server.Close()
				a.telnetDial = func(context.Context, string, string) (net.Conn, error) { return client, nil }
				a.secrets.markRead(loginSecretKey(saved))
				a.openSessionIn(a.current(), saved)
				session := a.current()
				defer session.disconnect()
				p.until(t, "Telnet connection", func() bool { return session.status != statusConnecting })
				if session.status != statusConnected {
					t.Fatal(session.err)
				}
				tt.Frame()
				sent := make(chan error, 1)
				go func() {
					server.SetWriteDeadline(time.Now().Add(2 * time.Second))
					_, err := io.WriteString(server, "Lab router / Telnet\r\nSystem ready.\r\nrouter> ")
					sent <- err
				}()
				if err := <-sent; err != nil {
					t.Fatal(err)
				}
				p.until(t, "fake terminal banner", func() bool { return strings.Contains(session.term().Text(), "router>") })
				tt.Frame()
				saveShot(t, tt, "telnet-terminal-"+suffix)
				a.openPanelTab(session, tabDocker)
				tt.Frame()
				wantTexts(t, tt, "full panel", locales.TelnetUnavailable())
				saveShot(t, tt, "telnet-panel-full-"+suffix)
				a.backToTerminal(a.current())
				short := ui.NewTester(func(c *ui.Context) { useTheme(c); a.bottomPanel(c, &a.bottomTab) }, 460, 280)
				a.bottomTab = tabNetwork
				short.SetDark(dark)
				short.SetLocale(lang)
				short.Frame()
				wantTexts(t, short, "short panel", locales.TelnetUnavailable())
				saveShot(t, short, "telnet-panel-short-"+suffix)
			})
		}
	}
}
