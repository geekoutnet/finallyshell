package monitor

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

const SysConfTimeCommand = sysConfMarks + `mark 'clock'
'date' '+%s'
'date' '+%z'
mark 'timedatectl'
'timedatectl' 'show' 2>/dev/null
mark 'zone'
` + sysConfZoneRead + `mark 'rtc'
if [ -r '/sys/class/rtc/rtc0/date' ] && [ -r '/sys/class/rtc/rtc0/time' ]; then
  'printf' '%s %s\n' "$('cat' '/sys/class/rtc/rtc0/date')" "$('cat' '/sys/class/rtc/rtc0/time')"
fi
mark 'sync'
if 'chronyc' 'tracking' >/dev/null 2>&1; then
  'printf' '%s\n' 'chronyc'
elif 'systemctl' 'is-active' '--quiet' 'systemd-timesyncd.service'; then
  'printf' '%s\n' 'timesyncd'
elif command -v 'ntpdate' >/dev/null 2>&1; then
  'printf' '%s\n' 'ntpdate'
fi
mark 'end'
`

const SysConfZonesCommand = `'timedatectl' 'list-timezones' 2>/dev/null || (
  cd '/usr/share/zoneinfo' && 'find' '.' \( '-type' 'f' '-o' '-type' 'l' \) '-print'
)
`

type SysConfTime struct {
	Clock                              time.Time
	Zone, NTP, Synchronized, RTC, Sync string
	LocalRTC                           string
	Timedatectl, CanNTP                bool
}

func ParseSysConfTime(output string) SysConfTime {
	sections := sysInfoSections(output)
	values := sysConfKeyValues(sections["timedatectl"])
	result := SysConfTime{
		Zone: values["Timezone"], NTP: values["NTP"], Synchronized: values["NTPSynchronized"],
		RTC: values["RTCTimeUSec"], Sync: firstLine(sections["sync"]), LocalRTC: values["LocalRTC"],
		Timedatectl: values["Timezone"] != "", CanNTP: values["CanNTP"] == "yes",
	}
	if result.Zone == "" {
		result.Zone = firstLine(sections["zone"])
	}
	if result.RTC == "" {
		result.RTC = firstLine(sections["rtc"])
	}
	clock := lines(sections["clock"])
	if len(clock) < 1 {
		return result
	}
	if epoch, err := strconv.ParseInt(clock[0], 10, 64); err == nil {
		offset := 0
		if len(clock) > 1 && len(clock[1]) == 5 && strings.ContainsRune("+-", rune(clock[1][0])) && strings.Trim(clock[1][1:], "0123456789") == "" {
			zone := clock[1]
			offset = (sysConfDigits(zone[1:3])*60 + sysConfDigits(zone[3:])) * 60
			if zone[0] == '-' {
				offset = -offset
			}
		}
		result.Clock = time.Unix(epoch, 0).In(time.FixedZone(result.Zone, offset))
	}
	return result
}

func validSysConfZone(zone string) bool {
	if zone == "" || len(zone) > 128 || path.IsAbs(zone) || path.Clean(zone) != zone || strings.HasPrefix(zone, "posix/") || strings.HasPrefix(zone, "right/") {
		return false
	}
	for _, part := range strings.Split(zone, "/") {
		if part == ".." || part == "." || strings.HasPrefix(part, "-") || strings.Trim(part, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_+-") != "" {
			return false
		}
	}
	switch zone {
	case "localtime", "posixrules", "leapseconds", "tzdata", "zone1970", "zone", "iso3166", "Factory":
		return false
	}
	return true
}

func ParseSysConfZones(output string) []string {
	var result []string
	for _, value := range lines(output) {
		value = strings.TrimPrefix(value, "./")
		if validSysConfZone(value) {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func BuildSysConfZone(zone string, timedatectl bool) (SysConfChange, error) {
	if !validSysConfZone(zone) {
		return SysConfChange{}, ErrSysConfZone
	}
	if timedatectl {
		command := SystemCommand("timedatectl", "set-timezone", zone)
		return SysConfChange{Target: command, Script: command + "\n"}, nil
	}
	zoneFile := "/usr/share/zoneinfo/" + zone
	return SysConfChange{
		Target: "/etc/localtime → " + zoneFile + "\n/etc/timezone ← " + zone,
		Script: "set -eu\n" + SystemCommand("test", "-f", zoneFile) + "\n" +
			"'test' \"$(" + SystemCommand("head", "-c", "4", zoneFile) + ")\" '=' 'TZif'\n" + sysConfFileGuard +
			"file='/etc/timezone'\n" + sysConfNewTemp +
			SystemCommand("printf", "%s\\n", zone) + " > \"$tmp\"\n" +
			"'chmod' '0644' \"$tmp\"\n" +
			"dir=$('mktemp' '-d' '/etc/.finallyshell.XXXXXXXX')\n" +
			"cleanup() { 'rm' '-f' '--' \"$tmp\" \"$dir/localtime\"; 'rmdir' '--' \"$dir\"; }\n" +
			SystemCommand("ln", "-s", "--", zoneFile) + " \"$dir/localtime\"\n" +
			"'mv' '-fT' '--' \"$dir/localtime\" '/etc/localtime'\n'mv' '-fT' '--' \"$tmp\" \"$file\"\n",
	}, nil
}

func BuildSysConfNTP(enabled bool) SysConfChange {
	command := SystemCommand("timedatectl", "set-ntp", strconv.FormatBool(enabled))
	return SysConfChange{Target: command, Script: command + "\n"}
}

func BuildSysConfSync(method string) (SysConfChange, bool) {
	var args []string
	switch method {
	case "chronyc":
		args = []string{"chronyc", "makestep"}
	case "timesyncd":
		args = []string{"systemctl", "restart", "systemd-timesyncd.service"}
	case "ntpdate":
		args = []string{"ntpdate", "-u", "-t", "5", "pool.ntp.org"}
	default:
		return SysConfChange{}, false
	}
	command := SystemCommand(args...)
	return SysConfChange{Target: command, Script: command + "\n"}, true
}
