package app

import (
	"bytes"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"finallyshell/internal/config"
)

var highlightColors = []string{"red", "yellow", "green", "cyan", "blue", "magenta"}
var highlightSGR = []string{"31", "33", "32", "36", "34", "35"}

func defaultHighlightRules() []config.HighlightRule {
	return []config.HighlightRule{
		{Pattern: `https?://[^\s<>"'\x00-\x20]+`, Regex: true, Color: "blue", Underline: true},
		{Pattern: `\b(error|fail|failed|fatal|denied|refused)\b`, Regex: true, Color: "red"},
		{Pattern: `\b(warn|warning)\b`, Regex: true, Color: "yellow"},
		{Pattern: `\b(success|ok|done|active)\b`, Regex: true, Color: "green"},
		{Pattern: defaultIPPattern, Regex: true, Color: "cyan"},
	}
}

type compiledHighlight struct {
	match *regexp.Regexp
	sgr   string
	word  bool
	ip    bool
}

func highlightRules(settings config.Settings, host string) []compiledHighlight {
	rules := defaultHighlightRules()
	useDefaults := true
	if cfg := settings.Highlight; cfg != nil {
		if cfg.Disabled {
			return nil
		}
		if cfg.UseCustom {
			rules = cfg.Rules
			useDefaults = false
		}
	}
	if useDefaults {
		return builtInHighlights()
	}
	var compiled []compiledHighlight
	for _, rule := range rules[:min(len(rules), 48)] {
		if rule.Host != "" && rule.Host != host {
			continue
		}
		if entry, err := compileHighlight(rule); err == nil && entry.match != nil {
			compiled = append(compiled, entry)
		}
	}
	return compiled
}

var builtInHighlights = sync.OnceValue(func() []compiledHighlight {
	var entries []compiledHighlight
	for _, rule := range defaultHighlightRules() {
		compiled, err := compileHighlight(rule)
		if err != nil {
			panic(err)
		}
		entries = append(entries, compiled)
	}
	return entries
})

func compileHighlight(rule config.HighlightRule) (compiledHighlight, error) {
	if rule.Pattern == "" || len(rule.Pattern) > 512 {
		return compiledHighlight{}, nil
	}
	pattern := rule.Pattern
	if !rule.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !rule.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	match, err := regexp.Compile(pattern)
	if err != nil {
		return compiledHighlight{}, err
	}
	color := slices.Index(highlightColors, rule.Color)
	style := highlightSGR[max(0, color)]
	if rule.Bold {
		style += ";1"
	}
	if rule.Underline {
		style += ";4"
	}
	return compiledHighlight{match: match, word: !rule.Regex, sgr: "\x1b[" + style + "m", ip: rule.Pattern == defaultIPPattern && rule.Regex}, nil
}

const defaultIPPattern = `(?:[0-9a-f]{0,4}:){2,}[0-9a-f:.]*|(?:[0-9]{1,3}\.){3}[0-9]{1,3}`
const highlightWindow = 2048

type highlighter struct {
	rules     []compiledHighlight
	ansi      ansiState
	text      []byte
	escape    []byte
	sgr       []byte
	sgrUnsafe bool
	alt       uint8
	spans     []highlightSpan
}

func newHighlighter(rules []compiledHighlight) *highlighter { return &highlighter{rules: rules} }

func (engine *highlighter) process(data []byte, flush bool) []byte {
	out := make([]byte, 0, len(data)+32)
	for _, value := range data {
		before := engine.ansi.state
		kind := engine.ansi.next(value)
		if kind == ansiText {
			if engine.alt != 0 || engine.sgrUnsafe || len(engine.rules) == 0 {
				out = append(out, value)
			} else {
				engine.text = append(engine.text, value)
				if len(engine.text) >= highlightWindow && utf8Boundary(engine.text) == len(engine.text) {
					out = engine.flush(out)
				}
			}
			continue
		}
		if len(engine.text) != 0 {
			out = engine.flush(out)
			out = append(out, engine.text...)
			engine.text = engine.text[:0]
		}
		out = append(out, value)
		if kind == ansiEscape {
			if before == ansiGround || value == 0x1b && before != ansiString && before != ansiStringEnd {
				engine.escape = engine.escape[:0]
			}
			if len(engine.escape) < 512 {
				if before == ansiGround && value == 0x9b {
					engine.escape = append(engine.escape, '\x1b', '[')
				} else {
					engine.escape = append(engine.escape, value)
				}
			}
			if engine.ansi.state == ansiGround {
				engine.applyEscape()
			}
		}
	}
	if flush {
		out = engine.flush(out)
	}
	return out
}

func (engine *highlighter) applyEscape() {
	escape := engine.escape
	if bytes.Equal(escape, []byte("\x1bc")) {
		engine.alt, engine.sgrUnsafe, engine.sgr = 0, false, engine.sgr[:0]
	}
	if len(escape) < 3 || len(escape) >= 512 || escape[0] != 0x1b || escape[1] != '[' {
		return
	}
	last := escape[len(escape)-1]
	params := string(escape[2 : len(escape)-1])
	if (last == 'h' || last == 'l') && strings.HasPrefix(params, "?") {
		for _, mode := range strings.Split(params[1:], ";") {
			bit := map[string]uint8{"47": 1, "1047": 2, "1049": 4}[mode]
			if last == 'h' {
				engine.alt |= bit
			} else if bit != 0 {
				engine.alt = 0
			}
		}
	}
	if last != 'm' || engine.alt != 0 {
		return
	}
	if params == "" || params == "0" || strings.HasPrefix(params, "0;") {
		engine.sgr, engine.sgrUnsafe = engine.sgr[:0], false
	}
	if params != "" && params != "0" {
		if len(engine.sgr)+len(escape) > 512 {
			engine.sgrUnsafe = true
		} else {
			engine.sgr = append(engine.sgr, escape...)
		}
	}
}

type highlightSpan struct{ start, end, rule int }

func (engine *highlighter) flush(out []byte) []byte {
	cut := utf8Boundary(engine.text)
	if cut == 0 {
		return out
	}
	if len(engine.rules) == 0 {
		out = append(out, engine.text[:cut]...)
	} else {
		out = engine.decorate(out, engine.text[:cut])
	}
	engine.text = append(engine.text[:0], engine.text[cut:]...)
	return out
}

func (engine *highlighter) decorate(out, text []byte) []byte {
	engine.spans = matchingSpans(engine.spans[:0], engine.rules, text)
	last := 0
	for _, span := range engine.spans {
		if span.start < last {
			continue
		}
		out = append(out, text[last:span.start]...)
		out = append(out, engine.rules[span.rule].sgr...)
		out = append(out, text[span.start:span.end]...)
		out = append(out, "\x1b[0m"...)
		out = append(out, engine.sgr...)
		last = span.end
	}
	return append(out, text[last:]...)
}

func matchingSpans(spans []highlightSpan, rules []compiledHighlight, text []byte) []highlightSpan {
	for index, rule := range rules {
		for _, match := range rule.match.FindAllIndex(text, -1) {
			start, end := match[0], match[1]
			if start == end || rule.word && !wordBoundary(text, start, end) {
				continue
			}
			if rule.ip {
				end = start + len(bytes.TrimRight(text[start:end], "."))
				if _, err := netip.ParseAddr(string(text[start:end])); err != nil || !wordBoundary(text, start, end) {
					continue
				}
			}
			spans = append(spans, highlightSpan{start, end, index})
		}
	}
	slices.SortFunc(spans, func(left, right highlightSpan) int {
		if left.start != right.start {
			return left.start - right.start
		}
		return left.rule - right.rule
	})
	return spans
}

func wordBoundary(text []byte, start, end int) bool {
	prev, _ := utf8.DecodeLastRune(text[:start])
	next, _ := utf8.DecodeRune(text[end:])
	word := func(char rune) bool {
		return unicode.IsLetter(char) || unicode.IsDigit(char) || unicode.IsMark(char) || char == '_'
	}
	return !word(prev) && !word(next)
}

func utf8Boundary(data []byte) int {
	for index := len(data) - 1; index >= 0 && index >= len(data)-utf8.UTFMax; index-- {
		if utf8.RuneStart(data[index]) {
			if !utf8.FullRune(data[index:]) {
				return index
			}
			break
		}
	}
	return len(data)
}

const (
	ansiGround byte = iota
	ansiEsc
	ansiIntermediate
	ansiCSI
	ansiString
	ansiStringEnd
)

const (
	ansiText byte = iota
	ansiControl
	ansiEscape
	ansiOpaque
)

type ansiState struct {
	state, utfLeft byte
	osc            bool
}

func (parser *ansiState) next(value byte) byte {
	if parser.utfLeft != 0 {
		if value&0xc0 == 0x80 {
			parser.utfLeft--
			if parser.state == ansiString || parser.state == ansiStringEnd {
				return ansiOpaque
			}
			return ansiText
		}
		parser.utfLeft = 0
	}
	if parser.state == ansiGround || parser.state == ansiString {
		switch {
		case value >= 0xc2 && value <= 0xdf:
			parser.utfLeft = 1
		case value >= 0xe0 && value <= 0xef:
			parser.utfLeft = 2
		case value >= 0xf0 && value <= 0xf4:
			parser.utfLeft = 3
		}
	}
	if parser.state != ansiGround && (value == 0x18 || value == 0x1a) {
		kind := byte(ansiEscape)
		if parser.state == ansiString || parser.state == ansiStringEnd {
			kind = ansiOpaque
		}
		parser.state = ansiGround
		return kind
	}
	switch parser.state {
	case ansiString, ansiStringEnd:
		if parser.state == ansiStringEnd && value == '\\' || parser.osc && value == 7 || value == 0x9c {
			parser.state = ansiGround
		} else if value == 0x1b {
			parser.state = ansiStringEnd
		} else {
			parser.state = ansiString
		}
		return ansiOpaque
	case ansiEsc:
		switch value {
		case ']', 'P', 'X', '^', '_':
			parser.state, parser.osc = ansiString, value == ']'
			return ansiOpaque
		case '[':
			parser.state = ansiCSI
		case 0x1b:
		default:
			if value >= 0x20 && value <= 0x2f {
				parser.state = ansiIntermediate
			} else if value >= 0x30 {
				parser.state = ansiGround
			}
		}
		return ansiEscape
	case ansiCSI, ansiIntermediate:
		if value == 0x1b {
			parser.state = ansiEsc
		} else if value >= 0x40 && value <= 0x7e || parser.state == ansiIntermediate && value >= 0x30 && value <= 0x7e {
			parser.state = ansiGround
		}
		return ansiEscape
	}
	switch value {
	case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
		parser.state, parser.osc = ansiString, value == 0x9d
		return ansiOpaque
	case 0x9b:
		parser.state = ansiCSI
		return ansiEscape
	case 0x1b:
		parser.state = ansiEsc
		return ansiEscape
	}
	if value < 0x20 || value >= 0x7f && value < 0xa0 {
		return ansiControl
	}
	return ansiText
}
