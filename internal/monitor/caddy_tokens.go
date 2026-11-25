package monitor

import "strings"

type caddyToken struct {
	value            string
	start, end, line int
	quoted           bool
}

func caddySpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\r' || char == '\n'
}

func caddyTokens(text string) ([]caddyToken, error) {
	var tokens []caddyToken
	line := 1
	for index := 0; index < len(text); {
		if caddySpace(text[index]) {
			if text[index] == '\n' {
				line++
			}
			index++
			continue
		}
		if text[index] == '#' || (text[index] == '\\' && index+1 < len(text) && text[index+1] == '\n') {
			for index < len(text) && text[index] != '\n' {
				index++
			}
			continue
		}
		token := caddyToken{start: index, line: line}
		var word strings.Builder
		if quote := text[index]; quote == '"' || quote == '`' {
			token.quoted = true
			index++
			for index < len(text) && text[index] != quote {
				if quote == '"' && text[index] == '\\' && index+1 < len(text) && text[index+1] == '"' {
					index++
				}
				if text[index] == '\n' {
					line++
				}
				word.WriteByte(text[index])
				index++
			}
			if index == len(text) {
				return tokens, ErrCaddyInvalid
			}
			index++
		} else {
			for index < len(text) && !caddySpace(text[index]) {
				word.WriteByte(text[index])
				index++
			}
			if marker, ok := strings.CutPrefix(word.String(), "<<"); ok && marker != "" && !strings.HasPrefix(marker, "<") {
				until := strings.IndexByte(text[index:], '\n')
				if until < 0 || strings.TrimSpace(text[index:index+until]) != "" {
					return tokens, ErrCaddyInvalid
				}
				index += until + 1
				line++
				for {
					row, _, _ := strings.Cut(text[index:], "\n")
					index += len(row)
					if strings.TrimSpace(row) == marker {
						break
					}
					if index == len(text) {
						return tokens, ErrCaddyInvalid
					}
					index++
					line++
				}
				token.quoted = true
			}
		}
		token.end, token.value = index, word.String()
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func (token caddyToken) is(word string) bool { return !token.quoted && token.value == word }

func caddyDirectives(tokens []caddyToken, name string) []int {
	var found []int
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].is(name) && tokens[index+1].line == tokens[index].line &&
			(index == 0 || tokens[index-1].line != tokens[index].line || tokens[index-1].is("{")) {
			found = append(found, index)
		}
	}
	return found
}

func caddySnippets(tokens []caddyToken, snippets map[string]bool) {
	depth := 0
	for index, token := range tokens {
		switch {
		case token.is("{"):
			if depth == 0 && index > 0 {
				previous := tokens[index-1]
				if !previous.quoted && strings.HasPrefix(previous.value, "(") && strings.HasSuffix(previous.value, ")") {
					snippets[strings.TrimSuffix(strings.TrimPrefix(previous.value, "("), ")")] = true
				}
			}
			depth++
		case token.is("}"):
			depth--
		}
	}
}
