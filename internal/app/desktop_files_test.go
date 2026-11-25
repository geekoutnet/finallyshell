package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"weak"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func desktopFilesFixture(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *desktopWindow, string, string) {
	t.Helper()
	local := t.TempDir()
	for _, name := range []string{"operations", "reports", "resources"} {
		if err := os.Mkdir(filepath.Join(local, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{"deploy.sh": "#!/bin/sh\n", "server.conf": "pages = 4\n", "README.md": "# A test fixture\n", "server.tar.gz": "not an archive in this fixture", "service.json": "{}\n", "reports/notes.txt": "sample"} {
		if err := os.WriteFile(filepath.Join(local, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, platform, _, session := desktopTestApp(t)
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
	remote := filepath.ToSlash(local)
	if !strings.HasPrefix(remote, "/") {
		remote = "/" + remote
	}
	window := a.desktopOpen(session, appFiles, remote)
	platform.until(t, "initial directory", func() bool { return !window.explorer().loading })
	if window.explorer().err != nil {
		t.Fatal(window.explorer().err)
	}
	tester.Frame()
	return a, platform, tester, session, window, local, remote
}

func desktopFilesTestItem(t *testing.T, f *desktopFilesState, name string) desktopFilesItem {
	t.Helper()
	for _, item := range f.entries {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing item %s in %+v", name, f.entries)
	return desktopFilesItem{}
}

func desktopFilesTestConfirm(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestDesktopFilesNavigationAndChanges(t *testing.T) {
	a, platform, tester, session, window, local, remote := desktopFilesFixture(t)
	f := window.explorer()
	if _, ok := tester.Find("server.conf"); !ok {
		t.Fatal("directory did not render in the desktop")
	}
	if err := tester.Click("reports"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnter)
	platform.until(t, "reports", func() bool { return !f.loading && f.dir == path.Join(remote, "reports") })
	tester.Key(ui.Alt, ui.KeyLeft)
	if !f.loading {
		a.desktopFilesHistory(window, 0)
		t.Error("Alt+Left did not start navigation")
	}
	platform.until(t, "back", func() bool { return !f.loading && f.dir == remote })
	a.desktopFilesHistory(window, 1)
	platform.until(t, "forward", func() bool { return !f.loading && f.dir == path.Join(remote, "reports") })
	a.desktopFilesUp(window)
	platform.until(t, "parent", func() bool { return !f.loading && f.dir == remote })

	a.desktopFilesNew(window, true)
	f.inline.value = "created folder"
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if _, err := os.Stat(filepath.Join(local, "created folder")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancel changed the remote directory: %v", err)
	}
	a.desktopFilesNew(window, true)
	f.inline.value = "created folder"
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesCreate())
	platform.until(t, "new folder", func() bool { return f.job == nil && !f.loading })
	if f.err != nil {
		t.Fatal(f.err)
	}
	created := desktopFilesTestItem(t, f, "created folder")
	a.desktopFilesNew(window, false)
	f.inline.value = "note.txt"
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesCreate())
	platform.until(t, "new text file", func() bool { return f.job == nil && !f.loading })
	a.desktopFilesNote(session, created.target, "A preserved note")
	a.desktopFilesFavorite(session, created.target, true)
	a.desktopFilesRename(window, created)
	f.inline.value = "renamed"
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmRename())
	platform.until(t, "rename", func() bool { return f.job == nil && !f.loading })
	renamed := desktopFilesTestItem(t, f, "renamed")
	if got := config.LoadSettings().Desktop.Hosts[session.Host.ID]; got.Notes[renamed.target] != "A preserved note" || !slices.Contains(got.Favorites, renamed.target) {
		t.Fatalf("saved settings = %+v", got)
	}
	for index := range 22 {
		a.desktopFilesRecent(session, path.Join(remote, fmt.Sprint(index)))
	}
	if recent := a.desktopFilesHost(session).Recent; len(recent) != 20 || recent[0] != path.Join(remote, "21") {
		t.Fatalf("recent = %v", recent)
	}

	a.desktopFilesDelete(window, []desktopFilesItem{renamed}, true)
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if _, err := os.Stat(filepath.Join(local, "renamed")); err != nil {
		t.Fatal(err)
	}
	a.desktopFilesDelete(window, []desktopFilesItem{renamed}, true)
	if !strings.Contains(f.modal.text, renamed.target) || !strings.Contains(f.modal.text, locales.DesktopFilesPermanentWarning()) {
		t.Fatal("permanent removal does not identify its target and consequence")
	}
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmDeletion())
	platform.until(t, "permanent deletion", func() bool { return f.job == nil && !f.loading })
	if _, err := os.Stat(filepath.Join(local, "renamed")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("not deleted: %v", err)
	}
	if entries := a.desktopOf(session).log; len(entries) != 4 || entries[len(entries)-1].err != "" {
		t.Fatalf("operation log = %+v", entries)
	}
}

func TestDesktopFilesClipboardConflicts(t *testing.T) {
	a, platform, tester, session, window, local, remote := desktopFilesFixture(t)
	f := window.explorer()
	f.mountsRead = true
	dir := path.Join(remote, "operations")
	a.desktopFilesRead(window, dir, false, true)
	platform.until(t, "operations folder", func() bool { return !f.loading })
	var commands []string
	f.run = func(_ context.Context, command string, _ func(string)) (string, string, error) {
		commands = append(commands, command)
		for _, target := range []string{"README.md", "README (1).md", "service.json"} {
			switch {
			case strings.HasSuffix(command, relayQuote("cp", "-a", "-nT", "--", path.Join(remote, "README.md"), path.Join(dir, target))):
				return "", "", os.WriteFile(filepath.Join(local, "operations", target), []byte("copied contents"), 0o644)
			case strings.HasSuffix(command, relayQuote("mv", "-nT", "--", path.Join(remote, target), path.Join(dir, target))):
				return "", "", os.Rename(filepath.Join(local, target), filepath.Join(local, "operations", target))
			}
		}
		return "", "unexpected command: " + command, errors.New("unexpected command")
	}
	copyClip := &desktopFilesClipboard{owner: weak.Make(a), s: session, paths: []string{path.Join(remote, "README.md")}}
	start := func(clip *desktopFilesClipboard) {
		a.desktopFilesPasteRemote(window, clip)
		platform.until(t, "paste plan", func() bool { return f.job == nil })
	}
	start(copyClip)
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if len(commands) != 0 {
		t.Fatal("cancel ran a command")
	}
	start(copyClip)
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmPaste())
	platform.until(t, "copy", func() bool { return f.job == nil && !f.loading })
	if f.err != nil || len(commands) != 1 {
		t.Fatalf("copy error: %v, commands: %q", f.err, commands)
	}
	start(copyClip)
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesKeepBoth())
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmPaste())
	platform.until(t, "keep both", func() bool { return f.job == nil && !f.loading })
	if _, err := os.Stat(filepath.Join(local, "operations", "README (1).md")); err != nil {
		t.Fatal(err)
	}
	cut := &desktopFilesClipboard{owner: weak.Make(a), s: session, paths: []string{path.Join(remote, "service.json")}, cut: true}
	desktopFilesClip = cut
	t.Cleanup(func() { desktopFilesClip = nil })
	start(cut)
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmPaste())
	platform.until(t, "cut", func() bool { return f.job == nil && !f.loading })
	if desktopFilesClip != nil || len(commands) != 3 || f.err != nil {
		t.Fatalf("cut: %v; %q; %v", desktopFilesClip, commands, f.err)
	}
	if _, err := os.Stat(filepath.Join(local, "service.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cut did not move the source: %v", err)
	}
	start(copyClip)
	desktopFilesTestConfirm(t, tester, locales.Replace())
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmPaste())
	platform.until(t, "replacement", func() bool { return f.job == nil && !f.loading })
	matches, err := filepath.Glob(filepath.Join(local, "operations", "README.md.bak-*"))
	if err != nil || len(matches) != 1 || f.err != nil {
		t.Fatalf("replacement: %q, %v, %v", matches, err, f.err)
	}
}

func TestDesktopFilesSearchAndProperties(t *testing.T) {
	a, platform, tester, _, window, _, remote := desktopFilesFixture(t)
	f := window.explorer()
	var calls []string
	entry := path.Join(remote, "reports", "notes.txt")
	f.mountsRead = true
	f.run = func(ctx context.Context, command string, _ func(string)) (string, string, error) {
		calls = append(calls, command)
		if command == desktopFilesSearchCommand(remote, "notes") {
			return strings.Join([]string{"f", "6", "1730000000", "644", "1000", "1000", entry, ""}, "\x00"), "", nil
		}
		if command == desktopFilesSearchCommand(remote, "wait") {
			<-ctx.Done()
			return "", "", ctx.Err()
		}
		if command == desktopFilesModeCommand(entry, "0740", false) {
			return "", "", nil
		}
		return "", "expected fake error", errors.New("denied")
	}
	a.desktopFilesSearch(window, "wait")
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	platform.until(t, "cancel search", func() bool { return !f.loading })
	if f.err != nil {
		t.Fatal(f.err)
	}
	a.desktopFilesSearch(window, "notes")
	platform.until(t, "search results", func() bool { return !f.loading })
	if item := desktopFilesTestItem(t, f, "notes.txt"); item.target != entry || item.UID != 1000 {
		t.Fatalf("search result = %+v", item)
	}
	a.desktopFilesAskProperties(window, f.entries[0])
	props := f.modal.properties
	platform.until(t, "properties read", func() bool { return !props.reading })
	props.octal = "0740"
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesApply())
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if len(calls) != 2 {
		t.Fatalf("cancel issued a command: %v", calls)
	}
	a.desktopFilesAskProperties(window, f.entries[0])
	props = f.modal.properties
	platform.until(t, "properties read again", func() bool { return !props.reading })
	props.octal = "0740"
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesApply())
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesApply())
	platform.until(t, "confirmed chmod", func() bool { return !f.loading && f.job == nil })
	if desktopFilesModeCommand(entry, "0740", false) != calls[2] || f.err != nil {
		t.Fatalf("apply: %v, commands %v", f.err, calls)
	}
}

func TestDesktopFilesUploadFromClipboard(t *testing.T) {
	a, platform, tester, session, window, local, _ := desktopFilesFixture(t)
	target := filepath.Join(t.TempDir(), "local-notes.txt")
	if err := os.WriteFile(target, []byte("local file"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := transfer.FileData(target)
	if err != nil {
		t.Fatal(err)
	}
	tester.SetClipboardData(data)
	desktopFilesTestConfirm(t, tester, "server.conf")
	tester.Key(ui.Cmd, ui.KeyV)
	if window.explorer().modal == nil {
		t.Fatal("clipboard paste did not ask before uploading")
	}
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if a.transfers.active() != 0 {
		t.Fatal("canceled upload queued a transfer")
	}
	tester.Key(ui.Cmd, ui.KeyV)
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesStartUpload())
	platform.until(t, "upload completed and logged", func() bool { return len(a.desktopOf(session).log) == 1 && !window.explorer().loading })
	contents, err := os.ReadFile(filepath.Join(local, "local-notes.txt"))
	if err != nil || string(contents) != "local file" {
		t.Fatalf("upload = %q, %v", contents, err)
	}
}

func TestDesktopFilesOwnership(t *testing.T) {
	a, platform, tester, session, window, _, remote := desktopFilesFixture(t)
	f := window.explorer()
	f.mountsRead = true
	f.run = func(_ context.Context, command string, _ func(string)) (string, string, error) {
		if command != relayQuote("du", "-sb", "--", path.Join(remote, "reports")) {
			t.Errorf("unexpected unprivileged command: %q", command)
		}
		return "6\tfixture\n", "", nil
	}
	var commands []string
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root"}, nil
		}
		commands = append(commands, request.Action)
		return monitor.PrivilegeResult{Started: true, Stderr: "fixture denied ownership change"}, errors.New("denied")
	}
	item := desktopFilesTestItem(t, f, "reports")
	ask := func() {
		a.desktopFilesAskProperties(window, item)
		p := f.modal.properties
		platform.until(t, "directory properties", func() bool { return !p.reading && !p.measuring })
		p.owner, p.group, p.recursive = "9912", "9913", true
		desktopFilesTestConfirm(t, tester, locales.DesktopFilesApply())
	}
	ask()
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if len(commands) != 0 {
		t.Fatal("cancel changed ownership")
	}
	ask()
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesApply())
	platform.until(t, "ownership failure", func() bool { return f.job == nil && !f.loading })
	if len(commands) != 1 || commands[0] != relayQuote("chown", "-h", "-R", "-P", "--", "9912:9913", item.target) {
		t.Fatalf("ownership commands: %q", commands)
	}
	if f.err == nil || !strings.Contains(f.err.Error(), "fixture denied ownership change") || !tester.HasText(f.err.Error()) {
		t.Fatalf("ownership error not shown: %v", f.err)
	}
}

func TestDesktopFilesArchivesAndFetch(t *testing.T) {
	a, platform, tester, _, window, local, remote := desktopFilesFixture(t)
	f := window.explorer()
	f.mountsRead = true
	const raw = "https://example.org/a'b?q=1&n=2"
	var calls []string
	f.run = func(_ context.Context, cmd string, report func(string)) (string, string, error) {
		calls = append(calls, cmd)
		stages, err := filepath.Glob(filepath.Join(local, ".finallyshell-explorer-*"))
		if err != nil || len(stages) != 1 {
			return "", "", fmt.Errorf("expected one stage, got %v: %v", stages, err)
		}
		stage := path.Join(remote, filepath.Base(stages[0]))
		switch cmd {
		case desktopFilesFetchCommand(raw, path.Join(stage, "download.bin")):
			report("#### 50.0%\r")
			return "", "", os.WriteFile(filepath.Join(stages[0], "download.bin"), []byte("download"), 0o600)
		case desktopFilesCompressCommand(".tar.gz", remote, path.Join(stage, "docs.tar.gz"), []string{"README.md"}):
			return "", "", os.WriteFile(filepath.Join(stages[0], "docs.tar.gz"), []byte("archive"), 0o600)
		case desktopFilesTool("cp") + relayQuote("cp", "--", path.Join(remote, "docs.tar.gz"), path.Join(stage, "docs.tar.gz")):
			return "", "", os.WriteFile(filepath.Join(stages[0], "docs.tar.gz"), []byte("archive"), 0o600)
		case desktopFilesTool("tar") + "unset TAR_OPTIONS\n" + relayQuote("tar", "--quoting-style=escape", "-tvf", path.Join(stage, "docs.tar.gz")):
			report("-rw-r--r-- root/root         4 2026-11-25 12:30 output.txt\n")
			return "", "", nil
		case desktopFilesExtractCommand(path.Join(stage, "docs.tar.gz"), path.Join(stage, "data")):
			return "", "", os.WriteFile(filepath.Join(stages[0], "data", "output.txt"), []byte("extracted"), 0o600)
		default:
			return "", "", fmt.Errorf("unexpected fake command: %s", cmd)
		}
	}
	finish := func(action, filename, content string, count int) {
		t.Helper()
		tester.Frame()
		button, ok := tester.AccessByRole(ui.RoleButton, action)
		if !ok {
			t.Fatalf("missing action button %q", action)
		}
		tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
		tester.Frame()
		if f.modal != nil {
			t.Fatalf("%s left the prompt open: title %q, value %q", action, f.modal.title, f.modal.value)
		}
		platform.until(t, action, func() bool { return f.job == nil && !f.loading })
		actual, err := os.ReadFile(filepath.Join(local, filename))
		if f.err != nil || err != nil || string(actual) != content || len(calls) != count {
			t.Fatalf("%s: %q, %v, %v, commands %q", action, actual, err, f.err, calls)
		}
	}
	a.desktopFilesFetch(window)
	f.modal.value, f.modal.second = raw, "download.bin"
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if len(calls) != 0 {
		t.Fatal("cancel ran a download command")
	}
	a.desktopFilesFetch(window)
	f.modal.value, f.modal.second = raw, "download.bin"
	finish(locales.Download(), "download.bin", "download", 1)
	a.desktopFilesCompress(window, []desktopFilesItem{desktopFilesTestItem(t, f, "README.md")}, ".tar.gz")
	f.modal.value = "docs.tar.gz"
	finish(locales.DesktopFilesCompress(), "docs.tar.gz", "archive", 2)
	item := desktopFilesTestItem(t, f, "docs.tar.gz")
	a.desktopFilesAskExtract(window, item, false)
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if len(calls) != 2 {
		t.Fatal("cancel ran an extraction command")
	}
	a.desktopFilesAskExtract(window, item, false)
	finish(locales.DesktopFilesExtractHere(), "output.txt", "extracted", 5)
	if stages, err := filepath.Glob(filepath.Join(local, ".finallyshell-explorer-*")); err != nil || len(stages) > 0 {
		t.Fatalf("leftover stages: %q, %v", stages, err)
	}
	started := make(chan struct{})
	f.run = func(ctx context.Context, _ string, _ func(string)) (string, string, error) {
		close(started)
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	a.desktopFilesFetch(window)
	f.modal.value, f.modal.second = raw, "canceled.bin"
	desktopFilesTestConfirm(t, tester, locales.Download())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	platform.until(t, "download cancellation", func() bool { return f.job == nil && !f.loading })
	if _, err := os.Stat(filepath.Join(local, "canceled.bin")); f.err != nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("canceled download published a file: %v, %v", f.err, err)
	}
}

func TestDesktopFilesThisPC(t *testing.T) {
	a, platform, tester, session, _, _, remote := desktopFilesFixture(t)
	pc := a.desktopOpen(session, appThisPC, nil)
	f := pc.explorer()
	platform.until(t, "This PC quick access", func() bool { return !f.loading })
	f.run = func(_ context.Context, script string, _ func(string)) (string, string, error) {
		if script != desktopFilesMountCommand {
			t.Errorf("mount command = %q", script)
		}
		return "/dev/sda1 ext4 1024 512 500 51% " + remote + "\n", "", nil
	}
	a.desktopFilesRefresh(pc)
	platform.until(t, "This PC drives", func() bool { return len(f.drives) == 1 })
	if _, ok := tester.Find(locales.DesktopPCQuick()); !ok {
		t.Fatal("quick access was not rendered")
	}
	if err := tester.Click("/dev/sda1"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnter)
	windows := a.desktopOf(session).windowsOf(appFiles)
	if len(windows) != 2 {
		t.Fatalf("drive tile opened %d Files windows, want 2", len(windows))
	}
	opened := windows[1].explorer()
	platform.until(t, "mount directory", func() bool { return !opened.loading })
	if opened.dir != remote || opened.err != nil {
		t.Fatalf("drive opened %q: %v", opened.dir, opened.err)
	}
}

func TestDesktopFilesCrossServerPaste(t *testing.T) {
	a, platform, tester, source, _, sourceLocal, sourceRemote := desktopFilesFixture(t)
	local := t.TempDir()
	remote := filepath.ToSlash(local)
	if !strings.HasPrefix(remote, "/") {
		remote = "/" + remote
	}
	server := sshtest.StartSFTP(t, filepath.ToSlash(local))
	target := demoSession(a, "web-2", server.Host)
	target.Host.NoMonitor = true
	target.client = server.Dial(t)
	var err error
	target.sftp, err = remotefs.NewClient(target.client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.sftp.Close() })
	a.openDesktop(target)
	window := a.desktopOpen(target, appFiles, remote)
	f := window.explorer()
	platform.until(t, "destination listing", func() bool { return !f.loading })
	clip := &desktopFilesClipboard{owner: weak.Make(a), s: source, paths: []string{path.Join(sourceRemote, "reports")}, cut: true}
	a.desktopFilesPasteRemote(window, clip)
	platform.until(t, "relay confirmation", func() bool { return f.modal != nil })
	desktopFilesTestConfirm(t, tester, locales.Cancel())
	if a.transfers.active() != 0 {
		t.Fatal("cancel queued a relay")
	}
	a.desktopFilesPasteRemote(window, clip)
	platform.until(t, "relay confirmation again", func() bool { return f.modal != nil })
	desktopFilesTestConfirm(t, tester, locales.DesktopFilesConfirmPaste())
	platform.until(t, "relay logged and refreshed", func() bool { return len(a.desktopOf(target).log) == 1 && !f.loading })
	got, err := os.ReadFile(filepath.Join(local, "reports", "notes.txt"))
	if f.err != nil || err != nil || string(got) != "sample" {
		t.Fatalf("relay result: %q, %v, %v", got, err, f.err)
	}
	if _, err := os.Stat(filepath.Join(sourceLocal, "reports", "notes.txt")); err != nil {
		t.Fatalf("cross-server paste modified the source: %v", err)
	}
}

func TestDesktopFilesSelectionAndMarquee(t *testing.T) {
	_, _, tester, _, window, _, _ := desktopFilesFixture(t)
	f := window.explorer()
	if err := tester.Click("operations"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ClickWith(ui.Cmd, "reports"); err != nil {
		t.Fatal(err)
	}
	if f.selected.Len() != 2 {
		t.Fatalf("modified click selected %d items", f.selected.Len())
	}
	tester.Key(ui.Cmd, ui.KeyA)
	if f.selected.Len() != len(f.entries) {
		t.Fatalf("select all selected %d of %d items", f.selected.Len(), len(f.entries))
	}
	f.selected.Clear()
	tester.Frame()
	row, ok := tester.Find("README.md")
	if !ok {
		t.Fatal("README row not shown")
	}
	tester.RightClickAt(row.X+10, row.Y+row.H/2)
	if !slices.Contains(tester.Menu(), locales.DesktopFilesProperties()) || f.selected.Len() != 1 {
		t.Fatalf("right-click did not select and open the item menu: %q", tester.Menu())
	}
	tester.CloseMenu()
	f.view = desktopFilesLarge
	f.selected.Clear()
	tester.Frame()
	first, ok := tester.Find("operations")
	if !ok {
		t.Fatal("operations icon not shown")
	}
	last, ok := tester.Find("reports")
	if !ok {
		t.Fatal("reports icon not shown")
	}
	tester.Press(first.X+8, first.Y+220)
	pressed := f.marquee.active
	tester.Move(last.X+10, first.Y-30)
	during := f.marquee
	tester.Release(last.X+10, first.Y-30)
	if f.selected.Len() < 2 {
		t.Fatalf("rubber band selected %d items; pressed=%v first=%+v last=%+v drag=%+v", f.selected.Len(), pressed, first, last, during)
	}
}

func TestDesktopFilesScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("set SHOT_DIR to save frames")
	}
	a, platform, tester, session := desktopTestApp(t)
	renderServer := sshtest.StartSFTP(t, filepath.ToSlash(t.TempDir()))
	var err error
	session.sftp, err = remotefs.NewClient(renderServer.Dial(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.sftp.Close() })
	tester = ui.NewTester(func(c *ui.Context) {
		c.SetMenuPresentation(ui.PopoverMenus)
		a.View(c)
	}, 1280, 820)
	platform.tt = tester
	tester.Frame()
	pc := a.desktopOpen(session, appThisPC, nil)
	w := a.desktopOpen(session, appFiles, "/srv/sites")
	for _, window := range []*desktopWindow{pc, w} {
		f := window.explorer()
		f.readID++
		f.loading = false
		f.err, f.home, f.mountsRead = nil, "/home/deploy", true
		f.run = func(context.Context, string, func(string)) (string, string, error) { return "", "", nil }
		f.quick = []string{"/home/deploy", "/var/log", "/etc", "/var/www", "/opt", "/tmp"}
		f.drives = []desktopFilesDrive{
			{"/dev/vda1", "ext4", "/", 42_153_565_000, 15_671_282_000, 63},
			{"/dev/vdb1", "xfs", "/srv", 107_374_182_400, 11_696_685_056, 89},
		}
	}
	f := w.explorer()
	w.title = "sites"
	f.dir = "/srv/sites"
	stamp := time.Date(2026, time.November, 25, 14, 30, 0, 0, time.Local)
	for index, name := range []string{"public", "releases", "logs", "site.conf", "README.md", "backup.tar.gz", "index.html", "server.go", "favicon.png", "data.json", "deploy.sh", "service.log"} {
		mode := fs.FileMode(0o644)
		if index < 3 {
			mode = fs.ModeDir | 0o755
		}
		f.entries = append(f.entries, desktopFilesItem{remotefs.Entry{Name: name, Mode: mode, Size: int64(index) * 8146, ModTime: stamp.Add(time.Duration(index) * time.Minute), UID: 1000, GID: 1000}, path.Join(f.dir, name)})
	}
	f.history = []string{"", "/srv", f.dir}
	f.historyAt = 2
	a.desktopFilesFavorite(session, "/srv/sites", true)
	a.desktopFilesFavorite(session, "/var/log", true)
	a.desktopFilesRecent(session, "/srv/sites/public")
	a.desktopFilesRecent(session, "/srv/sites/README.md")
	a.desktopFilesNote(session, "/srv/sites/site.conf", "Production virtual host")
	_ = a.fileIconTheme()
	platform.until(t, "file icon theme", func() bool { return a.iconThemes.loaded })
	for _, language := range []string{"en", "zh-Hans"} {
		platform.SetLocale(language)
		for _, appearance := range []string{"light", "dark"} {
			tester.SetDark(appearance == "dark")
			for _, size := range []string{"regular", "small"} {
				for _, window := range []*desktopWindow{pc, w} {
					window.x, window.y, window.w, window.h = 62, 35, 910, 605
					if size == "small" {
						window.w, window.h = window.app.minSize.W, window.app.minSize.H
					}
				}
				suffix := "-" + language + "-" + appearance + "-" + size + ".png"
				a.desktopFocus(pc)
				pc.minimized, w.minimized = false, true
				tester.Frame()
				saveShot(t, tester, "desktop-pc"+suffix)
				a.desktopFocus(w)
				pc.minimized = true
				for view, name := range []string{"details", "list", "medium", "large"} {
					f.view = view
					f.list.ScrollTo(0, ui.Start)
					f.grid.ScrollTo(0, ui.Start)
					f.selected.Clear()
					f.selected.Add("/srv/sites/site.conf")
					tester.Frame()
					saveShot(t, tester, "desktop-files-"+name+suffix)
				}
				f.view = desktopFilesDetails
				f.list.ScrollTo(0, ui.Start)
				tester.Frame()
				row, ok := tester.Find("backup.tar.gz")
				if !ok {
					t.Fatal("archive row not visible")
				}
				tester.RightClickAt(row.X+8, row.Y+row.H/2)
				if menu := tester.Menu(); !slices.Contains(menu, locales.DesktopFilesProperties()) {
					t.Fatalf("no item context menu at %+v for %s: %q", row, suffix, menu)
				}
				saveShot(t, tester, "desktop-files-menu"+suffix)
				tester.CloseMenu()
				item := desktopFilesTestItem(t, f, "site.conf")
				info := desktopFilesDemoInfo{item.Entry}
				f.modal = &desktopFilesPrompt{properties: &desktopFilesProperties{item: item, info: info, bytes: item.Size, accessed: stamp,
					owner: "1000", group: "1000", octal: "0644", note: a.desktopFilesHost(session).Notes[item.target], cancel: func() {}}}
				tester.Frame()
				saveShot(t, tester, "desktop-files-properties"+suffix)
				f.modal = nil
				f.conflict = &desktopFilesConflict{clip: &desktopFilesClipboard{s: session}, items: []desktopFilesPasteItem{
					{from: "/home/deploy/site.conf", to: item.target, existing: info},
				}}
				tester.Frame()
				saveShot(t, tester, "desktop-files-conflict"+suffix)
				f.conflict = nil
			}
		}
	}
}

type desktopFilesDemoInfo struct{ entry remotefs.Entry }

func (info desktopFilesDemoInfo) Name() string       { return info.entry.Name }
func (info desktopFilesDemoInfo) Size() int64        { return info.entry.Size }
func (info desktopFilesDemoInfo) Mode() fs.FileMode  { return info.entry.Mode }
func (info desktopFilesDemoInfo) ModTime() time.Time { return info.entry.ModTime }
func (info desktopFilesDemoInfo) IsDir() bool        { return info.entry.IsDir() }
func (info desktopFilesDemoInfo) Sys() any           { return nil }
