// Package rdp opens Remote Desktop hosts with the system's own client,
// which has every feature of the protocol: mstsc on Windows, FreeRDP
// (xfreerdp) on Linux, and on macOS the app that opens .rdp files,
// Microsoft's Windows App. File writes the .rdp file mstsc and the
// Windows App read; FreeRDPArgs are FreeRDP's arguments for the same
// options. Start (per platform) starts the client.
package rdp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"finallyshell/internal/config"
)

// Request is what opening a host takes: a copy of the host, and the
// passwords the app knows, "" for those it does not, which the client
// asks for.
type Request struct {
	Host            *config.Host
	Password        string
	GatewayPassword string
}

// ErrNoClient is the system having no Remote Desktop client the app
// can start.
var ErrNoClient = errors.New("no Remote Desktop client found")

// ExitError is the client ending with an error: its exit code, and the
// end of what it wrote.
type ExitError struct {
	Code   int
	Output string
}

func (e *ExitError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("the Remote Desktop client exited with code %d", e.Code)
	}
	return fmt.Sprintf("the Remote Desktop client exited with code %d: %s", e.Code, e.Output)
}

// hostPort is host:port, the host in brackets when it is an IPv6
// address.
func hostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

// gatewayName is the gateway's host name, without its port: the
// credential's target.
func gatewayName(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "[") {
		if i := strings.Index(addr, "]"); i > 0 {
			return addr[1:i]
		}
	}
	if host, _, ok := strings.Cut(addr, ":"); ok && strings.Count(addr, ":") == 1 {
		return host
	}
	return addr
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clean keeps s on one line of a .rdp file.
func clean(s string) string {
	return strings.NewReplacer("\r", "", "\n", " ").Replace(strings.TrimSpace(s))
}

// File is the text of the .rdp file that opens h: every option of h's,
// mstsc's defaults for those it leaves unset, then its own lines
// (RDPConfig.Custom) verbatim, which clients take over what comes
// before. It has no password: mstsc reads it from the Credential
// Manager (Start writes it there), and asks for it otherwise.
func File(h *config.Host) string {
	r := config.RDPOf(h)
	var b strings.Builder
	s := func(name, v string) { fmt.Fprintf(&b, "%s:s:%s\r\n", name, clean(v)) }
	i := func(name string, v int) { fmt.Fprintf(&b, "%s:i:%d\r\n", name, v) }

	s("full address", hostPort(strings.TrimSpace(h.Addr), h.PortOr22()))
	if login := config.RDPLogin(h); login != "" {
		s("username", login)
	}
	i("prompt for credentials", 0)
	i("administrative session", boolInt(r.Admin))

	// Display.
	i("screen mode id", map[bool]int{true: 1, false: 2}[r.Windowed])
	if r.Width > 0 && r.Height > 0 {
		i("desktopwidth", r.Width)
		i("desktopheight", r.Height)
	}
	i("use multimon", boolInt(r.AllMonitors))
	if r.AllMonitors && strings.TrimSpace(r.Monitors) != "" {
		s("selectedmonitors", strings.ReplaceAll(r.Monitors, " ", ""))
	}
	i("session bpp", r.ColorDepthOrDefault())
	i("smart sizing", boolInt(r.SmartSizing))
	i("dynamic resolution", boolInt(!r.NoDynamicResolution))
	i("displayconnectionbar", boolInt(!r.NoConnectionBar))

	// Local resources.
	i("redirectclipboard", boolInt(!r.NoClipboard))
	switch d := config.NormalizeDrives(r.Drives); d {
	case "":
	case "*":
		s("drivestoredirect", "*")
	default:
		s("drivestoredirect", d)
	}
	i("redirectprinters", boolInt(!r.NoPrinters))
	i("redirectsmartcards", boolInt(!r.NoSmartCards))
	i("redirectcomports", boolInt(r.Ports))
	i("audiocapturemode", boolInt(r.Microphone))
	switch r.Audio {
	case config.AudioRemote:
		i("audiomode", 1)
	case config.AudioOff:
		i("audiomode", 2)
	default:
		i("audiomode", 0)
	}
	i("redirectwebauthn", boolInt(!r.NoWebAuthn))
	if r.Cameras {
		s("camerastoredirect", "*")
	}
	switch r.Keyboard {
	case config.KeyboardLocal:
		i("keyboardhook", 0)
	case config.KeyboardRemote:
		i("keyboardhook", 1)
	default:
		i("keyboardhook", 2)
	}

	// The program to start.
	if p := strings.TrimSpace(r.Program); p != "" {
		s("alternate shell", p)
		if f := strings.TrimSpace(r.ProgramFolder); f != "" {
			s("shell working directory", f)
		}
	}

	// Experience.
	if n := networkType(r.Network); n == 7 {
		i("connection type", 7)
		i("networkautodetect", 1)
		i("bandwidthautodetect", 1)
	} else {
		i("connection type", n)
		i("networkautodetect", 0)
	}
	i("bitmapcachepersistenable", boolInt(!r.NoBitmapCache))
	i("autoreconnection enabled", boolInt(!r.NoAutoReconnect))

	// Connection.
	i("authentication level", serverAuthLevel(r.ServerAuth))
	i("enablecredsspsupport", boolInt(!r.NoNLA))
	if r.UsesGateway() {
		s("gatewayhostname", r.GatewayHost)
		i("gatewayusagemethod", gatewayUsage(r.GatewayUsage))
		i("gatewayprofileusagemethod", 1)
		i("gatewaycredentialssource", gatewayAuth(r.GatewayAuth))
		i("promptcredentialonce", boolInt(r.GatewaySameLogin))
		if u := strings.TrimSpace(r.GatewayUser); u != "" {
			s("gatewayusername", u)
		}
	} else {
		i("gatewayusagemethod", 0)
	}

	for _, line := range strings.Split(r.Custom, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			b.WriteString(line + "\r\n")
		}
	}
	return b.String()
}

// networkType is mstsc's "connection type" of n.
func networkType(n config.RDPNetwork) int {
	switch n {
	case config.NetworkModem:
		return 1
	case config.NetworkLowBroadband:
		return 2
	case config.NetworkSatellite:
		return 3
	case config.NetworkHighBroadband:
		return 4
	case config.NetworkWAN:
		return 5
	case config.NetworkLAN:
		return 6
	}
	return 7
}

// serverAuthLevel is mstsc's "authentication level" of a.
func serverAuthLevel(a config.RDPServerAuth) int {
	switch a {
	case config.ServerAuthConnect:
		return 0
	case config.ServerAuthRefuse:
		return 1
	case config.ServerAuthNone:
		return 3
	}
	return 2
}

// gatewayUsage is mstsc's "gatewayusagemethod" of u.
func gatewayUsage(u config.RDPGatewayUsage) int {
	switch u {
	case config.GatewayAlways:
		return 1
	case config.GatewayFallback:
		return 2
	case config.GatewayDetect:
		return 3
	}
	return 0
}

// gatewayAuth is mstsc's "gatewaycredentialssource" of a.
func gatewayAuth(a config.RDPGatewayAuth) int {
	switch a {
	case config.GatewayAuthPassword:
		return 0
	case config.GatewayAuthSmartCard:
		return 1
	}
	return 4
}

// fileName is the name of h's .rdp file, which the clients show as the
// connection's name: its title, without what file names cannot have.
func fileName(h *config.Host) string {
	name := strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(h.Title()))
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "remote-desktop"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return name + ".rdp"
}

// writeFile writes h's .rdp file into a new folder only the user can
// read, and returns its path and the folder, which the caller removes
// once the client is done with it.
func writeFile(h *config.Host) (path, dir string, err error) {
	dir, err = os.MkdirTemp("", "finallyshell-rdp-")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	path = filepath.Join(dir, fileName(h))
	if err := os.WriteFile(path, []byte(File(h)), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", "", err
	}
	return path, dir, nil
}

// tail keeps the last n bytes written to it, for what a client wrote
// before it failed.
type tail struct {
	n   int
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
	return len(p), nil
}

func (t *tail) String() string { return strings.TrimSpace(strings.ToValidUTF8(string(t.buf), "")) }
