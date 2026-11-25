package monitor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaddyCutoverWithFakeServices(t *testing.T) {
	for _, scenario := range []string{"success", "invalid", "stale", "start-failure", "missing-https", "rollback-failure"} {
		t.Run(scenario, func(t *testing.T) {
			shell, dir, root := caddyFixtureShell(t)
			caddyFixtureWrite(t, dir, "Caddyfile", "old", 0640)
			caddyFixtureWrite(t, dir, "tools/caddy", `#!/bin/sh
printf '%s\n' 'validate' >> calls
[ "$SCENARIO" != invalid ]
`, 0755)
			caddyFixtureWrite(t, dir, "tools/systemctl", `#!/bin/sh
set -eu
printf '%s\n' "$*" >> calls
case "$*" in
  'is-active --quiet nginx.service') exit '0' ;;
  'show --property=ActiveState --value caddy.service') printf '%s\n' 'inactive' ;;
  'show --property=UnitFileState --value nginx.service') printf '%s\n' 'enabled-runtime' ;;
  'show --property=UnitFileState --value caddy.service') printf '%s\n' 'disabled' ;;
  'show --property=MainPID --value nginx.service') printf '%s\n' '41' ;;
  'disable --now nginx.service') [ "$(cat Caddyfile)" = new ] && : > switched ;;
  'enable --now caddy.service') [ "$SCENARIO" != start-failure ] ;;
  'is-active --quiet caddy.service') exit '0' ;;
  'show --property=MainPID --value caddy.service') printf '%s\n' '52' ;;
  'stop caddy.service'|'disable caddy.service'|'disable nginx.service'|'enable --runtime nginx.service') exit '0' ;;
  'start nginx.service') [ "$(cat Caddyfile)" = old ] && [ "$SCENARIO" != rollback-failure ] ;;
  *) exit '98' ;;
esac
`, 0755)
			caddyFixtureWrite(t, dir, "tools/ss", `#!/bin/sh
if [ ! -f switched ]; then printf '%s\n' 'LISTEN 0 1024 *:80 *:* users:(("nginx",pid=41,fd=3))'; exit '0'; fi
case "$SCENARIO" in
  success) https_pid='52' ;;
  *) https_pid='53' ;;
esac
printf '%s\n' 'LISTEN 0 1024 *:80 *:* users:(("caddy",pid=52,fd=3))' "LISTEN 0 1024 *:443 *:* users:((\"other\",pid=$https_pid,fd=4))"
`, 0755)
			caddyFixtureWrite(t, dir, "tools/sleep", "#!/bin/sh\nexit '0'\n", 0755)
			run := func(ctx context.Context, command string, stdin io.Reader, progress func(string)) (string, string, error) {
				script := caddyTestScript(t, command, stdin)
				if command != caddyScriptInput || !strings.Contains(script, privilegeQuote(root+"/Caddyfile")) || progress == nil || strings.Contains(script, "'systemctl' 'reload'") {
					t.Fatal("not a fixture cutover transaction")
				}
				cmd := exec.CommandContext(ctx, shell, "-s")
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader("PATH=" + privilegeQuote(root+"/tools") + ":\"$PATH\"\nSCENARIO=" + privilegeQuote(scenario) + "\nexport PATH SCENARIO\n" + script)
				var stdout, stderr strings.Builder
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err := cmd.Run()
				progress(stderr.String())
				return stdout.String(), stderr.String(), err
			}
			inst := CaddyInstall{Method: CaddyBinary, Binary: root + "/tools/caddy", Config: root + "/Caddyfile", ServiceManager: "systemd", Unit: "caddy.service", Active: "inactive", Conflicts: []string{"nginx"}}
			expected := "old"
			if scenario == "stale" {
				expected = "stale"
			}
			change := CaddyChange{Writes: []CaddyFile{{Path: inst.Config, Content: "new"}}, Expect: map[string]string{inst.Config: expected}, Reload: true}
			result, err := CaddyNginxCutover(t.Context(), run, inst, change, func(string) {})
			current, _ := os.ReadFile(filepath.Join(dir, "Caddyfile"))
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			if scenario == "success" {
				if err != nil || result.Reloaded || result.RolledBack || string(current) != "new" || !strings.Contains(string(calls), "disable --now nginx.service\nenable --now caddy.service") {
					t.Fatalf("success = %+v, %v; config %q, calls %s", result, err, current, calls)
				}
				return
			}
			if err == nil || string(current) != "old" {
				t.Fatalf("failure = %+v, %v; config %q", result, err, current)
			}
			switch scenario {
			case "invalid", "stale":
				want := ErrCaddyInvalid
				if scenario == "stale" {
					want = ErrCaddyChanged
				}
				if !errors.Is(err, want) || strings.Contains(string(calls), "nginx.service") {
					t.Fatalf("error = %v; calls %s", err, calls)
				}
			default:
				if !strings.Contains(string(calls), "stop caddy.service\ndisable caddy.service\ndisable nginx.service\nenable --runtime nginx.service\nstart nginx.service") {
					t.Fatalf("rollback order: %s", calls)
				}
				if scenario == "rollback-failure" && !errors.Is(err, ErrCaddyRollback) || scenario != "rollback-failure" && !result.RolledBack {
					t.Fatalf("rollback = %+v, %v", result, err)
				}
			}
		})
	}
}

func TestCaddyInstallHostOptions(t *testing.T) {
	for _, firewall := range []string{"ufw", "firewalld"} {
		script, err := caddyInstallWithHostChanges("'false'\n", CaddyInstallOptions{Firewall: firewall, ReplaceConflicting: []string{"nginx", "apache2"}})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"'80/tcp'", "'443/tcp'", "'443/udp'", "'systemctl' 'disable' '--now' 'nginx.service'", "'systemctl' 'disable' '--now' 'apache2.service'", "restore_boot_state 'nginx.service'", "trap recover_host '0'"} {
			if !strings.Contains(script, want) {
				t.Errorf("missing %q", want)
			}
		}
		shell, _, _ := caddyFixtureShell(t)
		cmd := exec.Command(shell, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid script: %v: %s", err, out)
		}
	}
	if _, err := caddyInstallWithHostChanges("", CaddyInstallOptions{ReplaceConflicting: []string{"nginx\nother"}}); !errors.Is(err, ErrCaddyUnsafe) {
		t.Fatal(err)
	}
}
