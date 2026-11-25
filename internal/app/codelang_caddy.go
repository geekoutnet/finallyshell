package app

import (
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

var caddyDirectiveSnippets = []string{
	"abort", "acme_server", "basic_auth {\n\t${1:user} ${2:password_hash}\n}", "bind ${1:127.0.0.1}",
	"encode ${1:zstd gzip}", `error "${1:message}" ${2:500}`, "file_server",
	"forward_auth ${1:localhost:9091} {\n\turi ${2:/verify}\n}", "fs ${1:local}",
	"handle ${1:/api/*} {\n\t$0\n}", "handle_errors {\n\t$0\n}", "handle_path ${1:/api/*} {\n\treverse_proxy ${2:localhost:8080}\n}",
	`header ${1:X-Content-Type-Options} "${2:nosniff}"`, "import ${1:sites/*.caddy}", "intercept {\n\t$0\n}",
	"invoke ${1:route_name}", "log {\n\toutput file ${1:/var/log/caddy/access.log}\n}",
	"log_append ${1:key} ${2:value}", "log_skip ${1:@matcher}", "log_name ${1:access}", "log_level ${1:INFO}",
	"map ${1:{host\\}} ${2:{backend\\}} {\n\tdefault ${3:localhost:8080}\n}", "method ${1:GET}", "metrics",
	"php_fastcgi ${1:unix//run/php/php-fpm.sock}", "push ${1:/app.css}", "redir ${1:https://example.com} ${2:308}",
	"request_body {\n\tmax_size ${1:10MB}\n}", "request_header ${1:X-Example} ${2:value}", `respond "${1:OK}" ${2:200}`,
	"reverse_proxy ${1:localhost:8080}", "rewrite ${1:/index.html}", "root ${1:*} ${2:/srv/www}",
	"route {\n\t$0\n}", "templates", "timeouts {\n\tread_body ${1:30s}\n}", "tls ${1:internal}", "tracing",
	"try_files ${1:{path\\}} ${2:/index.html}", "uri strip_prefix ${1:/api}", "vars ${1:key} ${2:value}",
}

var caddyGlobalSnippets = []string{
	"debug", "http_port ${1:80}", "https_port ${1:443}", "default_bind ${1:127.0.0.1}", "order ${1:directive} before ${2:respond}",
	"storage ${1:file_system} {\n\troot ${2:/var/lib/caddy}\n}", "storage_clean_interval ${1:24h}", "admin ${1:localhost:2019}",
	"persist_config ${1:off}", "log {\n\tlevel ${1:INFO}\n}", "grace_period ${1:10s}", "shutdown_delay ${1:5s}", "metrics",
	"auto_https ${1:disable_redirects}", "email ${1:admin@example.com}", "default_sni ${1:example.com}", "fallback_sni ${1:example.com}",
	"local_certs", "skip_install_trust", "acme_ca ${1:https://acme-v02.api.letsencrypt.org/directory}", "acme_ca_root ${1:/path/to/ca.pem}",
	"acme_eab {\n\tkey_id ${1:id}\n\tmac_key ${2:key}\n}", "acme_dns ${1:provider}", "dns ${1:provider}", "ech ${1:example.com}",
	"on_demand_tls {\n\task ${1:https://example.com/authorize}\n}", "key_type ${1:p256}", "cert_issuer ${1:acme}",
	"renew_interval ${1:10m}", "cert_lifetime ${1:24h}", "ocsp_interval ${1:1h}", "ocsp_stapling ${1:off}",
	"renewal_window_ratio ${1:0.33}", "preferred_chains ${1:smallest}", "servers {\n\tprotocols ${1:h1 h2 h3}\n}",
	"pki {\n\tca ${1:local} {\n\t\tname ${2:Local CA}\n\t}\n}", "events {\n\ton ${1:cert_obtained} ${2:handler}\n}",
	"name ${1:server}", "listener_wrappers {\n\t$0\n}", "keepalive_interval ${1:15s}", "keepalive_idle ${1:30s}",
	"keepalive_count ${1:5}", "trusted_proxies static ${1:private_ranges}", "trusted_proxies_strict",
	"client_ip_headers ${1:X-Forwarded-For}", "trace", "max_header_size ${1:1MB}", "log_credentials",
	"protocols ${1:h1 h2 h3}", "strict_sni_host ${1:on}",
}

var caddyMatcherSnippets = []string{
	"path ${1:/api/*}", "host ${1:example.com}", "method ${1:GET}", "header ${1:Accept} ${2:application/json}",
	"query ${1:key}=${2:value}", "remote_ip ${1:192.0.2.0/24}", "client_ip ${1:192.0.2.0/24}", "protocol ${1:https}",
	"expression `${1:true}`", "not path ${1:/private/*}", "file ${1:/index.html}",
	"path_regexp ${1:name} ${2:^/api/(.*)$}", "header_regexp ${1:name} ${2:Header} ${3:pattern}",
}

var caddyCodeKeywords = func() map[string]bool {
	words := map[string]bool{}
	for _, group := range [][]string{caddyDirectiveSnippets, caddyGlobalSnippets, caddyMatcherSnippets} {
		for _, source := range group {
			words[strings.Fields(source)[0]] = true
		}
	}
	return words
}()

func caddyCompletions(indent, unit string, matcher bool) []editor.Completion {
	seen := map[string]bool{}
	var items []editor.Completion
	add := func(source string) {
		label := strings.Fields(source)[0]
		if label == "@matcher" {
			source = strings.Replace(source, "@matcher", "@${1:matcher}", 1)
		}
		if seen[label] {
			return
		}
		seen[label] = true
		lines := strings.Split(source, "\n")
		for index := 1; index < len(lines); index++ {
			trimmed := strings.TrimLeft(lines[index], "\t")
			lines[index] = indent + strings.Repeat(unit, len(lines[index])-len(trimmed)) + trimmed
		}
		snippet, err := editor.ParseSnippet(strings.Join(lines, "\n"))
		if err == nil {
			items = append(items, editor.Completion{Label: label, Snippet: snippet, Kind: editor.CompletionSnippet})
		}
	}
	groups := [][]string{caddyDirectiveSnippets, caddyGlobalSnippets, caddyMatcherSnippets}
	if matcher {
		groups = [][]string{caddyMatcherSnippets}
	} else {
		add("@matcher {\n\tpath ${2:/api/*}\n}")
	}
	for _, group := range groups {
		for _, source := range group {
			add(source)
		}
	}
	return items
}
