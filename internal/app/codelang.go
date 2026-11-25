package app

import (
	"hash/fnv"
	"path"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

var (
	caddyCodeLanguage = &editor.Language{
		Name: "Caddyfile", Filenames: []string{"Caddyfile"}, Extensions: []string{".caddy", ".caddyfile"},
		LineComment: "#", WordChars: "@-", HardTabs: true, TabSize: 4,
		Brackets: configBrackets(true), AutoClose: configAutoClose(true), Imports: regexp.MustCompile(`^\s*import\s`),
		Tokenizer: editor.TokenizerFunc(lexCaddyCode),
	}
	nginxCodeLanguage = &editor.Language{
		Name: "Nginx", Filenames: []string{"nginx.conf"}, LineComment: "#", WordChars: "-$", TabSize: 4,
		Brackets: configBrackets(false), AutoClose: configAutoClose(false), Imports: regexp.MustCompile(`^\s*include\s`),
		Tokenizer: editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
			return lexConfigCode(line, state, tokens, false)
		}),
	}
	systemdCodeLanguage = &editor.Language{
		Name: "systemd", LineComment: "#", Extensions: []string{".service", ".timer", ".socket", ".target", ".mount", ".path", ".slice", ".automount", ".swap", ".scope"},
		WordChars: "-", Tokenizer: codeINITokenizer(true),
	}
	iniCodeLanguage = &editor.Language{
		Name: "dotenv / INI", LineComment: "#", Filenames: []string{".env"}, Extensions: []string{".env", ".ini", ".cfg"},
		WordChars: "-", Tokenizer: codeINITokenizer(false),
	}
	configCodeLanguages = []*editor.Language{caddyCodeLanguage, nginxCodeLanguage, systemdCodeLanguage, iniCodeLanguage}
)

func configBrackets(caddy bool) []editor.BracketPair {
	pairs := []editor.BracketPair{{Open: "{", Close: "}"}}
	if caddy {
		pairs = append(pairs, editor.BracketPair{Open: "(", Close: ")"})
	}
	return pairs
}

func configAutoClose(caddy bool) []editor.BracketPair {
	pairs := configBrackets(caddy)
	quotes := `"'`
	if caddy {
		quotes = "\"`"
	}
	for _, quote := range quotes {
		pairs = append(pairs, editor.BracketPair{Open: string(quote), Close: string(quote), NotIn: []editor.TokenKind{editor.TokenString, editor.TokenComment}})
	}
	return pairs
}

func codeLanguage(file, text, preferred string) *editor.Language {
	if preferred != "" {
		if strings.EqualFold(preferred, "plain") {
			return nil
		}
		for _, lang := range configCodeLanguages {
			if strings.EqualFold(preferred, lang.Name) || lang == iniCodeLanguage && (strings.EqualFold(preferred, "ini") || strings.EqualFold(preferred, "dotenv") || strings.EqualFold(preferred, "env")) {
				return lang
			}
		}
		if lang := editor.LanguageNamed(preferred); lang != nil {
			return lang
		}
	}
	lower := strings.ToLower(file)
	base, ext := path.Base(lower), path.Ext(lower)
	switch {
	case strings.HasPrefix(lower, "/etc/nginx/"), strings.Contains(lower, "/nginx/") && ext == ".conf":
		return nginxCodeLanguage
	case strings.Contains(lower, "/systemd/") && ext == ".conf":
		return systemdCodeLanguage
	case strings.HasPrefix(base, ".env."):
		return iniCodeLanguage
	}
	for _, lang := range configCodeLanguages {
		for _, name := range lang.Filenames {
			if strings.EqualFold(base, name) {
				return lang
			}
		}
		for _, suffix := range lang.Extensions {
			if ext == suffix {
				return lang
			}
		}
	}
	return languageOf(file, text)
}

func lexCaddyCode(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	if state > 255 {
		if caddyHeredocState(strings.TrimSpace(line)) == state {
			state = 0
		}
		if len(line) != 0 {
			tokens = append(tokens, editor.Token{End: len(line), Kind: editor.TokenString})
		}
		return tokens, state
	}
	return lexConfigCode(line, state, tokens, true)
}

func caddyHeredocState(delimiter string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(delimiter))
	return hash.Sum64() | 256
}

var (
	configPlaceholder = regexp.MustCompile(`^\{[^{}\s]+\}`)
	configVariable    = regexp.MustCompile(`^\$(?:\{[A-Za-z_0-9]+\}|[A-Za-z_0-9]+)`)
	caddySnippetName  = regexp.MustCompile(`^\([a-zA-Z_0-9.-]+\)`)
	caddyHeredocStart = regexp.MustCompile(`^<<([A-Za-z_0-9-]+)$`)
)

func lexConfigCode(line string, state uint64, tokens []editor.Token, caddy bool) ([]editor.Token, uint64) {
	emit := func(end int, kind editor.TokenKind) { tokens = append(tokens, editor.Token{End: end, Kind: kind}) }
	quote := byte(state)
	first := quote == 0
	for offset := 0; offset < len(line); {
		tail := line[offset:]
		char := tail[0]
		if quote != 0 {
			if char == '\\' && quote != '`' && len(tail) > 1 {
				offset += 2
				emit(offset, editor.TokenEscape)
				continue
			}
			if size, kind := configInterpolation(tail, caddy); size != 0 && quote != '\'' {
				offset += size
				emit(offset, kind)
				continue
			}
			if char == quote {
				quote = 0
			}
			offset++
			if len(tokens) != 0 && tokens[len(tokens)-1].Kind == editor.TokenString {
				tokens[len(tokens)-1].End = offset
			} else {
				emit(offset, editor.TokenString)
			}
			continue
		}
		switch {
		case char == ' ' || char == '\t':
			offset++
			emit(offset, editor.TokenText)
			continue
		case char == '#':
			emit(len(line), editor.TokenComment)
			return tokens, 0
		case char == '"' || char == '`' && caddy || char == '\'' && !caddy:
			quote = char
			offset++
			emit(offset, editor.TokenString)
		case caddy && char == '<' && caddyHeredocStart.MatchString(strings.TrimSpace(tail)):
			delimiter := caddyHeredocStart.FindStringSubmatch(strings.TrimSpace(tail))[1]
			emit(len(line), editor.TokenString)
			return tokens, caddyHeredocState(delimiter)
		default:
			if size, kind := configInterpolation(tail, caddy); size != 0 {
				offset += size
				emit(offset, kind)
			} else if caddy && first && char == '(' && caddySnippetName.MatchString(tail) {
				offset += len(caddySnippetName.FindString(tail))
				emit(offset, editor.TokenFunction)
			} else if strings.ContainsRune("{};()", rune(char)) {
				offset++
				emit(offset, editor.TokenPunctuation)
				first = char == ';' || char == '{' || char == '}'
				continue
			} else {
				size := 1
				for size < len(tail) && !strings.ContainsRune(" \t{};()\"`'$", rune(tail[size])) {
					size++
				}
				word := tail[:size]
				kind := editor.TokenText
				switch {
				case caddy && strings.HasPrefix(word, "@"):
					kind = editor.TokenVariable
				case caddy && caddyCodeKeywords[word]:
					kind = editor.TokenKeyword
				case !caddy && first:
					kind = editor.TokenKeyword
				}
				offset += size
				emit(offset, kind)
			}
		}
		first = false
	}
	return tokens, uint64(quote)
}

func configInterpolation(text string, caddy bool) (int, editor.TokenKind) {
	if caddy && text[0] == '{' {
		return len(configPlaceholder.FindString(text)), editor.TokenEscape
	}
	if !caddy && text[0] == '$' {
		return len(configVariable.FindString(text)), editor.TokenVariable
	}
	return 0, editor.TokenText
}

var configINIKey = regexp.MustCompile(`^[\w.-]+\s*(?:=|:)`)

func codeINITokenizer(systemd bool) editor.Tokenizer {
	return editor.TokenizerFunc(func(line string, _ uint64, tokens []editor.Token) ([]editor.Token, uint64) {
		emit := func(end int, kind editor.TokenKind) { tokens = append(tokens, editor.Token{End: end, Kind: kind}) }
		rest := strings.TrimLeft(line, " \t")
		start := len(line) - len(rest)
		if start > 0 {
			emit(start, editor.TokenText)
		}
		if rest == "" {
			return tokens, 0
		}
		switch rest[0] {
		case '#', ';':
			emit(len(line), editor.TokenComment)
			return tokens, 0
		case '[':
			if end := strings.IndexByte(rest, ']'); end > 0 {
				emit(start+end+1, editor.TokenHeading)
				start += end + 1
			}
		}
		if !systemd && strings.HasPrefix(line[start:], "export ") {
			emit(start+6, editor.TokenKeyword)
			start += 6
			offset := len(line) - len(strings.TrimLeft(line[start:], " \t"))
			emit(offset, editor.TokenText)
			start = offset
		}
		if key := configINIKey.FindString(line[start:]); key != "" {
			emit(start+len(strings.TrimRight(key[:len(key)-1], " \t")), editor.TokenProperty)
			start += len(key)
			emit(start, editor.TokenOperator)
		}
		if start < len(line) {
			value := strings.TrimLeft(line[start:], " \t")
			kind := editor.TokenText
			if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") {
				kind = editor.TokenString
			} else if value == "true" || value == "false" || value == "yes" || value == "no" {
				kind = editor.TokenConstant
			}
			emit(len(line), kind)
		}
		return tokens, 0
	})
}
