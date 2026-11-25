package monitor

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

const sysConfMarks = `export LC_ALL='C'
mark() { 'printf' '%s\n' '---FINALLYSHELL:'"$1"'---'; }
`

const sysConfZoneRead = `
zone=$('timedatectl' 'show' '-p' 'Timezone' '--value' 2>/dev/null) || zone=''
case "$zone" in
  '') target=$('readlink' '-f' '/etc/localtime' 2>/dev/null) || target=''
      case "$target" in '/usr/share/zoneinfo/'*) zone=${target#/usr/share/zoneinfo/};; esac;;
esac
[ -n "$zone" ] || zone=$('head' '-n' '1' '/etc/timezone' 2>/dev/null) || zone=''
[ -n "$zone" ] || zone=$('date' '+%Z' 2>/dev/null) || zone=''
'printf' '%s\n' "$zone"
`

const SysConfInfoCommand = sysConfMarks + `mark 'os-release'
'cat' '/etc/os-release' 2>/dev/null || 'cat' '/usr/lib/os-release' 2>/dev/null
mark 'virtualization'
'systemd-detect-virt' 2>/dev/null
mark 'zone'
` + sysConfZoneRead + `mark 'addr'
'ip' '-o' 'addr' 'show' 2>/dev/null
mark 'mounts'
'df' '-kP' '-x' 'tmpfs' '-x' 'devtmpfs' '-x' 'squashfs' 2>/dev/null || 'df' '-kP' 2>/dev/null
mark 'end'
`

const SysConfPublicIPCommand = "'curl' '--fail' '--silent' '--show-error' '--max-time' '10' 'https://api.ipify.org'\n"

type SysConfInfo struct {
	Distro, Virtualization, Zone string
	Addrs                        []Addr
	Mounts                       []SysConfMount
}

type SysConfMount struct {
	Disk
	Device string
}

func ParseSysConfInfo(output string) SysConfInfo {
	sections := sysInfoSections(output)
	info := SysConfInfo{Virtualization: firstLine(sections["virtualization"]), Zone: firstLine(sections["zone"])}
	for _, line := range lines(sections["os-release"]) {
		if value, ok := strings.CutPrefix(line, "ID="); ok {
			info.Distro = strings.ToLower(strings.Trim(value, `"'`))
			break
		}
	}
	for _, line := range lines(sections["addr"]) {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[2] != "inet" && fields[2] != "inet6" {
			continue
		}
		addr, err := netip.ParsePrefix(fields[3])
		if err == nil && !addr.Addr().IsLoopback() {
			iface, _, _ := strings.Cut(fields[1], "@")
			info.Addrs = append(info.Addrs, Addr{Iface: iface, Addr: addr.String()})
		}
	}
	for _, line := range lines(sections["mounts"]) {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if mounts := parseDisks(line); len(mounts) == 1 {
			info.Mounts = append(info.Mounts, SysConfMount{Disk: mounts[0], Device: fields[0]})
		}
	}
	return info
}

func ValidSysConfOutput(output string) bool {
	return strings.Contains(output, sysInfoMark+"end---")
}

type SysConfChange struct {
	Target string
	Script string
}

type SysConfLogin struct {
	User, Terminal, From, When string
	Failed                     bool
}

const SysConfLastCommand = sysConfMarks + `'last' '-n' '50' '-F' '-w' 2>/dev/null || 'last' '-n' '50'`
const SysConfLastbCommand = sysConfMarks + `'lastb' '-n' '50' '-F' '-w' 2>/dev/null || 'lastb' '-n' '50'`

func ParseSysConfLogins(output string, failed bool) []SysConfLogin {
	var result []SysConfLogin
	for _, line := range lines(output) {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "reboot" && fields[1] == "system" || fields[0] == "shutdown" && fields[1] == "system" || fields[0] == "wtmp" || fields[0] == "btmp" {
			continue
		}
		for index := 2; index < min(4, len(fields)); index++ {
			if !strings.Contains(" Mon Tue Wed Thu Fri Sat Sun ", " "+fields[index]+" ") {
				continue
			}
			result = append(result, SysConfLogin{User: fields[0], Terminal: fields[1], From: strings.Join(fields[2:index], " "), When: strings.Join(fields[index:], " "), Failed: failed})
			break
		}
	}
	return result
}

var (
	ErrSysConfDNSAddress = errors.New("invalid DNS address")
	ErrSysConfDNSDomain  = errors.New("invalid DNS search domain")
	ErrSysConfDNSMode    = errors.New("DNS manager is not writable")
	ErrSysConfDNSLimit   = errors.New("too many resolv.conf nameservers")
	ErrSysConfZone       = errors.New("invalid time zone")
)

func sysConfKeyValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range lines(output) {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	return values
}

func sysConfDigits(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}
