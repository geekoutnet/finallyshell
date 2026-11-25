package caddyfile

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type TokenKind string

const (
	Word       TokenKind = "word"
	OpenBrace  TokenKind = "{"
	CloseBrace TokenKind = "}"
	Newline    TokenKind = "newline"
	Comment    TokenKind = "comment"
)

type Token struct {
	Kind             TokenKind
	Value            string
	Start, End, Line int
	Quoted           bool
}

type lexer struct {
	source string
	at     int
	line   int
}

func Lex(source string) ([]Token, error) {
	if !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return nil, fmt.Errorf("invalid Caddyfile text")
	}
	lx := lexer{source: source, line: 1}
	if strings.HasPrefix(source, "\ufeff") {
		lx.at = len("\ufeff")
	}
	var tokens []Token
	for lx.at < len(source) {
		start, line := lx.at, lx.line
		boundary, size := utf8.DecodeRuneInString(source[lx.at:])
		var kind TokenKind
		var value string
		var quoted bool
		var err error
		switch {
		case boundary == '\n' || boundary == '\r' && lx.newlineSize() > 0:
			kind = Newline
			lx.at += lx.newlineSize()
			lx.line++
		case unicode.IsSpace(boundary):
			lx.at += size
			continue
		case boundary == '#':
			kind = Comment
			end := strings.IndexByte(source[lx.at:], '\n')
			if end < 0 {
				lx.at = len(source)
			} else {
				lx.at += end
				if source[lx.at-1] == '\r' {
					lx.at--
				}
			}
			value = source[start:lx.at]
		case boundary == '"' || boundary == '`':
			kind, quoted = Word, true
			value, err = lx.quoted(byte(boundary))
		case heredocOpening(source[start:]):
			kind, quoted = Word, true
			value, err = lx.heredoc()
		default:
			if boundary == '\\' && start > 0 && (source[start-1] == ' ' || source[start-1] == '\t') {
				lx.at++
				if end := lx.newlineSize(); end > 0 {
					lx.at += end
					lx.line++
					continue
				}
				lx.at = start
			}
			value = lx.word()
			if value == "" {
				continue
			}
			kind = Word
			if value == "{" || value == "}" {
				kind = TokenKind(value)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("Caddyfile line %d: %w", line, err)
		}
		tokens = append(tokens, Token{kind, value, start, lx.at, line, quoted})
	}
	return tokens, nil
}

func (lx *lexer) newlineSize() int {
	if lx.at == len(lx.source) {
		return 0
	}
	if lx.source[lx.at] == '\n' {
		return 1
	}
	if strings.HasPrefix(lx.source[lx.at:], "\r\n") {
		return 2
	}
	return 0
}

func (lx *lexer) word() string {
	var value strings.Builder
	for lx.at < len(lx.source) {
		character, size := utf8.DecodeRuneInString(lx.source[lx.at:])
		if unicode.IsSpace(character) {
			break
		}
		if character == '\\' {
			lx.at++
			if lx.at < len(lx.source) && lx.source[lx.at] == '<' {
				value.WriteByte('<')
				lx.at++
			} else {
				value.WriteByte('\\')
			}
			continue
		}
		if strings.HasPrefix(lx.source[lx.at:], "{$") {
			part := lx.source[lx.at:]
			end := strings.IndexAny(part, "}\r\n")
			if end >= 0 && part[end] == '}' {
				value.WriteString(part[:end+1])
				lx.at += end + 1
				continue
			}
		}
		value.WriteString(lx.source[lx.at : lx.at+size])
		lx.at += size
	}
	return value.String()
}

func (lx *lexer) quoted(delimiter byte) (string, error) {
	lx.at++
	var value strings.Builder
	for lx.at < len(lx.source) {
		character := lx.source[lx.at]
		lx.at++
		if character == delimiter {
			return value.String(), nil
		}
		if character == '\\' && delimiter == '"' && lx.at < len(lx.source) {
			switch lx.source[lx.at] {
			case '"':
				lx.at++
				value.WriteByte('"')
				continue
			case '\\':
				lx.at++
				value.WriteString(`\\`)
				continue
			}
		}
		if character == '\n' {
			lx.line++
		}
		value.WriteByte(character)
	}
	return "", fmt.Errorf("unterminated quoted token")
}

func heredocOpening(source string) bool {
	if !strings.HasPrefix(source, "<<") {
		return false
	}
	end := strings.IndexAny(source, " \n")
	return end >= 0 && source[end] == '\n'
}

func (lx *lexer) heredoc() (string, error) {
	head := lx.at
	newline := strings.IndexByte(lx.source[head:], '\n')
	if newline < 0 {
		return "", fmt.Errorf("missing heredoc closing marker")
	}
	marker := strings.TrimSuffix(lx.source[head+2:head+newline], "\r")
	if marker == "" || strings.IndexFunc(marker, func(char rune) bool {
		return char != '-' && char != '_' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9')
	}) >= 0 {
		return "", fmt.Errorf("invalid heredoc marker")
	}
	lx.at = head + newline + 1
	lx.line++
	var body []string
	for lx.at < len(lx.source) {
		line, _, _ := strings.Cut(lx.source[lx.at:], "\n")
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if tail, found := strings.CutPrefix(trimmed, marker); found && (tail == "" || tail[0] == ' ' || tail[0] == '\t') {
			indent := line[:len(line)-len(trimmed)]
			for index, text := range body {
				if strings.TrimSpace(text) == "" {
					body[index] = ""
				} else if !strings.HasPrefix(text, indent) {
					return "", fmt.Errorf("inconsistent heredoc indentation")
				} else {
					body[index] = text[len(indent):]
				}
			}
			lx.at += len(indent) + len(marker)
			return strings.Join(body, "\n"), nil
		}
		body = append(body, line)
		if advance := strings.IndexByte(lx.source[lx.at:], '\n'); advance >= 0 {
			lx.at += advance + 1
			lx.line++
		} else {
			break
		}
	}
	return "", fmt.Errorf("missing heredoc closing marker %q", marker)
}

func SplitArgs(text string) ([]string, error) {
	tokens, err := Lex(text)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind != Word {
			return nil, fmt.Errorf("expected only Caddyfile arguments")
		}
		args = append(args, token.Value)
	}
	return args, nil
}
