package monitor

import (
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

const SecurityAuthScript = `export LC_ALL=C
if command -v 'journalctl' >/dev/null 2>&1; then
  data=$('journalctl' '--no-pager' '-q' '-n' '10000' '-o' 'short-iso' '-u' 'ssh' '-u' 'sshd' '--since' '7 days ago' 2>/dev/null) || data=''
  if [ -n "$data" ]; then printf '%s\n' "$data"; exit 0; fi
fi
for file in '/var/log/auth.log' '/var/log/secure'; do
  if [ -r "$file" ]; then 'date' '+---today---%Y-%m-%d'; 'tail' '-n' '10000' "$file"; exit $?; fi
done
exit 0`

const SecurityUsersScript = `export LC_ALL=C
'who' 2>/dev/null || 'w' '-h'
printf '%s\n' '---last---'
'last' '-n' '50' '-F' '-i' 2>/dev/null || 'last' '-n' '50' 2>/dev/null || :`

const SecuritySettingsScript = `export LC_ALL=C
if command -v 'sshd' >/dev/null 2>&1; then tool='sshd'; elif [ -x '/usr/sbin/sshd' ]; then tool='/usr/sbin/sshd'; else exit 1; fi
data=$("$tool" '-T') || exit $?
printf '%s\n' "$data" | 'grep' '-E' '^(permitrootlogin|passwordauthentication|port) '`

const SecuritySudoScript = `export LC_ALL=C
if command -v 'journalctl' >/dev/null 2>&1; then
  data=$('journalctl' '--no-pager' '-q' '-n' '200' '-o' 'short-iso' '--since' '7 days ago' 'SYSLOG_IDENTIFIER=sudo' 2>/dev/null) || data=''
fi
if [ -z "$data" ]; then
  for file in '/var/log/auth.log' '/var/log/secure'; do
    if [ -r "$file" ]; then data=$('tail' '-n' '10000' "$file"); break; fi
  done
fi
printf '%s\n' "$data" | 'sed' '-nE' 's/^(.+[[:space:]]sudo(\[[0-9]+\])?:[[:space:]]+[[:alnum:]_.-]+)[[:space:]]*:.*COMMAND=.*/\1/p' | 'tail' '-n' '100'`

type SSHLogin struct {
	When, Day, User, Method string
	IP                      netip.Addr
	Accepted                bool
}

var sshMessage = regexp.MustCompile(`^(.+?)\s+sshd(?:-session|-auth)?(?:\[([0-9]+)\])?:\s+(.+)$`)
var sshAttempt = regexp.MustCompile(`^(Accepted|Failed) (\S+) for (?:invalid user )?(\S+) from (\S+) port (\d+)`)
var sshInvalid = regexp.MustCompile(`^Invalid user (\S+) from (\S+)(?: port (\d+))?`)
var sshPreauth = regexp.MustCompile(`^(?:Disconnected from|Connection (?:closed|reset) by) (?:(?:invalid user|authenticating user|user) (\S+) )?(\S+) port (\d+) .*\[preauth\]$|^(?:Disconnected from|Connection (?:closed|reset) by) (?:(?:invalid user|authenticating user|user) (\S+) )?(\S+) port (\d+) \[preauth\]$`)
var sshUser = regexp.MustCompile(`^[[:alnum:]_.@$-]{1,128}$`)

func ParseSSHLog(output string) []SSHLogin {
	var today time.Time
	if stamp, remaining, ok := strings.Cut(output, "\n"); ok && strings.HasPrefix(stamp, "---today---") {
		today, _ = time.Parse("2006-01-02", strings.TrimPrefix(strings.TrimSpace(stamp), "---today---"))
		output = remaining
	}
	var records []SSHLogin
	var fallback []bool
	var keys []string
	counted := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		match := sshMessage.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) == 0 {
			continue
		}
		fields := strings.Fields(match[1])
		if len(fields) < 2 {
			continue
		}
		when := strings.Join(fields[:len(fields)-1], " ")
		entry := SSHLogin{When: when}
		if len(fields) >= 4 {
			entry.Day = strings.Join(fields[:2], " ")
			if !today.IsZero() {
				stamp, err := time.Parse("2006 Jan 2 15:04:05", today.Format("2006")+" "+strings.Join(fields[:3], " "))
				if err != nil {
					continue
				}
				if !stamp.Before(today.AddDate(0, 0, 1)) {
					stamp = stamp.AddDate(-1, 0, 0)
				}
				if stamp.Before(today.AddDate(0, 0, -7)) {
					continue
				}
				entry.Day, entry.When = stamp.Format("2006-01-02"), stamp.Format("2006-01-02 15:04:05")
			}
		} else if day, _, ok := strings.Cut(when, "T"); ok && len(day) == 10 {
			entry.Day = day
			if !today.IsZero() && day < today.AddDate(0, 0, -7).Format("2006-01-02") {
				continue
			}
		} else {
			continue
		}
		var address, port string
		isFallback := true
		switch {
		case sshAttempt.MatchString(match[3]):
			parts := sshAttempt.FindStringSubmatch(match[3])
			entry.Accepted, entry.Method, entry.User, address, port = parts[1] == "Accepted", parts[2], parts[3], parts[4], parts[5]
			isFallback = false
		case sshInvalid.MatchString(match[3]):
			parts := sshInvalid.FindStringSubmatch(match[3])
			entry.Method, entry.User, address, port = "invalid-user", parts[1], parts[2], parts[3]
		default:
			parts := sshPreauth.FindStringSubmatch(match[3])
			if len(parts) == 0 {
				continue
			}
			user, addr, sourcePort := parts[1], parts[2], parts[3]
			if addr == "" {
				user, addr, sourcePort = parts[4], parts[5], parts[6]
			}
			entry.Method, entry.User, address, port = "preauth", user, addr, sourcePort
		}
		ip, err := netip.ParseAddr(address)
		if err != nil {
			continue
		}
		if !sshUser.MatchString(entry.User) {
			entry.User = ""
		}
		entry.IP = ip.Unmap()
		key := entry.Day + "/" + match[2] + "/" + entry.IP.String() + "/" + port
		counted[key] = counted[key] || !isFallback
		fallback = append(fallback, isFallback)
		keys = append(keys, key)
		records = append(records, entry)
	}
	var result []SSHLogin
	for index, entry := range records {
		if !fallback[index] || !counted[keys[index]] {
			result = append(result, entry)
			counted[keys[index]] = true
		}
	}
	return result
}

const Fail2banStatusScript = `if command -v 'fail2ban-client' >/dev/null 2>&1; then
  printf '%s\n' 'installed'
  LC_ALL=C 'fail2ban-client' 'status' 2>/dev/null || printf '%s
' '---stopped---'
fi`

// Fail2banStopped reports whether Fail2banStatusScript found Fail2ban
// installed but its server not running.
func Fail2banStopped(status string) bool {
	return strings.Contains(status, "---stopped---")
}

var fail2banJail = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}$`)

func ParseFail2banJails(output string) []string {
	for _, line := range strings.Split(output, "\n") {
		if _, list, found := strings.Cut(line, "Jail list:"); found {
			var result []string
			for _, value := range strings.Split(list, ",") {
				if name := strings.TrimSpace(value); fail2banJail.MatchString(name) && !slices.Contains(result, name) {
					result = append(result, name)
				}
			}
			return result
		}
	}
	return nil
}

func ParseFail2banIPs(output string) []netip.Addr {
	var result []netip.Addr
	for _, line := range strings.Split(output, "\n") {
		if _, list, ok := strings.Cut(line, "Banned IP list:"); ok {
			for _, candidate := range strings.Fields(list) {
				if ip, err := netip.ParseAddr(candidate); err == nil && ip.Zone() == "" && !slices.Contains(result, ip) {
					result = append(result, ip)
				}
			}
		}
	}
	return result
}

func Fail2banJailScript(jail string) (string, error) {
	if !fail2banJail.MatchString(jail) {
		return "", ErrOpsInvalid
	}
	return opsCommand("fail2ban-client", "status", jail), nil
}

func Fail2banChangeScript(jail, address string, ban bool) (string, error) {
	ip, err := netip.ParseAddr(address)
	if !fail2banJail.MatchString(jail) || err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return "", ErrOpsInvalid
	}
	change := "unbanip"
	if ban {
		change = "banip"
	}
	return opsCommand("fail2ban-client", "set", jail, change, ip.Unmap().String()), nil
}
