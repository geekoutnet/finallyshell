package monitor

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// FirewallDetectScript runs as the login user, whose PATH on Debian and
// others leaves out the sbin folders the firewall tools live in.
const FirewallDetectScript = `PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin"
for tool in 'ufw' 'firewall-cmd' 'nft' 'iptables'; do
  if command -v "$tool" >/dev/null 2>&1; then printf '%s\n' "$tool"; fi
done`

const FirewallConnectionsScript = `LC_ALL=C 'ss' '-tnp' 'state' 'established'`
const FirewallListeningScript = `LC_ALL=C 'ss' '-tulpn'`
const FirewallSessionScript = `printf '%s\n' "${SSH_CONNECTION:-}"`

type FirewallRule struct {
	Number                    int
	To, Action, From, Comment string
}

type UFWState struct {
	Active, Known bool
	Default       string
	Rules         []FirewallRule
}

const UFWReadScript = `export LC_ALL=C
'ufw' 'status' 'verbose' && 'ufw' 'status' 'numbered'`

var ufwRule = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s+(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s{2,}(.+)$`)

func ParseUFW(output string) UFWState {
	var result UFWState
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "Status: active" || line == "Status: inactive":
			result.Known = true
			result.Active = line == "Status: active"
		case strings.HasPrefix(line, "Default:"):
			result.Default = strings.TrimSpace(strings.TrimPrefix(line, "Default:"))
		default:
			fields := ufwRule.FindStringSubmatch(line)
			if len(fields) == 0 {
				continue
			}
			number, err := strconv.Atoi(fields[1])
			if err != nil || number < 1 {
				continue
			}
			from, note, _ := strings.Cut(fields[5], " # ")
			result.Rules = append(result.Rules, FirewallRule{Number: number, To: strings.TrimSpace(fields[2]), Action: strings.TrimSpace(fields[3] + " " + fields[4]), From: from, Comment: note})
		}
	}
	return result
}

type FirewallZone struct {
	Name, Interfaces, Sources string
	Active                    bool
	Services, Ports, Rich     []string
}

func ParseFirewallZones(output string) []FirewallZone {
	var zones []FirewallZone
	inRich := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, suffix, _ := strings.Cut(trimmed, " ")
			if !firewallName.MatchString(name) {
				continue
			}
			zones = append(zones, FirewallZone{Name: name, Active: suffix == "(active)"})
			inRich = false
			continue
		}
		if len(zones) == 0 {
			continue
		}
		zone := &zones[len(zones)-1]
		if inRich && strings.HasPrefix(trimmed, "rule ") {
			zone.Rich = append(zone.Rich, trimmed)
			continue
		}
		key, value, _ := strings.Cut(trimmed, ":")
		value = strings.TrimSpace(value)
		inRich = key == "rich rules"
		switch key {
		case "interfaces":
			zone.Interfaces = value
		case "sources":
			zone.Sources = value
		case "services":
			zone.Services = strings.Fields(value)
		case "ports":
			zone.Ports = strings.Fields(value)
		case "rich rules":
			if value != "" {
				zone.Rich = append(zone.Rich, value)
			}
		}
	}
	return zones
}

const FirewallZonesScript = `LC_ALL=C 'firewall-cmd' '--list-all-zones'`
const FirewallNFTScript = `LC_ALL=C 'nft' '--numeric' 'list' 'ruleset'`
const FirewallIPTablesScript = `LC_ALL=C 'iptables' '-S'; if command -v 'ip6tables' >/dev/null 2>&1; then LC_ALL=C 'ip6tables' '-S'; fi`

func FirewallSessionEndpoint(output string) (netip.Addr, int) {
	fields := strings.Fields(output)
	if len(fields) != 4 {
		return netip.Addr{}, 0
	}
	peer, err := netip.ParseAddr(fields[0])
	if err != nil {
		return netip.Addr{}, 0
	}
	port, _ := strconv.Atoi(fields[3])
	if port < 1 || port > 65535 {
		return netip.Addr{}, 0
	}
	return peer.Unmap(), port
}

func FirewallSSHPorts(configured int, output string) []int {
	var ports []int
	if configured > 0 && configured <= 65535 {
		ports = append(ports, configured)
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, `"sshd"`) && !strings.Contains(line, `"sshd-session"`) && !strings.Contains(line, `"sshd-auth"`) {
			continue
		}
		fields := strings.Fields(line)
		index := 2
		if len(fields) > 0 && fields[0] == "ESTAB" {
			index = 3
		}
		if len(fields) <= index {
			continue
		}
		address := fields[index]
		port, _ := strconv.Atoi(address[strings.LastIndexByte(address, ':')+1:])
		if port > 0 && port <= 65535 && !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	slices.Sort(ports)
	return ports
}

func firewallPortRange(value string) (int, int, bool) {
	low, high, ranged := strings.Cut(strings.ReplaceAll(value, ":", "-"), "-")
	first, err := strconv.Atoi(low)
	if err != nil || first < 1 || first > 65535 || strconv.Itoa(first) != low {
		return 0, 0, false
	}
	last := first
	if ranged {
		last, err = strconv.Atoi(high)
		if err != nil || last <= first || last > 65535 || strconv.Itoa(last) != high {
			return 0, 0, false
		}
	}
	return first, last, true
}

func FirewallPortProtected(target string, ports []int) bool {
	if len(ports) == 0 {
		return true
	}
	value := strings.TrimSuffix(strings.TrimSpace(target), " (v6)")
	if strings.HasSuffix(value, "/udp") {
		_, _, valid := firewallPortRange(strings.TrimSuffix(value, "/udp"))
		return !valid
	}
	first, last, valid := firewallPortRange(strings.TrimSuffix(value, "/tcp"))
	if !valid {
		return true
	}
	return slices.ContainsFunc(ports, func(port int) bool { return first <= port && port <= last })
}

var firewallName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

const firewallBackups = `umask '077'
stamp=$('date' '+%Y%m%d%H%M%S')
backup_file() {
  [ -e "$1" ] || return 0
  [ -f "$1" ] && [ ! -L "$1" ] || return 1
  backup="$1.bak-$stamp"
  (set -C; : > "$backup") || return 1
  'cp' '--' "$1" "$backup"
}`

const ufwBackups = firewallBackups + `
for file in '/etc/ufw/ufw.conf' '/etc/ufw/user.rules' '/etc/ufw/user6.rules' '/lib/ufw/user.rules' '/lib/ufw/user6.rules'; do
  backup_file "$file"
done`

func UFWAddScript(action, port, proto, source, comment string, protected []int) (string, error) {
	first, last, valid := firewallPortRange(port)
	if !valid || action != "allow" && action != "deny" || proto != "tcp" && proto != "udp" || len(comment) > 100 || !opsValidText(comment) {
		return "", ErrOpsInvalid
	}
	if action == "deny" && (len(protected) == 0 || proto == "tcp" && FirewallPortProtected(port, protected)) {
		return "", ErrOpsInvalid
	}
	if source == "" {
		source = "any"
	} else if prefix, err := netip.ParsePrefix(source); err == nil {
		source = prefix.Masked().String()
	} else if addr, err := netip.ParseAddr(source); err == nil && addr.Zone() == "" {
		source = addr.Unmap().String()
	} else {
		return "", ErrOpsInvalid
	}
	port = strconv.Itoa(first)
	if last != first {
		port += ":" + strconv.Itoa(last)
	}
	args := []string{action, "proto", proto, "from", source, "to", "any", "port", port}
	if comment != "" {
		args = append(args, "comment", comment)
	}
	return "set -e\n" + ufwBackups + "\n" + opsCommand("ufw", args...), nil
}

func UFWDeleteScript(rule FirewallRule, protected []int) (string, error) {
	if rule.Number < 1 || len(protected) == 0 || FirewallPortProtected(rule.To, protected) || !strings.HasSuffix(rule.Action, " IN") {
		return "", ErrOpsInvalid
	}
	number := strconv.Itoa(rule.Number)
	expected := strings.Join(strings.Fields(rule.To+" "+rule.Action+" "+rule.From+" "+strings.TrimSpace(rule.Comment)), " ")
	if !opsValidText(expected) {
		return "", ErrOpsInvalid
	}
	return "set -e\nexport LC_ALL=C\n" + `rules=$('ufw' 'status' 'numbered')
current=$(printf '%s\n' "$rules" | 'sed' '-nE' ` + privilegeQuote(`s/^\[ *`+number+`\] *(.*)$/\1/p`) + ` | 'sed' '-E' 's/ +# / /; s/[[:space:]]+/ /g; s/ $//')
[ "$current" = ` + privilegeQuote(expected) + ` ] || { printf '%s\n' 'Firewall rule changed; reload and try again' >&2; exit 1; }
` + ufwBackups + "\n" + opsCommand("ufw", "--force", "delete", number), nil
}

func UFWToggleScript(enable bool, actual int, source netip.Addr) (string, error) {
	if !enable {
		return "set -e\n" + ufwBackups + "\n" + opsCommand("ufw", "disable"), nil
	}
	if actual < 1 || actual > 65535 || !source.IsValid() || source.Zone() != "" {
		return "", ErrOpsInvalid
	}
	port := strconv.Itoa(actual)
	prefix := "set -e\nexport LC_ALL=C\n"
	if source.Is6() {
		prefix += `'grep' '-Eq' '^[[:space:]]*IPV6=yes([[:space:]]|$)' '/etc/default/ufw' || { printf '%s\n' 'UFW IPv6 support is disabled' >&2; exit 1; }` + "\n"
	}
	prefix += `for file in '/etc/ufw/before.rules' '/etc/ufw/before6.rules'; do
  name=${file##*/}
  if 'cmp' '-s' "$file" '/usr/share/ufw/iptables/'"$name" || 'cmp' '-s' "$file" '/usr/share/ufw/'"$name"; then
    continue
  fi
  if [ -r "$file" ] && 'grep' '-Eq' '^-A .* (-j|--jump) (DROP|REJECT)([[:space:]]|$)' "$file"; then
    if 'grep' '-E' '^-A .* (-j|--jump) (DROP|REJECT)([[:space:]]|$)' "$file" | 'grep' '-Evq' -- '^-A ufw6?-before-input -m conntrack --ctstate INVALID -j DROP[[:space:]]*$'; then
      printf '%s\n' 'Custom UFW pre-rules may block SSH; review them before enabling' >&2
      exit 1
    fi
  fi
done` + "\n"
	return prefix + ufwBackups + "\n" + opsCommand("ufw", "prepend", "allow", "proto", "tcp", "from", source.String(), "to", "any", "port", port, "comment", "SSH session safety") + "\n" + opsCommand("ufw", "--force", "enable"), nil
}

func FirewallZoneChangeScript(zone, kind, value string, add bool, protected []int) (string, error) {
	if !firewallName.MatchString(zone) || len(protected) == 0 || kind != "port" && kind != "service" {
		return "", ErrOpsInvalid
	}
	if kind == "port" {
		port, proto, _ := strings.Cut(value, "/")
		first, last, valid := firewallPortRange(port)
		if !valid || proto != "tcp" && proto != "udp" || !add && FirewallPortProtected(value, protected) {
			return "", ErrOpsInvalid
		}
		value = strconv.Itoa(first)
		if first != last {
			value += "-" + strconv.Itoa(last)
		}
		value += "/" + proto
	} else if !firewallName.MatchString(value) || !add && strings.EqualFold(value, "ssh") {
		return "", ErrOpsInvalid
	}
	action := "remove"
	if add {
		action = "add"
	}
	target := "/etc/firewalld/zones/" + zone + ".xml"
	prefix := "set -e\nexport LC_ALL=C\n" + firewallBackups + "\nbackup_file " + privilegeQuote(target) + "\n"
	if !add && kind == "service" {
		var protectedArgs []string
		for _, port := range protected {
			if port < 1 || port > 65535 {
				return "", ErrOpsInvalid
			}
			protectedArgs = append(protectedArgs, privilegeQuote(strconv.Itoa(port)))
		}
		prefix += fmt.Sprintf(`info=$(%s)
if printf '%%s\n' "$info" | 'grep' '-Eq' '^[[:space:]]*(includes|protocols|source-ports|helpers):[[:space:]]+[^[:space:]]'; then exit 1; fi
ports=$(printf '%%s\n' "$info" | 'sed' '-n' 's/^[[:space:]]*ports:[[:space:]]*//p')
[ -n "$ports" ] || exit 1
set -f
for port in $ports; do
  case "$port" in
    */udp) continue;;
    */tcp) number=${port%%/tcp};;
    *) exit 1;;
  esac
  first=${number%%%%-*}; last=${number##*-}
  case "$first:$last" in :*|*:|*[!0-9:]*) exit 1;; esac
  for protected in %s; do
    if [ "$first" -le "$protected" ] && [ "$last" -ge "$protected" ]; then exit 1; fi
  done
done
`, opsCommand("firewall-cmd", "--info-service="+value), strings.Join(protectedArgs, " "))
	}
	args := []string{"--zone=" + zone, "--" + action + "-" + kind + "=" + value}
	reverse := "add"
	if add {
		reverse = "remove"
	}
	runtime := opsCommand("firewall-cmd", args...)
	permanent := opsCommand("firewall-cmd", append([]string{"--permanent"}, args...)...)
	rollback := opsCommand("firewall-cmd", "--zone="+zone, "--"+reverse+"-"+kind+"="+value)
	query := opsCommand("firewall-cmd", "--zone="+zone, "--query-"+kind+"="+value)
	state := "1"
	if add {
		state = "0"
	}
	return prefix + "before=0\n" + query + ` >/dev/null || before=$?
[ "$before" -le 1 ] || exit "$before"
if [ "$before" != ` + privilegeQuote(state) + " ]; then\n  " + runtime + "\nfi\n" + permanent + " || { if [ \"$before\" != " + privilegeQuote(state) + " ]; then " + rollback + " || :; fi; exit 1; }", nil
}
