package monitor

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var caddyNginxName = regexp.MustCompile(`^(?:\*\.)?[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

func caddyQuoteToken(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func caddyNginxLiteral(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\x00\r\n{}$\\")
}

type caddyNginxDetail struct {
	keys, certs, tryFiles, redirect []string
	complex                         bool
}

func caddyNginxAddresses(site NginxSite) []string {
	var addresses []string
	listen := site.Listen
	if len(listen) == 0 {
		listen = []NginxListen{{Port: 80}}
	}
	for _, binding := range listen {
		if binding.Port < 1 || binding.Port > 65535 {
			continue
		}
		for _, name := range site.Names {
			if !caddyNginxName.MatchString(name) && net.ParseIP(name) == nil && name != "_" {
				continue
			}
			if name == "_" {
				name = ""
			}
			scheme, port := "http", 80
			if binding.TLS {
				scheme, port = "https", 443
			}
			if name == "" || binding.Port != port {
				name = net.JoinHostPort(name, strconv.Itoa(binding.Port))
			} else if strings.Contains(name, ":") {
				name = "[" + name + "]"
			}
			addresses = append(addresses, scheme+"://"+name)
		}
	}
	slices.Sort(addresses)
	return slices.Compact(addresses)
}

func caddyNginxUpstream(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && caddyNginxLiteral(value)
}

// NginxToCaddy renders review-only Caddyfile site drafts from Nginx inventory.
// It never writes or runs anything. The inventory omits key paths, location
// rules, redirects and try_files; use NginxDumpToCaddy to convert simple
// examples of those directives from the original nginx -T output.
func NginxToCaddy(sites []NginxSite) (string, []string) {
	return caddyConvertNginx(sites, nil)
}

type CaddyNginxDraft struct {
	Name, Content string
	Notes         []string
}

func NginxToCaddySites(sites []NginxSite) []CaddyNginxDraft {
	return caddyNginxDrafts(sites, nil)
}

func caddyConvertNginx(sites []NginxSite, details map[string]caddyNginxDetail) (string, []string) {
	return caddyJoinNginxDrafts(caddyNginxDrafts(sites, details))
}

func caddyJoinNginxDrafts(drafts []CaddyNginxDraft) (string, []string) {
	var config strings.Builder
	var notes []string
	for _, draft := range drafts {
		if draft.Content != "" {
			config.WriteString(draft.Content + "\n")
		}
		notes = append(notes, draft.Notes...)
	}
	return strings.TrimSuffix(config.String(), "\n"), notes
}

func caddyNginxDrafts(sites []NginxSite, details map[string]caddyNginxDetail) []CaddyNginxDraft {
	var drafts []CaddyNginxDraft
	used := map[string]bool{}
	for _, site := range sites {
		source := fmt.Sprintf("%s:%d", site.File, site.Line)
		draft := CaddyNginxDraft{Name: strings.Join(site.Names, ", ")}
		if draft.Name == "" {
			draft.Name = source
		}
		note := func(text string) { draft.Notes = append(draft.Notes, source+": "+text) }
		detail, full := details[source]
		if detail.complex || (!full && len(site.Proxy) != 0) {
			note("review location matching, upstream selection, headers and access controls; they were not converted")
		}
		addresses := caddyNginxAddresses(site)
		if len(addresses) == 0 {
			note("no convertible site address")
			drafts = append(drafts, draft)
			continue
		}
		var safe []string
		for _, address := range addresses {
			if used[address] {
				note("duplicate address skipped: " + address)
				continue
			}
			used[address] = true
			safe = append(safe, address)
		}
		if len(safe) == 0 {
			drafts = append(drafts, draft)
			continue
		}
		var config strings.Builder
		fmt.Fprintf(&config, "%s {\n", strings.Join(safe, ", "))
		if len(detail.certs) == 1 && len(detail.keys) == 1 && caddyPath(detail.certs[0]) && caddyPath(detail.keys[0]) && caddyNginxLiteral(detail.certs[0]) && caddyNginxLiteral(detail.keys[0]) {
			fmt.Fprintf(&config, "\ttls %s %s\n", caddyQuoteToken(detail.certs[0]), caddyQuoteToken(detail.keys[0]))
		} else if len(site.Certificates) > 0 || len(detail.keys) > 0 {
			note("TLS certificates require a reviewed certificate/key pair; otherwise Caddy manages TLS automatically")
		}
		if len(detail.redirect) == 2 && detail.redirect[0] == "301" && caddyNginxLiteral(detail.redirect[1]) {
			fmt.Fprintf(&config, "\tredir %s 301\n", caddyQuoteToken(detail.redirect[1]))
		} else if len(detail.redirect) != 0 {
			note("conditional, variable or non-301 returns require manual conversion")
		}
		if caddyPath(site.Root) && caddyNginxLiteral(site.Root) && !detail.complex {
			fmt.Fprintf(&config, "\troot * %s\n", caddyQuoteToken(site.Root))
		} else if site.Root != "" {
			note("dynamic, relative or location-scoped root requires manual conversion")
		}
		if len(detail.tryFiles) > 0 {
			var candidates []string
			for _, arg := range detail.tryFiles {
				switch arg {
				case "$uri":
					candidates = append(candidates, "{path}")
				case "$uri/":
					candidates = append(candidates, "{path}/")
				case "=404":
					candidates = append(candidates, "=404")
				default:
					if strings.HasPrefix(arg, "/") && caddyNginxLiteral(arg) {
						candidates = append(candidates, caddyQuoteToken(arg))
					}
				}
			}
			if len(candidates) == len(detail.tryFiles) {
				fmt.Fprintf(&config, "\ttry_files %s\n", strings.Join(candidates, " "))
			} else {
				note("named locations or variables in try_files require manual conversion")
			}
		}
		switch {
		case len(site.Proxy) == 1 && caddyNginxUpstream(site.Proxy[0]) && !detail.complex:
			fmt.Fprintf(&config, "\treverse_proxy %s\n", caddyQuoteToken(site.Proxy[0]))
		case len(site.Proxy) != 0:
			note("multiple, scoped or URI-bearing proxy_pass directives require manual conversion")
			config.WriteString("\trespond 503\n")
		case caddyPath(site.Root) && caddyNginxLiteral(site.Root) && !detail.complex:
			config.WriteString("\tfile_server\n")
		case len(detail.redirect) == 0:
			config.WriteString("\trespond 503\n")
		}
		config.WriteString("}\n")
		draft.Content = config.String()
		drafts = append(drafts, draft)
	}
	return drafts
}

func caddyNginxDirectives(nodes []nginxNode) caddyNginxDetail {
	var detail caddyNginxDetail
	for _, node := range nodes {
		if len(node.words) == 0 {
			continue
		}
		if node.block {
			if len(node.words) == 2 && node.words[0] == "location" && node.words[1] == "/" {
				child := caddyNginxDirectives(node.child)
				detail.complex = detail.complex || child.complex
				if len(detail.tryFiles) == 0 {
					detail.tryFiles = child.tryFiles
				}
				if len(detail.redirect) == 0 {
					detail.redirect = child.redirect
				}
			} else {
				detail.complex = true
			}
			continue
		}
		if len(node.words) < 2 {
			continue
		}
		switch node.words[0] {
		case "ssl_certificate":
			detail.certs = append(detail.certs, node.words[1])
		case "ssl_certificate_key":
			detail.keys = append(detail.keys, node.words[1])
		case "try_files":
			detail.tryFiles = node.words[1:]
		case "return":
			detail.redirect = node.words[1:]
		case "listen", "server_name", "root", "proxy_pass", "access_log", "error_log", "http2":
		default:
			detail.complex = true
		}
	}
	return detail
}

// NginxDumpToCaddy parses nginx -T text with the existing inventory parser
// and enriches the draft with direct certificate/key, return 301 and
// try_files directives. Unsupported behavior is noted, not silently applied.
func NginxDumpToCaddy(dump string) (string, []string, error) {
	drafts, err := NginxDumpToCaddySites(dump)
	if err != nil {
		return "", nil, err
	}
	config, notes := caddyJoinNginxDrafts(drafts)
	return config, notes, nil
}

func NginxDumpToCaddySites(dump string) ([]CaddyNginxDraft, error) {
	tree, err := ParseNginx(dump)
	if err != nil {
		return nil, err
	}
	details := map[string]caddyNginxDetail{}
	var file string
	var content strings.Builder
	var walk func([]nginxNode)
	walk = func(nodes []nginxNode) {
		for _, node := range nodes {
			if node.block && len(node.words) != 0 {
				if node.words[0] == "server" {
					details[fmt.Sprintf("%s:%d", file, node.line)] = caddyNginxDirectives(node.child)
				} else if node.words[0] == "http" {
					walk(node.child)
				}
			}
		}
	}
	finish := func() error {
		if file == "" {
			return nil
		}
		tokens, err := nginxTokens(content.String())
		if err != nil {
			return err
		}
		nodes, err := nginxNodes(&tokens, 0)
		if err == nil {
			walk(nodes)
		}
		content.Reset()
		return err
	}
	for line := range strings.SplitSeq(dump, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "# configuration file "); ok && strings.HasSuffix(name, ":") {
			if err := finish(); err != nil {
				return nil, err
			}
			file = strings.TrimSuffix(name, ":")
		} else if file != "" {
			content.WriteString(line + "\n")
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return caddyNginxDrafts(tree.Sites, details), nil
}
