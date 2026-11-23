package rdp

import (
	"regexp"
	"strconv"
	"strings"

	"finallyshell/internal/config"
)

// FreeRDP's clients, in the order Start looks for them: FreeRDP 3's,
// then FreeRDP 2's (on some systems, xfreerdp is FreeRDP 3).
var freeRDPClients = []string{"xfreerdp3", "xfreerdp", "wlfreerdp3", "wlfreerdp", "sdl-freerdp3", "sdl-freerdp"}

var versionRE = regexp.MustCompile(`(?i)FreeRDP version (\d+)\.`)

// freeRDPVersion is the major version of the FreeRDP client named name,
// from what its --version printed: a name ending in 3 is FreeRDP 3, and
// one that says nothing is taken for FreeRDP 2.
func freeRDPVersion(name, out string) int {
	if strings.HasSuffix(name, "3") {
		return 3
	}
	if m := versionRE.FindStringSubmatch(out); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			return v
		}
	}
	return 2
}

// splitLogin splits DOMAIN\user into its user and domain; a user
// without a domain is returned as it is, with domain.
func splitLogin(user, domain string) (string, string) {
	if d, u, ok := strings.Cut(user, `\`); ok {
		return u, d
	}
	return user, domain
}

// FreeRDPArgs are the arguments of a FreeRDP client of version (2 or 3)
// that open h as File's .rdp file does, as near as FreeRDP has the
// options. With stdinPassword, the password is read from the standard
// input (/from-stdin:force), not put on the command line; the gateway's
// password, which FreeRDP cannot read there, is an argument, which
// FreeRDP overwrites in its arguments once it has read them. Drive
// letters, which Linux has none of, share the home folder; options
// FreeRDP does not have (cameras, WebAuthn, the .rdp lines of Custom)
// are left out.
func FreeRDPArgs(h *config.Host, version int, stdinPassword bool, gatewayPassword string) []string {
	r := config.RDPOf(h)
	v3 := version >= 3
	args := []string{"/v:" + hostPort(strings.TrimSpace(h.Addr), h.PortOr22())}
	if user, domain := splitLogin(strings.TrimSpace(h.User), strings.TrimSpace(r.Domain)); user != "" {
		args = append(args, "/u:"+user)
		if domain != "" {
			args = append(args, "/d:"+domain)
		}
		if stdinPassword {
			args = append(args, "/from-stdin:force")
		}
	}
	args = append(args, "/t:"+h.Title())

	// Display.
	if r.Windowed {
		if r.Width > 0 && r.Height > 0 {
			args = append(args, "/size:"+strconv.Itoa(r.Width)+"x"+strconv.Itoa(r.Height))
		}
	} else {
		args = append(args, "/f")
	}
	if r.AllMonitors {
		args = append(args, "/multimon")
		if m := strings.ReplaceAll(strings.TrimSpace(r.Monitors), " ", ""); m != "" {
			args = append(args, "/monitors:"+m)
		}
	}
	args = append(args, "/bpp:"+strconv.Itoa(r.ColorDepthOrDefault()))
	// FreeRDP refuses both at once.
	if r.SmartSizing {
		args = append(args, "/smart-sizing")
	} else if !r.NoDynamicResolution {
		args = append(args, "/dynamic-resolution")
	}

	// Local resources.
	if r.NoClipboard {
		args = append(args, "-clipboard")
	} else {
		args = append(args, "+clipboard")
	}
	switch d := config.NormalizeDrives(r.Drives); d {
	case "":
	case "*":
		args = append(args, "+drives")
	default:
		args = append(args, "+home-drive")
	}
	if !r.NoPrinters {
		args = append(args, "/printer")
	}
	if !r.NoSmartCards {
		args = append(args, "/smartcard")
	}
	if r.Ports {
		args = append(args, "/serial")
	}
	if r.Microphone {
		args = append(args, "/microphone")
	}
	switch r.Audio {
	case config.AudioRemote:
		args = append(args, "/audio-mode:1")
	case config.AudioOff:
		args = append(args, "/audio-mode:2")
	default:
		args = append(args, "/sound")
	}
	if r.Keyboard == config.KeyboardLocal {
		args = append(args, "-grab-keyboard")
	}
	if p := strings.TrimSpace(r.Program); p != "" {
		args = append(args, "/shell:"+p)
		if f := strings.TrimSpace(r.ProgramFolder); f != "" {
			args = append(args, "/shell-dir:"+f)
		}
	}

	// Experience.
	args = append(args, "/network:"+freeRDPNetwork(r.Network))
	if !r.NoAutoReconnect {
		args = append(args, "+auto-reconnect")
	}

	// Connection.
	if r.Admin {
		args = append(args, "/admin")
	}
	// No terminal to ask in: a certificate never seen is trusted, and
	// one that changed refused, unless the host connects regardless.
	switch {
	case r.ServerAuth == config.ServerAuthConnect && v3:
		args = append(args, "/cert:ignore")
	case r.ServerAuth == config.ServerAuthConnect:
		args = append(args, "/cert-ignore")
	case v3:
		args = append(args, "/cert:tofu")
	default:
		args = append(args, "/cert-tofu")
	}
	if r.NoNLA {
		args = append(args, "-sec-nla")
	}
	if r.UsesGateway() {
		args = append(args, gatewayArgs(r, v3, gatewayPassword)...)
	}
	return args
}

// gatewayArgs are FreeRDP's arguments for r's gateway.
func gatewayArgs(r *config.RDPConfig, v3 bool, password string) []string {
	host := strings.TrimSpace(r.GatewayHost)
	var user, domain string
	if !r.GatewaySameLogin {
		user, domain = splitLogin(strings.TrimSpace(r.GatewayUser), "")
	}
	usage := ""
	switch r.GatewayUsage {
	case config.GatewayAlways:
		usage = "direct"
	case config.GatewayFallback, config.GatewayDetect:
		usage = "detect"
	}
	if v3 {
		parts := []string{"g:" + host}
		if user != "" {
			parts = append(parts, "u:"+user)
			if domain != "" {
				parts = append(parts, "d:"+domain)
			}
			if password != "" {
				parts = append(parts, "p:"+password)
			}
		}
		if usage != "" {
			parts = append(parts, "usage-method:"+usage)
		}
		return []string{"/gateway:" + strings.Join(parts, ",")}
	}
	args := []string{"/g:" + host}
	if user != "" {
		args = append(args, "/gu:"+user)
		if domain != "" {
			args = append(args, "/gd:"+domain)
		}
		if password != "" {
			args = append(args, "/gp:"+password)
		}
	}
	if usage != "" {
		args = append(args, "/gateway-usage-method:"+usage)
	}
	return args
}

// freeRDPNetwork is FreeRDP's /network: value of n.
func freeRDPNetwork(n config.RDPNetwork) string {
	switch n {
	case config.NetworkModem:
		return "modem"
	case config.NetworkLowBroadband:
		return "broadband-low"
	case config.NetworkSatellite:
		return "broadband"
	case config.NetworkHighBroadband:
		return "broadband-high"
	case config.NetworkWAN:
		return "wan"
	case config.NetworkLAN:
		return "lan"
	}
	return "auto"
}
