package monitor

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The server's web servers and firewall, as the SSL certificates app
// looks at them before answering an HTTP challenge: the folders a site may
// serve on port 80 (Apache's, Caddy's, the usual ones), Caddy's admin API,
// and a firewall that may block port 80.

// ACMESurveyCommand prints, a line each: "apache <folder>" for Apache's
// DocumentRoot directives, "caddyfile <line>" for the root directives of
// the Caddyfiles in /etc/caddy, "default <folder>" for the usual web
// folders that exist, "ufw active allowed|blocked" and "firewalld running
// allowed|blocked" for a firewall that runs, "curl" and "python3" when
// they are there, and "redirect iptables|nft" for the tool a REDIRECT rule
// goes in; run as root.
const ACMESurveyCommand = NginxToolPath + `export LC_ALL=C
for f in /etc/apache2/apache2.conf /etc/apache2/sites-enabled/* /etc/apache2/conf-enabled/* /etc/httpd/conf/httpd.conf /etc/httpd/conf.d/*.conf; do
    [ -f "$f" ] || continue
    'sed' -n 's/^[[:space:]]*[Dd]ocument[Rr]oot[[:space:]][[:space:]]*"\{0,1\}\([^"[:space:]]*\)"\{0,1\}.*$/apache \1/p' "$f"
done
if [ -d /etc/caddy ]; then
    'grep' -rhE '^[[:space:]]*root[[:space:]]' /etc/caddy 2>/dev/null | 'sed' 's/^/caddyfile /' | 'head' -n 50
fi
for d in /var/www/html /usr/share/nginx/html /srv/www /var/www /srv/http /usr/share/caddy; do
    [ -d "$d" ] && [ ! -L "$d" ] && 'printf' 'default %s\n' "$d"
done
if command -v ufw >/dev/null 2>&1; then
    st=$('ufw' 'status' 'verbose' 2>/dev/null) || st=''
    case "$st" in *'Status: active'*)
        if 'printf' '%s\n' "$st" | 'grep' -Eqi '^Default:.*allow \(incoming\)' ||
            'printf' '%s\n' "$st" | 'grep' -Eqi '^(80|80/tcp|www|www full|apache|apache full|nginx http|nginx full)[[:space:]].*ALLOW'; then
            'echo' 'ufw active allowed'
        else 'echo' 'ufw active blocked'; fi;;
    esac
fi
if command -v firewall-cmd >/dev/null 2>&1 && 'firewall-cmd' '--state' >/dev/null 2>&1; then
    if 'firewall-cmd' '--query-port=80/tcp' >/dev/null 2>&1 || 'firewall-cmd' '--query-service=http' >/dev/null 2>&1; then
        'echo' 'firewalld running allowed'
    else 'echo' 'firewalld running blocked'; fi
fi
if command -v curl >/dev/null 2>&1; then 'echo' 'curl'; fi
if command -v python3 >/dev/null 2>&1; then 'echo' 'python3'; fi
if command -v iptables >/dev/null 2>&1 && 'iptables' '-t' 'nat' '-L' 'PREROUTING' '-n' >/dev/null 2>&1; then 'echo' 'redirect iptables'
elif command -v nft >/dev/null 2>&1; then 'echo' 'redirect nft'; fi
`

// ACMESurvey is what ACMESurveyCommand found.
type ACMESurvey struct {
	// Roots are the folders sites may serve, Apache's and Caddy's first,
	// then the usual ones, each once.
	Roots []string
	// Firewall is "ufw" or "firewalld" when one runs, and Blocked when it
	// does not let port 80 in.
	Firewall string
	Blocked  bool
	Curl     bool
	// Python3 and Redirect ("iptables", "nft" or "") say whether the split
	// of port 80 can run.
	Python3  bool
	Redirect string
}

// ParseACMESurvey reads what ACMESurveyCommand printed.
func ParseACMESurvey(out string) ACMESurvey {
	var s ACMESurvey
	var defaults []string
	add := func(list *[]string, dir string) {
		dir = strings.Trim(dir, `"'`)
		if ValidNginxPath(dir) && path.Clean(dir) == dir && !slices.Contains(*list, dir) && !slices.Contains(s.Roots, dir) {
			*list = append(*list, dir)
		}
	}
	for line := range strings.SplitSeq(out, "\n") {
		kind, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch kind {
		case "apache":
			add(&s.Roots, rest)
		case "caddyfile":
			// root [matcher] <path>: the last word, without placeholders.
			f := strings.Fields(rest)
			if len(f) >= 2 && f[0] == "root" && !strings.Contains(f[len(f)-1], "{") {
				add(&s.Roots, f[len(f)-1])
			}
		case "default":
			add(&defaults, rest)
		case "ufw", "firewalld":
			s.Firewall = kind
			s.Blocked = strings.HasSuffix(rest, "blocked")
		case "curl":
			s.Curl = true
		case "python3":
			s.Python3 = true
		case "redirect":
			if rest == "iptables" || rest == "nft" {
				s.Redirect = rest
			}
		}
	}
	for _, d := range defaults {
		if !slices.Contains(s.Roots, d) {
			s.Roots = append(s.Roots, d)
		}
	}
	return s
}

// Caddy's admin API (CaddyAdmin), which a temporary route for the
// challenges goes through: nothing in Caddy's files changes, and a restart
// of Caddy drops it.

// ACMECaddyServers is the admin API's path of Caddy's HTTP servers.
const ACMECaddyServers = "/config/apps/http/servers"

// CaddyServer is a server of Caddy's HTTP app.
type CaddyServer struct {
	Name   string
	Listen []string
	// Routes says whether it has a routes list, which a route is put at the
	// start of; else the list is made.
	Routes bool
}

var (
	caddyServerName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	caddyRouteID    = regexp.MustCompile(`^finallyshell-acme-[0-9]{1,3}$`)
)

// ParseCaddyServers reads the servers of ACMECaddyServersCommand, sorted
// by name, and the folders their file servers serve (without
// placeholders).
func ParseCaddyServers(out string) ([]CaddyServer, []string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return nil, nil, err
	}
	var servers []CaddyServer
	var roots []string
	for name, data := range raw {
		if !caddyServerName.MatchString(name) {
			continue
		}
		var srv struct {
			Listen []string          `json:"listen"`
			Routes []json.RawMessage `json:"routes"`
		}
		if json.Unmarshal(data, &srv) != nil {
			continue
		}
		servers = append(servers, CaddyServer{Name: name, Listen: srv.Listen, Routes: srv.Routes != nil})
		var tree any
		if json.Unmarshal(data, &tree) == nil {
			caddyRoots(tree, &roots)
		}
	}
	slices.SortFunc(servers, func(a, b CaddyServer) int { return strings.Compare(a.Name, b.Name) })
	return servers, roots, nil
}

// caddyRoots adds the roots of the file_server and vars handlers below v.
func caddyRoots(v any, roots *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if h, _ := t["handler"].(string); h == "file_server" || h == "vars" {
			if r, ok := t["root"].(string); ok && !strings.Contains(r, "{") && ValidNginxPath(r) && !slices.Contains(*roots, r) {
				*roots = append(*roots, r)
			}
		}
		for _, c := range t {
			caddyRoots(c, roots)
		}
	case []any:
		for _, c := range t {
			caddyRoots(c, roots)
		}
	}
}

// CaddyRouteID is the @id of the challenge route put in the n-th server.
func CaddyRouteID(n int) string { return "finallyshell-acme-" + strconv.Itoa(n) }

// ACMECaddyRoute is the admin API request that puts the route id, which
// serves the challenges from ACMERoot and nothing else, at the start of
// server's routes: PUT target with body.
func ACMECaddyRoute(server CaddyServer, id string) (target, body string, err error) {
	if !caddyServerName.MatchString(server.Name) || !caddyRouteID.MatchString(id) {
		return "", "", errACMEInput
	}
	route := `{"@id":"` + id + `","match":[{"path":["` + ACMEPath + `*"]}],"handle":[{"handler":"file_server","root":"` + ACMERoot + `"}],"terminal":true}`
	target = ACMECaddyServers + "/" + server.Name + "/routes"
	if server.Routes {
		return target + "/0", route, nil // a PUT at an index inserts there
	}
	return target, "[" + route + "]", nil
}

// ACMECaddyRouteTarget is the admin API path that DELETE takes the route id
// out at.
func ACMECaddyRouteTarget(id string) (string, error) {
	if !caddyRouteID.MatchString(id) {
		return "", errACMEInput
	}
	return "/id/" + id, nil
}

// Opening port 80 in a firewall for a while: ufw's rule until it is
// deleted, firewalld's runtime rule, which expires on its own after 30
// minutes and on a reload.

// ACMEFirewallOpenCommand lets port 80 in through firewall, "ufw" or
// "firewalld". It prints "opened", or "existing" when the rule was there.
func ACMEFirewallOpenCommand(firewall string) (string, error) {
	switch firewall {
	case "ufw":
		return NginxToolPath + `out=$('ufw' 'allow' '80/tcp' 2>&1) || { 'printf' '%s\n' "$out" >&2; exit 1; }
case "$out" in *Skipping*) 'echo' 'existing';; *) 'echo' 'opened';; esac
`, nil
	case "firewalld":
		return NginxToolPath + `if 'firewall-cmd' '--query-port=80/tcp' >/dev/null 2>&1; then 'echo' 'existing'
else 'firewall-cmd' '--add-port=80/tcp' '--timeout=1800' >/dev/null && 'echo' 'opened'; fi
`, nil
	}
	return "", errACMEInput
}

// ACMEFirewallCloseCommand takes the rule ACMEFirewallOpenCommand added out.
func ACMEFirewallCloseCommand(firewall string) (string, error) {
	switch firewall {
	case "ufw":
		return NginxToolPath + "'ufw' '--force' 'delete' 'allow' '80/tcp' >/dev/null\n", nil
	case "firewalld":
		return NginxToolPath + "'firewall-cmd' '--remove-port=80/tcp' >/dev/null 2>&1 || :\n", nil
	}
	return "", errACMEInput
}
