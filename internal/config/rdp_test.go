package config

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// fullRDP has every option set, which TestRDPJSON checks, so that an
// option added later is round-tripped too.
func fullRDP() *RDPConfig {
	return &RDPConfig{
		Domain:   "CORP",
		Windowed: true, Width: 1600, Height: 900,
		AllMonitors: true, Monitors: "0,1", ColorDepth: 24,
		SmartSizing: true, NoDynamicResolution: true, NoConnectionBar: true,
		NoClipboard: true, Drives: "C:;D:", NoPrinters: true, NoSmartCards: true,
		Ports: true, Microphone: true, Audio: AudioRemote, NoWebAuthn: true, Cameras: true,
		Keyboard: KeyboardRemote, Program: `C:\Tools\app.exe`, ProgramFolder: `C:\Tools`,
		Network: NetworkLAN, NoBitmapCache: true, NoAutoReconnect: true,
		Admin: true, ServerAuth: ServerAuthRefuse, NoNLA: true,
		SSHHostID: "ssh-host", SSHTarget: "[fd00::1]:3390",
		GatewayHost: "gw.example.com", GatewayUser: `CORP\gw`, GatewayUsage: GatewayFallback,
		GatewayAuth: GatewayAuthPassword, GatewaySameLogin: true,
		Custom: "redirectlocation:i:1\nvideoplaybackmode:i:0",
	}
}

func TestRDPSSHTarget(t *testing.T) {
	h := &Host{Kind: KindRDP, Addr: "desktop.internal", Port: 3391}
	for _, test := range []struct {
		input, want string
	}{
		{"", "desktop.internal:3391"}, {"win", "win:3389"}, {"win:3390", "win:3390"},
		{"fd00::5", "[fd00::5]:3389"}, {"[fd00::5]:3390", "[fd00::5]:3390"},
		{"https://win", ""}, {"win:0", ""}, {"win:65536", ""}, {"win\nexample.com", ""},
	} {
		h.RDP = &RDPConfig{SSHTarget: test.input}
		got, err := RDPSSHTarget(h)
		if got != test.want || (err == nil) != (test.want != "") {
			t.Errorf("%q: %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestRDPJSON(t *testing.T) {
	full := fullRDP()
	v := reflect.ValueOf(full).Elem()
	for i := range v.NumField() {
		if f := v.Type().Field(i); f.IsExported() && v.Field(i).IsZero() {
			t.Errorf("fullRDP leaves %s unset", f.Name)
		}
	}
	tests := []struct {
		name string
		host *Host
		// keys the host's JSON must have, and must not.
		has, lacks []string
	}{
		{"ssh", &Host{ID: "s", Addr: "example.com", User: "root", Auth: AuthPassword}, nil, []string{"kind", "rdp"}},
		{"rdp defaults", &Host{ID: "r", Addr: "win", User: "Administrator", Auth: AuthPassword, Kind: KindRDP}, []string{"kind"}, []string{"rdp"}},
		{"rdp empty options", &Host{ID: "r", Addr: "win", Kind: KindRDP, RDP: &RDPConfig{}}, []string{"kind", "rdp"}, nil},
		{"rdp every option", &Host{ID: "r", Addr: "win", Port: 3390, Kind: KindRDP, RDP: full}, []string{"kind", "rdp"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.host)
			if err != nil {
				t.Fatal(err)
			}
			var keys map[string]json.RawMessage
			json.Unmarshal(data, &keys)
			for _, k := range tc.has {
				if _, ok := keys[k]; !ok {
					t.Errorf("no %q in %s", k, data)
				}
			}
			for _, k := range tc.lacks {
				if _, ok := keys[k]; ok {
					t.Errorf("%q in %s", k, data)
				}
			}
			var back Host
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(&back, tc.host) {
				t.Errorf("read back\n%+v\nwant\n%+v", back, *tc.host)
			}
		})
	}
	if got := string(must(json.Marshal(&RDPConfig{}))); got != "{}" {
		t.Errorf("the defaults write %s", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// The options' keys this build does not know are kept; a copy shares
// nothing.
func TestRDPUnknownKeysAndClone(t *testing.T) {
	var h Host
	if err := json.Unmarshal([]byte(`{"id": "r", "addr": "win", "kind": "rdp", "rdp": {"domain": "CORP", "future": [1]}}`), &h); err != nil {
		t.Fatal(err)
	}
	if !h.IsRDP() || h.RDP.Domain != "CORP" || string(h.RDP.extra["future"]) != "[1]" {
		t.Fatalf("read %+v %+v", h, h.RDP)
	}
	data, _ := json.Marshal(h)
	var back struct {
		RDP map[string]any `json:"rdp"`
	}
	json.Unmarshal(data, &back)
	if back.RDP["domain"] != "CORP" || back.RDP["future"] == nil {
		t.Errorf("wrote %s", data)
	}
	c := h.Clone()
	c.RDP.Domain = "OTHER"
	c.RDP.extra["future"] = []byte("[]")
	if h.RDP.Domain != "CORP" || string(h.RDP.extra["future"]) != "[1]" {
		t.Errorf("the clone shares the options: %+v", h.RDP)
	}
}

// What an older build, which knows no kinds, makes of a Remote Desktop
// host: an SSH host with a password on port 3389, its options kept as
// keys it does not know.
func TestRDPHostInOlderBuild(t *testing.T) {
	h := &Host{ID: "r", Addr: "win", Port: 3389, User: "Administrator", Auth: AuthPassword, Kind: KindRDP, RDP: &RDPConfig{Domain: "CORP"}}
	data, _ := json.Marshal(h)
	type older struct {
		ID   string   `json:"id"`
		Addr string   `json:"addr"`
		Port int      `json:"port"`
		User string   `json:"user"`
		Auth AuthKind `json:"auth"`
	}
	var o older
	if err := json.Unmarshal(data, &o); err != nil || o.Auth != AuthPassword || o.Port != 3389 || o.User != "Administrator" {
		t.Errorf("older build reads %+v (%v) from %s", o, err, data)
	}
}

func TestRDPHelpers(t *testing.T) {
	ssh := &Host{Addr: "example.com", User: "root"}
	rdp := &Host{Addr: "win", User: "admin", Kind: KindRDP}
	if ssh.PortOr22() != 22 || rdp.PortOr22() != 3389 || rdp.Target() != "admin@win" || ssh.IsRDP() || !rdp.IsRDP() {
		t.Errorf("ports %d %d target %q", ssh.PortOr22(), rdp.PortOr22(), rdp.Target())
	}
	rdp.Port = 3390
	if rdp.Target() != "admin@win:3390" {
		t.Errorf("target %q", rdp.Target())
	}
	if RDPOf(ssh) == nil || RDPOf(rdp).ColorDepthOrDefault() != 32 || RDPOf(rdp).UsesGateway() {
		t.Error("the defaults")
	}
	for _, tc := range []struct{ user, domain, want string }{
		{"admin", "", "admin"},
		{"admin", "CORP", `CORP\admin`},
		{`OTHER\admin`, "CORP", `OTHER\admin`},
		{"admin@corp.example", "CORP", "admin@corp.example"},
		{"", "CORP", ""},
	} {
		h := &Host{User: tc.user, Kind: KindRDP, RDP: &RDPConfig{Domain: tc.domain}}
		if got := RDPLogin(h); got != tc.want {
			t.Errorf("RDPLogin(%q, %q) = %q, want %q", tc.user, tc.domain, got, tc.want)
		}
	}
	gw := &RDPConfig{GatewayHost: "gw", GatewayUsage: GatewayAlways}
	if !gw.UsesGateway() {
		t.Error("gateway not used")
	}
	gw.GatewayUsage = GatewayOff
	if gw.UsesGateway() {
		t.Error("gateway off used")
	}
}

func TestNormalizeDrives(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"*", "*"},
		{" c, d ", "C:;D:"},
		{`C:\;D:;c:`, "C:;D:"},
		{"e f;G:", "E:;F:;G:"},
		{"C, *", "*"},
		{"CD, 1, é", ""},
	} {
		if got := NormalizeDrives(tc.in); got != tc.want {
			t.Errorf("NormalizeDrives(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := (&RDPConfig{Drives: "C:;D:"}).DriveLetters(); !slices.Equal(got, []string{"C", "D"}) {
		t.Errorf("letters %q", got)
	}
	if (&RDPConfig{Drives: "*"}).DriveLetters() != nil {
		t.Error("all drives as letters")
	}
}
