package monitor

import (
	"fmt"
	"strings"
)

func caddyConflictUnit(process string) string {
	switch process {
	case "nginx":
		return "nginx.service"
	case "apache", "apache2":
		return "apache2.service"
	case "httpd":
		return "httpd.service"
	default:
		return ""
	}
}

func caddyInstallWithHostChanges(install string, options CaddyInstallOptions) (string, error) {
	var prepare, mutate, restore strings.Builder
	seen := map[string]bool{}
	for _, process := range options.ReplaceConflicting {
		unit := caddyConflictUnit(process)
		if unit == "" {
			return "", ErrCaddyUnsafe
		}
		if seen[unit] {
			continue
		}
		seen[unit] = true
		quoted := privilegeQuote(unit)
		name := fmt.Sprintf("service%d", len(seen))
		fmt.Fprintf(&prepare, "%s=$(read_boot_state %s)\n%s\n", name, quoted, SystemCommand("systemctl", "is-active", "--quiet", unit))
		fmt.Fprintf(&mutate, "%s_changed='yes'\n%s\n", name, SystemCommand("systemctl", "disable", "--now", unit))
		fmt.Fprintf(&restore, `  if [ -n "${%[1]s_changed-}" ]; then
    restore_boot_state %[2]s "$%[1]s" || failed='yes'
    systemctl 'start' %[2]s || failed='yes'
    systemctl 'is-active' '--quiet' %[2]s || failed='yes'
  fi
`, name, quoted)
	}
	switch options.Firewall {
	case "":
	case "ufw":
		prepare.WriteString(caddyUFWPrepare)
		mutate.WriteString("ufw_changed='yes'\n")
		for _, port := range []string{"80/tcp", "443/tcp", "443/udp"} {
			mutate.WriteString(SystemCommand("ufw", "allow", port, "comment", "Finallyshell Caddy") + "\n")
		}
		restore.WriteString(caddyUFWRestore)
	case "firewalld":
		prepare.WriteString(caddyFirewalldPrepare)
		mutate.WriteString(`for port in '80/tcp' '443/tcp' '443/udp'; do
  add_port 'runtime' "$port"
  add_port 'permanent' "$port" '--permanent'
done
`)
		restore.WriteString(`  if [ -f "$host_backup/firewalld" ]; then
    while read -r scope port; do
      if [ "$scope" = 'permanent' ]; then set -- '--permanent'; else set --; fi
      firewalld_port "$@" "--remove-port=$port" || failed='yes'
    done < "$host_backup/firewalld"
  fi
`)
	default:
		return "", ErrCaddyUnsafe
	}
	return `set -eu
export LC_ALL=C
umask '077'
mkdir -p -- '/var/backups'
host_backup=$(mktemp -d '/var/backups/finallyshell-caddy-host.XXXXXXXX')
` + caddyBootStateShell + `
recover_host() {
  status=$?; trap - '0'; trap '' HUP INT TERM; set +e
  failed=''
  if [ "$status" -ne '0' ]; then
` + restore.String() + `  fi
  if [ -n "$failed" ]; then
    printf '%s\n' 'CADDY_RECOVERY_FAILED' "Host recovery directory: $host_backup" >&2
    exit '1'
  fi
  rm -rf -- "$host_backup"
  exit "$status"
}
trap recover_host '0'
trap 'exit 1' HUP INT TERM
` + prepare.String() + mutate.String() + "(\n" + install + ")\n", nil
}

const caddyUFWPrepare = `ufw 'status' | grep -q '^Status: active'
for file in '/etc/ufw/user.rules' '/etc/ufw/user6.rules' '/lib/ufw/user.rules' '/lib/ufw/user6.rules' '/usr/lib/ufw/user.rules' '/usr/lib/ufw/user6.rules'; do
  [ ! -L "$file" ] || exit '1'
  [ -e "$file" ] || continue
  if [ "$(readlink '-f' -- "$file")" != "$file" ]; then continue; fi
  [ -f "$file" ]
  mkdir -p -- "$host_backup${file%/*}"
  cp -p -- "$file" "$host_backup$file"
  printf '%s\n' "$file" >> "$host_backup/ufw"
done
[ -s "$host_backup/ufw" ]
`

const caddyUFWRestore = `  if [ -n "${ufw_changed-}" ]; then
    while IFS= read -r file; do
      staged=$(mktemp "${file%/*}/.finallyshell.XXXXXXXX") || { failed='yes'; continue; }
      if ! { cp -p -- "$host_backup$file" "$staged" && mv -f -- "$staged" "$file"; }; then rm -f -- "$staged"; failed='yes'; fi
    done < "$host_backup/ufw"
    ufw 'reload' || failed='yes'
  fi
`

const caddyFirewalldPrepare = `firewall-cmd '--state' | grep -qx 'running'
zone=$(firewall-cmd '--get-default-zone')
case "$zone" in ''|*[!A-Za-z0-9_.-]*) exit '1' ;; esac
firewalld_port() { firewall-cmd "--zone=$zone" "$@"; }
add_port() {
  scope=$1; port=$2; shift '2'
  status='0'
  firewalld_port "$@" "--query-port=$port" >/dev/null || status=$?
  case "$status" in
    0) return '0' ;;
    1) printf '%s %s\n' "$scope" "$port" >> "$host_backup/firewalld" ;;
    *) return "$status" ;;
  esac
  firewalld_port "$@" "--add-port=$port"
}
`
