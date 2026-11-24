package monitor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

const NginxToolPath = "export PATH='/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/sbin:/usr/local/bin:/snap/bin'\n"

const NginxStatusCommand = NginxToolPath + `
export LC_ALL=C
if command -v 'nginx' >/dev/null 2>&1; then
    'printf' 'nginx\t%s\n' "$('nginx' '-v' 2>&1)"
fi
if [ -d '/run/systemd/system' ]; then
    'printf' 'systemd\n'
    'systemctl' 'show' '--property=LoadState,ActiveState,MainPID' 'nginx.service' 2>/dev/null
fi
if command -v 'apache2' >/dev/null 2>&1 || command -v 'httpd' >/dev/null 2>&1; then
    if [ -f '/etc/apache2/apache2.conf' ]; then 'printf' 'apache\t/etc/apache2/apache2.conf\n'
    else 'printf' 'apache\t/etc/httpd/conf/httpd.conf\n'; fi
fi
if command -v 'caddy' >/dev/null 2>&1; then 'printf' 'caddy\t/etc/caddy/Caddyfile\n'; fi
if command -v 'certbot' >/dev/null 2>&1; then 'printf' 'certbot\n'; fi
if command -v 'acme.sh' >/dev/null 2>&1 || [ -x "$HOME/.acme.sh/acme.sh" ]; then 'printf' 'acme\n'; fi
if command -v 'openssl' >/dev/null 2>&1; then 'printf' 'openssl\n'; fi
`

type NginxStatus struct {
	Version, State, Apache, Caddy                                            string
	PID                                                                      int
	Installed, Systemd, Unit, Certbot, ACME, OpenSSL, Custom, MasterVerified bool
}

func ParseNginxStatus(text string) NginxStatus {
	var status NginxStatus
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		key, value, _ := strings.Cut(line, "\t")
		switch key {
		case "nginx":
			status.Installed = true
			status.Version = strings.TrimPrefix(value, "nginx version: ")
		case "systemd":
			status.Systemd = true
		case "apache":
			status.Apache = value
		case "caddy":
			status.Caddy = value
		case "certbot":
			status.Certbot = true
		case "acme":
			status.ACME = true
		case "openssl":
			status.OpenSSL = true
		default:
			key, value, _ = strings.Cut(line, "=")
			switch key {
			case "LoadState":
				status.Unit = value == "loaded"
			case "ActiveState":
				status.State = value
			case "MainPID":
				status.PID, _ = strconv.Atoi(value)
			}
		}
	}
	return status
}

const nginxLayoutCommand = `
export LC_ALL=C
if [ -d '/etc/nginx/sites-available' ] && [ -d '/etc/nginx/sites-enabled' ]; then
    'printf' 'debian\n'
    for file in '/etc/nginx/sites-available/'*; do
        [ -f "$file" ] && [ ! -L "$file" ] || continue
        name=${file##*/}
        entry=/etc/nginx/sites-enabled/$name
        if [ -L "$entry" ] && [ "$('readlink' '-f' "$entry")" = "$file" ]; then state=enabled
        elif [ ! -e "$entry" ] && [ ! -L "$entry" ]; then state=disabled
        else state=other; fi
        'printf' '%s\t%s\n' "$state" "$name"
    done
fi
if [ -d '/etc/nginx/conf.d' ]; then 'printf' 'confd\n'; fi
`

func NginxMasterCommand(pid int) string {
	if pid > 0 {
		return "pid=" + privilegeQuote(strconv.Itoa(pid)) + "\n" + nginxMasterCheck
	}
	return `
for file in '/run/nginx.pid' '/var/run/nginx.pid' '/usr/local/nginx/logs/nginx.pid'; do
    if [ -r "$file" ]; then IFS= read -r pid <"$file"; break; fi
done
` + nginxMasterCheck
}

const nginxMasterCheck = `
case "${pid:-}" in ''|*[!0-9]*|0) exit 0;; esac
args=$('tr' '\000' ' ' <"/proc/$pid/cmdline" 2>/dev/null) || exit 0
case "$args" in
    'nginx: master process '*)
        'printf' '%s\n' "$pid"
        case "$args" in *' -c'*|*' -p'*) 'printf' 'custom\n';; esac;;
esac
`

type NginxInventory struct {
	NginxTree
	Links    map[string]string
	Inactive []NginxSite
}

func (inventory NginxInventory) Target(site NginxSite) (file, name, state string) {
	file = site.File
	if candidate, ok := strings.CutPrefix(file, NginxEnabled); ok {
		if inventory.Links[candidate] == "enabled" {
			return NginxAvailable + candidate, candidate, "enabled"
		}
		return file, "", "included"
	}
	if candidate, ok := strings.CutPrefix(file, NginxAvailable); ok && inventory.Debian {
		if inventory.Links[candidate] == "disabled" && !slices.Contains(inventory.Files, file) {
			return file, candidate, "disabled"
		}
		return file, "", "included"
	}
	return file, "", "included"
}

func (inventory NginxInventory) Certificates() []string {
	var files []string
	for _, site := range append(slices.Clone(inventory.Sites), inventory.Inactive...) {
		for _, file := range site.Certificates {
			files = append(files, inventory.CertificatePath(file))
		}
	}
	return files
}

func (inventory NginxInventory) CertificatePath(file string) string {
	if !strings.HasPrefix(file, "/") && !strings.Contains(file, ":") && inventory.Main != "" {
		return path.Join(path.Dir(inventory.Main), file)
	}
	return file
}

func ReadNginxInventory(ctx context.Context, run SystemRead) (NginxInventory, error) {
	var inventory NginxInventory
	text, stderr, err := run(ctx, nginxLF+NginxDumpCommand)
	if err != nil {
		return inventory, nginxOutputError(stderr, err)
	}
	inventory.NginxTree, err = ParseNginx(text)
	if err != nil {
		return inventory, err
	}
	layout, stderr, err := run(ctx, nginxLayoutCommand)
	if err != nil {
		return inventory, nginxOutputError(stderr, err)
	}
	inventory.Links = map[string]string{}
	for line := range strings.SplitSeq(layout, "\n") {
		state, name, _ := strings.Cut(strings.TrimSuffix(line, "\r"), "\t")
		switch state {
		case "debian":
			inventory.Debian = inventory.layoutAllows(NginxEnabled+"example.conf") || inventory.layoutAllows(NginxEnabled+"example")
		case "confd":
			inventory.ConfD = inventory.layoutAllows(NginxConfD + "example.conf")
		case "enabled", "disabled":
			if ValidSiteFilename(name) {
				inventory.Links[name] = state
			}
		}
	}
	var disabled []string
	for name, state := range inventory.Links {
		if state == "disabled" && inventory.Debian && !slices.Contains(inventory.Files, NginxAvailable+name) {
			disabled = append(disabled, name)
		}
	}
	slices.Sort(disabled)
	for names := range slices.Chunk(disabled, 80) {
		if err := ctx.Err(); err != nil {
			return inventory, err
		}
		if err := inventory.readInactive(ctx, run, names); err != nil {
			return inventory, err
		}
	}
	return inventory, nil
}

func (inventory *NginxInventory) readInactive(ctx context.Context, run SystemRead, names []string) error {
	args := []string{"set", "--"}
	for _, name := range names {
		args = append(args, NginxAvailable+name)
	}
	script := "set -eu\n" + nginxLF + nginxGuard + SystemCommand(args...) + `
for file do
    'printf' '# configuration file %s:\n' "$file"
    ('guard_public_config' "$file" && 'head' '-c' '262145' "$file") || :
    'printf' '\n'
done
`
	text, stderr, err := run(ctx, script)
	if err != nil {
		return nginxOutputError(stderr, err)
	}
	for chunk := range strings.SplitSeq(text, "# configuration file ") {
		file, content, _ := strings.Cut(chunk, "\n")
		file = strings.TrimSuffix(strings.TrimSuffix(file, "\r"), ":")
		name := strings.TrimPrefix(file, NginxAvailable)
		if !slices.Contains(names, name) {
			continue
		}
		fallback := NginxSite{File: file, Line: 1}
		if len(content) > SystemEditLimit+2 {
			inventory.Inactive = append(inventory.Inactive, fallback)
			continue
		}
		tokens, err := nginxTokens(content)
		if err != nil {
			inventory.Inactive = append(inventory.Inactive, fallback)
			continue
		}
		nodes, err := nginxNodes(&tokens, 0)
		if err != nil {
			inventory.Inactive = append(inventory.Inactive, fallback)
			continue
		}
		config := nginxConfig{tree: inventory.NginxTree}
		config.tree.Sites = nil
		config.http(NginxSite{}, config.expand(nodes, file, 0))
		if len(config.tree.Sites) == 0 {
			config.tree.Sites = []NginxSite{fallback}
		}
		inventory.Inactive = append(inventory.Inactive, config.tree.Sites...)
	}
	return nil
}

func nginxOutputError(stderr string, err error) error {
	if err != nil && strings.TrimSpace(stderr) != "" && !errors.Is(err, ErrPrivilegeDenied) && !errors.Is(err, ErrPrivilegeCancelled) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr))
	}
	return err
}
