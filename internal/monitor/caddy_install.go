package monitor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrCaddyAlreadyInstalled is returned before installation if detection finds Caddy.
	ErrCaddyAlreadyInstalled = errors.New("Caddy is already installed")
	// ErrCaddyPortConflict indicates a listener on HTTP or HTTPS ports.
	ErrCaddyPortConflict = errors.New("the HTTP or HTTPS port is already in use")
)

// CaddyInstallOptions pins the release or Docker tag and permits installing
// without starting or enabling Caddy, including on hosts with port conflicts.
type CaddyInstallOptions struct {
	Version, DockerTag string
	WithoutStart       bool
	WithoutEnable      bool
	Firewall           string
	ReplaceConflicting []string
}

const caddyDownload = `download() {
  if command -v 'curl' >/dev/null 2>&1; then
    curl -q --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout '15' --max-time '300' --output "$2" "$1"
  else
    wget '--https-only' '-T' '60' '-O' "$2" "$1"
  fi
}
fetch_binary() {
  case "$(uname -m)" in
    x86_64|amd64) arch='amd64' ;; aarch64|arm64) arch='arm64' ;;
    armv7*) arch='armv7' ;; armv6*) arch='armv6' ;; i386|i486|i586|i686) arch='386' ;;
    s390x|ppc64le|riscv64) arch=$(uname -m) ;; *) printf '%s\n' 'Unsupported Caddy architecture' >&2; return '1' ;;
  esac
  release="https://github.com/caddyserver/caddy/releases/download/$1"
  version=${1#v}
  archive="caddy_${version}_linux_${arch}.tar.gz"
  printf '%s\n' "Downloading $archive and its SHA-512 checksums"
  download "$release/$archive" "$tmp/$archive" || return '1'
  download "$release/caddy_${version}_checksums.txt" "$tmp/checksums" || return '1'
  digest=$(awk -v "name=$archive" '{file=$2; sub(/^\*/, "", file); if (file==name && length($1)==128 && $1 ~ /^[[:xdigit:]]+$/) {print tolower($1); hits++}} END {if(hits!=1) exit 1}' "$tmp/checksums") || return '1'
  printf '%s  %s\n' "$digest" "$tmp/$archive" | sha512sum -c - || return '1'
  tar -xOzf "$tmp/$archive" 'caddy' > "$tmp/caddy" || return '1'
  chmod '0755' "$tmp/caddy" || return '1'
  actual=$("$tmp/caddy" 'version') || return '1'
  [ "${actual%% *}" = "$1" ]
}
`

const caddyInstallShell = `set -eu
export LC_ALL=C
umask '022'
tmp=$(mktemp -d '/tmp/finallyshell-caddy.XXXXXXXX')
trap 'rm -rf -- "$tmp"' '0'
trap 'exit 1' HUP INT TERM
place() {
  target=$(mktemp "${2%/*}/.finallyshell.XXXXXXXX") || return '1'
  install -m "$3" "$1" "$target" && mv -f -- "$target" "$2" && return '0'
  rm -f -- "$target"
  return '1'
}
`

const caddyPackageTrust = `verify_rpm() {
  report=$(rpm '--checksig' '--verbose' "$1") || return '1'
  printf '%s\n' "$report" | grep -Eq 'Signature[^:]*: OK$'
}
`

const caddyDefaultContent = `:80 {
	root * /usr/share/caddy
	file_server
}
`

const caddyWelcome = "<!doctype html><meta charset=utf-8><title>Caddy</title><h1>Caddy is running</h1>\n"

const caddyServiceCommon = `[Unit]
Description=Caddy
Documentation=https://caddyserver.com/docs/
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
LimitNOFILE=1048576
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE
`

const caddySystemdInstall = `[Install]
WantedBy=multi-user.target
`

func caddySeedFiles(script *strings.Builder) {
	script.WriteString(`mkdir -p -- '/etc/caddy' '/usr/share/caddy'
for dir in '/etc/caddy' '/usr/share/caddy'; do
  [ "$(cd -P -- "$dir" && pwd -P)" = "$dir" ] || exit '1'
done
for file in '/etc/caddy/Caddyfile' '/usr/share/caddy/index.html'; do
  [ ! -L "$file" ] || exit '1'
done
if [ ! -e '/etc/caddy/Caddyfile' ]; then
`)
	caddyPayload(script, `base64 -d > "$tmp/Caddyfile"`, caddyDefaultContent)
	script.WriteString("place \"$tmp/Caddyfile\" '/etc/caddy/Caddyfile' '0644'\nfi\nif [ ! -e '/usr/share/caddy/index.html' ]; then\n")
	caddyPayload(script, `base64 -d > "$tmp/index.html"`, caddyWelcome)
	script.WriteString("place \"$tmp/index.html\" '/usr/share/caddy/index.html' '0644'\nfi\n")
}

func caddyPackageMethod(os string) string {
	switch os {
	case "debian", "ubuntu", "raspbian", "linuxmint":
		return CaddyAPT
	case "fedora", "rhel", "centos", "rocky", "almalinux":
		return CaddyDNF
	case "arch", "manjaro", "endeavouros":
		return CaddyPacman
	case "alpine":
		return CaddyAPK
	default:
		return CaddyBinary
	}
}

const caddyAPTSetup = `export DEBIAN_FRONTEND=noninteractive
apt-get 'install' '-y' 'debian-keyring' 'debian-archive-keyring' 'apt-transport-https' 'curl' 'gnupg'
download 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' "$tmp/caddy.asc"
mkdir -m '0700' "$tmp/gnupg"
gpg '--homedir' "$tmp/gnupg" '--batch' '--yes' '--dearmor' '--output' "$tmp/caddy.gpg" "$tmp/caddy.asc"
fingerprint=$(gpg '--homedir' "$tmp/gnupg" '--batch' '--with-colons' '--show-keys' "$tmp/caddy.gpg" | awk -F ':' '$1=="pub" {primary=1; keys++} $1=="fpr" && primary {print $10; primary=0} END {if(keys!=1) exit 1}')
[ "$fingerprint" = '65760C51EDEA2017CEA2CA15155B6D79CA56EA34' ]
key='/usr/share/keyrings/caddy-stable-archive-keyring.gpg'
repo='/etc/apt/sources.list.d/caddy-stable.list'
[ ! -L "$key" ] && [ ! -L "$repo" ]
if [ -e "$key" ]; then cp -p -- "$key" "$tmp/key.old"; fi
if [ -e "$repo" ]; then cp -p -- "$repo" "$tmp/repo.old"; fi
apt_recovery() {
  status=${1:-1}
  trap - '0'; trap '' HUP INT TERM; set +e
  if [ "$status" -ne '0' ]; then
    if [ -f "$tmp/key.old" ]; then cp -p -- "$tmp/key.old" "$key" || failed='yes'; else rm -f -- "$key" || failed='yes'; fi
    if [ -f "$tmp/repo.old" ]; then cp -p -- "$tmp/repo.old" "$repo" || failed='yes'; else rm -f -- "$repo" || failed='yes'; fi
  fi
  if [ -n "${own_policy-}" ]; then rm -f -- '/usr/sbin/policy-rc.d' || failed='yes'; fi
  if [ -z "${failed-}" ]; then rm -rf -- "$tmp"; else printf '%s\n' 'CADDY_RECOVERY_FAILED' "Recovery directory: $tmp" >&2; status='1'; fi
  exit "$status"
}
trap 'apt_recovery "$?"' '0'
printf '%s\n' 'deb [signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main' > "$tmp/caddy.list"
place "$tmp/caddy.gpg" "$key" '0644'
place "$tmp/caddy.list" "$repo" '0644'
apt-get 'update'
`

const caddyPackageInstallRollback = `package_touched=''
recover_package_install() {
  status=$?
  trap '' HUP INT TERM
  set +e
  if [ "$status" -ne '0' ] && [ -n "$package_touched" ]; then
    failed=''
    if [ -f '/etc/init.d/caddy' ] && command -v 'rc-service' >/dev/null 2>&1; then rc-service 'caddy' 'stop' || :; rc-update 'del' 'caddy' 'default' || :;
    elif command -v 'systemctl' >/dev/null 2>&1 && [ -d '/run/systemd/system' ]; then systemctl 'disable' '--now' 'caddy.service' || :; fi
    remove_package || failed='yes'
    if [ -d "$tmp/config.old" ]; then
      mkdir -p -- '/etc/caddy' && cp -a -- "$tmp/config.old/." '/etc/caddy/' || failed='yes'
    fi
    if [ -n "$failed" ]; then printf '%s\n' 'CADDY_RECOVERY_FAILED' "Recovery directory: $tmp" >&2; fi
  fi
  return "$status"
}
if [ -d '/etc/caddy' ]; then
  [ ! -L '/etc/caddy' ]
  cp -a -- '/etc/caddy' "$tmp/config.old"
fi
`

const caddyNoAPTStart = `if [ ! -e '/usr/sbin/policy-rc.d' ] && [ ! -L '/usr/sbin/policy-rc.d' ]; then
  (set -C; printf '%s\n' '#!/bin/sh' 'case "$1" in caddy|caddy.service|caddy-api|caddy-api.service) exit 101;; esac' 'exit 0' > '/usr/sbin/policy-rc.d')
  own_policy='yes'
  chmod '0755' '/usr/sbin/policy-rc.d'
else
  [ -x '/usr/sbin/policy-rc.d' ]
  result='0'; '/usr/sbin/policy-rc.d' 'caddy' 'start' >/dev/null 2>&1 || result=$?
  [ "$result" = '101' ]
  result='0'; '/usr/sbin/policy-rc.d' 'caddy.service' 'start' >/dev/null 2>&1 || result=$?
  [ "$result" = '101' ]
fi
`

func caddyPackageInstallCommands(os, method string) (string, string, error) {
	switch method {
	case CaddyAPT:
		return caddyAPTSetup, SystemCommand("apt-get", "install", "-y", "caddy"), nil
	case CaddyDNF:
		plugin := "dnf-plugins-core"
		if os == "fedora" {
			plugin = "dnf5-plugins"
		}
		return SystemCommand("dnf", "install", "-y", "--setopt=gpgcheck=1", plugin) + "\n" + SystemCommand("dnf", "copr", "enable", "-y", "@caddy/caddy") + "\n", SystemCommand("dnf", "install", "-y", "--setopt=gpgcheck=1", "caddy"), nil
	case CaddyPacman:
		return "", SystemCommand("pacman", "-Syu", "--noconfirm", "caddy"), nil
	case CaddyAPK:
		return "", SystemCommand("apk", "add", "caddy"), nil
	default:
		return "", "", ErrCaddyUnsafe
	}
}

func caddyEnsureInstallAbsent(allowStopped bool) string {
	script := `for binary in "$(command -v 'caddy' 2>/dev/null || :)" '/usr/bin/caddy' '/usr/local/bin/caddy' '/opt/caddy/caddy'; do
  if [ -n "$binary" ] && { [ -e "$binary" ] || [ -L "$binary" ]; }; then printf '%s\n' 'CADDY_EXISTING'; exit '1'; fi
done
if command -v 'docker' >/dev/null 2>&1; then
  containers=$(docker 'ps' '-a' '--format' '{{.Image}}' 2>/dev/null || :)
  printf '%s\n' "$containers" | awk -F '/' '$NF ~ /^caddy/ {found=1} END {exit !found}' && { printf '%s\n' 'CADDY_EXISTING'; exit '1'; }
fi
`
	if !allowStopped {
		script += `if ! command -v 'ss' >/dev/null 2>&1; then printf '%s\n' 'Cannot verify port availability without ss' >&2; exit '1'; fi
listeners=$(ss '-H' '-ltn' 'sport = :80 or sport = :443')
quic=$(ss '-H' '-lun' 'sport = :443')
if [ -n "$listeners$quic" ]; then printf '%s\n' 'CADDY_PORT_CONFLICT'; exit '1'; fi
`
	}
	return script
}

func caddyInstallScript(osinfo CaddyInstall, method string, options CaddyInstallOptions) (string, error) {
	var script strings.Builder
	script.WriteString(caddyInstallShell + caddyEnsureInstallAbsent(options.WithoutStart && options.WithoutEnable))
	switch method {
	case CaddyBinary:
		if !caddyVersionTag.MatchString(options.Version) {
			return "", ErrCaddyUnsafe
		}
		script.WriteString("[ -d '/run/systemd/system' ]\n" + caddyDownload)
		fmt.Fprintf(&script, "fetch_binary %s\n", privilegeQuote(options.Version))
		script.WriteString(`command -v 'systemctl' >/dev/null
for file in '/etc/systemd/system/caddy.service' '/etc/systemd/system/caddy-api.service'; do
  [ ! -e "$file" ] && [ ! -L "$file" ] || exit '1'
done
if ! getent 'group' 'caddy' >/dev/null; then groupadd '--system' 'caddy'; fi
if ! id 'caddy' >/dev/null 2>&1; then useradd '--system' '--gid' 'caddy' '--create-home' '--home-dir' '/var/lib/caddy' '--shell' '/usr/sbin/nologin' 'caddy'; fi
`)
		caddySeedFiles(&script)
		script.WriteString(caddyValidationEnvironment(CaddyInstall{}, `"$tmp/caddy" 'validate' '--config' '/etc/caddy/Caddyfile' '--adapter' 'caddyfile'`) + `
installed=''
undo_install() {
  status=$?; trap - '0'; set +e
  if [ -n "$installed" ]; then
    failed=''
    if [ -e '/etc/systemd/system/caddy.service' ]; then systemctl 'disable' '--now' 'caddy.service' || failed='yes'; fi
    rm -f -- '/usr/bin/caddy' '/etc/systemd/system/caddy.service' '/etc/systemd/system/caddy-api.service' || failed='yes'
    systemctl 'daemon-reload' || failed='yes'
    if [ -n "$failed" ]; then printf '%s\n' 'CADDY_RECOVERY_FAILED' >&2; status='1'; fi
  fi
  rm -rf -- "$tmp"
  exit "$status"
}
trap undo_install '0'
installed='yes'
place "$tmp/caddy" '/usr/bin/caddy' '0755'
`)
		caddyPayload(&script, `base64 -d > "$tmp/caddy.service"`, caddyServiceCommon+"ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile\nExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force\n\n"+caddySystemdInstall)
		caddyPayload(&script, `base64 -d > "$tmp/caddy-api.service"`, caddyServiceCommon+"ExecStart=/usr/bin/caddy run --environ --resume\n\n"+caddySystemdInstall)
		script.WriteString(`place "$tmp/caddy.service" '/etc/systemd/system/caddy.service' '0644'
place "$tmp/caddy-api.service" '/etc/systemd/system/caddy-api.service' '0644'
systemctl 'daemon-reload'
`)
		if !options.WithoutStart {
			if options.WithoutEnable {
				script.WriteString("systemctl 'start' 'caddy.service'\n")
			} else {
				script.WriteString("systemctl 'enable' '--now' 'caddy.service'\n")
			}
			script.WriteString("systemctl 'is-active' '--quiet' 'caddy.service'\n")
		} else if !options.WithoutEnable {
			script.WriteString("systemctl 'enable' 'caddy.service'\n")
		}
		script.WriteString("installed=''\n")
	case CaddyDocker:
		if options.WithoutStart && !options.WithoutEnable {
			return "", ErrCaddyUnsafe
		}
		tag := options.DockerTag
		if tag == "" {
			tag = "latest"
		}
		if !caddyIdentifier.MatchString(tag) {
			return "", ErrCaddyUnsafe
		}
		caddySeedFiles(&script)
		image := "docker.io/library/caddy:" + tag
		script.WriteString(SystemCommand("docker", "pull", image) + "\n")
		script.WriteString("image=$(" + SystemCommand("docker", "image", "inspect", "--format", "{{.Id}}", image) + ")\n")
		verb, restart := "run", "unless-stopped"
		if options.WithoutStart {
			verb, restart = "create", "no"
		} else if options.WithoutEnable {
			restart = "no"
		}
		args := []string{"docker", verb}
		if !options.WithoutStart {
			args = append(args, "-d")
		}
		args = append(args, "--name", "caddy", "--restart", restart, "-p", "80:80", "-p", "443:443", "-p", "443:443/udp", "-v", "/etc/caddy:/etc/caddy", "-v", "/usr/share/caddy:/usr/share/caddy:ro", "-v", "caddy_data:/data", "-v", "caddy_config:/config")
		script.WriteString("if docker 'inspect' '--type' 'container' 'caddy' >/dev/null 2>&1; then printf '%s\\n' 'CADDY_EXISTING'; exit '1'; fi\n")
		script.WriteString(SystemCommand("docker", "run", "--rm", "--network", "none", "--entrypoint", "caddy", "-v", "/etc/caddy:/etc/caddy:ro") + ` "$image" 'validate' '--config' '/etc/caddy/Caddyfile'` + "\n")
		script.WriteString(`managed=''
recover_container_install() {
  status=$?; trap - '0'; trap '' HUP INT TERM
  if [ "$status" -ne '0' ] && [ -n "$managed" ]; then
    docker 'rm' '-f' "$managed" >/dev/null || printf '%s\n' 'CADDY_RECOVERY_FAILED' >&2
  fi
  rm -rf -- "$tmp"
  exit "$status"
}
trap recover_container_install '0'
managed=$(` + SystemCommand(append(args, image)...) + `)
[ "$(docker 'inspect' '--format' '{{.Image}}' "$managed")" = "$image" ]
`)
		if !options.WithoutStart {
			script.WriteString("[ \"$(docker 'inspect' '--format' '{{.State.Running}}' \"$managed\")\" = 'true' ]\n")
		}
	case CaddyAPT, CaddyDNF, CaddyPacman, CaddyAPK:
		setup, install, err := caddyPackageInstallCommands(osinfo.OS, method)
		if err != nil {
			return "", err
		}
		if method == CaddyAPK {
			script.WriteString("command -v 'rc-service' >/dev/null\ncommand -v 'rc-update' >/dev/null\n")
		} else {
			script.WriteString("[ -d '/run/systemd/system' ]\n")
		}
		if method == CaddyAPT {
			script.WriteString(caddyDownload)
		}
		script.WriteString(setup)
		if method == CaddyAPT {
			script.WriteString(caddyNoAPTStart)
		}
		script.WriteString(caddyPackageInstallRollback)
		var installed, remove string
		switch method {
		case CaddyAPT:
			installed, remove = "dpkg-query '-W' '-f=${db:Status-Status}' 'caddy' | grep -Eq '^(installed|unpacked|half-installed|half-configured|triggers-awaited|triggers-pending)$'", "apt-get 'remove' '-y' 'caddy'"
		case CaddyDNF:
			installed, remove = "rpm '-q' 'caddy'", "dnf 'remove' '-y' '--noautoremove' 'caddy'"
		case CaddyPacman:
			installed, remove = "pacman '-Q' 'caddy'", "pacman '-R' '--noconfirm' 'caddy'"
		case CaddyAPK:
			installed, remove = "apk 'info' '-e' 'caddy'", "apk 'del' 'caddy'"
		}
		fmt.Fprintf(&script, "remove_package() { if %s >/dev/null 2>&1; then %s; fi; }\n", installed, remove)
		fmt.Fprintf(&script, "if %s >/dev/null 2>&1; then printf '%%s\\n' 'CADDY_EXISTING'; exit '1'; fi\n", installed)
		if method == CaddyAPT {
			script.WriteString("trap 'recover_package_install; apt_recovery \"$?\"' '0'\n")
		} else {
			script.WriteString("trap 'recover_package_install; status=$?; if [ \"$status\" -eq 0 ] || [ -z \"${failed-}\" ]; then rm -rf -- \"$tmp\"; fi; exit \"$status\"' '0'\n")
		}
		script.WriteString("package_touched='yes'\n" + install + "\n")
		script.WriteString(caddyValidationEnvironment(CaddyInstall{}, SystemCommand("caddy", "validate", "--config", caddyDefaultConfig, "--adapter", "caddyfile")) + "\n")
		if method == CaddyAPK {
			if !options.WithoutEnable {
				script.WriteString("rc-update 'add' 'caddy' 'default'\n")
			}
			if !options.WithoutStart {
				script.WriteString("rc-service 'caddy' 'start'\n")
			}
		} else if options.WithoutStart {
			script.WriteString("systemctl 'disable' '--now' 'caddy.service'\n")
			if !options.WithoutEnable {
				script.WriteString("systemctl 'enable' 'caddy.service'\n")
			}
		} else if options.WithoutEnable {
			script.WriteString("systemctl 'disable' 'caddy.service'\nsystemctl 'start' 'caddy.service'\nsystemctl 'is-active' '--quiet' 'caddy.service'\n")
		} else {
			script.WriteString("systemctl 'enable' '--now' 'caddy.service'\nsystemctl 'is-active' '--quiet' 'caddy.service'\n")
		}
	default:
		return "", ErrCaddyUnsafe
	}
	return script.String(), nil
}

// InstallCaddy detects existing installations and port conflicts, then streams
// the selected package, verified binary, or Docker installation script.
// An empty method selects the OS package; for static installs an empty version
// is resolved through LatestCaddyRelease on the local computer. It returns a
// fresh detection result, ErrCaddyAlreadyInstalled or ErrCaddyPortConflict.
func InstallCaddy(ctx context.Context, run CaddyRunner, osinfo CaddyInstall, method string, progress func(string), options ...CaddyInstallOptions) (CaddyInstall, error) {
	current, err := DetectCaddy(ctx, run)
	if err != nil {
		return current, err
	}
	if current.Method != CaddyNotInstalled {
		return current, ErrCaddyAlreadyInstalled
	}
	var selected CaddyInstallOptions
	if len(options) > 0 {
		selected = options[0]
	}
	if len(selected.ReplaceConflicting) != 0 && (!slices.Equal(current.Conflicts, selected.ReplaceConflicting) || selected.WithoutStart) {
		return current, ErrCaddyChanged
	}
	if len(current.Conflicts) > 0 && (!selected.WithoutStart || !selected.WithoutEnable) && len(selected.ReplaceConflicting) == 0 {
		return current, fmt.Errorf("%w: %s", ErrCaddyPortConflict, strings.Join(current.Conflicts, ", "))
	}
	if current.OS != "" {
		osinfo.OS = current.OS
	}
	if method == "" || method == "auto" {
		method = caddyPackageMethod(osinfo.OS)
	}
	if method != CaddyBinary && method != CaddyDocker && method != caddyPackageMethod(osinfo.OS) {
		return current, fmt.Errorf("%w: package method does not match the detected distribution", ErrCaddyUnsafe)
	}
	if method == CaddyBinary && selected.Version == "" {
		selected.Version, err = LatestCaddyRelease(ctx, nil)
		if err != nil {
			return current, err
		}
	}
	script, err := caddyInstallScript(osinfo, method, selected)
	if err != nil {
		return current, err
	}
	if selected.Firewall != "" || len(selected.ReplaceConflicting) != 0 {
		script, err = caddyInstallWithHostChanges(script, selected)
		if err != nil {
			return current, err
		}
	}
	stdout, stderr, err := run(ctx, script, nil, progress)
	if err != nil {
		if strings.Contains(stderr, "CADDY_RECOVERY_FAILED") {
			return current, caddyLifecycleError(stdout, stderr, err)
		}
		if strings.Contains(stdout, "CADDY_EXISTING") || strings.Contains(stderr, "CADDY_EXISTING") {
			return current, ErrCaddyAlreadyInstalled
		}
		if strings.Contains(stdout, "CADDY_PORT_CONFLICT") || strings.Contains(stderr, "CADDY_PORT_CONFLICT") {
			return current, ErrCaddyPortConflict
		}
		return current, caddyLifecycleError(stdout, stderr, err)
	}
	installed, err := DetectCaddy(ctx, run)
	if err == nil && installed.Method == CaddyNotInstalled {
		return installed, errors.New("Caddy installation completed but detection did not find it")
	}
	return installed, err
}
