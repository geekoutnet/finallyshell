package monitor

import (
	"strings"
	"testing"
)

// The nginx.conf Debian and Ubuntu install, trimmed.
const nginxDebianConf = `user www-data;
worker_processes auto;
pid /run/nginx.pid;
include /etc/nginx/modules-enabled/*.conf;

events {
	worker_connections 768;
	# multi_accept on;
}

http {
	sendfile on;
	keepalive_timeout 65;
	# server_tokens off;
	gzip on;
	map $http_upgrade $connection_upgrade { default upgrade; '' close; }
	server {
		listen 8080;
		client_max_body_size 2m; # a server's own: not the http setting
		location / { return 200 "a;b{c}"; }
	}
	include /etc/nginx/conf.d/*.conf;
}
`

func TestReadNginxSettings(t *testing.T) {
	got, err := ReadNginxSettings(nginxDebianConf)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"worker_processes": "auto", "worker_connections": "768", "keepalive_timeout": "65", "gzip": "on"}
	if len(got) != len(want) {
		t.Fatalf("settings %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
}

func TestApplyNginxSettings(t *testing.T) {
	out, err := ApplyNginxSettings(nginxDebianConf, map[string]string{
		"worker_processes":     "4",
		"worker_connections":   "768", // unchanged
		"keepalive_timeout":    "30s",
		"client_max_body_size": "100m",
		"server_tokens":        "off",
		"gzip_comp_level":      "5",
		"gzip":                 "", // left as it is
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"worker_processes 4;", "worker_connections 768;", "keepalive_timeout 30s;", "gzip on;",
		"http {\n    client_max_body_size 100m;\n    gzip_comp_level 5;\n    server_tokens off;\n\tsendfile on;",
		"# server_tokens off;", "client_max_body_size 2m; # a server's own", `return 200 "a;b{c}";`,
	} {
		if !strings.Contains(out, line) {
			t.Errorf("no %q in\n%s", line, out)
		}
	}
	// Read back, as nginx would.
	got, err := ReadNginxSettings(out)
	if err != nil || got["client_max_body_size"] != "100m" || got["worker_processes"] != "4" || got["server_tokens"] != "off" {
		t.Fatalf("read back %v: %v", got, err)
	}
	// Nothing to change: the same bytes.
	if same, err := ApplyNginxSettings(nginxDebianConf, map[string]string{"gzip": "on"}); err != nil || same != nginxDebianConf {
		t.Fatalf("an unchanged setting changed the file: %v", err)
	}
	// A main directive the file lacks goes above its first block.
	out, err = ApplyNginxSettings("events { }\nhttp { }\n", map[string]string{"worker_processes": "auto", "gzip": "on"})
	if err != nil || out != "worker_processes auto;\nevents { }\nhttp {\n    gzip on; }\n" {
		t.Fatalf("added to a bare file: %q, %v", out, err)
	}
}

func TestApplyNginxSettingsRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		text   string
		values map[string]string
	}{
		"injection":      {nginxDebianConf, map[string]string{"client_max_body_size": "1m; include /tmp/x"}},
		"bad level":      {nginxDebianConf, map[string]string{"gzip_comp_level": "10"}},
		"bad switch":     {nginxDebianConf, map[string]string{"gzip": "yes"}},
		"no http block":  {"events { }\n", map[string]string{"gzip": "on"}},
		"unclosed block": {"http {\n", map[string]string{"gzip": "on"}},
		"unclosed quote": {"http { return 200 \"x; }\n", map[string]string{"gzip": "on"}},
	} {
		if _, err := ApplyNginxSettings(tc.text, tc.values); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNginxStartStop(t *testing.T) {
	for _, tc := range []struct {
		action  string
		systemd bool
		want    string
	}{
		{"start", true, "set -e\n'nginx' '-t'\n'systemctl' 'start' 'nginx.service'"},
		{"start", false, "set -e\n'nginx' '-t'\n'nginx'"},
		{"stop", true, "'systemctl' 'stop' 'nginx.service'"},
		{"stop", false, "'nginx' '-s' 'quit'"},
	} {
		got, err := NginxActionCommand(tc.action, tc.systemd, NginxMain)
		if err != nil || got != tc.want {
			t.Errorf("%s (systemd %v) = %q, %v", tc.action, tc.systemd, got, err)
		}
	}
	if _, err := NginxActionCommand("start", true, "/opt/nginx/nginx.conf"); err == nil {
		t.Error("started an nginx with another main file")
	}
}
