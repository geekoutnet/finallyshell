package app

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

const (
	caddyGlobGeneral = iota
	caddyGlobPorts
	caddyGlobAdmin
	caddyGlobLogging
	caddyGlobTLS
	caddyGlobOnDemand
	caddyGlobServers
	caddyGlobPKI
	caddyGlobMetrics
	caddyGlobOther
	caddyGlobSnippets
	caddyGlobGroups
)

type caddyGlobalOption struct {
	group   int
	name    string
	example string
	form    caddyGlobalFormKind
	choices []string
	port    bool
}

type caddyGlobalFormKind uint8

const (
	caddyGlobSyntax caddyGlobalFormKind = iota
	caddyGlobFlag
	caddyGlobValue
	caddyGlobChoice
)

func caddyGlobalGroups() []string {
	return []string{locales.CglobGeneral(), locales.CglobPorts(), locales.CglobAdmin(), locales.CglobLogging(), locales.CglobTLS(),
		locales.CglobOnDemand(), locales.CglobServers(), locales.CglobPKI(), locales.CglobMetrics(), locales.CglobOther(), locales.CglobSnippets()}
}

func caddyGlobalValue(group int, name, example string) caddyGlobalOption {
	return caddyGlobalOption{group: group, name: name, example: name + " " + example, form: caddyGlobValue}
}

func caddyGlobalFlag(group int, name string) caddyGlobalOption {
	return caddyGlobalOption{group: group, name: name, example: name, form: caddyGlobFlag}
}

func caddyGlobalChoice(group int, name string, choices ...string) caddyGlobalOption {
	return caddyGlobalOption{group: group, name: name, example: name + " " + choices[0], form: caddyGlobChoice, choices: choices}
}

func caddyGlobalSyntax(group int, name, example string) caddyGlobalOption {
	return caddyGlobalOption{group: group, name: name, example: name + " " + example}
}

var caddyGlobalOptions = []caddyGlobalOption{
	caddyGlobalFlag(caddyGlobGeneral, "debug"),
	caddyGlobalSyntax(caddyGlobGeneral, "order", "respond before file_server"),
	caddyGlobalSyntax(caddyGlobGeneral, "storage", "file_system {\n\troot /var/lib/caddy/storage\n}"),
	caddyGlobalValue(caddyGlobGeneral, "storage_clean_interval", "24h"),
	caddyGlobalValue(caddyGlobGeneral, "grace_period", "30s"),
	caddyGlobalValue(caddyGlobGeneral, "shutdown_delay", "5s"),
	caddyGlobalSyntax(caddyGlobGeneral, "filesystem", "shared local {\n\troot /srv/shared\n}"),
	caddyGlobalSyntax(caddyGlobGeneral, "events", "{\n\t# on <event> <handler>\n}"),
	{group: caddyGlobPorts, name: "http_port", example: "http_port 80", form: caddyGlobValue, port: true},
	{group: caddyGlobPorts, name: "https_port", example: "https_port 443", form: caddyGlobValue, port: true},
	caddyGlobalValue(caddyGlobPorts, "default_bind", "127.0.0.1 ::1"),
	caddyGlobalSyntax(caddyGlobAdmin, "admin", "localhost:2019 {\n\torigins localhost:2019\n\tenforce_origin\n}"),
	caddyGlobalChoice(caddyGlobAdmin, "persist_config", "off"),
	caddyGlobalSyntax(caddyGlobLogging, "log", "{\n\toutput stderr\n\tformat json\n\tlevel INFO\n}"),
	caddyGlobalChoice(caddyGlobTLS, "auto_https", "off", "disable_redirects", "disable_certs", "ignore_loaded_certs"),
	caddyGlobalValue(caddyGlobTLS, "tls_automate_names", "example.test"),
	caddyGlobalValue(caddyGlobTLS, "email", "webmaster@example.test"),
	caddyGlobalValue(caddyGlobTLS, "default_sni", "example.test"),
	caddyGlobalValue(caddyGlobTLS, "fallback_sni", "example.test"),
	caddyGlobalFlag(caddyGlobTLS, "local_certs"),
	caddyGlobalFlag(caddyGlobTLS, "skip_install_trust"),
	caddyGlobalValue(caddyGlobTLS, "acme_ca", "https://acme-v02.api.letsencrypt.org/directory"),
	caddyGlobalValue(caddyGlobTLS, "acme_ca_root", "/etc/caddy/root-ca.pem"),
	caddyGlobalSyntax(caddyGlobTLS, "acme_eab", "{\n\tkey_id {$ACME_EAB_KEY_ID}\n\tmac_key {$ACME_EAB_MAC_KEY}\n}"),
	caddyGlobalSyntax(caddyGlobTLS, "acme_dns", "cloudflare {$CLOUDFLARE_API_TOKEN}"),
	caddyGlobalSyntax(caddyGlobTLS, "dns", "cloudflare {$CLOUDFLARE_API_TOKEN}"),
	caddyGlobalValue(caddyGlobTLS, "tls_resolvers", "1.1.1.1 8.8.8.8"),
	caddyGlobalSyntax(caddyGlobTLS, "ech", "example.test {\n\tdns cloudflare {$CLOUDFLARE_API_TOKEN}\n}"),
	caddyGlobalChoice(caddyGlobTLS, "key_type", "p256", "p384", "ed25519", "rsa2048", "rsa4096"),
	caddyGlobalSyntax(caddyGlobTLS, "cert_issuer", "acme"),
	caddyGlobalValue(caddyGlobTLS, "renew_interval", "10m"),
	caddyGlobalValue(caddyGlobTLS, "cert_lifetime", "2160h"),
	caddyGlobalValue(caddyGlobTLS, "ocsp_interval", "1h"),
	caddyGlobalChoice(caddyGlobTLS, "ocsp_stapling", "off"),
	caddyGlobalValue(caddyGlobTLS, "renewal_window_ratio", "0.33"),
	caddyGlobalSyntax(caddyGlobTLS, "preferred_chains", "smallest"),
	caddyGlobalSyntax(caddyGlobOnDemand, "on_demand_tls", "{\n\task https://example.test/check-domain\n}"),
	caddyGlobalSyntax(caddyGlobServers, "servers", "{\n\tprotocols h1 h2 h3\n\tmax_header_size 64KB\n\ttimeouts {\n\t\tread_header 5s\n\t}\n}"),
	caddyGlobalSyntax(caddyGlobPKI, "pki", "{\n\tca local {\n\t\tname \"Example CA\"\n\t}\n}"),
	caddyGlobalSyntax(caddyGlobMetrics, "metrics", "{\n\tper_host\n}"),
}

func caddyGlobalOptionFor(name string) (caddyGlobalOption, bool) {
	for _, option := range caddyGlobalOptions {
		if name == option.name {
			return option, true
		}
	}
	return caddyGlobalOption{}, false
}

var caddyGlobalInvalid = errors.New("invalid Caddyfile entry")

func caddyGlobalOptionNode(text, name string) (*caddyfile.Document, *caddyfile.Node, error) {
	if strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return nil, nil, caddyGlobalInvalid
	}
	doc, err := caddyfile.Parse("{\n" + text + "\n}\n")
	if err != nil || len(doc.Nodes) != 1 {
		return nil, nil, caddyGlobalInvalid
	}
	block := doc.GlobalBlock()
	if block == nil || len(block.Children) != 1 || block.Children[0].Name != name || block.Children[0].Kind != caddyfile.Directive {
		return nil, nil, caddyGlobalInvalid
	}
	return doc, block.Children[0], nil
}

func caddyGlobalOptionEdit(doc *caddyfile.Document, node *caddyfile.Node, option caddyGlobalOption, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		if node == nil {
			return doc.Source, nil
		}
		return doc.Replace(node, "")
	}
	_, parsed, err := caddyGlobalOptionNode(text, option.name)
	if err != nil || caddyGlobalSecret(option.name, text) || !caddyGlobalPortOK(option, parsed) {
		return "", caddyGlobalInvalid
	}
	if node == nil {
		if len(doc.GlobalOptions(option.name)) != 0 {
			if !caddyGlobalRepeatable(option.name) || doc.GlobalBlock() == nil {
				return "", caddyGlobalInvalid
			}
			return caddyGlobalInsertChild(doc.Source, doc.GlobalBlock().Close, text), nil
		}
		return doc.SetGlobal(option.name, text)
	}
	return doc.Replace(node, caddyGlobalNewlines(doc.Source, text))
}

func caddyGlobalRepeatable(name string) bool {
	switch name {
	case "servers", "log", "order", "filesystem", "cert_issuer":
		return true
	}
	return false
}

func caddyGlobalPortOK(option caddyGlobalOption, node *caddyfile.Node) bool {
	if !option.port {
		return true
	}
	if len(node.Args) != 1 || node.Block {
		return false
	}
	port := node.Args[0]
	if caddyfile.IsEnvReference(port) {
		return true
	}
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

func caddyGlobalNewlines(source, change string) string {
	if newline := caddyGlobalEOL(source); newline != "\n" {
		if change == "\n" {
			return newline
		}
		return caddyGlobalStyle(change, "", newline)
	}
	return change
}

func caddyGlobalEOL(source string) string {
	if first := strings.IndexByte(source, '\n'); first > 0 && source[first-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func caddyGlobalStyle(value, indent, newline string) string {
	if value == "" || indent == "" && newline == "\n" {
		return value
	}
	tokens, err := caddyfile.Lex(value)
	if err != nil {
		return value
	}
	var text strings.Builder
	start := 0
	for _, token := range tokens {
		if token.Kind == caddyfile.Newline {
			text.WriteString(value[start:token.Start])
			text.WriteString(newline + indent)
			start = token.End
		}
	}
	text.WriteString(value[start:])
	return text.String()
}

func caddyGlobalNodeIndent(doc *caddyfile.Document, node *caddyfile.Node) string {
	if node == nil {
		return ""
	}
	line := strings.LastIndexByte(doc.Source[:node.Start], '\n') + 1
	indent := doc.Source[line:node.Start]
	if strings.Trim(indent, " \t") != "" {
		return ""
	}
	return indent
}

func caddyGlobalDedent(doc *caddyfile.Document, node *caddyfile.Node) string {
	text, indent := doc.Text(node), caddyGlobalNodeIndent(doc, node)
	if indent == "" || !strings.Contains(text, "\n") {
		return text
	}
	tokens, err := caddyfile.Lex(text)
	if err != nil {
		return text
	}
	var result strings.Builder
	start := 0
	for _, token := range tokens {
		if token.Kind == caddyfile.Newline {
			if !strings.HasPrefix(text[token.End:], indent) {
				return text
			}
			result.WriteString(text[start:token.End])
			start = token.End + len(indent)
		}
	}
	result.WriteString(text[start:])
	return result.String()
}

func caddyGlobalRestoreIndent(doc *caddyfile.Document, node *caddyfile.Node, value string) string {
	indent := caddyGlobalNodeIndent(doc, node)
	if indent == "" || caddyGlobalDedent(doc, node) == doc.Text(node) && strings.Contains(doc.Text(node), "\n") {
		return value
	}
	return caddyGlobalStyle(value, indent, caddyGlobalEOL(doc.Source))
}

func caddyGlobalSecret(name, text string) bool {
	if systemRedact(text) != text {
		return true
	}
	if name == "acme_eab" || name == "dns" || name == "acme_dns" || name == "ech" {
		_, node, err := caddyGlobalOptionNode(text, name)
		if err == nil {
			return caddyGlobalSecretArguments(node)
		}
	}
	return false
}

func caddyGlobalSecretArguments(node *caddyfile.Node) bool {
	args := node.Args
	switch node.Name {
	case "acme_dns", "dns":
		if len(args) > 0 {
			args = args[1:]
		}
	case "mac_key", "api_token", "api_key", "secret", "password", "credentials", "access_key", "secret_key":
	default:
		args = nil
	}
	for _, value := range args {
		if !caddyfile.IsEnvReference(value) {
			return true
		}
	}
	for _, child := range node.Children {
		if caddyGlobalSecretArguments(child) {
			return true
		}
	}
	return false
}

type caddyGlobalRow struct {
	key, name string
	preview   string
	file      monitor.CaddyFile
	doc       *caddyfile.Document
	node      *caddyfile.Node
	option    caddyGlobalOption
	unknown   bool
	protected bool
}

func caddyGlobalRows(file monitor.CaddyFile, doc *caddyfile.Document) (rows [caddyGlobGroups][]caddyGlobalRow) {
	seen := map[string]bool{}
	if block := doc.GlobalBlock(); block != nil {
		for _, node := range block.Children {
			option, known := caddyGlobalOptionFor(node.Name)
			row := caddyGlobalRow{key: node.Name, name: node.Name, file: file, doc: doc, node: node, option: option, unknown: !known}
			if !known || seen[node.Name] {
				row.key = fmt.Sprintf("%s:%d:%d", node.Name, node.Line, node.Start)
			}
			raw := doc.Text(node)
			row.protected = caddyGlobalSecret(node.Name, raw)
			if !row.protected {
				row.preview = strings.Join(strings.Fields(systemTail(raw, 4000)), " ")
			}
			if known {
				rows[option.group] = append(rows[option.group], row)
			} else {
				rows[caddyGlobOther] = append(rows[caddyGlobOther], row)
			}
			seen[node.Name] = true
		}
	}
	for _, option := range caddyGlobalOptions {
		if !seen[option.name] {
			rows[option.group] = append(rows[option.group], caddyGlobalRow{key: option.name, name: option.name, file: file, doc: doc, option: option})
		}
	}
	return rows
}

func caddyGlobalSnippetRows(files []monitor.CaddyFile) (rows []caddyGlobalRow) {
	for _, file := range files {
		doc, err := caddyfile.Parse(file.Content)
		if err != nil {
			continue
		}
		for _, node := range doc.Snippets() {
			key := fmt.Sprintf("%s:%d", file.Path, node.Start)
			rows = append(rows, caddyGlobalRow{key: key, name: node.Name, file: file, doc: doc, node: node, protected: caddyGlobalSecret(node.Name, doc.Text(node))})
		}
	}
	return rows
}

func caddyGlobalSnippetName(text string) string {
	if strings.ContainsRune(text, 0) || !utf8.ValidString(text) {
		return ""
	}
	doc, err := caddyfile.Parse(text)
	if err != nil || len(doc.Nodes) != 1 || doc.Nodes[0].Kind != caddyfile.Snippet || !doc.Nodes[0].Block {
		return ""
	}
	name := doc.Nodes[0].Name
	if name == "" || strings.ContainsAny(name, " \t\r\n(){}#\"`") {
		return ""
	}
	return name
}

func caddyGlobalSnippetConflict(rows []caddyGlobalRow, old *caddyGlobalRow, name string) bool {
	for _, row := range rows {
		if row.name == name && (old == nil || row.file.Path != old.file.Path || old.node == nil || row.node.Start != old.node.Start) {
			return true
		}
	}
	return false
}
