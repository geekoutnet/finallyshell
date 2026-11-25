package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelRelayCommands(t *testing.T) {
	for tool, want := range map[string]string{
		"socat":   `'socat' 'TCP4-LISTEN:8080,bind=0.0.0.0,fork,reuseaddr' 'TCP4:127.0.0.1:41005'`,
		"ncat":    `'ncat' '-4' '--listen' '--keep-open' '--sh-exec' ''\''ncat'\'' '\''-4'\'' '\''127.0.0.1'\'' '\''41005'\''' '0.0.0.0' '8080'`,
		"python3": `'python3' '-' '0.0.0.0' '8080' '41005'`,
	} {
		script, err := TunnelRelayScript(tool, "0.0.0.0", 8080, 41005)
		if err != nil || !strings.Contains(script, want) || !strings.Contains(script, "'kill' '-TERM'") {
			t.Errorf("%s: %v\n%s", tool, err, script)
		}
	}
	for _, addr := range []string{"127.0.0.1", "*", "0.0.0.0,fork", "localhost", "fe80::a%eth0", "::1"} {
		if _, err := TunnelRelayScript("socat", addr, 8080, 41005); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
}

func TestTunnelRelayDetectionWithFakes(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	for _, test := range []struct {
		name, want string
		tools      []string
	}{
		{"preferred", "socat", []string{"setsid", "socat", "ncat", "python3"}},
		{"ncat", "ncat", []string{"setsid", "ncat", "python3"}},
		{"python", "python3", []string{"setsid", "python3"}},
		{"missing setsid", "", []string{"socat"}},
		{"missing relay", "", []string{"setsid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, tool := range test.tools {
				if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.CommandContext(t.Context(), bash, "--noprofile", "--norc", "-c", "PATH=.\n"+TunnelRelayDetectScript)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if strings.TrimSpace(string(out)) != test.want || (err == nil) != (test.want != "") {
				t.Fatalf("got %q, %v; want %q", out, err, test.want)
			}
		})
	}
}
