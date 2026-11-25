package monitor

import (
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

func caddyDetectRecord(tag, value string) string {
	return "CADDY " + tag + " " + base64.StdEncoding.EncodeToString([]byte(value)) + "\n"
}

func TestCaddyDetect(t *testing.T) {
	for _, test := range []struct{ name, report, method, config string }{
		{"none", "", CaddyNotInstalled, caddyDefaultConfig},
		{"apt", caddyDetectRecord("host", "/usr/bin/caddy\napt\nv2.10.0") + caddyDetectRecord("latest", "  Candidate: 2.10.2"), CaddyAPT, caddyDefaultConfig},
		{"dnf", caddyDetectRecord("host", "/usr/bin/caddy\ndnf\nv2.10.0"), CaddyDNF, caddyDefaultConfig},
		{"pacman", caddyDetectRecord("host", "/usr/bin/caddy\npacman\nv2.10.0"), CaddyPacman, caddyDefaultConfig},
		{"binary", caddyDetectRecord("unit", "Id=proxy.service\nFragmentPath=/etc/systemd/system/proxy.service\nExecStart={ path=/opt/caddy/caddy ; argv[]=/opt/caddy/caddy run --config \"/srv/caddy config/live.json\" ; }\nActiveState=active\nUnitFileState=enabled\nUser=caddy\n"), CaddyBinary, "/srv/caddy config/live.json"},
		{"docker", caddyDetectRecord("docker", `{"Name":"/web","State":{"Running":true},"Config":{"Image":"caddy:2"},"Mounts":[{"Type":"bind","Source":"/srv/caddy","Destination":"/etc/caddy","RW":true}]}`), CaddyDocker, "/srv/caddy/Caddyfile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
				if strings.Contains(script, "record 'done'") {
					return caddyDetectRecord("os", "ID=debian\nVERSION_ID=12") + test.report + caddyDetectRecord("ports", `LISTEN 0 128 0.0.0.0:80 0.0.0.0:* users:(("nginx",pid=1,fd=2))`) + caddyDetectRecord("done", ""), "", nil
				}
				if strings.Contains(script, "\ninspect_binary ") {
					return "", "", nil
				}
				return caddyDetectRecord("dir", "yes") + caddyDetectRecord("config", "{\n admin unix//run/caddy.sock\n}\nimport sites/*\n"), "", nil
			}
			inst, err := DetectCaddy(context.Background(), run)
			if err != nil || inst.Method != test.method || inst.Config != test.config || inst.OSVersion != "12" || len(inst.Conflicts) != 1 || inst.Conflicts[0] != "nginx" {
				t.Fatalf("install=%+v err=%v", inst, err)
			}
			if test.name == "apt" && (!inst.Managed || inst.AdminAddr != "unix//run/caddy.sock" || inst.PackageLatest != "2.10.2") {
				t.Fatalf("incomplete apt detection: %+v", inst)
			}
		})
	}
}
