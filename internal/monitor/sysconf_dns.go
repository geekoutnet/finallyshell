package monitor

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type SysConfDNSMode string

const (
	SysConfDNSPlain          SysConfDNSMode = "plain"
	SysConfDNSResolved       SysConfDNSMode = "resolved"
	SysConfDNSNetworkManager SysConfDNSMode = "networkmanager"
	SysConfDNSManaged        SysConfDNSMode = "managed"
	SysConfDNSUnknown        SysConfDNSMode = "unknown"
)

const (
	SysConfResolvPath   = "/etc/resolv.conf"
	SysConfResolvBackup = "/etc/resolv.conf.finallyshell.bak"
	SysConfResolvedPath = "/etc/systemd/resolved.conf.d/finallyshell.conf"
	SysConfResolvedSave = SysConfResolvedPath + ".bak"
	SysConfDNSChanged   = "FINALLYSHELL_DNS_CHANGED"
	sysConfNMBackupDir  = "/etc/NetworkManager"
	SysConfDNSProbe     = "'getent' 'hosts' 'example.com'\n"
)

type DNSValues struct {
	Servers, Search, Options []string
}

type SysConfDNSLink struct {
	Scope, Current string
	DNSValues
}

type SysConfDNS struct {
	Mode           SysConfDNSMode
	File, Edit     DNSValues
	Links          []SysConfDNSLink
	Routing        []string
	Symlink        string
	Device, UUID   string
	NetworkManager bool
	Backup         bool
}

const sysConfDNSModeScript = `
mode='unknown'
uuid=''
device=''
nm='no'
if command -v 'nmcli' >/dev/null 2>&1; then
  nm='yes'
  for family in '-4' '-6'; do
    device=$('ip' "$family" 'route' 'show' 'default' 2>/dev/null | 'awk' '{ for (i=1; i<NF; i++) if ($i=="dev") { print $(i+1); exit } }')
    [ -n "$device" ] || continue
    uuid=$('nmcli' '-t' '-f' 'UUID,DEVICE' 'connection' 'show' '--active' 2>/dev/null | 'awk' '-F:' -v "device=$device" '$2==device { print $1; exit }')
    [ -z "$uuid" ] || break
  done
fi
link=''
if [ -L '/etc/resolv.conf' ]; then
  link=$('readlink' '-f' '/etc/resolv.conf' 2>/dev/null) || link=''
fi
if 'systemctl' 'is-active' '--quiet' 'systemd-resolved.service' 2>/dev/null; then
  mode='resolved'
elif [ -L '/etc/resolv.conf' ] && { [ "$link" != '/run/NetworkManager/resolv.conf' ] || [ -z "$uuid" ]; }; then
  mode='managed'
elif [ -n "$uuid" ]; then
  mode='networkmanager'
elif [ -L '/etc/resolv.conf' ] || 'grep' '-Eiq' '^[[:space:]]*[#;].*(generated|managed).*(resolvconf|networkmanager|netconfig|dhcpcd|systemd-resolved)|^[[:space:]]*[#;].*(resolvconf|networkmanager|netconfig|dhcpcd|systemd-resolved).*(generated|managed)' '/etc/resolv.conf' 2>/dev/null; then
  mode='managed'
elif [ -f '/etc/resolv.conf' ]; then
  mode='plain'
fi
`

const SysConfDNSCommand = sysConfMarks + sysConfDNSModeScript + `mark 'manager'
'printf' 'MODE=%s\nNM=%s\nUUID=%s\nDEVICE=%s\n' "$mode" "$nm" "$uuid" "$device"
if [ -L '/etc/resolv.conf' ]; then
  'printf' 'LINK=%s\n' "$('readlink' '/etc/resolv.conf')"
fi
case "$mode" in
  resolved) backup='/etc/systemd/resolved.conf.d/finallyshell.conf.bak';;
  networkmanager) backup="/etc/NetworkManager/finallyshell-dns-$uuid.bak";;
  plain) backup='/etc/resolv.conf.finallyshell.bak';;
  *) backup='';;
esac
if [ -n "$backup" ] && { [ -f "$backup" ] || { [ "$mode" = 'resolved' ] && [ -f "$backup.absent" ]; }; }; then
  'printf' 'BACKUP=yes\n'
fi
mark 'file'
'cat' '/etc/resolv.conf' 2>/dev/null
mark 'resolved'
if [ "$mode" = 'resolved' ]; then
  'resolvectl' 'status' '--no-pager' 2>/dev/null || 'systemd-resolve' '--status' 2>/dev/null
fi
mark 'nm-device'
if [ -n "$uuid" ]; then
  'nmcli' '-t' '-f' 'IP4.DNS,IP6.DNS,IP4.DOMAIN,IP6.DOMAIN' 'device' 'show' "$device" 2>/dev/null
fi
mark 'end'
`

func ParseSysConfDNS(output string) SysConfDNS {
	sections := sysInfoSections(output)
	manager := sysConfKeyValues(sections["manager"])
	dns := SysConfDNS{Mode: SysConfDNSMode(manager["MODE"]), Device: manager["DEVICE"], UUID: manager["UUID"],
		Symlink: manager["LINK"], NetworkManager: manager["NM"] == "yes", Backup: manager["BACKUP"] == "yes"}
	dns.File = ParseResolvConf(sections["file"])
	dns.Edit = dns.File
	dns.Links = ParseResolvedStatus(sections["resolved"])
	if dns.Mode == SysConfDNSNetworkManager {
		link := SysConfDNSLink{Scope: dns.Device}
		for _, line := range lines(sections["nm-device"]) {
			key, value, ok := strings.Cut(line, ":")
			if !ok || value == "" {
				continue
			}
			value = strings.ReplaceAll(value, `\:`, `:`)
			switch {
			case strings.HasPrefix(key, "IP4.DNS["), strings.HasPrefix(key, "IP6.DNS["):
				link.Servers = append(link.Servers, value)
			case strings.HasPrefix(key, "IP4.DOMAIN["), strings.HasPrefix(key, "IP6.DOMAIN["):
				link.Search = append(link.Search, value)
			}
		}
		if len(link.Servers) > 0 {
			dns.Links, dns.Edit = append(dns.Links, link), link.DNSValues
		}
	}
	if dns.Mode == SysConfDNSResolved {
		dns.Edit = DNSValues{}
		for _, link := range dns.Links {
			if link.Scope == "" {
				dns.Edit = link.DNSValues
				for _, domain := range link.Search {
					if strings.HasPrefix(domain, "~") && validSysConfRouting(domain) {
						dns.Routing = append(dns.Routing, domain)
					}
				}
				break
			}
		}
	}
	dns.Edit.Search = slices.DeleteFunc(slices.Clone(dns.Edit.Search), func(domain string) bool { return strings.HasPrefix(domain, "~") })
	return dns
}

func ParseResolvConf(output string) DNSValues {
	var values DNSValues
	for _, line := range lines(output) {
		if index := strings.IndexAny(line, "#;"); index >= 0 {
			line = line[:index]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			values.Servers = append(values.Servers, fields[1])
		case "domain", "search":
			values.Search = fields[1:]
		case "options":
			values.Options = append(values.Options, fields[1:]...)
		}
	}
	return values
}

func ParseResolvedStatus(output string) []SysConfDNSLink {
	var links []SysConfDNSLink
	var current *SysConfDNSLink
	var continuation *[]string
	var valueColumn int
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "Global":
			links = append(links, SysConfDNSLink{})
			current, continuation = &links[len(links)-1], nil
		case strings.HasPrefix(line, "Link "):
			_, scope, _ := strings.Cut(line, "(")
			links = append(links, SysConfDNSLink{Scope: strings.TrimSuffix(scope, ")")})
			current, continuation = &links[len(links)-1], nil
		case current != nil:
			key, value, _ := strings.Cut(line, ":")
			switch key {
			case "DNS Servers":
				continuation = &current.Servers
			case "DNS Domain", "DNS Domains":
				continuation = &current.Search
			case "Current DNS Server":
				current.Current, continuation = strings.TrimSpace(value), nil
			default:
				if continuation != nil && line != "" && len(raw)-len(strings.TrimLeft(raw, " \t")) >= valueColumn {
					*continuation = append(*continuation, strings.Fields(line)...)
					continue
				}
				continuation = nil
			}
			if continuation != nil {
				valueColumn = strings.IndexByte(raw, ':') + 1 + len(value) - len(strings.TrimLeft(value, " \t"))
				*continuation = append(*continuation, strings.Fields(value)...)
			}
		}
	}
	return links
}

func validSysConfRouting(domain string) bool {
	if domain == "~." {
		return true
	}
	name, tagged := strings.CutPrefix(domain, "~")
	values, err := ValidateSysConfDNS("1.1.1.1", name)
	return tagged && err == nil && len(values.Search) == 1 && values.Search[0] == strings.ToLower(name)
}

const sysConfFileGuard = `
guard_file() {
  if [ -L "$1" ] || { [ -e "$1" ] && [ ! -f "$1" ]; }; then
    'printf' '%s\n' "$1" >&2
    exit 1
  fi
}
`

const sysConfNewTemp = `guard_file "$file"
tmp=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
cleanup() { 'rm' '-f' '--' "$tmp"; }
trap 'cleanup' '0'
`

const sysConfBackup = `guard_file "$backup"
if [ ! -f "$backup" ]; then
  'cp' '-p' '--' "$file" "$tmp"
  'ln' '--' "$tmp" "$backup"
  'rm' '-f' '--' "$tmp"
  tmp=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
fi
`

func BuildSysConfDNS(dns SysConfDNS, values DNSValues, restore bool) (SysConfChange, error) {
	if !restore {
		validated, err := ValidateSysConfDNS(strings.Join(values.Servers, " "), strings.Join(values.Search, " "))
		if err != nil {
			return SysConfChange{}, err
		}
		values = validated
	}
	if restore && !dns.Backup {
		return SysConfChange{}, ErrSysConfDNSMode
	}
	var change SysConfChange
	switch dns.Mode {
	case SysConfDNSPlain:
		if !restore && len(values.Servers) > 3 {
			return SysConfChange{}, ErrSysConfDNSLimit
		}
		change = sysConfPlainChange(values, restore)
	case SysConfDNSResolved:
		change = sysConfResolvedChange(values, dns.Routing, restore)
	case SysConfDNSNetworkManager:
		if !validSysConfUUID(dns.UUID) {
			return SysConfChange{}, ErrSysConfDNSMode
		}
		change = sysConfNMChange(dns.UUID, values, restore)
	default:
		return SysConfChange{}, ErrSysConfDNSMode
	}
	guard := "set -eu\nexport LC_ALL='C'\n" + sysConfDNSModeScript + "if [ \"$mode\" != " + privilegeQuote(string(dns.Mode))
	if dns.Mode == SysConfDNSNetworkManager {
		guard += " ] || [ \"$uuid\" != " + privilegeQuote(dns.UUID)
	}
	guard += " ]; then " + SystemCommand("printf", "%s\\n", SysConfDNSChanged) + " >&2; exit 1; fi\n"
	change.Script = guard + change.Script
	return change, nil
}

func sysConfPlainChange(values DNSValues, restore bool) SysConfChange {
	prefix := sysConfFileGuard + "file=" + privilegeQuote(SysConfResolvPath) + "\nbackup=" + privilegeQuote(SysConfResolvBackup) + "\n" + sysConfNewTemp
	replace := "'mv' '-fT' -- \"$tmp\" \"$file\"\n"
	if restore {
		return SysConfChange{Target: SysConfResolvBackup + " → " + SysConfResolvPath, Script: prefix + "guard_file \"$backup\"\n'cp' '-p' -- \"$backup\" \"$tmp\"\n" + replace}
	}
	var content strings.Builder
	for _, server := range values.Servers {
		content.WriteString("nameserver " + server + "\n")
	}
	if len(values.Search) > 0 {
		content.WriteString("search " + strings.Join(values.Search, " ") + "\n")
	}
	write := "'cp' '-p' -- \"$file\" \"$tmp\"\n" +
		SystemCommand("printf", "%s", content.String()) + " > \"$tmp\"\n" +
		SystemCommand("awk", `$1 != "nameserver" && $1 != "search" && $1 != "domain" { print }`, SysConfResolvPath) + " >> \"$tmp\"\n"
	return SysConfChange{Target: SysConfResolvPath + "\n" + SysConfResolvBackup + "\n\n" + strings.TrimSpace(content.String()), Script: prefix + sysConfBackup + write + replace}
}

func sysConfResolvedChange(values DNSValues, routing []string, restore bool) SysConfChange {
	restart := SystemCommand("systemctl", "restart", "systemd-resolved.service")
	prefix := sysConfFileGuard + "file=" + privilegeQuote(SysConfResolvedPath) + "\nbackup=" + privilegeQuote(SysConfResolvedSave) + "\n" + `
'test' '!' '-L' '/etc/systemd/resolved.conf.d'
if [ ! -d '/etc/systemd/resolved.conf.d' ]; then
  'mkdir' '-m' '0755' '/etc/systemd/resolved.conf.d'
fi
guard_file "$backup"
guard_file "$backup.absent"
` + sysConfNewTemp + `
previous=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
cleanup() { 'rm' '-f' '--' "$tmp" "$previous"; }
existed='no'
if [ -f "$file" ]; then
  existed='yes'
  'cp' '-p' -- "$file" "$previous"
fi
`
	replace := "'mv' '-fT' -- \"$tmp\" \"$file\"\n"
	rollback := "if ! " + restart + `; then
  if [ "$existed" = 'yes' ]; then 'mv' '-fT' -- "$previous" "$file"; else 'rm' '-f' -- "$file"; fi
  ` + restart + ` || :
  exit 1
fi
`
	if restore {
		restoreFile := `if [ -f "$backup" ]; then
  'cp' '-p' -- "$backup" "$tmp"
  'mv' '-fT' -- "$tmp" "$file"
else
  'test' '-f' "$backup.absent"
  'rm' '-f' -- "$file"
fi
`
		return SysConfChange{Target: SysConfResolvedSave + " → " + SysConfResolvedPath + "\n" + SysConfResolvedSave + ".absent\n" + restart, Script: prefix + restoreFile + rollback}
	}
	backup := `if [ ! -f "$backup" ] && [ ! -f "$backup.absent" ]; then
  if [ "$existed" = 'yes' ]; then
    'cp' '-p' -- "$previous" "$tmp"
    'ln' -- "$tmp" "$backup"
    'rm' '-f' -- "$tmp"
    tmp=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
  else
    (umask '077'; set -C; : > "$backup.absent")
  fi
fi
`
	domains := slices.Clone(values.Search)
	for _, domain := range routing {
		if validSysConfRouting(domain) {
			domains = append(domains, domain)
		}
	}
	content := "[Resolve]\nDNS=\nDNS=" + strings.Join(values.Servers, " ") + "\nDomains=\nDomains=" + strings.Join(domains, " ") + "\n"
	write := SystemCommand("printf", "%s", content) + " > \"$tmp\"\n'chmod' '0644' \"$tmp\"\n"
	return SysConfChange{Target: SysConfResolvedPath + "\n" + SysConfResolvedSave + " / " + SysConfResolvedSave + ".absent\n" + restart + "\n\n" + strings.TrimSpace(content), Script: prefix + backup + write + replace + rollback}
}

func validSysConfUUID(uuid string) bool {
	parts := strings.Split(strings.ToLower(uuid), "-")
	if len(parts) != 5 {
		return false
	}
	for index, size := range []int{8, 4, 4, 4, 12} {
		if len(parts[index]) != size || strings.Trim(parts[index], "0123456789abcdef") != "" {
			return false
		}
	}
	return true
}

var sysConfNMProps = []string{"ipv4.dns", "ipv4.dns-search", "ipv4.ignore-auto-dns", "ipv6.dns", "ipv6.dns-search", "ipv6.ignore-auto-dns"}

func sysConfNMChange(uuid string, values DNSValues, restore bool) SysConfChange {
	backup := sysConfNMBackupDir + "/finallyshell-dns-" + uuid + ".bak"
	modify := SystemCommand("nmcli", "connection", "modify", "uuid", uuid)
	up := SystemCommand("nmcli", "--wait", "20", "connection", "up", "uuid", uuid)
	prefix := sysConfFileGuard + "file=" + privilegeQuote(backup) + "\n" + sysConfNewTemp + "\n"
	read := "(set -e\n"
	for _, prop := range sysConfNMProps {
		read += SystemCommand("nmcli", "--escape", "no", "-g", prop, "connection", "show", "uuid", uuid) + "\n"
	}
	read += ") > \"$tmp\"\n"
	readValues := func(file string) string {
		var script, args string
		for index, prop := range sysConfNMProps {
			varName := "nmvalue" + strconv.Itoa(index)
			script += "IFS= read '-r' " + privilegeQuote(varName) + "\n"
			args += " " + privilegeQuote(prop) + " \"$" + varName + "\""
		}
		return "{\n" + script + modify + args + "\n} < " + file + "\n"
	}
	if restore {
		return SysConfChange{Target: backup + "\n" + modify + " " + strings.Join(sysConfNMProps, " ") + "\n" + up, Script: prefix + read + "'test' '-f' \"$file\"\n" + readValues(`"$file"`) + sysConfNMActivate(up, readValues(`"$tmp"`))}
	}
	var ipv4, ipv6 []string
	for _, server := range values.Servers {
		if addr, _ := netip.ParseAddr(server); addr.Is4() {
			ipv4 = append(ipv4, server)
		} else {
			ipv6 = append(ipv6, server)
		}
	}
	join := func(servers []string) string { return strings.Join(servers, ",") }
	search4, search6 := "", ""
	if len(ipv4) > 0 {
		search4 = join(values.Search)
	}
	if len(ipv6) > 0 {
		search6 = join(values.Search)
	}
	args := append([]string{"nmcli", "connection", "modify", "uuid", uuid},
		sysConfNMProps[0], join(ipv4), sysConfNMProps[1], search4, sysConfNMProps[2], "yes",
		sysConfNMProps[3], join(ipv6), sysConfNMProps[4], search6, sysConfNMProps[5], "yes")
	command := SystemCommand(args...)
	return SysConfChange{Target: backup + "\n" + command + "\n" + up, Script: prefix + read +
		"if [ ! -f \"$file\" ]; then 'ln' -- \"$tmp\" \"$file\"; fi\n" + command + "\n" + sysConfNMActivate(up, readValues(`"$tmp"`))}
}

func sysConfNMActivate(up, rollback string) string {
	return "if ! " + up + "; then\n" + rollback + up + " || :\nexit 1\nfi\n"
}

func ValidateSysConfDNS(servers, search string) (DNSValues, error) {
	if len(servers) > 2048 || strings.ContainsAny(servers, "\x00\r") {
		return DNSValues{}, ErrSysConfDNSAddress
	}
	if len(search) > 2048 || strings.ContainsAny(search, "\x00\r") {
		return DNSValues{}, ErrSysConfDNSDomain
	}
	split := func(text string) []string {
		return strings.FieldsFunc(text, func(char rune) bool { return strings.ContainsRune(", \t\n", char) })
	}
	var values DNSValues
	for _, server := range split(servers) {
		addr, err := netip.ParseAddr(server)
		addr = addr.Unmap()
		if err != nil || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() {
			return DNSValues{}, ErrSysConfDNSAddress
		}
		if canonical := addr.String(); !slices.Contains(values.Servers, canonical) {
			values.Servers = append(values.Servers, canonical)
		}
	}
	if len(values.Servers) == 0 || len(values.Servers) > 16 {
		return DNSValues{}, ErrSysConfDNSAddress
	}
	for _, domain := range split(search) {
		domain = strings.ToLower(strings.TrimSuffix(domain, "."))
		if len(domain) > 253 {
			return DNSValues{}, ErrSysConfDNSDomain
		}
		for _, label := range strings.Split(domain, ".") {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") || strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
				return DNSValues{}, ErrSysConfDNSDomain
			}
		}
		if !slices.Contains(values.Search, domain) {
			values.Search = append(values.Search, domain)
		}
	}
	return values, nil
}
