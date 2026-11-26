package monitor

import (
	"errors"
	"regexp"
	"slices"
	"strings"
)

// The performance settings of nginx.conf, as a hosting panel tunes them:
// a directive each, in the main context, events or http. Reading them
// finds the directive where nginx reads it, not in a server or a location;
// changing them edits that directive's value in place, or adds the
// directive at the start of its block, leaving every other byte of the
// file, its comments included, as it was.

// NginxSetting is a directive the settings tune.
type NginxSetting struct {
	Name string
	// Block is the block nginx reads it in: "" for the main context,
	// "events" or "http".
	Block string
	// Default is nginx's value when the file does not set it.
	Default string
	valid   *regexp.Regexp
}

var (
	nginxWorkers = regexp.MustCompile(`^(auto|[1-9][0-9]{0,3})$`)
	nginxCount   = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)
	nginxSeconds = regexp.MustCompile(`^[0-9]{1,5}[smh]?$`)
	nginxSize    = regexp.MustCompile(`^[0-9]{1,6}[kKmMgG]?$`)
	nginxOnOff   = regexp.MustCompile(`^(on|off)$`)
	nginxLevel   = regexp.MustCompile(`^[1-9]$`)
)

// NginxSettings are the directives the settings tune, in the order they
// show.
var NginxSettings = []NginxSetting{
	{"worker_processes", "", "1", nginxWorkers},
	{"worker_connections", "events", "512", nginxCount},
	{"keepalive_timeout", "http", "75s", nginxSeconds},
	{"client_max_body_size", "http", "1m", nginxSize},
	{"client_header_buffer_size", "http", "1k", nginxSize},
	{"client_body_buffer_size", "http", "16k", nginxSize},
	{"server_names_hash_bucket_size", "http", "64", nginxCount},
	{"gzip", "http", "off", nginxOnOff},
	{"gzip_min_length", "http", "20", nginxSize},
	{"gzip_comp_level", "http", "1", nginxLevel},
	{"server_tokens", "http", "on", nginxOnOff},
}

// ErrNginxSettings is a value a setting does not take, or a file whose
// structure the settings cannot edit.
var ErrNginxSettings = errors.New("invalid nginx setting or configuration")

// Valid reports whether the setting takes value.
func (setting NginxSetting) Valid(value string) bool { return setting.valid.MatchString(value) }

// nginxStatement is a directive of a configuration file: its name, the raw
// text of its arguments, the blocks it is in, and where it is.
type nginxStatement struct {
	name, args string
	block      string // the names of the blocks it is in, joined by "/"
	start      int    // its name's first byte
	nameEnd    int    // the byte after its name
	end        int    // its ';', or the '{' of a block's start
	opens      bool
}

// nginxStatements reads text's directives, with where each is.
func nginxStatements(text string) ([]nginxStatement, error) {
	var out []nginxStatement
	var stack []string
	start, nameEnd := -1, -1
	inWord := false
	var quote byte
	word := func(i int) {
		if start < 0 {
			start = i
		}
		inWord = true
	}
	endWord := func(i int) {
		if inWord && nameEnd < 0 {
			nameEnd = i
		}
		inWord = false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\\':
			word(i)
			i++
		case '"', '\'':
			word(i)
			quote = c
		case '$':
			word(i)
			if i+1 < len(text) && text[i+1] == '{' {
				close := strings.IndexByte(text[i+2:], '}')
				if close < 0 {
					return nil, ErrNginxSettings
				}
				i += close + 2
			}
		case '#':
			if inWord {
				break
			}
			for i+1 < len(text) && text[i+1] != '\n' {
				i++
			}
		case ' ', '\t', '\r', '\n':
			endWord(i)
		case ';', '{', '}':
			endWord(i)
			if c == '}' {
				if start >= 0 || len(stack) == 0 {
					return nil, ErrNginxSettings
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if start < 0 {
				if c == ';' {
					continue
				}
				return nil, ErrNginxSettings
			}
			st := nginxStatement{name: text[start:nameEnd], args: strings.TrimSpace(text[nameEnd:i]), block: strings.Join(stack, "/"),
				start: start, nameEnd: nameEnd, end: i, opens: c == '{'}
			out = append(out, st)
			if st.opens {
				stack = append(stack, st.name)
			}
			start, nameEnd = -1, -1
		default:
			word(i)
		}
	}
	if quote != 0 || start >= 0 || len(stack) > 0 {
		return nil, ErrNginxSettings
	}
	return out, nil
}

// ReadNginxSettings is the value of each setting text sets, by name; a
// setting it leaves to nginx's default is missing.
func ReadNginxSettings(text string) (map[string]string, error) {
	statements, err := nginxStatements(text)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, setting := range NginxSettings {
		for _, st := range statements {
			if !st.opens && st.name == setting.Name && st.block == setting.Block {
				values[setting.Name] = st.args
				break
			}
		}
	}
	return values, nil
}

// ApplyNginxSettings is text with the settings in values: each one's
// directive takes its value where it is, or is added at the start of its
// block; settings values leaves out, or gives "", stay as they are.
func ApplyNginxSettings(text string, values map[string]string) (string, error) {
	statements, err := nginxStatements(text)
	if err != nil {
		return "", err
	}
	type edit struct {
		at, end int
		text    string
	}
	var edits []edit
	for _, setting := range NginxSettings {
		value := values[setting.Name]
		if value == "" {
			continue
		}
		if !setting.Valid(value) {
			return "", ErrNginxSettings
		}
		found := slices.IndexFunc(statements, func(st nginxStatement) bool {
			return !st.opens && st.name == setting.Name && st.block == setting.Block
		})
		if found >= 0 {
			if st := statements[found]; st.args != value {
				edits = append(edits, edit{st.nameEnd, st.end, " " + value})
			}
			continue
		}
		line := setting.Name + " " + value + ";"
		if setting.Block == "" {
			// Before the first block of the main context, as nginx.conf
			// keeps its main directives above events and http.
			at := len(text)
			if first := slices.IndexFunc(statements, func(st nginxStatement) bool { return st.opens && st.block == "" }); first >= 0 {
				at = strings.LastIndexByte(text[:statements[first].start], '\n') + 1
				edits = append(edits, edit{at, at, line + "\n"})
			} else {
				if text != "" && !strings.HasSuffix(text, "\n") {
					line = "\n" + line
				}
				edits = append(edits, edit{at, at, line + "\n"})
			}
			continue
		}
		block := slices.IndexFunc(statements, func(st nginxStatement) bool {
			return st.opens && st.block == "" && st.name == setting.Block
		})
		if block < 0 {
			return "", ErrNginxSettings
		}
		at := statements[block].end + 1
		edits = append(edits, edit{at, at, "\n    " + line})
	}
	// From the end, so that each edit's place holds; those at one place
	// keep the settings' order.
	slices.SortStableFunc(edits, func(a, b edit) int { return b.at - a.at })
	out := text
	for i := 0; i < len(edits); {
		j := i
		for j < len(edits) && edits[j].at == edits[i].at {
			j++
		}
		group := edits[i:j]
		var inserted strings.Builder
		end := group[0].end
		for _, e := range group {
			inserted.WriteString(e.text)
			end = max(end, e.end)
		}
		out = out[:group[0].at] + inserted.String() + out[end:]
		i = j
	}
	return out, nil
}
