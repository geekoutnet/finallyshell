package monitor

import (
	"bytes"
	"context"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func caddyFixtureShell(t *testing.T) (string, string, string) {
	t.Helper()
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = `C:\Program Files\Git\usr\bin\bash.exe`
		if _, err := os.Stat(shell); err != nil {
			t.Skip("Git Bash not available")
		}
	}
	dir := t.TempDir()
	cmd := exec.Command(shell, "-c", "pwd -P")
	cmd.Dir = dir
	root, err := cmd.Output()
	if err != nil {
		t.Skipf("POSIX shell not available: %v", err)
	}
	return shell, dir, strings.TrimSpace(string(root))
}

func caddyFixtureWrite(t *testing.T, dir, name, content string, perm os.FileMode) {
	t.Helper()
	name = filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func TestCaddyTransactionWithFakeCommands(t *testing.T) {
	for _, test := range []struct {
		name, expected, validate, reload string
		want                             error
		rollback                         bool
	}{
		{name: "success", expected: "old", validate: "0", reload: "0"},
		{name: "validation", expected: "old", validate: "1", reload: "0", want: ErrCaddyInvalid, rollback: true},
		{name: "reload", expected: "old", validate: "0", reload: "1", rollback: true},
		{name: "changed", expected: "stale", validate: "0", reload: "0", want: ErrCaddyChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			shell, dir, root := caddyFixtureShell(t)
			caddyFixtureWrite(t, dir, "Caddyfile", "old", 0600)
			caddyFixtureWrite(t, dir, "removed", "original", 0640)
			caddyFixtureWrite(t, dir, "tools/caddy", "#!/bin/sh\nprintf '%s\\n' 'validate' >> "+privilegeQuote(root+"/calls")+"\nexit "+test.validate+"\n", 0755)
			caddyFixtureWrite(t, dir, "tools/systemctl", "#!/bin/sh\nprintf '%s\\n' 'reload' >> "+privilegeQuote(root+"/calls")+"\nif [ \"$(cat "+privilegeQuote(root+"/Caddyfile")+")\" = 'new' ]; then exit "+test.reload+"; fi\nexit '0'\n", 0755)
			caddyFixtureWrite(t, dir, "tools/id", "#!/bin/sh\nexit '1'\n", 0755)
			if test.name == "success" {
				for index := range 21 {
					name := "199901010000" + strconv.Itoa(10+index)
					caddyFixtureWrite(t, dir, ".backups/"+name+"/old", "old", 0600)
				}
			}
			run := func(ctx context.Context, command string, stdin io.Reader, _ func(string)) (string, string, error) {
				script := caddyTestScript(t, command, stdin)
				if command != caddyScriptInput || !strings.Contains(script, privilegeQuote(root+"/Caddyfile")) {
					t.Fatal("refusing to execute script outside fixture")
				}
				cmd := exec.CommandContext(ctx, shell, "-s")
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader("PATH=" + privilegeQuote(root+"/tools") + ":\"$PATH\"\nexport PATH\n" + script)
				var output, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &output, &diagnostic
				err := cmd.Run()
				return output.String(), diagnostic.String(), err
			}
			inst := CaddyInstall{Config: root + "/Caddyfile", Binary: root + "/tools/caddy", Unit: "caddy.service"}
			change := CaddyChange{Writes: []CaddyFile{{Path: inst.Config, Content: "new"}, {Path: root + "/sites/new", Content: "site"}}, Deletes: []string{root + "/removed"}, Expect: map[string]string{inst.Config: test.expected}, Reload: true}
			result, err := ApplyCaddyChange(context.Background(), run, inst, change)
			shouldFail := test.name != "success"
			if (err != nil) != shouldFail || (test.want != nil && !errors.Is(err, test.want)) || result.RolledBack != test.rollback {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			data, readErr := os.ReadFile(filepath.Join(dir, "Caddyfile"))
			if readErr != nil || (!shouldFail && string(data) != "new") || (shouldFail && string(data) != "old") {
				t.Fatalf("file=%q err=%v", data, readErr)
			}
			_, removed := os.Stat(filepath.Join(dir, "removed"))
			_, created := os.Stat(filepath.Join(dir, "sites", "new"))
			if (removed != nil) == shouldFail || (created != nil) != shouldFail {
				t.Fatalf("deleted=%v created=%v", removed, created)
			}
			if test.name == "reload" {
				calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
				if string(calls) != "validate\nreload\nreload\n" {
					t.Fatalf("calls=%q result=%+v err=%v", calls, result, err)
				}
			}
			if test.name == "success" {
				backups, err := os.ReadDir(filepath.Join(dir, ".backups"))
				if err != nil || len(backups) != 20 || !strings.HasSuffix(result.Backup, "/"+backups[len(backups)-1].Name()) {
					t.Fatalf("backup=%+v count=%d err=%v", result, len(backups), err)
				}
			}
		})
	}
}

func TestCaddyShellSyntax(t *testing.T) {
	shell, _, _ := caddyFixtureShell(t)
	check := func(script string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(shell, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("invalid script: %v %s\n%s", err, out, script)
		}
	}
	check(caddyDetectScript, nil)
	inst := CaddyInstall{Config: caddyDefaultConfig, OS: "debian", Binary: "/usr/bin/caddy", Unit: "caddy.service", Active: "active", DataDir: "/var/lib/caddy/.local/share/caddy", Envfile: "/etc/default/caddy", Adapter: "caddyfile"}
	for _, method := range []string{CaddyAPT, CaddyDNF, CaddyPacman, CaddyAPK, CaddyBinary, CaddyDocker} {
		inst.Method = method
		check(caddyInstallScript(inst, method, CaddyInstallOptions{Version: "v2.10.2"}))
		if method == CaddyBinary {
			check(caddyBinaryUpdateScript(inst, "v2.10.2"))
			check(caddyPackageMigrationScript(inst))
		} else if method != CaddyDocker {
			check(caddyPackageUpdateScript(inst))
		}
		if method == CaddyDocker {
			inst.Container, inst.ContainerConfig = "caddy", caddyDefaultConfig
			request := recreate{image: "caddy:latest", body: []byte(`{"Image":"caddy:latest"}`)}
			check(caddyDockerReplacementScript(inst, strings.Repeat("a", 64), "caddy:2", request))
		}
		check(caddyUninstallScript(inst, CaddyUninstallOptions{}))
	}
	read, _ := caddyReadScript("/etc/caddy/sites/*.caddy", false)
	check(read, nil)
}

func TestCaddyVerifiedDownloadWithFakeCommands(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		shell, dir, root := caddyFixtureShell(t)
		payload := "archive fixture"
		checksum := sha512.Sum512([]byte(payload))
		if invalid {
			checksum[0] ^= 1
		}
		caddyFixtureWrite(t, dir, "tools/curl", fmt.Sprintf(`#!/bin/sh
case "$*" in *github.com*) ;; *) exit '1' ;; esac
while [ "$1" != '--output' ]; do shift; done
case "$3" in
  */caddy_2.10.2_linux_amd64.tar.gz) printf '%%s' %s > "$2" ;;
  */caddy_2.10.2_checksums.txt) printf '%%s\n' '%x  caddy_2.10.2_linux_amd64.tar.gz' > "$2" ;;
  *) exit '1' ;;
esac
`, privilegeQuote(payload), checksum), 0755)
		caddyFixtureWrite(t, dir, "tools/uname", "#!/bin/sh\nprintf '%s\\n' 'x86_64'\n", 0755)
		caddyFixtureWrite(t, dir, "tools/tar", "#!/bin/sh\nprintf '%s\\n' '#!/bin/sh' \"echo 'v2.10.2'\"\nprintf '%s' ran > "+privilegeQuote(root+"/tar-ran")+"\n", 0755)
		cmd := exec.Command(shell, "-s")
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("PATH=" + privilegeQuote(root+"/tools") + ":\"$PATH\"\nexport PATH\ntmp=" + privilegeQuote(root) + "\n" + caddyDownload + "fetch_binary 'v2.10.2'\n")
		out, err := cmd.CombinedOutput()
		_, markerErr := os.Stat(filepath.Join(dir, "tar-ran"))
		if (err != nil) != invalid || (markerErr != nil) != invalid {
			t.Fatalf("invalid=%t output=%s err=%v marker=%v", invalid, out, err, markerErr)
		}
	}
}
