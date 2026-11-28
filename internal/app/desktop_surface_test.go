package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"finallyshell/internal/remotefs"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// desktopSurfaceFixture is a desktop over SFTP whose home has a Desktop
// folder with a text file, a script, a folder and a hidden file; or no
// Desktop folder, when bare.
func desktopSurfaceFixture(t *testing.T, bare bool) (*App, *fakePlatform, *ui.Tester, *Session, *desktopState, string) {
	t.Helper()
	local := t.TempDir()
	desk := filepath.Join(local, "Desktop")
	if !bare {
		if err := os.MkdirAll(filepath.Join(desk, "logs"), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"notes.txt": "hello\n", "deploy.sh": "#!/bin/sh\necho hi\n", ".hidden": "x"} {
			if err := os.WriteFile(filepath.Join(desk, name), []byte(value), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	a, platform, _, session := desktopShellTestApp(t)
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetMenuPresentation(ui.PopoverMenus)
		a.View(c)
	}, 1280, 820)
	platform.tt = tester
	server := sshtest.StartSFTP(t, filepath.ToSlash(local))
	session.client = server.Dial(t)
	session.Host.NoMonitor = true
	var err error
	session.sftp, err = remotefs.NewClient(session.client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.sftp.Close() })
	d := a.desktopOf(session)
	tester.Frame()
	if runtime.GOOS == "windows" {
		// The test server's home is C:/…, which is not a server's path:
		// the desktop's folder is set as it is on Linux.
		platform.until(t, "the home", func() bool { tester.Frame(); return d.files != nil && !d.files.explorer().loading })
		d.filesDir = "/" + filepath.ToSlash(desk)
		a.desktopSurfaceRead(d)
	}
	platform.until(t, "the desktop's folder", func() bool {
		tester.Frame()
		return d.filesDir != "" && d.files != nil && !d.files.explorer().loading
	})
	tester.Frame()
	return a, platform, tester, session, d, desk
}

func desktopSurfaceIDs(d *desktopState) []string {
	var out []string
	for _, icon := range desktopSurfaceIcons(d) {
		out = append(out, icon.App)
	}
	return out
}

func TestDesktopSurfaceShowsFiles(t *testing.T) {
	_, _, tt, _, d, _ := desktopSurfaceFixture(t, false)
	got := desktopSurfaceIDs(d)
	want := []string{"dir:logs", "file:deploy.sh", "file:notes.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("icons %v, want %v", got, want)
	}
	for _, name := range []string{"logs", "deploy.sh", "notes.txt"} {
		if !tt.HasText(name) {
			t.Fatalf("%s is not on the desktop", name)
		}
	}
	if tt.HasText(".hidden") {
		t.Fatal("a hidden file is on the desktop")
	}
	if !strings.HasSuffix(d.filesDir, "/Desktop") {
		t.Fatalf("folder %q", d.filesDir)
	}
}

func TestDesktopSurfaceOpen(t *testing.T) {
	a, p, _, s, d, _ := desktopSurfaceFixture(t, false)
	a.desktopOpenIcon(d, s, "dir:logs")
	if w := d.windowsOf(appFiles); len(w) != 1 {
		t.Fatal("a folder did not open in the file manager")
	}
	a.desktopOpenIcon(d, s, "file:notes.txt")
	p.until(t, "the editor", func() bool { return len(a.fe.tabs) == 1 })
	if !strings.HasSuffix(a.fe.tabs[0].path, "/Desktop/notes.txt") {
		t.Fatalf("opened %q", a.fe.tabs[0].path)
	}
}

func TestDesktopSurfaceNewFiles(t *testing.T) {
	a, p, tt, s, d, desk := desktopSurfaceFixture(t, true)
	if len(desktopSurfaceIcons(d)) != 0 {
		t.Fatal("files without a Desktop folder")
	}
	if _, err := os.Stat(desk); err == nil {
		t.Fatal("opening the desktop made its folder")
	}
	make := func(kind int, name string) {
		t.Helper()
		a.desktopSurfaceNew(d, s, kind)
		tt.Frame()
		f := d.files.explorer()
		if f.modal == nil {
			t.Fatal("no name asked")
		}
		f.modal.value = name
		desktopFilesTestConfirm(t, tt, locales.DesktopFilesCreate())
		p.until(t, name, func() bool {
			tt.Frame()
			return slices.Contains(desktopSurfaceIDs(d), "file:"+name) || slices.Contains(desktopSurfaceIDs(d), "dir:"+name)
		})
	}
	make(desktopNewText, "todo.txt")
	make(desktopNewScript, "run.sh")
	make(desktopNewDir, "backup")
	script, err := os.ReadFile(filepath.Join(desk, "run.sh"))
	if err != nil || string(script) != desktopScriptTemplate {
		t.Fatalf("script %q, %v", script, err)
	}
	if info, err := os.Stat(filepath.Join(desk, "backup")); err != nil || !info.IsDir() {
		t.Fatal("folder not made")
	}
	if !d.shell.selection["dir:backup"] {
		t.Fatal("the new folder is not selected")
	}
	// A name taken is refused.
	a.desktopSurfaceNew(d, s, desktopNewText)
	f := d.files.explorer()
	f.modal.value = "todo.txt"
	if f.modal.valid(f.modal) {
		t.Fatal("a name taken was accepted")
	}
	f.modal = nil
	// The menu makes them too.
	if !tt.HasText("run.sh") {
		t.Fatal("new script not shown")
	}
}

func TestDesktopSurfaceRunScript(t *testing.T) {
	item := desktopFilesItem{target: "/root/Desktop/deploy.sh"}
	item.Name, item.Mode = "deploy.sh", 0o644
	if !desktopSurfaceRunnable(item) {
		t.Fatal("a shell script is not runnable")
	}
	if got := desktopSurfaceRunCommand(item); got != `"$(command -v bash || command -v sh)" './deploy.sh'` {
		t.Fatalf("command %q", got)
	}
	item.Mode = 0o755
	if got := desktopSurfaceRunCommand(item); got != `'./deploy.sh'` {
		t.Fatalf("command %q", got)
	}
	text := desktopFilesItem{target: "/root/Desktop/a.txt"}
	text.Name, text.Mode = "a.txt", 0o644
	if desktopSurfaceRunnable(text) {
		t.Fatal("a text file is runnable")
	}
	state := &desktopTerminalState{dir: "/root/Desktop", command: "'./deploy.sh'"}
	if got := desktopTerminalCommand(desktopTerminalCD(state.dir), state); got != "cd '--' '/root/Desktop' && clear && './deploy.sh'\r" && !strings.HasSuffix(got, " && './deploy.sh'\r") {
		t.Fatalf("typed %q", got)
	}
	bad := &desktopTerminalState{dir: "relative", command: "x"}
	if got := desktopTerminalCommand(desktopTerminalCD(bad.dir), bad); got != "" {
		t.Fatalf("ran elsewhere than asked: %q", got)
	}
}

func TestDesktopSurfaceMenuRunsInTerminal(t *testing.T) {
	a, _, tt, s, d, _ := desktopSurfaceFixture(t, false)
	var opened any
	desktopAppByID(appTerminal).opened = func(_ *App, _ *desktopWindow, arg any) { opened = arg }
	pt := desktopSurfacePoint(t, a, s, "file:deploy.sh")
	x, y := desktopShellPoint(d, pt)
	tt.RightClickAt(x, y)
	if err := tt.ChooseMenuItem(locales.DesktopShellRunInTerminal()); err != nil {
		t.Fatal(err)
	}
	ws := d.windowsOf(appTerminal)
	if len(ws) != 1 {
		t.Fatal("no terminal")
	}
	run, ok := opened.(desktopTerminalRun)
	if !ok || !strings.HasSuffix(run.dir, "/Desktop") || !strings.Contains(run.command, "deploy.sh") {
		t.Fatalf("terminal opened with %#v", opened)
	}
}

func TestDesktopSurfaceCopyPasteRenameDelete(t *testing.T) {
	a, p, tt, s, d, desk := desktopSurfaceFixture(t, false)
	// Copy with the keys, paste on the desktop: a second copy.
	pt := desktopSurfacePoint(t, a, s, "file:notes.txt")
	x, y := desktopShellPoint(d, pt)
	tt.ClickAt(x, y)
	if !d.shell.selection["file:notes.txt"] {
		t.Fatal("not selected")
	}
	tt.Key(ui.Cmd, ui.KeyC)
	if desktopFilesClip == nil || !strings.HasSuffix(desktopFilesClip.paths[0], "/Desktop/notes.txt") {
		t.Fatalf("Ctrl+C did not copy: %+v", desktopFilesClip)
	}
	// The test server runs no commands: cp is done here, as the file
	// manager's own tests do.
	f := d.files.explorer()
	var commands []string
	f.run = func(_ context.Context, command string, _ func(string)) (string, string, error) {
		commands = append(commands, command)
		if !strings.Contains(command, "'cp'") {
			return "", "unexpected command", errors.New("unexpected command")
		}
		data, err := os.ReadFile(filepath.Join(desk, "notes.txt"))
		if err != nil {
			return "", "", err
		}
		return "", "", os.WriteFile(filepath.Join(desk, "notes (1).txt"), data, 0o644)
	}
	tt.Key(ui.Cmd, ui.KeyV)
	p.until(t, "paste", func() bool {
		tt.Frame()
		if f.conflict != nil {
			// The file is there: keep both.
			if err := tt.Click(locales.DesktopFilesKeepBoth()); err != nil {
				t.Fatal(err)
			}
		}
		if f.conflict == nil && f.modal != nil && f.job == nil {
			if err := tt.Click(f.modal.action); err != nil { // confirm the paste
				t.Fatal(err)
			}
		}
		return len(desktopSurfaceIcons(d)) == 4
	})
	// Rename with F2.
	clear(d.shell.selection)
	tt.ClickAt(x, y)
	tt.Key(0, ui.KeyF2)
	tt.Frame()
	if f.modal == nil {
		t.Fatal("F2 did not ask for a name")
	}
	f.modal.value = "renamed.txt"
	desktopFilesTestConfirm(t, tt, locales.DesktopFilesConfirmRename())
	p.until(t, "rename", func() bool {
		tt.Frame()
		return slices.Contains(desktopSurfaceIDs(d), "file:renamed.txt")
	})
	if _, err := os.Stat(filepath.Join(desk, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	// Delete: asks, then the file goes.
	clear(d.shell.selection)
	pt = desktopSurfacePoint(t, a, s, "file:renamed.txt")
	x, y = desktopShellPoint(d, pt)
	tt.ClickAt(x, y)
	tt.Key(ui.Shift, ui.KeyDelete)
	tt.Frame()
	if f.modal == nil {
		t.Fatal("delete did not ask")
	}
	desktopFilesTestConfirm(t, tt, f.modal.action)
	p.until(t, "delete", func() bool {
		tt.Frame()
		return !slices.Contains(desktopSurfaceIDs(d), "file:renamed.txt")
	})
	if _, err := os.Stat(filepath.Join(desk, "renamed.txt")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
}

// desktopSurfacePoint is the middle of the desktop's file icon id.
func desktopSurfacePoint(t *testing.T, a *App, s *Session, id string) ui.Point {
	t.Helper()
	d, h := a.desktopOf(s), a.desktopHost(s)
	g := desktopMakeGrid(d.area, a.desktopPreferences().IconSize)
	for _, cell := range g.layout(s, h, append(slices.Clone(a.desktopHostIcons(s, h)), desktopSurfaceIcons(d)...)) {
		if cell.icon.App == id {
			return ui.Point{X: cell.rect.X + cell.rect.W/2, Y: cell.rect.Y + cell.rect.H/2}
		}
	}
	t.Fatalf("missing icon %q", id)
	return ui.Point{}
}
