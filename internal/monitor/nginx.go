package monitor

import (
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"
)

type NginxListen struct {
	Port                int
	TLS, HTTP2, Default bool
	// Addr is the address listened on: "" for every one (and for a UNIX
	// socket), "*", an IP address without brackets, or a host name.
	Addr string
}

type NginxSite struct {
	File, Root, AccessLog, ErrorLog string
	Line                            int
	Names, Proxy, Certificates      []string
	Listen                          []NginxListen
}

type NginxTree struct {
	Main          string
	Files         []string
	Sites         []NginxSite
	Debian, ConfD bool
	// Unparsed are the files of the dump this parser could not read.
	Unparsed []string
	// Upstreams are the servers of each upstream block, by name.
	Upstreams      map[string][]string
	includes       []string
	digest         string
	httpIncludes   []string
	hasCredentials bool
}

type nginxNode struct {
	words []string
	line  int
	block bool
	child []nginxNode
}

type nginxToken struct {
	word string
	line int
	mark byte
}

var errNginxDump = errors.New("incomplete or unsupported nginx configuration dump")

// nginxTokens splits a configuration file the way nginx's own
// ngx_conf_read_token does: a word ends at whitespace, ';' or '{' ('}',
// '#' and quotes inside a word are ordinary characters, and so is '{'
// right after '$'), a quote opens only at the start of a word and must be
// followed by whitespace, ';', '{' or ')', and a backslash keeps the next
// character in the word. The block of a directive whose name ends in
// _by_lua_block holds Lua, not directives: it is skipped the way
// lua-nginx-module reads it (OpenResty, and nginx built with that module,
// as the BaoTa panel does) and stands as an empty block.
func nginxTokens(text string) ([]nginxToken, error) {
	const breaks = " \t\r\n;{"
	var tokens []nginxToken
	var word strings.Builder
	line, start := 1, 0
	var quote byte
	inWord, variable, needSpace := false, false, false
	words, directive := 0, ""
	for index := 0; index < len(text); index++ {
		char := text[index]
		if char == '\n' {
			line++
		}
		if needSpace {
			// nginx lets ')' follow a quote, as in if ($uri ~ "^/a").
			if !strings.ContainsRune(breaks+")", rune(char)) {
				return nil, errNginxDump
			}
			needSpace = false
		}
		if inWord {
			if char == '{' && variable {
				word.WriteByte(char)
				continue
			}
			variable = false
			if char == '\\' {
				index++
				if index == len(text) {
					return nil, errNginxDump
				}
				if text[index] == '\n' {
					line++
				}
				nginxEscape(&word, text[index])
				continue
			}
			if char == '$' {
				variable = true
			}
			if quote != 0 {
				if char != quote {
					word.WriteByte(char)
					continue
				}
				needSpace = true
			} else if !strings.ContainsRune(breaks, rune(char)) {
				word.WriteByte(char)
				continue
			}
			if words == 0 {
				directive = word.String()
			}
			tokens = append(tokens, nginxToken{word: word.String(), line: start})
			word.Reset()
			inWord, quote, words = false, 0, words+1
			if needSpace {
				continue
			}
		}
		switch char {
		case ' ', '\t', '\r', '\n':
		case ';', '{':
			if words == 0 {
				return nil, errNginxDump
			}
			tokens = append(tokens, nginxToken{line: line, mark: char})
			words = 0
			if char == '{' && strings.HasSuffix(directive, "_by_lua_block") {
				end, ok := nginxLuaBlock(text, index)
				if !ok {
					return nil, errNginxDump
				}
				line += strings.Count(text[index:end], "\n")
				index = end
				tokens = append(tokens, nginxToken{line: line, mark: '}'})
			}
		case '}':
			if words != 0 {
				return nil, errNginxDump
			}
			tokens = append(tokens, nginxToken{line: line, mark: char})
		case '#':
			for index+1 < len(text) && text[index+1] != '\n' {
				index++
			}
		case '"', '\'':
			inWord, start, quote = true, line, char
		default:
			// The word starts here: the character is read again as part
			// of it, so that '\\' and '$' are handled in one place.
			inWord, start = true, line
			index--
		}
	}
	if inWord {
		return nil, errNginxDump
	}
	return tokens, nil
}

// nginxEscape adds the character after a backslash to word as nginx
// does: \" \' \\ \t \r \n stand for the character, any other pair stays.
func nginxEscape(word *strings.Builder, char byte) {
	switch char {
	case '"', '\'', '\\':
		word.WriteByte(char)
	case 't':
		word.WriteByte('\t')
	case 'r':
		word.WriteByte('\r')
	case 'n':
		word.WriteByte('\n')
	default:
		word.WriteByte('\\')
		word.WriteByte(char)
	}
}

// nginxLuaBlock returns the index of the '}' closing the Lua block whose
// '{' is text[open], skipping braces in Lua strings and comments; false
// when the block does not end.
func nginxLuaBlock(text string, open int) (int, bool) {
	depth := 1
	for index := open + 1; index < len(text); index++ {
		switch char := text[index]; char {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return index, true
			}
		case '"', '\'':
			for index++; index < len(text) && text[index] != char; index++ {
				if text[index] == '\\' {
					index++
				}
			}
			if index >= len(text) {
				return 0, false
			}
		case '[', '-':
			comment := char == '-'
			if comment {
				if index+1 >= len(text) || text[index+1] != '-' {
					continue
				}
				index += 2
			}
			if end, long := luaLongBracket(text, index); long {
				if end < 0 {
					return 0, false
				}
				index = end
			} else if comment {
				for index < len(text) && text[index] != '\n' {
					index++
				}
			}
		}
	}
	return 0, false
}

// luaLongBracket returns, when text[index:] opens a Lua long bracket
// ([[, [==[ …), the index of the last character of its closing bracket,
// or -1 when it does not close.
func luaLongBracket(text string, index int) (int, bool) {
	if index >= len(text) || text[index] != '[' {
		return 0, false
	}
	level := index + 1
	for level < len(text) && text[level] == '=' {
		level++
	}
	if level >= len(text) || text[level] != '[' {
		return 0, false
	}
	closing := "]" + strings.Repeat("=", level-index-1) + "]"
	end := strings.Index(text[level+1:], closing)
	if end < 0 {
		return -1, true
	}
	return level + end + len(closing), true
}

func nginxNodes(tokens *[]nginxToken, depth int) ([]nginxNode, error) {
	if depth > 64 {
		return nil, errNginxDump
	}
	var nodes []nginxNode
	var node nginxNode
	for len(*tokens) != 0 {
		token := (*tokens)[0]
		*tokens = (*tokens)[1:]
		switch token.mark {
		case 0:
			if len(node.words) == 0 {
				node.line = token.line
			}
			node.words = append(node.words, token.word)
		case '}':
			if depth == 0 || len(node.words) != 0 {
				return nil, errNginxDump
			}
			return nodes, nil
		case ';', '{':
			if len(node.words) == 0 {
				return nil, errNginxDump
			}
			if token.mark == '{' {
				var err error
				node.block = true
				node.child, err = nginxNodes(tokens, depth+1)
				if err != nil {
					return nil, err
				}
			}
			nodes = append(nodes, node)
			node = nginxNode{}
		}
	}
	if depth != 0 || len(node.words) != 0 {
		return nil, errNginxDump
	}
	return nodes, nil
}

type nginxSource struct {
	nginxNode
	file string
}

type nginxConfig struct {
	tree  NginxTree
	files map[string][]nginxNode
	steps int
	err   error
}

func ParseNginx(dump string) (NginxTree, error) {
	if len(dump) >= procOutputLimit {
		return NginxTree{}, errNginxDump
	}
	config := nginxConfig{files: map[string][]nginxNode{}}
	var file string
	var text strings.Builder
	// A file nginx reads but this parser does not is left out, and named
	// in Unparsed, rather than failing the whole tree: its sites are not
	// listed and nothing is changed while it is there (AllowsBackup).
	finish := func() {
		if file == "" {
			return
		}
		tokens, err := nginxTokens(text.String())
		if err == nil {
			config.files[file], err = nginxNodes(&tokens, 0)
		}
		if err != nil {
			config.files[file] = nil
			config.tree.Unparsed = append(config.tree.Unparsed, file)
		}
		text.Reset()
	}
	for line := range strings.SplitSeq(dump, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "# configuration file "); ok && strings.HasSuffix(name, ":") {
			finish()
			file = path.Clean(strings.TrimSuffix(name, ":"))
			if !path.IsAbs(file) || slices.Contains(config.tree.Files, file) || len(config.tree.Files) == 4096 {
				return NginxTree{}, errNginxDump
			}
			config.tree.Files = append(config.tree.Files, file)
		} else if file != "" {
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	finish()
	if len(config.tree.Files) == 0 || slices.Contains(config.tree.Unparsed, config.tree.Files[0]) {
		return NginxTree{}, errNginxDump
	}
	config.tree.Main = config.tree.Files[0]
	config.tree.digest = systemDigest(dump)
	config.tree.hasCredentials = nginxSecret.MatchString(dump)
	main := config.expand(config.files[config.tree.Main], config.tree.Main, 0)
	base := NginxSite{AccessLog: "logs/access.log", ErrorLog: "logs/error.log"}
	base, _ = config.settings(base, main)
	for _, node := range main {
		if node.words[0] == "http" && node.block {
			config.http(base, config.expandHTTP(node.child, node.file))
		}
	}
	config.collectIncludes()
	return config.tree, config.err
}

func (config *nginxConfig) include(pattern string) string {
	if !path.IsAbs(pattern) {
		return path.Join(path.Dir(config.tree.Main), pattern)
	}
	return path.Clean(pattern)
}

func (config *nginxConfig) expand(nodes []nginxNode, file string, depth int) []nginxSource {
	return config.expandIn(nodes, file, depth, nil)
}

func (config *nginxConfig) expandHTTP(nodes []nginxNode, file string) []nginxSource {
	return config.expandIn(nodes, file, 0, &config.tree.httpIncludes)
}

func (config *nginxConfig) expandIn(nodes []nginxNode, file string, depth int, includes *[]string) []nginxSource {
	config.steps += len(nodes)
	if depth > 32 || config.steps > 200000 {
		config.err = errNginxDump
		return nil
	}
	var expanded []nginxSource
	for _, node := range nodes {
		if node.words[0] == "include" && len(node.words) == 2 {
			pattern := config.include(node.words[1])
			if includes != nil {
				*includes = append(*includes, pattern)
			}
			for _, child := range config.tree.Files {
				if matches, _ := path.Match(pattern, child); matches {
					expanded = append(expanded, config.expandIn(config.files[child], child, depth+1, includes)...)
				}
			}
		} else {
			expanded = append(expanded, nginxSource{node, file})
		}
	}
	return expanded
}

func (config *nginxConfig) collectIncludes() {
	var patterns []string
	var visit func([]nginxNode)
	visit = func(nodes []nginxNode) {
		for _, node := range nodes {
			if node.words[0] == "include" && len(node.words) == 2 {
				patterns = append(patterns, config.include(node.words[1]))
			}
			visit(node.child)
		}
	}
	for _, file := range config.tree.Files {
		visit(config.files[file])
	}
	config.tree.includes = patterns
}

func (config *nginxConfig) http(base NginxSite, nodes []nginxSource) {
	base, inheritedHTTP2 := config.settings(base, nodes)
	for _, node := range nodes {
		if node.words[0] == "upstream" && node.block && len(node.words) == 2 {
			config.upstream(node.words[1], config.expand(node.child, node.file, 0))
		}
		if node.words[0] != "server" || !node.block {
			continue
		}
		children := config.expand(node.child, node.file, 0)
		site, http2 := config.settings(base, children)
		site.File, site.Line = node.file, node.line
		if len(site.Listen) == 0 {
			site.Listen = []NginxListen{{Port: 80}}
		}
		if inheritedHTTP2 && !slices.ContainsFunc(children, func(child nginxSource) bool {
			return len(child.words) == 2 && child.words[0] == "http2"
		}) {
			http2 = true
		}
		for index := range site.Listen {
			site.Listen[index].HTTP2 = site.Listen[index].HTTP2 || http2
		}
		config.locations(&site, children)
		config.tree.Sites = append(config.tree.Sites, site)
	}
}

func (config *nginxConfig) upstream(name string, nodes []nginxSource) {
	if config.tree.Upstreams == nil {
		config.tree.Upstreams = map[string][]string{}
	}
	for _, node := range nodes {
		if node.words[0] == "server" && !node.block && len(node.words) >= 2 {
			config.tree.Upstreams[name] = append(config.tree.Upstreams[name], node.words[1])
		}
	}
}

func (config *nginxConfig) settings(site NginxSite, nodes []nginxSource) (NginxSite, bool) {
	var certificates []string
	var http2 bool
	for _, node := range nodes {
		if node.block || len(node.words) < 2 {
			continue
		}
		value := node.words[1]
		switch node.words[0] {
		case "server_name":
			site.Names = append(site.Names, node.words[1:]...)
		case "listen":
			site.Listen = append(site.Listen, nginxListen(node.words[1:]))
		case "http2":
			http2 = value == "on"
		case "root":
			site.Root = value
		case "proxy_pass":
			site.Proxy = append(site.Proxy, value)
		case "ssl_certificate":
			certificates = append(certificates, value)
		case "access_log":
			site.AccessLog = value
		case "error_log":
			site.ErrorLog = value
		}
	}
	if len(certificates) > 0 {
		site.Certificates = certificates
	}
	return site, http2
}

func (config *nginxConfig) locations(site *NginxSite, nodes []nginxSource) {
	for _, node := range nodes {
		if node.block && (node.words[0] == "location" || node.words[0] == "if" || node.words[0] == "limit_except") {
			children := config.expand(node.child, node.file, 0)
			values, _ := config.settings(NginxSite{}, children)
			site.Proxy = append(site.Proxy, values.Proxy...)
			if site.Root == "" {
				site.Root = values.Root
			}
			config.locations(site, children)
		}
	}
}

func nginxListen(words []string) NginxListen {
	address := words[0]
	port, addr := 80, ""
	switch {
	case strings.HasPrefix(address, "unix:"):
		port = 0
	case strings.HasSuffix(address, "]"):
		addr = address
	case strings.Contains(address, ":"):
		colon := strings.LastIndexByte(address, ':')
		port, _ = strconv.Atoi(address[colon+1:])
		addr = address[:colon]
	default:
		if value, err := strconv.Atoi(address); err == nil {
			port = value
		} else {
			addr = address
		}
	}
	addr = strings.TrimSuffix(strings.TrimPrefix(addr, "["), "]")
	return NginxListen{port, slices.Contains(words[1:], "ssl") || slices.Contains(words[1:], "quic"), slices.Contains(words[1:], "http2"), slices.Contains(words[1:], "default_server") || slices.Contains(words[1:], "default"), addr}
}

func (tree NginxTree) AllowsBackup(backup string) bool {
	if len(tree.Unparsed) != 0 {
		return false
	}
	for _, pattern := range tree.includes {
		matched, err := path.Match(pattern, backup)
		if matched || err != nil {
			return false
		}
	}
	return true
}

func (tree NginxTree) layoutAllows(patternPath string) bool {
	for _, pattern := range tree.httpIncludes {
		if matched, _ := path.Match(pattern, patternPath); matched {
			return true
		}
	}
	return false
}
