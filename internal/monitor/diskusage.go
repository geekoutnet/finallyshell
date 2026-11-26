package monitor

import (
	"cmp"
	"context"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

var ErrOpsInvalid = errors.New("invalid or protected target")
var ErrDiskTimeout = errors.New("disk scan timed out")

func opsCommand(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, privilegeQuote(name))
	for _, arg := range args {
		parts = append(parts, privilegeQuote(arg))
	}
	return strings.Join(parts, " ")
}

func opsValidText(value string) bool { return !strings.ContainsFunc(value, unicode.IsControl) }

func RunOpsStream(ctx context.Context, client *ssh.Client, script string, progress func(string)) (string, string, error) {
	return runScript(ctx, client, script, progress)
}

func opsTimed(seconds int, script string) string {
	return "if command -v 'timeout' >/dev/null 2>&1; then\n  exec " + opsCommand("timeout", strconv.Itoa(seconds), "sh", "-c", script) + "\nfi\n" + script
}

func DiskScanError(err error) error {
	var exit interface{ ExitStatus() int }
	if errors.As(err, &exit) && exit.ExitStatus() == 124 {
		return ErrDiskTimeout
	}
	return err
}

const DiskMountScript = `LC_ALL=C 'df' '-kP'`
const DiskFreeRootScript = `if [ "$('id' '-u')" = '0' ] || 'sudo' '-n' '-u' 'root' '--' 'true' >/dev/null 2>&1; then printf '%s\n' 'yes'; fi`

func ParseDiskMounts(output string) []Disk { return parseDisks(output) }

type DiskEntry struct {
	Path     string
	Bytes    int64
	Modified time.Time
	File     bool
}

type DiskScan struct {
	Entries []DiskEntry
	Total   int64
}

func ValidDiskPath(target string) bool {
	return path.IsAbs(target) && path.Clean(target) == target && opsValidText(target)
}

func DiskScanScript(target string, largest bool) (string, error) {
	if !ValidDiskPath(target) {
		return "", ErrOpsInvalid
	}
	arg := privilegeQuote(target)
	// BusyBox without realpath, as OpenWrt's, has readlink -f.
	prefix := "export LC_ALL=C\n[ -d " + arg + " ] && [ \"$('realpath' " + arg + " 2>/dev/null || 'readlink' '-f' " + arg + ")\" = " + arg + " ] || exit 1\n"
	if largest {
		return opsTimed(85, prefix+`[ "$( 'find' '/dev/null' '-printf' 'yes' 2>/dev/null )" = 'yes' ] && 'sort' '-z' </dev/null >/dev/null 2>&1
if [ "$?" -eq 0 ]; then
  'find' `+arg+` '-xdev' '-type' 'f' '-size' '+50M' '-printf' '%s\t%T@\t%p\0' | 'sort' '-z' '-nr' | 'head' '-z' '-n' '100'
else
  'find' `+arg+` '-xdev' '-type' 'f' '-size' '+52428800c' '-exec' 'sh' '-c' '
    for file do
      case "$file" in *[[:cntrl:]]*) continue;; esac
      meta=$(stat -c "%s %Y" -- "$file") || continue
      printf "%s\t%s\t%s\n" "${meta%% *}" "${meta#* }" "$file"
    done' 'sh' '{}' '+' | 'sort' '-nr' | 'head' '-n' '100'
fi`), nil
	}
	return opsTimed(85, prefix+`if 'du' '--max-depth=0' '--time' '--time-style=+%s' '--null' '/dev/null' >/dev/null 2>&1 && [ "$( 'find' '/dev/null' '-printf' 'yes' 2>/dev/null )" = 'yes' ]; then
  'find' `+arg+` '-xdev' '-maxdepth' '1' '-type' 'f' '-printf' 'F\t%p\0'
  'du' '-x' '-a' '-k' '--max-depth=1' '--time' '--time-style=+%s' '--null' `+arg+`
else
  'find' `+arg+` '-xdev' '-maxdepth' '1' '-type' 'f' '-exec' 'sh' '-c' '
    for file do case "$file" in *[[:cntrl:]]*) continue;; esac; printf "F\t%s\n" "$file"; done' 'sh' '{}' '+'
  'du' '-x' '-a' '-k' '-d' '1' `+arg+`
fi`), nil
}

func ParseDiskScan(output, target string, files bool) DiskScan {
	separator := "\x00"
	if !strings.Contains(output, separator) {
		separator = "\n"
	}
	result := DiskScan{}
	seen := make(map[string]bool)
	regular := make(map[string]bool)
	for _, line := range strings.Split(output, separator) {
		number, rest, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "\t")
		if number == "F" && ok {
			regular[rest] = true
			continue
		}
		size, err := strconv.ParseInt(number, 10, 64)
		if !ok || err != nil || size < 0 {
			continue
		}
		var modified time.Time
		if stamp, name, ok := strings.Cut(rest, "\t"); ok && !strings.HasPrefix(stamp, "/") {
			if seconds, err := strconv.ParseFloat(stamp, 64); err == nil && seconds > 0 {
				nanos := int64((seconds - float64(int64(seconds))) * 1e9)
				modified = time.Unix(int64(seconds), nanos)
			}
			rest = name
		}
		if !files {
			if size > (1<<63-1)/1024 {
				continue
			}
			size *= 1024
			if rest == target {
				result.Total = size
				continue
			}
		}
		if !path.IsAbs(rest) || path.Clean(rest) != rest || !strings.HasPrefix(rest, strings.TrimSuffix(target, "/")+"/") || !files && path.Dir(rest) != target || seen[rest] {
			continue
		}
		seen[rest] = true
		result.Entries = append(result.Entries, DiskEntry{Path: rest, Bytes: size, Modified: modified, File: files || regular[rest]})
	}
	if files {
		slices.SortStableFunc(result.Entries, func(left, right DiskEntry) int {
			return cmp.Compare(right.Bytes, left.Bytes)
		})
		result.Entries = result.Entries[:min(100, len(result.Entries))]
	}
	return result
}

func DiskMutationAllowed(target string) bool {
	if !ValidDiskPath(target) || target == "/" || path.Dir(target) == "/" || path.Dir(target) == "/home" {
		return false
	}
	first := strings.SplitN(target[1:], "/", 2)[0]
	if strings.HasPrefix(first, "lib") || slices.Contains([]string{"etc", "usr", "bin", "sbin", "boot", "proc", "sys", "dev", "run", "snap"}, first) {
		return false
	}
	for _, component := range strings.Split(target, "/") {
		if slices.Contains([]string{".ssh", ".gnupg", "authorized_keys"}, component) {
			return false
		}
	}
	for _, protected := range []string{"/var/lib", "/var/log", "/var/cache", "/var/spool", "/var/tmp", "/root/.ssh"} {
		if target == protected || strings.HasPrefix(protected, target+"/") || protected != "/var/log" && protected != "/var/tmp" && strings.HasPrefix(target, protected+"/") {
			return false
		}
	}
	return true
}

const diskSafeTarget = `safe_target() {
  [ -e "$1" ] && [ "$('realpath' "$1" 2>/dev/null || 'readlink' '-f' "$1")" = "$1" ] || return 1
  [ ! -L "$1" ] || return 1
  if [ "$('id' '-u')" = '0' ]; then
    parent=${1%/*}
    while [ -n "$parent" ]; do
      permission=$('stat' '-c' '%u:%a' '--' "$parent") || return 1
      case "$permission" in 0:*[2367]?|0:*[2367]) return 1;; 0:*) :;; *) return 1;; esac
      parent=${parent%/*}
    done
  fi
  [ -r '/proc/self/mountinfo' ] || return 1
  while IFS=' ' read -r id parent device root mount rest; do
    mount=$(printf '%b' "$mount"; printf '.')
    mount=${mount%.}
    case "$mount" in "$1"|"$1"/*) return 1;; esac
  done < '/proc/self/mountinfo'
  return 0
}`

func DiskDeleteScript(targets []string) (string, error) {
	if len(targets) == 0 || len(targets) > 100 {
		return "", ErrOpsInvalid
	}
	args := make([]string, 0, len(targets))
	for _, target := range targets {
		if !DiskMutationAllowed(target) || strings.HasPrefix(target, "/var/log/") {
			return "", ErrOpsInvalid
		}
		args = append(args, privilegeQuote(target))
	}
	return "set -e\n" + diskSafeTarget + `
set -- ` + strings.Join(args, " ") + `
for target do safe_target "$target" || { printf '%s\n' 'Unsafe path, writable parent or mounted filesystem' >&2; exit 1; }; done
for target do
  if [ -d "$target" ]; then
    'rm' '-r' '--one-file-system' '--' "$target"
  elif [ -f "$target" ]; then
    'rm' '--' "$target"
  else
    exit 1
  fi
done`, nil
}

func DiskTruncateScript(target string) (string, error) {
	if !DiskMutationAllowed(target) || !strings.HasSuffix(target, ".log") {
		return "", ErrOpsInvalid
	}
	return "set -e\n" + diskSafeTarget + `
file=` + privilegeQuote(target) + `
safe_target "$file" && [ -f "$file" ] && [ "$('stat' '-c' '%h' '--' "$file")" = '1' ] || exit 1
stamp=$('date' '+%Y%m%d%H%M%S')
backup="$file.bak-$stamp"
(umask '077'; set -C; : > "$backup") || exit 1
'cp' '--' "$file" "$backup"
safe_target "$file" && [ -f "$file" ] && [ "$('stat' '-c' '%h' '--' "$file")" = '1' ] || exit 1
: > "$file"`, nil
}

const DiskCleanupEstimatesScript = `export LC_ALL=C
for utility in 'apt-get' 'dnf' 'journalctl'; do
  if command -v "$utility" >/dev/null 2>&1; then printf '%s\n' "$utility"; fi
done
'du' '-sk' '/var/cache/apt/archives' '/var/cache/dnf' '/var/log/journal' '/run/log/journal' 2>/dev/null || :
for file in /var/log/*.gz /var/log/*.[0-9]; do
  [ -f "$file" ] && [ ! -L "$file" ] && 'du' '-k' "--" "$file"
done
exit 0`

func ParseDiskCleanupEstimates(output string) (map[string]int64, []string) {
	result := make(map[string]int64)
	var rotated []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "apt-get" || line == "dnf" || line == "journalctl" {
			result[line] = -1
			continue
		}
		number, target, _ := strings.Cut(line, "\t")
		size, err := strconv.ParseInt(number, 10, 64)
		if err != nil || size < 0 || size > (1<<63-1)/1024 {
			continue
		}
		var key string
		switch target {
		case "/var/cache/apt/archives":
			key = "apt-get"
		case "/var/cache/dnf":
			key = "dnf"
		case "/var/log/journal", "/run/log/journal":
			key = "journalctl"
		default:
			if !diskRotatedPath(target) || slices.Contains(rotated, target) {
				continue
			}
			rotated = append(rotated, target)
			key = "rotated"
		}
		result[key] = max(0, result[key]) + size*1024
	}
	return result, rotated
}

func diskRotatedPath(target string) bool {
	if !ValidDiskPath(target) || path.Dir(target) != "/var/log" {
		return false
	}
	name := path.Base(target)
	last := name[len(name)-1]
	return strings.HasSuffix(name, ".gz") || len(name) > 2 && name[len(name)-2] == '.' && last >= '0' && last <= '9'
}

func DiskRotatedScript(targets []string) (string, error) {
	if len(targets) == 0 || len(targets) > 256 {
		return "", ErrOpsInvalid
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		if !diskRotatedPath(target) {
			return "", ErrOpsInvalid
		}
		paths = append(paths, privilegeQuote(target))
	}
	return "set -e\n" + diskSafeTarget + `
set -- ` + strings.Join(paths, " ") + `
for file do
  safe_target "$file" && [ -f "$file" ] || exit 1
done
for file do 'rm' '--' "$file"; done`, nil
}

func DiskCleanupScript(kind string, size int) (string, error) {
	switch kind {
	case "apt-get":
		return opsCommand("apt-get", "clean"), nil
	case "dnf":
		return opsCommand("dnf", "clean", "all"), nil
	case "journalctl":
		if size < 16 || size > 65536 {
			break
		}
		return opsCommand("journalctl", "--vacuum-size="+strconv.Itoa(size)+"M"), nil
	}
	return "", ErrOpsInvalid
}
