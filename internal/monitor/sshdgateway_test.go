package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGatewayPortsParsing(t *testing.T) {
	for _, mode := range []string{"no", "yes", "clientspecified"} {
		got, err := ParseGatewayPorts("port 22\r\ngatewayports " + mode + "\r\nfinally_match yes\n")
		if err != nil || got.Mode != mode || !got.Match {
			t.Fatalf("%s: %+v, %v", mode, got, err)
		}
	}
	for _, invalid := range []string{"", "gatewayports maybe", "gatewayports no\ngatewayports yes"} {
		if _, err := ParseGatewayPorts(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
}

func TestGatewayPortsRollbackWithFakeSSHD(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	for _, test := range []struct {
		name, initial, failStep string
		wantDropin              bool
	}{
		{"drop-in", "Include /etc/ssh/sshd_config.d/*.conf\nPort 22\n", "", true},
		{"prepend", "GatewayPorts no\nInclude /etc/ssh/sshd_config.d/*.conf\n", "", false},
		{"invalid syntax", "Port 22\n", "syntax", false},
		{"reload failure", "Include /etc/ssh/sshd_config.d/*.conf\n", "reload", true},
		{"match shortcut", "Match User test\n  GatewayPorts no\n", "match", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			configDir := filepath.Join(dir, "sshd_config.d")
			if err := os.Mkdir(configDir, 0700); err != nil {
				t.Fatal(err)
			}
			configFile := filepath.Join(dir, "sshd_config")
			writeFake := func(name, value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0700); err != nil {
					t.Fatal(err)
				}
			}
			writeFake("sshd_config", test.initial)
			writeFake("sshd", `#!/bin/sh
if 'grep' '-q' 'GatewayPorts clientspecified' './sshd_config' './sshd_config.d/00-finallyshell-gatewayports.conf' 2>/dev/null; then
  if [ "$1" = '-t' ] && [ "$FAIL_STEP" = 'syntax' ]; then exit 1; fi
  if [ "$1" = '-T' ]; then 'printf' '%s\n' 'gatewayports clientspecified'; fi
elif [ "$1" = '-T' ]; then
  'printf' '%s\n' 'gatewayports no'
fi
`)
			writeFake("systemctl", `#!/bin/sh
if [ "$1" = 'is-active' ] && [ "$2" = '--quiet' ] && [ "$3" = 'sshd' ]; then exit 0; fi
if [ "$1" != 'reload' ] || [ "$2" != 'sshd' ]; then exit 1; fi
'printf' '%s\n' 'reload' >> './reloads'
if [ "$FAIL_STEP" = 'reload' ] && [ "$('wc' '-l' < './reloads')" -eq 1 ]; then exit 1; fi
`)
			writeFake("restorecon", "#!/bin/sh\nexit 0\n")
			script := strings.NewReplacer(
				"'/etc/ssh/sshd_config'", "'./sshd_config'",
				"'/etc/ssh/sshd_config.d'", "'./sshd_config.d'",
				"'/etc/ssh/.finallyshell-sshd.lock'", "'./lock'",
			).Replace(GatewayPortsChangeScript)
			path := ".:$PATH"
			if runtime.GOOS == "windows" {
				path = ".:/usr/bin:/bin:$PATH"
			}
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", "PATH="+path+"\n"+script)
			cmd.Dir, cmd.Env = dir, append(os.Environ(), "FAIL_STEP="+test.failStep)
			out, err := cmd.CombinedOutput()
			if (err == nil) != (test.failStep == "") {
				t.Fatalf("executed: %v, %s", err, out)
			}
			main, _ := os.ReadFile(configFile)
			dropin, dropErr := os.ReadFile(filepath.Join(configDir, "00-finallyshell-gatewayports.conf"))
			if test.failStep != "" {
				if string(main) != test.initial || !os.IsNotExist(dropErr) {
					t.Fatalf("not rolled back: %s, %s, %v", main, dropin, dropErr)
				}
				if test.failStep == "reload" {
					if reloads, _ := os.ReadFile(filepath.Join(dir, "reloads")); strings.Count(string(reloads), "reload") != 2 {
						t.Errorf("restored config not reloaded: %q", reloads)
					}
				}
			} else if test.wantDropin {
				if string(main) != test.initial || string(dropin) != "GatewayPorts clientspecified\n" {
					t.Fatalf("drop-in failed: %s, %s", main, dropin)
				}
			} else if string(main) != "GatewayPorts clientspecified\n"+test.initial {
				t.Fatalf("prepend failed: %s", main)
			}
			if _, err := os.Stat(filepath.Join(dir, "lock")); !os.IsNotExist(err) {
				t.Errorf("lock left behind: %v", err)
			}
		})
	}
}

func TestGatewayPortsChangeScript(t *testing.T) {
	for _, fragment := range []string{"00-finallyshell-gatewayports.conf", "GatewayPorts clientspecified", `"$sshd" '-t' '-f' "$config"`, `trap 'rollback' EXIT`, `'systemctl' 'reload' "$service"`, `"$target.bak-`, `'grep' '-qxF' 'gatewayports clientspecified'`} {
		if !strings.Contains(GatewayPortsChangeScript, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
}
