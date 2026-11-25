package caddyfile

import (
	"encoding/base64"
	"strings"
	"testing"
)

const sampleSiteHash = "$2a$14$yf3pBSXg2hTjFzLc42XQA.agOHKNAPVYCJ4Mwbe0dSkAwbJjTS2kG"

func TestSiteGenerateKinds(t *testing.T) {
	for _, test := range []struct {
		name string
		spec SiteSpec
		want string
	}{
		{"static", SiteSpec{Type: Static, Root: "/srv/site photos", Index: []string{"home.html"}, Hide: []string{".git", "/srv/site photos/private"}}, "\troot * \"/srv/site photos\"\n\tfile_server {\n\t\tindex home.html\n\t\thide .git \"/srv/site photos/private\"\n\t}\n"},
		{"proxy", SiteSpec{Type: Proxy, Upstreams: []string{"https://127.0.0.1:8443", "h2c://127.0.0.1:8000"}}, "\treverse_proxy https://127.0.0.1:8443 h2c://127.0.0.1:8000\n"},
		{"php", SiteSpec{Type: PHP, Root: "/var/www", PHPUpstream: "unix//run/php/php-fpm.sock"}, "\troot * /var/www\n\tphp_fastcgi unix//run/php/php-fpm.sock\n\tfile_server\n"},
		{"redirect", SiteSpec{Type: Redirect, RedirectURL: "https://example.test{uri}", RedirectCode: "308"}, "\tredir https://example.test{uri} 308\n"},
		{"spa", SiteSpec{Type: SPA, Root: "/srv/spa"}, "\troot * /srv/spa\n\ttry_files {path} /index.html\n\tfile_server\n"},
		{"respond", SiteSpec{Type: Respond, Body: "site ready", Status: "200"}, "\trespond \"site ready\" 200\n"},
		{"browser", SiteSpec{Type: Browser, Root: "/srv/shared"}, "\troot * /srv/shared\n\tfile_server browse\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.spec.Addresses = []string{"example.test", "http://other.example.test:8080"}
			got, err := GenerateSite(test.spec)
			want := "example.test, http://other.example.test:8080 {\n" + test.want + "}\n"
			if err != nil || got != want {
				t.Fatalf("GenerateSite = %q, %v; want %q", got, err, want)
			}
			parsed := readTestSite(t, got)
			if parsed.Type != test.spec.Type {
				t.Fatalf("parsed type = %q, want %q", parsed.Type, test.spec.Type)
			}
			assertStableSite(t, parsed, got)
		})
	}
}

func TestSiteGenerateOptions(t *testing.T) {
	spec := SiteSpec{
		Addresses: []string{"*.example.test"}, Type: Proxy, Upstreams: []string{"https://localhost:8000", "https://localhost:8001"},
		Proxy: ProxyOptions{
			Policy: "header", PolicyArgs: []string{"X-Client"}, TryDuration: "5s", HealthURI: "/health", HealthInterval: "10s", HealthTimeout: "3s", HealthStatus: "2xx", FailDuration: "30s", MaxFails: "3", FlushInterval: "-1", TLS: true, InsecureSkipVerify: true, DialTimeout: "1s", ReadTimeout: "2s", WriteTimeout: "3s", KeepAlive: "30s", HeaderUp: []Header{{"Host", "{upstream_hostport}"}, {"X-Real-IP", "{remote_host}"}}, HeaderDown: []Header{{"-X-Internal", ""}},
		},
		Encode: []string{"zstd", "gzip"}, SecurityHeaders: true, RemoveServer: true, Headers: []Header{{"X-Test", "two words"}},
		CORS: CORSOptions{Origins: []string{"https://app.example.test"}, Methods: []string{"GET", "OPTIONS"}, Headers: []string{"Authorization", "Content-Type"}},
		Auth: []AuthUser{{"alice", sampleSiteHash}}, AuthPaths: []string{"/private/*"},
		IP:      IPOptions{Source: "client_ip", Allow: []string{"192.0.2.0/24"}, Deny: []string{"192.0.2.55"}, Action: "abort"},
		MaxBody: "20MB", Log: LogOptions{Path: "/var/log/caddy/site.log", RollSize: "10MiB", RollKeep: "4", Format: "json"},
		TLS:    TLSOptions{Mode: "dns", DNS: "cloudflare", DNSArgs: []string{"{$CLOUDFLARE_API_TOKEN}"}, ClientAuth: "require_and_verify", CAPool: "/etc/caddy/client-ca.pem", MinProtocol: "tls1.3"},
		Routes: []PathRoute{{Path: "/api/*", Upstreams: []string{"localhost:9090"}}},
	}
	got, err := GenerateSite(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"dns cloudflare {$CLOUDFLARE_API_TOKEN}", "protocols tls1.3", "mode require_and_verify", "trust_pool file {", "pem_file /etc/caddy/client-ca.pem",
		"output file /var/log/caddy/site.log {", "roll_size 10MiB", "roll_keep 4", "format json",
		"not client_ip 192.0.2.0/24", "@fs_ip_deny client_ip 192.0.2.55", "abort @fs_ip_deny", "max_size 20MB", "encode zstd gzip",
		"Strict-Transport-Security max-age=31536000", "X-Content-Type-Options nosniff", "X-Frame-Options DENY", "-Server", "X-Test \"two words\"",
		"header Origin https://app.example.test", "method OPTIONS", "header Access-Control-Request-Method GET", "header !Access-Control-Request-Method", "Access-Control-Allow-Origin https://app.example.test", "Access-Control-Allow-Methods \"GET, OPTIONS\"", "route @fs_preflight_1 {", "respond 204", "respond @fs_cors_invalid_preflight 403", "header @fs_cors_unmatched -Access-Control-Allow-Origin", "-Access-Control-Allow-Credentials",
		"@fs_auth path /private/*", "basic_auth @fs_auth {", "alice " + sampleSiteHash, "handle_path /api/* {", "reverse_proxy localhost:9090", "handle {",
		"lb_policy header X-Client", "lb_try_duration 5s", "health_uri /health", "health_interval 10s", "health_timeout 3s", "health_status 2xx", "fail_duration 30s", "max_fails 3",
		"header_up Host {upstream_hostport}", "header_up X-Real-IP {remote_host}", "header_down -X-Internal", "flush_interval -1", "transport http {", "tls_insecure_skip_verify", "dial_timeout 1s", "read_timeout 2s", "write_timeout 3s", "keepalive 30s",
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("generated site missing %q:\n%s", line, got)
		}
	}
	if strings.Index(got, "abort @fs_ip_deny") > strings.Index(got, "handle_path /api/*") || strings.Index(got, "basic_auth") > strings.Index(got, "handle_path /api/*") {
		t.Fatalf("access controls must run before routing:\n%s", got)
	}
	parsed := readTestSite(t, got)
	assertStableSite(t, parsed, got)
}

func TestSiteReadExisting(t *testing.T) {
	site := readTestSite(t, "example.test {\n\tlog\n\troot /srv/www\n\tfile_server\n\tencode gzip\n}\n")
	if site.Root != "/srv/www" || site.Type != Static || !site.Log.Enabled || len(site.Encode) != 1 || site.Encode[0] != "gzip" {
		t.Fatalf("unexpected model: %#v", site)
	}
}

func TestSiteSPAAuthenticatesOriginalPathBeforeRewrite(t *testing.T) {
	spec := SiteSpec{Addresses: []string{"example.test"}, Type: SPA, Root: "/srv/spa", Auth: []AuthUser{{"alice", sampleSiteHash}}, AuthPaths: []string{"/private/*"}}
	got, err := GenerateSite(spec)
	if err != nil {
		t.Fatal(err)
	}
	auth, rewrite := strings.Index(got, "\t\tbasic_auth @fs_auth"), strings.Index(got, "\t\ttry_files")
	if auth < 0 || rewrite < 0 || auth > rewrite {
		t.Fatalf("authentication must see the original request path:\n%s", got)
	}
	assertStableSite(t, readTestSite(t, got), got)
}

func TestSiteAuthHashFromOlderCaddy(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(sampleSiteHash))
	text := "example.test {\n\tbasic_auth {\n\t\talice " + encoded + "\n\t}\n\trespond \"\" 200\n}\n"
	site := readTestSite(t, text)
	if !IsBcryptHash(encoded) || len(site.Auth) != 1 || site.Auth[0].Hash != encoded {
		t.Fatalf("legacy Caddy hash not preserved: %#v", site.Auth)
	}
	assertStableSite(t, site, text)
}

func TestSiteReadRespondRedirectAndAddresses(t *testing.T) {
	for _, test := range []struct {
		input, output string
	}{
		{`respond "0404"`, `respond 0404 200`},
		{`respond "404"`, `respond "" 404`},
		{`redir * /other permanent`, `redir * /other 301`},
	} {
		text := readTestSite(t, "http://, example.test:{$WEB_PORT:443}, {$HOST}, :8080 {\n\t"+test.input+"\n}\n")
		if len(text.Addresses) != 4 {
			t.Fatalf("unexpected addresses: %#v", text.Addresses)
		}
		got, err := GenerateSite(text)
		if err != nil || !strings.Contains(got, "\t"+test.output+"\n") {
			t.Errorf("round trip = %q, %v; want %q", got, err, test.output)
		}
	}
}

func TestSiteQuoteArgRoundTrip(t *testing.T) {
	for _, original := range []string{"", "x\u00a0y", "}", "{}", "# phrase", `this " is literal`, `has \ and \\ backslashes`, "hello\nworld", "<<marker"} {
		arg, err := QuoteArg(original)
		if err != nil {
			t.Fatal(err)
		}
		args, err := SplitArgs(arg)
		if err != nil || len(args) != 1 || args[0] != original {
			t.Fatalf("QuoteArg(%q) = %q, split = %v, %v", original, arg, args, err)
		}
	}
}

func TestSiteEditUnknown(t *testing.T) {
	for _, body := range []string{
		"reverse_proxy localhost:8080 {\n\t\tstream_timeout 1h\n\t}",
		"import production",
		"@assets path /static/*\n\tfile_server @assets",
		"reverse_proxy localhost:8080\n\trespond failed 500",
		"route {\n\t\treverse_proxy localhost:8080\n\t\trequest_body {\n\t\t\tmax_size 10MB\n\t\t}\n\t}",
		"tls {\n\t\tissuer internal\n\t}",
		"basic_auth {\n\t\talice plaintext\n\t}",
	} {
		t.Run(body, func(t *testing.T) {
			doc, err := Parse("example.test {\n\t" + body + "\n}\n")
			if err != nil {
				t.Fatal(err)
			}
			spec, err := ParseSite(doc.Nodes[0])
			if err == nil || len(spec.Addresses) != 1 {
				t.Fatalf("unknown config should require a code editor, got %#v, %v", spec, err)
			}
		})
	}
}

func TestSiteRejectFormInjectionAndInvalidValues(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*SiteSpec)
	}{
		{"address newline", func(site *SiteSpec) { site.Addresses = []string{"example.test\n{ debug }"} }},
		{"address syntax", func(site *SiteSpec) { site.Addresses = []string{"example.test{debug"} }},
		{"double scheme", func(site *SiteSpec) { site.Addresses = []string{"http://https://example.test"} }},
		{"relative root", func(site *SiteSpec) { site.Root = "srv/www" }},
		{"log newline", func(site *SiteSpec) { site.Log.Path = "/tmp/log\nfoo" }},
		{"auth plaintext", func(site *SiteSpec) { site.Auth = []AuthUser{{"alice", "plaintext"}} }},
		{"auth reserved username", func(site *SiteSpec) { site.Auth = []AuthUser{{"import", sampleSiteHash}} }},
		{"auth paths without users", func(site *SiteSpec) { site.AuthPaths = []string{"/private/*"} }},
		{"ip syntax", func(site *SiteSpec) { site.IP.Allow = []string{"not-an-ip"} }},
		{"invalid CORS origin", func(site *SiteSpec) { site.CORS.Origins = []string{"https://app.example.test/private"} }},
		{"mixed CORS wildcard", func(site *SiteSpec) { site.CORS.Origins = []string{"*", "https://app.example.test"} }},
		{"DNS plaintext credential", func(site *SiteSpec) {
			site.TLS = TLSOptions{Mode: "dns", DNS: "cloudflare", DNSArgs: []string{"clear-token"}}
		}},
		{"DNS credential as env default", func(site *SiteSpec) {
			site.TLS = TLSOptions{Mode: "dns", DNS: "cloudflare", DNSArgs: []string{"{$TOKEN:clear-token}"}}
		}},
		{"insecure transport without TLS", func(site *SiteSpec) {
			site.Type, site.Upstreams, site.Proxy.InsecureSkipVerify = Proxy, []string{"localhost:80"}, true
		}},
		{"custom escapes site", func(site *SiteSpec) { site.Custom = "}\nnew.example.test {\n\trespond 200" }},
		{"dangling custom continuation", func(site *SiteSpec) { site.Custom = "respond 200 \\" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			site := SiteSpec{Addresses: []string{"example.test"}, Type: Static, Root: "/srv/www"}
			test.edit(&site)
			if got, err := GenerateSite(site); err == nil || got != "" {
				t.Fatalf("accepted invalid configuration: %q, %v", got, err)
			}
		})
	}
}

func TestSiteTemplatesAndWWW(t *testing.T) {
	if got := len(SiteTemplateIDs()); got != 11 {
		t.Fatalf("template count = %d", got)
	}
	for _, id := range SiteTemplateIDs() {
		t.Run(id, func(t *testing.T) {
			site := SiteTemplate(id)
			for index := range site.Auth {
				site.Auth[index].Hash = sampleSiteHash
			}
			got, err := GenerateSite(site)
			if err != nil {
				t.Fatal(err)
			}
			assertStableSite(t, readTestSite(t, got), got)
		})
	}
	for _, toWWW := range []bool{false, true} {
		site, err := WWWRedirect("www.example.test", toWWW)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(site.Addresses[0], "www.") == toWWW || strings.HasPrefix(site.RedirectURL, "https://www.") != toWWW {
			t.Fatalf("unexpected redirect: %#v", site)
		}
	}
	if _, err := WWWRedirect("https://bad.test/elsewhere", true); err == nil {
		t.Fatal("URL accepted as bare domain")
	}
	if !HasDNSProvider([]string{"dns.providers.cloudflare"}, "cloudflare") || HasDNSProvider([]string{"dns.providers.cloudflare-not"}, "cloudflare") {
		t.Fatal("DNS module lookup must match exact module ID")
	}
}

func readTestSite(t *testing.T, text string) SiteSpec {
	t.Helper()
	doc, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := ParseSite(doc.Nodes[0])
	if err != nil {
		t.Fatalf("ParseSite: %v\n%s", err, text)
	}
	return spec
}

func assertStableSite(t *testing.T, parsed SiteSpec, want string) {
	t.Helper()
	got, err := GenerateSite(parsed)
	if err != nil || got != want {
		t.Fatalf("round trip = %v\n%s\nwant:\n%s", err, got, want)
	}
}
