package monitor

import (
	"regexp"
	"slices"
	"strings"
)

const PackageInfoScript = `export LC_ALL=C
for tool in 'apt' 'dnf' 'yum' 'zypper' 'apk' 'pacman'; do
  if command -v "$tool" >/dev/null 2>&1; then printf '%s\n' "$tool"; break; fi
done
[ -n "$tool" ] && command -v "$tool" >/dev/null 2>&1 || printf '\n'
release=$('sed' '-n' 's/^PRETTY_NAME=//p' '/etc/os-release' 2>/dev/null | 'head' '-n' '1')
printf '%s\n' "$release"
'uname' '-r'`

type PackageUpdate struct {
	Name, Current, New, Repository string
	Security                       bool
}

func PackageManager(value string) bool {
	return slices.Contains([]string{"apt", "dnf", "yum", "zypper", "apk", "pacman"}, value)
}

func PackageCanUpgrade(manager string) bool {
	return manager == "apt" || manager == "dnf" || manager == "yum"
}

func PackageListScript(manager string) string {
	switch manager {
	case "apt":
		return "LC_ALL=C " + opsCommand("apt", "list", "--upgradable")
	case "dnf", "yum":
		return "LC_ALL=C " + opsCommand(manager, "-q", "-C", "check-update") + `; code=$?; [ "$code" -eq 100 ] || exit "$code"`
	case "zypper":
		return "LC_ALL=C " + opsCommand("zypper", "--non-interactive", "--no-refresh", "list-updates")
	case "apk":
		return "LC_ALL=C " + opsCommand("apk", "version", "-l", "<")
	case "pacman":
		return "LC_ALL=C " + opsCommand("pacman", "-Qu")
	}
	return ""
}

func PackageDetailsScript(manager string) string {
	if manager == "dnf" || manager == "yum" {
		return "export LC_ALL=C\n" + opsCommand("rpm", "-qa", "--qf", `%{NAME}.%{ARCH}\t%{EPOCHNUM}:%{VERSION}-%{RELEASE}\n`) + "\nprintf '%s\\n' '---security---'\n" + opsCommand(manager, "-q", "-C", "updateinfo", "list", "--updates", "--security")
	}
	return ""
}

var packageName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+:-]{0,199}$`)
var aptVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+:~_-]{0,239}$`)

func ParsePackageUpdates(manager, output, details string) []PackageUpdate {
	installed := make(map[string]string)
	var security []string
	for _, line := range strings.Split(details, "\n") {
		if name, version, ok := strings.Cut(line, "\t"); ok {
			installed[name] = strings.TrimPrefix(strings.TrimSpace(version), "0:")
		} else if fields := strings.Fields(line); len(fields) >= 3 && strings.Contains(strings.ToLower(fields[1]), "sec") {
			security = append(security, fields[2:]...)
		}
	}
	var rows []PackageUpdate
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		var update PackageUpdate
		switch manager {
		case "apt":
			if len(fields) < 4 {
				continue
			}
			name, repo, ok := strings.Cut(fields[0], "/")
			_, current, upgradable := strings.Cut(line, "[upgradable from: ")
			if !ok || !upgradable {
				continue
			}
			update = PackageUpdate{Name: name, Current: strings.TrimSuffix(strings.TrimSpace(current), "]"), New: fields[1], Repository: repo}
			if fields[2] != "all" {
				update.Name += ":" + fields[2]
			}
			for _, origin := range strings.Split(repo, ",") {
				update.Security = update.Security || strings.HasSuffix(origin, "-security") || strings.HasSuffix(origin, "/updates")
			}
		case "dnf", "yum":
			if strings.HasPrefix(strings.TrimSpace(line), "Obsoleting Packages") {
				return rows
			}
			if len(fields) != 3 || !strings.Contains(fields[0], ".") || fields[1] == "metadata" {
				continue
			}
			update = PackageUpdate{Name: fields[0], New: fields[1], Repository: fields[2], Current: installed[fields[0]]}
			archIndex := strings.LastIndexByte(update.Name, '.')
			name, arch := update.Name[:archIndex], update.Name[archIndex+1:]
			for _, nevra := range security {
				if strings.HasPrefix(nevra, name+"-") && strings.HasSuffix(nevra, "."+arch) {
					version := strings.TrimSuffix(strings.TrimPrefix(nevra, name+"-"), "."+arch)
					if strings.TrimPrefix(version, "0:") == strings.TrimPrefix(update.New, "0:") || securityVersionOfPackage(version) {
						update.Security = true
					}
				}
			}
		case "zypper":
			columns := strings.Split(line, "|")
			if len(columns) != 7 || strings.TrimSpace(columns[0]) != "v" {
				continue
			}
			update = PackageUpdate{Name: strings.TrimSpace(columns[2]), Repository: strings.TrimSpace(columns[1]), Current: strings.TrimSpace(columns[3]), New: strings.TrimSpace(columns[4])}
		case "apk":
			if len(fields) != 3 || fields[1] != "<" {
				continue
			}
			matches := apkVersion.FindStringSubmatch(fields[0])
			if len(matches) != 3 {
				continue
			}
			update = PackageUpdate{Name: matches[1], Current: matches[2], New: fields[2]}
		case "pacman":
			if len(fields) != 4 || fields[2] != "->" {
				continue
			}
			update = PackageUpdate{Name: fields[0], Current: fields[1], New: fields[3]}
		}
		if packageName.MatchString(update.Name) {
			rows = append(rows, update)
		}
	}
	return rows
}

var apkVersion = regexp.MustCompile(`^(.+?)-([0-9].*-r[0-9]+)$`)

var rpmAdvisoryVersion = regexp.MustCompile(`^(?:[0-9]+:)?[0-9][^-]*-[^-]+$`)

func securityVersionOfPackage(version string) bool { return rpmAdvisoryVersion.MatchString(version) }

func PackageRefreshScript(manager string) (string, error) {
	switch manager {
	case "apt":
		return "LC_ALL=C DEBIAN_FRONTEND=noninteractive " + opsCommand("apt-get", "update"), nil
	case "dnf", "yum":
		return "LC_ALL=C " + opsCommand(manager, "check-update") + `; code=$?; [ "$code" -eq 100 ] || exit "$code"`, nil
	}
	return "", ErrOpsInvalid
}

func PackageUpgradeScript(manager, scope string, names []string) (string, error) {
	if !PackageCanUpgrade(manager) || len(names) > 4096 || scope != "all" && scope != "security" && scope != "selected" {
		return "", ErrOpsInvalid
	}
	if (scope == "selected" || manager == "apt" && scope == "security") != (len(names) != 0) {
		return "", ErrOpsInvalid
	}
	for _, name := range names {
		pkg, version, pinned := strings.Cut(name, "=")
		if !packageName.MatchString(pkg) || strings.HasSuffix(pkg, "+") || strings.HasSuffix(pkg, "-") || pinned && (manager != "apt" || !aptVersion.MatchString(version)) || manager == "apt" && scope == "security" && !pinned {
			return "", ErrOpsInvalid
		}
	}
	if manager == "apt" {
		prefix := "LC_ALL=C DEBIAN_FRONTEND=noninteractive "
		if scope == "all" {
			return prefix + opsCommand("apt-get", "upgrade", "-y", "--no-remove"), nil
		}
		return prefix + opsCommand("apt-get", append([]string{"install", "--only-upgrade", "--no-remove", "-y", "--"}, names...)...), nil
	}
	args := []string{"upgrade", "-y"}
	if scope == "security" {
		args = append(args, "--security")
	}
	if len(names) > 0 {
		args = append(append(args, "--"), names...)
	}
	return "LC_ALL=C " + opsCommand(manager, args...), nil
}

func PackageRebootCheckScript(manager string) string {
	if manager == "apt" {
		return `if [ -f '/var/run/reboot-required' ]; then printf '%s\n' 'required'; else printf '%s\n' 'clear'; fi`
	}
	if manager == "dnf" || manager == "yum" {
		return `if command -v 'needs-restarting' >/dev/null 2>&1; then
  'needs-restarting' '-r' >/dev/null 2>&1
  case "$?" in 0) printf '%s\n' 'clear';; 1) printf '%s\n' 'required';; esac
fi`
	}
	return ""
}

func ParseRebootRequired(output string) bool { return strings.TrimSpace(output) == "required" }

const PackageRebootScript = `'sync' && 'shutdown' '-r' '+1'`

func PackageLastUpdateScript(manager string) string {
	switch manager {
	case "apt":
		return `'grep' '^End-Date:' '/var/log/apt/history.log' 2>/dev/null | 'tail' '-n' '1' | 'cut' '-d:' '-f2-'`
	case "dnf", "yum":
		return `'rpm' '-qa' '--last' | 'head' '-n' '1' | 'cut' '-d ' '-f2-'`
	}
	return ""
}
