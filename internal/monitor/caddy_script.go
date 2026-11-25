package monitor

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const caddyDefaultConfig = "/etc/caddy/Caddyfile"
const caddyContainerConfig = "/etc/caddy/Caddyfile"
const caddyFileLimit = 4 << 20

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
	return name != "/" && path.IsAbs(name) && path.Clean(name) == name && !strings.ContainsAny(name, "\x00\r\n")
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
		(inst.Envfile != "" && !caddyPath(inst.Envfile)) || (inst.Unit != "" && !ValidService(inst.Unit)) ||
		(inst.Method == CaddyDocker && !caddyIdentifier.MatchString(inst.Container)) {
		return inst, ErrCaddyUnsafe
	}
	for _, name := range inst.EnvironmentFiles {
		if !caddyPath(strings.TrimPrefix(name, "-")) {
			return inst, ErrCaddyUnsafe
		}
	}
	return inst, nil
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
	if inst.Method == CaddyDocker {
		return command
	}
	files := inst.EnvironmentFiles
	if len(files) == 0 && inst.Envfile != "" {
		files = []string{inst.Envfile}
	}
	if len(files) == 0 {
		return command
	}
	var script strings.Builder
	script.WriteString("(\n" + caddyShellEnvironment)
	for _, name := range files {
		if actual, optional := strings.CutPrefix(name, "-"); optional {
			fmt.Fprintf(&script, "if [ -e %s ]; then load_env %s || exit '1'; fi\n", privilegeQuote(actual), privilegeQuote(actual))
		} else {
			fmt.Fprintf(&script, "load_env %s || exit '1'\n", privilegeQuote(name))
		}
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
