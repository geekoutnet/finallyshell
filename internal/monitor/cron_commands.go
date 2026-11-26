package monitor

import (
	"errors"
	"strings"
)

func cronArgs(user string) ([]string, error) {
	args := []string{"env", "LC_ALL=C", "crontab"}
	if user != "" {
		if !ValidCronUser(user) {
			return nil, errors.New("invalid user name")
		}
		args = append(args, "-u", user)
	}
	return args, nil
}

func CronReadCommand(user string) (string, error) {
	args, err := cronArgs(user)
	if err != nil {
		return "", err
	}
	return SystemCommand(append(args, "-l")...), nil
}

func CronReadTransport(user string) (string, error) {
	command, err := CronReadCommand(user)
	if err != nil || user == "" {
		return command, err
	}
	return "if [ -t 1 ]; then 'stty' '-onlcr' || exit 1; fi\nexec " + command, nil
}

func CronMissing(stderr string) bool {
	text := strings.TrimSpace(stderr)
	return strings.HasPrefix(text, "no crontab for ") || strings.HasPrefix(text, "crontab: no crontab for ") ||
		strings.HasPrefix(text, "crontab: can't open '") && strings.HasSuffix(text, "': No such file or directory") && !strings.Contains(text, "\n")
}

func CronWriteCommand(user, before string, exists bool) (string, error) {
	read, err := CronReadCommand(user)
	if err != nil {
		return "", err
	}
	args, _ := cronArgs(user)
	write := SystemCommand(append(args, "-")...)
	return `set -eu
umask 077
uid=$('id' '-u')
if command -v getent >/dev/null 2>&1; then
    entry=$('getent' 'passwd' "$uid")
else
    entry=$('awk' '-F:' '-v' "uid=$uid" '$3 == uid {print; exit}' '/etc/passwd')
fi
home=$('printf' '%s\n' "$entry" | 'cut' '-d:' '-f6')
case "$home" in /*) ;; *) exit 1 ;; esac
tmp=$('mktemp' '-d' "$home/.finallyshell-cron.XXXXXXXX")
trap 'rm -f -- "$tmp/old" "$tmp/err" "$tmp/new"; rmdir -- "$tmp"' EXIT
'cat' >"$tmp/new"
if ` + read + ` >"$tmp/old" 2>"$tmp/err"; then
    [ ` + privilegeQuote(boolWord(exists)) + ` = 'yes' ] || { 'echo' 'Crontab now exists; refresh first' >&2; exit 1; }
    actual=$('sha256sum' <"$tmp/old")
    [ "${actual%% *}" = ` + privilegeQuote(systemDigest(before)) + ` ] || { 'echo' 'Crontab changed; refresh first' >&2; exit 1; }
    stamp=$('date' '+%Y%m%d%H%M%S')
    'ln' "$tmp/old" "$home/.crontab.bak-$stamp"
    'printf' 'Backup: %s\n' "$home/.crontab.bak-$stamp"
else
    case $('cat' "$tmp/err") in
        'no crontab for '*|'crontab: no crontab for '*|"crontab: can't open '"*"': No such file or directory") [ ` + privilegeQuote(boolWord(exists)) + ` = 'no' ] || { 'echo' 'Crontab no longer exists; refresh first' >&2; exit 1; } ;;
        *) 'cat' "$tmp/err" >&2; exit 1 ;;
    esac
fi
` + write + ` <"$tmp/new"
`, nil
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

const CronUsers = `if command -v getent >/dev/null 2>&1; then 'getent' 'passwd'; else 'cat' '/etc/passwd'; fi | 'cut' '-d:' '-f1'`

const CronSystemFiles = `for file in /etc/crontab /etc/cron.d/*; do
    if [ -f "$file" ]; then 'printf' '%s\n' "$file"; fi
done`

func CronTimersCommand() string {
	return systemctl("list-timers", "--all", "--no-pager", "--no-legend", "--full")
}

func CronRunCommand(user string) (string, error) {
	if user == "" {
		return SystemCommand("sh", "-s"), nil
	}
	if !ValidCronUser(user) {
		return "", errors.New("invalid user name")
	}
	// The user it already runs as needs no su, which OpenWrt does not have.
	return "if [ \"$('id' '-un')\" = " + privilegeQuote(user) + " ]; then exec 'sh' '-s'; fi\nexec " +
		SystemCommand("su", "-s", "/bin/sh", "-c", "exec sh -s", "--", user), nil
}

func CronManualInput(command string) (string, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "%\x00\r\n") {
		return "", errors.New("command uses unsupported cron syntax")
	}
	return command + "\n", nil
}

func CronLogRedirect(file string) (string, error) {
	if !ValidLogPath(file) || strings.Contains(file, "%") {
		return "", errors.New("invalid log path")
	}
	return " >> " + privilegeQuote(file) + " 2>&1", nil
}
