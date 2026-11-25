package monitor

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCaddyInstallPlans(t *testing.T) {
	for _, test := range []struct{ os, method, want string }{
		{"debian", CaddyAPT, "'apt-get' 'install' '-y' 'caddy'"},
		{"fedora", CaddyDNF, "'dnf5-plugins'"},
		{"rhel", CaddyDNF, "'dnf-plugins-core'"},
		{"arch", CaddyPacman, "'pacman' '-Syu' '--noconfirm' 'caddy'"},
		{"alpine", CaddyAPK, "'apk' 'add' 'caddy'"},
		{"debian", CaddyBinary, "sha512sum"},
		{"debian", CaddyDocker, "'443:443/udp'"},
	} {
		t.Run(test.os+test.method, func(t *testing.T) {
			script, err := caddyInstallScript(CaddyInstall{OS: test.os}, test.method, CaddyInstallOptions{Version: "v2.10.2"})
			if err != nil || !strings.Contains(script, test.want) {
				t.Fatalf("missing %q, err=%v: %s", test.want, err, script)
			}
			if test.method == CaddyAPT && (!strings.Contains(script, "'signed-by=") && !strings.Contains(script, "signed-by=")) {
				t.Fatal("missing signed repository")
			}
			if test.method == CaddyBinary {
				for _, arch := range []string{"armv7", "armv6", "amd64", "arm64", "386", "s390x", "ppc64le", "riscv64"} {
					if !strings.Contains(script, arch) {
						t.Errorf("no %s", arch)
					}
				}
			}
		})
	}
}

func TestCaddyBinaryUpdateRollbackPlan(t *testing.T) {
	for _, modules := range [][]string{nil, {"dns.providers.test"}} {
		inst := CaddyInstall{Method: CaddyBinary, Binary: "/usr/bin/caddy", Config: caddyDefaultConfig, Unit: "caddy.service", Active: "active", Modules: modules}
		run := func(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
			if strings.Contains(script, "record 'done'") {
				return caddyDetectRecord("host", "/usr/bin/caddy\nbinary\nv2.10.2") + caddyDetectRecord("done", ""), "", nil
			}
			if strings.Contains(script, "record 'config'") {
				return "", "", nil
			}
			for _, want := range []string{".bak-", "recovery()", "'systemctl' 'restart' 'caddy.service'", "'validate' '--config' '/etc/caddy/Caddyfile'"} {
				if !strings.Contains(script, want) {
					t.Errorf("missing %q", want)
				}
			}
			if (len(modules) != 0) != strings.Contains(script, "'upgrade' '--keep-backup'") {
				t.Error("incorrect upgrade path")
			}
			return "", "", nil
		}
		if _, err := UpdateCaddy(context.Background(), run, inst, nil, CaddyUpdateOptions{Version: "v2.10.2"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCaddyInstallRefusesExistingAndConflicts(t *testing.T) {
	for _, test := range []struct {
		values string
		want   error
	}{
		{caddyDetectRecord("host", "/usr/bin/caddy\napt\nv2.10.0"), ErrCaddyAlreadyInstalled},
		{caddyDetectRecord("ports", `LISTEN 0 128 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=42,fd=2))`), ErrCaddyPortConflict},
	} {
		calls := 0
		run := func(_ context.Context, script string, stdin io.Reader, _ func(string)) (string, string, error) {
			calls++
			if stdin != nil || !strings.Contains(script, "record 'done'") && !strings.Contains(script, "record 'config'") {
				t.Fatal("installation attempted before safety checks")
			}
			return test.values + caddyDetectRecord("done", ""), "", nil
		}
		if _, err := InstallCaddy(context.Background(), run, CaddyInstall{OS: "debian"}, CaddyAPT, nil); err == nil || !strings.Contains(err.Error(), test.want.Error()) || calls > 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestCaddyManagedSplitter(t *testing.T) {
	main, err := os.ReadFile("testdata/caddy/managed-input.Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	inst := CaddyInstall{Config: caddyDefaultConfig, SitesDir: "/etc/caddy/sites"}
	change, err := caddyManagedChange(inst, []CaddyFile{{Path: inst.Config, Content: string(main)}})
	if err != nil || len(change.Writes) != 3 || len(change.ExpectMissing) != 2 || !change.Reload {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if change.Writes[1].Path != "/etc/caddy/sites/example.com.caddy" || !strings.Contains(change.Writes[1].Content, "import ../../common/*.conf") || !strings.Contains(change.Writes[2].Content, "# the next site") || !strings.Contains(change.Writes[0].Content, "import sites/*.caddy") {
		t.Fatalf("incorrect split: %+v", change.Writes)
	}
}

func TestCaddyNginxConversion(t *testing.T) {
	text, notes := NginxToCaddy([]NginxSite{
		{File: "test.conf", Names: []string{"example.com"}, Listen: []NginxListen{{Port: 80}}, Proxy: []string{"http://127.0.0.1:3000"}},
		{File: "test.conf", Names: []string{"other.example"}, Listen: []NginxListen{{Port: 443, TLS: true}}, Root: "/srv/website", Certificates: []string{"/ssl/cert.pem", "/ssl/key.pem"}},
		{File: "unsupported.conf", Names: []string{"~aaa"}, Proxy: []string{"http://host/path", "http://other"}},
	})
	if !strings.Contains(text, `http://example.com {`) || !strings.Contains(text, `reverse_proxy "http://127.0.0.1:3000"`) || !strings.Contains(text, `root * "/srv/website"`) || len(notes) < 2 || strings.Contains(text, "~aaa") {
		t.Fatalf("config=%s notes=%v", text, notes)
	}
	dump := "# configuration file /etc/nginx/nginx.conf:\nhttp {\nserver {\n listen 443 ssl; server_name other.example; root /srv/www; ssl_certificate /ssl/cert.pem; ssl_certificate_key /ssl/key.pem; try_files $uri $uri/ =404;\n}\nserver { listen 80; server_name first.example; return 301 https://other.example/; }\n}\n"
	if text, _, err := NginxDumpToCaddy(dump); err != nil || !strings.Contains(text, `tls "/ssl/cert.pem" "/ssl/key.pem"`) || !strings.Contains(text, `try_files {path} {path}/ =404`) || !strings.Contains(text, `redir "https://other.example/" 301`) {
		t.Fatalf("config=%s err=%v", text, err)
	}
}
