package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const caddyDetectPrelude = `export LC_ALL=C
record() {
  printf 'CADDY %s ' "$1"
  printf '%s' "$2" | base64 | tr -d '\n'
  printf '\n'
}
inspect_binary() {
  [ -x "$1" ] || return '1'
  version=$("$1" 'version' 2>/dev/null) || return '1'
  method='binary'; latest=''
  if dpkg-query -S -- "$1" >/dev/null 2>&1; then
    method='apt'; latest=$(apt-cache policy 'caddy' 2>/dev/null)
  elif rpm -qf -- "$1" >/dev/null 2>&1; then
    method='dnf'; latest=$(dnf -q -C info --available 'caddy' 2>/dev/null)
  elif pacman -Qo -- "$1" >/dev/null 2>&1; then
    method='pacman'; latest=$(pacman -Si 'caddy' 2>/dev/null)
  elif apk info -W "$1" >/dev/null 2>&1; then
    method='apk'; latest=$(apk policy 'caddy' 2>/dev/null)
  fi
  record 'host' "$(printf '%s\n%s\n%s' "$1" "$method" "$version")"
  record 'latest' "$latest"
  record 'modules' "$("$1" 'list-modules' '--skip-standard' 2>/dev/null)"
}
`

const caddyDetectScript = caddyDetectPrelude + `
if [ -r '/etc/os-release' ]; then record 'os' "$(grep -E '^(ID|VERSION_ID)=' '/etc/os-release')"; fi
for binary in "$(command -v 'caddy' 2>/dev/null)" '/usr/bin/caddy' '/usr/local/bin/caddy' '/opt/caddy/caddy'; do
  case "$binary" in /*) [ -x "$binary" ] && inspect_binary "$binary" && break ;; esac
done
if command -v 'systemctl' >/dev/null 2>&1; then
  units=$({ printf '%s\n' 'caddy.service' 'caddy-api.service'; systemctl 'list-unit-files' '--type=service' '--no-legend' '--no-pager' 2>/dev/null | awk '{print $1}'; systemctl 'list-units' '--all' '--type=service' '--no-legend' '--no-pager' '--plain' 2>/dev/null | awk '{print $1}'; } | sort -u)
  set -f
  set -- $units
  systemctl 'show' '-p' 'Id,FragmentPath,ExecStart,ActiveState,UnitFileState,User,EnvironmentFiles,Environment' '--' "$@" 2>/dev/null |
    awk 'BEGIN {RS=""; FS="\n"} /ExecStart=[^\n]*path=[^;]*\/caddy[ ;]/ {
      for (i=1;i<=NF;i++) {
        if ($i !~ /^Environment=/) print $i
        else {
          n=split(substr($i,13),env," "); for(j=1;j<=n;j++)
            if (env[j] ~ /^"?(CADDY_ADMIN|XDG_DATA_HOME|HOME)=/) print "CaddyEnv=" env[j]
        }
      }
      print ""
    }' | while IFS= read -r line; do
      if [ -n "$line" ]; then unit="${unit-}$line
"; elif [ -n "${unit-}" ]; then record 'unit' "$unit"; unit=''; fi
    done
  set +f
elif command -v 'rc-service' >/dev/null 2>&1 && [ -f '/etc/init.d/caddy' ]; then
  active='inactive'; enabled='disabled'
  if rc-service 'caddy' 'status' >/dev/null 2>&1; then active='active'; fi
  if rc-update 'show' 'default' 2>/dev/null | grep -Eq '^[[:space:]]*caddy[[:space:]]'; then enabled='enabled'; fi
  record 'openrc' "$(printf '%s\n%s' "$active" "$enabled")"
fi
if command -v 'docker' >/dev/null 2>&1; then
  docker 'ps' '-a' '--format' '{{json .}}' 2>/dev/null | while IFS= read -r row; do record 'ps' "$row"; done
fi
if command -v 'ss' >/dev/null 2>&1; then record 'ports' "$(ss '-ltnp' 2>/dev/null)"; fi
record 'done' ''
`

type caddyRecord struct{ tag, value string }

func caddyRecords(text string) ([]caddyRecord, error) {
	var records []caddyRecord
	for line := range strings.SplitSeq(text, "\n") {
		rest, found := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "CADDY ")
		if !found {
			continue
		}
		tag, data, found := strings.Cut(rest, " ")
		value, err := base64.StdEncoding.DecodeString(data)
		if !found || err != nil {
			return nil, ErrCaddyUnsafe
		}
		records = append(records, caddyRecord{tag, string(value)})
	}
	return records, nil
}

func caddyRunRecords(ctx context.Context, run CaddyRunner, script string) ([]caddyRecord, error) {
	stdout, stderr, err := run(ctx, script, nil, nil)
	if err != nil {
		return nil, caddyCommandError(err, strings.TrimSpace(stderr))
	}
	return caddyRecords(stdout)
}

func caddyPackageVersion(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		key, value, _ := strings.Cut(line, ":")
		if strings.TrimSpace(key) == "Candidate" || strings.TrimSpace(key) == "Version" {
			if value = strings.TrimSpace(value); value != "(none)" {
				return value
			}
		}
	}
	return ""
}

var caddyUnitArgs = regexp.MustCompile(`argv\[\]=(.*?)(?: ;|$)`)
var caddyUnitPath = regexp.MustCompile(`(?:^|[ {])path=(.*?) ;`)
var caddyEnvPaths = regexp.MustCompile(`\s*([^\n]*?)\s+\(ignore_errors=(yes|no)\)`)
var caddyHexEscape = regexp.MustCompile(`\\x[0-9a-fA-F]{2}`)

func caddyUnitWords(text string) []string {
	text = caddyHexEscape.ReplaceAllStringFunc(text, func(escape string) string {
		if escape == `\x20` || escape == `\x09` {
			return `\` + map[string]string{`\x20`: " ", `\x09`: "\t"}[escape]
		}
		return escape
	})
	var words []string
	var word strings.Builder
	var quote byte
	for index := 0; index < len(text); index++ {
		char := text[index]
		switch {
		case char == '\\' && index+1 < len(text):
			if index+3 < len(text) && text[index+1] == 'x' {
				if value, err := strconv.ParseUint(text[index+2:index+4], 16, 8); err == nil {
					word.WriteByte(byte(value))
					index += 3
					continue
				}
			}
			index++
			word.WriteByte(text[index])
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				word.WriteByte(char)
			}
		case char == '\'' || char == '"':
			quote = char
		case caddySpace(char):
			if word.Len() != 0 {
				words = append(words, word.String())
				word.Reset()
			}
		default:
			word.WriteByte(char)
		}
	}
	if word.Len() != 0 {
		words = append(words, word.String())
	}
	return words
}

func caddyStartArgs(inst *CaddyInstall, words []string) {
	for index := 0; index < len(words); index++ {
		key, value, inline := strings.Cut(words[index], "=")
		if key == "--resume" {
			inst.Resume = !inline || value == "true"
		}
		if key != "--config" && key != "-c" && key != "--adapter" && key != "-a" {
			continue
		}
		if !inline && index+1 < len(words) {
			index++
			value = words[index]
		}
		if key == "--config" || key == "-c" {
			if strings.HasPrefix(value, "/") {
				inst.Config = path.Clean(value)
			} else {
				inst.Config = value
			}
		} else {
			inst.Adapter = value
		}
	}
}

func caddyParseUnit(text string) (CaddyInstall, map[string]string, bool) {
	var inst CaddyInstall
	env := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "Id":
			inst.Unit = value
		case "FragmentPath":
			inst.UnitFile = value
		case "ExecStart":
			parts, args := caddyUnitPath.FindStringSubmatch(value), caddyUnitArgs.FindStringSubmatch(value)
			if len(parts) == 2 {
				inst.Binary = strings.Trim(parts[1], `"`)
			}
			if len(args) == 2 {
				caddyStartArgs(&inst, caddyUnitWords(args[1]))
			}
		case "ActiveState":
			inst.Active = value
		case "UnitFileState":
			inst.Enabled = value == "enabled" || value == "enabled-runtime"
		case "User":
			inst.User = value
		case "EnvironmentFiles":
			for _, parts := range caddyEnvPaths.FindAllStringSubmatch(value, -1) {
				name := strings.Trim(parts[1], `"`)
				if parts[2] == "yes" {
					name = "-" + name
				}
				inst.EnvironmentFiles = append(inst.EnvironmentFiles, name)
			}
			if len(inst.EnvironmentFiles) == 0 && caddyPath(value) {
				inst.EnvironmentFiles = append(inst.EnvironmentFiles, value)
			}
			if len(inst.EnvironmentFiles) > 0 {
				inst.Envfile = strings.TrimPrefix(inst.EnvironmentFiles[0], "-")
			}
		case "Environment", "CaddyEnv":
			for _, word := range caddyUnitWords(value) {
				name, val, found := strings.Cut(word, "=")
				if found && (name == "HOME" || name == "XDG_DATA_HOME" || name == "CADDY_ADMIN") {
					env[name] = val
				}
			}
		}
	}
	inst.ServiceManager = "systemd"
	return inst, env, ValidService(inst.Unit) && caddyPath(inst.Binary) && path.Base(inst.Binary) == "caddy"
}

func caddyUnitRank(inst CaddyInstall) int {
	rank := 0
	if inst.Active == "active" || inst.Active == "activating" {
		rank += 8
	}
	if inst.Enabled {
		rank += 4
	}
	switch inst.Unit {
	case "caddy.service":
		rank += 2
	case "caddy-api.service":
		rank++
	}
	return rank
}

type caddyDockerInspect struct {
	Name  string
	Args  []string
	State struct {
		Status  string
		Running bool
	}
	Config struct {
		Image string
		Env   []string
	}
	HostConfig      struct{ RestartPolicy struct{ Name string } }
	Mounts          []CaddyMount
	NetworkSettings struct{ Ports json.RawMessage }
}

const caddyDockerInspectFormat = `{"Name":{{json .Name}},"Args":{{json .Args}},"State":{{json .State}},"Config":{"Image":{{json .Config.Image}},"Env":[{{range .Config.Env}}{{if or (eq (index (split . "=") 0) "CADDY_ADMIN") (eq (index (split . "=") 0) "XDG_DATA_HOME")}}{{json .}},{{end}}{{end}}""]},"HostConfig":{"RestartPolicy":{{json .HostConfig.RestartPolicy}}},"Mounts":{{json .Mounts}},"NetworkSettings":{"Ports":{{json .NetworkSettings.Ports}}}}`

func caddyImage(image string) bool {
	image = strings.SplitN(image, "@", 2)[0]
	return strings.HasPrefix(strings.SplitN(path.Base(image), ":", 2)[0], "caddy")
}

func caddyParseDocker(text string) (CaddyInstall, map[string]string, bool) {
	var info caddyDockerInspect
	if json.Unmarshal([]byte(text), &info) != nil {
		return CaddyInstall{}, nil, false
	}
	name := strings.TrimPrefix(info.Name, "/")
	if !caddyIdentifier.MatchString(name) || !caddyImage(info.Config.Image) {
		return CaddyInstall{}, nil, false
	}
	inst := CaddyInstall{Method: CaddyDocker, Container: name, ContainerImage: info.Config.Image, Active: info.State.Status, Mounts: info.Mounts, Config: caddyContainerConfig, Ports: string(info.NetworkSettings.Ports), RestartPolicy: info.HostConfig.RestartPolicy.Name, ServiceManager: "docker"}
	if info.State.Running {
		inst.Active = "active"
	} else if inst.Active == "" || inst.Active == "exited" || inst.Active == "created" {
		inst.Active = "inactive"
	}
	inst.Enabled = inst.RestartPolicy == "always" || inst.RestartPolicy == "unless-stopped"
	caddyStartArgs(&inst, info.Args)
	inst.ContainerConfig = inst.Config
	if mapped, ok := caddyHostPath(inst, inst.Config); ok {
		inst.Config = mapped
	}
	env := map[string]string{}
	for _, assignment := range info.Config.Env {
		if key, value, ok := strings.Cut(assignment, "="); ok {
			env[key] = value
		}
	}
	return inst, env, true
}

var caddyProcess = regexp.MustCompile(`\("([^"\n]+)",pid=`)

func caddyListening(text string) (listening, conflicts []string) {
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "LISTEN" {
			continue
		}
		address := fields[3]
		port80or443 := strings.HasSuffix(address, ":80") || strings.HasSuffix(address, ":443")
		for _, match := range caddyProcess.FindAllStringSubmatch(line, -1) {
			switch process := match[1]; {
			case process == "caddy":
				listening = append(listening, address)
			case port80or443 && process != "docker-proxy":
				conflicts = append(conflicts, process)
			case port80or443:
				conflicts = append(conflicts, "docker-proxy")
			}
		}
	}
	slices.Sort(listening)
	slices.Sort(conflicts)
	return slices.Compact(listening), slices.Compact(conflicts)
}

func caddyModules(text string) []string {
	var modules []string
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 0 && strings.Contains(fields[0], ".") && caddyIdentifier.MatchString(fields[0]) {
			modules = append(modules, fields[0])
		}
	}
	slices.Sort(modules)
	return slices.Compact(modules)
}

func caddyUseHostRecords(inst *CaddyInstall, records []caddyRecord) {
	for _, record := range records {
		switch record.tag {
		case "host":
			if parts := strings.SplitN(record.value, "\n", 3); len(parts) == 3 && caddyPath(parts[0]) {
				inst.Binary, inst.Method, inst.Version = parts[0], parts[1], parts[2]
			}
		case "latest":
			inst.PackageLatest = caddyPackageVersion(record.value)
		case "modules":
			inst.Modules = caddyModules(record.value)
		}
	}
}

func detectCaddy(ctx context.Context, run CaddyRunner) (CaddyInstall, error) {
	records, err := caddyRunRecords(ctx, run, caddyDetectScript)
	if err != nil {
		return CaddyInstall{}, err
	}
	native, selected := CaddyInstall{}, CaddyInstall{}
	caddyUseHostRecords(&native, records)
	var osInfo, ports, openrc string
	var env map[string]string
	var candidates []string
	completed := false
	for _, record := range records {
		switch record.tag {
		case "done":
			completed = true
		case "os":
			osInfo = record.value
		case "ports":
			ports = record.value
		case "openrc":
			openrc = record.value
		case "unit":
			if unit, values, ok := caddyParseUnit(record.value); ok && (selected.Unit == "" || caddyUnitRank(unit) > caddyUnitRank(selected)) {
				selected, env = unit, values
			}
		case "ps":
			var row struct{ Names, Image, State string }
			if json.Unmarshal([]byte(record.value), &row) == nil && caddyIdentifier.MatchString(row.Names) && caddyImage(row.Image) {
				candidates = append(candidates, row.Names)
			}
		case "docker":
			if docker, values, ok := caddyParseDocker(record.value); ok && selected.Active != "active" && (docker.Active == "active" || (selected.Unit == "" && native.Method == "")) {
				selected, env = docker, values
			}
		}
	}
	if !completed {
		return CaddyInstall{}, fmt.Errorf("%w: incomplete detection report", ErrCaddyUnsafe)
	}
	if len(candidates) > 0 && selected.Active != "active" {
		slices.Sort(candidates)
		if len(candidates) > 32 {
			candidates = candidates[:32]
		}
		var inspect strings.Builder
		inspect.WriteString(caddyDetectPrelude)
		for _, name := range candidates {
			fmt.Fprintf(&inspect, "if info=$(%s 2>/dev/null); then record 'docker' \"$info\"; fi\n", SystemCommand("docker", "inspect", "--type", "container", "--format", caddyDockerInspectFormat, name))
		}
		rows, err := caddyRunRecords(ctx, run, inspect.String())
		if err != nil {
			return CaddyInstall{}, err
		}
		for _, row := range rows {
			if docker, values, ok := caddyParseDocker(row.value); row.tag == "docker" && ok && (docker.Active == "active" || (selected.Unit == "" && selected.Container == "" && native.Method == "")) {
				selected, env = docker, values
				if docker.Active == "active" {
					break
				}
			}
		}
	}
	if selected.Unit != "" {
		if selected.Binary != native.Binary {
			rows, err := caddyRunRecords(ctx, run, caddyDetectPrelude+"inspect_binary "+privilegeQuote(selected.Binary)+"\n")
			if err != nil {
				return CaddyInstall{}, err
			}
			selected.Method = CaddyBinary
			caddyUseHostRecords(&selected, rows)
		} else {
			selected.Method, selected.Version, selected.PackageLatest, selected.Modules = native.Method, native.Version, native.PackageLatest, native.Modules
		}
	} else if selected.Method != CaddyDocker {
		selected = native
		selected.ServiceManager = "none"
		if openrc != "" && selected.Method != "" {
			selected.ServiceManager = "openrc"
			active, enabled, _ := strings.Cut(openrc, "\n")
			selected.Active, selected.Enabled = active, enabled == "enabled"
		}
	}
	for line := range strings.SplitSeq(osInfo, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "ID":
			selected.OS = strings.Trim(value, `"'`)
		case "VERSION_ID":
			selected.OSVersion = strings.Trim(value, `"'`)
		}
	}
	selected.Listening, selected.Conflicts = caddyListening(ports)
	selected.AdminAddr = "localhost:2019"
	selected, err = caddyNormalize(selected)
	if err != nil || selected.Method == CaddyNotInstalled {
		return selected, err
	}
	return caddyDetectDetails(ctx, run, selected, env)
}

func caddyDetectDetails(ctx context.Context, run CaddyRunner, inst CaddyInstall, env map[string]string) (CaddyInstall, error) {
	var script strings.Builder
	script.WriteString(caddyDetectPrelude + caddyShellEnvironment)
	script.WriteString("unset CADDY_ADMIN XDG_DATA_HOME\n")
	if inst.Method == CaddyDocker {
		fmt.Fprintf(&script, "record 'version' \"$(%s 2>/dev/null)\"\nrecord 'modules' \"$(%s 2>/dev/null)\"\n", caddyCLI(inst, "version"), caddyCLI(inst, "list-modules", "--skip-standard"))
	} else {
		user := inst.User
		if user == "" {
			user = "root"
			if inst.ServiceManager != "systemd" {
				user = "caddy"
			}
		}
		fmt.Fprintf(&script, "record 'home' \"$(%s 2>/dev/null | cut -d ':' -f '6')\"\n", SystemCommand("getent", "passwd", user))
		for _, name := range inst.EnvironmentFiles {
			fmt.Fprintf(&script, "load_env %s >/dev/null 2>&1 || :\n", privilegeQuote(strings.TrimPrefix(name, "-")))
		}
		script.WriteString("record 'admin' \"${CADDY_ADMIN-}\"\nrecord 'xdg' \"${XDG_DATA_HOME-}\"\n")
	}
	fmt.Fprintf(&script, "if [ -d %s ]; then record 'dir' 'yes'; fi\nif [ -f %s ]; then record 'config' \"$(head -c '4194305' -- %s)\"; fi\n", privilegeQuote(inst.SitesDir), privilegeQuote(inst.Config), privilegeQuote(inst.Config))
	rows, err := caddyRunRecords(ctx, run, script.String())
	if err != nil {
		return inst, err
	}
	var conf, home, xdg, admin string
	hasDir := false
	for _, row := range rows {
		switch row.tag {
		case "version":
			inst.Version = row.value
		case "modules":
			inst.Modules = caddyModules(row.value)
		case "home":
			home = row.value
		case "xdg":
			xdg = row.value
		case "admin":
			admin = row.value
		case "config":
			if len(row.value) <= caddyFileLimit {
				conf = row.value
			}
		case "dir":
			hasDir = row.value == "yes"
		}
	}
	if env["HOME"] != "" {
		home = env["HOME"]
	}
	if xdg == "" {
		xdg = env["XDG_DATA_HOME"]
	}
	if admin == "" {
		admin = env["CADDY_ADMIN"]
	}
	if admin != "" {
		inst.AdminAddr = admin
	}
	if inst.Method == CaddyDocker {
		if !caddyPath(xdg) {
			xdg = "/data"
		}
		inst.DataDir, _ = caddyHostPath(inst, path.Join(xdg, "caddy"))
	} else if caddyPath(xdg) {
		inst.DataDir = path.Join(xdg, "caddy")
	} else {
		if !caddyPath(home) {
			home = "/var/lib/caddy"
		}
		inst.DataDir = path.Join(home, ".local/share/caddy")
	}
	caddyDetectConfig(&inst, conf, hasDir)
	return inst, nil
}

func caddyDetectConfig(inst *CaddyInstall, config string, hasDir bool) {
	if inst.Adapter == "json" {
		var conf struct {
			Admin struct {
				Disabled bool
				Listen   string
			}
		}
		if json.Unmarshal([]byte(config), &conf) == nil {
			if conf.Admin.Disabled {
				inst.AdminAddr = "off"
			} else if conf.Admin.Listen != "" {
				inst.AdminAddr = conf.Admin.Listen
			}
		}
		return
	}
	tokens, err := caddyTokens(config)
	if err != nil || len(tokens) == 0 {
		return
	}
	depth := 0
	for index, token := range tokens {
		switch {
		case token.is("{"):
			depth++
		case token.is("}"):
			depth--
		case depth == 1 && tokens[0].is("{") && token.is("admin") && index+1 < len(tokens) && tokens[index+1].line == token.line:
			value := tokens[index+1].value
			if value != "{" && !strings.HasPrefix(value, "{$") {
				inst.AdminAddr = value
			}
		case depth == 0 && hasDir && token.is("import") && index+1 < len(tokens) && tokens[index+1].line == token.line:
			pattern := tokens[index+1].value
			configPath := inst.Config
			if inst.Method == CaddyDocker {
				configPath = inst.ContainerConfig
			}
			if !path.IsAbs(pattern) {
				pattern = path.Join(path.Dir(configPath), pattern)
			}
			if host, ok := caddyHostPath(*inst, pattern); ok && path.Dir(host) == inst.SitesDir && (path.Base(host) == "*" || path.Base(host) == "*.caddy") {
				inst.Managed = true
			}
		}
	}
}
