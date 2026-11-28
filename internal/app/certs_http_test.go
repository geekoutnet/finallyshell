package app

import (
	"slices"
	"testing"

	"finallyshell/internal/monitor"
)

func TestCertNameMatch(t *testing.T) {
	for _, tc := range []struct {
		names  []string
		domain string
		want   bool
	}{
		{[]string{"example.com", "www.example.com"}, "www.example.com", true},
		{[]string{"Example.COM"}, "example.com", true},
		{[]string{"*.example.com"}, "a.example.com", true},
		{[]string{"*.example.com"}, "a.b.example.com", false},
		{[]string{"*.example.com"}, "example.com", false},
		{[]string{".example.com"}, "example.com", true},
		{[]string{".example.com"}, "a.b.example.com", true},
		{[]string{"~^www\\.(.+)$"}, "www.example.com", false},
		{[]string{"_"}, "example.com", false},
		{[]string{"notexample.com"}, "example.com", false},
		{[]string{"*.example.com"}, "aexample.com", false},
	} {
		if got := certNameMatch(tc.names, tc.domain); got != tc.want {
			t.Errorf("%q takes %q: %v, want %v", tc.names, tc.domain, got, tc.want)
		}
	}
}

func certTestInventory() monitor.NginxInventory {
	l80 := []monitor.NginxListen{{Port: 80}}
	return monitor.NginxInventory{
		NginxTree: monitor.NginxTree{Main: monitor.NginxMain, Sites: []monitor.NginxSite{
			{File: "/etc/nginx/sites-enabled/default", Line: 1, Names: []string{"_"}, Root: "/var/www/html", Listen: []monitor.NginxListen{{Port: 80, Default: true}}},
			{File: "/etc/nginx/sites-enabled/web", Line: 1, Names: []string{"web.example"}, Root: "/srv/web", Listen: l80},
			{File: "/etc/nginx/sites-enabled/web", Line: 9, Names: []string{"web.example"}, Root: "/srv/web-tls", Listen: []monitor.NginxListen{{Port: 443, TLS: true}}},
			{File: "/etc/nginx/conf.d/api.conf", Line: 3, Names: []string{"api.example"}, Proxy: []string{"http://127.0.0.1:3000"}, Listen: l80},
			{File: "/etc/nginx/conf.d/odd.conf", Line: 1, Names: []string{"odd.example"}, Root: "html", Listen: l80},
			{File: "/etc/nginx/conf.d/mix.conf", Line: 1, Names: []string{"mix.example"}, Listen: l80},
			{File: "/etc/nginx/sites-enabled/mix2", Line: 1, Names: []string{"mix2.example"}, Listen: l80},
		}},
		Links: map[string]string{"web": "enabled", "default": "enabled", "mix2": "enabled"},
	}
}

func TestCertICPPage(t *testing.T) {
	for body, want := range map[string]bool{
		`<!DOCTYPE html><html><head><title>网站暂时无法访问</title></head><body>该网站未根据规定备案</body></html>`: true,
		`<html><body>This site is blocked: no ICP filing (beian).</body></html>`:                  true,
		"abc.def":                             false, // a key authorization
		`<html><body>Not Found</body></html>`: false,
		"ICP not html":                        false,
	} {
		if got := certICPPage(body); got != want {
			t.Errorf("%q: %v, want %v", body, got, want)
		}
	}
}

func TestCertWebroots(t *testing.T) {
	inv := certTestInventory()
	// The site's own folder, then the default server's; never one only on
	// TLS, nor a relative one.
	if got := certWebroots(inv, "web.example"); !slices.Equal(got, []string{"/srv/web", "/var/www/html"}) {
		t.Fatalf("web: %q", got)
	}
	if got := certWebroots(inv, "odd.example"); !slices.Equal(got, []string{"/var/www/html"}) {
		t.Fatalf("relative root: %q", got)
	}
	if got := certWebroots(inv, "nowhere.example"); !slices.Equal(got, []string{"/var/www/html"}) {
		t.Fatalf("unknown domain: %q", got)
	}
}

func TestCertRootsFor(t *testing.T) {
	srv := certServer{nginx: true, inv: certTestInventory(),
		caddyRoots: []string{"/srv/caddy"},
		survey:     monitor.ACMESurvey{Roots: []string{"/var/www/apache", "/var/www/html", "/a", "/b", "/c", "/d", "/e"}}}
	got := certRootsFor(srv, "web.example")
	want := []string{"/srv/web", "/var/www/html", "/srv/caddy", "/var/www/apache", "/a", "/b", "/c", "/d"}
	if !slices.Equal(got, want) {
		t.Fatalf("roots %q, want %q", got, want)
	}
	// Without Nginx, its sites are not looked at.
	srv.nginx = false
	if got := certRootsFor(srv, "web.example"); got[0] != "/srv/caddy" {
		t.Fatalf("no nginx: %q", got)
	}
}
