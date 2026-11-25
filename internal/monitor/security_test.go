package monitor

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOpsSecurityParsers(t *testing.T) {
	log := "2026-04-21T01:02:03+0000 host sshd[22]: Failed password for invalid user guest from 192.0.2.12 port 43112 ssh2\n" +
		"2026-04-21T01:02:03+0000 host sshd[22]: Invalid user guest from 192.0.2.12 port 43112\n" +
		"Apr 21 01:03:03 host sshd-session[82]: Accepted publickey for admin from 2001:db8::12 port 61032 ssh2: ED25519 SHA256:example\n" +
		"Apr 21 01:04:03 host sshd[92]: Connection closed by authenticating user admin 192.0.2.12 port 42129 [preauth]\n"
	read := ParseSSHLog(log)
	if len(read) != 3 || read[0].User != "guest" || read[0].Accepted || !read[1].Accepted || read[1].Method != "publickey" || read[2].Method != "preauth" {
		t.Fatalf("ssh: %+v", read)
	}
	if jails := ParseFail2banJails("Status\n|- Number of jail: 2\n`- Jail list: sshd, nginx-http-auth\n"); len(jails) != 2 {
		t.Fatalf("jails: %v", jails)
	}
	if banned := ParseFail2banIPs("   `- Banned IP list: 192.0.2.3 2001:db8::8 invalid\n"); len(banned) != 2 {
		t.Fatalf("banned: %v", banned)
	}
	if cmd, err := Fail2banChangeScript("sshd", "2001:db8::4", false); err != nil || cmd != "'fail2ban-client' 'set' 'sshd' 'unbanip' '2001:db8::4'" {
		t.Fatalf("unban: %q, %v", cmd, err)
	}
	older := "---today---2026-01-03\nDec 28 12:34:56 box sshd[3]: Accepted password for ops from 192.0.2.1 port 31322 ssh2\nDec 15 12:34:56 box sshd[3]: Invalid user guest from 192.0.2.1\n"
	if rows := ParseSSHLog(older); len(rows) != 1 || rows[0].Day != "2025-12-28" || rows[0].Method != "password" {
		t.Fatalf("syslog window: %+v", rows)
	}
}

func TestOpsFail2banStatusWithoutDaemon(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell not installed")
	}
	for _, test := range []struct {
		message string
		success bool
	}{
		{"Failed to access socket path: /run/fail2ban/fail2ban.sock. Is fail2ban running?", true},
		{"invalid configuration", false},
	} {
		command := exec.Command(shell, "-s")
		command.Stdin = strings.NewReader("fake_fail2ban() { printf '%s\\n' " + privilegeQuote(test.message) + " >&2; return 255; }\n" + strings.ReplaceAll(Fail2banStatusScript, "fail2ban-client", "fake_fail2ban"))
		output, err := command.CombinedOutput()
		if test.success && (err != nil || string(output) != "installed\n") || !test.success && (err == nil || !strings.Contains(string(output), test.message)) {
			t.Errorf("%s: %q, %v", test.message, output, err)
		}
	}
}
