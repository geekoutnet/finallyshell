package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/zmodem"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

type fakeZmodemRunner struct {
	send    func([]string, func(zmodem.Progress)) error
	receive func(string, func(string, int64) bool, func(zmodem.Progress)) error
}

func (runner fakeZmodemRunner) Send(paths []string, progress func(zmodem.Progress)) error {
	return runner.send(paths, progress)
}

func (runner fakeZmodemRunner) Receive(dir string, accept func(string, int64) bool, progress func(zmodem.Progress)) error {
	return runner.receive(dir, accept, progress)
}

func TestZmodemActions(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		send, confirm, cancel, fail bool
	}{
		{name: "confirm upload", send: true, confirm: true},
		{name: "decline upload", send: true},
		{name: "decline download"},
		{name: "confirm download", confirm: true},
		{name: "cancel via row", confirm: true, cancel: true},
		{name: "download timeout", confirm: true, fail: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			a, platform := testApp(t)
			s := demoSession(a, "example", "192.0.2.2")
			dest := t.TempDir()
			platform.chooseFolder(dest)
			platform.opened = []string{filepath.Join(t.TempDir(), "example.txt")}
			platform.answer = scenario.confirm
			tester := ui.NewTester(func(c *ui.Context) { useTheme(c); a.transferPanel(c) }, 460, 250)
			platform.tt = tester
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := make(chan string, 1)
			result := make(chan error, 1)
			run := func(path string, report func(zmodem.Progress)) error {
				called <- path
				report(zmodem.Progress{Name: "example.txt", Done: 512, Total: 1024})
				if scenario.cancel {
					<-ctx.Done()
					return ctx.Err()
				}
				if scenario.fail {
					return zmodem.ErrTimeout
				}
				report(zmodem.Progress{Name: "example.txt", Done: 1024, Total: 1024})
				return nil
			}
			go func() {
				result <- a.zmodemSession(ctx, cancel, s, s.Host.ID, scenario.send, fakeZmodemRunner{send: func(paths []string, report func(zmodem.Progress)) error {
					return run(strings.Join(paths, "|"), report)
				}, receive: func(dir string, accept func(string, int64) bool, report func(zmodem.Progress)) error {
					if !accept("example.txt", 1024) {
						return context.Canceled
					}
					return run(dir, report)
				}})
			}()
			platform.until(t, "transfer row", func() bool { return len(a.transfers.jobs) == 1 })
			if scenario.cancel {
				platform.until(t, "fake transfer progress", func() bool { return a.transfers.jobs[0].done == 512 })
				tester.Frame()
				if err := tester.Click(locales.Cancel()); err != nil {
					t.Fatal(err)
				}
			}
			platform.until(t, "transfer result", a.transfers.jobs[0].finished)
			job := a.transfers.jobs[0]
			err := <-result
			questions := platform.questions()
			if len(questions) != 1 || !strings.Contains(questions[0].Message, "example") {
				t.Fatalf("missing host confirmation: %+v", questions)
			}
			if !scenario.send && !strings.Contains(questions[0].Message, "example.txt") {
				t.Fatalf("missing incoming filename: %+v", questions[0])
			}
			switch {
			case !scenario.confirm || scenario.cancel:
				if !errors.Is(err, context.Canceled) || job.state != transferCanceled {
					t.Fatalf("not canceled: %v / %v", err, job.state)
				}
			case scenario.fail:
				if !errors.Is(err, zmodem.ErrTimeout) || job.state != transferFailed || !tester.HasText(locales.TransferFailed(locales.ZmTimeout())) {
					t.Fatalf("missing timeout: %v / %v / %q", err, job.state, tester.Texts())
				}
				if retry, ok := tester.AccessByRole(ui.RoleButton, locales.Retry()); !ok || !retry.Disabled {
					t.Fatalf("retry must require restarting the shell command: %+v", retry)
				}
			default:
				if err != nil || job.state != transferDone || job.done != 1024 {
					t.Fatalf("failed: %v / %+v", err, job)
				}
			}
			select {
			case got := <-called:
				want := dest
				if scenario.send {
					want = platform.opened[0]
				}
				if !scenario.confirm || got != want || !strings.Contains(questions[0].Detail, filepath.Base(want)) {
					t.Fatalf("unexpected file: %q, question %+v", got, questions[0])
				}
			default:
				if scenario.confirm {
					t.Fatal("confirmed transfer did not run")
				}
			}
		})
	}
}

type streamFixture struct {
	*io.PipeReader
}

func (conn streamFixture) Write(data []byte) (int, error) { return len(data), nil }

func testStreamPane(t *testing.T, a *App, s *Session) (*terminalStream, *io.PipeWriter) {
	t.Helper()
	reader, writer := io.Pipe()
	stream := newTerminalStream(streamFixture{reader}, highlightRules(a.settings, s.Host.ID), nil)
	opts := a.terminalOptions(s.Host)
	opts.Conn = stream
	term, err := terminal.New(opts)
	if err != nil {
		stream.Close()
		writer.Close()
		t.Fatal(err)
	}
	s.setTerm(term)
	s.pane().stream.Store(stream)
	t.Cleanup(func() { term.Close(); writer.Close() })
	return stream, writer
}

func TestRecordingControls(t *testing.T) {
	a, platform := testApp(t)
	s := demoSession(a, "example", "192.0.2.2")
	a.active = 0
	stream, writer := testStreamPane(t, a, s)
	a.settings.Recording = &config.TerminalRecording{Folder: t.TempDir(), Text: true}
	tester := ui.NewTester(a.View, 1280, 820)
	platform.tt = tester
	tester.Frame()
	if err := tester.Click(locales.RecStart()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "declined recording", func() bool { return len(platform.questions()) == 1 && !stream.asking })
	if stream.recording() {
		t.Fatal("recording after decline")
	}
	platform.answer = true
	if err := tester.Click(locales.RecStart()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "recording started", func() bool { return stream.recording() && !stream.asking })
	_, _ = writer.Write([]byte("recorded example\n"))
	platform.until(t, "terminal output", func() bool { return strings.Contains(s.term().Text(), "recorded example") })
	if err := tester.Click(locales.RecStop()); err != nil {
		t.Fatal(err)
	}
	if stream.recording() {
		t.Fatal("recording did not stop")
	}
	files, _ := filepath.Glob(filepath.Join(a.settings.Recording.Folder, "*", "*.cast"))
	if len(files) != 1 {
		t.Fatalf("recording files: %v", files)
	}
	if data, err := os.ReadFile(files[0]); err != nil || !strings.Contains(string(data), "recorded example") {
		t.Fatalf("cast: %s, %v", data, err)
	}
}

func TestHighlightOptions(t *testing.T) {
	a, platform := testApp(t)
	a.openOptions()
	a.optionsNav.Page = pageHighlight
	tester := platform.show(t, WindowOptions)
	tester.Frame()
	if !tester.HasText(locales.HlPreview()) || !tester.HasText("warning") {
		t.Fatalf("no preview: %q", tester.Texts())
	}
	if err := tester.Click(locales.HlEnable()); err != nil {
		t.Fatal(err)
	}
	if cfg := config.LoadSettings().Highlight; cfg == nil || !cfg.Disabled {
		t.Fatal("highlight setting was not applied")
	}
	if err := tester.Click(locales.HlAdd()); err != nil {
		t.Fatal(err)
	}
	editor := platform.show(t, windowHighlightRule)
	editor.Frame()
	if button, ok := editor.AccessByRole(ui.RoleButton, locales.Save()); !ok || !button.Disabled {
		t.Fatalf("empty rule could be saved: %+v", button)
	}
	if err := editor.Click(locales.HlPattern()); err != nil {
		t.Fatal(err)
	}
	editor.Type("ready")
	if err := editor.Click(locales.Save()); err != nil {
		t.Fatal(err)
	}
	if cfg := config.LoadSettings().Highlight; cfg == nil || len(cfg.Rules) != 6 || cfg.Rules[5].Pattern != "ready" || !cfg.Disabled {
		t.Fatalf("custom rule was not saved: %+v", cfg)
	}
	backups, _ := filepath.Glob(filepath.Join(config.Dir(), "settings.json.bak-*"))
	if len(backups) != 1 {
		t.Fatalf("wanted one backup of existing settings: %v", backups)
	}
}

func TestTerminalStreamScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, platform := testApp(t)
			s := demoSession(a, "edge-demo", "192.0.2.20")
			stream, writer := testStreamPane(t, a, s)
			a.settings.Recording = &config.TerminalRecording{Folder: "C:\\Demo\\recordings"}
			tester := ui.NewTester(a.View, 1280, 820)
			platform.tt = tester
			platform.SetLocale(language)
			recording, err := newRecording(t.TempDir(), "edge-demo", 100, 28, 1<<20, false, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			stream.setRecording(recording)
			_, _ = writer.Write([]byte("ops@edge-demo:~$ cat demo.log\r\nerror: connection refused by 192.0.2.10\r\nwarning: retrying [2001:db8::4]\r\nsuccess: demo is active at https://example.invalid/status\r\nops@edge-demo:~$ "))
			platform.until(t, "rendered output", func() bool { return strings.Contains(s.term().Text(), "example.invalid/status") })
			a.transfers.jobs = []*transferJob{{id: 1, sess: s, zmodem: true, dir: download, src: "example.tar", dstDir: "C:\\Demo\\Downloads", state: transferRunning, done: 12 << 20, total: 32 << 20, speed: 3 << 20}}
			a.openOptions()
			options := platform.show(t, WindowOptions)
			for _, mode := range []string{"light", "dark"} {
				tester.SetDark(mode == "dark")
				options.SetDark(mode == "dark")
				shot := func(screen *ui.Tester, scene string) {
					screen.Frame()
					screen.Frame()
					saveShot(t, screen, "terminal-"+scene+"-"+language+"-"+mode+".png")
				}
				a.openBottomTab(tabTransfers)
				shot(tester, "short")
				a.openPanelTab(s, tabTransfers)
				shot(tester, "full")
				a.active = 0
				a.setPanelOnRight(true)
				shot(tester, "right")
				a.setPanelOnRight(false)
				a.optionsNav.Page = pageHighlight
				shot(options, "highlight-options")
				a.optionsNav.Page = pageRecording
				shot(options, "recording-options")
			}
		})
	}
}
