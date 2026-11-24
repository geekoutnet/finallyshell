package app

import (
	"regexp"
	"strings"
	"sync"
)

type systemChunks struct {
	mu               sync.Mutex
	max, next, count int
	lines            []string
	partial          string
	key              bool
}

func newSystemChunks(maxLines int) *systemChunks {
	return &systemChunks{max: maxLines, lines: make([]string, maxLines)}
}

func (buffer *systemChunks) add(chunk string) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	for chunk != "" {
		line, rest, ended := strings.Cut(chunk, "\n")
		if room := 8192 - len(buffer.partial); room > 0 {
			buffer.partial += line[:min(room, len(line))]
		}
		if ended {
			buffer.line(buffer.partial)
			buffer.partial = ""
		}
		chunk = rest
	}
}

func (buffer *systemChunks) line(line string) {
	line = strings.ToValidUTF8(strings.TrimSuffix(line, "\r"), "�")
	if strings.Contains(line, "-----BEGIN ") && strings.Contains(line, "PRIVATE KEY-----") {
		buffer.key = true
	}
	if buffer.key {
		if strings.Contains(line, "-----END ") && strings.Contains(line, "PRIVATE KEY-----") {
			buffer.key = false
		}
		line = "[redacted]"
	} else {
		line = systemRedact(line)
	}
	buffer.lines[buffer.next] = line
	buffer.next = (buffer.next + 1) % buffer.max
	buffer.count = min(buffer.count+1, buffer.max)
}

func (buffer *systemChunks) take(final bool) []string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if final && buffer.partial != "" {
		buffer.line(buffer.partial)
		buffer.partial = ""
	}
	start := (buffer.next - buffer.count + buffer.max) % buffer.max
	result := make([]string, 0, buffer.count)
	for index := range buffer.count {
		pos := (start + index) % buffer.max
		result = append(result, buffer.lines[pos])
		buffer.lines[pos] = ""
	}
	buffer.count = 0
	return result
}

var systemSecret = regexp.MustCompile(`(?i)\b((?:password|passwd|passphrase|secret|token|api[_-]?key|private[_-]?key|authorization)\s*[:=][ \t]*).+`)
var systemURLSecret = regexp.MustCompile(`(://[^/@\s:]+:)[^/@\s]+(@)`)
var systemPrivate = regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?(?:-----END [^-\r\n]*PRIVATE KEY-----|$)`)
var systemPasswordArg = regexp.MustCompile(`(?i)(--(?:password|passwd|passphrase|token|secret|api-key)(?:=|\s+))(?:(?:"[^"]*"|'[^']*')|[^\s;&]+)`)

func systemRedact(text string) string {
	text = systemPrivate.ReplaceAllString(text, "[redacted]")
	text = systemPasswordArg.ReplaceAllString(text, "${1}[redacted]")
	text = systemURLSecret.ReplaceAllString(text, "${1}[redacted]$2")
	return systemSecret.ReplaceAllString(text, "${1}[redacted]")
}

var logError = regexp.MustCompile(`(?i)\b(?:err(?:or)?s?|fail(?:ed|ure)?|fatal|panic|critical)\b`)
var logWarning = regexp.MustCompile(`(?i)\bwarn(?:ing)?s?\b`)

func systemLogLevel(line string, keywords []string) int {
	if logError.MatchString(line) {
		return 3
	}
	if logWarning.MatchString(line) {
		return 2
	}
	line = strings.ToLower(line)
	for _, key := range keywords {
		if key != "" && strings.Contains(line, key) {
			return 1
		}
	}
	return 0
}

func logMatcher(query string, regex, exact bool) (func(string) bool, error) {
	if query == "" {
		return func(string) bool { return true }, nil
	}
	if regex {
		if !exact {
			query = "(?i)" + query
		}
		compiled, err := regexp.Compile(query)
		if err != nil {
			return nil, err
		}
		return compiled.MatchString, nil
	}
	if exact {
		return func(line string) bool { return strings.Contains(line, query) }, nil
	}
	query = strings.ToLower(query)
	return func(line string) bool { return strings.Contains(strings.ToLower(line), query) }, nil
}
