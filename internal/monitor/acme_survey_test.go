package monitor

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestParseACMESurvey(t *testing.T) {
	out := `apache /var/www/site
apache "/srv/quoted"
apache relative/path
caddyfile root * /srv/caddy
caddyfile 	root /var/www/{host}
caddyfile root
default /var/www/html
default /srv/caddy
ufw active blocked
curl
`
	s := ParseACMESurvey(out)
	if want := []string{"/var/www/site", "/srv/quoted", "/srv/caddy", "/var/www/html"}; !slices.Equal(s.Roots, want) {
		t.Fatalf("roots %q, want %q", s.Roots, want)
	}
	if s.Firewall != "ufw" || !s.Blocked || !s.Curl {
		t.Fatalf("survey %+v", s)
	}
	if s := ParseACMESurvey("firewalld running allowed\n"); s.Firewall != "firewalld" || s.Blocked {
		t.Fatalf("firewalld %+v", s)
	}
	if s := ParseACMESurvey(""); s.Firewall != "" || len(s.Roots) != 0 {
		t.Fatalf("nothing %+v", s)
	}
}

func TestCaddyServersAndRoute(t *testing.T) {
	out := `{"srv0":{"listen":[":443"],"routes":[{"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"vars","root":"/srv/www"},{"handler":"file_server"}]}]}]}]},
"srv1":{"listen":[":80"]},"bad name!":{"listen":[":81"]},
"srv2":{"listen":[":8080"],"routes":[{"handle":[{"handler":"file_server","root":"/var/www/{http.request.host}"}]}]}}`
	servers, roots, err := ParseCaddyServers(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 || servers[0].Name != "srv0" || !servers[0].Routes || servers[1].Routes || !servers[2].Routes {
		t.Fatalf("servers %+v", servers)
	}
	if !slices.Equal(roots, []string{"/srv/www"}) {
		t.Fatalf("roots %q", roots)
	}
	if _, _, err := ParseCaddyServers("not json"); err == nil {
		t.Fatal("bad JSON parsed")
	}

	target, body, err := ACMECaddyRoute(servers[0], CaddyRouteID(0))
	if err != nil || target != "/config/apps/http/servers/srv0/routes/0" || !json.Valid([]byte(body)) || !strings.HasPrefix(body, "{") {
		t.Fatalf("route into a list: %q %q %v", target, body, err)
	}
	var route struct {
		ID    string `json:"@id"`
		Match []struct {
			Path []string `json:"path"`
		} `json:"match"`
		Handle []struct {
			Handler, Root string
		} `json:"handle"`
	}
	if err := json.Unmarshal([]byte(body), &route); err != nil || route.ID != "finallyshell-acme-0" ||
		route.Match[0].Path[0] != "/.well-known/acme-challenge/*" || route.Handle[0].Root != ACMERoot {
		t.Fatalf("route %+v, %v", route, err)
	}
	target, body, err = ACMECaddyRoute(servers[1], CaddyRouteID(1))
	if err != nil || target != "/config/apps/http/servers/srv1/routes" || !strings.HasPrefix(body, "[") || !json.Valid([]byte(body)) {
		t.Fatalf("a new list: %q %q %v", target, body, err)
	}
	if _, _, err := ACMECaddyRoute(CaddyServer{Name: "../x"}, CaddyRouteID(0)); err == nil {
		t.Fatal("a bad server name passed")
	}
	if _, _, err := ACMECaddyRoute(servers[0], "other"); err == nil {
		t.Fatal("another ID passed")
	}
	if got, err := ACMECaddyRouteTarget("finallyshell-acme-12"); err != nil || got != "/id/finallyshell-acme-12" {
		t.Fatalf("delete target %q %v", got, err)
	}
	if _, err := ACMECaddyRouteTarget("finallyshell-acme-0/../x"); err == nil {
		t.Fatal("a bad ID passed")
	}
	if !caddyAdminPath("/id/finallyshell-acme-3") || !caddyAdminPath("/config/apps/http/servers/srv0/routes/0") {
		t.Fatal("the admin API helper refuses the route's paths")
	}
}

func TestACMEFirewallCommands(t *testing.T) {
	for _, fw := range []string{"ufw", "firewalld"} {
		if _, err := ACMEFirewallOpenCommand(fw); err != nil {
			t.Fatal(err)
		}
		if _, err := ACMEFirewallCloseCommand(fw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ACMEFirewallOpenCommand("iptables; rm -rf /"); err == nil {
		t.Fatal("an unknown firewall passed")
	}
	open, _ := ACMEFirewallOpenCommand("firewalld")
	if strings.Contains(open, "--permanent") || !strings.Contains(open, "--timeout=1800") {
		t.Fatalf("firewalld rule is not temporary: %s", open)
	}
}

func TestParsePort80Programs(t *testing.T) {
	out := `LISTEN 0 4096 0.0.0.0:80 0.0.0.0:* users:(("docker-proxy",pid=901,fd=4))
LISTEN 0 5 [::]:80 [::]:* users:(("python3",pid=77,fd=3),("docker-proxy",pid=902,fd=4))`
	if busy, programs := ParsePort80(out); !busy || !slices.Equal(programs, []string{"docker-proxy", "python3"}) {
		t.Fatalf("ss: %v %q", busy, programs)
	}
	if busy, programs := ParsePort80("tcp6 0 0 :::80 :::* LISTEN -\n"); !busy || len(programs) != 0 {
		t.Fatalf("netstat without programs: %v %q", busy, programs)
	}
}

func TestACMEPerlResponder(t *testing.T) {
	// It goes in single quotes.
	if strings.Contains(acmePerlResponder, "'") {
		t.Fatal("the Perl responder holds a single quote")
	}
	if !strings.Contains(ACMEResponderStartCommand, "'perl' '-e' 'use strict;") {
		t.Fatal("the Perl responder is not in the start command")
	}
}
