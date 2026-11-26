package monitor

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// OpenWrtService is an init script of /etc/init.d, and what procd runs
// of it.
type OpenWrtService struct {
	Name    string
	Enabled bool // started at boot: it has a link in /etc/rc.d
	// Managed tells that procd knows it: Running and PIDs say whether it
	// runs. A one-shot script, as boot or sysctl, is not.
	Managed bool
	Running bool
	PIDs    []int
}

// OpenWrtServicesScript lists the init scripts, those started at boot,
// and what procd runs.
const OpenWrtServicesScript = openWrtMarks + `m init; for f in /etc/init.d/*; do [ -x "$f" ] && echo "${f##*/}"; done
m rcd; ls /etc/rc.d 2>/dev/null
m procd; ubus call service list 2>/dev/null
m end
`

// ParseOpenWrtServices reads the output of OpenWrtServicesScript, by
// name.
func ParseOpenWrtServices(out string) []OpenWrtService {
	sec := procSections(out)
	enabled := map[string]bool{}
	for name := range strings.FieldsSeq(sec["rcd"]) {
		// S19dnsmasq: started at boot; K… only stops it at shutdown.
		if len(name) > 1 && name[0] == 'S' {
			enabled[strings.TrimLeft(name[1:], "0123456789")] = true
		}
	}
	var procd map[string]struct {
		Instances map[string]struct {
			Running bool
			PID     int
		}
	}
	json.Unmarshal([]byte(sec["procd"]), &procd)
	var services []OpenWrtService
	for name := range strings.FieldsSeq(sec["init"]) {
		svc := OpenWrtService{Name: name, Enabled: enabled[name]}
		if p, ok := procd[name]; ok {
			svc.Managed = true
			for _, inst := range p.Instances {
				if inst.Running {
					svc.Running = true
					if inst.PID > 0 {
						svc.PIDs = append(svc.PIDs, inst.PID)
					}
				}
			}
			slices.Sort(svc.PIDs)
		}
		services = append(services, svc)
	}
	slices.SortFunc(services, func(a, b OpenWrtService) int { return strings.Compare(a.Name, b.Name) })
	return services
}

// OpenWrtServiceActions are what an init script does, in the order the
// menu has them.
var OpenWrtServiceActions = []string{"start", "stop", "restart", "reload", "enable", "disable"}

// OpenWrtServiceCommand runs the init script of name with action.
func OpenWrtServiceCommand(name, action string) (string, error) {
	if !openWrtNameRE.MatchString(name) || !slices.Contains(OpenWrtServiceActions, action) {
		return "", ErrOpenWrtInvalid
	}
	script := privilegeQuote("/etc/init.d/" + name)
	return "[ -x " + script + " ] || exit 1\n" + script + " " + action + "\n", nil
}

// OpenWrtServiceCutsSSH tells whether stopping or restarting name may
// end the SSH connection the app is on.
func OpenWrtServiceCutsSSH(name string) bool {
	return slices.Contains([]string{"network", "dropbear", "sshd", "firewall"}, name)
}

// OpenWrtPackage is an installed package.
type OpenWrtPackage struct {
	Name, Version string
	Upgrade       string // the version it would upgrade to, "" for none
}

// OpenWrtPackagesScript lists the installed packages, and those there is
// an upgrade of in the package lists: opkg's, or apk's on 25.12 and
// later. "lists" tells whether the lists are there: opkg keeps them in
// /var, so a reboot drops them, and apk warns of each missing one.
const OpenWrtPackagesScript = openWrtMarks + `if command -v opkg >/dev/null 2>&1; then
  m pm; echo opkg
  m installed; opkg list-installed 2>/dev/null
  m upgradable; opkg list-upgradable 2>/dev/null
  m lists; ls "$(sed -n 's/^lists_dir [^ ]* //p' /etc/opkg.conf 2>/dev/null | head -n 1)" 2>/dev/null | head -n 1
elif command -v apk >/dev/null 2>&1; then
  m pm; echo apk
  m installed; apk query --fields name,version --installed '*' 2>/dev/null || { m apklist; apk list --installed 2>/dev/null; }
  m upgradable; apk list --upgradable 2>/dev/null
  m lists; apk list --upgradable 2>&1 >/dev/null | grep -q 'No such file' || echo yes
fi
m end
`

// ParseOpenWrtPackages reads the output of OpenWrtPackagesScript: the
// package manager, the packages by name, and whether the package lists
// were downloaded.
func ParseOpenWrtPackages(out string) (pm string, pkgs []OpenWrtPackage, lists bool) {
	sec := procSections(out)
	pm = strings.TrimSpace(sec["pm"])
	lists = strings.TrimSpace(sec["lists"]) != ""
	upgrades := map[string]string{}
	switch pm {
	case "opkg":
		// name - version
		for line := range strings.SplitSeq(sec["installed"], "\n") {
			if f := strings.SplitN(line, " - ", 3); len(f) >= 2 {
				pkgs = append(pkgs, OpenWrtPackage{Name: strings.TrimSpace(f[0]), Version: strings.TrimSpace(f[1])})
			}
		}
		// name - current - new
		for line := range strings.SplitSeq(sec["upgradable"], "\n") {
			if f := strings.Split(line, " - "); len(f) == 3 {
				upgrades[strings.TrimSpace(f[0])] = strings.TrimSpace(f[2])
			}
		}
	case "apk":
		if list, ok := sec["apklist"]; ok {
			for line := range strings.SplitSeq(list, "\n") {
				if name, version, ok := apkNameVersion(line); ok {
					pkgs = append(pkgs, OpenWrtPackage{Name: name, Version: version})
				}
			}
		} else {
			var cur OpenWrtPackage
			for line := range strings.SplitSeq(sec["installed"]+"\n", "\n") {
				switch k, v, _ := strings.Cut(strings.TrimSpace(line), ": "); k {
				case "Name":
					cur.Name = v
				case "Version":
					cur.Version = v
				case "":
					if cur.Name != "" {
						pkgs = append(pkgs, cur)
					}
					cur = OpenWrtPackage{}
				}
			}
		}
		// name-newversion arch {origin} (license) [upgradable from: name-oldversion]
		for line := range strings.SplitSeq(sec["upgradable"], "\n") {
			if name, version, ok := apkNameVersion(line); ok && strings.Contains(line, "[upgradable from:") {
				upgrades[name] = version
			}
		}
	}
	for i := range pkgs {
		pkgs[i].Upgrade = upgrades[pkgs[i].Name]
	}
	slices.SortFunc(pkgs, func(a, b OpenWrtPackage) int { return strings.Compare(a.Name, b.Name) })
	pkgs = slices.CompactFunc(pkgs, func(a, b OpenWrtPackage) bool { return a.Name == b.Name })
	return pm, pkgs, lists
}

// apkNameVersion splits the first field of an apk list line, as
// apk-mbedtls-3.0.5-r2, at the first hyphen before a digit: names have
// none, versions start with one.
func apkNameVersion(line string) (name, version string, ok bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(line), " ")
	for i := 1; i < len(first)-1; i++ {
		if first[i] == '-' && first[i+1] >= '0' && first[i+1] <= '9' {
			return first[:i], first[i+1:], true
		}
	}
	return "", "", false
}

// OpenWrtPackageCommand runs the package manager pm: "update" downloads
// the lists, "install", "remove" and "upgrade" act on the packages.
func OpenWrtPackageCommand(pm, action string, packages ...string) (string, error) {
	for _, p := range packages {
		if !ValidOpenWrtPackage(p) {
			return "", ErrOpenWrtInvalid
		}
	}
	if action != "update" && len(packages) == 0 {
		return "", ErrOpenWrtInvalid
	}
	verbs := map[string]map[string]string{
		"opkg": {"update": "opkg update", "install": "opkg install", "remove": "opkg remove", "upgrade": "opkg upgrade"},
		"apk":  {"update": "apk update", "install": "apk add", "remove": "apk del", "upgrade": "apk add --upgrade"},
	}
	verb, ok := verbs[pm][action]
	if !ok {
		return "", ErrOpenWrtInvalid
	}
	for _, p := range packages {
		verb += " " + privilegeQuote(p)
	}
	return verb + "\n", nil
}

// OpenWrtSearchCommand lists the packages there are whose names hold
// term, from the downloaded lists.
func OpenWrtSearchCommand(pm, term string) (string, error) {
	if !openWrtPackageRE.MatchString(term) {
		return "", ErrOpenWrtInvalid
	}
	switch pm {
	case "opkg":
		return "opkg list " + privilegeQuote("*"+term+"*") + " 2>/dev/null | head -n 200\n", nil
	case "apk":
		return "apk search " + privilegeQuote(term) + " 2>/dev/null | head -n 200\n", nil
	}
	return "", ErrOpenWrtInvalid
}

// ParseOpenWrtSearch reads the output of OpenWrtSearchCommand: the
// names and versions found, with opkg's descriptions.
func ParseOpenWrtSearch(pm, out string) []OpenWrtPackage {
	var found []OpenWrtPackage
	for line := range strings.SplitSeq(out, "\n") {
		switch pm {
		case "opkg": // name - version - description
			if f := strings.SplitN(line, " - ", 3); len(f) >= 2 {
				found = append(found, OpenWrtPackage{Name: strings.TrimSpace(f[0]), Version: strings.TrimSpace(f[1])})
			}
		case "apk":
			if name, version, ok := apkNameVersion(line); ok {
				found = append(found, OpenWrtPackage{Name: name, Version: version})
			}
		}
	}
	return found
}

// OpenWrtSFTPPackage is the package with the SFTP server OpenWrt lacks.
const OpenWrtSFTPPackage = "openssh-sftp-server"

// OpenWrtInstallSFTPCommand downloads the package lists and installs the
// SFTP server, with whichever package manager there is.
const OpenWrtInstallSFTPCommand = `set -e
if command -v opkg >/dev/null 2>&1; then
  opkg update
  opkg install ` + OpenWrtSFTPPackage + `
elif command -v apk >/dev/null 2>&1; then
  apk update
  apk add ` + OpenWrtSFTPPackage + `
else
  echo 'No package manager (opkg or apk) on this system.' >&2
  exit 1
fi
`

// OpenWrtLogSources are the logs the Logs section reads.
const (
	OpenWrtLogSystem = iota // logread: procd's log, or BusyBox syslogd's
	OpenWrtLogKernel        // dmesg
)

// OpenWrtLogCommand reads the last lines lines of the log source.
func OpenWrtLogCommand(source, lines int) string {
	n := strconv.Itoa(max(1, min(lines, 5000)))
	if source == OpenWrtLogKernel {
		return "dmesg 2>&1 | tail -n " + n + "\n"
	}
	// ubox's logread has -l; BusyBox's has not.
	return "logread -l " + n + " 2>/dev/null || logread 2>&1 | tail -n " + n + "\n"
}

// OpenWrtRebootCommand reboots the system, after the session's answer
// is sent.
const OpenWrtRebootCommand = "(sleep 1; reboot) >/dev/null 2>&1 </dev/null &\necho rebooting\n"

// OpenWrtBackupCommand writes a backup of the configuration, as
// sysupgrade -b and LuCI make it, to stdout: a .tar.gz.
const OpenWrtBackupCommand = "sysupgrade -b - 2>/dev/null || { f=/tmp/finallyshell-backup.$$.tar.gz; sysupgrade -b \"$f\" >/dev/null && cat \"$f\"; r=$?; rm -f \"$f\"; exit $r; }\n"
