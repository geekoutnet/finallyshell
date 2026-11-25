package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const trashTestStore = "/home/ops/.local/share/Trash"

type desktopTrashFake struct {
	sync.Mutex
	scripts       []string
	entries       []string
	fail, blocked bool
}

func trashTestRecord(store, mount, name, original, date, mode, owner, note, size, kind string) string {
	info := "[Trash Info]\nPath=" + original + "\nDeletionDate=" + date + "\nX-Finallyshell-Note=" + note
	return strings.Join([]string{"item", store, mount, name, info, kind, mode, owner, size, "stamp:" + name, ""}, "\x00")
}

func (fake *desktopTrashFake) run(_ context.Context, script string) (string, string, error) {
	fake.Lock()
	defer fake.Unlock()
	if strings.HasSuffix(script, "printf 'end\\000'\n\n") {
		return strings.Join(fake.entries, "") + "end\x00", "", nil
	}
	fake.scripts = append(fake.scripts, script)
	last := script[strings.LastIndex(strings.TrimSuffix(script, "\n"), "\n")+1:]
	if fake.fail {
		return "", "fake command failed", errors.New("fake failure")
	}
	switch {
	case strings.HasPrefix(last, "put "):
		if fake.blocked && strings.HasPrefix(script, "root_trash='0'") {
			return "permission\n", "permission denied", errors.New("fake failure")
		}
		return trashTestStore + "\x00\x00moved\x00", "", nil
	case strings.HasPrefix(last, "purge "):
		fake.entries = fake.entries[1:]
		return "", "", nil
	case strings.HasSuffix(last, " 'check' ''\n"):
		return "conflict\x00existing\x00", "", nil
	case strings.HasSuffix(last, " 'keep' 'existing'\n"):
		return "/var/log/a'b (restored)\x00", "", nil
	default:
		return "", "unexpected command", errors.New("fake failure")
	}
}

func (fake *desktopTrashFake) operations() []string {
	fake.Lock()
	defer fake.Unlock()
	return append([]string(nil), fake.scripts...)
}

func trashTestClick(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestDesktopTrashActions(t *testing.T) {
	a, plat, tt, session := desktopTestApp(t)
	const store = trashTestStore
	fake := &desktopTrashFake{entries: []string{
		trashTestRecord(store, "", "a'b", "/var/log/a%27b", "2026-11-18T09:42:30", "640", "ops:staff", "old%20logs", "1024", "f"),
		trashTestRecord("/data/.Trash-1000", "/data", "scratch", "", "", "755", "ops:staff", "", "4096", "d"),
	}}
	win := a.desktopOpen(session, appTrash, desktopTrashRunners{user: fake.run, loginRoot: true})
	state := win.state.(*desktopTrashState)
	plat.until(t, "trash items", func() bool { return state.loaded })
	if _, ok := tt.Find("a'b"); !ok {
		t.Fatal("first trashed item not visible")
	}
	if _, ok := tt.Find(locales.DesktopTrashUnknown()); !ok {
		t.Fatal("orphan does not show unknown origin")
	}
	trashTestClick(t, tt, locales.DesktopTrashSearch())
	tt.Type("old logs")
	tt.Frame()
	if len(state.visibleItems()) != 1 {
		t.Fatal("search did not match the note")
	}
	state.query = ""
	tt.Frame()
	trashTestClick(t, tt, locales.DesktopTrashSelectAll())
	if len(state.selected) != 2 {
		t.Fatal("select all did not select both items")
	}
	trashTestClick(t, tt, locales.DesktopTrashSelectAll())
	if len(state.selected) != 0 {
		t.Fatal("select all did not clear the selection")
	}
	if err := tt.RightClick("a'b"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.DesktopTrashCopy()); err != nil || tt.Clipboard() != "/var/log/a'b" {
		t.Fatalf("copy original path: %q, %v", tt.Clipboard(), err)
	}
	trashTestClick(t, tt, "a'b")
	trashTestClick(t, tt, locales.DesktopTrashDelete())
	if state.question == nil || len(state.question.items) != 1 {
		t.Fatal("missing delete confirmation")
	}
	trashTestClick(t, tt, locales.Cancel())
	if len(fake.operations()) != 0 || state.question != nil {
		t.Fatal("cancel ran a remote operation or left its confirmation open")
	}
	trashTestClick(t, tt, locales.DesktopTrashRestore())
	trashTestClick(t, tt, locales.SvcConfirm())
	plat.until(t, "restore conflict", func() bool { return state.conflict != nil })
	trashTestClick(t, tt, locales.DesktopTrashKeepBoth())
	plat.until(t, "restored item", func() bool { return state.cancel == nil })
	commands := fake.operations()
	if len(commands) != 2 || !strings.HasSuffix(commands[0], "\nrestore '"+store+"' 'a'\\''b' 'stamp:a'\\''b' '/var/log/a'\\''b' 'check' ''\n") ||
		!strings.HasSuffix(commands[1], "\nrestore '"+store+"' 'a'\\''b' 'stamp:a'\\''b' '/var/log/a'\\''b' 'keep' 'existing'\n") {
		t.Fatalf("incorrect restore invocations: %d", len(commands))
	}
	if got := a.settings.Desktop.Hosts[session.Host.ID].Notes["/var/log/a'b (restored)"]; got != "old logs" {
		t.Fatalf("restored duplicate lost its note: %q", got)
	}
	trashTestClick(t, tt, "a'b")
	trashTestClick(t, tt, locales.DesktopTrashDelete())
	trashTestClick(t, tt, locales.SvcConfirm())
	plat.until(t, "permanent deletion", func() bool { return state.cancel == nil })
	commands = fake.operations()
	if len(state.items) != 1 || len(commands) != 3 || !strings.HasSuffix(commands[2], "\npurge '"+store+"' 'a'\\''b' 'stamp:a'\\''b'\n") {
		t.Fatal("permanent deletion did not run the confirmed item command")
	}
	fake.Lock()
	fake.fail = true
	fake.Unlock()
	trashTestClick(t, tt, locales.DesktopTrashEmpty())
	trashTestClick(t, tt, locales.Cancel())
	if len(fake.operations()) != 3 || state.question != nil {
		t.Fatal("cancelling Empty trash must not change files")
	}
	trashTestClick(t, tt, locales.DesktopTrashEmpty())
	trashTestClick(t, tt, locales.SvcConfirm())
	plat.until(t, "empty failure", func() bool { return state.cancel == nil })
	if !strings.Contains(state.message, "fake command failed") || len(state.items) != 1 {
		t.Fatalf("missing failure: %q", state.message)
	}
}

func TestDesktopTrashMoveRefreshAndEscalation(t *testing.T) {
	a, plat, _, session := desktopTestApp(t)
	fake := &desktopTrashFake{blocked: true}
	runners := desktopTrashRunners{user: fake.run, root: fake.run, cachedRoot: fake.run}
	win := a.desktopOpen(session, appTrash, runners)
	state := win.state.(*desktopTrashState)
	plat.until(t, "initial listing", func() bool { return state.loaded })
	original := "/srv/Q'1"
	a.settings.Desktop = &config.DesktopSettings{Hosts: map[string]config.DesktopHost{session.Host.ID: {Notes: map[string]string{original: "hello + %"}}}}
	fake.Lock()
	fake.entries = []string{trashTestRecord(trashTestStore, "", "Q'1", "/srv/Q%271", "2026-11-18T09:42:30", "640", "ops:staff", "hello", "1024", "f")}
	fake.Unlock()
	var finished bool
	var failure error
	a.desktopTrashWith(session, []string{"/", original}, nil, func(err error) { finished, failure = true, err }, runners)
	plat.until(t, "moves and refresh", func() bool { return finished && state.cancel == nil })
	commands := fake.operations()
	if len(commands) != 2 || !strings.HasPrefix(commands[1], "root_trash='1'") || !strings.HasSuffix(commands[1], "\nput '/srv/Q'\\''1' '/srv/Q%271' 'hello%20%2B%20%25'\n") {
		t.Fatal("missing escalation or exact quoted command")
	}
	if failure == nil || !strings.Contains(failure.Error(), "/: ") || len(state.items) == 0 || len(a.desktopOf(session).log) != 2 {
		t.Fatalf("expected aggregate failure, refreshed listing and two log entries, got %v", failure)
	}
}

func TestDesktopTrashScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("screenshots are opt-in")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, look := range []string{"light", "dark"} {
			t.Run(language+"-"+look, func(t *testing.T) {
				a, plat, tt, session := desktopTestApp(t)
				plat.SetLocale(language)
				tt.SetDark(look == "dark")
				fake := &desktopTrashFake{entries: []string{
					trashTestRecord(trashTestStore, "", "release-notes.md", "/srv/releases/release-notes.md", "2026-11-18T09:42:30", "640", "ops:staff", "Reviewed%20draft", "12288", "f"),
					trashTestRecord("/data/.Trash-1000", "/data", "build-output", "cache/build-output", "2026-11-17T15:24:00", "755", "ops:staff", "Build%20artifacts", "5263360", "d"),
					trashTestRecord("/root/.local/share/Trash", "", "app.log.1", "/var/log/app.log.1", "2026-11-16T21:06:12", "600", "root:root", "", "33288192", "f"),
				}}
				win := a.desktopOpen(session, appTrash, desktopTrashRunners{user: fake.run})
				state := win.state.(*desktopTrashState)
				plat.until(t, "sample items", func() bool { return state.loaded })
				state.items[2].Root = true
				for _, layout := range []string{"wide", "small"} {
					if layout == "small" {
						win.w, win.h = win.app.minSize.W, win.app.minSize.H+desktopTitleHeight
					}
					suffix := "-" + language + "-" + look + "-" + layout + ".png"
					shot := func(name string) {
						tt.Frame()
						tt.Frame()
						saveShot(t, tt, "trash-"+name+suffix)
					}
					shot("items")
					state.ask(state.items, locales.DesktopTrashEmpty(), true)
					shot("confirm")
					state.question = nil
					state.conflict = &desktopTrashConflict{item: state.items[0], answer: make(chan monitor.TrashRestoreMode, 1)}
					shot("conflict")
					state.conflict = nil
					items := state.items
					state.items = nil
					shot("empty")
					state.items = items
				}
			})
		}
	}
}
