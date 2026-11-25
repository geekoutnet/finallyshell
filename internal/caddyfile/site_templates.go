package caddyfile

import "strings"

func SiteTemplateIDs() []string {
	return []string{"static", "proxy", "app", "wordpress", "spa", "www", "browser", "gateway", "docker", "websocket", "balancer"}
}

func SiteTemplate(id string) SiteSpec {
	base := SiteSpec{Addresses: []string{"example.test"}, Type: Static, Root: "/var/www/example"}
	switch id {
	case "proxy", "app", "docker", "websocket", "balancer", "gateway":
		base.Type, base.Root = Proxy, ""
		base.Upstreams = []string{"127.0.0.1:8080"}
	}
	switch id {
	case "app":
		base.Encode = []string{"zstd", "gzip"}
		base.Proxy.HeaderUp = []Header{{"X-Real-IP", "{remote_host}"}}
	case "wordpress":
		base.Type, base.PHPUpstream = PHP, "unix//run/php/php-fpm.sock"
		base.Encode, base.SecurityHeaders = []string{"zstd", "gzip"}, true
	case "spa":
		base.Type, base.Root = SPA, "/var/www/example/dist"
		base.Encode = []string{"zstd", "gzip"}
	case "www":
		base, _ = WWWRedirect("example.test", false)
	case "browser":
		base.Type, base.Root, base.Browse = Browser, "/srv/files", true
		base.Auth = []AuthUser{{Name: "admin"}}
	case "gateway":
		base.Routes = []PathRoute{
			{Path: "/api/*", Upstreams: []string{"127.0.0.1:8081"}},
			{Path: "/assets/*", Upstreams: []string{"127.0.0.1:8082"}},
		}
	case "docker":
		base.Proxy.HeaderUp = []Header{{"Host", "{upstream_hostport}"}}
	case "websocket":
		base.Proxy.FlushInterval = "-1"
	case "balancer":
		base.Upstreams = []string{"127.0.0.1:8080", "127.0.0.1:8081"}
		base.Proxy = ProxyOptions{Policy: "round_robin", TryDuration: "5s", HealthURI: "/health", HealthInterval: "30s", HealthTimeout: "3s", HealthStatus: "2xx", FailDuration: "30s", MaxFails: "2"}
	}
	return base
}

func WWWRedirect(host string, toWWW bool) (SiteSpec, error) {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(host, "www.")
	if !siteDomain.MatchString(host) || !strings.Contains(host, ".") || strings.Contains(host, "*") || !siteAddress(host) {
		return SiteSpec{}, SiteFieldError{"addresses"}
	}
	from, to := "www."+host, host
	if toWWW {
		from, to = to, from
	}
	return SiteSpec{Addresses: []string{from}, Type: Redirect, RedirectCode: "308", RedirectURL: "https://" + to + "{uri}"}, nil
}

func HasDNSProvider(modules []string, provider string) bool {
	if !siteModule.MatchString(provider) {
		return false
	}
	for _, module := range modules {
		name, _, _ := strings.Cut(strings.TrimSpace(module), " ")
		if name == "dns.providers."+provider {
			return true
		}
	}
	return false
}
