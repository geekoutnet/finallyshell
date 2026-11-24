package monitor

import "testing"

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
