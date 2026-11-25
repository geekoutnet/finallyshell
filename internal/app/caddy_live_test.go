package app

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"

	"github.com/egoist/mygo/ui"
)

// The Caddy tab against a real server (FINALLYSHELL_TEST_SSH, root, with
// Caddy installed): every section reads the server and draws, without
// freezing or crashing. A section that does not finish its read in time
// fails the test instead of hanging it.
func TestCaddyLive(t *testing.T) {
	a, p, s := connectTestServer(t)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	a.openPanelTab(s, tabCaddy)
	for section, name := range caddySectionNames() {
		t.Run(name, func(t *testing.T) {
			s.caddy.section = section
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range 600 { // about a minute of frames
					tt.Frame()
					for pending := true; pending; {
						select {
						case fn := <-p.posts:
							fn()
						default:
							pending = false
						}
					}
					time.Sleep(100 * time.Millisecond)
				}
			}()
			select {
			case <-done:
			case <-time.After(90 * time.Second):
				t.Fatalf("the %s section froze", name)
			}
			t.Logf("%s: %q", name, firstTexts(tt.Texts(), 12))
		})
	}
}

func firstTexts(texts []string, n int) []string {
	if len(texts) > n {
		return texts[len(texts)-n:]
	}
	return texts
}

// Saving a site with an access log through the real transaction on a
// disposable test server (FINALLYSHELL_TEST_MUTATE=1: it changes the
// server's Caddy config, then removes what it added): the reload works,
// and the log file belongs to Caddy's user, not root.
func TestCaddyLiveSaveWithLog(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE=1 not set: this test changes the server")
	}
	_, _, s := connectTestServer(t)
	ctx := t.Context()
	run := func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		return monitor.RunSystemInput(ctx, s.client, script, input, progress)
	}
	inst, err := monitor.DetectCaddy(ctx, run)
	if err != nil || inst.Method == monitor.CaddyNotInstalled {
		t.Fatalf("detect %+v, %v", inst, err)
	}
	if inst.User != "caddy" {
		t.Fatalf("service user %q", inst.User)
	}
	const site, logFile = "/etc/caddy/sites/fs-live.caddy", "/var/log/caddy/fs-live.log"
	run(ctx, "rm -f "+logFile, nil, nil)
	result, err := monitor.ApplyCaddyChange(ctx, run, inst, monitor.CaddyChange{
		Writes: []monitor.CaddyFile{{Path: site, Content: "live.example.test {\n\ttls internal\n\tlog {\n\t\toutput file " + logFile + "\n\t}\n\trespond ok\n}\n"}},
		Reload: true,
	})
	t.Cleanup(func() {
		monitor.ApplyCaddyChange(context.Background(), run, inst, monitor.CaddyChange{Deletes: []string{site}, Reload: true})
		run(context.Background(), "rm -f "+logFile, nil, nil)
	})
	if err != nil || !result.Reloaded || result.RolledBack {
		t.Fatalf("apply %+v, %v", result, err)
	}
	owner, _, _ := run(ctx, "stat -c %U "+logFile, nil, nil)
	if strings.TrimSpace(owner) != "caddy" {
		t.Fatalf("the log file belongs to %q, the service can't write it", strings.TrimSpace(owner))
	}
}
