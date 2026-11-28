package monitor

import (
	"net"
	"strconv"
	"strings"
)

// The split of port 80: when another program holds port 80 and no folder
// of its serves the challenges, a short-lived proxy takes port 80's
// connections through a firewall REDIRECT rule, answers the ones that ask
// for a challenge file, and passes every other one, byte for byte, both
// ways, to the program on port 80. The proxy's own connections to port 80
// leave from the server itself, which PREROUTING does not see, so they are
// not redirected.
//
// Only connections to the server's own addresses are redirected, not the
// ones it forwards (a router's LAN, Docker's containers going out); the
// proxy's port is let in for redirected connections alone. A program on
// every address is reached at the server's own address, not the loopback,
// so that it never takes a visitor for a local client; one on the loopback
// alone, which no visitor reached before, is not passed anything.

// ACMESplitMark marks the firewall rules of the split, and names its nft
// table.
const (
	ACMESplitMark  = "finallyshell-acme"
	acmeSplitTable = "finallyshell_acme"
)

// acmeSplitProxy is the proxy, in Python 3: on a free port of every
// address, IPv4 and IPv6, it serves the challenge files of the folder it
// is given (argv[1]) to GET and HEAD requests for them, and passes every
// other connection to port 80 of argv[2], first line and all: an address,
// "local" for the server's own (its loopback when it has no other), or
// "none" to answer 404; it writes its port to the folder's file "port"
// once it listens. It exits when the upstream does not take a connection
// at start.
const acmeSplitProxy = `import asyncio, os, re, socket, sys
DIR, UP = sys.argv[1], sys.argv[2]
def own():
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.connect(("192.0.2.1", 80))
        a = s.getsockname()[0]
        s.close()
        if a and not a.startswith("127."):
            return a
    except OSError:
        pass
    return "127.0.0.1"
if UP == "local":
    UP = own()
TOKEN = re.compile(rb"^(GET|HEAD) /\.well-known/acme-challenge/([A-Za-z0-9_-]{1,128})(\?\S*)? HTTP/1\.[01]$")
async def pipe(r, w):
    try:
        while True:
            b = await r.read(65536)
            if not b:
                break
            w.write(b)
            await w.drain()
    except Exception:
        pass
    finally:
        try:
            w.close()
        except Exception:
            pass
async def handle(r, w):
    try:
        line = await asyncio.wait_for(r.readline(), 15)
    except Exception:
        w.close()
        return
    m = TOKEN.match(line.rstrip(b"\r\n"))
    if m:
        path = os.path.join(DIR, ".well-known", "acme-challenge", m.group(2).decode())
        try:
            with open(path, "rb") as f:
                body = f.read()
        except OSError:
            body = None
        if body is not None:
            try:
                while True:
                    h = await asyncio.wait_for(r.readline(), 5)
                    if h in (b"\r\n", b"\n", b""):
                        break
            except Exception:
                pass
            head = b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n" % len(body)
            w.write(head + (b"" if m.group(1) == b"HEAD" else body))
            try:
                await w.drain()
            except Exception:
                pass
            w.close()
            return
    if UP == "none":
        w.write(b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
        try:
            await w.drain()
        except Exception:
            pass
        w.close()
        return
    try:
        ur, uw = await asyncio.wait_for(asyncio.open_connection(UP, 80), 10)
    except Exception:
        w.close()
        return
    uw.write(line)
    await asyncio.gather(pipe(r, uw), pipe(ur, w))
async def main():
    if UP != "none":
        s = socket.create_connection((UP, 80), 3)
        s.close()
    s4 = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s4.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s4.bind(("0.0.0.0", 0))
    port = s4.getsockname()[1]
    socks = [s4]
    try:
        s6 = socket.socket(socket.AF_INET6, socket.SOCK_STREAM)
        s6.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
        s6.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s6.bind(("::", port))
        socks.append(s6)
    except OSError:
        pass
    servers = []
    for s in socks:
        s.listen(128)
        servers.append(await asyncio.start_server(handle, sock=s))
    with open(os.path.join(DIR, "port"), "w") as f:
        f.write(str(port))
    await asyncio.gather(*(s.serve_forever() for s in servers))
asyncio.run(main())
`

// ACMESplitStartCommand starts the split's proxy, for 30 minutes at most,
// passing other connections to port 80 of upstream: an IP address,
// "local" or "none" (ParsePort80Upstream); run as root. It prints "started <pid> <folder> <port>", or "none" without
// Python 3, or "failed" (the reason on stderr).
func ACMESplitStartCommand(upstream string) (string, error) {
	if upstream != "local" && upstream != "none" && net.ParseIP(upstream) == nil {
		return "", errACMEInput
	}
	return `set -eu
command -v python3 >/dev/null 2>&1 || { 'echo' 'none'; exit 0; }
dir=$('mktemp' '-d' '/tmp/finallyshell-acme.XXXXXXXX')
'chmod' '755' "$dir"
'mkdir' '-p' "$dir/.well-known/acme-challenge"
'chmod' '755' "$dir/.well-known" "$dir/.well-known/acme-challenge"
cd /
# Not a shell function: see ACMEResponderStartCommand.
if command -v setsid >/dev/null 2>&1; then set -- 'setsid'; else set --; fi
"$@" 'nohup' 'timeout' '1800' 'python3' '-c' ` + privilegeQuote(acmeSplitProxy) + ` "$dir" ` + privilegeQuote(upstream) + ` >/dev/null 2>"$dir/log" </dev/null &
pid=$!
i=0
while [ ! -s "$dir/port" ] && [ "$i" -lt 50 ] && 'kill' '-0' "$pid" 2>/dev/null; do 'sleep' '0.1'; i=$((i + 1)); done
if 'kill' '-0' "$pid" 2>/dev/null && [ -s "$dir/port" ]; then
    'printf' 'started %s %s %s\n' "$pid" "$dir" "$('cat' "$dir/port")"
else
    'tail' '-n' '3' "$dir/log" >&2 2>/dev/null || :
    'kill' "$pid" 2>/dev/null || :
    'rm' '-rf' "$dir"
    'echo' 'failed'
fi
`, nil
}

// ParseACMESplit reads what ACMESplitStartCommand printed.
func ParseACMESplit(out string) (pid int, dir string, port int, ok bool) {
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) != 4 || f[0] != "started" {
		return 0, "", 0, false
	}
	pid, err := strconv.Atoi(f[1])
	if err != nil || pid <= 1 || !acmeTempDir.MatchString(f[2]) {
		return 0, "", 0, false
	}
	port, err = strconv.Atoi(f[3])
	if err != nil || !acmeSplitPortOK(port) {
		return 0, "", 0, false
	}
	return pid, f[2], port, true
}

func acmeSplitPortOK(port int) bool { return port >= 1024 && port <= 65535 }

// ACMESplitRulesCommand redirects port 80's incoming connections to port,
// the proxy's, through iptables (and ip6tables, when it can), else nft in
// a table of its own, letting port in through the filter too, and through
// firewalld for a while when it runs. It prints "rules <tools…>", or
// "none" when the server has neither.
func ACMESplitRulesCommand(port int) (string, error) {
	if !acmeSplitPortOK(port) {
		return "", errACMEInput
	}
	p := strconv.Itoa(port)
	mark := "'-m' 'comment' '--comment' '" + ACMESplitMark + "'"
	// Connections to the server's own addresses, not the ones it forwards;
	// and the proxy's port let in for them alone.
	local := "'-m' 'addrtype' '--dst-type' 'LOCAL'"
	redirected := "'-m' 'conntrack' '--ctstate' 'DNAT'"
	return NginxToolPath + `set -eu
tools=''
if command -v iptables >/dev/null 2>&1 && 'iptables' '-t' 'nat' '-L' 'PREROUTING' '-n' >/dev/null 2>&1; then
    'iptables' '-t' 'nat' '-I' 'PREROUTING' '1' '-p' 'tcp' '--dport' '80' ` + local + ` ` + mark + ` '-j' 'REDIRECT' '--to-ports' '` + p + `'
    'iptables' '-I' 'INPUT' '1' '-p' 'tcp' '--dport' '` + p + `' ` + redirected + ` ` + mark + ` '-j' 'ACCEPT' || :
    tools='iptables'
    if command -v ip6tables >/dev/null 2>&1 && 'ip6tables' '-t' 'nat' '-L' 'PREROUTING' '-n' >/dev/null 2>&1; then
        if 'ip6tables' '-t' 'nat' '-I' 'PREROUTING' '1' '-p' 'tcp' '--dport' '80' ` + local + ` ` + mark + ` '-j' 'REDIRECT' '--to-ports' '` + p + `'; then
            'ip6tables' '-I' 'INPUT' '1' '-p' 'tcp' '--dport' '` + p + `' ` + redirected + ` ` + mark + ` '-j' 'ACCEPT' || :
            tools="$tools ip6tables"
        fi
    fi
elif command -v nft >/dev/null 2>&1; then
    'nft' 'add' 'table' 'inet' '` + acmeSplitTable + `'
    'nft' 'add' 'chain' 'inet' '` + acmeSplitTable + `' 'prerouting' '{ type nat hook prerouting priority -110; }'
    'nft' 'add' 'rule' 'inet' '` + acmeSplitTable + `' 'prerouting' 'fib' 'daddr' 'type' 'local' 'tcp' 'dport' '80' 'redirect' 'to' ':` + p + `'
    tools='nft'
else
    'echo' 'none'
    exit 0
fi
if command -v firewall-cmd >/dev/null 2>&1 && 'firewall-cmd' '--state' >/dev/null 2>&1; then
    'firewall-cmd' '--add-port=` + p + `/tcp' '--timeout=1800' >/dev/null 2>&1 && tools="$tools firewalld" || :
fi
'printf' 'rules %s\n' "$tools"
`, nil
}

// ACMESplitStopCommand undoes the split whose proxy is pid, in dir, on
// port: its rules first, wherever they went, then the proxy, then its
// folder, each step whatever the one before did. It prints "left <n>"
// with the number of the split's rules still there.
func ACMESplitStopCommand(pid int, dir string, port int) (string, error) {
	if pid <= 1 || !acmeTempDir.MatchString(dir) || !acmeSplitPortOK(port) {
		return "", errACMEInput
	}
	p := strconv.Itoa(port)
	mark := "'-m' 'comment' '--comment' '" + ACMESplitMark + "'"
	local := "'-m' 'addrtype' '--dst-type' 'LOCAL'"
	redirected := "'-m' 'conntrack' '--ctstate' 'DNAT'"
	return NginxToolPath + `for t in iptables ip6tables; do
    command -v "$t" >/dev/null 2>&1 || continue
    n=0
    while [ "$n" -lt 20 ] && "$t" '-t' 'nat' '-D' 'PREROUTING' '-p' 'tcp' '--dport' '80' ` + local + ` ` + mark + ` '-j' 'REDIRECT' '--to-ports' '` + p + `' 2>/dev/null; do n=$((n + 1)); done
    n=0
    while [ "$n" -lt 20 ] && "$t" '-D' 'INPUT' '-p' 'tcp' '--dport' '` + p + `' ` + redirected + ` ` + mark + ` '-j' 'ACCEPT' 2>/dev/null; do n=$((n + 1)); done
done
if command -v nft >/dev/null 2>&1; then 'nft' 'delete' 'table' 'inet' '` + acmeSplitTable + `' 2>/dev/null || :; fi
if command -v firewall-cmd >/dev/null 2>&1; then 'firewall-cmd' '--remove-port=` + p + `/tcp' >/dev/null 2>&1 || :; fi
'kill' '` + strconv.Itoa(pid) + `' 2>/dev/null || :
'rm' '-rf' ` + privilegeQuote(dir) + `
left=0
for t in iptables ip6tables; do
    command -v "$t" >/dev/null 2>&1 || continue
    c=$("$t" '-t' 'nat' '-S' 'PREROUTING' 2>/dev/null | 'grep' '-c' '` + ACMESplitMark + `') || c=0
    left=$((left + c))
    c=$("$t" '-S' 'INPUT' 2>/dev/null | 'grep' '-c' '` + ACMESplitMark + `') || c=0
    left=$((left + c))
done
if command -v nft >/dev/null 2>&1 && 'nft' 'list' 'table' 'inet' '` + acmeSplitTable + `' >/dev/null 2>&1; then left=$((left + 1)); fi
'printf' 'left %s\n' "$left"
`, nil
}

// ParsePort80Upstream is where the split's proxy reaches the program on
// port 80, from ACMEPort80Command's lines: "local" (the server's own
// address) for one on every address, the first address it listens on
// otherwise, or "none" for one on the loopback alone, which no visitor
// reached; "" for no program.
func ParsePort80Upstream(out string) string {
	var first string
	loop := false
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		// ss -H: State Recv-Q Send-Q Local Peer…; netstat: Proto Recv-Q Send-Q Local…
		if len(f) < 4 {
			continue
		}
		addr := f[3]
		if rest, ok := strings.CutPrefix(addr, ":::"); ok { // netstat's IPv6 wildcard
			addr = "[::]:" + rest
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil || port != "80" {
			continue
		}
		host, _, _ = strings.Cut(host, "%")
		switch host {
		case "0.0.0.0", "*", "::", "[::]":
			return "local"
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		switch {
		case ip == nil:
		case ip.IsLoopback():
			loop = true
		case first == "" && !ip.IsLinkLocalUnicast():
			first = ip.String()
		}
	}
	if first == "" && loop {
		return "none"
	}
	return first
}
