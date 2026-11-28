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
// responder on port 80, a temporary Caddy route or the split of port 80
// (acme_split.go) serves.

// ACMEPath is where a challenge file sits below the folder served.
const ACMEPath = "/.well-known/acme-challenge/"

// ACMERoot is the folder a temporary Caddy route serves challenges from.
const ACMERoot = "/var/lib/finallyshell-acme"

var (
	acmeToken    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	acmeKeyAuth  = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}\.[A-Za-z0-9_-]{8,128}$`)
	acmeTempDir  = regexp.MustCompile(`^/tmp/finallyshell-acme\.[A-Za-z0-9]{8}$`)
	errACMEInput = errors.New("invalid ACME challenge input")
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

// ParsePort80 says whether something listens on port 80, and the names
// of the programs that do, when the tool tells them: ss's
// users:(("nginx",pid=…)), netstat's "812/nginx".
func ParsePort80(out string) (busy bool, programs []string) {
	add := func(name string) {
		if name != "" && name != "-" && !slices.Contains(programs, name) {
			programs = append(programs, name)
		}
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		busy = true
		if m := ssPrograms.FindAllStringSubmatch(line, -1); m != nil {
			for _, p := range m {
				add(p[1])
			}
			continue
		}
		f := strings.Fields(line)
		if _, name, ok := strings.Cut(f[len(f)-1], "/"); ok && strings.Contains(line, "LISTEN") {
			add(name)
		}
	}
	return busy, programs
}

var ssPrograms = regexp.MustCompile(`\("([^"]{1,64})",pid=`)

// ACMEResponderStartCommand starts a web server on port 80 that serves a
// new folder below /tmp, for 30 minutes at most, so that it never stays
// if the app goes; run as root: Python 3's, else BusyBox's httpd, else a
// Perl one that serves the challenge files alone. It prints "started
// <pid> <folder>", or "none" when the server has none of them, or
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
elif command -v perl >/dev/null 2>&1 && 'perl' '-MIO::Socket::INET' '-e' '1' 2>/dev/null; then
    "$@" 'nohup' 'timeout' '1800' 'perl' '-e' '` + acmePerlResponder + `' "$dir" >/dev/null 2>&1 </dev/null &
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

// ACMERootCommand makes ACMERoot, which a temporary Caddy route serves.
const ACMERootCommand = "set -eu\n'mkdir' '-p' '" + ACMERoot + "'\n'chmod' '755' '" + ACMERoot + "'\n"

// acmePerlResponder is a web server in Perl, for servers with neither
// Python 3 nor BusyBox: on port 80, it serves the files of the folder it
// is given below /.well-known/acme-challenge/ alone, to GET and HEAD, one
// request at a time, each within 5 seconds. It holds no single quote, as
// it goes in one.
const acmePerlResponder = `use strict; use IO::Socket::INET;
my $d = shift;
my $s = IO::Socket::INET->new(LocalAddr => "0.0.0.0", LocalPort => 80, Listen => 16, ReuseAddr => 1, Proto => "tcp") or die "listen: $!";
$SIG{PIPE} = "IGNORE";
while (my $c = $s->accept) {
    eval {
        local $SIG{ALRM} = sub { die "timeout\n" };
        alarm 5;
        my $l = <$c>;
        $l = "" unless defined $l;
        while (my $h = <$c>) { last if $h =~ /^\r?\n$/ }
        my ($m, $p) = $l =~ m{^(GET|HEAD) (\S+)};
        my $b;
        if (defined $p && $p =~ m{^/\.well-known/acme-challenge/([A-Za-z0-9_-]+)$} && open(my $f, "<", "$d/.well-known/acme-challenge/$1")) {
            local $/; $b = <$f>; close $f;
        }
        if (defined $b) {
            print $c "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: " . length($b) . "\r\nConnection: close\r\n\r\n" . ($m eq "HEAD" ? "" : $b);
        } else {
            print $c "HTTP/1.0 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n";
        }
        alarm 0;
    };
    close $c;
}
`
