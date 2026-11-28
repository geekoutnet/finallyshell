package monitor

import (
	"strings"
	"testing"
)

func TestACMECommands(t *testing.T) {
	if _, err := ACMEWriteCommand("/var/www/html", "tok_EN-123456", "tok_EN-123456.thumbprint_xyz"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][3]string{
		{"relative/path", "tokentoken", "tokentoken.thumbprint"},
		{"/var/www/../etc", "tokentoken", "tokentoken.thumbprint"},
		{"/var/www", "../../etc/x", "tokentoken.thumbprint"},
		{"/var/www", "tokentoken", "x'; rm -rf /; echo '.thumbprint"},
		{"/var/www", "tokentoken", "no-dot-here-at-all"},
	} {
		if _, err := ACMEWriteCommand(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("write %q passed", bad)
		}
	}
	if _, err := ACMERemoveCommand("/var/www", "a/b"); err == nil {
		t.Error("remove with a slash passed")
	}
	if _, err := ACMEResponderStopCommand(1, "/tmp/finallyshell-acme.abcdEFGH"); err == nil {
		t.Error("stopping pid 1 passed")
	}
	if _, err := ACMEResponderStopCommand(4242, "/tmp/elsewhere"); err == nil {
		t.Error("removing another folder passed")
	}
	pid, dir, ok := ParseACMEResponder("started 4242 /tmp/finallyshell-acme.abcdEFGH\n")
	if !ok || pid != 4242 || dir != "/tmp/finallyshell-acme.abcdEFGH" {
		t.Fatalf("responder %d %q %v", pid, dir, ok)
	}
	for _, out := range []string{"none", "failed", "started 1 /tmp/finallyshell-acme.abcdEFGH", "started 42 /etc"} {
		if _, _, ok := ParseACMEResponder(out); ok {
			t.Errorf("%q parsed", out)
		}
	}
}

func TestParsePort80(t *testing.T) {
	if busy, _ := ParsePort80("\n"); busy {
		t.Fatal("free port busy")
	}
	ss := `LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=812,fd=6),("nginx",pid=811,fd=6))
LISTEN 0 511 [::]:80 [::]:* users:(("nginx",pid=812,fd=7))`
	if busy, servers := ParsePort80(ss); !busy || len(servers) != 1 || servers[0] != "nginx" {
		t.Fatalf("ss: %v %q", busy, servers)
	}
	netstat := "tcp 0 0 :::80 :::* LISTEN 1203/caddy"
	if busy, servers := ParsePort80(netstat); !busy || len(servers) != 1 || servers[0] != "caddy" {
		t.Fatalf("netstat: %v %q", busy, servers)
	}
	if busy, servers := ParsePort80("LISTEN 0 4096 *:80 *:*"); !busy || len(servers) != 0 {
		t.Fatalf("unknown program: %v %q", busy, servers)
	}
}

func TestNginxACMELocation(t *testing.T) {
	text := "server {\n    listen 80;\n    server_name a.example;\n    return 301 https://$host$request_uri;\n}\n\n  server {  # tls\n    listen 443 ssl;\n    location / { proxy_pass http://127.0.0.1:3000; }\n  }\n"
	got, err := NginxACMELocation(text, []int{7, 1})
	if err != nil {
		t.Fatal(err)
	}
	want := "server {\n    location ^~ /.well-known/acme-challenge/ { # finallyshell-acme\n        root /var/lib/finallyshell-acme;\n        default_type text/plain;\n    }\n" +
		"    listen 80;\n    server_name a.example;\n    return 301 https://$host$request_uri;\n}\n\n  server {  # tls\n" +
		"      location ^~ /.well-known/acme-challenge/ { # finallyshell-acme\n          root /var/lib/finallyshell-acme;\n          default_type text/plain;\n      }\n" +
		"    listen 443 ssl;\n    location / { proxy_pass http://127.0.0.1:3000; }\n  }\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if !NginxConfigSafe(got) {
		t.Fatal("the location is refused as unsafe")
	}
	for _, lines := range [][]int{{2}, {0}, {99}, {1, 1}} {
		if _, err := NginxACMELocation(text, lines); err == nil {
			t.Errorf("lines %v passed", lines)
		}
	}
	if _, err := NginxACMELocation("server\n{\n}\n", []int{1}); err == nil {
		t.Error("a brace on its own line passed")
	}
	if _, err := NginxACMELocation(got, []int{1}); err == nil || !strings.Contains(got, ACMELocationMark) {
		t.Error("a second location passed")
	}
}
