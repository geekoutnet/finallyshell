package monitor

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The files and web server that answer Let's Encrypt's HTTP challenge
// (http://<domain>/.well-known/acme-challenge/<token>) from a server. A
// file goes in a site's document root, or in a folder a temporary
// responder on port 80 or a temporary Nginx location serves.

// ACMEPath is where a challenge file sits below the folder served.
const ACMEPath = "/.well-known/acme-challenge/"

// ACMERoot is the folder a temporary Nginx location serves challenges
// from.
const ACMERoot = "/var/lib/finallyshell-acme"

// ACMELocationMark marks the location the app adds to a server block, and
// removes afterwards.
const ACMELocationMark = "# finallyshell-acme"

var (
	acmeToken     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	acmeKeyAuth   = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}\.[A-Za-z0-9_-]{8,128}$`)
	acmeTempDir   = regexp.MustCompile(`^/tmp/finallyshell-acme\.[A-Za-z0-9]{8}$`)
	nginxServerAt = regexp.MustCompile(`^\s*server\s*\{\s*(#.*)?$`)
	errACMEInput  = errors.New("invalid ACME challenge input")
)

// ACMEWriteCommand writes keyAuth as the challenge file token below root,
// making the folders it needs; run as root.
func ACMEWriteCommand(root, token, keyAuth string) (string, error) {
	if !ValidNginxPath(root) || !acmeToken.MatchString(token) || !acmeKeyAuth.MatchString(keyAuth) {
		return "", errACMEInput
	}
	return "set -eu\nroot=" + privilegeQuote(root) + `
[ -d "$root" ] && [ ! -L "$root" ]
for d in "$root/.well-known" "$root/.well-known/acme-challenge"; do
    [ ! -L "$d" ] || exit 1
    [ -d "$d" ] || { 'mkdir' "$d"; 'chmod' '755' "$d"; }
done
f="$root/.well-known/acme-challenge/"` + token + `
[ ! -L "$f" ] || exit 1
'printf' '%s' ` + privilegeQuote(keyAuth) + ` >"$f"
'chmod' '644' "$f"
`, nil
}

// ACMERemoveCommand removes the challenge file token below root, and its
// folders once empty.
func ACMERemoveCommand(root, token string) (string, error) {
	if !ValidNginxPath(root) || !acmeToken.MatchString(token) {
		return "", errACMEInput
	}
	return "root=" + privilegeQuote(root) + `
'rm' '-f' "$root/.well-known/acme-challenge/` + token + `"
'rmdir' "$root/.well-known/acme-challenge" 2>/dev/null || :
'rmdir' "$root/.well-known" 2>/dev/null || :
`, nil
}

// ACMEPort80Command lists what listens on TCP port 80, a line each, with
// the programs when the tool tells them: nothing for a free port.
const ACMEPort80Command = `if command -v ss >/dev/null 2>&1; then 'ss' '-Hltnp' 'sport = :80'
else 'netstat' '-ltnp' 2>/dev/null | 'awk' '$4 ~ /[:.]80$/'; fi
`

// ParsePort80 says whether something listens on port 80, and which known
// web servers do.
func ParsePort80(out string) (busy bool, servers []string) {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		busy = true
		for _, name := range []string{"nginx", "caddy", "apache2", "httpd", "lighttpd", "traefik", "haproxy", "docker-proxy"} {
			if strings.Contains(line, `"`+name+`"`) || strings.Contains(line, "/"+name) {
				if !slices.Contains(servers, name) {
					servers = append(servers, name)
				}
			}
		}
	}
	return busy, servers
}

// ACMEResponderStartCommand starts a web server on port 80 that serves a
// new folder below /tmp, for 30 minutes at most, so that it never stays
// if the app goes; run as root. It prints "started <pid> <folder>", or
// "none" when the server has neither Python 3 nor BusyBox's httpd, or
// "failed".
const ACMEResponderStartCommand = `set -eu
dir=$('mktemp' '-d' '/tmp/finallyshell-acme.XXXXXXXX')
'chmod' '755' "$dir"
'mkdir' '-p' "$dir/.well-known/acme-challenge"
'chmod' '755' "$dir/.well-known" "$dir/.well-known/acme-challenge"
cd "$dir"
# Not a shell function: dash does not pass a backgrounded function call's
# redirections to its commands, and the SSH channel then stays open.
if command -v setsid >/dev/null 2>&1; then set -- 'setsid'; else set --; fi
if command -v python3 >/dev/null 2>&1; then
    "$@" 'nohup' 'timeout' '1800' 'python3' '-m' 'http.server' '80' >/dev/null 2>&1 </dev/null &
elif command -v busybox >/dev/null 2>&1 && 'busybox' '--list' 2>/dev/null | 'grep' '-qx' 'httpd'; then
    "$@" 'nohup' 'timeout' '1800' 'busybox' 'httpd' '-f' '-p' '80' '-h' "$dir" >/dev/null 2>&1 </dev/null &
else
    cd /; 'rm' '-rf' "$dir"; 'echo' 'none'; exit 0
fi
pid=$!
'sleep' '1'
if 'kill' '-0' "$pid" 2>/dev/null; then 'printf' 'started %s %s\n' "$pid" "$dir"
else cd /; 'rm' '-rf' "$dir"; 'echo' 'failed'; fi
`

// ParseACMEResponder reads what ACMEResponderStartCommand printed: the
// responder's process and folder, or ok false.
func ParseACMEResponder(out string) (pid int, dir string, ok bool) {
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) != 3 || f[0] != "started" {
		return 0, "", false
	}
	pid, err := strconv.Atoi(f[1])
	if err != nil || pid <= 1 || !acmeTempDir.MatchString(f[2]) {
		return 0, "", false
	}
	return pid, f[2], true
}

// ACMEResponderStopCommand stops the responder pid and removes its folder.
func ACMEResponderStopCommand(pid int, dir string) (string, error) {
	if pid <= 1 || !acmeTempDir.MatchString(dir) {
		return "", errACMEInput
	}
	return "'kill' " + strconv.Itoa(pid) + " 2>/dev/null || :\n'rm' '-rf' " + privilegeQuote(dir) + "\n", nil
}

// ACMERootCommand makes ACMERoot, which a temporary Nginx location
// serves.
const ACMERootCommand = "set -eu\n'mkdir' '-p' '" + ACMERoot + "'\n'chmod' '755' '" + ACMERoot + "'\n"

// NginxACMELocation is text with a location serving the challenges from
// ACMERoot added to the server blocks that open on lines (1-based), each
// one on a line of its own, as "server {": an error for another.
func NginxACMELocation(text string, lines []int) (string, error) {
	rows := strings.Split(text, "\n")
	seen := map[int]bool{}
	for _, n := range lines {
		if n < 1 || n > len(rows) || seen[n] || !nginxServerAt.MatchString(strings.TrimSuffix(rows[n-1], "\r")) {
			return "", errACMEInput
		}
		seen[n] = true
	}
	if strings.Contains(text, ACMELocationMark) {
		return "", errACMEInput // one left behind: the file is restored first
	}
	var out []string
	for i, row := range rows {
		out = append(out, row)
		if seen[i+1] {
			indent := row[:len(row)-len(strings.TrimLeft(row, " \t"))] + "    "
			out = append(out,
				indent+"location ^~ "+ACMEPath+" { "+ACMELocationMark,
				indent+"    root "+ACMERoot+";",
				indent+"    default_type text/plain;",
				indent+"}")
		}
	}
	return strings.Join(out, "\n"), nil
}
