package app

import (
	"strings"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type caddyGlobalNestedOption struct {
	name, example string
	block         bool
}

var caddyGlobalNestedOptions = map[string][]caddyGlobalNestedOption{
	"admin": {
		{"origins", "localhost:2019", false}, {"enforce_origin", "", false},
	},
	"log": {
		{"output", "stderr", false}, {"format", "json", false}, {"level", "INFO", false},
		{"include", "http.log.access", false}, {"exclude", "http.log.error", false},
	},
	"on_demand_tls": {
		{"ask", "https://example.test/check-domain", false}, {"permission", "http", false},
	},
	"servers": {
		{"name", "public", false}, {"protocols", "h1 h2 h3", false},
		{"listener_wrappers", "{\n\t# <module>\n}", true},
		{"trusted_proxies", "static private_ranges", false}, {"trusted_proxies_strict", "", false},
		{"trusted_proxies_unix", "", false}, {"client_ip_headers", "X-Forwarded-For", false},
		{"timeouts", "{\n\tread_header 5s\n\tread_body 15s\n\twrite 30s\n\tidle 5m\n}", true},
		{"keepalive_interval", "30s", false}, {"keepalive_idle", "5m", false}, {"keepalive_count", "9", false},
		{"max_header_size", "64KB", false}, {"trace", "", false}, {"log_credentials", "", false},
		{"strict_sni_host", "on", false}, {"metrics", "", false},
	},
	"pki": {
		{"ca", "local {\n\tname \"Example CA\"\n\tintermediate_lifetime 168h\n}", true},
	},
	"metrics": {
		{"per_host", "", false}, {"observe_catchall_hosts", "", false}, {"otlp", "", false},
	},
}

type caddyGlobalNestedForm struct {
	index   int
	source  string
	value   string
	enabled bool
}

func (form *caddyGlobalForm) drawNested(c *ui.Context, height float32) {
	options := caddyGlobalNestedOptions[form.row.name]
	if len(options) == 0 || form.fetching {
		return
	}
	state := &form.nested
	choices := make([]string, 0, len(options)+1)
	choices = append(choices, locales.CglobUnset())
	for _, option := range options {
		choices = append(choices, option.name)
	}
	ui.Text(c, locales.CglobServerField()).FontSize(12)
	changed := systemSelect(c.Key("nested-choice"), &state.index, locales.CglobServerField(), choices)
	if state.index == 0 {
		return
	}
	option := options[state.index-1]
	if changed || state.source != form.text {
		state.source = form.text
		state.value, state.enabled = caddyGlobalNestedValue(state.source, form.row.name, option.name)
	}
	if option.block {
		ui.TextArea(c.Key(option.name), &state.value).Label(option.name).Placeholder(option.example).FillWidth().Font("monospace").Height(min(100, height/5))
	} else if option.example == "" {
		ui.Checkbox(c.Key(option.name), &state.enabled, option.name)
	} else {
		ui.TextInput(c.Key(option.name), &state.value).Label(option.name).Placeholder(option.example).FillWidth().Font("monospace")
	}
	value := strings.TrimSpace(state.value)
	if option.example == "" && state.enabled {
		value = option.name
	} else if value != "" {
		value = option.name + " " + value
	}
	updated, err := caddyGlobalNestedEdit(form.text, form.row.name, option.name, value)
	if err != nil {
		widgets.Muted(c, locales.CglobInvalidNested()).TextColor(theme.Warning).FontSize(12)
	}
	if ui.Button(c, locales.CglobStage()).Disabled(err != nil || updated == form.text).AlignSelf(ui.Start).Clicked() && err == nil {
		form.text = updated
	}
}

func caddyGlobalChild(node *caddyfile.Node, name string) (*caddyfile.Node, error) {
	var found *caddyfile.Node
	for _, child := range node.Children {
		if child.Name == name {
			if found != nil {
				return nil, caddyGlobalInvalid
			}
			found = child
		}
	}
	return found, nil
}

func caddyGlobalNestedValue(text, parent, key string) (string, bool) {
	doc, node, err := caddyGlobalOptionNode(text, parent)
	if err != nil {
		return "", false
	}
	child, err := caddyGlobalChild(node, key)
	if err != nil || child == nil {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(doc.Text(child), key))
	return value, true
}

func caddyGlobalNestedEdit(text, parent, key, value string) (string, error) {
	if strings.TrimSpace(text) == "" {
		if value == "" {
			return text, nil
		}
		if parent == "admin" {
			text = "admin localhost:2019 {\n}"
		} else {
			text = parent + " {\n}"
		}
	}
	_, node, err := caddyGlobalOptionNode(text, parent)
	if err != nil {
		return "", err
	}
	child, err := caddyGlobalChild(node, key)
	if err != nil {
		return "", err
	}
	if value != "" {
		if _, _, err = caddyGlobalOptionNode(value, key); err != nil {
			return "", err
		}
	}
	if child != nil {
		value = caddyGlobalIndentNested(text, child.Start-2, value)
		return text[:child.Start-2] + value + text[child.End-2:], nil
	}
	if value == "" {
		return text, nil
	}
	if !node.Block {
		newline := caddyGlobalNewlines(text, "\n")
		text = text[:node.End-2] + " {" + newline + "}" + text[node.End-2:]
		_, node, err = caddyGlobalOptionNode(text, parent)
		if err != nil {
			return "", err
		}
	}
	return caddyGlobalInsertChild(text, node.Close-2, value), nil
}

func caddyGlobalIndentNested(text string, position int, value string) string {
	line := strings.LastIndexByte(text[:position], '\n') + 1
	indent := text[line:position]
	if strings.Trim(indent, " \t") != "" || indent == "" {
		return value
	}
	return caddyGlobalStyle(value, indent, caddyGlobalEOL(text))
}
