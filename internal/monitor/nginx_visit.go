package monitor

import (
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// NginxVisit is an address the desktop browser, whose connections leave
// from the server, can open for a site.
type NginxVisit struct {
	URL string
	// Host, when not "", is the site's name, to be connected to at Addr
	// whatever DNS says, as a hosts file on the server would: the site
	// opens through nginx on the server, with its own certificate, even
	// when its name points elsewhere or nowhere yet.
	Host, Addr string
	// Backend tells a URL of what nginx forwards to (proxy_pass, or a
	// server of its upstream) from one of the site itself.
	Backend bool
}

// Visits are the addresses to open site at: each of its names on each
// port it listens on, through nginx, then what it forwards to, upstream
// blocks taken apart into their servers.
func (tree NginxTree) Visits(site NginxSite) []NginxVisit {
	var visits []NginxVisit
	add := func(visit NginxVisit) {
		if !slices.ContainsFunc(visits, func(v NginxVisit) bool { return v.URL == visit.URL }) {
			visits = append(visits, visit)
		}
	}
	var names []string
	for _, name := range site.Names {
		if nginxLiteralName(name) {
			names = append(names, strings.ToLower(name))
		}
	}
	for _, listen := range site.Listen {
		if listen.Port <= 0 || listen.Port > 65535 {
			continue
		}
		addr := nginxDialAddr(listen.Addr)
		if addr == "" {
			continue
		}
		scheme, port := "http", ""
		if listen.TLS {
			scheme = "https"
		}
		if scheme == "http" && listen.Port != 80 || scheme == "https" && listen.Port != 443 {
			port = strconv.Itoa(listen.Port)
		}
		join := func(host string) string {
			if port == "" {
				return (&url.URL{Scheme: scheme, Host: bracket(host), Path: "/"}).String()
			}
			return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port), Path: "/"}).String()
		}
		named := false
		for _, name := range names {
			if net.ParseIP(name) == nil {
				add(NginxVisit{URL: join(name), Host: name, Addr: addr})
				named = true
			} else if slices.Contains([]string{"", "*", "0.0.0.0", "::"}, listen.Addr) || net.ParseIP(name).Equal(net.ParseIP(addr)) {
				// An address name is only reached where nginx listens.
				add(NginxVisit{URL: join(name)})
				named = true
			}
		}
		if !named {
			add(NginxVisit{URL: join(addr)})
		}
	}
	for _, target := range site.Proxy {
		for _, address := range tree.backendURLs(target) {
			add(NginxVisit{URL: address, Backend: true})
		}
	}
	return visits
}

// backendURLs are the URLs a proxy_pass to target reaches: target itself,
// or one for each server of the upstream it names. Targets made of
// variables, and UNIX sockets, have none.
func (tree NginxTree) backendURLs(target string) []string {
	if strings.Contains(target, "$") {
		return nil
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || strings.HasPrefix(parsed.Host, "unix:") {
		return nil
	}
	servers, ok := tree.Upstreams[parsed.Host]
	if !ok {
		return []string{parsed.String()}
	}
	var urls []string
	for _, server := range servers {
		if strings.HasPrefix(server, "unix:") || strings.ContainsAny(server, "/$") {
			continue
		}
		next := *parsed
		next.Host = server
		if _, err := url.Parse(next.String()); err == nil {
			urls = append(urls, next.String())
		}
	}
	return urls
}

// nginxLiteralName tells whether a server_name is one name, not a
// wildcard, a regular expression or the catch-all "_".
func nginxLiteralName(name string) bool {
	return name == "localhost" || ValidSiteHost(name) || net.ParseIP(name) != nil
}

// nginxDialAddr is the address on the server to reach a listener on
// addr: the loopback one for every address.
func nginxDialAddr(addr string) string {
	switch addr {
	case "", "*", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String()
	}
	if ValidSiteHost(addr) || addr == "localhost" {
		return addr
	}
	return ""
}

func bracket(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}
