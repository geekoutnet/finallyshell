package app

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/rdp"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// fakeRDP stands in for the Remote Desktop client: it keeps what it was
// asked to open, and fails with err when set. Its wait returns waitErr.
type fakeRDP struct {
	mu      sync.Mutex
	opened  []rdp.Request
	err     error
	waitErr error
}

func (f *fakeRDP) start(r rdp.Request) (func() error, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.opened = append(f.opened, r)
	waitErr := f.waitErr
	return func() error { return waitErr }, nil
}

func (f *fakeRDP) requests() []rdp.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rdp.Request(nil), f.opened...)
}

func fakeRDPClient(a *App) *fakeRDP {
	f := &fakeRDP{}
	a.rdpStart = f.start
	return f
}

// The type choice switches the editor to the Remote Desktop pages, which
// have none of SSH's, and back; OK saves every option, and the password
// and the gateway's into the credential store.
func TestEditorRDP(t *testing.T) {
	a, p := testApp(t)
	saved := savedHosts(a)
	a.newConnection()
	tt := editorWindow(t, a, p)
	sshOnly := []string{locales.EditorPageTerminal(), locales.EditorPageProxy(), locales.EditorPageTunnels(), locales.FieldAuth(), locales.MonitorChannel()}
	rdpOnly := []string{locales.EditorPageRDPDisplay(), locales.EditorPageRDPResources(), locales.EditorPageRDPConnection(), locales.FieldDomain()}
	for _, s := range sshOnly {
		if !tt.HasText(s) {
			t.Errorf("SSH: no %q", s)
		}
	}

	choose(t, tt, locales.FieldHostKind(), locales.KindRDP())
	if a.editor.kind != config.KindRDP || fieldValue(t, tt, locales.FieldPort()) != "3389" {
		t.Fatalf("kind %q port %q", a.editor.kind, fieldValue(t, tt, locales.FieldPort()))
	}
	for _, s := range sshOnly {
		if tt.HasText(s) {
			t.Errorf("RDP shows %q", s)
		}
	}
	for _, s := range rdpOnly {
		if !tt.HasText(s) {
			t.Errorf("RDP: no %q; texts %q", s, tt.Texts())
		}
	}
	if _, ok := tt.AccessByRole(ui.RoleTextField, locales.FieldKeyFile()); ok {
		t.Error("RDP shows the key file")
	}
	// And back: the SSH pages, port 22; then Remote Desktop again.
	choose(t, tt, locales.FieldHostKind(), locales.KindSSH())
	if a.editor.kind != config.KindSSH || fieldValue(t, tt, locales.FieldPort()) != "22" || !tt.HasText(locales.EditorPageTunnels()) {
		t.Fatalf("back to SSH: kind %q port %q", a.editor.kind, fieldValue(t, tt, locales.FieldPort()))
	}
	choose(t, tt, locales.FieldHostKind(), locales.KindRDP())

	// OK without an address says so; no user is fine.
	typeInto(t, tt, locales.FieldUser(), "")
	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(locales.ErrHostRequired()) || tt.HasText(locales.ErrUserRequired()) || len(a.hosts.Hosts) != 0 {
		t.Fatalf("errors; texts %q", tt.Texts())
	}
	typeInto(t, tt, locales.FieldName(), "win-1")
	typeInto(t, tt, locales.FieldHost(), "10.0.0.5")
	typeInto(t, tt, locales.FieldUser(), "Administrator")
	typeInto(t, tt, locales.FieldDomain(), "CORP")
	typeInto(t, tt, locales.FieldPassword(), "pw")

	// Display: a window of a set size, which must be a size.
	if err := tt.Click(locales.EditorPageRDPDisplay()); err != nil {
		t.Fatal(err)
	}
	choose(t, tt, locales.FieldRDPSize(), locales.RdpSizeCustom())
	typeInto(t, tt, locales.FieldRDPWidth(), "12")
	typeInto(t, tt, locales.FieldRDPHeight(), "900")
	if !tt.HasText(locales.ErrRDPSize()) {
		t.Errorf("no size error; texts %q", tt.Texts())
	}
	typeInto(t, tt, locales.FieldRDPWidth(), "1600")
	choose(t, tt, locales.FieldRDPColors(), locales.RdpColors24())
	mustClick(t, tt, locales.RdpSmartSizing())
	mustClick(t, tt, locales.RdpAllMonitors())
	tt.Frame()
	typeInto(t, tt, locales.FieldRDPMonitors(), "0,1")
	choose(t, tt, locales.FieldRDPNetwork(), locales.RdpNetworkLAN())

	// Local resources.
	if err := tt.Click(locales.EditorPageRDPResources()); err != nil {
		t.Fatal(err)
	}
	choose(t, tt, locales.FieldRDPAudio(), locales.RdpAudioRemote())
	choose(t, tt, locales.FieldRDPKeyboard(), locales.RdpKeyboardRemote())
	mustClick(t, tt, locales.RdpClipboard())
	mustClick(t, tt, locales.RdpMicrophone())
	mustClick(t, tt, locales.RdpCameras())
	choose(t, tt, locales.FieldRDPDrives(), locales.RdpDrivesSome())
	typeInto(t, tt, locales.FieldRDPDriveLetters(), "c d")
	tt.Scroll(450, 300, 0, 600) // the program is below
	tt.Frame()
	typeInto(t, tt, locales.FieldRDPProgram(), "notepad.exe")

	// Connection: the console session and a gateway with its password.
	if err := tt.Click(locales.EditorPageRDPConnection()); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, locales.RdpAdmin())
	choose(t, tt, locales.FieldRDPServerAuth(), locales.RdpServerAuthConnect())
	choose(t, tt, locales.FieldRDPGatewayUsage(), locales.RdpGatewayAlways())
	typeInto(t, tt, locales.RdpGatewayHost(), "gw.example.com")
	typeInto(t, tt, locales.RdpGatewayUser(), `CORP\gw`)
	typeInto(t, tt, locales.RdpGatewayPasswordTitle(), "gw-pw")

	// Advanced: raw lines.
	if err := tt.Click(locales.SectionAdvanced()); err != nil {
		t.Fatal(err)
	}
	a.editor.rdp.cfg.Custom = "redirectlocation:i:1\n"
	tt.Frame()

	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	if a.windowOpen(WindowEditor) {
		t.Fatalf("the editor stayed open; texts %q", tt.Texts())
	}
	hosts := a.hosts.Live()
	if len(hosts) != 1 {
		t.Fatalf("hosts %v", hosts)
	}
	h := hosts[0]
	want := &config.RDPConfig{
		Domain: "CORP", Windowed: true, Width: 1600, Height: 900, AllMonitors: true, Monitors: "0,1", ColorDepth: 24,
		SmartSizing: true, NoClipboard: true, Drives: "C:;D:", Microphone: true, Audio: config.AudioRemote, Cameras: true,
		Keyboard: config.KeyboardRemote, Program: "notepad.exe", Network: config.NetworkLAN,
		Admin: true, ServerAuth: config.ServerAuthConnect,
		GatewayHost: "gw.example.com", GatewayUser: `CORP\gw`, GatewayUsage: config.GatewayAlways,
		Custom: "redirectlocation:i:1",
	}
	if !h.IsRDP() || h.Name != "win-1" || h.Addr != "10.0.0.5" || h.Port != 3389 || h.User != "Administrator" ||
		h.Auth != config.AuthPassword || h.Proxy != nil || h.Tunnels != nil || !reflect.DeepEqual(h.RDP, want) {
		t.Errorf("host %+v\nrdp %+v\nwant %+v", h, h.RDP, want)
	}
	if !strings.Contains(string(*saved), `"kind":"rdp"`) || !strings.Contains(string(*saved), `"gatewayHost":"gw.example.com"`) {
		t.Errorf("saved %s", *saved)
	}
	p.until(t, "the passwords stored", func() bool {
		s := p.secrets()
		return s[hostSecretKey(h.ID, secretPassword)] == "pw" && s[hostSecretKey(h.ID, secretGatewayPassword)] == "gw-pw"
	})

	// Edited again, it shows what was saved, and saving it again keeps it.
	a.editConnection(h)
	tt = editorWindow(t, a, p)
	if a.editor.kind != config.KindRDP || a.editor.rdp.size != rdpSizeCustom || a.editor.rdp.letters != "C, D" ||
		fieldValue(t, tt, locales.FieldDomain()) != "CORP" || !tt.HasText(locales.EditorPageRDPDisplay()) {
		t.Errorf("edit: %+v", a.editor.rdp)
	}
	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.RDP, want) {
		t.Errorf("saved again %+v", h.RDP)
	}

	// Back to SSH: its gateway's password goes; its options stay for
	// switching back.
	a.editConnection(h)
	tt = editorWindow(t, a, p)
	choose(t, tt, locales.FieldHostKind(), locales.KindSSH())
	typeInto(t, tt, locales.FieldUser(), "root")
	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	if h.IsRDP() || h.Port != 22 || h.RDP == nil {
		t.Errorf("as SSH %+v", h)
	}
	p.until(t, "the gateway password deleted", func() bool {
		_, ok := p.secrets()[hostSecretKey(h.ID, secretGatewayPassword)]
		return !ok
	})
}

// An SSH host saved by the editor has no kind and no Remote Desktop
// options in hosts.json.
func TestEditorSSHHostHasNoRDP(t *testing.T) {
	a, p := testApp(t)
	saved := savedHosts(a)
	a.newConnection()
	tt := editorWindow(t, a, p)
	typeInto(t, tt, locales.FieldHost(), "example.com")
	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	h := a.hosts.Live()[0]
	if h.Kind != config.KindSSH || h.RDP != nil || strings.Contains(string(*saved), `"rdp"`) || strings.Contains(string(*saved), `"kind"`) {
		t.Errorf("host %+v saved %s", h, *saved)
	}
}

func mustClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%s: %v; texts %q", label, err, tt.Texts())
	}
	tt.Frame()
}

// rdpHost is a Remote Desktop host on the Connections page of a's main
// window.
func rdpHost(a *App) *config.Host {
	h := &config.Host{ID: "r1", Name: "win-1", Addr: "10.0.0.5", Port: 3389, User: "Administrator", Auth: config.AuthPassword,
		Kind: config.KindRDP, RDP: &config.RDPConfig{Domain: "CORP"}}
	a.hosts.Add(h)
	return h
}

// Opening a Remote Desktop host asks for its password, then starts the
// client with it, and opens no session; the password is remembered, and
// the next open asks no more.
func TestOpenRDPHost(t *testing.T) {
	a, p := testApp(t)
	client := fakeRDPClient(a)
	h := rdpHost(a)
	a.setLeftPage(leftConnections)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	tt.Frame()
	sessions := len(a.sessions)

	rowMenu(t, tt, "win-1", locales.Connect())
	p.until(t, "the prompt", func() bool { return a.pwOpen })
	if a.pwNeed.who != "Administrator@10.0.0.5" || len(client.requests()) != 0 {
		t.Fatalf("prompt for %q; opened %v", a.pwNeed.who, client.requests())
	}
	tt.Type("s3cret")
	clickButton(t, tt, locales.Connect())
	tt.Frame()
	reqs := client.requests()
	if len(reqs) != 1 || reqs[0].Host.ID != h.ID || reqs[0].Host == h || reqs[0].Password != "s3cret" || reqs[0].Host.RDP.Domain != "CORP" {
		t.Fatalf("opened %+v", reqs)
	}
	if len(a.sessions) != sessions {
		t.Errorf("a session opened: %d, was %d", len(a.sessions), sessions)
	}
	for _, s := range a.sessions {
		if s.Host != nil {
			t.Errorf("a session to %s", s.Host.Title())
		}
	}
	if h.LastConnectedAt.IsZero() {
		t.Error("not recorded as connected")
	}
	if !tt.HasText(locales.RdpOpening()) && !tt.HasText(locales.RdpOpeningNoPassword()) {
		t.Errorf("no notice; texts %q", tt.Texts())
	}
	p.until(t, "the password stored", func() bool { return p.secrets()[hostSecretKey(h.ID, secretPassword)] == "s3cret" })

	// From the start page's recent list, at once with the password.
	a.setLeftPage(leftSessions)
	a.NewTab()
	tt.Advance(10 * time.Second) // the toast gone
	tt.Frame()
	tt.ClickAt(rowCenter(t, tt, "win-1"))
	tt.Frame()
	if reqs := client.requests(); len(reqs) != 2 || reqs[1].Password != "s3cret" || a.pwOpen {
		t.Fatalf("second open %+v, prompt %v", reqs, a.pwOpen)
	}
	if a.current() == nil || !a.current().isNewTab() {
		t.Error("the new tab was filled")
	}
	saveShot(t, tt, "rdp-start-en.png")
}

// A gateway's saved password goes to the client with the host's; a
// gateway with no password saved opens all the same, the client asking.
func TestOpenRDPHostGateway(t *testing.T) {
	a, p := testApp(t)
	client := fakeRDPClient(a)
	h := rdpHost(a)
	h.RDP.GatewayHost, h.RDP.GatewayUser, h.RDP.GatewayUsage = "gw", "gwuser", config.GatewayAlways
	p.put(hostSecretKey(h.ID, secretPassword), "pw")
	a.openSession(h)
	p.until(t, "the client", func() bool { return len(client.requests()) == 1 })
	if r := client.requests()[0]; r.Password != "pw" || r.GatewayPassword != "" || a.pwOpen {
		t.Errorf("request %+v, prompt %v", r, a.pwOpen)
	}
	p.put(hostSecretKey("r2", secretGatewayPassword), "gw-pw")
	h2 := h.Clone()
	h2.ID = "r2"
	a.hosts.Add(h2)
	a.passwords["r2"] = "pw2"
	a.openSession(h2)
	p.until(t, "the client", func() bool { return len(client.requests()) == 2 })
	if r := client.requests()[1]; r.Password != "pw2" || r.GatewayPassword != "gw-pw" {
		t.Errorf("request %+v", r)
	}
	// A host without a user is not asked for a password.
	h3 := &config.Host{ID: "r3", Addr: "10.0.0.9", Kind: config.KindRDP}
	a.hosts.Add(h3)
	a.openSession(h3)
	if reqs := client.requests(); len(reqs) != 3 || reqs[2].Password != "" || a.pwOpen {
		t.Errorf("no user: %+v, prompt %v", reqs, a.pwOpen)
	}
}

// The client not starting, or failing, says why in a dialog.
func TestOpenRDPHostFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err, wait error
		want      string
	}{
		{"no client", rdp.ErrNoClient, nil, describeRDPError(rdp.ErrNoClient)},
		{"start", errors.New("access denied"), nil, "access denied"},
		{"exit", nil, &rdp.ExitError{Code: 131, Output: "ERRCONNECT_LOGON_FAILURE"}, "ERRCONNECT_LOGON_FAILURE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, p := testApp(t)
			client := fakeRDPClient(a)
			client.err, client.waitErr = tc.err, tc.wait
			h := rdpHost(a)
			a.passwords[h.ID] = "pw"
			tt := ui.NewTester(a.View, 1280, 820)
			p.tt = tt
			a.openSession(h)
			p.until(t, "the dialog", func() bool { return a.rdpErr != nil })
			tt.Frame()
			if !tt.HasText(locales.RdpCannotOpen()) || !strings.Contains(strings.Join(tt.Texts(), "\n"), tc.want) {
				t.Errorf("texts %q", tt.Texts())
			}
			if tc.name == "no client" {
				saveShot(t, tt, "rdp-error-en.png")
			}
			clickButton(t, tt, locales.Ok())
			tt.Frame()
			if a.rdpErr != nil {
				t.Error("OK left the dialog")
			}
		})
	}
}

// A Remote Desktop host's connection info: where, who, its passwords
// and its gateway, no SSH command.
func TestRDPConnInfo(t *testing.T) {
	a, _ := testApp(t)
	h := rdpHost(a)
	h.RDP.GatewayHost, h.RDP.GatewayUser, h.RDP.GatewayUsage, h.RDP.Admin = "gw.example.com", "gwuser", config.GatewayAlways, true
	a.passwords[h.ID] = "pw"
	a.passwords[gatewaySecretKey(h.ID)] = "gw-pw"
	text := connInfoText(a.connInfoFields(h, false))
	for _, want := range []string{
		"Name: win-1\n", "Type: Remote Desktop (RDP)\n", "Host: 10.0.0.5\n", "Port: 3389\n", "User: Administrator\n",
		"Domain: CORP\n", "Password: pw\n", "Gateway server: gw.example.com\n", "Gateway user: gwuser\n",
		"Gateway password: gw-pw\n", "Address: 10.0.0.5:3389\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "ssh") || strings.Contains(text, locales.ConnInfoSSHCommand()) {
		t.Errorf("an SSH line in\n%s", text)
	}
	if needs := connSecretNeeds(h); len(needs) != 2 || !needs[1].gateway {
		t.Errorf("needs %+v", needs)
	}
}

// Screenshots of the editor's Remote Desktop pages and of the lists with
// a Remote Desktop host, light and dark, in both languages, for SHOT_DIR.
func TestRDPShots(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			a, p := testApp(t)
			p.SetLocale(lang)
			h := rdpHost(a)
			h.RDP.GatewayHost, h.RDP.GatewayUsage, h.RDP.Drives = "gw.example.com", config.GatewayAlways, "C:"
			a.hosts.Add(&config.Host{ID: "s1", Name: "web-1", Addr: "45.56.78.90", User: "root", Auth: config.AuthPassword})
			a.editConnection(h)
			tt := editorWindow(t, a, p)
			for _, page := range []struct {
				name  string
				title func() string
			}{
				{"general", locales.EditorPageRDP},
				{"display", locales.EditorPageRDPDisplay},
				{"resources", locales.EditorPageRDPResources},
				{"connection", locales.EditorPageRDPConnection},
				{"advanced", locales.SectionAdvanced},
			} {
				mustClick(t, tt, page.title())
				saveShot(t, tt, "editor-rdp-"+page.name+"-"+lang+".png")
				tt.SetDark(true)
				tt.Frame()
				saveShot(t, tt, "editor-rdp-"+page.name+"-dark-"+lang+".png")
				tt.SetDark(false)
				tt.Frame()
			}
			a.closeWindow(WindowEditor)

			a.openManager()
			m := p.show(t, WindowManager)
			m.Frame()
			if !m.HasText("win-1") || !m.HasText("3389") {
				t.Errorf("manager texts %q", m.Texts())
			}
			saveShot(t, m, "manager-rdp-"+lang+".png")
			m.SetDark(true)
			m.Frame()
			saveShot(t, m, "manager-rdp-dark-"+lang+".png")
			a.closeWindow(WindowManager)

			a.setLeftPage(leftConnections)
			main := ui.NewTester(a.View, 1280, 820)
			p.tt = main
			main.Frame()
			if !main.HasText("win-1") {
				t.Errorf("Connections page texts %q", main.Texts())
			}
			saveShot(t, main, "conntree-rdp-"+lang+".png")
			main.SetDark(true)
			main.Frame()
			saveShot(t, main, "conntree-rdp-dark-"+lang+".png")
		})
	}
}
