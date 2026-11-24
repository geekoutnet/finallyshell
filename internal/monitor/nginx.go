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
}

type NginxSite struct {
	File, Root, AccessLog, ErrorLog string
	Line                            int
	Names, Proxy, Certificates      []string
	Listen                          []NginxListen
}

type NginxTree struct {
	Main           string
	Files          []string
	Sites          []NginxSite
	Debian, ConfD  bool
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

func nginxTokens(text string) ([]nginxToken, error) {
	var tokens []nginxToken
	var word strings.Builder
	line, start := 1, 0
	var quote byte
	flush := func() {
		if start != 0 {
			tokens = append(tokens, nginxToken{word: word.String(), line: start})
			word.Reset()
			start = 0
		}
	}
	for index := 0; index < len(text); index++ {
		char := text[index]
		if char == '\\' {
			if start == 0 {
				start = line
			}
			index++
			if index == len(text) {
				return nil, errNginxDump
			}
			next := text[index]
			if next == '\n' {
				line++
			}
			if !strings.ContainsRune("\\\"' \t;{}#", rune(next)) {
				word.WriteByte(char)
			}
			word.WriteByte(next)
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteByte(char)
			}
		} else {
			switch char {
			case '\'', '"':
				if start == 0 {
					start = line
				}
				quote = char
			case '$':
				if start == 0 {
					start = line
				}
				if index+1 < len(text) && text[index+1] == '{' {
					end := strings.IndexByte(text[index+2:], '}')
					if end < 0 {
						return nil, errNginxDump
					}
					end += index + 2
					word.WriteString(text[index : end+1])
					index = end
				} else {
					word.WriteByte(char)
				}
			case '#':
				if start != 0 {
					word.WriteByte(char)
					break
				}
				for index+1 < len(text) && text[index+1] != '\n' {
					index++
				}
			case ' ', '\r', '\t', '\n':
				flush()
			case '{', '}', ';':
				flush()
				tokens = append(tokens, nginxToken{line: line, mark: char})
			default:
				if start == 0 {
					start = line
				}
				word.WriteByte(char)
			}
		}
		if char == '\n' {
			line++
		}
	}
	if quote != 0 {
		return nil, errNginxDump
	}
	flush()
	return tokens, nil
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
	finish := func() error {
		if file == "" {
			return nil
		}
		tokens, err := nginxTokens(text.String())
		if err != nil {
			return err
		}
		config.files[file], err = nginxNodes(&tokens, 0)
		text.Reset()
		return err
	}
	for line := range strings.SplitSeq(dump, "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "# configuration file "); ok && strings.HasSuffix(name, ":") {
			if err := finish(); err != nil {
				return NginxTree{}, err
			}
			file = strings.TrimSuffix(name, ":")
			if !path.IsAbs(file) || path.Clean(file) != file || slices.Contains(config.tree.Files, file) || len(config.tree.Files) == 4096 {
				return NginxTree{}, errNginxDump
			}
			config.tree.Files = append(config.tree.Files, file)
		} else if file != "" {
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	if err := finish(); err != nil || len(config.tree.Files) == 0 {
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
	port := 80
	switch {
	case strings.HasPrefix(address, "unix:"):
		port = 0
	case strings.HasSuffix(address, "]"):
	case strings.Contains(address, ":"):
		port, _ = strconv.Atoi(address[strings.LastIndexByte(address, ':')+1:])
	default:
		if value, err := strconv.Atoi(address); err == nil {
			port = value
		}
	}
	return NginxListen{port, slices.Contains(words[1:], "ssl") || slices.Contains(words[1:], "quic"), slices.Contains(words[1:], "http2"), slices.Contains(words[1:], "default_server") || slices.Contains(words[1:], "default")}
}

func (tree NginxTree) AllowsBackup(backup string) bool {
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
