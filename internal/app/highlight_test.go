package app

import (
	"bytes"
	"strings"
	"testing"

	"finallyshell/internal/config"
)

func TestHighlightStream(t *testing.T) {
	engine := newHighlighter(highlightRules(config.Settings{}, "demo"))
	var out []byte
	for _, fragment := range []string{"\x1b[35merr", "or 世界\r\n", "\x1b]0;error", "\x07\x1bPwarning\x1b\\", "\x1b[?1049h error\r\n", "\x1b[?1049l success\r\n"} {
		out = append(out, engine.process([]byte(fragment), false)...)
	}
	out = append(out, engine.process(nil, true)...)
	for _, want := range []string{"\x1b[31merror\x1b[0m\x1b[35m 世界", "\x1b]0;error\x07\x1bPwarning\x1b\\", "\x1b[?1049h error\r\n", "\x1b[32msuccess"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestHighlightRules(t *testing.T) {
	settings := config.Settings{Highlight: &config.TerminalHighlight{UseCustom: true, Rules: []config.HighlightRule{{Pattern: "工作", Host: "demo", Color: "cyan", Bold: true, CaseSensitive: true}}}}
	for _, host := range []string{"demo", "other"} {
		engine := newHighlighter(highlightRules(settings, host))
		out := string(engine.process([]byte("工作 工作中\n"), true))
		if got := strings.Count(out, "\x1b[36;1m"); got != map[string]int{"demo": 1, "other": 0}[host] {
			t.Fatalf("%s: %q", host, out)
		}
	}
}

func TestHighlightANSI(t *testing.T) {
	var state ansiState
	for _, value := range []byte("\x1b[31m") {
		if kind := state.next(value); kind != ansiEscape {
			t.Fatalf("at %q: kind %d state %d", value, kind, state.state)
		}
	}
}

func TestHighlightStateDuringToggle(t *testing.T) {
	engine := newHighlighter(nil)
	engine.process([]byte("\x9b?1049h"), false)
	engine.rules = highlightRules(config.Settings{}, "demo")
	data := "error\r\n\x9d0;fatal\x9c\x90warning\x9c\x9b?1049lsuccess 🧛\r\n"
	want := strings.Replace(data, "lsuccess", "l\x1b[32msuccess\x1b[0m", 1)
	if output := string(engine.process([]byte(data), true)); output != want {
		t.Fatalf("got %q, want %q", output, want)
	}
}

func BenchmarkHighlight10MB(b *testing.B) {
	block := []byte(strings.Repeat("worker: warning 192.0.2.10 service https://example.invalid/api is active\n", 32))
	rules := highlightRules(config.Settings{}, "demo")
	b.SetBytes(10 << 20)
	for b.Loop() {
		engine := newHighlighter(rules)
		for remaining := 10 << 20; remaining > 0; remaining -= len(block) {
			engine.process(block[:min(len(block), remaining)], false)
		}
	}
}
