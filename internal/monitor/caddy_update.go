package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// CaddyUpdateOptions optionally selects an exact release or Docker tag.
type CaddyUpdateOptions struct{ Version, DockerTag string }

func CaddyCanUpdate(inst CaddyInstall) bool {
	if inst.Method == CaddyDocker {
		return caddyIdentifier.MatchString(inst.Container) && !inst.Resume && !inst.ContainerCompose && !inst.ContainerAutoRemove && inst.Active != "paused" && inst.Active != "restarting"
	}
	if inst.Resume || inst.Method == CaddyNotInstalled || !caddyPath(inst.Binary) || !caddyPath(inst.Config) {
		return false
	}
	if inst.Method == CaddyBinary && len(inst.Modules) > 0 && path.Base(inst.Binary) != "caddy" {
		return false
	}
	return inst.Active != "active" && len(inst.Listening) == 0 || inst.ServiceManager == "systemd" || inst.ServiceManager == "openrc"
}

const caddyConfigSnapshot = `snapshot_config() {
  config_backup=$(mktemp -d "$1/.finallyshell-caddy-backup.XXXXXXXX") || return '1'
  if [ -f "$2" ] && [ ! -L "$2" ]; then cp -p -- "$2" "$config_backup/Caddyfile" || return '1'; fi
  if [ -d '/etc/caddy' ] && [ ! -L '/etc/caddy' ]; then
    mkdir -- "$config_backup/tree" || return '1'
    for entry in /etc/caddy/*; do
      [ -e "$entry" ] || [ -L "$entry" ] || continue
      cp -a -- "$entry" "$config_backup/tree/" || return '1'
    done
  fi
  printf '%s\n' "Configuration backup: $config_backup"
}
restore_config() {
  if [ -d "$config_backup/tree" ]; then
    [ ! -L '/etc/caddy' ] && mkdir -p -- '/etc/caddy' && cp -a -- "$config_backup/tree/." '/etc/caddy/' || return '1'
  fi
  if [ -f "$config_backup/Caddyfile" ]; then
    restored=$(mktemp "${1%/*}/.finallyshell.XXXXXXXX") || return '1'
    cp -p -- "$config_backup/Caddyfile" "$restored" && mv -f -- "$restored" "$1" && return '0'
    rm -f -- "$restored"; return '1'
  fi
}
`

func caddyRestoreService(inst CaddyInstall) (string, error) {
	if inst.Active == "active" {
		if action, err := caddyServiceScript(inst, CaddyRestart); err == nil {
			return action, nil
		}
		return caddyReloadCommand(inst, inst.Config)
	}
	return ":", nil
}

func caddyBinaryUpdateScript(inst CaddyInstall, tag string) (string, error) {
	if !caddyPath(inst.Binary) || inst.Resume || (len(inst.Modules) != 0 && path.Base(inst.Binary) != "caddy") {
		return "", ErrCaddyUnsafe
	}
	if len(inst.Modules) == 0 && !caddyVersionTag.MatchString(tag) {
		return "", ErrCaddyUnsafe
	}
	restart, err := caddyRestoreService(inst)
	if err != nil {
		return "", err
	}
	var script strings.Builder
	script.WriteString(caddyInstallShell + caddyConfigSnapshot)
	if len(inst.Modules) == 0 {
		script.WriteString(caddyDownload)
		fmt.Fprintf(&script, "fetch_binary %s\n", privilegeQuote(tag))
	}
	fmt.Fprintf(&script, "binary=%s\nconfig=%s\n", privilegeQuote(inst.Binary), privilegeQuote(inst.Config))
	script.WriteString(`[ -f "$binary" ] && [ ! -L "$binary" ]
[ -f "$config" ] && [ ! -L "$config" ]
snapshot_config "${config%/*}" "$config"
backup=$(mktemp "$binary.bak-$(date -u '+%Y%m%d%H%M%S').XXXXXXXX")
cp -p -- "$binary" "$backup"
printf '%s\n' "Binary backup: $backup"
restore_binary() {
  restored=$(mktemp "${binary%/*}/.finallyshell.XXXXXXXX") || return '1'
  cp -p -- "$backup" "$restored" && mv -f -- "$restored" "$binary" && return '0'
  rm -f -- "$restored"; return '1'
}
dirty='yes'
recovery() {
  status=$?; trap - '0'; trap '' HUP INT TERM; set +e
  if [ -n "$dirty" ]; then
    printf '%s\n' 'Restoring the previous Caddy binary and configuration' >&2
`)
	fmt.Fprintf(&script, "    if restore_binary && restore_config \"$config\" && { %s; }; then :; else printf '%%s\\n' 'CADDY_RECOVERY_FAILED' >&2; status='1'; fi\n", restart)
	script.WriteString("  fi\n  rm -rf -- \"$tmp\"\n  exit \"$status\"\n}\ntrap recovery '0'\n")
	if len(inst.Modules) == 0 {
		script.WriteString("place \"$tmp/caddy\" \"$binary\" '0755'\n")
	} else {
		script.WriteString(caddyCLI(inst, "upgrade", "--keep-backup") + "\n")
	}
	script.WriteString(caddyCLI(inst, "version") + "\n" + caddyValidateCommand(inst, inst.Config) + "\n" + restart + "\n")
	if inst.Active == "active" && inst.Unit != "" {
		script.WriteString(SystemCommand("systemctl", "is-active", "--quiet", inst.Unit) + "\n")
	}
	script.WriteString("dirty=''\n")
	return script.String(), nil
}

func caddyPackageUpdateScript(inst CaddyInstall) (string, error) {
	if inst.Resume {
		return "", ErrCaddyUnsafe
	}
	restart, err := caddyRestoreService(inst)
	if err != nil {
		return "", err
	}
	var stage, update, rollback string
	switch inst.Method {
	case CaddyAPT:
		stage = `export DEBIAN_FRONTEND=noninteractive
old=$(dpkg-query '-W' '-f=${Version}' 'caddy')
(cd "$tmp" && apt-get 'download' "caddy=$old")
set -- "$tmp"/caddy_*.deb; [ "$#" = '1' ] && [ -f "$1" ]
previous=$1
[ "$(dpkg-deb '-f' "$previous" 'Package')" = 'caddy' ]
[ "$(dpkg-deb '-f' "$previous" 'Version')" = "$old" ]
`
		update = "apt-get 'install' '-y' '--only-upgrade' 'caddy'"
		rollback = `apt-get 'install' '-y' '--allow-downgrades' "$previous"`
	case CaddyDNF:
		stage = `old=$(rpm '-q' '--qf' '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' 'caddy')
dnf 'download' '--destdir' "$tmp" "$old"
set -- "$tmp"/caddy-*.rpm; [ "$#" = '1' ] && [ -f "$1" ]
previous=$1
verify_rpm "$previous"
`
		update = "dnf 'upgrade' '-y' '--setopt=gpgcheck=1' 'caddy'"
		rollback = `dnf 'downgrade' '-y' '--setopt=localpkg_gpgcheck=1' "$previous"`
	case CaddyPacman:
		stage = `old=$(pacman '-Q' 'caddy'); old=${old#caddy }
for archive in /var/cache/pacman/pkg/caddy-"$old"-*.pkg.tar.*; do
  case "$archive" in *.sig) continue ;; esac
  if [ -f "$archive" ] && [ -f "$archive.sig" ]; then previous=$archive; break; fi
done
[ -n "${previous-}" ]
pacman-key '--verify' "$previous.sig" "$previous"
`
		update = "pacman '-S' '--noconfirm' 'caddy'"
		rollback = `pacman '-U' '--noconfirm' "$previous"`
	case CaddyAPK:
		stage = `old=$(apk 'info' '-v' 'caddy' | head -n '1'); old=${old#caddy-}
apk 'fetch' '-o' "$tmp" "caddy=$old"
set -- "$tmp"/caddy-*.apk; [ "$#" = '1' ] && [ -f "$1" ]
previous=$1
apk 'verify' "$previous"
`
		update = "apk 'upgrade' 'caddy'"
		rollback = `apk 'add' "$previous"`
	default:
		return "", ErrCaddyUnsafe
	}
	var script strings.Builder
	script.WriteString(caddyInstallShell + caddyConfigSnapshot)
	if inst.Method == CaddyDNF {
		script.WriteString(caddyPackageTrust)
	}
	fmt.Fprintf(&script, "config=%s\n", privilegeQuote(inst.Config))
	script.WriteString("[ -f \"$config\" ] && [ ! -L \"$config\" ]\n" + stage + "snapshot_config \"${config%/*}\" \"$config\"\n")
	script.WriteString(`dirty=''
recovery() {
  status=$?; trap - '0'; trap '' HUP INT TERM; set +e
  if [ -n "$dirty" ]; then
    printf '%s\n' 'Restoring the previous Caddy package and configuration' >&2
`)
	fmt.Fprintf(&script, "    if { %s; } && restore_config \"$config\" && { %s; }; then :; else\n      printf '%%s\\n' 'CADDY_RECOVERY_FAILED' \"Package recovery directory: $tmp\" >&2; keep='yes'; status='1'\n    fi\n", rollback, restart)
	script.WriteString("  fi\n  if [ -n \"${own_policy-}\" ]; then rm -f -- '/usr/sbin/policy-rc.d'; fi\n  if [ -z \"${keep-}\" ]; then rm -rf -- \"$tmp\"; fi\n  exit \"$status\"\n}\ntrap recovery '0'\n")
	if inst.Method == CaddyAPT {
		script.WriteString(caddyNoAPTStart)
	}
	script.WriteString("dirty='yes'\n" + update + "\n" + caddyCLI(inst, "version") + "\n" + caddyValidateCommand(inst, inst.Config) + "\n" + restart + "\ndirty=''\n")
	return script.String(), nil
}

func caddyDockerReplacement(ctx context.Context, run CaddyRunner, inst CaddyInstall, tag string, progress func(string)) error {
	stdout, stderr, err := run(ctx, SystemCommand("docker", "inspect", "--type", "container", "--format", "{{json .}}", inst.Container), nil, nil)
	if err != nil {
		return caddyCommandError(err, strings.TrimSpace(stderr))
	}
	var info struct {
		ID     string `json:"Id"`
		Name   string
		Config struct {
			Image  string
			Labels map[string]string
		}
		HostConfig struct{ AutoRemove bool }
		State      struct{ Paused, Restarting bool }
	}
	if len(stdout) > caddyFileLimit || json.Unmarshal([]byte(stdout), &info) != nil || len(info.ID) != 64 || !isCtrHex(info.ID) || info.Name != "/"+inst.Container || !caddyImage(info.Config.Image) || info.HostConfig.AutoRemove || info.State.Paused || info.State.Restarting || info.Config.Labels["com.docker.compose.project"] != "" {
		return ErrCaddyUnsafe
	}
	request, err := recreateRequestFor(json.RawMessage(stdout), false)
	if err != nil {
		return err
	}
	image := request.image
	if tag != "" {
		if !caddyIdentifier.MatchString(tag) {
			return ErrCaddyUnsafe
		}
		image = strings.SplitN(image, "@", 2)[0]
		if colon := strings.LastIndexByte(image, ':'); colon > strings.LastIndexByte(image, '/') {
			image = image[:colon]
		}
		image += ":" + tag
	}
	if !validCtrImage(image) {
		return ErrCaddyUnsafe
	}
	script, err := caddyDockerReplacementScript(inst, info.ID, image, request)
	if err != nil {
		return err
	}
	stdout, stderr, err = caddyRunPayload(ctx, run, script, progress)
	return caddyLifecycleError(stdout, stderr, err)
}

func caddyLifecycleError(stdout, stderr string, err error) error {
	if strings.Contains(stderr, "CADDY_RECOVERY_FAILED") {
		return caddyCommandError(ErrCaddyRollback, caddyOutput(stdout, stderr))
	}
	if err != nil {
		return caddyCommandError(err, caddyOutput(stdout, stderr))
	}
	return nil
}

func caddyUpdatedDetection(ctx context.Context, run CaddyRunner, before CaddyInstall) (CaddyInstall, error) {
	updated, err := DetectCaddy(ctx, run)
	if err != nil {
		return before, err
	}
	if updated.Method == CaddyNotInstalled {
		return before, fmt.Errorf("%w: Caddy was not found after the operation", ErrCaddyUnsafe)
	}
	return updated, nil
}

// UpdateCaddy streams the selected update and returns the updated detection
// data. Package updates require a verified copy of the installed package before
// replacing it. Static/custom builds keep their previous binary; Docker keeps
// the original container until the replacement is verified. Recovery failures
// are reported as ErrCaddyRollback.
func UpdateCaddy(ctx context.Context, run CaddyRunner, inst CaddyInstall, progress func(string), options ...CaddyUpdateOptions) (CaddyInstall, error) {
	inst, err := caddyNormalize(inst)
	if err != nil {
		return inst, err
	}
	var selected CaddyUpdateOptions
	if len(options) != 0 {
		selected = options[0]
	}
	if inst.Method == CaddyDocker {
		if err := caddyDockerReplacement(ctx, run, inst, selected.DockerTag, progress); err != nil {
			return inst, err
		}
		return caddyUpdatedDetection(ctx, run, inst)
	}
	var script string
	if inst.Method == CaddyBinary {
		if len(inst.Modules) == 0 && selected.Version == "" {
			selected.Version, err = LatestCaddyRelease(ctx, nil)
			if err != nil {
				return inst, err
			}
		}
		script, err = caddyBinaryUpdateScript(inst, selected.Version)
	} else {
		script, err = caddyPackageUpdateScript(inst)
	}
	if err != nil {
		return inst, err
	}
	stdout, stderr, err := run(ctx, script, nil, progress)
	if err := caddyLifecycleError(stdout, stderr, err); err != nil {
		return inst, err
	}
	return caddyUpdatedDetection(ctx, run, inst)
}
