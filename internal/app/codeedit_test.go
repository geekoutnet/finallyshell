package app

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const codeTestMain = "/etc/caddy/Caddyfile"
const codeTestSite = "/etc/caddy/sites/demo.caddy"

func TestCodeEditorReviewAndSave(t *testing.T) {
	type call struct {
		files  []monitor.CaddyFile
		expect map[string]string
	}
	var calls []call
	invalid := true
	saved := 0
	a, platform := testApp(t)
	a.openCodeEditor(CodeEditorSpec{
		Root: true, Language: "caddyfile",
		Files: []CodeEditorFile{{Path: codeTestMain, Content: "import sites/*.caddy\r\n", Loaded: true}, {Path: codeTestSite, Content: "demo.test {\n}\n", Loaded: true}},
		Validate: func(_ context.Context, files []monitor.CaddyFile) (string, error) {
			calls = append(calls, call{files: files})
			if invalid {
				return "bad caddy directive", errors.New("invalid config")
			}
			return "valid config", nil
		},
		Save: func(_ context.Context, files []monitor.CaddyFile, expect map[string]string) (string, error) {
			calls = append(calls, call{files, expect})
			return "/etc/caddy/backup", nil
		},
		Saved: func() { saved++ },
	})
	var window *codeEditorWindow
	for _, item := range a.codeEditors {
		window = item
	}
	if window == nil {
		t.Fatal("code editor did not open")
	}
	tt := platform.show(t, window.kind)
	wantTexts(t, tt, "tree", "Caddyfile", "sites", "demo.caddy")
	clickButton(t, tt, "sites")
	if tt.HasText("demo.caddy") {
		t.Fatal("folder did not collapse")
	}
	clickButton(t, tt, "sites")
	window.files[0].tab.code.Buffer.Set("# changed\n")
	clickButton(t, tt, "demo.caddy")
	if window.active != window.files[1] || len(window.tabs) != 2 {
		t.Fatal("tree did not open a second tab")
	}
	window.files[1].tab.code.Buffer.Set("demo.test {\n\trespond ok\n}\n")
	tt.Frame()
	clickButton(t, tt, locales.Save())
	if window.review == nil || len(window.review.items) != 2 || len(window.review.items[0].diff.Hunks()) == 0 || len(calls) != 0 {
		t.Fatal("save did not wait for diff review")
	}
	clickButton(t, tt, locales.CeditBack())
	if len(calls) != 0 || window.review != nil {
		t.Fatal("back from review saved")
	}
	clickButton(t, tt, locales.SaveAll())
	clickButton(t, tt, locales.Save())
	platform.until(t, "validation failure", func() bool { return window.cancel == nil })
	tt.Frame()
	wantTexts(t, tt, "validation failure", "bad caddy directive")
	if len(calls) != 1 || saved != 0 || window.unsaved() != 2 || tt.HasText(locales.CeditSaveAnyway()) {
		t.Fatal("validation failure did not block save")
	}
	invalid = false
	clickButton(t, tt, locales.SaveAll())
	clickButton(t, tt, locales.Save())
	platform.until(t, "save callback", func() bool { return saved == 1 })
	if len(calls) != 3 || !reflect.DeepEqual(calls[2].expect, map[string]string{codeTestMain: "import sites/*.caddy\r\n", codeTestSite: "demo.test {\n}\n"}) || !reflect.DeepEqual(calls[1].files, calls[2].files) ||
		calls[2].files[0].Content != "# changed\r\n" || window.unsaved() != 0 || !strings.Contains(window.output, "valid config") || !strings.Contains(window.output, "/etc/caddy/backup") {
		t.Fatalf("save calls: %+v; output %q", calls, window.output)
	}
	window.active.tab.code.Buffer.Set("# unsaved\n")
	if platform.userClose(window.kind) {
		t.Fatal("unsaved window closed")
	}
	tt.Frame()
	wantTexts(t, tt, "close prompt", locales.EditorUnsavedFiles(1), locales.DontSave(), locales.Cancel(), locales.SaveAll())
	clickButton(t, tt, locales.Cancel())
	if !a.windowOpen(window.kind) || window.unsaved() != 1 {
		t.Fatal("cancel lost text")
	}
	if platform.userClose(window.kind) {
		t.Fatal("unsaved window closed after cancel")
	}
	tt.Frame()
	clickButton(t, tt, locales.DontSave())
	if a.windowOpen(window.kind) || len(a.codeEditors) != 0 {
		t.Fatal("discard did not close")
	}
}

func TestCodeEditorRootWindowAndExplicitOverride(t *testing.T) {
	a, platform := testApp(t)
	session := demoSession(a, "Editor test", "web.test")
	var actions []string
	var written string
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root"}, nil
		}
		actions = append(actions, request.Action)
		if request.Stdin == nil {
			return monitor.PrivilegeResult{Started: true, Stdout: base64.StdEncoding.EncodeToString([]byte("# original\r\n\xff\x00\xfe\xff"))}, nil
		}
		data, err := io.ReadAll(request.Stdin)
		written = string(data)
		return monitor.PrivilegeResult{Started: true, Stdout: codeTestMain + ".bak-20261125203045\n"}, err
	}
	saved := false
	a.openCodeEditor(CodeEditorSpec{
		Session: session, Root: true, Files: []CodeEditorFile{{Path: codeTestMain}}, AllowSaveAnyway: true,
		Format: func(_ context.Context, path, text string) (string, error) {
			if path != codeTestMain || text != "# original\r\n" {
				t.Errorf("format request: %q %q", path, text)
			}
			return "# formatted\n", nil
		},
		Validate: func(ctx context.Context, files []monitor.CaddyFile) (string, error) {
			codeEditorProgress(ctx)("checking fixture")
			return "invalid fixture", errors.New("check failed")
		},
		Saved: func() { saved = true },
	})
	for _, window := range a.codeEditors {
		platform.until(t, "root file read", func() bool { return window.cancel == nil })
		if len(actions) != 1 || window.active.content() != "# original\r\n" {
			t.Fatal("file was not read through the root session")
		}
		tt := platform.show(t, window.kind)
		clickButton(t, tt, locales.CeditFormat())
		platform.until(t, "format", func() bool { return window.cancel == nil })
		if len(actions) != 1 || window.active.content() != "# formatted\r\n" || window.unsaved() != 1 {
			t.Fatal("format did not leave a CRLF draft")
		}
		clickButton(t, tt, locales.Save())
		clickButton(t, tt, locales.Save())
		platform.until(t, "failed verification", func() bool { return window.failed })
		tt.Frame()
		if len(actions) != 1 || saved {
			t.Fatal("wrote before explicit override")
		}
		clickButton(t, tt, locales.CeditSaveAnyway())
		platform.until(t, "root save", func() bool { return saved })
		if len(actions) != 2 || written != "# formatted\r\n" || !strings.Contains(window.output, ".bak-20261125203045") || window.unsaved() != 0 {
			t.Fatalf("unexpected root save: %d actions, %q, %q", len(actions), written, window.output)
		}
	}
}

func TestCodeEditorReconnectAndCancel(t *testing.T) {
	a, platform := testApp(t)
	session := demoSession(a, "Editor test", "web.test")
	started := make(chan struct{}, 1)
	var writes int
	a.openCodeEditor(CodeEditorSpec{
		Session: session, Files: []CodeEditorFile{{Path: codeTestMain, Content: "# initial\n", Loaded: true}},
		Validate: func(ctx context.Context, _ []monitor.CaddyFile) (string, error) {
			started <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		},
		Save: func(context.Context, []monitor.CaddyFile, map[string]string) (string, error) {
			writes++
			return "", nil
		},
	})
	for _, window := range a.codeEditors {
		tt := platform.show(t, window.kind)
		window.active.tab.code.Buffer.Set("# remains in memory\n")
		session.status = statusClosed
		tt.Frame()
		tt.Key(ui.Cmd, ui.KeyS)
		clickButton(t, tt, locales.Save())
		wantTexts(t, tt, "disconnected save", locales.CeditReconnectOriginal())
		if len(started) != 0 || writes != 0 || window.unsaved() != 1 {
			t.Fatal("disconnected save reached the server or lost the draft")
		}
		session.status = statusConnected
		tt.Frame()
		clickButton(t, tt, locales.Save())
		clickButton(t, tt, locales.Save())
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("validation did not start")
		}
		clickButton(t, tt, locales.Cancel())
		platform.until(t, "cancelled validation", func() bool { return window.cancel == nil })
		tt.Frame()
		wantTexts(t, tt, "cancelled output", locales.CeditInterrupted())
		if writes != 0 || window.unsaved() != 1 || window.active.content() != "# remains in memory\n" {
			t.Fatal("cancel did not preserve the draft")
		}
	}
}

func TestCodeEditorLargeAndBinary(t *testing.T) {
	large := &codeEditorDoc{tab: editTab{path: codeTestMain}}
	large.fill(strings.Repeat("# test\n", codeFileLimit/7+1), "")
	if !large.limited || !large.tab.code.ReadOnly || large.tab.code.Buffer.Len() != 256<<10 || large.tab.modified() {
		t.Fatal("oversized file was not limited to a read-only preview")
	}
	for _, content := range []string{"# text\x00\n", "\xff\x01"} {
		doc := &codeEditorDoc{tab: editTab{path: codeTestSite}}
		doc.fill(content, "")
		if doc.tab.state != tabBinary || doc.tab.code != nil {
			t.Fatal("opened binary text")
		}
	}
}

func TestCodeEditorMultipleWindowsAndTray(t *testing.T) {
	a, platform := testApp(t)
	spec := CodeEditorSpec{Files: []CodeEditorFile{{Path: codeTestMain, Content: "# one", Loaded: true}}}
	a.openCodeEditor(spec)
	a.openCodeEditor(spec)
	if len(a.codeEditors) != 2 {
		t.Fatal("expected two separate windows")
	}
	var modified *codeEditorWindow
	for _, window := range a.codeEditors {
		window.active.tab.code.Buffer.Set("# modified")
		modified = window
		break
	}
	a.closeWindows()
	if len(a.codeEditors) != 1 || !a.windowOpen(modified.kind) || a.unsavedCodeFiles() != 1 || platform.windows[modified.kind] == nil {
		t.Fatal("tray close removed a modified editor")
	}
}

func TestCodeEditorFileMenu(t *testing.T) {
	a, platform := testApp(t)
	session := demoSession(a, "Editor test", "web.test")
	a.active = 0
	selected := "/lib64/ld-2.12.so"
	view := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		a.filePanel(c)
	}, 740, 330)
	chooseRoot := func() {
		t.Helper()
		if err := view.RightClick("ld-2.12.so"); err != nil {
			t.Fatal(err)
		}
	}
	chooseRoot()
	_ = view.ChooseMenuItem(locales.CeditOpenRoot())
	if len(a.codeEditors) != 0 {
		t.Fatal("without SSH, root menu entry should be disabled")
	}
	view.Key(0, ui.KeyEscape)
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root"}, nil
		}
		if !strings.Contains(request.Action, monitor.SystemCommand(selected)) {
			t.Errorf("read not requested for %s: %s", selected, request.Action)
		}
		return monitor.PrivilegeResult{Started: true, Stdout: "/wD+/w=="}, nil
	}
	chooseRoot()
	if err := view.ChooseMenuItem(locales.CeditOpenRoot()); err != nil || len(a.codeEditors) != 1 {
		t.Fatalf("root action did not open editor: %v", err)
	}
	for _, window := range a.codeEditors {
		platform.until(t, "read selected file", func() bool { return window.cancel == nil })
		if !window.spec.Root || window.active.tab.path != selected || window.active.tab.state != tabReady {
			t.Fatal("incorrect editor target")
		}
	}
}

func TestCodeEditorScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	sample := "# Edge routes for the demo service\n{\n\temail ops@example.test\n}\n\nimport sites/*.caddy\n\nexample.test {\n\t@api path /api/*\n\tencode zstd gzip\n\treverse_proxy @api localhost:8080\n\theader X-Content-Type-Options nosniff\n\trespond \"Path: {http.request.uri}\"\n}\n"
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				a, platform := testApp(t)
				platform.SetLocale(language)
				capture := func(tt *ui.Tester, scene string) {
					tt.Frame()
					saveShot(t, tt, "cedit-"+scene+"-"+language+"-"+appearance+".png")
				}
				windowFor := func(spec CodeEditorSpec) (*codeEditorWindow, *ui.Tester) {
					a.openCodeEditor(spec)
					for _, window := range a.codeEditors {
						tt := ui.NewTester(platform.windows[window.kind].view, 1280, 820)
						tt.SetLocale(language)
						tt.SetDark(appearance == "dark")
						return window, tt
					}
					t.Fatal("no code editor window")
					return nil, nil
				}
				window, tt := windowFor(CodeEditorSpec{
					Root: true, Language: "caddyfile",
					Files:  []CodeEditorFile{{Path: codeTestMain, Content: sample, Loaded: true}, {Path: codeTestSite, Content: "api.example.test {\n\treverse_proxy localhost:8080\n}\n", Loaded: true}},
					Format: func(_ context.Context, _, text string) (string, error) { return text, nil },
					Validate: func(context.Context, []monitor.CaddyFile) (string, error) {
						return "Caddyfile:10: unrecognized directive: reverse_prox\nCheck the reverse_proxy directive and try again.", errors.New("invalid config")
					},
					Save: func(context.Context, []monitor.CaddyFile, map[string]string) (string, error) {
						return "", errors.New("validation should block all writes")
					},
				})
				window.activate(window.files[1])
				window.activate(window.files[0])
				capture(tt, "caddyfile")
				if got := window.active.tab.code.ViewportHeight(); got < 650 {
					t.Fatalf("full editor has only %.1f DIP of space", got)
				}
				short := ui.NewTester(platform.windows[window.kind].view, 460, 300)
				short.SetLocale(language)
				short.SetDark(appearance == "dark")
				capture(short, "short")
				state := window.active.tab.code
				state.Buffer.Set(strings.Replace(sample, "\treverse_proxy @api localhost:8080", "\treverse_prox", 1))
				state.GoTo(state.Buffer.Pos(strings.Index(state.Buffer.String(), "\treverse_prox") + len("\treverse_prox")))
				window.refocus = true
				tt.Frame()
				tt.Key(ui.Ctrl, ui.KeySpace)
				wantTexts(t, tt, "completion", "reverse_proxy")
				capture(tt, "completion")
				tt.Key(0, ui.KeyEscape)
				clickButton(t, tt, locales.Save())
				wantTexts(t, tt, "review", locales.CeditReviewTitle(1), locales.CeditSplit(), locales.CeditUnified())
				capture(tt, "diff")
				clickButton(t, tt, locales.Save())
				platform.until(t, "validation output", func() bool { return window.failed })
				capture(tt, "validation")
				platform.userClose(window.kind)
				capture(tt, "close")
				clickButton(t, tt, locales.DontSave())
				for _, fixture := range []struct{ scene, file, content string }{
					{"nginx", "/etc/nginx/sites-available/demo.conf", "# HTTP proxy\nserver {\n    listen 80;\n    server_name example.test;\n\n    location /api/ {\n        proxy_pass http://backend;\n        proxy_set_header Host $host;\n    }\n}\n"},
					{"systemd", "/etc/systemd/system/caddy.service", "# Caddy service\n[Unit]\nDescription=Caddy\nAfter=network.target\n\n[Service]\nUser=caddy\nExecStart=/usr/bin/caddy run --config /etc/caddy/Caddyfile\nRestart=on-failure\n\n[Install]\nWantedBy=multi-user.target\n"},
				} {
					window, shot := windowFor(CodeEditorSpec{Root: true, Files: []CodeEditorFile{{Path: fixture.file, Content: fixture.content, Loaded: true}}})
					capture(shot, fixture.scene)
					platform.userClose(window.kind)
				}
			})
		}
	}
}

func TestCodeEditorFontSize(t *testing.T) {
	a, platform := testApp(t)
	a.openCodeEditor(CodeEditorSpec{Files: []CodeEditorFile{{Path: codeTestMain, Loaded: true}}})
	for _, window := range a.codeEditors {
		tt := platform.show(t, window.kind)
		clickButton(t, tt, locales.CeditFontLarger())
		if got := config.LoadSettings().CodeEditorFontSize; got != int(codeFont.Size)+1 || got != a.settings.CodeEditorFontSize {
			t.Fatalf("font size: %d", got)
		}
		if _, synced := config.SyncedSettings(a.settings)["codeEditorFontSize"]; synced {
			t.Fatal("font size must stay local")
		}
	}
}

func TestCodeEditorCaddyEditing(t *testing.T) {
	a, platform := testApp(t)
	a.openCodeEditor(CodeEditorSpec{Files: []CodeEditorFile{{Path: codeTestMain, Content: "example.test {", Loaded: true}}})
	for _, window := range a.codeEditors {
		state := window.active.tab.code
		state.Select(state.Buffer.Len(), state.Buffer.Len())
		tt := platform.show(t, window.kind)
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		if got := state.Buffer.String(); got != "example.test {\n\t" {
			t.Fatalf("brace indent: %q", got)
		}
		tt.Type("reverse_prox")
		tt.Key(ui.Ctrl, ui.KeySpace)
		tt.Key(0, ui.KeyEnter)
		if got := state.Buffer.String(); got != "example.test {\n\treverse_proxy localhost:8080" || state.SelectedText() != "localhost:8080" {
			t.Fatalf("completing typed prefix: %q (selected %q)", got, state.SelectedText())
		}
		tt.Key(ui.Cmd, ui.KeySlash)
		if got := state.Buffer.Line(1); got != "\t# reverse_proxy localhost:8080" {
			t.Fatalf("comment toggle: %q", got)
		}
	}
}

func TestCodeEditorNarrow(t *testing.T) {
	a, platform := testApp(t)
	platform.SetLocale("zh-Hans")
	a.openCodeEditor(CodeEditorSpec{Files: []CodeEditorFile{{Path: codeTestMain, Loaded: true}, {Path: codeTestSite, Loaded: true}}})
	for _, window := range a.codeEditors {
		view := platform.windows[window.kind].view
		tt := ui.NewTester(view, 460, 300)
		tt.SetLocale("zh-Hans")
		tt.Frame()
		wantTexts(t, tt, "narrow editor", locales.Save(), locales.SaveAll(), locales.CeditFiles(), locales.CeditFontLarger())
		if got := window.active.tab.code.ViewportHeight(); got < 150 {
			t.Fatalf("narrow editor has only %.1f DIP of space", got)
		}
	}
}
