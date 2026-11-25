package monitor

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func caddyLogCount(lines int) (string, error) {
	if lines < 0 || lines > 10000 {
		return "", ErrCaddyUnsafe
	}
	return strconv.Itoa(lines), nil
}

// CaddyLogsScript builds a journalctl or Docker log command. The caller can
// stream its output with CaddyRunner and cancel through the runner's context.
// Unmanaged services or invalid line counts return ErrCaddyUnsafe.
func CaddyLogsScript(inst CaddyInstall, lines int, follow bool) (string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil {
		return "", err
	}
	count, err := caddyLogCount(lines)
	if err != nil {
		return "", err
	}
	var args []string
	if inst.Method == CaddyDocker {
		args = []string{"docker", "logs", "--timestamps", "--tail", count}
		if follow {
			args = append(args, "--follow")
		}
		return SystemCommand(append(args, inst.Container)...), nil
	}
	if inst.Unit == "" || inst.ServiceManager == "openrc" {
		return "", ErrCaddyUnsafe
	}
	args = []string{"journalctl", "-u", inst.Unit, "-o", "short-iso", "-n", count}
	if follow {
		args = append(args, "-f")
	}
	return SystemCommand(append(args, "--no-pager")...), nil
}

// CaddyLogFileScript builds a bounded tail command for an absolute log path.
// For Docker, the path from CaddyLogFiles is interpreted inside the container.
func CaddyLogFileScript(inst CaddyInstall, filename string, lines int, follow bool) (string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || !ValidLogPath(filename) || (inst.Method == CaddyDocker && inst.Active != "active" && inst.Active != "running") {
		return "", ErrCaddyUnsafe
	}
	count, err := caddyLogCount(lines)
	if err != nil {
		return "", err
	}
	args := []string{"tail", "-n", count}
	if follow {
		args = append(args, "-F")
	}
	args = append(args, "--", filename)
	if inst.Method == CaddyDocker {
		args = append([]string{"docker", "exec", inst.Container}, args...)
	}
	return SystemCommand(args...), nil
}

// CaddyLogFiles returns unique absolute file outputs inside Caddyfile log
// blocks. A path in a snippet that uses the snippet's arguments, as
// /var/log/caddy/{args[0]}.log, gives one path per `import snippet …`
// that passes them. Other dynamic or relative paths are omitted because
// their service working directory or environment cannot be inferred from
// the files alone.
func CaddyLogFiles(files []CaddyFile) []string {
	var names []string
	templates := map[string][]string{} // log paths with {args…}, by snippet
	var imports [][]string             // import name args…, as written
	for _, file := range files {
		if file.Adapter == "json" || path.Ext(file.Path) == ".json" {
			continue
		}
		tokens, err := caddyTokens(file.Content)
		if err != nil {
			continue
		}
		depth, logDepth := 0, 0
		inLogLine := 0
		snippet, snippetDepth := "", 0
		for index, token := range tokens {
			switch {
			case token.is("{"):
				depth++
				if token.line == inLogLine && logDepth == 0 {
					logDepth = depth
				}
				if depth == 1 && index > 0 && tokens[index-1].line == token.line {
					if name := tokens[index-1].value; len(name) > 2 && name[0] == '(' && name[len(name)-1] == ')' {
						snippet, snippetDepth = name[1:len(name)-1], depth
					}
				}
			case token.is("}"):
				if depth == logDepth {
					logDepth = 0
				}
				if depth == snippetDepth {
					snippet, snippetDepth = "", 0
				}
				depth--
			case token.is("log"):
				inLogLine = token.line
			case token.is("import") && !token.quoted:
				var line []string
				for _, next := range tokens[index+1:] {
					if next.line != token.line || next.is("{") {
						break
					}
					line = append(line, next.value)
				}
				if len(line) > 0 {
					imports = append(imports, line)
				}
			case logDepth != 0 && depth == logDepth && token.is("output") && index+2 < len(tokens):
				driver, arg := tokens[index+1], tokens[index+2]
				if driver.line != token.line || arg.line != token.line || !driver.is("file") {
					continue
				}
				if snippet != "" && strings.Contains(arg.value, "{args") {
					templates[snippet] = append(templates[snippet], arg.value)
				} else if caddyPath(arg.value) {
					names = append(names, arg.value)
				}
			}
		}
	}
	for _, line := range imports {
		for _, template := range templates[line[0]] {
			if name := caddyExpandArgs(template, line[1:]); caddyPath(name) {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// caddyArgPlaceholder is a snippet argument: {args[0]} or {args.0}.
var caddyArgPlaceholder = regexp.MustCompile(`\{args(?:\[(\d+)\]|\.(\d+))\}`)

// caddyExpandArgs puts args in template's {args[N]} placeholders; "" when
// one names an argument the import does not pass.
func caddyExpandArgs(template string, args []string) string {
	missing := false
	expanded := caddyArgPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		parts := caddyArgPlaceholder.FindStringSubmatch(match)
		number, _ := strconv.Atoi(parts[1] + parts[2])
		if number >= len(args) {
			missing = true
			return match
		}
		return args[number]
	})
	if missing || strings.Contains(expanded, "{") {
		return ""
	}
	return expanded
}
