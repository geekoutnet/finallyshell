package monitor

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strconv"
	"strings"
)

const caddyMaxFiles, caddyMaxDepth, caddyMaxTree = 128, 16, 16 << 20

func caddyGlob(name string) (string, bool) {
	var shell strings.Builder
	start := 0
	for index := 0; index < len(name); index++ {
		switch name[index] {
		case '*', '?', '[':
			shell.WriteString(privilegeQuote(name[start:index]))
			if name[index] == '[' {
				end := strings.IndexByte(name[index+1:], ']')
				if end < 0 {
					return "", false
				}
				index += end + 1
				shell.WriteByte('?')
			} else {
				shell.WriteByte(name[index])
			}
			start = index + 1
		}
	}
	shell.WriteString(privilegeQuote(name[start:]))
	return shell.String(), start != 0
}

func caddyReadScript(name string, literal bool) (string, bool) {
	pattern, glob := caddyGlob(name)
	if literal || !glob {
		pattern, glob = privilegeQuote(name), false
	}
	return `export LC_ALL=C
count=0
total=0
for file in ` + pattern + `; do
  [ -f "$file" ] || continue
  count=$((count + 1)); [ "$count" -le '128' ] || exit '1'
  size=$(wc -c < "$file") || exit '1'
  total=$((total + size)); [ "$size" -le '4194304' ] && [ "$total" -le '16777216' ] || exit '1'
  printf 'F\n'
  printf '%s' "$file" | base64 | tr -d '\n'
  printf '\n%d\n' "$size"
  data=$(base64 < "$file") || exit '1'
  printf '%s\n' "$(printf '%s' "$data" | tr -d '\n')"
done
`, glob
}

func caddyDecodeFiles(stdout string) ([]CaddyFile, error) {
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	if len(lines)%4 != 0 || len(lines)/4 > caddyMaxFiles {
		return nil, ErrCaddyUnsafe
	}
	files := make([]CaddyFile, 0, len(lines)/4)
	for index := 0; index < len(lines); index += 4 {
		name, nameErr := base64.StdEncoding.DecodeString(lines[index+1])
		size, sizeErr := strconv.ParseInt(lines[index+2], 10, 64)
		if lines[index] != "F" || nameErr != nil || !caddyPath(string(name)) || sizeErr != nil || size < 0 || size > caddyFileLimit || len(lines[index+3]) != base64.StdEncoding.EncodedLen(int(size)) {
			return nil, ErrCaddyUnsafe
		}
		content, err := base64.StdEncoding.DecodeString(lines[index+3])
		if err != nil || int64(len(content)) != size {
			return nil, ErrCaddyChanged
		}
		files = append(files, CaddyFile{Path: string(name), Content: string(content), Size: size})
	}
	return files, nil
}

func caddyHostPath(inst CaddyInstall, name string) (string, bool) {
	if inst.Method != CaddyDocker {
		return name, true
	}
	var source, target string
	for _, mount := range inst.Mounts {
		if caddyPath(mount.Source) && caddyPath(mount.Destination) && len(mount.Destination) > len(target) &&
			(name == mount.Destination || strings.HasPrefix(name, mount.Destination+"/")) {
			source, target = mount.Source, mount.Destination
		}
	}
	if target != "" {
		return source + strings.TrimPrefix(name, target), true
	}
	if name == inst.ContainerConfig && caddyPath(inst.Config) {
		return inst.Config, true
	}
	return "", false
}

func readCaddyFiles(ctx context.Context, run CaddyRunner, inst CaddyInstall) ([]CaddyFile, error) {
	inst, err := caddyNormalize(inst)
	if err != nil {
		return nil, err
	}
	var files []CaddyFile
	visited, queried, snippets := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var total int64
	var visit func(string, int) error
	visit = func(name string, depth int) error {
		if queried[name] {
			return nil
		}
		if depth > caddyMaxDepth || len(queried) >= caddyMaxFiles || !caddyPath(name) {
			return fmt.Errorf("%w: import %q", ErrCaddyUnsafe, name)
		}
		queried[name] = true
		script, glob := caddyReadScript(name, depth == 0)
		stdout, stderr, err := run(ctx, script, nil, nil)
		if err != nil {
			return caddyCommandError(err, strings.TrimSpace(stderr))
		}
		rows, err := caddyDecodeFiles(stdout)
		if err != nil {
			return err
		}
		if len(rows) == 0 && !glob {
			return fmt.Errorf("%w: missing configuration %q", ErrCaddyInvalid, name)
		}
		for _, file := range rows {
			if glob {
				if match, _ := path.Match(name, file.Path); !match {
					continue
				}
			} else if file.Path != name {
				return ErrCaddyUnsafe
			}
			if visited[file.Path] {
				continue
			}
			total += file.Size
			if len(files) >= caddyMaxFiles || total > caddyMaxTree {
				return ErrCaddyUnsafe
			}
			visited[file.Path] = true
			file.Adapter = inst.Adapter
			files = append(files, file)
			if inst.Adapter != "caddyfile" {
				continue
			}
			tokens, err := caddyTokens(file.Content)
			if err != nil {
				return fmt.Errorf("%w: %s", err, file.Path)
			}
			caddySnippets(tokens, snippets)
			for _, index := range caddyDirectives(tokens, "import") {
				item := tokens[index+1].value
				if snippets[item] {
					continue
				}
				if strings.ContainsAny(item, "{}\\\x00\r\n") || item == "" {
					return fmt.Errorf("%w: dynamic import %q", ErrCaddyUnsafe, item)
				}
				if !path.IsAbs(item) {
					dir := path.Dir(file.Path)
					if inst.Method == CaddyDocker {
						mapped, ok := caddyContainerPath(inst, file.Path)
						if !ok {
							return ErrCaddyUnsafe
						}
						dir = path.Dir(mapped)
					}
					item = path.Join(dir, item)
				}
				item, ok := caddyHostPath(inst, item)
				if !ok {
					return fmt.Errorf("%w: unmapped container import", ErrCaddyUnsafe)
				}
				if err := visit(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(inst.Config, 0); err != nil {
		return nil, err
	}
	return files, nil
}
