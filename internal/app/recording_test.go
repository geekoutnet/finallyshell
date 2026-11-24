package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"
)

func TestRecordingCast(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1000, 0)
	recorder, err := newRecording(dir, "demo", 90, 24, 512, true, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.output([]byte("\x1b[31mhello \xe4\xb8"), start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.output([]byte("\xad\x1b[0m\r\n\x1b]52;c;hidden\x07"), start.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.write("r", "100x32", start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	cast, _ := filepath.Glob(filepath.Join(dir, "*.cast"))
	if len(cast) != 1 {
		t.Fatalf("cast files: %v", cast)
	}
	data, err := os.ReadFile(cast[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	var header map[string]any
	if err := json.Unmarshal(lines[0], &header); err != nil || header["version"] != float64(2) || header["width"] != float64(90) || header["timestamp"] != float64(1000) {
		t.Fatalf("header %v: %v", header, err)
	}
	if len(lines) != 4 || !bytes.HasPrefix(lines[1], []byte(`[1,"o",`)) || !bytes.Contains(lines[2], []byte("中")) || string(lines[3]) != `[3,"r","100x32"]` || bytes.Contains(data, []byte("hidden")) {
		t.Fatalf("events: %s", data)
	}
	plain, err := os.ReadFile(strings.TrimSuffix(cast[0], ".cast") + ".txt")
	if err != nil || string(plain) != "hello 中\n" {
		t.Fatalf("plain: %q, %v", plain, err)
	}
}

func TestRecordingRotation(t *testing.T) {
	dir := t.TempDir()
	start := time.Unix(1000, 0)
	recorder, err := newRecording(dir, "demo", 80, 24, 512, false, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.output([]byte(strings.Repeat("界\\p abcdefg\n", 300)), start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.cast"))
	if len(files) < 2 {
		t.Fatalf("no rotation: %v", files)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 512 {
			t.Fatalf("%s: length=%d, %v", path, len(data), err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
			if !json.Valid(line) {
				t.Fatalf("split event in %s: %q", path, line)
			}
		}
	}
}

func TestRecordingAutomaticOption(t *testing.T) {
	a, platform := testApp(t)
	a.openOptions()
	a.optionsNav.Page = pageRecording
	tester := platform.show(t, WindowOptions)
	tester.Frame()
	if config.LoadSettings().Recording != nil || !tester.HasText(locales.RecPrivacy()) {
		t.Fatal("recording should be off by default and display a privacy note")
	}
	platform.answer = true
	if err := tester.Click(locales.RecAutomatic()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "automatic recording", func() bool { return a.recordingSettings().Automatic })
	if len(platform.questions()) != 1 {
		t.Fatal("missing automatic recording confirmation")
	}
	if err := tester.Click(locales.RecPlain()); err != nil {
		t.Fatal(err)
	}
	if cfg := config.LoadSettings().Recording; cfg == nil || !cfg.Automatic || !cfg.Text {
		t.Fatalf("recording settings were not saved: %+v", cfg)
	}
}
