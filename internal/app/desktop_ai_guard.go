package app

import (
	"regexp"
	"strings"
)

// The user's rules forbidding commands (AISettings.DenyCommands): regular
// expressions, one per line, which a command the model calls must match
// none of to run, before anything that would let it run (read-only, the
// conversation running everything, the user's approval).

// aiDenyLines are text's rules: its lines, trimmed, without blanks.
func aiDenyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// aiDenyRules compiles lines; bad are the lines that are not regular
// expressions, which forbid nothing.
func aiDenyRules(lines []string) (rules []*regexp.Regexp, bad []string) {
	for _, line := range lines {
		re, err := regexp.Compile(line)
		if err != nil {
			bad = append(bad, line)
			continue
		}
		rules = append(rules, re)
	}
	return rules, bad
}

// aiDenied is the rule that forbids command, "" for none.
func aiDenied(rules []*regexp.Regexp, command string) string {
	for _, re := range rules {
		if re.MatchString(command) {
			return re.String()
		}
	}
	return ""
}
