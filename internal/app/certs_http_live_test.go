package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
)

// TestCertHTTPLive serves HTTP challenges from the test box in every way,
// as root, and checks that what ran there before goes on: a responder on
// the free port 80; an Nginx site's document root; the split of port 80
// in front of a proxy-only Nginx site, of a Docker-like published port
// (socat and a DNAT rule) and of an unknown program; Caddy's folder and a
// temporary route through its admin API; Apache's folder and the split in
// front of it; the Perl responder; ufw blocking port 80, opened once
// agreed. The box publishes its port 80 on TESTBOX_HTTP (127.0.0.1:8080),
// where the probes go with the domain as the host. Everything is undone
// at the end; only the processes the test starts are stopped, by PID.
func TestCertHTTPLive(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE not set")
	}
	a, _, s := connectTestServer(t)
	t.Cleanup(a.CloseAll)
	run := a.sitesRoot(s)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	// cur is the test the helpers fail: the subtest that runs.
	cur := t
	sh := func(script string) string {
		cur.Helper()
		out, stderr, err := run(ctx, script, nil, nil)
		if err != nil {
			cur.Fatalf("%s: %v: %s", script, err, stderr)
		}
		return out
	}
	addr := os.Getenv("TESTBOX_HTTP")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	// get reads url through the box's published port, with its host.
	get := func(ctx context.Context, raw string) (string, int, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", 0, err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+u.RequestURI(), nil)
		req.Host = u.Host
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return "", 0, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), resp.StatusCode, nil
	}
	fetch := func(ctx context.Context, raw string) (string, error) {
		body, status, err := get(ctx, raw)
		if err == nil && status != http.StatusOK {
			err = fmt.Errorf("HTTP %d: %.200q", status, body)
		}
		if err != nil && os.Getenv("CERT_DEBUG") != "" && strings.Contains(raw, "proxy.example.test") {
			out, _, _ := run(context.Background(), `iptables -t nat -S PREROUTING; echo ---; iptables -S INPUT | head -8; echo ---; ss -ltnp; echo ---; ps -eo pid,args | grep -e python3 -e nginx | grep -v grep; echo ---; ls -la /tmp/finallyshell-acme.*/.well-known/acme-challenge/ 2>&1; cat /tmp/finallyshell-acme.*/log 2>/dev/null`, nil, nil)
			cur.Logf("DEBUG %s:\n%s", raw, out)
		}
		return body, err
	}
	// page checks that the program behind port 80 answers host's page.
	page := func(host, want string, status int) {
		cur.Helper()
		body, got, err := get(ctx, "http://"+host+"/")
		if err != nil || got != status || !strings.Contains(body, want) {
			cur.Fatalf("%s: HTTP %d %q, %v; want %d with %q", host, got, body, err, status, want)
		}
	}
	// serves checks that h answers a challenge of domain, and no longer
	// once it is cleaned up.
	serves := func(h *certHTTP, domain string) {
		cur.Helper()
		token, keyAuth := certRandom(), certRandom()+"."+certRandom()
		if err := h.Present(domain, token, keyAuth); err != nil {
			cur.Fatal(err)
		}
		if got, err := fetch(ctx, "http://"+domain+monitor.ACMEPath+token); err != nil || got != keyAuth {
			cur.Fatalf("%s: challenge %q, %v", domain, got, err)
		}
		if err := h.CleanUp(domain, token, keyAuth); err != nil {
			cur.Fatal(err)
		}
		if _, err := fetch(ctx, "http://"+domain+monitor.ACMEPath+token); err == nil {
			cur.Fatalf("%s: the challenge is still served", domain)
		}
	}
	// split applies found's split, checks it answers domain's challenges
	// and passes its page on, then undoes it.
	split := func(found certHTTPFound, domain, want string, status int) {
		cur.Helper()
		if found.h != nil || found.confirm == nil || found.confirm.kind != certConfirmSplit || found.confirm.target != "iptables" {
			cur.Fatalf("%s: %+v", domain, found)
		}
		h, err := found.confirm.apply(ctx)
		if err != nil {
			cur.Fatal(err)
		}
		if h.mode != certHTTPSplit {
			cur.Fatalf("mode %d", h.mode)
		}
		serves(h, domain)
		page(domain, want, status) // through the split
		if err := h.close(); err != nil {
			cur.Fatal(err)
		}
		if n := strings.Count(sh("iptables -t nat -S; iptables -S; ip6tables -t nat -S"), monitor.ACMESplitMark); n != 0 {
			cur.Fatalf("%d of the split's rules are left", n)
		}
		if out := sh("ls -d /tmp/finallyshell-acme.* 2>/dev/null || :"); strings.TrimSpace(out) != "" {
			cur.Fatalf("left %q", out)
		}
		page(domain, want, status) // straight
	}
	// start runs script in the background on the box, for the test alone,
	// and gives its PID, stopped at the end.
	var pids []int
	start := func(script string) {
		cur.Helper()
		out := sh("setsid sh -c " + strconv.Quote(script) + " >/dev/null 2>&1 </dev/null & echo $!; sleep 1")
		pid, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || pid <= 1 {
			cur.Fatalf("started %q", out)
		}
		pids = append(pids, pid)
	}
	stopStarted := func() {
		for _, pid := range pids {
			// The process group setsid made: the shell and its server.
			run(context.Background(), "kill -- -"+strconv.Itoa(pid)+" 2>/dev/null || kill "+strconv.Itoa(pid)+" 2>/dev/null || :", nil, nil)
		}
		pids = nil
		run(context.Background(), "sleep 1", nil, nil)
	}
	const reset = `for u in nginx caddy apache2; do systemctl stop "$u" 2>/dev/null || :; done
[ -x /usr/bin/python3.cert-test ] && mv /usr/bin/python3.cert-test /usr/bin/python3 || :
[ -x /usr/sbin/nginx.cert-test ] && mv /usr/sbin/nginx.cert-test /usr/sbin/nginx || :
[ -x /usr/bin/caddy.cert-test ] && mv /usr/bin/caddy.cert-test /usr/bin/caddy || :
[ -L /etc/nginx/cert-test-default.link ] && mv /etc/nginx/cert-test-default.link /etc/nginx/sites-enabled/default || :
ufw --force reset >/dev/null 2>&1 || :; ufw disable >/dev/null 2>&1 || :
for t in iptables ip6tables; do
    $t -t nat -S PREROUTING | grep -e finallyshell-acme -e cert-test | sed 's/^-A/-D/' | while read -r r; do eval "$t -t nat $r" || :; done
    $t -S INPUT | grep finallyshell-acme | sed 's/^-A/-D/' | while read -r r; do eval "$t $r" || :; done
done
rm -f /etc/nginx/sites-enabled/cert-* /etc/nginx/sites-available/cert-* /etc/caddy/sites/cert-test.caddy
rm -f /etc/apache2/sites-enabled/cert-* /etc/apache2/sites-available/cert-*
rm -rf /var/www/certweb /srv/cert-* /tmp/finallyshell-acme.* ` + monitor.ACMERoot + "\n"
	sh(reset)
	t.Cleanup(func() {
		stopStarted()
		run(context.Background(), reset, nil, nil)
	})
	free := func() {
		cur.Helper()
		stopStarted()
		sh(reset)
		if busy, programs := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); busy {
			cur.Fatalf("port 80 still held by %q", programs)
		}
	}

	t.Run("responder", func(t *testing.T) {
		cur = t
		free()
		found := certFindHTTP(ctx, run, []string{"free.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPResponder {
			t.Fatalf("%+v", found)
		}
		serves(found.h, "free.example.test")
		if err := found.h.close(); err != nil {
			t.Fatal(err)
		}
		if busy, _ := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); busy {
			t.Fatal("the responder still runs")
		}
	})

	t.Run("nginx", func(t *testing.T) {
		cur = t
		free()
		// A backend the proxy-only site passes to.
		sh("mkdir -p /srv/cert-backend && echo nginx-backend >/srv/cert-backend/index.html")
		start("cd /srv/cert-backend && exec python3 -m http.server 9 --bind 127.0.0.1")
		sh(`mkdir -p /var/www/certweb
cat >/etc/nginx/sites-available/cert-web <<'EOF'
server {
    listen 80;
    server_name web.example.test;
    root /var/www/certweb;
}
EOF
cat >/etc/nginx/sites-available/cert-proxy <<'EOF'
server {
    listen 80;
    server_name proxy.example.test;
    location / {
        proxy_pass http://127.0.0.1:9;
    }
}
EOF
ln -sf /etc/nginx/sites-available/cert-web /etc/nginx/sites-enabled/cert-web
ln -sf /etc/nginx/sites-available/cert-proxy /etc/nginx/sites-enabled/cert-proxy
nginx -t && systemctl start nginx
`)
		found := certFindHTTP(ctx, run, []string{"web.example.test", "other.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["web.example.test"] != "/var/www/certweb" || found.h.roots["other.example.test"] != "/var/www/html" {
			t.Fatalf("webroot: %+v", found)
		}
		serves(found.h, "web.example.test")
		serves(found.h, "other.example.test")
		// A proxy-only site: the split, which passes its page on.
		page("proxy.example.test", "nginx-backend", 200)
		found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
		split(found, "proxy.example.test", "nginx-backend", 200)
	})

	t.Run("docker", func(t *testing.T) {
		cur = t
		free()
		// A container's published port, as Docker makes it: a DNAT rule
		// to the container's address (the box's own here) for incoming
		// connections, and a proxy on port 80 (docker-proxy, socat here)
		// for the server's own.
		sh("mkdir -p /srv/cert-dnat && echo dnat-backend >/srv/cert-dnat/index.html")
		start("cd /srv/cert-dnat && exec python3 -m http.server 18080")
		start("exec socat TCP-LISTEN:80,fork,reuseaddr TCP:127.0.0.1:18080")
		sh(`ip=$(ip -4 -o addr show scope global | awk '{split($4, a, "/"); print a[1]; exit}')
[ -n "$ip" ]
iptables -t nat -A PREROUTING -p tcp --dport 80 -m comment --comment cert-test -j DNAT --to-destination "$ip:18080"`)
		page("app.example.test", "dnat-backend", 200)
		found := certFindHTTP(ctx, run, []string{"app.example.test"}, fetch)
		if len(found.servers) == 0 || found.servers[0] != "socat" {
			t.Fatalf("holders %q", found.servers)
		}
		split(found, "app.example.test", "dnat-backend", 200)
		if !strings.Contains(sh("iptables -t nat -S PREROUTING"), "cert-test") {
			t.Fatal("Docker's rule went")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		cur = t
		free()
		sh("mkdir -p /srv/cert-other && echo other-program >/srv/cert-other/index.html")
		start("cd /srv/cert-other && exec python3 -m http.server 80")
		found := certFindHTTP(ctx, run, []string{"x.example.test"}, fetch)
		// Its folder is not one the server's configuration names: the split.
		split(found, "x.example.test", "other-program", 200)
	})

	t.Run("caddy", func(t *testing.T) {
		cur = t
		free()
		sh(`cat >/etc/caddy/sites/cert-test.caddy <<'EOF'
http://proxy.example.test {
	reverse_proxy 127.0.0.1:9
}
EOF
systemctl start caddy && caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1; sleep 1`)
		before := sh("cat /etc/caddy/Caddyfile /etc/caddy/sites/*.caddy")
		// The catch-all :80 site serves /srv/www.
		found := certFindHTTP(ctx, run, []string{"site.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["site.example.test"] != "/srv/www" {
			t.Fatalf("caddy folder: %+v", found)
		}
		serves(found.h, "site.example.test")
		// A proxy-only site: a route through the admin API.
		found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPCaddy || len(found.h.caddyRoutes) == 0 {
			t.Fatalf("caddy route: %+v (%v)", found, found.err)
		}
		serves(found.h, "proxy.example.test")
		if err := found.h.close(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sh("curl -s localhost:2019/config/"), "finallyshell-acme") {
			t.Fatal("the route is still in Caddy")
		}
		if after := sh("cat /etc/caddy/Caddyfile /etc/caddy/sites/*.caddy"); after != before {
			t.Fatal("Caddy's files changed")
		}
	})

	// Both web servers, Nginx on port 80 and Caddy on another port with its
	// admin API: Nginx's folders serve, and a proxy-only Nginx site gets the
	// split, never a route in Caddy, which does not hold port 80.
	t.Run("both-nginx80", func(t *testing.T) {
		cur = t
		free()
		sh("mkdir -p /srv/cert-backend && echo nginx-backend >/srv/cert-backend/index.html")
		start("cd /srv/cert-backend && exec python3 -m http.server 9 --bind 127.0.0.1")
		sh(`mkdir -p /srv/cert-alt && echo caddy-alt >/srv/cert-alt/index.html
cat >/srv/cert-alt/Caddyfile <<'EOF'
{
	admin localhost:2019
}
:8081 {
	root * /srv/cert-alt
	file_server
}
EOF`)
		start("exec caddy run --adapter caddyfile --config /srv/cert-alt/Caddyfile")
		sh(`mkdir -p /var/www/certweb
cat >/etc/nginx/sites-available/cert-web <<'EOF'
server {
    listen 80;
    server_name web.example.test;
    root /var/www/certweb;
}
EOF
cat >/etc/nginx/sites-available/cert-proxy <<'EOF'
server {
    listen 80;
    server_name proxy.example.test;
    location / {
        proxy_pass http://127.0.0.1:9;
    }
}
EOF
ln -sf /etc/nginx/sites-available/cert-web /etc/nginx/sites-enabled/cert-web
ln -sf /etc/nginx/sites-available/cert-proxy /etc/nginx/sites-enabled/cert-proxy
nginx -t && systemctl start nginx
for i in 1 2 3 4 5 6 7 8 9 10; do curl -sf localhost:2019/config/ >/dev/null && break; sleep 0.5; done`)
		if busy, programs := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); !busy || !slices.Contains(programs, "nginx") || slices.Contains(programs, "caddy") {
			t.Fatalf("port 80 held by %q, want nginx alone", programs)
		}
		caddyBefore := sh("curl -s localhost:2019/config/")
		found := certFindHTTP(ctx, run, []string{"web.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["web.example.test"] != "/var/www/certweb" {
			t.Fatalf("nginx folder: %+v", found)
		}
		serves(found.h, "web.example.test")
		found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
		split(found, "proxy.example.test", "nginx-backend", 200)
		if after := sh("curl -s localhost:2019/config/"); after != caddyBefore {
			t.Fatalf("Caddy, not on port 80, was changed:\n%s", after)
		}
	})

	// Both web servers, Caddy on port 80 and Nginx on another port: Caddy's
	// folder serves, a proxy-only Caddy site gets a route through its admin
	// API, and Nginx, which does not hold port 80, is not touched.
	t.Run("both-caddy80", func(t *testing.T) {
		cur = t
		free()
		sh("mkdir -p /srv/cert-backend && echo caddy-backend >/srv/cert-backend/index.html")
		start("cd /srv/cert-backend && exec python3 -m http.server 9 --bind 127.0.0.1")
		sh(`mv /etc/nginx/sites-enabled/default /etc/nginx/cert-test-default.link
mkdir -p /srv/cert-alt && echo nginx-alt >/srv/cert-alt/index.html
cat >/etc/nginx/sites-available/cert-alt <<'EOF'
server {
    listen 8081;
    server_name alt.example.test;
    root /srv/cert-alt;
}
EOF
ln -sf /etc/nginx/sites-available/cert-alt /etc/nginx/sites-enabled/cert-alt
nginx -t && systemctl start nginx
cat >/etc/caddy/sites/cert-test.caddy <<'EOF'
http://proxy.example.test {
	reverse_proxy 127.0.0.1:9
}
EOF
systemctl start caddy && caddy reload --config /etc/caddy/Caddyfile >/dev/null 2>&1; sleep 1`)
		if busy, programs := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); !busy || !slices.Contains(programs, "caddy") || slices.Contains(programs, "nginx") {
			t.Fatalf("port 80 held by %q, want caddy alone", programs)
		}
		nginxBefore := sh("nginx -T 2>/dev/null | sha256sum")
		found := certFindHTTP(ctx, run, []string{"site.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["site.example.test"] != "/srv/www" {
			t.Fatalf("caddy folder: %+v", found)
		}
		serves(found.h, "site.example.test")
		found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPCaddy {
			t.Fatalf("caddy route: %+v (%v)", found, found.err)
		}
		serves(found.h, "proxy.example.test")
		if err := found.h.close(); err != nil {
			t.Fatal(err)
		}
		page("proxy.example.test", "caddy-backend", 200)
		if after := sh("nginx -T 2>/dev/null | sha256sum"); after != nginxBefore {
			t.Fatal("Nginx, not on port 80, was changed")
		}
		if strings.Contains(sh("nginx -T 2>/dev/null"), "acme-challenge") {
			t.Fatal("Nginx got a challenge location")
		}
	})

	// Neither web server installed: a responder of the app's own.
	t.Run("neither", func(t *testing.T) {
		cur = t
		free()
		sh("mv /usr/sbin/nginx /usr/sbin/nginx.cert-test; mv /usr/bin/caddy /usr/bin/caddy.cert-test")
		found := certFindHTTP(ctx, run, []string{"bare.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPResponder {
			sh("mv /usr/sbin/nginx.cert-test /usr/sbin/nginx; mv /usr/bin/caddy.cert-test /usr/bin/caddy")
			t.Fatalf("%+v (%v)", found, found.err)
		}
		serves(found.h, "bare.example.test")
		err := found.h.close()
		sh("mv /usr/sbin/nginx.cert-test /usr/sbin/nginx; mv /usr/bin/caddy.cert-test /usr/bin/caddy")
		if err != nil {
			t.Fatal(err)
		}
		if busy, _ := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); busy {
			t.Fatal("the responder still runs")
		}
	})

	t.Run("apache", func(t *testing.T) {
		cur = t
		free()
		sh(`command -v apache2 >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq apache2 >/dev/null
systemctl stop apache2 || :
cat >/etc/apache2/sites-available/cert-deny.conf <<'EOF'
<VirtualHost *:80>
    ServerName apx.example.test
    DocumentRoot /var/www/html
    <Location />
        Require all denied
    </Location>
</VirtualHost>
EOF
a2ensite -q cert-deny >/dev/null
systemctl start apache2`)
		found := certFindHTTP(ctx, run, []string{"default.example.test"}, fetch)
		if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["default.example.test"] != "/var/www/html" {
			t.Fatalf("apache folder: %+v", found)
		}
		serves(found.h, "default.example.test")
		// A site that refuses everything: the split, which passes the 403 on.
		page("apx.example.test", "Forbidden", 403)
		found = certFindHTTP(ctx, run, []string{"apx.example.test"}, fetch)
		split(found, "apx.example.test", "Forbidden", 403)
		sh("a2dissite -q cert-deny >/dev/null; systemctl stop apache2; systemctl disable -q apache2")
	})

	t.Run("perl", func(t *testing.T) {
		cur = t
		free()
		sh("mv /usr/bin/python3 /usr/bin/python3.cert-test")
		found := certFindHTTP(ctx, run, []string{"perl.example.test"}, fetch)
		sh("mv /usr/bin/python3.cert-test /usr/bin/python3")
		if found.h == nil || found.h.mode != certHTTPResponder {
			t.Fatalf("%+v", found)
		}
		if !strings.Contains(sh("ps -o args= -p "+strconv.Itoa(found.h.pid)), "perl") {
			t.Fatal("not the Perl responder")
		}
		serves(found.h, "perl.example.test")
		if err := found.h.close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ufw", func(t *testing.T) {
		cur = t
		free()
		sh("ufw allow 22/tcp >/dev/null && ufw default deny incoming >/dev/null && ufw --force enable >/dev/null")
		found := certFindHTTP(ctx, run, []string{"fw.example.test"}, fetch)
		if found.h != nil || found.confirm == nil || found.confirm.kind != certConfirmFirewall || found.confirm.target != "ufw" {
			t.Fatalf("%+v (%v)", found, found.err)
		}
		h, err := found.confirm.apply(ctx)
		if err != nil {
			t.Fatal(err)
		}
		serves(h, "fw.example.test")
		if err := h.close(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sh("ufw status"), "80/tcp") {
			t.Fatal("ufw still lets port 80 in")
		}
		// Behind ufw, with port 80 open for Nginx, the split's redirected
		// connections still get in. A connection made before the split
		// keeps going to Nginx, as NAT applies to new ones: Docker
		// Desktop's port forward keeps its connections open, so the site
		// closes them after each answer.
		sh("mkdir -p /srv/cert-backend && echo nginx-backend >/srv/cert-backend/index.html")
		start("cd /srv/cert-backend && exec python3 -m http.server 9 --bind 127.0.0.1")
		sh(`ufw allow 80/tcp >/dev/null
cat >/etc/nginx/sites-available/cert-proxy <<'EOF'
server {
    listen 80;
    server_name proxy.example.test;
    keepalive_timeout 0;
    location / {
        proxy_pass http://127.0.0.1:9;
    }
}
EOF
ln -sf /etc/nginx/sites-available/cert-proxy /etc/nginx/sites-enabled/cert-proxy
nginx -t && systemctl start nginx`)
		found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
		split(found, "proxy.example.test", "nginx-backend", 200)
	})
}
