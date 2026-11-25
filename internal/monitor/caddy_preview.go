package monitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const caddyPreviewShell = `set -eu
umask '077'
tmp=$(mktemp -d '/tmp/finallyshell-caddy.XXXXXXXX')
trap 'rm -rf -- "$tmp"' '0'
trap 'exit 1' HUP INT TERM
mkdir -p -- "$tmp/tree" "$tmp/data" "$tmp/config"
HOME="$tmp"; XDG_DATA_HOME="$tmp/data"; XDG_CONFIG_HOME="$tmp/config"
export HOME XDG_DATA_HOME XDG_CONFIG_HOME
preview_file() {
  mkdir -p -- "$tmp/tree${1%/*}"
  if [ "$2" = 'rewrite' ]; then
    base64 -d | awk -v "root=$tmp/tree" -v "marker=$3" '{gsub(marker, root); print}' > "$tmp/tree$1"
  else
    base64 -d > "$tmp/tree$1"
  fi
}
`

func caddyPreviewContent(content, filename, marker string, snippets map[string]bool) (string, bool, error) {
	tokens, err := caddyTokens(content)
	if err != nil {
		return "", false, err
	}
	var output strings.Builder
	last := 0
	for _, index := range caddyDirectives(tokens, "import") {
		argument := tokens[index+1]
		if snippets[argument.value] {
			continue
		}
		target := argument.value
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(filename), target)
		}
		if !caddyPath(target) || strings.ContainsAny(target, "{}\\") {
			return "", false, ErrCaddyUnsafe
		}
		output.WriteString(content[last:argument.start])
		output.WriteString(caddyQuoteToken(marker + target))
		last = argument.end
	}
	if last == 0 {
		return content, false, nil
	}
	output.WriteString(content[last:])
	return output.String(), true, nil
}

func validateCaddyPreview(ctx context.Context, run CaddyRunner, inst CaddyInstall, overrides []CaddyFile) (string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || inst.Resume {
		return "", ErrCaddyUnsafe
	}
	if _, _, err := caddyChangePaths(CaddyInstall{Config: inst.Config}, CaddyChange{Writes: overrides}); err != nil {
		return "", err
	}
	overlay := make(map[string]CaddyFile, len(overrides))
	for _, file := range overrides {
		file.Size = int64(len(file.Content))
		overlay[file.Path] = file
	}
	files, err := caddyReadTree(ctx, run, inst, overlay)
	if err != nil {
		return "", err
	}
	var script strings.Builder
	script.WriteString(caddyPreviewShell)
	snippets := map[string]bool{}
	if inst.Adapter == "caddyfile" {
		for _, file := range files {
			tokens, _ := caddyTokens(file.Content)
			caddySnippets(tokens, snippets)
		}
	}
	marker := "FINALLYSHELL_ABSOLUTE_IMPORT_ROOT"
	for {
		collision := false
		for _, file := range files {
			collision = collision || strings.Contains(file.Content, marker)
		}
		if !collision {
			break
		}
		marker += "_"
	}
	for _, file := range files {
		name, content := file.Path, file.Content
		if inst.Method == CaddyDocker {
			var ok bool
			name, ok = caddyContainerPath(inst, name)
			if !ok {
				return "", ErrCaddyUnsafe
			}
		}
		mode := "literal"
		if inst.Adapter == "caddyfile" {
			var rewritten bool
			content, rewritten, err = caddyPreviewContent(content, name, marker, snippets)
			if err != nil {
				return "", err
			}
			if rewritten {
				mode = "rewrite"
			}
		}
		caddyPayload(&script, SystemCommand("preview_file", name, mode, marker), content)
	}
	config := inst.Config
	cli := inst
	if inst.Method == CaddyDocker {
		var ok bool
		config, ok = caddyContainerPath(inst, config)
		if !ok {
			return "", ErrCaddyUnsafe
		}
		cli = CaddyInstall{Adapter: inst.Adapter}
	}
	configArg := `"$tmp/tree"` + privilegeQuote(config)
	if inst.Adapter != "json" {
		fmt.Fprintf(&script, "%s %s > \"$tmp/adapted.json\"\n", caddyCLI(cli, "adapt", "--adapter", inst.Adapter, "--config"), configArg)
		configArg = `"$tmp/adapted.json"`
	}
	fmt.Fprintf(&script, "%s %s\n", caddyCLI(cli, "validate", "--config"), configArg)
	command := caddyEnvironment(inst, script.String())
	var stdout, stderr string
	if inst.Method == CaddyDocker {
		stdout, stderr, err = run(ctx, SystemCommand("docker", "exec", "-i", inst.Container, "sh", "-s"), strings.NewReader(command), nil)
	} else {
		stdout, stderr, err = caddyRunPayload(ctx, run, command, nil)
	}
	output := caddyOutput(stdout, stderr)
	if err != nil && ctx.Err() == nil {
		return output, fmt.Errorf("%w: %w: %s", ErrCaddyInvalid, err, output)
	}
	return output, err
}

var caddyBcryptHash = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

// CaddyPasswordHash runs Caddy's bcrypt hasher with a single line on stdin.
// It returns the hash, algorithm, and a redacted error if hashing fails.
func CaddyPasswordHash(ctx context.Context, run CaddyRunner, inst CaddyInstall, plain string) (hash, algorithm string, err error) {
	inst, err = caddyNormalize(inst)
	if err != nil || plain == "" || len(plain) > 72 || strings.ContainsAny(plain, "\x00\r\n") {
		return "", "", ErrCaddyUnsafe
	}
	stdout, _, err := run(ctx, caddyCLI(inst, "hash-password", "--algorithm", "bcrypt"), strings.NewReader(plain+"\n"), nil)
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	for line := range strings.SplitSeq(stdout, "\n") {
		if candidate := strings.TrimSpace(line); err == nil && caddyBcryptHash.MatchString(candidate) {
			return candidate, "bcrypt", nil
		}
	}
	return "", "", errors.New("Caddy password hashing failed")
}
