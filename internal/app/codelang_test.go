package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

func TestCodeEditorLanguages(t *testing.T) {
	for _, sample := range []struct {
		path, name, text, word string
		kind                   editor.TokenKind
	}{
		{"/etc/caddy/Caddyfile", "Caddyfile", "\treverse_proxy @api localhost:9000", "reverse_proxy", editor.TokenKeyword},
		{"/srv/site.caddy", "Caddyfile", "@api path /api/*", "@api", editor.TokenVariable},
		{"/srv/site.caddyfile", "Caddyfile", `respond "{http.request.uri} {$ENV}"`, "{$ENV}", editor.TokenEscape},
		{"/etc/nginx/sites-enabled/default", "Nginx", "proxy_pass http://$backend;", "$backend", editor.TokenVariable},
		{"/srv/nginx/proxy.conf", "Nginx", "server {", "server", editor.TokenKeyword},
		{"/lib/systemd/system/caddy.service", "systemd", "[Service]", "[Service]", editor.TokenHeading},
		{"/etc/systemd/caddy.timer", "systemd", "OnCalendar=daily", "OnCalendar", editor.TokenProperty},
		{"/etc/systemd/caddy.socket", "systemd", "ListenStream=8080", "ListenStream", editor.TokenProperty},
		{"/srv/.env.local", "dotenv / INI", "export CADDY_ADMIN=localhost:2019", "CADDY_ADMIN", editor.TokenProperty},
		{"/srv/config.ini", "dotenv / INI", "[server]", "[server]", editor.TokenHeading},
	} {
		t.Run(sample.path, func(t *testing.T) {
			lang := codeLanguage(sample.path, sample.text, "")
			if lang == nil || lang.Name != sample.name {
				t.Fatalf("language: %v", lang)
			}
			tokens, _ := lang.Tokenizer.Tokenize(sample.text, 0, nil)
			start := strings.Index(sample.text, sample.word)
			for _, token := range tokens {
				if token.End > start {
					if token.Kind != sample.kind || token.End < start+len(sample.word) {
						t.Fatalf("%q: tokens %+v", sample.word, tokens)
					}
					return
				}
			}
			t.Fatalf("no token for %q", sample.word)
		})
	}
	_, state := caddyCodeLanguage.Tokenizer.Tokenize("respond <<BODY", 0, nil)
	for _, line := range []string{"# not a comment", "OTHER"} {
		tokens, next := caddyCodeLanguage.Tokenizer.Tokenize(line, state, nil)
		if state == 0 || next != state || len(tokens) != 1 || tokens[0].Kind != editor.TokenString {
			t.Fatalf("heredoc: %v %d %d", tokens, state, next)
		}
	}
	if _, next := caddyCodeLanguage.Tokenizer.Tokenize("\tBODY", state, nil); next != 0 {
		t.Fatal("heredoc did not close")
	}
}

func TestCodeEditorSnippet(t *testing.T) {
	items := caddyCompletions("\t", "\t", false)
	for _, source := range caddyDirectiveSnippets {
		label := strings.Fields(source)[0]
		found := false
		for _, item := range items {
			if item.Label == label && item.Snippet != nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing valid snippet for %s", label)
		}
	}
	for _, item := range items {
		if item.Label == "handle_path" {
			state := &editor.State{Buffer: editor.NewBuffer("\t")}
			state.Select(1, 1)
			if !state.InsertSnippet(item.Snippet) || state.SelectedText() != "/api/*" || state.Buffer.String() != "\thandle_path /api/* {\n\t\treverse_proxy localhost:8080\n\t}" {
				t.Fatalf("snippet: %q, selected %q", state.Buffer.String(), state.SelectedText())
			}
			return
		}
	}
	t.Fatal("missing handle_path completion")
}
