package app

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
)

// TestCertHTTPLive serves HTTP challenges from the test box in each of the
// three ways, as root: a responder on the free port 80, an Nginx site's
// document root, and a temporary location in a proxy-only site, taken out
// afterwards. The box publishes its port 80 on TESTBOX_HTTP
// (127.0.0.1:8080), where the probes go with the domain as the host.
func TestCertHTTPLive(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE not set")
	}
	a, _, s := connectTestServer(t)
	t.Cleanup(a.CloseAll)
	run := a.sitesRoot(s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sh := func(script string) string {
		t.Helper()
		out, stderr, err := run(ctx, script, nil, nil)
		if err != nil {
			t.Fatalf("%s: %v: %s", script, err, stderr)
		}
		return out
	}
	addr := os.Getenv("TESTBOX_HTTP")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	fetch := func(ctx context.Context, raw string) (string, error) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+u.Path, nil)
		req.Host = u.Host
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "", &url.Error{Op: "GET", URL: raw, Err: io.ErrUnexpectedEOF}
		}
		return string(b), nil
	}
	// serves checks that h answers a challenge of domain, and no longer
	// once it is cleaned up.
	serves := func(h *certHTTP, domain string) {
		t.Helper()
		token, keyAuth := certRandom(), certRandom()+"."+certRandom()
		if err := h.Present(domain, token, keyAuth); err != nil {
			t.Fatal(err)
		}
		if got, err := fetch(ctx, "http://"+domain+monitor.ACMEPath+token); err != nil || got != keyAuth {
			t.Fatalf("%s: challenge %q, %v", domain, got, err)
		}
		if err := h.CleanUp(domain, token, keyAuth); err != nil {
			t.Fatal(err)
		}
		if _, err := fetch(ctx, "http://"+domain+monitor.ACMEPath+token); err == nil {
			t.Fatalf("%s: the challenge is still served", domain)
		}
	}
	cleanup := `systemctl stop nginx || :
rm -f /etc/nginx/sites-enabled/cert-web /etc/nginx/sites-enabled/cert-proxy
rm -f /etc/nginx/sites-available/cert-web* /etc/nginx/sites-available/cert-proxy*
rm -rf /var/www/certweb ` + monitor.ACMERoot + "\n"
	sh(cleanup)
	t.Cleanup(func() { run(context.Background(), cleanup, nil, nil) })

	// Nothing on port 80: a responder of its own.
	found := certFindHTTP(ctx, run, []string{"free.example.test"}, fetch)
	if found.h == nil || found.h.mode != certHTTPResponder {
		t.Fatalf("free port: %+v", found)
	}
	serves(found.h, "free.example.test")
	if err := found.h.close(); err != nil {
		t.Fatal(err)
	}
	if busy, _ := monitor.ParsePort80(sh(monitor.ACMEPort80Command)); busy {
		t.Fatal("the responder still runs")
	}

	// A site with a document root, and another domain the default site
	// takes.
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
	found = certFindHTTP(ctx, run, []string{"web.example.test", "other.example.test"}, fetch)
	if found.h == nil || found.h.mode != certHTTPWebroot || found.h.roots["web.example.test"] != "/var/www/certweb" || found.h.roots["other.example.test"] != "/var/www/html" {
		t.Fatalf("webroot: %+v", found)
	}
	serves(found.h, "web.example.test")
	serves(found.h, "other.example.test")

	// A proxy-only site: a temporary location, which needs a go-ahead.
	found = certFindHTTP(ctx, run, []string{"proxy.example.test"}, fetch)
	if found.h != nil || found.confirm == nil || found.confirm.kind != certConfirmNginx ||
		found.confirm.target != "/etc/nginx/sites-available/cert-proxy" || len(found.confirm.lines) != 1 {
		t.Fatalf("proxy site: %+v", found)
	}
	before := sh("cat /etc/nginx/sites-available/cert-proxy")
	h, err := found.confirm.apply(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sh("cat /etc/nginx/sites-available/cert-proxy"), monitor.ACMELocationMark) {
		t.Fatal("no location added")
	}
	serves(h, "proxy.example.test")
	if err := h.close(); err != nil {
		t.Fatal(err)
	}
	if after := sh("cat /etc/nginx/sites-available/cert-proxy"); after != before {
		t.Fatalf("the site was not put back:\n%s", after)
	}
	if out := sh("nginx -T 2>/dev/null | grep -c finallyshell-acme || :"); strings.TrimSpace(out) != "0" {
		t.Fatalf("nginx still has the location: %q", out)
	}
}
