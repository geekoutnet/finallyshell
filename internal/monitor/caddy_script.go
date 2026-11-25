package monitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const caddyDefaultConfig = "/etc/caddy/Caddyfile"
const caddyContainerConfig = "/etc/caddy/Caddyfile"
const caddyFileLimit = 4 << 20

const caddyScriptInput = "exec 'sh' '-s'"

const caddyValidationScratch = `scratch=$(mktemp -d '/tmp/finallyshell-caddy-check.XXXXXXXX') || exit '1'
trap 'rm -rf -- "$scratch"' '0'
trap 'exit 1' HUP INT TERM
mkdir -p -- "$scratch/data" "$scratch/config" || exit '1'
HOME="$scratch"; XDG_DATA_HOME="$scratch/data"; XDG_CONFIG_HOME="$scratch/config"
export HOME XDG_DATA_HOME XDG_CONFIG_HOME
`

func caddyRunPayload(ctx context.Context, run CaddyRunner, script string, progress func(string)) (string, string, error) {
	return run(ctx, caddyScriptInput, strings.NewReader(script), progress)
}

var (
	// ErrCaddyUnsafe indicates a path, identifier, or layout the backend will not operate on.
	ErrCaddyUnsafe = errors.New("unsupported or unsafe Caddy input")
	// ErrCaddyBusy indicates that another configuration transaction holds the lock.
	ErrCaddyBusy = errors.New("another Caddy configuration transaction is in progress")
	// ErrCaddyRollback means recovery did not finish; retain the reported backup for manual recovery.
	ErrCaddyRollback = errors.New("Caddy rollback did not finish")
)

var caddyIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func caddyPath(name string) bool {
	return len(name) <= 4096 && name != "/" && path.IsAbs(name) && path.Clean(name) == name && !strings.ContainsAny(name, "\x00\r\n")
}

func caddyNormalize(inst CaddyInstall) (CaddyInstall, error) {
	if inst.Config == "" {
		inst.Config = caddyDefaultConfig
	}
	if inst.Adapter == "" {
		inst.Adapter = "caddyfile"
		if strings.EqualFold(path.Ext(inst.Config), ".json") {
			inst.Adapter = "json"
		}
	}
	if inst.SitesDir == "" {
		inst.SitesDir = path.Join(path.Dir(inst.Config), "sites")
	}
	if !caddyPath(inst.Config) || !caddyPath(inst.SitesDir) || !caddyIdentifier.MatchString(inst.Adapter) ||
		(inst.Method != CaddyDocker && inst.Binary != "" && !caddyPath(inst.Binary)) ||
		(inst.Envfile != "" && !caddyPath(inst.Envfile)) || (inst.Unit != "" && !(inst.ServiceManager == "openrc" && inst.Unit == "caddy") && !ValidService(inst.Unit)) ||
		(inst.Method == CaddyDocker && !caddyIdentifier.MatchString(inst.Container)) {
		return inst, ErrCaddyUnsafe
	}
	for _, name := range inst.EnvironmentFiles {
		if !caddyPath(strings.TrimPrefix(name, "-")) {
			return inst, ErrCaddyUnsafe
		}
	}
	if inst.WorkingDir != "" && inst.WorkingDir != "/" && !caddyPath(inst.WorkingDir) {
		return inst, ErrCaddyUnsafe
	}
	return inst, nil
}

func CaddyConfigWritable(inst CaddyInstall, name string) bool {
	if inst.Resume || inst.ContainerConfigUnmapped || !caddyPath(name) {
		return false
	}
	if inst.Method != CaddyDocker {
		return true
	}
	_, _, err := caddyChangePaths(inst, CaddyChange{Writes: []CaddyFile{{Path: name}}})
	return len(inst.Mounts) > 0 && err == nil
}

func CaddyConfigPreviewSupported(inst CaddyInstall) bool {
	return !inst.Resume && (inst.Method != CaddyDocker || inst.Active == "active" || inst.Active == "running")
}

func caddyCLI(inst CaddyInstall, args ...string) string {
	if inst.Method == CaddyDocker {
		return SystemCommand(append([]string{"docker", "exec", "-i", inst.Container, "caddy"}, args...)...)
	}
	binary := inst.Binary
	if binary == "" {
		binary = "caddy"
	}
	return SystemCommand(append([]string{binary}, args...)...)
}

func caddyContainerPath(inst CaddyInstall, name string) (string, bool) {
	if inst.ContainerConfigUnmapped {
		return name, caddyPath(name)
	}
	var source, target string
	for _, mount := range inst.Mounts {
		if caddyPath(mount.Source) && caddyPath(mount.Destination) && len(mount.Source) > len(source) &&
			(name == mount.Source || strings.HasPrefix(name, mount.Source+"/")) {
			source, target = mount.Source, mount.Destination
		}
	}
	if source != "" {
		return target + strings.TrimPrefix(name, source), true
	}
	if inst.ContainerConfig != "" && caddyPath(inst.ContainerConfig) && name == inst.Config {
		return inst.ContainerConfig, true
	}
	if inst.ContainerConfig == "" && name == inst.Config {
		return caddyContainerConfig, true
	}
	return "", false
}

func caddyConfigArgs(inst CaddyInstall, verb, config string) []string {
	args := []string{verb, "--config", config}
	if inst.Adapter != "json" {
		args = append(args, "--adapter", inst.Adapter)
	}
	return args
}

const caddyShellEnvironment = `load_env() {
  [ -r "$1" ] || return '1'
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|'#'*|';'*) continue ;; esac
    key=${line%%=*}; value=${line#*=}
    case "$key" in ''|[!a-zA-Z_]*|*[!a-zA-Z0-9_]*) return '1' ;; esac
    [ "$line" != "$value" ] || return '1'
    case "$value" in
      \"*\") value=${value#\"}; value=${value%\"}; case "$value" in *\"*|*\\*) return '1' ;; esac ;;
      \'*\') value=${value#\'}; value=${value%\'}; case "$value" in *\'*) return '1' ;; esac ;;
      *\\*|*\"*|*\'*) return '1' ;;
    esac
    case "$key" in PATH|IFS|ENV|BASH_ENV|SHELLOPTS|LD_*|DYLD_*|GCONV_PATH) return '1' ;; esac
    export "$key=$value" || return '1'
  done < "$1"
}
`

func caddyEnvironment(inst CaddyInstall, command string) string {
	return caddyEnvScript(inst, command, false)
}

func caddyValidationEnvironment(inst CaddyInstall, command string) string {
	return caddyEnvScript(inst, command, true)
}

func caddyEnvScript(inst CaddyInstall, command string, sandbox bool) string {
	if inst.Method == CaddyDocker {
		return command
	}
	files := inst.EnvironmentFiles
	if len(files) == 0 && inst.Envfile != "" {
		files = []string{inst.Envfile}
	}
	if !sandbox && len(files) == 0 && inst.WorkingDir == "" && len(inst.CaddyEnvironment) == 0 {
		return command
	}
	var script strings.Builder
	script.WriteString("(\n" + caddyShellEnvironment)
	if inst.WorkingDir != "" {
		fmt.Fprintf(&script, "cd -- %s || exit '1'\n", privilegeQuote(inst.WorkingDir))
	}
	for _, key := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "CADDY_ADMIN"} {
		if value, ok := inst.CaddyEnvironment[key]; ok && !strings.ContainsAny(value, "\x00\r\n") {
			fmt.Fprintf(&script, "export %s\n", privilegeQuote(key+"="+value))
		}
	}
	for _, name := range files {
		if actual, optional := strings.CutPrefix(name, "-"); optional {
			fmt.Fprintf(&script, "if [ -e %s ]; then load_env %s || exit '1'; fi\n", privilegeQuote(actual), privilegeQuote(actual))
		} else {
			fmt.Fprintf(&script, "load_env %s || exit '1'\n", privilegeQuote(name))
		}
	}
	if sandbox {
		script.WriteString(caddyValidationScratch)
	}
	script.WriteString(command + "\n)")
	return script.String()
}

func caddyOutput(stdout, stderr string) string {
	return strings.TrimSpace(stdout + "\n" + stderr)
}

func caddyCommandError(err error, output string) error {
	if output == "" {
		return err
	}
	if err == nil {
		return errors.New(output)
	}
	return fmt.Errorf("%w: %s", err, output)
}
