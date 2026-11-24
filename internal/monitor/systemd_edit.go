package monitor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const SystemEditLimit = 256 << 10

var cronSystemName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

func validSystemFile(file string) bool {
	if file == "/etc/crontab" {
		return true
	}
	if name, ok := strings.CutPrefix(file, "/etc/cron.d/"); ok {
		return cronSystemName.MatchString(name)
	}
	if name, ok := strings.CutPrefix(file, "/etc/systemd/system/"); ok {
		unit, ok := strings.CutSuffix(name, ".d/override.conf")
		return ok && ValidService(unit)
	}
	return false
}

func SystemFileReadCommand(file string) (string, error) {
	if !validSystemFile(file) {
		return "", errors.New("invalid system file")
	}
	return "file=" + privilegeQuote(file) + "\ndir=" + privilegeQuote(path.Dir(file)) + `
if [ -t 1 ]; then 'stty' '-onlcr' || exit 1; fi
if [ -L "$dir" ] || [ -L "$file" ]; then exit 1; fi
if [ -f "$file" ]; then
    'printf' 'present\n'
    exec 'head' '-c' '262145' "$file"
fi
if [ -e "$file" ]; then exit 1; fi
'printf' 'missing\n'`, nil
}

func SystemFileWriteCommand(file, original string, exists bool) (string, error) {
	if !validSystemFile(file) {
		return "", errors.New("invalid system file")
	}
	dir := path.Dir(file)
	script := "file=" + privilegeQuote(file) + "\ndir=" + privilegeQuote(dir) + "\n" + `
set -eu
umask 077
[ ! -L "$dir" ] && [ ! -L "$file" ] || { 'echo' 'Refusing a symbolic link' >&2; exit 1; }
'mkdir' '-p' "$dir"
tmp=$('mktemp' "$dir/.finallyshell.XXXXXXXX")
backup=''
trap 'rm -f -- "$tmp" ${backup:+"$backup"}' EXIT
'cat' >"$tmp"
`
	if exists {
		script += "expected=" + privilegeQuote(systemDigest(original)) + "\n" + `
[ -f "$file" ] || { 'echo' 'File no longer exists' >&2; exit 1; }
backup=$('mktemp' "$dir/.finallyshell.XXXXXXXX")
'cp' '-p' "$file" "$backup"
actual=$('sha256sum' <"$backup")
[ "${actual%% *}" = "$expected" ] || { 'echo' 'File changed; reopen the editor' >&2; exit 1; }
stamp=$('date' '+%Y%m%d%H%M%S')
'ln' "$backup" "$file.bak-$stamp"
'printf' 'Backup: %s\n' "$file.bak-$stamp"
'chown' '--reference='"$backup" "$tmp"
'chmod' '--reference='"$backup" "$tmp"
'cmp' '-s' "$file" "$backup" || { 'echo' 'File changed; reopen the editor' >&2; exit 1; }
'mv' '-f' "$tmp" "$file"
`
	} else {
		script += "'ln' \"$tmp\" \"$file\"\n"
	}
	if strings.HasPrefix(file, "/etc/systemd/") {
		script += systemctl("daemon-reload", "--no-ask-password") + "\n"
	}
	return script, nil
}

func systemDigest(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(text))) }
