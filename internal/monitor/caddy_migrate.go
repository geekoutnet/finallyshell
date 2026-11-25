package monitor

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// AdoptCaddy performs read-only detection. It does not install, write or
// restart anything; the UI may store its own decision to manage the result.
func AdoptCaddy(ctx context.Context, run CaddyRunner) (CaddyInstall, error) {
	return DetectCaddy(ctx, run)
}

type caddyBlock struct {
	start, end int
	address    string
}

func caddyTopLevelBlocks(text string) ([]caddyBlock, error) {
	tokens, err := caddyTokens(text)
	if err != nil {
		return nil, err
	}
	var blocks []caddyBlock
	depth, previous, start := 0, 0, 0
	var address string
	for index, token := range tokens {
		if depth == 0 && index == previous && token.is("import") {
			if index+1 >= len(tokens) || tokens[index+1].line != token.line {
				return nil, ErrCaddyInvalid
			}
			end := index + 1
			for end+1 < len(tokens) && tokens[end+1].line == token.line && !tokens[end+1].is("{") {
				end++
			}
			if end+1 < len(tokens) && tokens[end+1].line == token.line {
				return nil, ErrCaddyUnsafe
			}
			blocks = append(blocks, caddyBlock{start: token.start, end: tokens[end].end})
			previous = end + 1
			continue
		}
		if index < previous {
			continue
		}
		switch {
		case token.is("{"):
			if depth == 0 {
				start = tokens[previous].start
				address = ""
				if previous < index {
					if len(caddyDirectives(tokens[previous:index], "import")) != 0 {
						return nil, ErrCaddyUnsafe
					}
					address = strings.TrimSuffix(tokens[previous].value, ",")
				}
			}
			depth++
		case token.is("}"):
			depth--
			if depth < 0 {
				return nil, ErrCaddyInvalid
			}
			if depth == 0 {
				blocks = append(blocks, caddyBlock{start: start, end: token.end, address: address})
				previous = index + 1
			}
		}
	}
	if depth != 0 {
		return nil, ErrCaddyInvalid
	}
	if previous != len(tokens) {
		return nil, ErrCaddyUnsafe
	}
	return blocks, nil
}

var caddySiteFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func CaddySiteFileBase(address string) string {
	address = strings.TrimPrefix(strings.TrimPrefix(address, "https://"), "http://")
	address = strings.Trim(caddySiteFilename.ReplaceAllString(address, "-"), ".-")
	if address == "" {
		address = "site"
	}
	if len(address) > 96 {
		address = address[:96]
	}
	return strings.ToLower(address)
}

func caddyRelative(from, to string) string {
	left := strings.Split(strings.TrimPrefix(from, "/"), "/")
	right := strings.Split(strings.TrimPrefix(to, "/"), "/")
	for len(left) > 0 && len(right) > 0 && left[0] == right[0] {
		left, right = left[1:], right[1:]
	}
	if len(left) == 1 && left[0] == "" {
		left = nil
	}
	return strings.Repeat("../", len(left)) + strings.Join(right, "/")
}

func caddyMoveImports(content, from, to string, snippets map[string]bool) (string, error) {
	tokens, err := caddyTokens(content)
	if err != nil {
		return "", err
	}
	var result strings.Builder
	last := 0
	for _, index := range caddyDirectives(tokens, "import") {
		arg := tokens[index+1]
		if path.IsAbs(arg.value) || snippets[arg.value] {
			continue
		}
		if strings.ContainsAny(arg.value, "{}\\\x00\r\n") {
			return "", ErrCaddyUnsafe
		}
		moved := caddyRelative(to, path.Join(from, arg.value))
		result.WriteString(content[last:arg.start])
		if strings.ContainsAny(moved, " \t\"#`") || arg.quoted {
			result.WriteString(caddyQuoteToken(moved))
		} else {
			result.WriteString(moved)
		}
		last = arg.end
	}
	result.WriteString(content[last:])
	return result.String(), nil
}

func caddyManagedChange(inst CaddyInstall, files []CaddyFile) (CaddyChange, error) {
	if inst.SitesDir == "" || len(files) == 0 || files[0].Path != inst.Config {
		return CaddyChange{}, ErrCaddyUnsafe
	}
	main := files[0].Content
	blocks, err := caddyTopLevelBlocks(main)
	if err != nil {
		return CaddyChange{}, err
	}
	change := CaddyChange{Writes: []CaddyFile{{Path: inst.Config}}, Expect: map[string]string{}, Reload: true}
	snippets := map[string]bool{}
	for _, file := range files {
		change.Expect[file.Path] = file.Content
		tokens, err := caddyTokens(file.Content)
		if err != nil {
			return CaddyChange{}, err
		}
		caddySnippets(tokens, snippets)
	}
	for _, file := range files {
		tokens, _ := caddyTokens(file.Content)
		for _, index := range caddyDirectives(tokens, "import") {
			item := tokens[index+1].value
			if snippets[item] || !strings.ContainsAny(item, "*?") {
				continue
			}
			if !path.IsAbs(item) {
				name, ok := caddyContainerPath(inst, file.Path)
				if inst.Method != CaddyDocker {
					name, ok = file.Path, true
				}
				if !ok {
					return CaddyChange{}, ErrCaddyUnsafe
				}
				item = path.Join(path.Dir(name), item)
			}
			if item, ok := caddyHostPath(inst, item); ok && !strings.Contains(item, "[") {
				change.importPatterns = append(change.importPatterns, item)
			}
		}
	}
	originalDir, sitesDir := path.Dir(inst.Config), inst.SitesDir
	if inst.Method == CaddyDocker {
		containerConfig, configOK := caddyContainerPath(inst, inst.Config)
		containerSites, sitesOK := caddyContainerPath(inst, inst.SitesDir)
		if !configOK || !sitesOK {
			return CaddyChange{}, ErrCaddyUnsafe
		}
		originalDir, sitesDir = path.Dir(containerConfig), containerSites
	}
	var keep strings.Builder
	previous := 0
	used := map[string]bool{}
	for _, block := range blocks {
		if block.address == "" || (strings.HasPrefix(block.address, "(") && strings.HasSuffix(block.address, ")")) || strings.HasPrefix(block.address, "&(") {
			keep.WriteString(main[previous:block.end])
			previous = block.end
			continue
		}
		start := block.start
		if leading := main[previous:start]; strings.Contains(leading, "#") {
			start = previous
		} else {
			keep.WriteString(leading)
		}
		content, err := caddyMoveImports(main[start:block.end], originalDir, sitesDir, snippets)
		if err != nil {
			return CaddyChange{}, err
		}
		base := CaddySiteFileBase(block.address)
		name := base + ".caddy"
		for index := 2; used[name]; index++ {
			name = base + "-" + strconv.Itoa(index) + ".caddy"
		}
		used[name] = true
		filename := path.Join(inst.SitesDir, name)
		change.Writes = append(change.Writes, CaddyFile{Path: filename, Content: strings.TrimSpace(content) + "\n"})
		change.ExpectMissing = append(change.ExpectMissing, filename)
		previous = block.end
	}
	if len(change.Writes) < 2 {
		return CaddyChange{}, ErrCaddyUnsafe
	}
	keep.WriteString(main[previous:])
	config := strings.TrimSpace(keep.String())
	if config != "" {
		config += "\n\n"
	}
	pattern := path.Join(caddyRelative(originalDir, sitesDir), "*.caddy")
	if strings.ContainsAny(pattern, " \t\"#`") {
		pattern = caddyQuoteToken(pattern)
	}
	change.Writes[0].Content = config + "import " + pattern + "\n"
	return change, nil
}

// MigrateToManaged splits explicit top-level sites into SitesDir, retaining
// global options, snippets and top-level imports, and rebasing relative
// imports in the moved sites. It refuses unbraced sites and managed layouts.
// ApplyCaddyChange verifies the source files, backs up, validates and reloads.
func MigrateToManaged(ctx context.Context, run CaddyRunner, inst CaddyInstall) (CaddyApplyResult, error) {
	change, err := PreviewCaddyManaged(ctx, run, inst)
	if err != nil {
		return CaddyApplyResult{}, err
	}
	return ApplyCaddyChange(ctx, run, inst, change)
}

// PreviewCaddyManaged reads the source graph and constructs the checked
// transaction used by MigrateToManaged, without writing or reloading.
func PreviewCaddyManaged(ctx context.Context, run CaddyRunner, inst CaddyInstall) (CaddyChange, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || inst.Adapter != "caddyfile" || inst.Managed || inst.Resume || inst.ContainerConfigUnmapped {
		return CaddyChange{}, ErrCaddyUnsafe
	}
	files, err := ReadCaddyFiles(ctx, run, inst)
	if err != nil {
		return CaddyChange{}, err
	}
	change, err := caddyManagedChange(inst, files)
	if err != nil {
		return CaddyChange{}, err
	}
	change.requireEmptySites = true
	return change, nil
}

func caddySystemdWord(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\n\r$%") {
		return "", ErrCaddyUnsafe
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`, nil
}

func CaddyCanMigrateBinary(inst CaddyInstall) bool {
	method := caddyPackageMethod(inst.OS)
	return inst.Method == CaddyBinary && ValidService(inst.Unit) && !inst.Resume && len(inst.Modules) == 0 && caddyPath(inst.Binary) && caddyPath(inst.DataDir) &&
		path.Base(inst.DataDir) == "caddy" && (method == CaddyAPT || method == CaddyDNF) &&
		(inst.UnitFile == "" || caddyPath(inst.UnitFile) && strings.HasPrefix(inst.UnitFile, "/etc/systemd/system/"))
}

func caddyPackageMigrationScript(inst CaddyInstall) (string, error) {
	if !CaddyCanMigrateBinary(inst) {
		return "", ErrCaddyUnsafe
	}
	method := caddyPackageMethod(inst.OS)
	setup, install, err := caddyPackageInstallCommands(inst.OS, method)
	if err != nil {
		return "", err
	}
	dir := "/etc/systemd/system/" + inst.Unit + ".d"
	override := dir + "/99-finallyshell-caddy.conf"
	args := append([]string{"/usr/bin/caddy"}, caddyConfigArgs(inst, "run", inst.Config)...)
	reload := append([]string{"/usr/bin/caddy"}, caddyConfigArgs(inst, "reload", inst.Config)...)
	reload = append(reload, "--force")
	var unit strings.Builder
	unit.WriteString("[Service]\n")
	data, err := caddySystemdWord("XDG_DATA_HOME=" + path.Dir(inst.DataDir))
	if err != nil {
		return "", err
	}
	unit.WriteString("Environment=" + data + "\n")
	for index, words := range [][]string{args, reload} {
		key := []string{"ExecStart", "ExecReload"}[index]
		unit.WriteString(key + "=\n" + key + "=")
		for argIndex, word := range words {
			quoted, err := caddySystemdWord(word)
			if err != nil {
				return "", err
			}
			if argIndex > 0 {
				unit.WriteByte(' ')
			}
			unit.WriteString(quoted)
		}
		unit.WriteByte('\n')
	}
	var script strings.Builder
	script.WriteString(caddyInstallShell + caddyConfigSnapshot)
	if method == CaddyAPT {
		script.WriteString(caddyDownload)
	}
	fmt.Fprintf(&script, "old=%s\nconfig=%s\nunit=%s\ndropdir=%s\noverride=%s\n", privilegeQuote(inst.Binary), privilegeQuote(inst.Config), privilegeQuote(inst.Unit), privilegeQuote(dir), privilegeQuote(override))
	script.WriteString(`[ -f "$old" ] && [ ! -L "$old" ] && [ -f "$config" ] && [ ! -L "$config" ]
[ ! -e "$override" ] && [ ! -L "$override" ] && [ ! -L "$dropdir" ]
if [ "$old" != '/usr/bin/caddy' ]; then [ ! -e '/usr/bin/caddy' ] && [ ! -L '/usr/bin/caddy' ]; fi
snapshot_config "${config%/*}" "$config"
cp -p -- "$old" "$config_backup/caddy"
`)
	script.WriteString(setup)
	if method == CaddyAPT {
		script.WriteString(caddyNoAPTStart)
	}
	remove := "dnf 'remove' '-y' '--noautoremove' 'caddy'"
	if method == CaddyAPT {
		remove = "apt-get 'remove' '-y' 'caddy'"
	}
	script.WriteString(`dirty='yes'; package=''; dropped=''
recover() {
  status=$?; trap - '0'; trap '' HUP INT TERM; set +e
  if [ -n "$dirty" ]; then
    failed=''
    systemctl 'stop' "$unit" || failed='yes'
    if [ -n "$dropped" ]; then rm -f -- "$override" || failed='yes'; fi
`)
	fmt.Fprintf(&script, "    if [ -n \"$package\" ]; then { %s; } || failed='yes'; fi\n", remove)
	script.WriteString(`    if [ ! -e "$old" ] || [ "$old" = '/usr/bin/caddy' ]; then
      place "$config_backup/caddy" "$old" '0755' || failed='yes'
    fi
    restore_config "$config" || failed='yes'
    systemctl 'daemon-reload' || failed='yes'
`)
	if inst.Active == "active" {
		script.WriteString("    systemctl 'start' \"$unit\" || failed='yes'\n")
	}
	script.WriteString(`    if [ -n "$failed" ]; then printf '%s\n' 'CADDY_RECOVERY_FAILED' "$config_backup" >&2; status='1'; fi
  fi
  if [ -n "${own_policy-}" ]; then rm -f -- '/usr/sbin/policy-rc.d'; fi
  rm -rf -- "$tmp"
  exit "$status"
}
trap recover '0'
systemctl 'stop' "$unit"
if [ "$old" = '/usr/bin/caddy' ]; then rm -f -- "$old"; fi
package='yes'
`)
	script.WriteString(install + "\nrestore_config \"$config\"\n")
	if inst.Unit != "caddy.service" && method == CaddyAPT {
		script.WriteString("systemctl 'disable' '--now' 'caddy.service'\n")
	}
	caddyPayload(&script, `base64 -d > "$tmp/override"`, unit.String())
	script.WriteString("mkdir -p -- \"$dropdir\"\ndropped='yes'\nplace \"$tmp/override\" \"$override\" '0644'\nsystemctl 'daemon-reload'\n")
	inst.Binary = "/usr/bin/caddy"
	script.WriteString(caddyValidateCommand(inst, inst.Config) + "\nsystemctl 'start' \"$unit\"\nsystemctl 'is-active' '--quiet' \"$unit\"\ndirty=''\n")
	return script.String(), nil
}

// MigrateBinaryToPackage installs a signed APT or COPR package in place of
// a standard hand-installed systemd Caddy. It retains the selected unit,
// config and data directory with a drop-in and restores the previous binary
// and config on failure. Custom modules and --resume installs are refused.
func MigrateBinaryToPackage(ctx context.Context, run CaddyRunner, inst CaddyInstall, progress ...func(string)) (CaddyInstall, error) {
	inst, err := caddyNormalize(inst)
	if err != nil {
		return inst, err
	}
	script, err := caddyPackageMigrationScript(inst)
	if err != nil {
		return inst, err
	}
	stdout, stderr, err := run(ctx, script, nil, caddyProgress(progress))
	if err := caddyLifecycleError(stdout, stderr, err); err != nil {
		return inst, err
	}
	return caddyUpdatedDetection(ctx, run, inst)
}
