package monitor

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const NginxMain = "/etc/nginx/nginx.conf"
const NginxAvailable = "/etc/nginx/sites-available/"
const NginxEnabled = "/etc/nginx/sites-enabled/"
const NginxConfD = "/etc/nginx/conf.d/"
const NginxDumpCommand = "'nginx' '-T'"

var nginxFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var nginxBackupSuffix = regexp.MustCompile(`\.bak-[0-9]{14}$`)
var nginxPath = regexp.MustCompile(`^/[A-Za-z0-9_./+@-]+$`)
var dnsLabel = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var nginxSecret = regexp.MustCompile(`(?im)-----BEGIN [^-\r\n]*PRIVATE KEY-----|://[^/\s"'@]+@|[?&](?:token|secret|password|api[_-]?key|signature)=|\b(?:authorization|proxy-authorization|cookie|x-api-key|password|passwd|passphrase|secret|token|api[_-]?key)\s*(?:[:=]|['"]?[ \t]+)[ \t]*[^$\s;]|^\s*(?:auth_basic_user_file|ssl_password_file)\s+[^/\s]`)

var errNginxInput = errors.New("invalid or unsupported nginx configuration target")

func ValidSiteHost(name string) bool {
	if len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	labels := strings.Split(name, ".")
	if _, err := strconv.Atoi(labels[len(labels)-1]); err == nil {
		return false
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func ValidSiteFilename(name string) bool {
	return nginxFileName.MatchString(name) && !nginxBackupSuffix.MatchString(name) &&
		!strings.HasSuffix(strings.ToLower(name), ".pem") && ValidLogPath("/etc/nginx/"+name)
}

func ValidNginxPath(file string) bool {
	return len(file) <= 1024 && nginxPath.MatchString(file) && path.Clean(file) == file && file != "/"
}

// Editable tells whether file is a configuration file this tree's nginx
// reads that may be edited: one of the usual Debian and conf.d files, or,
// for nginx kept elsewhere (the BaoTa panel's /www/server), any file of
// the dump.
func (tree NginxTree) Editable(file string) bool {
	if NginxEditable(file) {
		return true
	}
	return tree.Main != "" && tree.Main != NginxMain && ValidNginxPath(file) && ValidSiteFilename(path.Base(file)) && slices.Contains(tree.Files, file)
}

func NginxEditable(file string) bool {
	if file == NginxMain {
		return true
	}
	for _, dir := range []string{NginxAvailable, NginxConfD, "/etc/nginx/snippets/"} {
		if name, ok := strings.CutPrefix(file, dir); ok && ValidSiteFilename(name) {
			return true
		}
	}
	return false
}

func NginxConfigSafe(text string) bool {
	return len(text) <= SystemEditLimit && !strings.ContainsRune(text, 0) && !nginxSecret.MatchString(text)
}

func NginxOutputSafe(line string) bool { return !nginxSecret.MatchString(line) }

type NginxTemplate struct {
	Names                  []string
	Root, Certificate, Key string
	Port                   int
}

func (form NginxTemplate) Render() (string, error) {
	if len(form.Names) == 0 || len(form.Names) > 30 || form.Port < 0 || form.Port > 65535 || form.Port == 0 && !ValidNginxPath(form.Root) {
		return "", errNginxInput
	}
	for _, name := range form.Names {
		if !ValidSiteHost(name) {
			return "", errNginxInput
		}
	}
	tls := form.Certificate != "" || form.Key != ""
	if tls && (!ValidNginxPath(form.Certificate) || !ValidNginxPath(form.Key)) {
		return "", errNginxInput
	}
	text := "server {\n    listen 80;\n"
	if tls {
		text += "    listen 443 ssl;\n    ssl_certificate " + form.Certificate + ";\n    ssl_certificate_key " + form.Key + ";\n"
	}
	text += "    server_name " + strings.Join(form.Names, " ") + ";\n"
	if form.Port != 0 {
		text += fmt.Sprintf("    location / {\n        proxy_pass http://127.0.0.1:%d;\n        proxy_set_header Host $host;\n        proxy_set_header X-Real-IP $remote_addr;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n    }\n", form.Port)
	} else {
		text += "    root " + form.Root + ";\n    location / {\n        try_files $uri $uri/ =404;\n    }\n"
	}
	return text + "}\n", nil
}

func nginxTest(main string) (string, error) {
	if !ValidNginxPath(main) {
		return "", errNginxInput
	}
	if main == NginxMain {
		return SystemCommand("nginx", "-t"), nil
	}
	return SystemCommand("nginx", "-t", "-c", main), nil
}

// nginxCommand runs nginx with args on the configuration main: given with
// -c unless it is /etc/nginx/nginx.conf.
func nginxCommand(main string, args ...string) string {
	if main != NginxMain {
		args = append([]string{"-c", main}, args...)
	}
	return SystemCommand(append([]string{"nginx"}, args...)...)
}

func nginxReload(systemd bool) string {
	if systemd {
		return SystemCommand("systemctl", "reload", "nginx.service")
	}
	return SystemCommand("nginx", "-s", "reload")
}

func NginxActionCommand(action string, systemd bool, main string) (string, error) {
	test, err := nginxTest(main)
	if err != nil {
		return "", err
	}
	// A configuration other than /etc/nginx/nginx.conf (nginx built with
	// another path, as on the BaoTa panel, or a master started with -c)
	// is reloaded by signalling the master its pid directive names, not
	// through a unit that may start nginx with another file.
	switch action {
	case "test":
		return test, nil
	case "reload":
		if main == NginxMain {
			return "set -e\n" + test + "\n" + nginxReload(systemd), nil
		}
		return "set -e\n" + test + "\n" + nginxCommand(main, "-s", "reload"), nil
	case "restart":
		if systemd {
			return "set -e\n" + test + "\n" + SystemCommand("systemctl", "restart", "nginx.service"), nil
		}
	case "start":
		// Tested first, as a configuration nginx refuses would not start.
		if systemd {
			return "set -e\n" + test + "\n" + SystemCommand("systemctl", "start", "nginx.service"), nil
		}
		return "set -e\n" + test + "\n" + nginxCommand(main), nil
	case "stop":
		if systemd {
			return SystemCommand("systemctl", "stop", "nginx.service"), nil
		}
		return nginxCommand(main, "-s", "quit"), nil
	}
	return "", errNginxInput
}

const nginxGuard = `
guard_dir() {
    dir=$1
    while [ "$dir" != / ]; do
        [ -d "$dir" ] && [ ! -L "$dir" ] || exit 1
        attrs=$('stat' '-c' '%u %a' "$dir")
        case "$attrs" in '0 '*[2367]?|'0 '*?[2367]) exit 1;; '0 '*) ;; *) exit 1;; esac
        dir=${dir%/*}
        [ -n "$dir" ] || dir=/
    done
}
guard_file() {
    guard_dir "${1%/*}"
    [ -f "$1" ] && [ ! -L "$1" ] || exit 1
    attrs=$('stat' '-c' '%u %a' "$1")
    case "$attrs" in '0 '*[2367]?|'0 '*?[2367]) exit 1;; '0 '*) ;; *) exit 1;; esac
}
guard_public_config() {
    'guard_file' "$1"
    if 'grep' '-Eq' '^-----BEGIN .*PRIVATE KEY-----|^[^[:space:]:]+:\$[A-Za-z0-9]+\$' "$1"; then
        'echo' 'Refusing a file that appears to contain credentials' >&2
        exit 1
    fi
}
`

const nginxLF = "if [ -t 1 ]; then 'stty' '-onlcr' || exit 1; fi\n"

func (tree NginxTree) guardSnapshot() string {
	if tree.digest == "" {
		return "exit '1'\n"
	}
	return "snapshot=$(" + nginxCommand(cmp.Or(tree.Main, NginxMain), "-T") + " | 'sha256sum')\n[ \"${snapshot%% *}\" = " + privilegeQuote(tree.digest) + " ] || { 'echo' 'Nginx configuration changed; refresh first' >&2; exit 1; }\n"
}

func NginxReadCommand(file string, tree NginxTree) (string, error) {
	if !tree.Editable(file) {
		return "", errNginxInput
	}
	return "set -eu\n" + nginxLF + nginxGuard + SystemCommand("guard_public_config", file) + "\n" + SystemCommand("head", "-c", strconv.Itoa(SystemEditLimit+1), file), nil
}

type NginxChange struct {
	File, Backup, Digest string
	original             string
}

func NewNginxChange(file, original, text string, now time.Time, tree NginxTree) (NginxChange, error) {
	backup := file + ".bak-" + now.Format("20060102150405")
	if !tree.Editable(file) || !tree.AllowsBackup(backup) || !NginxConfigSafe(original) || !NginxConfigSafe(text) {
		return NginxChange{}, errNginxInput
	}
	return NginxChange{file, backup, systemDigest(text), systemDigest(original)}, nil
}

func (change NginxChange) valid(tree NginxTree) bool {
	return tree.Editable(change.File) && len(change.Digest) == 64 && strings.Trim(change.Digest, "0123456789abcdef") == "" &&
		strings.HasPrefix(change.Backup, change.File+".bak-") && len(change.Backup) == len(change.File)+19 && nginxBackupSuffix.MatchString(change.Backup)
}

const nginxReplace = `
guard_file "$file"
dir=${file%/*}
tmp=$('mktemp' "$dir/.finallyshell.XXXXXXXX")
saved=$('mktemp' "$dir/.finallyshell.XXXXXXXX") || { 'rm' '-f' "$tmp"; exit 1; }
trap 'rm -f -- "$tmp" "$saved"' EXIT
'cp' '-p' "$file" "$saved"
actual=$('sha256sum' <"$saved")
[ "${actual%% *}" = "$expected" ] || { 'echo' 'File changed; reopen the editor' >&2; exit 1; }
`

const nginxReplaceCommit = `
'chown' '--reference' "$saved" "$tmp"
'chmod' '--reference' "$saved" "$tmp"
'cmp' '-s' "$file" "$saved" || { 'echo' 'File changed; reopen the editor' >&2; exit 1; }
'ln' '-T' "$saved" "$backup"
'mv' '-fT' "$tmp" "$file"
'printf' 'finallyshell-written\n'
`

func (change NginxChange) SaveCommand(original string, tree NginxTree) (string, error) {
	if !change.valid(tree) || !NginxConfigSafe(original) || systemDigest(original) != change.original || !tree.AllowsBackup(change.Backup) {
		return "", errNginxInput
	}
	return "set -eu\n" + tree.guardSnapshot() + nginxGuard + "file=" + privilegeQuote(change.File) + "\nbackup=" + privilegeQuote(change.Backup) + "\nexpected=" + privilegeQuote(systemDigest(original)) + "\n" + nginxReplace + `
'cat' >"$tmp"
incoming=$('sha256sum' <"$tmp")
[ "${incoming%% *}" = ` + privilegeQuote(change.Digest) + " ]\n" + nginxReplaceCommit, nil
}

func (change NginxChange) RestoreCommand(tree NginxTree, now time.Time) (string, error) {
	backup := change.RestoreBackup(now)
	if !change.valid(tree) || !tree.AllowsBackup(backup) {
		return "", errNginxInput
	}
	return "set -eu\n" + nginxGuard + "file=" + privilegeQuote(change.File) + "\nbackup=" + privilegeQuote(backup) + "\nexpected=" + privilegeQuote(change.Digest) + "\n" + nginxReplace +
		SystemCommand("guard_file", change.Backup) + "\n" + SystemCommand("cat", change.Backup) + " >\"$tmp\"\n" +
		"incoming=$('sha256sum' <\"$tmp\")\n[ \"${incoming%% *}\" = " + privilegeQuote(change.original) + " ] || { 'echo' 'Backup changed; refusing restore' >&2; exit 1; }\n" + nginxReplaceCommit, nil
}

func (change NginxChange) RestoreBackup(now time.Time) string {
	backup := change.File + ".bak-" + now.Format("20060102150405")
	if backup == change.Backup {
		backup = change.File + ".bak-" + now.Add(time.Second).Format("20060102150405")
	}
	return backup
}

func NginxLogsCommand(file string) string {
	if !ValidNginxPath(file) || !ValidLogPath(file) || !strings.HasPrefix(file, "/var/log/") {
		return ""
	}
	return "set -e\n" + nginxLF + "[ -f " + privilegeQuote(file) + " ]\n" + SystemCommand("tail", "-n", "200", file)
}

func (tree NginxTree) NewSite(name string) (file, link string, err error) {
	if !ValidSiteFilename(name) || tree.Main != NginxMain {
		return "", "", errNginxInput
	}
	if tree.Debian {
		if !tree.layoutAllows(NginxEnabled+name) && tree.layoutAllows(NginxEnabled+name+".conf") {
			name += ".conf"
		}
		if tree.layoutAllows(NginxEnabled+name) && ValidSiteFilename(name) {
			return NginxAvailable + name, NginxEnabled + name, nil
		}
	} else if tree.ConfD {
		if !strings.HasSuffix(name, ".conf") {
			name += ".conf"
		}
		if tree.layoutAllows(NginxConfD+name) && ValidSiteFilename(name) {
			return NginxConfD + name, "", nil
		}
	}
	return "", "", errNginxInput
}

func (tree NginxTree) ToggleCommand(names []string, enable, reload, systemd bool) (string, error) {
	if tree.Main != NginxMain || !tree.Debian || tree.digest == "" || len(names) == 0 || len(names) > 100 {
		return "", errNginxInput
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !ValidSiteFilename(name) || seen[name] || !tree.layoutAllows(NginxEnabled+name) {
			return "", errNginxInput
		}
		seen[name] = true
	}
	script := "set -eu\n" + nginxGuard + nginxLinkFunctions + SystemCommand("guard_dir", strings.TrimSuffix(NginxEnabled, "/")) + "\n" + SystemCommand(append([]string{"set", "--"}, names...)...)
	script += "\nfor name do\n    'paths' \"$name\"\n    'guard_file' \"$file\"\n"
	change, undo := "'add_link'", "'remove_link'"
	check := "'absent'"
	if !enable {
		change, undo, check = "'remove_link'", "'add_link'", "'linked'"
	}
	script += "    " + check + "\ndone\n" + tree.guardSnapshot() + "changed=''\n"
	script += "rollback_links() {\n    for name in $changed; do 'paths' \"$name\"; " + undo + " || :; done\n}\ntrap rollback_links EXIT\n"
	script += "for name do 'paths' \"$name\"; " + change + "; changed=\"$name $changed\"; done\n" + NginxActionMustTest + "\ntrap - EXIT\n"
	if reload {
		script += nginxReload(systemd)
	}
	return script, nil
}

const NginxActionMustTest = "'nginx' '-t'"

const nginxLinkFunctions = `
paths() { file=/etc/nginx/sites-available/$1; link=/etc/nginx/sites-enabled/$1; }
absent() { [ ! -e "$link" ] && [ ! -L "$link" ]; }
linked() { [ -L "$link" ] && [ "$('readlink' '-f' "$link")" = "$file" ]; }
add_link() { 'absent' && 'ln' '-sT' "$file" "$link"; }
remove_link() { 'linked' && 'rm' "$link"; }
`

func (tree NginxTree) CreateCommand(name, text string, reload, systemd bool) (string, error) {
	file, link, err := tree.NewSite(name)
	if err != nil || tree.digest == "" || strings.TrimSpace(text) == "" || !NginxConfigSafe(text) {
		return "", errNginxInput
	}
	script := "set -eu\n" + tree.guardSnapshot() + nginxGuard + nginxLinkFunctions + "file=" + privilegeQuote(file) + "\nlink=" + privilegeQuote(link) + "\nexpected=" + privilegeQuote(systemDigest(text)) + `
'guard_dir' "${file%/*}"
if [ -n "$link" ]; then 'guard_dir' "${link%/*}"; 'absent'; fi
[ ! -e "$file" ] && [ ! -L "$file" ] || exit 1
tmp=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
created=''
enabled=''
cleanup() {
    if [ -n "$enabled" ]; then 'remove_link' || :; fi
    if [ -n "$created" ] && [ "$tmp" -ef "$file" ]; then 'rm' "$file" || :; fi
    'rm' '-f' "$tmp"
}
trap cleanup EXIT
'cat' >"$tmp"
actual=$('sha256sum' <"$tmp")
[ "${actual%% *}" = "$expected" ]
'chmod' '644' "$tmp"
'ln' '-T' "$tmp" "$file"
created=1
if [ -n "$link" ]; then 'add_link'; enabled=1; fi
'nginx' '-t'
created=''
enabled=''
`
	if reload {
		script += nginxReload(systemd)
	}
	return script, nil
}

func (inventory NginxInventory) CertbotIssueWithBackups(domains []string, email string, now time.Time) (string, []string, error) {
	return inventory.certbotWithBackups(CertbotIssueCommand(domains, email), now)
}

func (inventory NginxInventory) CertbotRenewWithBackups(dry bool, now time.Time) (string, []string, error) {
	return inventory.certbotWithBackups(CertbotRenewCommand(dry), now)
}

func (inventory NginxInventory) CertbotBackupAvailable(now time.Time) bool {
	_, _, err := inventory.CertbotRenewWithBackups(false, now)
	return err == nil
}

func (inventory NginxInventory) certbotWithBackups(command string, now time.Time) (string, []string, error) {
	if command == "" || inventory.Main != NginxMain || inventory.hasCredentials || len(inventory.Files) == 0 || len(inventory.Files) > 100 {
		return "", nil, errNginxInput
	}
	stamp := now.Format("20060102150405")
	var backups []string
	args := []string{"set", "--"}
	seen := map[string]bool{}
	for _, file := range inventory.Files {
		if name, ok := strings.CutPrefix(file, NginxEnabled); ok && inventory.Links[name] == "enabled" {
			file = NginxAvailable + name
		}
		if seen[file] {
			continue
		}
		backup := file + ".bak-" + stamp
		if !ValidNginxPath(file) || !strings.HasPrefix(file, "/etc/nginx/") || !inventory.AllowsBackup(backup) {
			return "", nil, errNginxInput
		}
		args, backups = append(args, file), append(backups, backup)
		seen[file] = true
	}
	script := "set -eu\n" + inventory.guardSnapshot() + nginxGuard + SystemCommand(args...) + "\nstamp=" + privilegeQuote(stamp) + `
for file do
    'guard_file' "$file"
    backup=$file.bak-$stamp
    [ ! -e "$backup" ] && [ ! -L "$backup" ] || exit 1
done
for file do
    tmp=$('mktemp' "${file%/*}/.finallyshell.XXXXXXXX")
    trap 'rm -f -- "$tmp"' EXIT
    'cp' '-p' "$file" "$tmp"
    'ln' '-T' "$tmp" "$file.bak-$stamp"
    'rm' "$tmp"
    trap - EXIT
    'printf' '%s\n' "$file.bak-$stamp"
done
` + command
	return script, backups, nil
}
