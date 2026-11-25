package app

import (
	"path/filepath"
	"strings"
)

func tunnelLANFirewallMatch(output, exe string, port int) string {
	fields := map[string]string{}
	matched := ""
	check := func() {
		program := strings.Trim(fields["program"], `"`)
		if fields["enabled"] != "yes" || fields["direction"] != "in" || program == "" || !strings.EqualFold(filepath.Clean(program), filepath.Clean(exe)) {
			return
		}
		if proto := fields["protocol"]; proto != "tcp" && proto != "6" && proto != "any" {
			return
		}
		ports := fields["localport"]
		if ports != "any" && !strings.ContainsFunc(ports, func(char rune) bool { return char >= '0' && char <= '9' }) {
			return
		}
		for _, value := range strings.Split(ports, ",") {
			if value == "any" || tunnelFirewallPort(strings.TrimSpace(value), port) {
				if fields["action"] == "block" || matched == "" && fields["action"] == "allow" {
					matched = fields["action"]
				}
				return
			}
		}
	}
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "rule name" {
			check()
			clear(fields)
		}
		fields[key] = strings.ToLower(strings.TrimSpace(value))
	}
	check()
	return matched
}
