package monitor

import (
	"context"
	"fmt"
	"slices"
)

func CaddyCanCutoverNginx(inst CaddyInstall) bool {
	return inst.Method != CaddyNotInstalled && inst.Method != CaddyDocker && inst.ServiceManager == "systemd" &&
		ValidService(inst.Unit) && inst.Unit != "nginx.service" && !inst.Resume &&
		(inst.Active == "inactive" || inst.Active == "failed") && slices.Equal(inst.Conflicts, []string{"nginx"})
}

func CaddyNginxCutover(ctx context.Context, run CaddyRunner, inst CaddyInstall, change CaddyChange, progress func(string)) (CaddyApplyResult, error) {
	if !CaddyCanCutoverNginx(inst) || len(change.Writes) == 0 {
		return CaddyApplyResult{}, ErrCaddyUnsafe
	}
	for _, file := range change.Writes {
		if _, ok := change.Expect[file.Path]; !ok && !slices.Contains(change.ExpectMissing, file.Path) {
			return CaddyApplyResult{}, ErrCaddyChanged
		}
	}
	for _, name := range change.Deletes {
		if _, ok := change.Expect[name]; !ok {
			return CaddyApplyResult{}, ErrCaddyChanged
		}
	}
	change.Reload = false
	unit := privilegeQuote(inst.Unit)
	enable, err := caddyServiceScript(inst, CaddyEnableNow)
	if err != nil {
		return CaddyApplyResult{}, err
	}
	stop, err := caddyServiceScript(inst, CaddyStop)
	if err != nil {
		return CaddyApplyResult{}, err
	}
	hooks := caddyTransactionHooks{
		prepare: "cutover=''\ncommand -v 'ss' >/dev/null || exit '1'\n" + caddyBootStateShell,
		apply: fmt.Sprintf(`systemctl 'is-active' '--quiet' 'nginx.service'
caddy_state=$(systemctl 'show' '--property=ActiveState' '--value' %[1]s)
case "$caddy_state" in inactive|failed) ;; *) exit '1' ;; esac
nginx_boot=$(read_boot_state 'nginx.service')
caddy_boot=$(read_boot_state %[1]s)
nginx_pid=$(systemctl 'show' '--property=MainPID' '--value' 'nginx.service')
case "$nginx_pid" in ''|0|*[!0-9]*) exit '1' ;; esac
before=$(ss '-H' '-ltnp' 'sport = :80 or sport = :443')
printf '%%s\n' "$before" | awk -v "pid=$nginx_pid" 'index($0, "pid=" pid ",") { found=1 } END { exit !found }'
cutover='yes'
printf '%%s\n' 'Stopping and disabling nginx.service'
'systemctl' 'disable' '--now' 'nginx.service'
printf '%%s\n' 'Enabling and starting Caddy, then verifying TCP ports 80 and 443'
%[2]s
verified=''
for attempt in '1' '2' '3' '4' '5'; do
  systemctl 'is-active' '--quiet' %[1]s || exit '1'
  pid=$(systemctl 'show' '--property=MainPID' '--value' %[1]s)
  case "$pid" in ''|0|*[!0-9]*) exit '1' ;; esac
  listeners=$(ss '-H' '-ltnp' 'sport = :80 or sport = :443')
  if printf '%%s\n' "$listeners" | awk -v "pid=$pid" 'index($0, "pid=" pid ",") { if ($4 ~ /:80$/) http=1; if ($4 ~ /:443$/) https=1 } END { exit !(http && https) }'; then
    verified='yes'; break
  fi
  sleep '1'
done
if [ -z "$verified" ]; then printf '%%s\n' 'Caddy did not listen on both TCP ports 80 and 443' >&2; exit '1'; fi
`, unit, enable),
		beforeRestore: fmt.Sprintf(`    if [ -n "$cutover" ]; then
      %s >&2 || failed='yes'
      restore_boot_state %s "$caddy_boot" >&2 || failed='yes'
    fi
`, stop, unit),
		afterRestore: `    if [ -n "$cutover" ]; then
      restore_boot_state 'nginx.service' "$nginx_boot" >&2 || failed='yes'
      systemctl 'start' 'nginx.service' >&2 || failed='yes'
      systemctl 'is-active' '--quiet' 'nginx.service' || failed='yes'
    fi
`,
	}
	return caddyRunChange(ctx, run, inst, change, hooks, progress)
}

const caddyBootStateShell = `read_boot_state() {
  boot=$(systemctl 'show' '--property=UnitFileState' '--value' "$1") || return '1'
  case "$boot" in enabled|disabled|enabled-runtime) printf '%s' "$boot" ;; *) return '1' ;; esac
}
restore_boot_state() {
  systemctl 'disable' "$1" || return '1'
  case "$2" in enabled) systemctl 'enable' "$1" ;; enabled-runtime) systemctl 'enable' '--runtime' "$1" ;; esac
}
`
