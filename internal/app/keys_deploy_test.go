package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
)

func TestKeysAuthorizedScript(t *testing.T) {
	_, public, err := sshx.GenerateKey("ed25519", "fixture ' $(false)", "")
	if err != nil {
		t.Fatal(err)
	}
	command, err := keysAuthorizedScript(string(public))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`'mkdir' '-p' "$dir"`, `'chmod' '700' "$dir"`, `'chmod' '600' "$file"`, `'grep' '-qxF' '-f' '-' "$file"`, `'restorecon' "$dir" "$file"`, `<<'FINALLYSHELL_AUTHORIZED_KEY'`, `.bak-`} {
		if !strings.Contains(command, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"", "ssh-ed25519 not-a-key", string(public) + string(public), "command=\"false\" " + string(public)} {
		if _, err := keysAuthorizedScript(bad); err == nil {
			t.Errorf("invalid public key accepted")
		}
	}
}

func keysTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		root := filepath.Join(os.Getenv("ProgramFiles"), "Git")
		shell := filepath.Join(root, "bin", "sh.exe")
		if _, err := os.Stat(shell); err != nil {
			t.Skip("Git sh is unavailable")
		}
		return shell
	}
	return "/bin/sh"
}

func keysShellPath(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		return "/" + strings.ToLower(path[:1]) + path[2:]
	}
	return path
}

func runKeysFixture(t *testing.T, script, home, bin string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), keysHostTimeout)
	defer cancel()
	process := exec.CommandContext(ctx, keysTestShell(t), "-s")
	path := os.Getenv("PATH")
	if runtime.GOOS == "windows" {
		path = filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin") + ";" + path
	}
	process.Env = append(os.Environ(), "HOME="+keysShellPath(home), "PATH="+bin+string(os.PathListSeparator)+path)
	process.Stdin = strings.NewReader(script)
	output, err := process.CombinedOutput()
	return string(output), err
}

func TestKeysAuthorizedScriptIdempotent(t *testing.T) {
	_, pub, err := sshx.GenerateKey("ed25519", "has ' and $(false)", "")
	if err != nil {
		t.Fatal(err)
	}
	script, err := keysAuthorizedScript(string(pub))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	for range 2 {
		if text, err := runKeysFixture(t, script, home, home); err != nil {
			t.Fatalf("authorized_keys script: %v %q", err, text)
		}
	}
	path := filepath.Join(home, ".ssh", "authorized_keys")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(pub) {
		t.Fatalf("unexpected authorized_keys contents: %v", err)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one backup, got %v, %v", backups, err)
	}
	fields := strings.Fields(string(pub))
	restricted := `from="fixture.example" ` + strings.Join(fields[:2], " ") + " previous comment\n"
	if err := os.WriteFile(path, []byte(restricted), 0o600); err != nil {
		t.Fatal(err)
	}
	if text, err := runKeysFixture(t, script, home, home); err != nil {
		t.Fatalf("authorized_keys script: %v %q", err, text)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != restricted {
		t.Fatal("a restricted existing key was duplicated")
	}
}

func TestKeysDisableConfigFixture(t *testing.T) {
	for _, tc := range []struct {
		name        string
		drop, fails bool
	}{
		{"drop-in", true, false}, {"main file", false, false}, {"validation fails", false, true}, {"drop-in validation fails", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, bin := t.TempDir(), t.TempDir()
			directory := filepath.Join(root, "sshd_config.d")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			active := filepath.Join(root, "sshd_config")
			drop := filepath.Join(directory, "00-finallyshell-disable-password.conf")
			original := "# fixture\nPasswordAuthentication yes\n"
			if tc.drop {
				original = "Include " + keysShellPath(directory) + "/*.conf\n" + original
				if err := os.WriteFile(drop, []byte("# previous drop-in\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(active, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := func(name, script string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			target, targetBefore := active, original
			if tc.drop {
				target, targetBefore = drop, "# previous drop-in\n"
			}
			sshd := "if [ \"$1\" = '-T' ]; then printf '%s\\n' 'passwordauthentication no' 'pubkeyauthentication yes' 'authenticationmethods any'; exit; fi\n"
			if tc.fails {
				sshd += "if grep -qxF 'PasswordAuthentication no' " + shellQuote(keysShellPath(target)) + "; then echo 'fixture: invalid sshd config' >&2; exit 1; fi\n"
			}
			fixture("sshd", sshd+"exit 0\n")
			reload := filepath.Join(root, "reload")
			fixture("systemctl", "if [ \"$1\" = 'reload' ]; then touch "+shellQuote(keysShellPath(reload))+"; fi\nexit 0\n")
			script := strings.ReplaceAll(keysDisablePasswordScript, "/etc/ssh", keysShellPath(root))
			for _, name := range []string{"sshd", "systemctl", "service"} {
				fixturePath := shellQuote(keysShellPath(filepath.Join(bin, name)))
				script = strings.ReplaceAll(script, "'command' '-v' "+shellQuote(name), "'command' '-v' "+fixturePath)
				for _, arg := range []string{"is-active", "reload", "$candidate", "$service"} {
					quoted := shellQuote(arg)
					if strings.HasPrefix(arg, "$") {
						quoted = `"` + arg + `"`
					}
					script = strings.ReplaceAll(script, shellQuote(name)+" "+quoted, fixturePath+" "+quoted)
				}
			}
			if !strings.Contains(script, keysShellPath(root)+"/sshd_config") || strings.Contains(script, "/etc/ssh/") {
				t.Fatal("script not confined to the fixture directory")
			}
			text, err := runKeysFixture(t, script, root, bin)
			if tc.fails != (err != nil) || tc.fails && !strings.Contains(text, "fixture: invalid sshd config") {
				t.Fatalf("unexpected result %v: %s", err, text)
			}
			if _, err := os.Stat(reload); tc.fails != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reload state: %v", err)
			}
			backups, err := filepath.Glob(target + ".bak-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("expected one backup, got %v: %v", backups, err)
			}
			backup, err := os.ReadFile(backups[0])
			if err != nil || string(backup) != targetBefore {
				t.Fatalf("wrong backup: %v", err)
			}
			data, err := os.ReadFile(target)
			if err != nil || tc.fails && string(data) != targetBefore || !tc.fails && !strings.HasPrefix(string(data), "PasswordAuthentication no\n") {
				t.Fatalf("wrong final config: %v %q", err, data)
			}
		})
	}
}

func TestKeysDisableUsesPrivilege(t *testing.T) {
	a, p := testApp(t)
	host := &config.Host{ID: "verified", Addr: "lab.example", User: "operator", Auth: config.AuthAgent}
	a.hosts.Add(host)
	command := make(chan monitor.PrivilegeRequest, 2)
	remote := keysRemote{privilege: func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		command <- request
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "sudo-n"}, nil
		}
		return monitor.PrivilegeResult{Started: true, Stdout: "fixture result"}, nil
	}}
	done := make(chan error, 1)
	go func() {
		out, _, err := a.keysDisableRemote(t.Context(), remote, host)
		if err == nil && out != "fixture result" {
			err = fmt.Errorf("unexpected output %q", out)
		}
		done <- err
	}()
	p.until(t, "privilege command", func() bool { return len(done) == 1 })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(command) != 2 || !(<-command).Probe {
		t.Fatal("missing privilege probe or action")
	}
	if action := <-command; action.Action != keysDisablePasswordScript || !strings.Contains(action.Command, "sudo -n") {
		t.Fatalf("action did not run through sudo: %q", action.Command)
	}
}

func TestKeysDeploySteps(t *testing.T) {
	failure := errors.New("fixture: unavailable")
	for _, test := range []struct {
		name, fail, want string
		verified         bool
	}{
		{"works", "", "connect append verify", true},
		{"connect error", "connect", "connect", false},
		{"append error", "append", "connect append", false},
		{"verify error", "verify", "connect append verify", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			check := func(step string) error {
				calls = append(calls, step)
				if step == test.fail {
					return failure
				}
				return nil
			}
			backend := keysDeployBackend{
				connect: func(_ context.Context, h *config.Host, opts sshx.Options) (keysRemote, error) {
					if h.Auth == config.AuthKey {
						if opts.Password != "" || opts.Passphrase != "fixture" || h.KeyPath != "id_fixture" {
							t.Fatal("verification did not use the chosen key only")
						}
						return keysRemote{}, check("verify")
					}
					return keysRemote{run: func(_ context.Context, script string) (string, string, error) {
						if script != "fixture script" {
							t.Fatalf("unexpected script: %q", script)
						}
						return "", "fixture output", check("append")
					}, close: func() {}}, check("connect")
				},
			}
			target := keysTarget{host: &config.Host{Auth: config.AuthPassword}, opts: sshx.Options{Password: "old fixture"}}
			result := backend.deploy(t.Context(), target, "fixture script", "id_fixture", "fixture", "fixture fingerprint", true)
			if strings.Join(calls, " ") != test.want || result.verified != test.verified || !errors.Is(result.err, map[bool]error{true: nil, false: failure}[test.verified]) {
				t.Fatalf("calls=%v result=%+v", calls, result)
			}
			if result.output != "fixture output" && test.fail != "connect" {
				t.Fatalf("missing output: %q", result.output)
			}
		})
	}
}
