package monitor

import (
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// OpenWrt and its derivatives (ImmortalWrt, iStoreOS, …) run procd,
// ubus, uci and BusyBox rather than systemd and GNU tools. The OpenWrt
// pages (internal/app/openwrt*.go) read and change them with the scripts
// here, each a round trip in POSIX sh: they run on every release from
// 21.02 (fw3, iptables, opkg) to 25.12 (fw4, nftables, apk).

// openWrtMarks starts the OpenWrt scripts: m prints the mark of a
// section, which procSections splits by.
const openWrtMarks = "export LC_ALL=C\nm() { echo \"" + procMark + "$1---\"; }\n"

// ErrNotOpenWrt is a server without /etc/openwrt_release.
var ErrNotOpenWrt = errors.New("not an OpenWrt system")

// OpenWrtInfo is what OpenWrtProbeScript tells of a system.
type OpenWrtInfo struct {
	// Distribution is DISTRIB_ID: OpenWrt, ImmortalWrt, iStoreOS, …
	Distribution string
	Version      string // DISTRIB_RELEASE, as 24.10.4 or SNAPSHOT
	Revision     string // DISTRIB_REVISION
	Target       string // DISTRIB_TARGET, as x86/64 or ramips/mt7621
	Description  string // DISTRIB_DESCRIPTION
	Model        string // the board's model, "" when unknown
	Hostname     string
	Kernel       string
	// PackageManager is "opkg", "apk" (25.12 and later) or "".
	PackageManager string
	// Firewall is "fw4" (nftables, 22.03 and later), "fw3" or "".
	Firewall string
	// SFTP is the path of an SFTP server, "" for none: OpenWrt has none
	// until openssh-sftp-server is installed.
	SFTP string
	// Uptime in seconds, the loads, and the memory and filesystems in
	// bytes, from ubus; 0 when ubus did not answer.
	Uptime                 int64
	Load                   [3]float64
	MemTotal, MemAvailable uint64
	RootTotal, RootFree    uint64 // the root filesystem: the overlay on a router
	TmpTotal, TmpFree      uint64
}

// OpenWrtProbeScript reads OpenWrtInfo: the release file, ubus's board
// and info, the tools there are and the SFTP server.
const OpenWrtProbeScript = openWrtMarks + `m release; cat /etc/openwrt_release 2>/dev/null
m board; ubus call system board 2>/dev/null
m info; ubus call system info 2>/dev/null
m model; cat /tmp/sysinfo/model 2>/dev/null
m host; cat /proc/sys/kernel/hostname 2>/dev/null; uname -r 2>/dev/null
m tools; for t in opkg apk fw4 fw3; do command -v $t >/dev/null 2>&1 && echo $t; done
m sftp; for f in /usr/libexec/sftp-server /usr/lib/sftp-server /usr/lib/openssh/sftp-server /usr/libexec/openssh/sftp-server; do [ -x "$f" ] && echo "$f"; done
m df; df -k / /tmp 2>/dev/null
m end
`

// ParseOpenWrtProbe reads the output of OpenWrtProbeScript:
// ErrNotOpenWrt for another system.
func ParseOpenWrtProbe(out string) (OpenWrtInfo, error) {
	sec := procSections(out)
	release := parseShellVars(sec["release"])
	info := OpenWrtInfo{
		Distribution: release["DISTRIB_ID"],
		Version:      release["DISTRIB_RELEASE"],
		Revision:     release["DISTRIB_REVISION"],
		Target:       release["DISTRIB_TARGET"],
		Description:  release["DISTRIB_DESCRIPTION"],
	}
	if info.Distribution == "" && info.Description == "" {
		return OpenWrtInfo{}, ErrNotOpenWrt
	}
	if info.Distribution == "" {
		info.Distribution = "OpenWrt"
	}
	var board struct {
		Kernel, Hostname, Model string
	}
	if json.Unmarshal([]byte(sec["board"]), &board) == nil {
		info.Kernel, info.Hostname, info.Model = board.Kernel, board.Hostname, board.Model
	}
	if info.Model == "" {
		info.Model = strings.TrimSpace(sec["model"])
	}
	if host := strings.Fields(sec["host"]); len(host) == 2 {
		if info.Hostname == "" {
			info.Hostname = host[0]
		}
		if info.Kernel == "" {
			info.Kernel = host[1]
		}
	}
	var sys struct {
		Uptime int64
		Load   []float64
		Memory struct{ Total, Available, Free, Buffered, Cached uint64 }
		Root   struct{ Total, Avail uint64 }
		Tmp    struct{ Total, Avail uint64 }
	}
	if json.Unmarshal([]byte(sec["info"]), &sys) == nil {
		info.Uptime = sys.Uptime
		// ubus gives the loads times 65536, as the kernel keeps them.
		for i := 0; i < len(sys.Load) && i < 3; i++ {
			info.Load[i] = sys.Load[i] / 65536
		}
		info.MemTotal, info.MemAvailable = sys.Memory.Total, sys.Memory.Available
		if info.MemAvailable == 0 { // before 21.02 ubus had no available
			info.MemAvailable = sys.Memory.Free + sys.Memory.Buffered + sys.Memory.Cached
		}
		// ubus counts the filesystems in KiB.
		info.RootTotal, info.RootFree = sys.Root.Total*1024, sys.Root.Avail*1024
		info.TmpTotal, info.TmpFree = sys.Tmp.Total*1024, sys.Tmp.Avail*1024
	}
	tools := strings.Fields(sec["tools"])
	for _, pm := range []string{"opkg", "apk"} {
		if slices.Contains(tools, pm) {
			info.PackageManager = pm
			break
		}
	}
	for _, fw := range []string{"fw4", "fw3"} {
		if slices.Contains(tools, fw) {
			info.Firewall = fw
			break
		}
	}
	if paths := strings.Fields(sec["sftp"]); len(paths) > 0 {
		info.SFTP = paths[0]
	}
	// 21.02's ubus has no root nor tmp: df has them, in KiB.
	if info.RootTotal == 0 {
		for line := range strings.SplitSeq(sec["df"], "\n") {
			f := strings.Fields(line)
			if len(f) < 6 {
				continue
			}
			total, err1 := strconv.ParseUint(f[1], 10, 64)
			avail, err2 := strconv.ParseUint(f[3], 10, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			switch f[len(f)-1] {
			case "/":
				info.RootTotal, info.RootFree = total*1024, avail*1024
			case "/tmp":
				info.TmpTotal, info.TmpFree = total*1024, avail*1024
			}
		}
	}
	return info, nil
}

// parseShellVars reads NAME='value' lines, as /etc/openwrt_release has
// them: single or double quoted, or bare.
func parseShellVars(s string) map[string]string {
	vars := map[string]string{}
	for line := range strings.SplitSeq(s, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || name == "" {
			continue
		}
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		vars[name] = value
	}
	return vars
}

// UCISection is a section of a uci config, as uci -X show prints it.
type UCISection struct {
	Name, Type string // Name is cfgXXXXXX for an anonymous section
	Options    map[string][]string
}

// Get is the option's value, its values joined by spaces for a list.
func (s UCISection) Get(option string) string { return strings.Join(s.Options[option], " ") }

// parseUCI reads uci -X show: config.section=type, then
// config.section.option='value' or a list 'a' 'b', in order.
func parseUCI(s string) []UCISection {
	var sections []UCISection
	index := map[string]int{}
	for line := range strings.SplitSeq(s, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		parts := strings.SplitN(key, ".", 3)
		switch len(parts) {
		case 2:
			index[parts[1]] = len(sections)
			sections = append(sections, UCISection{Name: parts[1], Type: value, Options: map[string][]string{}})
		case 3:
			if i, ok := index[parts[1]]; ok {
				sections[i].Options[parts[2]] = parseUCIValues(value)
			}
		}
	}
	return sections
}

// parseUCIValues reads 'a' 'b' 'it'\”s' into its values.
func parseUCIValues(s string) []string {
	var values []string
	var cur strings.Builder
	in, started := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			in, started = !in, true
		case c == '\\' && !in && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
		case c == ' ' && !in:
			if started {
				values = append(values, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		values = append(values, cur.String())
	}
	return values
}

// Validation of what goes into the commands: each is quoted too.
var (
	openWrtNameRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	openWrtSectionRE = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	openWrtPackageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._-]{0,99}$`)
	openWrtMACRE     = regexp.MustCompile(`^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$`)
	openWrtHostRE    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	openWrtPortRE    = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?$`)
)

// ErrOpenWrtInvalid is a value a command does not take.
var ErrOpenWrtInvalid = errors.New("invalid value")

// ValidOpenWrtPorts tells whether s is a port or a range of ports, as
// 8080 or 8000-8100.
func ValidOpenWrtPorts(s string) bool {
	if !openWrtPortRE.MatchString(s) {
		return false
	}
	lo, hi, isRange := strings.Cut(s, "-")
	a, _ := strconv.Atoi(lo)
	b := a
	if isRange {
		b, _ = strconv.Atoi(hi)
	}
	return a >= 1 && b <= 65535 && a <= b
}

// ValidOpenWrtIPv4 tells whether s is an IPv4 address.
func ValidOpenWrtIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
}

// ValidOpenWrtMAC tells whether s is a MAC address, as 00:11:22:aa:bb:cc.
func ValidOpenWrtMAC(s string) bool { return openWrtMACRE.MatchString(s) }

// ValidOpenWrtHostname tells whether s is a host name dnsmasq takes.
func ValidOpenWrtHostname(s string) bool { return openWrtHostRE.MatchString(s) }

// ValidOpenWrtPackage tells whether s is a package name.
func ValidOpenWrtPackage(s string) bool { return openWrtPackageRE.MatchString(s) }

// openWrtCommit commits a uci config and has its service read it again:
// the service is that of the config, as dnsmasq for dhcp.
func openWrtCommit(config, service string) string {
	return "uci commit " + config + " && /etc/init.d/" + service + " reload"
}
