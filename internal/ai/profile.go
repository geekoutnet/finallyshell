package ai

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// The server profile: what the assistant knows of a server before it runs
// a command, so that the commands it gives fit it (its package manager,
// init system, firewall, what runs on it). ProfileScript reads it in one
// round trip, ParseProfile reads the script's output, and Text renders it
// for the model.

// ProfileScript reads the server profile. It is POSIX sh that only reads:
// it runs on dash and BusyBox (Alpine, OpenWrt), each part guarded, so a
// tool the server lacks leaves its line out. Each line it prints is
// key=value. The tools that could hang run with a time limit where the
// server has timeout(1), and none reads the script, its stdin.
const ProfileScript = `export LC_ALL=C
PATH=$PATH:/usr/local/sbin:/usr/local/bin:/usr/sbin:/sbin:/snap/bin:/usr/local/go/bin
has() { command -v "$1" >/dev/null 2>&1; }
TO=
if has timeout && timeout 2 true </dev/null >/dev/null 2>&1; then TO=1; fi
t() { if [ -n "$TO" ]; then timeout 3 "$@" </dev/null; else "$@" </dev/null; fi; }
kv() { printf '%s=%s\n' "$1" "$2"; }
f=
if [ -r /etc/os-release ]; then f=/etc/os-release; elif [ -r /usr/lib/os-release ]; then f=/usr/lib/os-release; fi
if [ -n "$f" ]; then
  grep -E '^(PRETTY_NAME|NAME|ID|ID_LIKE|VERSION_ID)=' "$f" 2>/dev/null | sed 's/^/os_/'
fi
kv os_release "$(head -n 1 /etc/redhat-release 2>/dev/null || head -n 1 /etc/alpine-release 2>/dev/null || head -n 1 /etc/issue 2>/dev/null)"
kv kernel "$(uname -sr 2>/dev/null)"
kv arch "$(uname -m 2>/dev/null)"
kv hostname "$(hostname 2>/dev/null || uname -n 2>/dev/null)"
v=
if has systemd-detect-virt; then v=$(t systemd-detect-virt 2>/dev/null); fi
if [ -z "$v" ] && [ -f /.dockerenv ]; then v=docker; fi
if [ -z "$v" ] && [ -f /run/.containerenv ]; then v=podman; fi
kv virt "$v"
i=$(cat /proc/1/comm 2>/dev/null)
[ -n "$i" ] || i=$(ps -p 1 -o comm= 2>/dev/null)
kv init "$i"
SD=
if has systemctl && [ -d /run/systemd/system ]; then SD=1; kv systemd yes; fi
if has rc-service; then kv openrc yes; fi
p=
for m in apt-get dnf yum zypper apk pacman opkg emerge xbps-install; do has "$m" && p="$p $m"; done
kv pkg "${p# }"
kv user "$(id -un 2>/dev/null || whoami 2>/dev/null)"
u=$(id -u 2>/dev/null)
kv uid "$u"
if [ "$u" = 0 ]; then kv sudo root
elif has sudo; then
  if t sudo -n true >/dev/null 2>&1; then kv sudo yes; else kv sudo no; fi
else kv sudo missing; fi
kv cpus "$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || grep -c '^processor' /proc/cpuinfo 2>/dev/null)"
kv mem_kb "$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null)"
kv root_disk "$(df -Pk / 2>/dev/null | awk 'NR == 2 {print $2, $3, $5}')"
z=$(cat /etc/timezone 2>/dev/null)
[ -n "$z" ] || z=$(readlink /etc/localtime 2>/dev/null | sed -n 's|.*zoneinfo/||p')
if [ -z "$z" ] && has uci; then z=$(uci -q get 'system.@system[0].zonename' 2>/dev/null); fi
[ -n "$z" ] || z=$(cat /etc/TZ 2>/dev/null)
if [ -z "$z" ] && has timedatectl; then z=$(t timedatectl show -p Timezone --value 2>/dev/null); fi
kv tz "$z"
kv tz_offset "$(date +%z 2>/dev/null)"
f=
for m in firewall-cmd ufw nft iptables fw4 fw3; do has "$m" && f="$f $m"; done
kv firewall "${f# }"
if [ -n "$SD" ]; then kv fw_state "$(t systemctl is-active firewalld nftables 2>/dev/null | tr '\n' ' ')"; fi
if [ -r /etc/ufw/ufw.conf ]; then
  if grep -q '^ENABLED=yes' /etc/ufw/ufw.conf 2>/dev/null; then kv ufw_enabled yes; else kv ufw_enabled no; fi
fi
if has getenforce; then kv selinux "$(t getenforce 2>/dev/null)"
elif [ -r /sys/fs/selinux/enforce ]; then kv selinux_enforce "$(cat /sys/fs/selinux/enforce 2>/dev/null)"; fi
if [ -r /sys/module/apparmor/parameters/enabled ]; then kv apparmor "$(cat /sys/module/apparmor/parameters/enabled 2>/dev/null)"; fi
c=
for m in docker podman containerd nerdctl kubectl k3s lxc incus; do has "$m" && c="$c $m"; done
kv containers "${c# }"
if [ -S /var/run/docker.sock ]; then
  if [ -w /var/run/docker.sock ]; then kv docker_sock usable; else kv docker_sock denied; fi
fi
units() { t systemctl list-units --type=service --state="$1" --no-legend 2>/dev/null | awk '{ if ($1 ~ /[.]service$/) print $1; else print $2 }' | sed 's/[.]service$//'; }
if [ -n "$SD" ]; then
  kv services "$(units running | grep -E '^(nginx|openresty|tengine|apache2|httpd|caddy|lighttpd|haproxy|traefik|mysql|mysqld|mariadb|postgresql.*|mongod|redis|redis-server|valkey.*|memcached|elasticsearch|rabbitmq-server|docker|containerd|podman|php.*fpm.*|sshd|ssh|firewalld|ufw|nftables|fail2ban|crond|cron|supervisor.*|pm2-.*|kubelet|k3s|named|bind9|postfix|exim4|dovecot|vsftpd|pure-ftpd|nfs-server|smbd|zabbix-agent.*|node_exporter|prometheus|grafana-server|jenkins|tomcat.*|frpc|frps|xray|v2ray|nezha-agent|bt|1panel.*|openvpn.*|wg-quick@.*|wireguard.*)$' | head -n 40 | tr '\n' ' ')"
  kv failed "$(units failed | head -n 8 | tr '\n' ' ')"
else
  r=
  for n in nginx httpd apache2 caddy lighttpd uhttpd mysqld mariadbd postgres redis-server dockerd containerd sshd dropbear php-fpm fail2ban-server crond dnsmasq; do
    pidof "$n" >/dev/null 2>&1 && r="$r $n"
  done
  kv daemons "${r# }"
fi
if has node; then kv rt_node "$(t node --version 2>/dev/null | head -n 1)"; fi
if has python3; then kv rt_python3 "$(t python3 --version 2>&1 | head -n 1)"; fi
if has java; then kv rt_java "$(t java -version 2>&1 | head -n 1)"; fi
if has go; then kv rt_go "$(t go version 2>/dev/null | head -n 1)"; fi
if has php; then kv rt_php "$(t php -v 2>/dev/null | head -n 1)"; fi
l=
if has ss; then l=$(t ss -tln 2>/dev/null)
elif has netstat; then l=$(t netstat -tln 2>/dev/null); fi
kv tcp_listen "$(printf '%s\n' "$l" | awk '{print $4}' | grep -E ':[0-9]+$' | sort -u | head -n 300 | tr '\n' ' ')"
s=${SHELL:-}
if [ -z "$s" ] && has getent; then s=$(getent passwd "$(id -un 2>/dev/null)" 2>/dev/null | cut -d: -f7); fi
[ -n "$s" ] || s=$(grep "^$(id -un 2>/dev/null):" /etc/passwd 2>/dev/null | head -n 1 | cut -d: -f7)
kv shell "$s"
if [ -d /www/server/panel ]; then kv panel_bt yes; fi
if has 1pctl; then kv panel_1panel yes; fi
kv end 1
`

// ServerProfile is what ProfileScript found on a server, by key.
type ServerProfile map[string]string

var profileKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// profileValueLimit is the longest value kept, but for the listening
// ports, which are many.
const profileValueLimit = 200

// ParseProfile reads ProfileScript's output: the key=value lines, the
// first of each key, with control characters and quotes taken out. Lines
// that are not the script's are left out.
func ParseProfile(out string) ServerProfile {
	p := ServerProfile{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || !profileKey.MatchString(key) {
			continue
		}
		if _, seen := p[key]; seen {
			continue
		}
		limit := profileValueLimit
		if key == "tcp_listen" {
			limit = 8 << 10
		}
		if value = profileValue(value, limit); value != "" {
			p[key] = value
		}
	}
	return p
}

// profileValue is value on one line, without the quotes of os-release,
// at most limit bytes.
func profileValue(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"' || value[0] == '\'' && value[len(value)-1] == '\'') {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return Limit(value, limit)
}

// profileMaxPorts is how many listening ports Text lists.
const profileMaxPorts = 40

// Text is the profile for the model, a line for each thing known, with
// secrets masked; "" when the output was not the script's.
func (p ServerProfile) Text() string {
	if p["kernel"] == "" && p["os_pretty_name"] == "" && p["os_id"] == "" {
		return ""
	}
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	list := func(value string) string { return strings.Join(strings.Fields(value), ", ") }

	name := cmp.Or(p["os_pretty_name"], profileIssue(p["os_release"]), p["os_name"], p["os_id"])
	var about []string
	for _, field := range []struct{ key, label string }{{"os_id", "id"}, {"os_id_like", "like"}, {"os_version_id", "version"}} {
		if v := p[field.key]; v != "" {
			about = append(about, field.label+" "+v)
		}
	}
	if len(about) > 0 {
		name += "; " + strings.Join(about, ", ")
	}
	add("OS", name)
	add("Kernel", profileJoin(", ", p["kernel"], p["arch"]))
	add("Hostname", p["hostname"])
	add("Virtualization", p["virt"])
	add("Init", profileInit(p))
	add("Package managers", list(p["pkg"]))
	add("User", profileUser(p))
	add("Hardware", profileHardware(p))
	offset := p["tz_offset"]
	if offset != "" {
		offset = "UTC" + offset
		if p["tz"] != "" {
			offset = "(" + offset + ")"
		}
	}
	add("Timezone", profileJoin(" ", p["tz"], offset))
	add("Firewall", profileFirewall(p))
	add("Security modules", profileSecurity(p))
	containers := list(p["containers"])
	switch p["docker_sock"] {
	case "usable":
		containers = profileJoin(", ", containers, "Docker daemon socket usable by this user")
	case "denied":
		containers = profileJoin(", ", containers, "Docker daemon socket needs sudo or the docker group")
	}
	add("Container tools", containers)
	add("Running services", list(p["services"]))
	add("Failed services", list(p["failed"]))
	add("Running daemons", list(p["daemons"]))
	add("Runtimes", profileRuntimes(p))
	add("Listening TCP ports", profilePorts(p["tcp_listen"]))
	add("Login shell", p["shell"])
	var panels []string
	if p["panel_bt"] == "yes" {
		panels = append(panels, "BT Panel (/www/server/panel)")
	}
	if p["panel_1panel"] == "yes" {
		panels = append(panels, "1Panel")
	}
	add("Server panel", strings.Join(panels, ", "))
	return Mask(strings.Join(lines, "\n"))
}

// profileJoin joins the parts that are not empty.
func profileJoin(sep string, parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), sep)
}

// profileIssue is the first line of /etc/issue or a release file, without
// the escapes getty expands (\n, \l).
func profileIssue(line string) string {
	words := slices.DeleteFunc(strings.Fields(line), func(word string) bool { return strings.HasPrefix(word, `\`) })
	return strings.Join(words, " ")
}

func profileInit(p ServerProfile) string {
	init := p["init"]
	switch {
	case p["systemd"] == "yes":
		return "systemd (systemctl, journalctl)"
	case p["openrc"] == "yes":
		return strings.TrimSpace(init + " with OpenRC (rc-service, rc-update)")
	case init == "procd":
		return "procd (OpenWrt: /etc/init.d/<name>, uci)"
	}
	return init
}

func profileUser(p ServerProfile) string {
	user := p["user"]
	if uid := p["uid"]; uid != "" {
		user = strings.TrimSpace(user + " (uid " + uid + ")")
	}
	switch p["sudo"] {
	case "root":
		user += "; running as root"
	case "yes":
		user += "; sudo -n works without a password"
	case "no":
		user += "; sudo needs a password or is not allowed"
	case "missing":
		user += "; no sudo"
	}
	return strings.TrimPrefix(user, "; ")
}

func profileHardware(p ServerProfile) string {
	var parts []string
	if cpus := p["cpus"]; cpus != "" {
		parts = append(parts, cpus+" CPUs")
	}
	if kb, err := strconv.ParseUint(p["mem_kb"], 10, 64); err == nil && kb > 0 {
		parts = append(parts, profileSize(kb)+" memory")
	}
	if disk := strings.Fields(p["root_disk"]); len(disk) == 3 {
		if kb, err := strconv.ParseUint(disk[0], 10, 64); err == nil && kb > 0 {
			parts = append(parts, "root disk "+profileSize(kb)+", "+disk[2]+" used")
		}
	}
	return strings.Join(parts, "; ")
}

// profileSize is a size given in KiB.
func profileSize(kb uint64) string {
	switch gib := float64(kb) / (1 << 20); {
	case gib >= 1024:
		return fmt.Sprintf("%.1f TiB", gib/1024)
	case gib >= 1:
		return fmt.Sprintf("%.1f GiB", gib)
	}
	return fmt.Sprintf("%d MiB", kb>>10)
}

func profileFirewall(p ServerProfile) string {
	tools := strings.Join(strings.Fields(p["firewall"]), ", ")
	var active []string
	known := false
	if states := strings.Fields(p["fw_state"]); len(states) > 0 {
		known = true
		for i, unit := range []string{"firewalld", "nftables"} {
			if i < len(states) && states[i] == "active" {
				active = append(active, unit)
			}
		}
	}
	if enabled := p["ufw_enabled"]; enabled != "" {
		known = true
		if enabled == "yes" {
			active = append(active, "ufw")
		}
	}
	if tools == "" && !known {
		return ""
	}
	if tools == "" {
		tools = "no firewall tool found"
	}
	if known {
		state := "none of firewalld, nftables, ufw"
		if len(active) > 0 {
			state = strings.Join(active, ", ")
		}
		tools += "; active: " + state
	}
	return tools
}

func profileSecurity(p ServerProfile) string {
	var parts []string
	selinux := p["selinux"]
	switch p["selinux_enforce"] {
	case "1":
		selinux = "Enforcing"
	case "0":
		selinux = "Permissive"
	}
	if selinux != "" {
		parts = append(parts, "SELinux "+selinux)
	}
	switch p["apparmor"] {
	case "Y", "y", "1":
		parts = append(parts, "AppArmor enabled")
	case "N", "n", "0":
		parts = append(parts, "AppArmor disabled")
	}
	return strings.Join(parts, "; ")
}

var profileVersion = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)+(?:[_+-][0-9A-Za-z.]+)?`)

func profileRuntimes(p ServerProfile) string {
	var parts []string
	for _, name := range []string{"node", "python3", "java", "go", "php"} {
		raw := p["rt_"+name]
		if raw == "" {
			continue
		}
		version := profileVersion.FindString(raw)
		if version == "" {
			version = Limit(raw, 40)
		}
		parts = append(parts, name+" "+version)
	}
	return strings.Join(parts, ", ")
}

// profilePorts lists the ports of the listening addresses (ss and netstat
// print them as 0.0.0.0:22, [::]:22, :::22, *:80, 127.0.0.1:6379), in
// order, those that listen on loopback alone marked.
func profilePorts(value string) string {
	public := map[int]bool{}
	for _, addr := range strings.Fields(value) {
		i := strings.LastIndexByte(addr, ':')
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(addr[i+1:])
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		host := strings.Trim(addr[:i], "[]")
		loopback := strings.HasPrefix(host, "127.") || host == "::1" || host == "localhost" || strings.HasPrefix(host, "::ffff:127.")
		public[port] = public[port] || !loopback
	}
	ports := make([]int, 0, len(public))
	for port := range public {
		ports = append(ports, port)
	}
	slices.Sort(ports)
	var parts []string
	for _, port := range ports[:min(len(ports), profileMaxPorts)] {
		part := strconv.Itoa(port)
		if !public[port] {
			part += " (localhost)"
		}
		parts = append(parts, part)
	}
	if more := len(ports) - profileMaxPorts; more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	return strings.Join(parts, ", ")
}

// ProfileHeading starts the profile's message, with the day it was read:
// the day alone, so that requests on the same day begin alike, and the
// providers' prompt caches keep them.
func ProfileHeading(day time.Time) string {
	return "[Server profile, untrusted data, collected " + day.Format(time.DateOnly) + "]"
}

// ProfileMessage is the message that gives the model text, a profile read
// on day.
func ProfileMessage(text string, day time.Time) string {
	return ProfileHeading(day) + "\n" + text
}
