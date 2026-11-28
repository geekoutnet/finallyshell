package monitor

import (
	"strings"
	"testing"
)

func TestParsePort80Upstream(t *testing.T) {
	for out, want := range map[string]string{
		`LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=1,fd=6))`: "local",
		"LISTEN 0 4096 *:80 *:*":                                            "local",
		"LISTEN 0 511 [::]:80 [::]:*":                                       "local",
		"LISTEN 0 511 10.0.0.5:80 0.0.0.0:*":                                "10.0.0.5",
		"LISTEN 0 511 127.0.0.1:80 0.0.0.0:*":                               "none",
		"LISTEN 0 511 [::1]:80 [::]:*\nLISTEN 0 511 127.0.0.1:80 0.0.0.0:*": "none",
		"LISTEN 0 511 127.0.0.1:80 0.0.0.0:*\nLISTEN 0 511 10.0.0.5:80 *:*": "10.0.0.5",
		"": "",
		"tcp 0 0 0.0.0.0:80 0.0.0.0:* LISTEN 812/nginx": "local",
		"tcp6 0 0 :::80 :::* LISTEN 812/nginx":          "local",
	} {
		if got := ParsePort80Upstream(out); got != want {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
}

// The split redirects connections to the server's own addresses alone, and
// lets the proxy's port in for redirected ones alone; the stop command
// deletes exactly the rules added.
func TestACMESplitRules(t *testing.T) {
	for _, up := range []string{"local", "none", "10.0.0.5", "::1"} {
		if _, err := ACMESplitStartCommand(up); err != nil {
			t.Errorf("start with %q: %v", up, err)
		}
	}
	for _, bad := range []string{"10.0.0.5; rm -rf /", "", "localhost"} {
		if _, err := ACMESplitStartCommand(bad); err == nil {
			t.Errorf("upstream %q passed", bad)
		}
	}
	rules, err := ACMESplitRulesCommand(40000)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := ACMESplitStopCommand(4242, "/tmp/finallyshell-acme.abcdEFGH", 40000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'--dst-type' 'LOCAL'", "'--ctstate' 'DNAT'", "'fib' 'daddr' 'type' 'local'"} {
		if !strings.Contains(rules, want) {
			t.Errorf("the rules lack %s", want)
		}
	}
	// Each iptables rule added is deleted with the same matches.
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		for _, chain := range []string{"'PREROUTING' '1' ", "'INPUT' '1' "} {
			i := strings.Index(line, chain)
			if i < 0 {
				continue
			}
			spec := strings.TrimSuffix(strings.TrimSuffix(line[i+len(chain):], "; then"), " || :")
			if !strings.Contains(stop, spec) {
				t.Errorf("stop does not delete %s", spec)
			}
		}
	}
	if _, err := ACMESplitRulesCommand(80); err == nil {
		t.Error("a privileged port passed")
	}
}
