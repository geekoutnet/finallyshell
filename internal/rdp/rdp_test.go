package rdp

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"finallyshell/internal/config"
)

// lines are the lines of a .rdp file.
func lines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n")
}

func TestFile(t *testing.T) {
	tests := []struct {
		name string
		host *config.Host
		// want are lines the file has; lacks, names it must not have.
		want  []string
		lacks []string
	}{
		{
			name: "defaults",
			host: &config.Host{Addr: "win.example.com", User: "Administrator", Kind: config.KindRDP},
			want: []string{
				"full address:s:win.example.com:3389", `username:s:Administrator`, "prompt for credentials:i:0",
				"administrative session:i:0", "screen mode id:i:2", "use multimon:i:0", "session bpp:i:32",
				"smart sizing:i:0", "dynamic resolution:i:1", "displayconnectionbar:i:1",
				"redirectclipboard:i:1", "redirectprinters:i:1", "redirectsmartcards:i:1", "redirectcomports:i:0",
				"audiocapturemode:i:0", "audiomode:i:0", "redirectwebauthn:i:1", "keyboardhook:i:2",
				"connection type:i:7", "networkautodetect:i:1", "bandwidthautodetect:i:1",
				"bitmapcachepersistenable:i:1", "autoreconnection enabled:i:1",
				"authentication level:i:2", "enablecredsspsupport:i:1", "gatewayusagemethod:i:0",
			},
			lacks: []string{"desktopwidth", "drivestoredirect", "selectedmonitors", "gatewayhostname", "camerastoredirect", "alternate shell", "password"},
		},
		{
			name: "every option",
			host: &config.Host{Name: "build box", Addr: "10.0.0.5", Port: 3390, User: "ops", Kind: config.KindRDP, RDP: &config.RDPConfig{
				Domain: "CORP", Windowed: true, Width: 1600, Height: 900, AllMonitors: true, Monitors: "0, 1", ColorDepth: 16,
				SmartSizing: true, NoDynamicResolution: true, NoConnectionBar: true,
				NoClipboard: true, Drives: "c d", NoPrinters: true, NoSmartCards: true, Ports: true, Microphone: true,
				Audio: config.AudioRemote, NoWebAuthn: true, Cameras: true, Keyboard: config.KeyboardLocal,
				Program: `C:\Tools\app.exe`, ProgramFolder: `C:\Tools`,
				Network: config.NetworkLAN, NoBitmapCache: true, NoAutoReconnect: true,
				Admin: true, ServerAuth: config.ServerAuthRefuse, NoNLA: true,
				GatewayHost: "gw.example.com:443", GatewayUser: `CORP\gw`, GatewayUsage: config.GatewayFallback,
				GatewayAuth: config.GatewayAuthPassword, GatewaySameLogin: true,
				Custom: "redirectlocation:i:1\r\n\n  videoplaybackmode:i:0  \n",
			}},
			want: []string{
				"full address:s:10.0.0.5:3390", `username:s:CORP\ops`, "administrative session:i:1",
				"screen mode id:i:1", "desktopwidth:i:1600", "desktopheight:i:900", "use multimon:i:1", "selectedmonitors:s:0,1",
				"session bpp:i:16", "smart sizing:i:1", "dynamic resolution:i:0", "displayconnectionbar:i:0",
				"redirectclipboard:i:0", "drivestoredirect:s:C:;D:", "redirectprinters:i:0", "redirectsmartcards:i:0",
				"redirectcomports:i:1", "audiocapturemode:i:1", "audiomode:i:1", "redirectwebauthn:i:0",
				"camerastoredirect:s:*", "keyboardhook:i:0",
				`alternate shell:s:C:\Tools\app.exe`, `shell working directory:s:C:\Tools`,
				"connection type:i:6", "networkautodetect:i:0", "bitmapcachepersistenable:i:0", "autoreconnection enabled:i:0",
				"authentication level:i:1", "enablecredsspsupport:i:0",
				"gatewayhostname:s:gw.example.com:443", "gatewayusagemethod:i:2", "gatewayprofileusagemethod:i:1",
				"gatewaycredentialssource:i:0", "promptcredentialonce:i:1", `gatewayusername:s:CORP\gw`,
				"redirectlocation:i:1", "videoplaybackmode:i:0",
			},
			lacks: []string{"bandwidthautodetect"},
		},
		{
			name: "all drives, audio off, IPv6, a user with its domain",
			host: &config.Host{Addr: "fe80::1", User: "admin@corp.example", Kind: config.KindRDP, RDP: &config.RDPConfig{
				Domain: "CORP", Drives: "*", Audio: config.AudioOff, Keyboard: config.KeyboardRemote,
				ServerAuth: config.ServerAuthConnect, Network: config.NetworkModem,
				GatewayHost: "gw", GatewayUsage: config.GatewayAlways,
			}},
			want: []string{
				"full address:s:[fe80::1]:3389", "username:s:admin@corp.example", "drivestoredirect:s:*", "audiomode:i:2",
				"keyboardhook:i:1", "authentication level:i:0", "connection type:i:1",
				"gatewayhostname:s:gw", "gatewayusagemethod:i:1", "gatewaycredentialssource:i:4", "promptcredentialonce:i:0",
			},
			lacks: []string{"gatewayusername"},
		},
		{
			name:  "a gateway without a host is not used; no user, no username",
			host:  &config.Host{Addr: "win", Kind: config.KindRDP, RDP: &config.RDPConfig{GatewayUsage: config.GatewayAlways, AllMonitors: true}},
			want:  []string{"gatewayusagemethod:i:0", "use multimon:i:1"},
			lacks: []string{"gatewayhostname", "username", "selectedmonitors"},
		},
		{
			name: "values stay on their line",
			host: &config.Host{Addr: "win\nfull address:s:evil", User: "a\r\nb", Kind: config.KindRDP},
			want: []string{"full address:s:[win full address:s:evil]:3389", "username:s:a b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := File(tc.host)
			if !strings.HasSuffix(got, "\r\n") {
				t.Error("the last line has no CRLF")
			}
			ls := lines(got)
			for _, w := range tc.want {
				if !slices.Contains(ls, w) {
					t.Errorf("no line %q in\n%s", w, got)
				}
			}
			for _, name := range tc.lacks {
				for _, l := range ls {
					if strings.HasPrefix(l, name+":") {
						t.Errorf("has %q", l)
					}
				}
			}
			// Each name once, but for the custom lines at the end.
			seen := map[string]bool{}
			for _, l := range ls {
				name, _, _ := strings.Cut(l, ":")
				if seen[name] && !strings.Contains(config.RDPOf(tc.host).Custom, l) {
					t.Errorf("%q twice", name)
				}
				seen[name] = true
			}
		})
	}
	// The custom lines come last, so that they win.
	h := &config.Host{Addr: "win", Kind: config.KindRDP, RDP: &config.RDPConfig{Custom: "session bpp:i:8"}}
	if ls := lines(File(h)); ls[len(ls)-1] != "session bpp:i:8" {
		t.Errorf("last line %q", ls[len(ls)-1])
	}
}

func TestFreeRDPArgs(t *testing.T) {
	plain := &config.Host{Name: "win", Addr: "10.0.0.5", User: "Administrator", Kind: config.KindRDP}
	full := &config.Host{Name: "box", Addr: "fe80::1", Port: 3390, User: `CORP\ops`, Kind: config.KindRDP, RDP: &config.RDPConfig{
		Domain: "IGNORED", Windowed: true, Width: 1280, Height: 720, AllMonitors: true, Monitors: "0, 2", ColorDepth: 24,
		SmartSizing: true, NoClipboard: true, Drives: "*", NoPrinters: true, NoSmartCards: true, Ports: true, Microphone: true,
		Audio: config.AudioRemote, Keyboard: config.KeyboardLocal, Program: "notepad.exe", ProgramFolder: `C:\`,
		Network: config.NetworkWAN, NoAutoReconnect: true, Admin: true, ServerAuth: config.ServerAuthConnect, NoNLA: true,
		GatewayHost: "gw.example.com", GatewayUser: `CORP\gw`, GatewayUsage: config.GatewayAlways,
	}}
	tests := []struct {
		name     string
		host     *config.Host
		version  int
		stdin    bool
		gwPass   string
		want     []string
		unwanted []string
	}{
		{
			name: "defaults, FreeRDP 2", host: plain, version: 2, stdin: true,
			want: []string{"/v:10.0.0.5:3389", "/u:Administrator", "/from-stdin:force", "/t:win", "/f", "/bpp:32",
				"/dynamic-resolution", "+clipboard", "/printer", "/smartcard", "/sound", "/network:auto", "+auto-reconnect", "/cert-tofu"},
			unwanted: []string{"/d:", "/size:", "/multimon", "/admin", "-sec-nla", "/g:", "/gateway:", "+drives", "/cert:tofu", "/p:"},
		},
		{
			name: "defaults, FreeRDP 3, no password", host: plain, version: 3,
			want:     []string{"/v:10.0.0.5:3389", "/u:Administrator", "/cert:tofu"},
			unwanted: []string{"/from-stdin:force", "/cert-tofu"},
		},
		{
			name: "every option, FreeRDP 2", host: full, version: 2, stdin: true, gwPass: "gw-pw",
			want: []string{"/v:[fe80::1]:3390", "/u:ops", "/d:CORP", "/from-stdin:force", "/t:box", "/size:1280x720",
				"/multimon", "/monitors:0,2", "/bpp:24", "/smart-sizing", "-clipboard", "+drives", "/serial", "/microphone",
				"/audio-mode:1", "-grab-keyboard", "/shell:notepad.exe", `/shell-dir:C:\`, "/network:wan", "/admin",
				"/cert-ignore", "-sec-nla", "/g:gw.example.com", "/gu:gw", "/gd:CORP", "/gp:gw-pw", "/gateway-usage-method:direct"},
			unwanted: []string{"/f", "/dynamic-resolution", "/printer", "/smartcard", "/sound", "+auto-reconnect", "/d:IGNORED"},
		},
		{
			name: "every option, FreeRDP 3", host: full, version: 3, gwPass: "gw-pw",
			want:     []string{"/cert:ignore", "/gateway:g:gw.example.com,u:gw,d:CORP,p:gw-pw,usage-method:direct"},
			unwanted: []string{"/g:gw.example.com", "/gp:gw-pw", "/from-stdin:force"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FreeRDPArgs(tc.host, tc.version, tc.stdin, tc.gwPass)
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("no %q in %q", w, got)
				}
			}
			for _, u := range tc.unwanted {
				for _, a := range got {
					if a == u || strings.HasSuffix(u, ":") && strings.HasPrefix(a, u) {
						t.Errorf("has %q: %q", a, got)
					}
				}
			}
		})
	}
	// The domain of the options, for a user without one; drive letters
	// share the home folder; a gateway with the same login has no user.
	h := &config.Host{Addr: "w", User: "u", Kind: config.KindRDP, RDP: &config.RDPConfig{Domain: "D", Drives: "C:",
		GatewayHost: "g", GatewayUser: "x", GatewaySameLogin: true, GatewayUsage: config.GatewayFallback}}
	got := FreeRDPArgs(h, 3, false, "pw")
	for _, w := range []string{"/u:u", "/d:D", "+home-drive", "/gateway:g:g,usage-method:detect"} {
		if !slices.Contains(got, w) {
			t.Errorf("no %q in %q", w, got)
		}
	}
}

func TestFreeRDPVersion(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      int
	}{
		{"xfreerdp3", "", 3},
		{"xfreerdp", "This is FreeRDP version 3.5.1 (3.5.1)\n", 3},
		{"xfreerdp", "This is FreeRDP version 2.11.5 (2.11.5)\n", 2},
		{"wlfreerdp", "", 2},
	} {
		if got := freeRDPVersion(tc.name, tc.out); got != tc.want {
			t.Errorf("freeRDPVersion(%q, %q) = %d, want %d", tc.name, tc.out, got, tc.want)
		}
	}
}

func TestNames(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"web: prod/1", "web_ prod_1.rdp"},
		{"  ..  ", "remote-desktop.rdp"},
		{"服务器", "服务器.rdp"},
	} {
		if got := fileName(&config.Host{Name: tc.title}); got != tc.want {
			t.Errorf("fileName(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"gw.example.com", "gw.example.com"},
		{"gw.example.com:443", "gw.example.com"},
		{"[fe80::1]:443", "fe80::1"},
		{"fe80::1", "fe80::1"},
	} {
		if got := gatewayName(tc.in); got != tc.want {
			t.Errorf("gatewayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The file is written where only the user can read it.
func TestWriteFile(t *testing.T) {
	h := &config.Host{Name: "win", Addr: "127.0.0.2", Port: 1, Kind: config.KindRDP}
	path, dir, err := writeFile(h)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if filepath.Dir(path) != dir || filepath.Base(path) != "win.rdp" {
		t.Errorf("path %s in %s", path, dir)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != File(h) {
		t.Errorf("read %q (%v)", data, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
			t.Errorf("folder mode %v", fi.Mode())
		}
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode %v", fi.Mode())
		}
	}
}

func TestTail(t *testing.T) {
	tl := &tail{n: 5}
	tl.Write([]byte("hello "))
	tl.Write([]byte("world\n"))
	if got := tl.String(); got != "orld" {
		t.Errorf("tail %q", got)
	}
}
