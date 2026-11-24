package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"

	"golang.org/x/crypto/ssh"
)

var errKeyLine = errors.New("invalid SSH public key")

func keysAuthorizedScript(input string) (string, error) {
	line := strings.TrimSuffix(input, "\n")
	key, comment, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) != 0 || len(rest) != 0 || !sshx.ValidKeyComment(comment) || strings.ContainsAny(line, "\r\n\x00") {
		return "", errKeyLine
	}
	canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if !strings.HasPrefix(line+" ", canonical+" ") {
		return "", errKeyLine
	}
	_, blob, _ := strings.Cut(canonical, " ")
	return `set -eu
umask '077'
dir="${HOME:?}/.ssh"
file="$dir/authorized_keys"
[ ! -L "$dir" ] && [ ! -L "$file" ] || exit 1
'mkdir' '-p' "$dir"
lock="$dir/.finallyshell-authorized_keys.lock"
'mkdir' "$lock" || exit 1
trap 'rmdir "$lock"' EXIT
trap 'exit 1' HUP INT TERM
[ ! -L "$file" ] || exit 1
'chmod' '700' "$dir"
if [ -e "$file" ]; then
  [ -f "$file" ] && [ -r "$file" ] || exit 1
else
  'touch' "$file"
fi
'chmod' '600' "$file"
if 'grep' '-qxF' '-f' '-' "$file" <<'FINALLYSHELL_AUTHORIZED_KEY'
` + line + `
FINALLYSHELL_AUTHORIZED_KEY
then
  :
else
  [ "$?" -eq '1' ] || exit 1
  if 'awk' 'FILENAME == "-" { blob = $0; next } /^[[:space:]]*#/ { next } { for (i = 2; i <= NF; i++) if ($i == blob && $(i-1) ~ /^(ssh-|ecdsa-|sk-)/) found=1 } END { exit !found }' '-' "$file" <<'FINALLYSHELL_KEY_BLOB'
` + blob + `
FINALLYSHELL_KEY_BLOB
  then
    :
  else
    [ "$?" -eq '1' ] || exit 1
    stamp=$('date' '+%Y%m%d%H%M%S')
    backup="$file.bak-$stamp"
    (set -C; 'cat' "$file" > "$backup") || exit 1
    stage="$lock/authorized_keys"
    trap 'rm -f "$lock/authorized_keys"; rmdir "$lock"' EXIT
    'cat' "$file" > "$stage"
    { if [ -s "$file" ]; then 'printf' '\n'; fi; 'cat'; } >> "$stage" <<'FINALLYSHELL_AUTHORIZED_KEY'
` + line + `
FINALLYSHELL_AUTHORIZED_KEY
    'mv' '-f' "$stage" "$file"
  fi
fi
if 'command' '-v' 'restorecon' >/dev/null 2>&1; then 'restorecon' "$dir" "$file"; fi
`, nil
}

const keysDisablePasswordScript = `set -eu
umask '077'
'export' 'LC_ALL=C'
config='/etc/ssh/sshd_config'
directory='/etc/ssh/sshd_config.d'
dropin="$directory/00-finallyshell-disable-password.conf"
[ -f "$config" ] && [ ! -L "$config" ] || exit 1
sshd=$('command' '-v' 'sshd')
"$sshd" '-t' '-f' "$config"
safe_config() {
  'awk' '-v' "allowed=$2" '
    /^[[:space:]]*#/ { next }
    tolower($0) ~ /^[[:space:]]*match([[:space:]=]|$)/ { blocked=1 }
    tolower($0) ~ /^[[:space:]]*include([[:space:]=]|$)/ && (tolower($1)!="include" || allowed=="" || $2!=allowed || (NF>2 && $3 !~ /^#/)) { blocked=1 }
    END { exit blocked }
  ' "$1"
}
if ! safe_config "$config" '/etc/ssh/sshd_config.d/*.conf'; then
  'printf' '%s\n' 'Custom Match or Include rules require manual review' >&2
  exit 1
fi
[ ! -L "$directory" ] || exit 1
for included in "$directory"/*.conf; do
  [ -e "$included" ] || continue
  if [ -L "$included" ] || [ ! -f "$included" ] || ! safe_config "$included" ''; then
    'printf' '%s\n' 'Custom Match or Include rules require manual review' >&2
    exit 1
  fi
done
mode=''
service=''
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
lock='/etc/ssh/.finallyshell-sshd.lock'
'mkdir' "$lock"
target=''
backup=''
installed=''
rollback() {
  if [ -n "$installed" ]; then
    if [ -n "$backup" ]; then
      'cp' '-p' "$backup" "$lock/restore" && 'mv' '-f' "$lock/restore" "$target" || 'printf' '%s\n' 'Rollback failed; restore the backup manually' >&2
    else
      'rm' '-f' "$target"
    fi
    if 'command' '-v' 'restorecon' >/dev/null 2>&1 && [ -e "$target" ]; then 'restorecon' "$target" || :; fi
    'printf' '%s\n' 'Configuration change rolled back' >&2
    if [ -n "$backup" ]; then 'printf' '%s\n' "$backup" >&2; fi
  fi
  'rm' '-f' "$lock/new" "$lock/restore"
  'rmdir' "$lock"
}
trap 'rollback' EXIT
trap 'exit 1' HUP INT TERM
case "$mode" in systemd) 'systemctl' 'is-active' '--quiet' "$service" ;; esac
if [ -d "$directory" ] && [ ! -L "$directory" ] && 'awk' '
  tolower($1)=="match" || tolower($1)=="passwordauthentication" { exit }
  tolower($1)=="include" { if ($2=="/etc/ssh/sshd_config.d/*.conf") found=1; exit }
  END { exit !found }
' "$config"; then
  target="$dropin"
else
  target="$config"
fi
[ ! -L "$target" ] || exit 1
if [ -e "$target" ]; then
  [ -f "$target" ] || exit 1
  stamp=$('date' '+%Y%m%d%H%M%S')
  backup="$target.bak-$stamp"
  'ln' "$target" "$backup"
  'cp' '-p' "$target" "$lock/new"
fi
{
  'printf' '%s\n' 'PasswordAuthentication no'
  if [ "$target" = "$config" ]; then 'cat' "$config"; fi
} > "$lock/new"
installed='yes'
'mv' '-f' "$lock/new" "$target"
if 'command' '-v' 'restorecon' >/dev/null 2>&1; then 'restorecon' "$target"; fi
"$sshd" '-t' '-f' "$config"
effective=$("$sshd" '-T' '-f' "$config")
'printf' '%s\n' "$effective" | 'grep' '-qxF' 'passwordauthentication no'
'printf' '%s\n' "$effective" | 'grep' '-qxF' 'pubkeyauthentication yes'
'printf' '%s\n' "$effective" | 'grep' '-qE' '^authenticationmethods (any|publickey)( |$)'
case "$mode" in
  systemd) 'systemctl' 'reload' "$service" ;;
  service) 'service' "$service" 'reload' ;;
esac
installed=''
'printf' '%s\n' "$target"
if [ -n "$backup" ]; then 'printf' '%s\n' "$backup"; fi
`

type keysRemote struct {
	client    *ssh.Client
	run       func(context.Context, string) (string, string, error)
	close     func()
	privilege monitor.PrivilegeRunner
}

type keysDeployBackend struct {
	connect func(context.Context, *config.Host, sshx.Options) (keysRemote, error)
}

type keysTarget struct {
	host *config.Host
	opts sshx.Options
	err  error
}

type keysResult struct {
	target   keysTarget
	verified bool
	appended bool
	switched bool
	disabled bool
	output   string
	err      error
}

func (b keysDeployBackend) deploy(ctx context.Context, target keysTarget, script, path, passphrase, fingerprint string, verify bool) keysResult {
	result := keysResult{target: target, err: target.err}
	if result.err != nil {
		return result
	}
	if ctx.Err() != nil {
		result.err = ctx.Err()
		return result
	}
	remote, err := b.connect(ctx, target.host, target.opts)
	if err != nil {
		result.err = err
		return result
	}
	if remote.run == nil {
		if remote.close != nil {
			remote.close()
		}
		result.err = errors.New("SSH exec unavailable")
		return result
	}
	out, stderr, err := remote.run(ctx, script)
	if remote.close != nil {
		remote.close()
	}
	result.output, result.err = keysOutput(out, stderr, script, target.opts.Password, target.opts.Passphrase, target.opts.ProxyPassword), err
	result.appended = err == nil
	if err != nil || !verify {
		return result
	}
	keyHost, opts := target.keyLogin(path, passphrase, fingerprint)
	opts.AskHostKey = nil
	verified, err := b.connect(ctx, keyHost, opts)
	if verified.close != nil {
		verified.close()
	}
	result.verified, result.err = err == nil, err
	return result
}

func (target keysTarget) keyLogin(path, passphrase, fingerprint string) (*config.Host, sshx.Options) {
	host, opts := target.host.Clone(), target.opts
	host.Auth, host.KeyPath = config.AuthKey, path
	opts.Password, opts.Passphrase, opts.VerifyKeyFingerprint = "", passphrase, fingerprint
	return host, opts
}

func keysOutput(out, stderr, script string, secrets ...string) string {
	text := strings.TrimSpace(out + "\n" + stderr)
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	for line := range strings.SplitSeq(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (strings.HasPrefix(fields[0], "ssh-") || strings.HasPrefix(fields[0], "ecdsa-") || strings.HasPrefix(fields[0], "sk-")) {
			text = strings.ReplaceAll(text, line, "[redacted]")
			text = strings.ReplaceAll(text, fields[1], "[redacted]")
		}
	}
	return strings.TrimSpace(strings.Map(func(char rune) rune {
		if char < ' ' && char != '\n' && char != '\t' || char == 127 {
			return -1
		}
		return char
	}, text))
}

func connectKeysRemote(ctx context.Context, host *config.Host, opts sshx.Options) (keysRemote, error) {
	if err := ctx.Err(); err != nil {
		return keysRemote{}, err
	}
	type dialResult struct {
		client *ssh.Client
		err    error
	}
	done := make(chan dialResult)
	go func() {
		client, err := sshx.Dial(host, opts)
		select {
		case done <- dialResult{client, err}:
		case <-ctx.Done():
			if client != nil {
				client.Close()
			}
		}
	}()
	select {
	case result := <-done:
		if result.err != nil {
			return keysRemote{}, result.err
		}
		return keysRemote{client: result.client, run: func(ctx context.Context, script string) (string, string, error) {
			return monitor.RunScript(ctx, result.client, script)
		}, close: func() { result.client.Close() }}, nil
	case <-ctx.Done():
		return keysRemote{}, ctx.Err()
	}
}

const keysHostTimeout = 2 * time.Minute
