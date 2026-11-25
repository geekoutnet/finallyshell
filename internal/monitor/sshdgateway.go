package monitor

import (
	"errors"
	"strings"
)

type GatewayPorts struct {
	Mode  string
	Match bool
}

func ParseGatewayPorts(output string) (GatewayPorts, error) {
	var result GatewayPorts
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "gatewayports":
			if result.Mode != "" {
				return GatewayPorts{}, errGatewayPorts
			}
			result.Mode = strings.ToLower(fields[1])
		case "finally_match":
			result.Match = result.Match || fields[1] == "yes"
		}
	}
	switch result.Mode {
	case "no", "yes", "clientspecified":
		return result, nil
	}
	return GatewayPorts{}, errGatewayPorts
}

var errGatewayPorts = errors.New("unknown sshd GatewayPorts setting")

const gatewayInspectConfig = `inspect_config() {
  'awk' '-v' "allowed_pattern=$2" '
    /^[[:space:]]*#/ { next }
    tolower($0) ~ /^[[:space:]]*match([[:space:]=]|$)/ { blocked=1 }
    tolower($0) ~ /^[[:space:]]*include([[:space:]=]|$)/ && (tolower($1)!="include" || allowed_pattern=="" || $2!=allowed_pattern || (NF>2 && $3 !~ /^#/)) { blocked=1 }
    END { exit blocked }
  ' "$1"
}
standard_config() {
  [ -f "$config" ] && [ ! -L "$config" ] && [ ! -L "$directory" ] || return 1
  inspect_config "$config" '/etc/ssh/sshd_config.d/*.conf' || return 1
  for included in "$directory"/*.conf; do
    [ ! -L "$included" ] || return 1
    [ -e "$included" ] || continue
    [ -f "$included" ] && inspect_config "$included" '' || return 1
  done
}
`

const gatewayConfigPaths = `config='/etc/ssh/sshd_config'
directory='/etc/ssh/sshd_config.d'
sshd=$('command' '-v' 'sshd')
`

const GatewayPortsReadScript = `set -eu
'export' 'LC_ALL=C'
` + gatewayConfigPaths + gatewayInspectConfig + `"$sshd" '-T' '-f' "$config"
if ! standard_config; then 'printf' '%s\n' 'finally_match yes'; fi
`

const GatewayPortsChangeScript = `set -eu
umask '077'
'export' 'LC_ALL=C'
` + gatewayConfigPaths + gatewayInspectConfig + `dropin="$directory/00-finallyshell-gatewayports.conf"
lock='/etc/ssh/.finallyshell-sshd.lock'
'mkdir' "$lock"
target=''
backup=''
installed=''
reload_attempted=''
mode=''
service=''
reload_sshd() {
  case "$mode" in
    systemd) 'systemctl' 'reload' "$service" ;;
    service) 'service' "$service" 'reload' ;;
    *) return 1 ;;
  esac
}
rollback() {
  if [ -n "$installed" ]; then
    if [ -n "$backup" ]; then
      'cp' '-p' "$backup" "$lock/restore" && 'mv' '-f' "$lock/restore" "$target" || 'printf' '%s\n' 'Rollback failed; restore the backup manually' >&2
    else
      'rm' '-f' "$target" || 'printf' '%s\n' 'Rollback failed; remove the new drop-in manually' >&2
    fi
    if 'command' '-v' 'restorecon' >/dev/null 2>&1 && [ -e "$target" ]; then 'restorecon' "$target" || :; fi
    if [ -n "$reload_attempted" ]; then
      "$sshd" '-t' '-f' "$config" && reload_sshd || 'printf' '%s\n' 'Could not reload the restored sshd configuration' >&2
    fi
    if [ -n "$backup" ]; then 'printf' '%s\n' "$backup" >&2; fi
  fi
  'rm' '-f' "$lock/new" "$lock/restore"
  'rmdir' "$lock"
}
trap 'rollback' EXIT
trap 'exit 1' HUP INT TERM
standard_config || { 'printf' '%s\n' 'Custom Match or Include rules require manual review' >&2; exit 1; }
"$sshd" '-t' '-f' "$config"
effective=$("$sshd" '-T' '-f' "$config")
'printf' '%s\n' "$effective" | 'grep' '-qxF' 'gatewayports no'
for candidate in 'sshd' 'ssh'; do
  if 'command' '-v' 'systemctl' >/dev/null 2>&1 && 'systemctl' 'is-active' '--quiet' "$candidate"; then
    mode='systemd'; service="$candidate"; break
  fi
done
if [ -z "$mode" ]; then
  for candidate in 'sshd' 'ssh'; do
    if 'command' '-v' 'service' >/dev/null 2>&1 && 'service' "$candidate" 'status' >/dev/null 2>&1; then
      mode='service'; service="$candidate"; break
    fi
  done
fi
[ -n "$mode" ] || { 'printf' '%s\n' 'No active sshd service found' >&2; exit 1; }
target="$config"
if [ -d "$directory" ] && 'awk' '
  tolower($0) ~ /^[[:space:]]*gatewayports([[:space:]=]|$)/ { exit }
  tolower($1)=="include" && $2=="/etc/ssh/sshd_config.d/*.conf" { found=1; exit }
  END { exit !found }
' "$config"; then
  target="$dropin"
  for included in "$directory"/*.conf; do
    [ -f "$included" ] || continue
    [ "$included" != "$target" ] || break
    if [ "$( 'printf' '%s\n' "$included" "$target" | 'sort' | 'head' '-n' '1' )" = "$target" ]; then break; fi
    if 'grep' '-qiE' '^[[:space:]]*gatewayports([[:space:]=]|$)' "$included"; then target="$config"; break; fi
  done
fi
if [ -e "$target" ]; then
  [ -f "$target" ] && [ ! -L "$target" ] || exit 1
  stamp=$('date' '+%Y%m%d%H%M%S')
  name="$target.bak-$stamp"
  'ln' "$target" "$name"
  backup="$name"
  'cp' '-p' "$target" "$lock/new"
fi
{
  'printf' '%s\n' 'GatewayPorts clientspecified'
  if [ -n "$backup" ]; then 'cat' "$backup"; fi
} > "$lock/new"
installed='yes'
'mv' '-f' "$lock/new" "$target"
if 'command' '-v' 'restorecon' >/dev/null 2>&1; then 'restorecon' "$target"; fi
"$sshd" '-t' '-f' "$config"
effective=$("$sshd" '-T' '-f' "$config")
'printf' '%s\n' "$effective" | 'grep' '-qxF' 'gatewayports clientspecified'
reload_attempted='yes'
reload_sshd
installed=''
'printf' '%s\n' "$target"
if [ -n "$backup" ]; then 'printf' '%s\n' "$backup"; fi
`
