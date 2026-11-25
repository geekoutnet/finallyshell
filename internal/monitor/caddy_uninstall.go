package monitor

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// CaddyUninstallOptions allows removing the standard configuration and data
// directories. The default retains them; a private recovery copy is kept
// even when they are explicitly removed.
type CaddyUninstallOptions struct{ RemoveData bool }

func caddyUninstallScript(inst CaddyInstall, options CaddyUninstallOptions) (string, error) {
	if inst.Method == CaddyNotInstalled {
		return "", ErrCaddyUnsafe
	}
	if options.RemoveData && (inst.Config != caddyDefaultConfig || inst.DataDir != "/var/lib/caddy/.local/share/caddy" || inst.Method == CaddyDocker) {
		return "", fmt.Errorf("%w: only standard native Caddy data can be removed", ErrCaddyUnsafe)
	}
	var script strings.Builder
	script.WriteString("set -eu\numask '077'\nmkdir -p -- '/var/backups'\nbackup=$(mktemp -d '/var/backups/finallyshell-caddy.XXXXXXXX')\nprintf '%s\\n' \"Uninstall backup: $backup\"\n")
	if inst.Method == CaddyDocker {
		name := privilegeQuote(inst.Container)
		fmt.Fprintf(&script, `id=$(docker 'inspect' '--type' 'container' '--format' '{{.Id}}' %s)
case "$id" in ''|*[!0-9a-f]*) exit '1' ;; esac
if [ -n "$(docker 'inspect' '--format' '{{with index .Config.Labels "com.docker.compose.project"}}{{.}}{{end}}' "$id")" ]; then exit '1'; fi
[ "$(docker 'inspect' '--format' '{{.HostConfig.AutoRemove}}' "$id")" = 'false' ]
docker 'inspect' "$id" > "$backup/container.json"
docker 'rm' '-f' "$id"
`, name)
		return script.String(), nil
	}
	if inst.Binary != "" && !caddyPath(inst.Binary) {
		return "", ErrCaddyUnsafe
	}
	fmt.Fprintf(&script, "config=%s\n", privilegeQuote(inst.Config))
	script.WriteString(`if [ -f "$config" ]; then cp -pL -- "$config" "$backup/Caddyfile"; fi
if [ -d '/etc/caddy' ]; then cp -a -- '/etc/caddy' "$backup/etc-caddy"; fi
`)
	if inst.Binary != "" {
		fmt.Fprintf(&script, "binary=%s\n[ -f \"$binary\" ]\ncp -pL -- \"$binary\" \"$backup/caddy\"\n", privilegeQuote(inst.Binary))
	}
	if caddyPath(inst.UnitFile) {
		fmt.Fprintf(&script, "if [ -f %s ]; then cp -pL -- %s \"$backup/service\"; fi\n", privilegeQuote(inst.UnitFile), privilegeQuote(inst.UnitFile))
	}
	if inst.ServiceManager == "openrc" {
		script.WriteString("if rc-service 'caddy' 'status' >/dev/null 2>&1; then rc-service 'caddy' 'stop'; fi\nrc-update 'del' 'caddy' 'default' || :\n")
	} else if inst.Unit != "" {
		script.WriteString(SystemCommand("systemctl", "disable", "--now", inst.Unit) + "\n")
	} else if inst.Active == "active" || len(inst.Listening) != 0 {
		return "", fmt.Errorf("%w: stop the unmanaged Caddy process before uninstalling", ErrCaddyUnsafe)
	}
	switch inst.Method {
	case CaddyAPT:
		script.WriteString("DEBIAN_FRONTEND=noninteractive apt-get 'remove' '-y' 'caddy'\n")
	case CaddyDNF:
		script.WriteString("dnf 'remove' '-y' '--noautoremove' 'caddy'\n")
	case CaddyPacman:
		script.WriteString("pacman '-R' '--noconfirm' 'caddy'\n")
	case CaddyAPK:
		script.WriteString("apk 'del' 'caddy'\n")
	case CaddyBinary:
		if !caddyPath(inst.Binary) || path.Base(inst.Binary) != "caddy" {
			return "", ErrCaddyUnsafe
		}
		script.WriteString("[ ! -L \"$binary\" ]\nrm -f -- \"$binary\"\n")
		if inst.UnitFile == "/etc/systemd/system/caddy.service" || inst.UnitFile == "/etc/systemd/system/caddy-api.service" {
			script.WriteString(SystemCommand("rm", "-f", "--", inst.UnitFile) + "\nsystemctl 'daemon-reload'\n")
		}
	default:
		return "", ErrCaddyUnsafe
	}
	if options.RemoveData {
		script.WriteString(`if [ -d '/etc/caddy' ]; then [ ! -L '/etc/caddy' ]; mv -- '/etc/caddy' "$backup/removed-config"; fi
if [ -d '/var/lib/caddy/.local/share/caddy' ]; then
  [ "$(cd -P '/var/lib/caddy/.local/share/caddy' && pwd -P)" = '/var/lib/caddy/.local/share/caddy' ]
  mv -- '/var/lib/caddy/.local/share/caddy' "$backup/removed-data"
fi
`)
	} else {
		script.WriteString(`if [ -d "$backup/etc-caddy" ]; then mkdir -p -- '/etc/caddy'; cp -an -- "$backup/etc-caddy/." '/etc/caddy/'; fi
if [ -f "$backup/Caddyfile" ] && [ ! -e "$config" ] && [ ! -L "$config" ]; then cp -p -- "$backup/Caddyfile" "$config"; fi
`)
	}
	return script.String(), nil
}

// UninstallCaddy removes the selected package, binary or container, leaving
// configuration and data intact unless explicitly requested for a standard
// native installation. It streams the backup location and command output.
func UninstallCaddy(ctx context.Context, run CaddyRunner, inst CaddyInstall, progress func(string), options ...CaddyUninstallOptions) error {
	inst, err := caddyNormalize(inst)
	if err != nil {
		return err
	}
	var selected CaddyUninstallOptions
	if len(options) > 0 {
		selected = options[0]
	}
	script, err := caddyUninstallScript(inst, selected)
	if err != nil {
		return err
	}
	stdout, stderr, err := run(ctx, script, nil, progress)
	return caddyLifecycleError(stdout, stderr, err)
}
