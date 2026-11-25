package monitor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func caddyTestFile(name, data string) string {
	return fmt.Sprintf("F\n%s\n%d\n%s\n", base64.StdEncoding.EncodeToString([]byte(name)), len(data), base64.StdEncoding.EncodeToString([]byte(data)))
}

func caddyTestScript(t *testing.T, cmd string, stdin io.Reader) string {
	t.Helper()
	if cmd != caddyScriptInput {
		return cmd
	}
	if stdin == nil {
		t.Fatal("script stdin missing")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCaddyReadRecursive(t *testing.T) {
	inst := CaddyInstall{Binary: "/usr/bin/caddy", Config: "/etc/caddy/Caddyfile"}
	next := []struct{ arg, response string }{
		{inst.Config, caddyTestFile(inst.Config, "(shared) {\n respond `import ignored`\n}\nimport shared\nimport sites/*.caddy\n")},
		{"/etc/caddy/sites/", caddyTestFile("/etc/caddy/sites/one.caddy", "import ../common.conf\n")},
		{"/etc/caddy/common.conf", caddyTestFile("/etc/caddy/common.conf", "# end\n")},
	}
	run := func(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
		t.Helper()
		if len(next) == 0 || !strings.Contains(script, privilegeQuote(next[0].arg)) {
			t.Fatalf("unexpected read: %s", script)
		}
		response := next[0].response
		next = next[1:]
		return response, "", nil
	}
	files, err := ReadCaddyFiles(context.Background(), run, inst)
	if err != nil || len(next) != 0 || len(files) != 3 || files[2].Content != "# end\n" {
		t.Fatalf("files=%+v remaining=%d err=%v", files, len(next), err)
	}
}

func TestCaddyApplyProtocol(t *testing.T) {
	inst := CaddyInstall{Binary: "/usr/bin/caddy", Config: "/etc/caddy/Caddyfile", Unit: "custom-caddy.service"}
	for _, test := range []struct {
		name, marks string
		want        error
		rolled      bool
	}{
		{"ok", "backup /etc/caddy/.backups/20260213120000\nvalidated\nreloaded\n", nil, false},
		{"validation", "invalid\nrolled-back\n", ErrCaddyInvalid, true},
		{"reload", "reload-failed\nrolled-back\n", io.ErrUnexpectedEOF, true},
		{"changed", "changed\n", ErrCaddyChanged, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(_ context.Context, cmd string, stdin io.Reader, _ func(string)) (string, string, error) {
				script := caddyTestScript(t, cmd, stdin)
				for _, needle := range []string{"sha256sum", "mktemp", "rolled-back", "prune_backups", "'systemctl' 'reload' 'custom-caddy.service'", "'validate' '--config' '/etc/caddy/Caddyfile'"} {
					if !strings.Contains(script, needle) {
						t.Errorf("script missing %q", needle)
					}
				}
				if strings.Contains(script, "private payload") {
					t.Error("unencoded file content in script")
				}
				var output strings.Builder
				for mark := range strings.SplitSeq(strings.TrimSpace(test.marks), "\n") {
					fmt.Fprintln(&output, "CADDY_TX "+mark)
				}
				if test.want != nil {
					return output.String(), "diagnostic", io.ErrUnexpectedEOF
				}
				return output.String(), "", nil
			}
			result, err := ApplyCaddyChange(context.Background(), run, inst, CaddyChange{
				Writes: []CaddyFile{{Path: inst.Config, Content: "private payload"}}, Expect: map[string]string{inst.Config: "old"}, Reload: true,
			})
			if !errors.Is(err, test.want) || result.RolledBack != test.rolled || result.Reloaded != (test.want == nil) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCaddyApplyRejectsBadPaths(t *testing.T) {
	for _, name := range []string{"relative", "/etc/caddy/../passwd", "/bad\nname", "/bad\x00name", "/etc/caddy/.backups/test"} {
		_, err := ApplyCaddyChange(context.Background(), func(context.Context, string, io.Reader, func(string)) (string, string, error) {
			t.Fatalf("runner invoked for %q", name)
			return "", "", nil
		}, CaddyInstall{Config: "/etc/caddy/Caddyfile"}, CaddyChange{Deletes: []string{name}})
		if err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestCaddyBackupPreview(t *testing.T) {
	ctx := context.Background()
	inst := CaddyInstall{Config: caddyDefaultConfig, Active: "active"}
	backup := "/etc/caddy/.backups/20260213120000"
	created := "/etc/caddy/sites/new.caddy"
	replies := []string{inst.Config + "\n" + created + "\n", caddyTestFile(backup+inst.Config, "original"), caddyTestFile(inst.Config, "modified"), "", caddyTestFile(created, "new site")}
	run := func(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
		if len(replies) == 0 {
			t.Fatalf("unexpected script: %s", script)
		}
		response := replies[0]
		replies = replies[1:]
		return response, "", nil
	}
	change, err := PreviewCaddyBackup(ctx, run, inst, backup)
	if err != nil || !change.Reload || len(change.Writes) != 1 || change.Writes[0].Content != "original" || len(change.Deletes) != 1 || change.Deletes[0] != created || change.Expect[inst.Config] != "modified" || change.Expect[created] != "new site" {
		t.Fatalf("change=%+v err=%v", change, err)
	}
	if err := DeleteCaddyBackup(ctx, run, inst, "/somewhere/20260213120000"); !errors.Is(err, ErrCaddyUnsafe) {
		t.Fatal(err)
	}
}

func TestCaddyPreviewRebasesImports(t *testing.T) {
	config := "import ../../../shared/*.caddy\nimport \"/srv/config/a b.caddy\"\nimport shared\n# import /ignore\n"
	result, changed, err := caddyPreviewContent(config, "/etc/caddy/Caddyfile", "MARKER", map[string]bool{"shared": true})
	if err != nil || !changed || result != "import \"MARKER/shared/*.caddy\"\nimport \"MARKER/srv/config/a b.caddy\"\nimport shared\n# import /ignore\n" {
		t.Fatalf("result=%q changed=%t err=%v", result, changed, err)
	}
}
