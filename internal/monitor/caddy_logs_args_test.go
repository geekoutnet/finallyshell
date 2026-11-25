package monitor

import (
	"slices"
	"strings"
	"testing"
)

// A log path in a snippet using its arguments gives one file per import
// that passes them (the panel tailed "/var/log/caddy/{args[0]}.log").
func TestCaddyLogFilesSnippetArgs(t *testing.T) {
	main := "(logging) {\n\tlog {\n\t\toutput file /var/log/caddy/{args[0]}.log {\n\t\t\troll_size 10MiB\n\t\t}\n\t}\n}\n\n" +
		"(other) {\n\tlog {\n\t\toutput file /var/log/caddy/{args.1}-{args[0]}.log\n\t}\n}\n\n" +
		":80 {\n\tlog {\n\t\toutput file /var/log/caddy/plain.log\n\t}\n}\nimport sites/*.caddy\n"
	site := "app.example.test {\n\timport logging app\n}\nshop.example.test {\n\timport logging shop\n\timport other a b\n}\nbare.example.test {\n\timport logging\n}\n"
	got := CaddyLogFiles([]CaddyFile{{Path: "/etc/caddy/Caddyfile", Content: main}, {Path: "/etc/caddy/sites/apps.caddy", Content: site}})
	want := []string{"/var/log/caddy/app.log", "/var/log/caddy/b-a.log", "/var/log/caddy/plain.log", "/var/log/caddy/shop.log"}
	if !slices.Equal(got, want) {
		t.Fatalf("log files %q, want %q", got, want)
	}
}

// Validation runs as the service's user, on its scratch folder; root,
// Docker and odd names keep the command as it was.
func TestCaddyAsServiceUser(t *testing.T) {
	cmd := "'caddy' 'validate'"
	if got := caddyAsServiceUser(CaddyInstall{User: "caddy"}, cmd, `"$tmp"`); got == cmd ||
		!contains(got, "runuser -p -u 'caddy' -- 'caddy' 'validate'") || !contains(got, `chown -R 'caddy' -- "$tmp"`) {
		t.Errorf("as caddy: %s", got)
	}
	for _, inst := range []CaddyInstall{{}, {User: "root"}, {User: "caddy", Method: CaddyDocker}, {User: "bad;name"}} {
		if got := caddyAsServiceUser(inst, cmd, `"$tmp"`); got != cmd {
			t.Errorf("%+v: %s", inst, got)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
