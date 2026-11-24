package config

import (
	"maps"
	"reflect"
	"strings"
	"unicode"
)

// HostKind is what a host is reached with.
type HostKind string

const (
	// KindSSH is an SSH server, as every host saved before hosts had a
	// kind.
	KindSSH HostKind = ""
	// KindRDP is a Windows computer reached with Remote Desktop: the
	// app opens the system's own client (mstsc on Windows, FreeRDP on
	// Linux, Microsoft's Windows App on macOS) on it. A version without
	// kinds shows such a host as an SSH host that logs in with a
	// password.
	KindRDP HostKind = "rdp"
)

// DefaultRDPPort is the port Remote Desktop listens on.
const DefaultRDPPort = 3389

// IsRDP says whether h is a Remote Desktop host.
func (h *Host) IsRDP() bool { return h.Kind == KindRDP }

// DefaultPort is the port of h's kind: 22, or 3389 for Remote Desktop.
func (h *Host) DefaultPort() int {
	switch h.Kind {
	case KindRDP:
		return DefaultRDPPort
	case KindTelnet:
		return DefaultTelnetPort
	case KindSerial:
		return 0
	}
	return 22
}

// RDPOf is h's Remote Desktop options, the defaults when it has none.
// It is never nil, and is h's own when h has some.
func RDPOf(h *Host) *RDPConfig {
	if h.RDP == nil {
		return &RDPConfig{}
	}
	return h.RDP
}

// RDPAudio is where a Remote Desktop session plays its sound.
type RDPAudio string

const (
	AudioLocal  RDPAudio = ""       // on this computer, mstsc's default
	AudioRemote RDPAudio = "remote" // on the remote computer
	AudioOff    RDPAudio = "off"    // nowhere
)

// RDPKeyboard is where Windows key combinations (Alt+Tab...) go.
type RDPKeyboard string

const (
	KeyboardFullScreen RDPKeyboard = ""       // to the remote computer in full screen only, mstsc's default
	KeyboardLocal      RDPKeyboard = "local"  // to this computer
	KeyboardRemote     RDPKeyboard = "remote" // to the remote computer
)

// RDPServerAuth is what to do when the remote computer's identity
// cannot be verified: mstsc's "authentication level".
type RDPServerAuth string

const (
	ServerAuthWarn    RDPServerAuth = ""        // warn, and let the user connect (2), mstsc's default
	ServerAuthConnect RDPServerAuth = "connect" // connect without warning (0)
	ServerAuthRefuse  RDPServerAuth = "refuse"  // do not connect (1)
	ServerAuthNone    RDPServerAuth = "none"    // no requirement (3)
)

// RDPNetwork is the connection speed a Remote Desktop session tunes its
// experience for: mstsc's "connection type".
type RDPNetwork string

const (
	NetworkAuto          RDPNetwork = ""              // detected (7), mstsc's default
	NetworkModem         RDPNetwork = "modem"         // 56 kbps (1)
	NetworkLowBroadband  RDPNetwork = "lowBroadband"  // 256 kbps to 2 Mbps (2)
	NetworkSatellite     RDPNetwork = "satellite"     // 2 to 16 Mbps with high latency (3)
	NetworkHighBroadband RDPNetwork = "highBroadband" // 2 to 10 Mbps (4)
	NetworkWAN           RDPNetwork = "wan"           // 10 Mbps or more with high latency (5)
	NetworkLAN           RDPNetwork = "lan"           // 10 Mbps or more (6)
)

// RDPGatewayUsage is when a Remote Desktop Gateway is used.
type RDPGatewayUsage string

const (
	GatewayOff      RDPGatewayUsage = ""         // never (0), mstsc's default
	GatewayAlways   RDPGatewayUsage = "always"   // always (1)
	GatewayFallback RDPGatewayUsage = "fallback" // when a direct connection fails (2)
	GatewayDetect   RDPGatewayUsage = "detect"   // as the system's default settings say (3)
)

// RDPGatewayAuth is how the client logs in to the gateway: mstsc's
// "gatewaycredentialssource".
type RDPGatewayAuth string

const (
	GatewayAuthAny       RDPGatewayAuth = ""          // chosen later (4), mstsc's default
	GatewayAuthPassword  RDPGatewayAuth = "password"  // a password, NTLM (0)
	GatewayAuthSmartCard RDPGatewayAuth = "smartcard" // a smart card (1)
)

// RDPConfig is a Remote Desktop host's options: mstsc's, as its .rdp
// files have them. The zero value is mstsc's defaults: an option that is
// on by default is kept as its inverse (NoClipboard...), as NoMonitor is,
// so that a host without the key, and a new RDPConfig{}, have it on. The
// user, the address and the port are the Host's; the password is kept as
// an SSH host's, in the credential store, and the gateway's too, under a
// key of its own.
type RDPConfig struct {
	// Domain is the user's Windows domain: DOMAIN\user.
	Domain string `json:"domain,omitempty"`

	// Display.

	// Windowed opens the session in a window, Width by Height, rather
	// than full screen.
	Windowed bool `json:"windowed,omitempty"`
	// Width and Height are the remote desktop's size: 0 for the client's
	// choice (the screen's in full screen).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// AllMonitors spans the session over every monitor, or those of
	// Monitors, the system's monitor IDs separated by commas ("0,1").
	AllMonitors bool   `json:"allMonitors,omitempty"`
	Monitors    string `json:"monitors,omitempty"`
	// ColorDepth is 15, 16, 24 or 32 bits per pixel: 0 for 32.
	ColorDepth int `json:"colorDepth,omitempty"`
	// SmartSizing scales the remote desktop to fit the window.
	SmartSizing bool `json:"smartSizing,omitempty"`
	// NoDynamicResolution keeps the remote desktop's size when the
	// window is resized.
	NoDynamicResolution bool `json:"noDynamicResolution,omitempty"`
	// NoConnectionBar hides the bar at the top of a full screen session.
	NoConnectionBar bool `json:"noConnectionBar,omitempty"`

	// Local resources.

	NoClipboard bool `json:"noClipboard,omitempty"`
	// Drives are the drives of this computer the session sees: "" for
	// none, "*" for all, else letters as "C:;D:" (NormalizeDrives).
	Drives       string      `json:"drives,omitempty"`
	NoPrinters   bool        `json:"noPrinters,omitempty"`
	NoSmartCards bool        `json:"noSmartCards,omitempty"`
	Ports        bool        `json:"ports,omitempty"`      // serial (COM) ports
	Microphone   bool        `json:"microphone,omitempty"` // recording from this computer
	Audio        RDPAudio    `json:"audio,omitempty"`
	NoWebAuthn   bool        `json:"noWebAuthn,omitempty"` // Windows Hello and security keys
	Cameras      bool        `json:"cameras,omitempty"`
	Keyboard     RDPKeyboard `json:"keyboard,omitempty"`

	// Program is a program to start on connecting instead of the
	// desktop, in the folder ProgramFolder.
	Program       string `json:"program,omitempty"`
	ProgramFolder string `json:"programFolder,omitempty"`

	// Experience.

	Network         RDPNetwork `json:"network,omitempty"`
	NoBitmapCache   bool       `json:"noBitmapCache,omitempty"` // persistent bitmap caching
	NoAutoReconnect bool       `json:"noAutoReconnect,omitempty"`

	// Connection.

	// Admin connects to the console (administrative) session: mstsc
	// /admin.
	Admin      bool          `json:"admin,omitempty"`
	ServerAuth RDPServerAuth `json:"serverAuth,omitempty"`
	// NoNLA turns off Network Level Authentication (CredSSP).
	NoNLA bool `json:"noNLA,omitempty"`
	// The Remote Desktop Gateway: its address (host or host:port) and
	// user, when to use it and how to log in to it. GatewaySameLogin
	// logs in to the remote computer with the gateway's user and
	// password.
	GatewayHost      string          `json:"gatewayHost,omitempty"`
	GatewayUser      string          `json:"gatewayUser,omitempty"`
	GatewayUsage     RDPGatewayUsage `json:"gatewayUsage,omitempty"`
	GatewayAuth      RDPGatewayAuth  `json:"gatewayAuth,omitempty"`
	GatewaySameLogin bool            `json:"gatewaySameLogin,omitempty"`

	// Custom are more lines of a .rdp file, as "name:type:value", added
	// at its end: what the options above do not have. Clients reading
	// .rdp files take the last of a name.
	Custom string `json:"custom,omitempty"`

	extra extras
}

// UsesGateway says whether the host connects through a Remote Desktop
// Gateway.
func (r *RDPConfig) UsesGateway() bool {
	return r.GatewayUsage != GatewayOff && strings.TrimSpace(r.GatewayHost) != ""
}

// ColorDepthOrDefault is the color depth in bits per pixel: 32 when
// unset.
func (r *RDPConfig) ColorDepthOrDefault() int {
	switch r.ColorDepth {
	case 15, 16, 24, 32:
		return r.ColorDepth
	}
	return 32
}

// NormalizeDrives turns what the user typed for the drives into Drives:
// "" for none, "*" for all, else the letters, each once, in order, as
// "C:;D:". Anything but letters, colons, separators and "*" is dropped.
func NormalizeDrives(s string) string {
	s = strings.TrimSpace(s)
	if s == "*" {
		return "*"
	}
	var out []string
	seen := map[rune]bool{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || unicode.IsSpace(r) }) {
		f = strings.TrimSuffix(strings.TrimSuffix(f, `\`), ":")
		if f == "*" {
			return "*"
		}
		if len(f) != 1 {
			continue
		}
		r := unicode.ToUpper(rune(f[0]))
		if r < 'A' || r > 'Z' || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, string(r)+":")
	}
	return strings.Join(out, ";")
}

// DriveLetters are the letters of Drives, without colons: nil for none
// or all.
func (r *RDPConfig) DriveLetters() []string {
	d := NormalizeDrives(r.Drives)
	if d == "" || d == "*" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(d, ";") {
		out = append(out, strings.TrimSuffix(l, ":"))
	}
	return out
}

// RDPLogin is the user as Remote Desktop clients take it: DOMAIN\user, or
// the user alone when there is no domain or the user already names one
// (DOMAIN\user, user@domain).
func RDPLogin(h *Host) string {
	user, domain := strings.TrimSpace(h.User), strings.TrimSpace(RDPOf(h).Domain)
	if user == "" || domain == "" || strings.ContainsAny(user, `\@`) {
		return user
	}
	return domain + `\` + user
}

// clone is a copy of r sharing nothing with it.
func (r *RDPConfig) clone() *RDPConfig {
	c := *r
	c.extra = maps.Clone(r.extra)
	return &c
}

type rdpJSON RDPConfig

var rdpKeys = knownKeys(reflect.TypeFor[rdpJSON]())

func (r RDPConfig) MarshalJSON() ([]byte, error) { return encodeObject(rdpJSON(r), r.extra) }

func (r *RDPConfig) UnmarshalJSON(data []byte) error {
	var v rdpJSON
	ex, err := decodeObject(data, &v, rdpKeys)
	if err != nil {
		return err
	}
	*r = RDPConfig(v)
	r.extra = ex
	return nil
}
