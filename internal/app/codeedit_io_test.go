package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
)

func TestCodeEditorRootIO(t *testing.T) {
	const file = "/etc/caddy/my 'site'.caddy"
	const before = "example.test {\r\n}\r\n"
	const after = "# revised\r\n"
	var scripts []string
	conflict := false
	run := func(_ context.Context, script string, input io.Reader, _ func(string)) (string, string, error) {
		scripts = append(scripts, script)
		if input == nil {
			return base64.StdEncoding.EncodeToString([]byte(before + "\xff\x00\xfe\xff")), "", nil
		}
		data, err := io.ReadAll(input)
		if err != nil || string(data) != after || strings.Contains(script, after) {
			t.Fatalf("payload got %q; error %v", data, err)
		}
		if conflict {
			return "", "cedit:changed\n", errors.New("exit status 43")
		}
		return file + ".bak-20261125203045\n", "", nil
	}
	if got, err := readRootCode(t.Context(), run, file); err != nil || string(got) != before {
		t.Fatalf("read: %q %v", got, err)
	}
	if got, err := writeRootCode(t.Context(), run, file, before, after, nil); err != nil || got != file+".bak-20261125203045" {
		t.Fatalf("write: %q %v", got, err)
	}
	conflict = true
	if _, err := writeRootCode(t.Context(), run, file, before, after, nil); !errors.Is(err, monitor.ErrCaddyChanged) {
		t.Fatalf("conflict: %v", err)
	}
	for _, invalid := range []string{"relative", "/a\nb", "/a\x00b", "/a\rb", "/", "/a/../b"} {
		if _, err := readRootCode(t.Context(), run, invalid); err == nil {
			t.Errorf("read accepted %q", invalid)
		}
		if _, err := writeRootCode(t.Context(), run, invalid, before, after, nil); err == nil {
			t.Errorf("write accepted %q", invalid)
		}
	}
	if len(scripts) != 3 {
		t.Fatalf("got %d calls", len(scripts))
	}
	if !strings.Contains(scripts[0], `'head' '-c' '5242881'`) || !strings.Contains(scripts[0], monitor.SystemCommand(file)) {
		t.Fatalf("unsafe read script:\n%s", scripts[0])
	}
	for _, command := range []string{monitor.SystemCommand(file), monitor.SystemCommand("./my 'site'.caddy"), `'cd' '-P'`, `'sha256sum'`, `'mktemp' '-d'`, `'cp' '-p'`, `'.bak-'`, `'ln' '-T'`, `'mv' '-fT'`} {
		if !strings.Contains(scripts[1], command) {
			t.Errorf("write missing %s:\n%s", command, scripts[1])
		}
	}
	for _, value := range []string{before, after} {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
		if !strings.Contains(scripts[1], monitor.SystemCommand(hash)) {
			t.Errorf("write did not check SHA-256 %s", hash)
		}
	}
	backup := strings.Index(scripts[1], `'ln' '-T'`)
	rename := strings.Index(scripts[1], `'mv' '-fT'`)
	if backup < 0 || rename < backup || !strings.Contains(scripts[1][backup:rename], "\nunchanged\n") {
		t.Fatal("backup and latest-content check must precede the rename")
	}
}

func TestCodeEditorRootReadFraming(t *testing.T) {
	for _, example := range []struct {
		name, stdout, stderr, want string
		err                        error
	}{
		{"empty", "/wD+/w==\r\n", "", "", nil},
		{"crlf in text and PTY", "YQ0K\r\n/wD+/w==\r\n", "", "a\r\n", nil},
		{"missing frame sentinel", "YQ0K\n", "read error", "", errCodeRead},
		{"not regular", "cedit:regular\r\n", "", "", errCodeRegular},
	} {
		t.Run(example.name, func(t *testing.T) {
			run := func(context.Context, string, io.Reader, func(string)) (string, string, error) {
				return example.stdout, example.stderr, nil
			}
			got, err := readRootCode(t.Context(), run, "/tmp/example")
			if string(got) != example.want || !errors.Is(err, example.err) {
				t.Fatalf("read: %q, %v", got, err)
			}
		})
	}
}
