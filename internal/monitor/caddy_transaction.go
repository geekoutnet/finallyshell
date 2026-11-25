package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

func caddyValidateCommand(inst CaddyInstall, config string) string {
	args := caddyConfigArgs(inst, "validate", config)
	if inst.Method == CaddyDocker {
		script := "set -eu\n" + caddyValidationScratch + SystemCommand(append([]string{"caddy"}, args...)...)
		return SystemCommand("docker", "exec", inst.Container, "sh", "-c", script)
	}
	return caddyValidationEnvironment(inst, caddyCLI(inst, args...))
}

func caddyReloadCommand(inst CaddyInstall, config string) (string, error) {
	if inst.Method != CaddyDocker && inst.Unit != "" {
		return SystemCommand("systemctl", "reload", inst.Unit), nil
	}
	if inst.AdminAddr == "off" {
		return "", ErrCaddyUnsafe
	}
	args := append(caddyConfigArgs(inst, "reload", config), "--force")
	if inst.AdminAddr != "" && inst.AdminAddr != "localhost:2019" && !strings.ContainsAny(inst.AdminAddr, "\x00\n\r") {
		args = append(args, "--address", inst.AdminAddr)
	}
	return caddyEnvironment(inst, caddyCLI(inst, args...)), nil
}

const caddyTransactionShell = `set -eu
umask '077'
mark() { printf 'CADDY_TX %s\n' "$*"; }
safe_caddy_dir() {
  [ -d "$1" ] && [ ! -L "$1" ] && [ "$(cd -P -- "$1" && pwd -P)" = "$1" ]
}
check_path() {
  parent=${1%/*}; [ -n "$parent" ] || parent='/'
  [ ! -L "$1" ] && { [ ! -e "$1" ] || [ -f "$1" ]; } || return '1'
  while [ ! -d "$parent" ]; do
    [ ! -e "$parent" ] && [ ! -L "$parent" ] || return '1'
    parent=${parent%/*}; [ -n "$parent" ] || parent='/'
  done
  [ "$(cd -P -- "$parent" && pwd -P)" = "$parent" ]
}
save_old() {
  if [ -f "$1" ]; then
    mkdir -p -- "$backup${1%/*}" && cp -p -- "$1" "$backup$1" || return '1'
  fi
}
restore_one() {
  if [ -f "$backup$1" ]; then
    new=$(mktemp "${1%/*}/.finallyshell.XXXXXXXX") || return '1'
    cp -p -- "$backup$1" "$new" && mv -f -- "$new" "$1" && return '0'
    rm -f -- "$new"
    return '1'
  fi
  rm -f -- "$1"
}
ensure_dir() {
  [ ! -L "$1" ] || return '1'
  [ ! -d "$1" ] || return '0'
  ensure_dir "${1%/*}" && (umask '022'; mkdir -- "$1") || return '1'
  printf '%s\n' "$1" >> "$lock/dirs"
}
stage() {
  parent=${1%/*}; ensure_dir "${parent:-/}" || return '1'
  tmp=$(mktemp "${1%/*}/.finallyshell.XXXXXXXX") || return '1'
  base64 -d > "$tmp" || return '1'
  if [ -f "$1" ]; then
    owner=$(stat -c '%u:%g' -- "$1") && mode=$(stat -c '%a' -- "$1") || return '1'
    chown -- "$owner" "$tmp" && chmod -- "$mode" "$tmp" || return '1'
  else
    if id 'caddy' >/dev/null 2>&1; then chown 'caddy:caddy' "$tmp" || return '1'; fi
    chmod '0644' "$tmp" || return '1'
  fi
  mv -f -- "$tmp" "$1" || return '1'
  tmp=''
}
expect_file() {
  [ -f "$1" ] || return '1'
  actual=$(sha256sum < "$1") || return '1'
  [ "${actual%% *}" = "$2" ]
}
check_import_set() {
  for found do
    [ -f "$found" ] || continue
    case "$found" in */.*) continue ;; esac
    [ -f "$lock/expected$found" ] || { mark 'changed'; exit '1'; }
  done
}
prune_backups() {
  count=0
  ls -1r -- "$backups" | while IFS= read -r name; do
    case "$name" in *[!0-9.]*|'') continue ;; esac
    case "$name" in ??????????????|??????????????.[0-9]*) ;; *) continue ;; esac
    [ -d "$backups/$name" ] && [ ! -L "$backups/$name" ] || continue
    count=$((count + 1))
    if [ "$count" -gt '20' ] && [ "$backups/$name" != "$backup" ]; then rm -rf -- "$backups/$name" || break; fi
  done
}
`

func caddyChangePaths(inst CaddyInstall, change CaddyChange) ([]string, []string, error) {
	touched, checked := map[string]bool{}, map[string]bool{}
	backupRoot := caddyBackupRoot(inst)
	add := func(name string, touch bool) error {
		if !caddyPath(name) || strings.HasPrefix(name, backupRoot+"/") || name == backupRoot || strings.Contains(name, "/.finallyshell.") || (touch && touched[name]) {
			return fmt.Errorf("%w: %q", ErrCaddyUnsafe, name)
		}
		if touch {
			touched[name] = true
			if inst.Method == CaddyDocker && len(inst.Mounts) > 0 {
				var selected CaddyMount
				for _, mount := range inst.Mounts {
					if name == mount.Source {
						return fmt.Errorf("%w: cannot replace a container file mount: %q", ErrCaddyUnsafe, name)
					}
					if caddyPath(mount.Source) && caddyPath(mount.Destination) && strings.HasPrefix(name, mount.Source+"/") && len(mount.Source) > len(selected.Source) {
						selected = mount
					}
				}
				if !selected.RW {
					return fmt.Errorf("%w: not on a writable container directory mount: %q", ErrCaddyUnsafe, name)
				}
			}
		}
		checked[name] = true
		return nil
	}
	total := 0
	for _, file := range change.Writes {
		total += len(file.Content)
		if len(file.Content) > caddyFileLimit || total > caddyMaxTree {
			return nil, nil, ErrCaddyUnsafe
		}
		if err := add(file.Path, true); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range change.Deletes {
		if name == inst.Config {
			return nil, nil, ErrCaddyUnsafe
		}
		if err := add(name, true); err != nil {
			return nil, nil, err
		}
	}
	for name := range change.Expect {
		if err := add(name, false); err != nil {
			return nil, nil, err
		}
	}
	for _, name := range slices.Sorted(slices.Values(change.ExpectMissing)) {
		if _, exists := change.Expect[name]; exists {
			return nil, nil, ErrCaddyUnsafe
		}
		if err := add(name, false); err != nil {
			return nil, nil, err
		}
	}
	if len(checked) > caddyMaxFiles {
		return nil, nil, ErrCaddyUnsafe
	}
	return slices.Sorted(maps.Keys(touched)), slices.Sorted(maps.Keys(checked)), nil
}

func caddyPayload(script *strings.Builder, command, content string) {
	fmt.Fprintf(script, "%s <<'CADDY_PAYLOAD'\n", command)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	for len(encoded) > 76 {
		script.WriteString(encoded[:76] + "\n")
		encoded = encoded[76:]
	}
	script.WriteString(encoded + "\nCADDY_PAYLOAD\n")
}

func caddyTransactionScript(inst CaddyInstall, change CaddyChange) (string, error) {
	if inst.Resume || inst.ContainerConfigUnmapped {
		return "", ErrCaddyUnsafe
	}
	touched, checked, err := caddyChangePaths(inst, change)
	if err != nil {
		return "", err
	}
	config := inst.Config
	if inst.Method == CaddyDocker {
		var ok bool
		config, ok = caddyContainerPath(inst, config)
		if !ok {
			return "", ErrCaddyUnsafe
		}
	}
	var reload string
	if change.Reload {
		reload, err = caddyReloadCommand(inst, config)
		if err != nil {
			return "", err
		}
	}
	var script strings.Builder
	script.WriteString(caddyTransactionShell)
	base := path.Dir(inst.Config)
	fmt.Fprintf(&script, "lock=%s\nbackups=%s\n", privilegeQuote(path.Join(base, ".finallyshell.lock")), privilegeQuote(path.Join(base, ".backups")))
	fmt.Fprintf(&script, "check_path %s\n", privilegeQuote(inst.Config))
	script.WriteString(`mkdir -- "$lock" || { mark 'busy'; exit '1'; }
backup=''; tmp=''; dirty=''; reload_started=''
cleanup() {
  status=$?
  trap - '0'; trap '' HUP INT TERM
  set +e
  [ -z "$tmp" ] || rm -f -- "$tmp"
  if [ -n "$dirty" ]; then
    failed=''
`)
	for _, name := range touched {
		fmt.Fprintf(&script, "    restore_one %s || failed='yes'\n", privilegeQuote(name))
	}
	script.WriteString(`    if [ -f "$lock/dirs" ]; then
      awk '{ rows[NR]=$0 } END { for (idx=NR; idx>0; idx--) print rows[idx] }' "$lock/dirs" |
        while IFS= read -r dir; do rmdir -- "$dir" || exit '1'; done || failed='yes'
    fi
`)
	if reload != "" {
		fmt.Fprintf(&script, "    if [ -z \"$failed\" ] && [ -n \"$reload_started\" ]; then { %s; } >&2 || failed='yes'; fi\n", reload)
	}
	script.WriteString(`    if [ -z "$failed" ]; then mark 'rolled-back'; else mark 'rollback-failed'; status='1'; fi
  fi
  rm -f -- "$lock/dirs" "$lock/validate"
  if [ -d "$lock/expected" ]; then rm -rf -- "$lock/expected"; fi
  rmdir -- "$lock"
  exit "$status"
}
trap cleanup '0'
trap 'exit 1' HUP INT TERM
`)
	for _, name := range checked {
		fmt.Fprintf(&script, "check_path %s\n", privilegeQuote(name))
	}
	for _, name := range slices.Sorted(maps.Keys(change.Expect)) {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(change.Expect[name])))
		fmt.Fprintf(&script, "expect_file %s %s || { mark 'changed'; exit '1'; }\n", privilegeQuote(name), privilegeQuote(digest))
	}
	for _, name := range slices.Sorted(slices.Values(change.ExpectMissing)) {
		fmt.Fprintf(&script, "[ ! -e %s ] || { mark 'changed'; exit '1'; }\n", privilegeQuote(name))
	}
	for _, pattern := range change.importPatterns {
		if !caddyPath(pattern) {
			return "", ErrCaddyUnsafe
		}
	}
	if change.requireEmptySites {
		fmt.Fprintf(&script, "for site in %s/*.caddy; do if [ -e \"$site\" ] || [ -L \"$site\" ]; then mark 'changed'; exit '1'; fi; done\n", privilegeQuote(inst.SitesDir))
	}
	if len(change.importPatterns) != 0 {
		for _, name := range slices.Sorted(maps.Keys(change.Expect)) {
			fmt.Fprintf(&script, "mkdir -p -- \"$lock/expected\"%s\n: > \"$lock/expected\"%s\n", privilegeQuote(path.Dir(name)), privilegeQuote(name))
		}
		for _, pattern := range change.importPatterns {
			if expansion, ok := caddyGlob(pattern); ok {
				fmt.Fprintf(&script, "check_import_set %s\n", expansion)
			}
		}
	}
	if len(touched) != 0 {
		script.WriteString(`[ ! -L "$backups" ]
mkdir -p -- "$backups"
safe_caddy_dir "$backups"
stamp=$(date -u '+%Y%m%d%H%M%S')
backup="$backups/$stamp"; serial=0
while ! mkdir -- "$backup" 2>/dev/null; do
  [ -d "$backup" ] && [ ! -L "$backup" ] || exit '1'
  serial=$((serial + 1)); [ "$serial" -le '999' ] || exit '1'
  backup="$backups/$stamp.$(printf '%03d' "$serial")"
done
`)
		for _, name := range touched {
			fmt.Fprintf(&script, "save_old %s\nprintf '%%s\\n' %s >> \"$backup/.finallyshell-paths\"\n", privilegeQuote(name), privilegeQuote(name))
		}
		script.WriteString("mark \"backup $backup\"\ndirty='yes'\n")
	}
	for _, file := range change.Writes {
		caddyPayload(&script, "stage "+privilegeQuote(file.Path), file.Content)
	}
	for _, name := range change.Deletes {
		fmt.Fprintf(&script, "rm -f -- %s\n", privilegeQuote(name))
	}
	fmt.Fprintf(&script, `valid='yes'
{ %s; } > "$lock/validate" 2>&1 || valid=''
printf 'CADDY_TX output '
base64 < "$lock/validate" | tr -d '\n'
printf '\n'
[ -n "$valid" ] || { mark 'invalid'; exit '1'; }
mark 'validated'
`, caddyValidateCommand(inst, config))
	if reload != "" {
		fmt.Fprintf(&script, "reload_started='yes'\n{ %s; } >&2 || { mark 'reload-failed'; exit '1'; }\n", reload)
	}
	script.WriteString("dirty=''\n")
	if reload != "" {
		script.WriteString("mark 'reloaded'\n")
	}
	script.WriteString("if [ -n \"$backup\" ]; then prune_backups >&2 || :; fi\n")
	return script.String(), nil
}

func applyCaddyChange(ctx context.Context, run CaddyRunner, inst CaddyInstall, change CaddyChange) (CaddyApplyResult, error) {
	var result CaddyApplyResult
	inst, err := caddyNormalize(inst)
	if err != nil {
		return result, err
	}
	script, err := caddyTransactionScript(inst, change)
	if err != nil {
		return result, err
	}
	stdout, stderr, err := caddyRunPayload(ctx, run, script, nil)
	var state error
	validated := false
	for line := range strings.SplitSeq(stdout, "\n") {
		mark, ok := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "CADDY_TX ")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(mark, "backup "):
			result.Backup = strings.TrimPrefix(mark, "backup ")
		case strings.HasPrefix(mark, "output "):
			data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(mark, "output "))
			result.Validate = strings.TrimSpace(string(data))
		case mark == "validated":
			validated = true
		case mark == "reloaded":
			result.Reloaded = true
		case mark == "rolled-back":
			result.RolledBack = true
		case mark == "rollback-failed":
			state = ErrCaddyRollback
		case mark == "changed":
			state = ErrCaddyChanged
		case mark == "busy":
			state = ErrCaddyBusy
		case mark == "invalid":
			state = ErrCaddyInvalid
		}
	}
	if state != nil {
		return result, caddyCommandError(state, caddyOutput(result.Validate, stderr))
	}
	if err != nil {
		if result.Backup != "" && !result.RolledBack && !result.Reloaded {
			return result, fmt.Errorf("%w: %w", ErrCaddyRollback, caddyCommandError(err, strings.TrimSpace(stderr)))
		}
		return result, caddyCommandError(err, strings.TrimSpace(stderr))
	}
	if !validated || (change.Reload && !result.Reloaded) {
		return result, fmt.Errorf("%w: incomplete transaction report", ErrCaddyRollback)
	}
	return result, nil
}
