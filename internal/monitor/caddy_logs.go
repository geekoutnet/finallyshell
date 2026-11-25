package monitor

import (
	"path"
	"slices"
	"strconv"
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
// blocks. Dynamic or relative paths are omitted because their service working
// directory or environment cannot be inferred from the files alone.
func CaddyLogFiles(files []CaddyFile) []string {
	var names []string
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
		for index, token := range tokens {
			switch {
			case token.is("{"):
				depth++
				if token.line == inLogLine && logDepth == 0 {
					logDepth = depth
				}
			case token.is("}"):
				if depth == logDepth {
					logDepth = 0
				}
				depth--
			case token.is("log"):
				inLogLine = token.line
			case logDepth != 0 && depth == logDepth && token.is("output") && index+2 < len(tokens):
				driver, arg := tokens[index+1], tokens[index+2]
				if driver.line == token.line && arg.line == token.line && driver.is("file") && caddyPath(arg.value) {
					names = append(names, arg.value)
				}
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}
