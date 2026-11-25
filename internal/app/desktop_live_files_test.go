package app

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"weak"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopLiveFiles struct {
	*App
	p       *fakePlatform
	tt      *ui.Tester
	s       *Session
	account string
	secret  string
}

func openDesktopLiveFiles(t *testing.T, mutate bool) desktopLiveFiles {
	t.Helper()
	spec := os.Getenv("FINALLYSHELL_TEST_SSH")
	if spec == "" {
		t.Skip("FINALLYSHELL_TEST_SSH not set")
	}
	if mutate && os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE=1 not set")
	}
	cred, endpoint, _ := strings.Cut(spec, "@")
	account, secret, _ := strings.Cut(cred, ":")
	if endpoint != "127.0.0.1:2222" || (account != "root" && account != "tester") || secret == "" {
		t.Fatal("use testbox.sh ssh-env root or tester for these testbox-only tests")
	}
	a, platform, session := connectTestServer(t)
	t.Cleanup(session.disconnect)
	platform.until(t, "real SFTP channel", func() bool { return session.sftp != nil })
	tester := ui.NewTester(func(ctx *ui.Context) {
		ctx.SetMenuPresentation(ui.PopoverMenus)
		a.View(ctx)
	}, 1440, 920)
	platform.tt = tester
	a.openDesktop(session)
	return desktopLiveFiles{a, platform, tester, session, account, secret}
}

func (live desktopLiveFiles) exec(script string) (string, string, error) {
	return live.execInput(relayQuote("sh", "-c", script), "")
}

func (live desktopLiveFiles) execInput(command, input string) (string, string, error) {
	channel, err := live.s.client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer channel.Close()
	stop := time.AfterFunc(20*time.Second, func() { _ = channel.Close() })
	defer stop.Stop()
	var stdout, stderr strings.Builder
	channel.Stdin, channel.Stdout, channel.Stderr = strings.NewReader(input), &stdout, &stderr
	err = channel.Run(command)
	return stdout.String(), stderr.String(), err
}

func (live desktopLiveFiles) run(t *testing.T, script string) string {
	t.Helper()
	out, stderr, err := live.exec("set -e\n" + script)
	if err != nil {
		t.Fatalf("testbox command: %s: %v: %s", script, err, stderr)
	}
	return strings.TrimSpace(out)
}

func (live desktopLiveFiles) scratch(t *testing.T) string {
	t.Helper()
	dir := live.run(t, "mktemp -d /tmp/fs-desk-XXXXXXXX")
	if !strings.HasPrefix(dir, "/tmp/fs-desk-") || path.Dir(dir) != "/tmp" || !desktopFilesPath(dir) {
		t.Fatal("unexpected scratch path")
	}
	t.Cleanup(func() { live.run(t, relayQuote("rm", "-rf", "--", dir)) })
	return dir
}

func (live desktopLiveFiles) files(t *testing.T, dir string) *desktopWindow {
	t.Helper()
	w := live.desktopOpen(live.s, appFiles, dir)
	live.fileWait(t, w, "open "+dir)
	return w
}

func (live desktopLiveFiles) fileWait(t *testing.T, w *desktopWindow, step string) {
	t.Helper()
	f := w.explorer()
	live.p.until(t, step, func() bool { return !f.loading && f.job == nil })
	if f.err != nil {
		t.Fatalf("%s: %v", step, f.err)
	}
}

func (live desktopLiveFiles) click(t *testing.T, text string) {
	t.Helper()
	desktopFilesTestConfirm(t, live.tt, text)
}

func (live desktopLiveFiles) accept(w *desktopWindow) {
	prompt := w.explorer().modal
	w.explorer().modal = nil
	prompt.accept(prompt)
}

func TestDesktopLiveFilesThisPC(t *testing.T) {
	live := openDesktopLiveFiles(t, false)
	w := live.desktopOpen(live.s, appThisPC, nil)
	f := w.explorer()
	live.p.until(t, "disk inventory", func() bool { return !f.loading && (f.mountsRead || f.driveErr != nil) })
	if f.err != nil || f.driveErr != nil {
		t.Fatalf("drive inventory: %v / %v", f.err, f.driveErr)
	}
	saveShot(t, live.tt, "this-pc-"+live.account+".png")
	var want []desktopFilesDrive
	for line := range strings.SplitSeq(live.run(t, "LC_ALL=C df -PT"), "\n") {
		columns := strings.Fields(line)
		if len(columns) != 7 || columns[6] != "/" {
			continue
		}
		total, _ := strconv.ParseInt(columns[2], 10, 64)
		free, _ := strconv.ParseInt(columns[4], 10, 64)
		used, _ := strconv.Atoi(strings.TrimSuffix(columns[5], "%"))
		want = append(want, desktopFilesDrive{columns[0], columns[1], "/", total * 1024, free * 1024, used})
	}
	if len(want) != 1 || len(f.drives) != 1 {
		t.Errorf("expected only the user-visible root drive on the testbox, got %+v", f.drives)
	} else if got := f.drives[0]; got.mount != "/" || got.kind != want[0].kind || got.total != want[0].total || got.used != want[0].used || absDesktopLiveBytes(got.free-want[0].free) > 16<<20 {
		t.Errorf("drive %+v differs from df %+v", got, want[0])
	}
	for _, target := range []string{live.run(t, `printf '%s' "$HOME"`), "/var/log", "/etc", "/opt", "/tmp"} {
		if !slices.Contains(f.quick, target) {
			t.Errorf("missing quick access to %q in %v", target, f.quick)
		}
	}
	for _, target := range f.quick {
		live.run(t, relayQuote("test", "-d", target))
	}
}

func absDesktopLiveBytes(amount int64) int64 {
	return max(amount, -amount)
}

func TestDesktopLiveFilesMissingTool(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	if _, _, err := live.exec("command -v zip"); err == nil {
		t.Skip("zip is already installed on the testbox")
	}
	dir := live.scratch(t)
	live.run(t, relayQuote("touch", path.Join(dir, "input.txt")))
	w := live.files(t, dir)
	f := w.explorer()
	live.desktopFilesCompress(w, f.entries, ".zip")
	live.accept(w)
	live.p.until(t, "missing ZIP command", func() bool { return f.job == nil && !f.loading })
	if f.err == nil || !strings.Contains(f.err.Error(), locales.DesktopFilesMissingTool("zip")) {
		t.Fatalf("missing ZIP error: %v", f.err)
	}
	live.run(t, "test ! -e "+relayQuote(path.Join(dir, "input.txt.zip")))
}

func TestDesktopLiveFilesBrowse(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	const unusual = "it's 中文 file.txt"
	target := path.Join(dir, unusual)
	longDir := path.Join(dir, "a long breadcrumb with spaces and 中文 characters for the screenshot")
	live.run(t, relayQuote("mkdir", "-p", longDir, path.Join(dir, "large"))+"\n"+
		"printf 'sample' >"+relayQuote(target)+"\n"+relayQuote("chmod", "0640", target)+"\n"+
		relayQuote("touch", "-d", "@1730000000", target)+"\n"+relayQuote("touch", path.Join(dir, ".hidden"), path.Join(longDir, "child-needle.txt"))+"\n"+
		relayQuote("ln", "-s", unusual, path.Join(dir, "link"))+"\n"+relayQuote("ln", "-s", "absent", path.Join(dir, "broken-link"))+"\n"+
		"cd "+relayQuote(path.Join(dir, "large"))+"; seq -f 'large-%04.0f' 1 2000 | xargs touch --")
	w := live.files(t, dir)
	f := w.explorer()
	item := desktopFilesTestItem(t, f, unusual)
	got := fmt.Sprintf("%d %d %o %d %d", item.UID, item.GID, item.Mode.Perm(), item.Size, item.ModTime.Unix())
	if want := live.run(t, relayQuote("stat", "-c", "%u %g %a %s %Y", target)); got != want {
		t.Errorf("listing = %q, stat = %q", got, want)
	}
	if link := desktopFilesTestItem(t, f, "link"); !link.Link || link.Broken || link.Target != unusual || link.Size != item.Size {
		t.Errorf("symlink: %+v", link)
	}
	if link := desktopFilesTestItem(t, f, "broken-link"); !link.Link || !link.Broken {
		t.Errorf("broken symlink: %+v", link)
	}
	live.tt.Frame()
	wantTexts(t, live.tt, "real files", unusual, ".hidden")
	saveShot(t, live.tt, "files-"+live.account+".png")
	live.desktopFilesAskProperties(w, desktopFilesTestItem(t, f, path.Base(longDir)))
	props := f.modal.properties
	live.p.until(t, "folder size", func() bool { return !props.reading && !props.measuring })
	wantSize := strings.Fields(live.run(t, relayQuote("du", "-sb", "--", longDir)))[0]
	if props.unknownSize || strconv.FormatInt(props.bytes, 10) != wantSize {
		t.Errorf("folder size %d, want %s (%s)", props.bytes, wantSize, props.problem)
	}
	props.octal, props.recursive = "0750", true
	live.desktopFilesApplyProperties(w, props)
	live.accept(w)
	live.fileWait(t, w, "recursive chmod")
	live.run(t, "test "+`"$(stat -c %a -- `+relayQuote(path.Join(longDir, "child-needle.txt"))+`)" = 750`)
	live.desktopFilesSearch(w, "child-needle")
	live.fileWait(t, w, "recursive search")
	if len(f.entries) != 1 || f.entries[0].target != path.Join(longDir, "child-needle.txt") || f.entries[0].UID != item.UID || f.entries[0].GID != item.GID {
		t.Errorf("recursive find: %+v", f.entries)
	}
	live.desktopFilesSearch(w, "large-")
	live.fileWait(t, w, "limited search")
	if !f.limited || len(f.entries) != desktopFilesSearchLimit {
		t.Errorf("search limit: %d, limited=%t", len(f.entries), f.limited)
	}
	// A short loopback find can finish before a click. Hold the real SSH
	// command open after find produces output, then cancel through the banner.
	marker := path.Join(dir, "search-started")
	fixture := "FINALLYSHELL_SEARCH_FIXTURE=" + dir
	realRun := desktopFilesRunner(live.s, f)
	running := make(chan context.Context, 1)
	f.run = func(ctx context.Context, script string, report func(string)) (string, string, error) {
		running <- ctx
		return realRun(ctx, script+"\nprintf '%s' \"$$\" >"+relayQuote(marker)+"\nexec "+relayQuote("env", fixture, "sleep", "10"), report)
	}
	live.desktopFilesSearch(w, "child-needle")
	t.Cleanup(f.readCancel)
	var searchPID string
	live.p.until(t, "running SSH search", func() bool {
		out, _, err := live.exec(relayQuote("cat", marker))
		pid, parseErr := strconv.Atoi(out)
		if err != nil || parseErr != nil || pid <= 1 {
			return false
		}
		searchPID = out
		return true
	})
	t.Cleanup(func() {
		if _, _, err := live.exec(relayQuote("grep", "-zFxq", "--", fixture, "/proc/"+searchPID+"/environ")); err == nil {
			live.run(t, "kill -TERM "+searchPID+" 2>/dev/null || :")
		}
	})
	live.tt.Frame()
	clickButton(t, live.tt, locales.Cancel())
	if err := (<-running).Err(); err != context.Canceled {
		t.Fatalf("cancel button did not cancel the SSH search: %v", err)
	}
	if f.loading || f.searching {
		t.Error("cancel left the UI waiting for the remote command to exit")
	}
	live.fileWait(t, w, "cancelled search")
	f.run = nil
	if f.searching || f.deep != "large-" || len(f.entries) != desktopFilesSearchLimit {
		t.Fatal("cancel did not preserve the previous search results")
	}
	started := time.Now()
	live.desktopFilesRead(w, path.Join(dir, "large"), false, true)
	live.tt.Frame()
	live.fileWait(t, w, "2000-entry listing")
	if len(f.entries) != 2000 || time.Since(started) >= 5*time.Second {
		t.Errorf("large listing: %d entries in %v", len(f.entries), time.Since(started))
	}
	t.Logf("2000 real entries loaded and rendered in %v", time.Since(started))
	live.desktopFilesRead(w, longDir, false, true)
	live.fileWait(t, w, "long breadcrumb")
	saveShot(t, live.tt, "files-long-path-"+live.account+".png")
	live.desktopFilesFavorite(live.s, longDir, true)
	live.desktopFilesNote(live.s, target, "备注 % +")
	if settings := config.LoadSettings().Desktop.Hosts[live.s.Host.ID]; !slices.Contains(settings.Favorites, longDir) || !slices.Contains(settings.Recent, longDir) || settings.Notes[target] != "备注 % +" {
		t.Fatalf("favorites, recent or notes not persisted: %+v", settings)
	}
}

func TestDesktopLiveFilesActions(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	w := live.files(t, dir)
	f := w.explorer()
	live.desktopFilesNew(w, true)
	f.inline.value = "destination 中文"
	live.click(t, locales.DesktopFilesCreate())
	live.fileWait(t, w, "new folder")
	dest := desktopFilesTestItem(t, f, "destination 中文").target
	live.desktopFilesNew(w, false)
	f.inline.value = "created.txt"
	live.click(t, locales.DesktopFilesCreate())
	live.fileWait(t, w, "new text file")
	live.desktopFilesRename(w, desktopFilesTestItem(t, f, "created.txt"))
	f.inline.value = "renamed '中文'.txt"
	live.click(t, locales.DesktopFilesConfirmRename())
	live.fileWait(t, w, "rename")
	source := desktopFilesTestItem(t, f, "renamed '中文'.txt")
	live.run(t, "test -d "+relayQuote(dest)+"; test -f "+relayQuote(source.target)+"; test ! -e "+relayQuote(path.Join(dir, "created.txt"))+"; printf 'new contents' >"+relayQuote(source.target))
	other := live.files(t, dest)
	o := other.explorer()
	clip := &desktopFilesClipboard{owner: weak.Make(live.App), s: live.s, paths: []string{source.target}}
	paste := func(decide func()) {
		live.desktopFilesPasteRemote(other, clip)
		live.fileWait(t, other, "paste planning")
		if decide != nil {
			if o.conflict == nil {
				t.Fatal("missing paste conflict")
			}
			decide()
		}
		if o.modal != nil {
			live.click(t, locales.DesktopFilesConfirmPaste())
			live.fileWait(t, other, "paste completed")
		}
	}
	remote := path.Join(dest, source.Name)
	paste(nil)
	if got := live.run(t, relayQuote("cat", remote)); got != "new contents" {
		t.Fatalf("copy content: %q", got)
	}
	live.run(t, "printf 'existing' >"+relayQuote(remote))
	paste(func() { live.click(t, locales.DesktopFilesSkip()) })
	if got := live.run(t, relayQuote("cat", remote)); got != "existing" {
		t.Fatalf("skip content: %q", got)
	}
	paste(func() { live.click(t, locales.DesktopFilesKeepBoth()) })
	live.run(t, "test -f "+relayQuote(path.Join(dest, "renamed '中文' (1).txt")))
	clip.cut = true
	paste(func() { live.click(t, locales.Replace()) })
	if got := live.run(t, "test ! -e "+relayQuote(source.target)+"; "+relayQuote("cat", remote)); got != "new contents" {
		t.Fatalf("cut/replace content: %q", got)
	}
	if got := live.run(t, "cat -- "+relayQuote(remote)+".bak-*"); got != "existing" {
		t.Fatalf("replacement backup: %q", got)
	}
	live.desktopFilesDelete(other, []desktopFilesItem{desktopFilesTestItem(t, o, source.Name)}, true)
	live.click(t, locales.DesktopFilesConfirmDeletion())
	live.fileWait(t, other, "permanent deletion")
	live.run(t, "test ! -e "+relayQuote(remote))
}

func TestDesktopLiveFilesTransferAndEdit(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	w := live.files(t, dir)
	f := w.explorer()
	const name = "upload 中文.txt"
	local := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(local, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	logCount := len(live.desktopOf(live.s).log)
	live.desktopFilesUpload(w, []string{local}, false)
	live.click(t, locales.DesktopFilesStartUpload())
	live.p.until(t, "real upload", func() bool { return len(live.desktopOf(live.s).log) > logCount && !f.loading })
	remote := path.Join(dir, name)
	if got := live.run(t, relayQuote("cat", remote)); got != "original" {
		t.Fatalf("remote upload: %q", got)
	}
	item := desktopFilesTestItem(t, f, name)
	live.desktopFilesOpenItem(w, item, false)
	live.p.until(t, "remote editor", func() bool { return live.fe.current() != nil && live.fe.current().state != tabLoading })
	tab := live.fe.current()
	if tab.state != tabReady || tab.code.Buffer.String() != "original" {
		t.Fatalf("editor: state %v: %v", tab.state, tab.err)
	}
	editor := live.p.show(t, WindowFileEditor)
	editor.Frame()
	box, ok := editor.Find(name)
	if !ok || box.W <= 0 {
		t.Fatal("remote file tab not drawn")
	}
	editor.Key(ui.Cmd, ui.KeyA)
	editor.Type("edited in 中文")
	editor.Key(ui.Cmd, ui.KeyS)
	live.p.until(t, "remote editor save", func() bool { return !tab.saving && !tab.savedAt.IsZero() && !f.loading })
	if got := live.run(t, relayQuote("cat", remote)); got != "edited in 中文" {
		t.Fatalf("remote edit: %q", got)
	}
	live.settings.AskDownloadDir = false
	live.desktopFilesDownload(w, []desktopFilesItem{desktopFilesTestItem(t, f, name)})
	live.p.until(t, "real download", func() bool {
		b, _ := os.ReadFile(filepath.Join(live.p.downloads, name))
		return string(b) == "edited in 中文" && live.transfers.active() == 0
	})
}

func TestDesktopLiveFilesArchivesAndFetch(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	const payload = "data '中文'.txt"
	live.run(t, "printf 'archive contents' >"+relayQuote(path.Join(dir, payload)))
	w := live.files(t, dir)
	f := w.explorer()
	for _, kind := range []string{".tar.gz", ".zip"} {
		t.Run(kind, func(t *testing.T) {
			if kind == ".zip" {
				if _, _, err := live.exec("set -e; command -v zip; command -v unzip"); err != nil {
					t.Skip("zip or unzip is not installed")
				}
			}
			live.desktopFilesCompress(w, []desktopFilesItem{desktopFilesTestItem(t, f, payload)}, kind)
			f.modal.value = "bundle" + kind
			live.accept(w)
			live.fileWait(t, w, "compress "+kind)
			item := desktopFilesTestItem(t, f, "bundle"+kind)
			live.desktopFilesAskExtract(w, item, kind == ".zip")
			dest := dir
			if kind == ".zip" {
				f.modal.value = "extracted 中文"
				dest = path.Join(dir, f.modal.value)
			} else {
				live.run(t, relayQuote("rm", "--", path.Join(dir, payload)))
			}
			live.accept(w)
			live.fileWait(t, w, "extract "+kind)
			if got := live.run(t, relayQuote("cat", path.Join(dest, payload))); got != "archive contents" {
				t.Fatalf("archive content: %q", got)
			}
		})
	}
	const source = "http://127.0.0.1/index.html"
	want := live.run(t, relayQuote("curl", "-fsS", source))
	live.desktopFilesFetch(w)
	f.modal.value = source
	live.accept(w)
	job := f.job
	live.fileWait(t, w, "Caddy fetch")
	if got := live.run(t, relayQuote("cat", path.Join(dir, "index.html"))); got != want || job.progress == "" {
		t.Errorf("remote download contents match=%t, progress=%q", got == want, job.progress)
	}
	live.desktopFilesFetch(w)
	f.modal.value = "http://127.0.0.1/" + path.Base(dir) + "-missing.html"
	missing := path.Join(dir, desktopFilesFetchName(f.modal.value, ""))
	live.accept(w)
	live.p.until(t, "failed remote fetch", func() bool { return f.job == nil && !f.loading })
	if f.err == nil || !live.tt.HasText(f.err.Error()) {
		t.Errorf("remote download error not shown: %v", f.err)
	}
	live.run(t, "test ! -e "+relayQuote(missing))
}

func (live desktopLiveFiles) rootRun(t *testing.T, script string) string {
	t.Helper()
	if live.account == "root" {
		return live.run(t, script)
	}
	out, stderr, err := live.execInput(relayQuote("sudo", "-kS", "-p", "", "--", "sh", "-ec", script), live.secret+"\n")
	if err != nil {
		t.Fatalf("privileged testbox command: %v: %s", err, stderr)
	}
	return strings.TrimSpace(out)
}

func (live desktopLiveFiles) requireSudoPrompt(t *testing.T) {
	t.Helper()
	if live.account == "root" {
		return
	}
	kind := privilegeWindow(live.s)
	live.p.until(t, "real sudo prompt", func() bool { return live.windowOpen(kind) })
	dialog := live.p.show(t, kind)
	wantTexts(t, dialog, "sudo prompt", locales.PrivSudoPrompt(live.account+"@"+live.s.Host.Addr))
	if err := dialog.Click(locales.PrivPassword()); err != nil {
		t.Fatal("could not focus sudo credential field")
	}
	dialog.Type(live.secret)
	clickButton(t, dialog, locales.PrivRun())
}

func TestDesktopLiveFilesOwnership(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	target := path.Join(dir, "root-owned.txt")
	live.rootRun(t, relayQuote("touch", target))
	w := live.files(t, dir)
	f := w.explorer()
	item := desktopFilesTestItem(t, f, "root-owned.txt")
	if item.UID != 0 || item.GID != 0 {
		t.Fatal("fixture was not created by root")
	}
	live.desktopFilesAskProperties(w, item)
	props := f.modal.properties
	live.p.until(t, "root-owned file metadata", func() bool { return !props.reading })
	// Force the prompt rather than the production shortcut that first tries the SSH login password.
	delete(live.passwords, live.s.Host.ID)
	props.owner, props.group = "tester", "tester"
	live.desktopFilesApplyProperties(w, props)
	live.accept(w)
	live.requireSudoPrompt(t)
	live.fileWait(t, w, "chown")
	if got := live.run(t, relayQuote("stat", "-c", "%U:%G", target)); got != "tester:tester" {
		t.Fatalf("chown: %q", got)
	}
}

func (live desktopLiveFiles) discardTrash(t *testing.T, store, name string, root bool) {
	t.Helper()
	// All entries used by this test carry its mktemp prefix. Also remove any duplicate of those entries.
	t.Cleanup(func() {
		script := "for candidate in " + relayQuote(path.Join(store, "files", name)) + " " + relayQuote(path.Join(store, "files", name)) + ".[0-9]*; do\n" +
			"  if [ -e \"$candidate\" ] || [ -L \"$candidate\" ]; then rm -rf -- \"$candidate\"; fi\n" +
			"  rm -f -- " + relayQuote(path.Join(store, "info")) + `/"${candidate##*/}.trashinfo"` + "\n" +
			"done; rm -f -- " + relayQuote(path.Join(store, "info", name+".trashinfo"))
		if root {
			live.rootRun(t, script)
		} else {
			live.run(t, script)
		}
	})
}

func (live desktopLiveFiles) trashWait(t *testing.T, state *desktopTrashState) {
	t.Helper()
	live.p.until(t, "real Trash inventory", func() bool { return state.cancel == nil })
	if state.message != "" || !state.loaded {
		t.Fatalf("trash: %s", state.message)
	}
}

func (live desktopLiveFiles) trashRunners(prefix string) desktopTrashRunners {
	runners := live.desktopTrashRunners(live.s)
	guard := func(run monitor.TrashRunner) monitor.TrashRunner {
		return func(ctx context.Context, script string) (string, string, error) {
			// Production inventory removes stale .trashinfo files. In the shared
			// testbox it may remove only records created by this test.
			const orphanName = `name=${info##*/}; name=${name%.trashinfo};`
			if strings.Contains(script, `printf 'orphan\000`) {
				if !strings.Contains(script, orphanName) {
					return "", "", fmt.Errorf("testbox trash inventory no longer has a recognized orphan cleanup loop")
				}
				script = strings.Replace(script, orphanName, orphanName+` case "$name" in `+relayQuote(prefix)+`*) :;; *) continue;; esac;`, 1)
			}
			return run(ctx, script)
		}
	}
	runners.user, runners.cachedRoot, runners.root = guard(runners.user), guard(runners.cachedRoot), guard(runners.root)
	return runners
}

func desktopLiveTrashItem(t *testing.T, state *desktopTrashState, original string) monitor.TrashItem {
	t.Helper()
	for _, item := range state.items {
		if item.Original == original {
			return item
		}
	}
	t.Fatalf("Trash did not list %q", original)
	return monitor.TrashItem{}
}

func TestDesktopLiveTrashHome(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	name := path.Base(dir) + " it's 中文.txt"
	original := path.Join(dir, "removed parent", name)
	store := path.Join(live.run(t, `printf '%s' "$HOME"`), ".local/share/Trash")
	live.cleanTrashFolders(t, store, false)
	live.discardTrash(t, store, name, false)
	live.run(t, relayQuote("mkdir", "-p", path.Dir(original))+"; printf 'trash contents' >"+relayQuote(original))
	live.desktopFilesNote(live.s, original, "备注 % +")
	w := live.files(t, path.Dir(original))
	logCount := len(live.desktopOf(live.s).log)
	live.desktopFilesDelete(w, []desktopFilesItem{desktopFilesTestItem(t, w.explorer(), name)}, false)
	live.accept(w)
	live.p.until(t, "Files reports move to trash", func() bool { return len(live.desktopOf(live.s).log) > logCount })
	live.fileWait(t, w, "move to home trash")
	metadata := live.run(t, relayQuote("cat", path.Join(store, "info", name+".trashinfo")))
	encoded := strings.NewReplacer(" ", "%20", "'", "%27", "中文", "%E4%B8%AD%E6%96%87").Replace(original)
	for _, field := range []string{"[Trash Info]", "Path=" + encoded, "X-Finallyshell-Mode=", "X-Finallyshell-Owner=" + live.account + ":", "X-Finallyshell-Size=14", "X-Finallyshell-Note=%E5%A4%87%E6%B3%A8%20%25%20%2B"} {
		if !strings.Contains(metadata, field) {
			t.Errorf("missing %q in %q", field, metadata)
		}
	}
	_, deletionDate, _ := strings.Cut(metadata, "\nDeletionDate=")
	deletionDate, _, _ = strings.Cut(deletionDate, "\n")
	if _, err := time.Parse("2006-01-02T15:04:05", deletionDate); err != nil {
		t.Errorf("invalid trash deletion date %q: %v", deletionDate, err)
	}
	if got := live.run(t, "test ! -e "+relayQuote(original)+"; "+relayQuote("cat", path.Join(store, "files", name))); got != "trash contents" {
		t.Fatalf("trashed data: %q", got)
	}
	trash := live.desktopOpen(live.s, appTrash, live.trashRunners(path.Base(dir)))
	state := trash.state.(*desktopTrashState)
	live.trashWait(t, state)
	item := desktopLiveTrashItem(t, state, original)
	if item.Note != "备注 % +" || item.Store != store {
		t.Fatalf("real trash metadata: %+v", item)
	}
	saveShot(t, live.tt, "trash-"+live.account+".png")
	live.run(t, relayQuote("rmdir", path.Dir(original)))
	state.selected[trashID(item)] = true
	live.click(t, locales.DesktopTrashRestore())
	live.click(t, locales.SvcConfirm())
	live.trashWait(t, state)
	if got := live.run(t, relayQuote("cat", original)); got != "trash contents" {
		t.Fatalf("restore with a missing parent: %q", got)
	}
	t.Run("freedesktop interoperability", func(t *testing.T) {
		if _, _, err := live.exec("set -e; command -v trash-put; command -v trash-list"); err != nil {
			t.Skip("trash-cli is not installed")
		}
		// Use trash-cli for the move, then restore the freedesktop entry via the application.
		live.run(t, relayQuote("trash-put", "--", original))
		live.desktopTrashRefresh(trash, state, false)
		live.trashWait(t, state)
		external := desktopLiveTrashItem(t, state, original)
		live.run(t, "printf 'new file' >"+relayQuote(original))
		live.desktopTrashApply(trash, state, &desktopTrashQuestion{items: []monitor.TrashItem{external}})
		live.p.until(t, "restore collision", func() bool { return state.conflict != nil })
		live.click(t, locales.DesktopTrashKeepBoth())
		live.trashWait(t, state)
		if got := live.run(t, relayQuote("cat", original, original+" (restored)")); got != "new filetrash contents" {
			t.Fatalf("restore conflict: %q", got)
		}
	})
	var moved bool
	var moveError error
	live.desktopTrash(live.s, []string{original}, func(err error) { moved, moveError = true, err })
	live.p.until(t, "second move to home trash", func() bool { return moved })
	if moveError != nil {
		t.Fatal(moveError)
	}
	live.trashWait(t, state)
	if _, _, err := live.exec("command -v trash-list"); err == nil && !strings.Contains(live.run(t, "trash-list"), original) {
		t.Error("trash-cli did not list the app-created entry")
	}
	item = desktopLiveTrashItem(t, state, original)
	state.selected[trashID(item)] = true
	live.click(t, locales.DesktopTrashDelete())
	live.click(t, locales.SvcConfirm())
	live.trashWait(t, state)
	live.run(t, "test ! -e "+relayQuote(path.Join(store, "files", item.Name))+"; test ! -e "+relayQuote(path.Join(store, "info", item.Name+".trashinfo")))
}

func (live desktopLiveFiles) cleanTrashFolders(t *testing.T, store string, root bool) {
	t.Helper()
	run := live.run
	if root {
		run = live.rootRun
	}
	var created []string
	for _, dir := range []string{path.Dir(path.Dir(store)), path.Dir(store), store, path.Join(store, "files"), path.Join(store, "info")} {
		if run(t, "if [ -d "+relayQuote(dir)+" ]; then printf yes; fi") != "yes" {
			created = append(created, dir)
		}
	}
	t.Cleanup(func() {
		for _, dir := range slices.Backward(created) {
			run(t, "rmdir -- "+relayQuote(dir)+" 2>/dev/null || :")
		}
	})
}

func TestDesktopLiveTrashEscalation(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	if live.account == "root" {
		t.Skip("sudo to root is only exercised by tester")
	}
	dir := live.rootRun(t, "mktemp -d /tmp/fs-desk-root-XXXXXXXX")
	if path.Dir(dir) != "/tmp" || !strings.HasPrefix(path.Base(dir), "fs-desk-root-") || !desktopFilesPath(dir) {
		t.Fatal("unexpected root scratch path")
	}
	t.Cleanup(func() { live.rootRun(t, relayQuote("rm", "-rf", "--", dir)) })
	live.rootRun(t, relayQuote("chmod", "0755", dir))
	const store = "/root/.local/share/Trash"
	live.cleanTrashFolders(t, store, true)
	name := path.Base(dir) + " root-only.txt"
	live.discardTrash(t, store, name, true)
	original := path.Join(dir, name)
	live.rootRun(t, "printf 'root-only contents' >"+relayQuote(original))
	files := live.files(t, dir)
	logCount := len(live.desktopOf(live.s).log)
	delete(live.passwords, live.s.Host.ID)
	live.desktopFilesDelete(files, []desktopFilesItem{desktopFilesTestItem(t, files.explorer(), name)}, false)
	live.accept(files)
	live.requireSudoPrompt(t)
	live.p.until(t, "move to root trash", func() bool { return len(live.desktopOf(live.s).log) > logCount })
	live.fileWait(t, files, "escalated Files deletion")
	if !live.tt.HasText(locales.DesktopTrashRootNotice(store)) {
		t.Error("root trash notice was not shown")
	}
	if got := live.rootRun(t, "test ! -e "+relayQuote(original)+"; "+relayQuote("cat", path.Join(store, "files", name))); got != "root-only contents" {
		t.Fatalf("root trash file: %q", got)
	}
	trash := live.desktopOpen(live.s, appTrash, live.trashRunners(path.Base(dir)))
	state := trash.state.(*desktopTrashState)
	live.trashWait(t, state)
	item := desktopLiveTrashItem(t, state, original)
	if !state.rootVisible || !item.Root || item.Store != store {
		t.Fatalf("expected an entry in root trash: %+v", item)
	}
	saveShot(t, live.tt, "trash-escalated-tester.png")
}

func TestDesktopLiveTrashPartitionAndEmpty(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	dir := live.scratch(t)
	other := path.Join(dir, "tmpfs mount")
	data := path.Join(dir, "xdg")
	live.run(t, relayQuote("mkdir", "-p", other, data))
	live.rootRun(t, relayQuote("mount", "-t", "tmpfs", "-o", "size=4M,mode=0755", "tmpfs", other))
	t.Cleanup(func() { live.rootRun(t, relayQuote("umount", "--", other)) })
	live.rootRun(t, relayQuote("chown", live.account, other))
	first, second := path.Join(dir, path.Base(dir)+"-empty.txt"), path.Join(other, path.Base(dir)+"-cross.txt")
	live.run(t, "printf 'empty' >"+relayQuote(first)+"; printf 'partition' >"+relayQuote(second))
	// Isolate HOME/XDG for the Empty action; retain the real SSH trash runner.
	runners := live.trashRunners(path.Base(dir))
	user := runners.user
	runners.user = func(ctx context.Context, script string) (string, string, error) {
		return user(ctx, "export HOME="+relayQuote(dir+"/home")+" XDG_DATA_HOME="+relayQuote(data)+"\n"+script)
	}
	runners.root, runners.cachedRoot = nil, nil
	live.run(t, relayQuote("mkdir", "-p", dir+"/home"))
	var done bool
	var moveError error
	live.desktopTrashWith(live.s, []string{first, second}, nil, func(err error) { done, moveError = true, err }, runners)
	live.p.until(t, "real cross-filesystem trash move", func() bool { return done })
	if moveError != nil {
		t.Fatal(moveError)
	}
	trash := live.desktopOpen(live.s, appTrash, runners)
	state := trash.state.(*desktopTrashState)
	live.trashWait(t, state)
	cross := desktopLiveTrashItem(t, state, second)
	if cross.Mount != other || !strings.HasPrefix(cross.Store, other+"/.Trash-") {
		t.Fatalf("wrong partition store: %+v", cross)
	}
	metadata := live.run(t, relayQuote("cat", path.Join(cross.Store, "info", cross.Name+".trashinfo")))
	if !strings.Contains(metadata, "\nPath="+path.Base(second)+"\n") {
		t.Fatalf("partition trash path is not relative: %q", metadata)
	}
	if len(state.items) != 2 || desktopLiveTrashItem(t, state, first).Store != data+"/Trash" {
		t.Fatal("Trash is not isolated; refusing to empty it")
	}
	live.click(t, locales.DesktopTrashEmpty())
	live.click(t, locales.SvcConfirm())
	live.trashWait(t, state)
	if len(state.items) != 0 {
		t.Fatalf("trash not empty: %d entries", len(state.items))
	}
	live.run(t, "test ! -e "+relayQuote(path.Join(cross.Store, "files", cross.Name))+"; test ! -e "+relayQuote(path.Join(data, "Trash/files", path.Base(first))))
}

func TestDesktopLiveTerminalWindows(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	base := live.scratch(t)
	start, next := path.Join(base, "it's 中文 here"), path.Join(base, "next 中文 folder")
	live.run(t, relayQuote("mkdir", "--", start, next))
	files := live.files(t, start)
	live.tt.Frame()
	live.tt.RightClickAt(live.desktopOf(live.s).shell.root.X+files.x+files.w/2, live.desktopOf(live.s).shell.root.Y+files.y+files.h/2)
	if err := live.tt.ChooseMenuItem(locales.DesktopFilesTerminalHere()); err != nil {
		t.Fatal(err)
	}
	live.tt.Frame()
	windows := live.desktopOf(live.s).windowsOf(appTerminal)
	if len(windows) != 1 {
		t.Fatal("Open terminal here did not open a shell window")
	}
	first := windows[0]
	firstState := first.state.(*desktopTerminalState)
	live.p.until(t, "first shell", func() bool { return firstState.pane != nil })
	firstTerm := firstState.pane.term
	send := func(line string) {
		live.tt.Frame()
		live.tt.Type(line)
		live.tt.Key(0, ui.KeyEnter)
	}
	writeIdentity := func(name string) string {
		target := path.Join(base, name)
		send(`printf '%s\n' "$$" "$(tty)" "$PWD" >` + relayQuote(target))
		var identity string
		live.p.until(t, "identity from focused shell", func() bool {
			out, _, err := live.exec(relayQuote("cat", target))
			identity = strings.TrimSpace(out)
			return err == nil && len(strings.Split(identity, "\n")) == 3
		})
		return identity
	}
	firstID := strings.Split(writeIdentity("first.id"), "\n")
	if firstID[2] != start {
		t.Fatalf("first shell started in %q, want %q", firstID[2], start)
	}
	second := live.desktopOpen(live.s, appTerminal, nil)
	secondState := second.state.(*desktopTerminalState)
	live.p.until(t, "second shell", func() bool { return secondState.pane != nil })
	secondID := strings.Split(writeIdentity("second.id"), "\n")
	if secondID[0] == firstID[0] || secondID[1] == firstID[1] || !strings.HasPrefix(secondID[1], "/dev/pts/") {
		t.Fatalf("shells share a PID or TTY: %v and %v", firstID, secondID)
	}
	live.desktopFocus(first)
	send("cd " + relayQuote(next))
	live.p.until(t, "title follows first shell cd", func() bool { return firstState.dir == next && strings.HasSuffix(first.title, next) })
	if secondState.dir == next {
		t.Fatal("inactive shell followed the focused shell's cd")
	}
	if got := strings.Split(writeIdentity("focused.id"), "\n"); got[0] != firstID[0] || got[2] != next {
		t.Fatalf("typing went to a different shell or folder: %v", got)
	}
	send(`export LC_ALL=C.UTF-8; clear; printf '\033[2J\033[HCurrent folder: %s\n' "$PWD"`)
	live.p.until(t, "terminal screenshot has UTF-8 output", func() bool { return strings.Contains(firstTerm.Text(), "Current folder: "+next) })
	live.tt.Frame()
	saveShot(t, live.tt, "terminal-"+live.account+".png")
	// Both windows have the same toolbar labels. Hide the inactive one so
	// Tester.Click does not select its visible button below the active window.
	live.desktopMinimize(live.desktopOf(live.s), second)
	live.click(t, locales.DesktopToolsOpenFolder())
	if opened := live.desktopOf(live.s).focused; opened == files || opened.app.id != appFiles {
		t.Fatal("Open current folder in Files did not open Files")
	} else {
		live.fileWait(t, opened, "shell's real folder in Files")
		if opened.explorer().dir != next {
			t.Fatalf("Files opened %q, want %q", opened.explorer().dir, next)
		}
	}
	live.desktopFocus(second)
	live.desktopRequestClose(second)
	live.tt.Frame()
	clickButton(t, live.tt, locales.DesktopToolsCloseShell())
	live.tt.Frame()
	if live.desktopToolsLive(second) {
		t.Fatalf("close confirmation did not close the window (confirming=%t)", secondState.confirming)
	}
	live.p.until(t, "closed terminal window", func() bool { return secondState.pane == nil })
	pid, err := strconv.Atoi(secondID[0])
	if err != nil || pid <= 1 {
		t.Fatal("invalid remote shell PID")
	}
	live.p.until(t, "closed window shell gone from ps", func() bool {
		out, _, _ := live.exec(fmt.Sprintf("ps -o stat= -p %d", pid))
		return strings.TrimSpace(out) == ""
	})
	live.desktopFocus(first)
	send("exit")
	live.p.until(t, "shell exit", func() bool { return firstState.pane == nil })
	live.click(t, locales.Reconnect())
	live.p.until(t, "shell reconnect", func() bool { return firstState.pane != nil })
	if firstState.pane.term == firstTerm {
		t.Fatal("reconnect did not start a new terminal")
	}
	reconnected := strings.Split(writeIdentity("reconnected.id"), "\n")
	if reconnected[0] == firstID[0] || reconnected[2] != next {
		t.Fatalf("reconnected shell: %v", reconnected)
	}
	live.backToTerminal(live.current())
	if live.current() != live.s || live.s.pane().term == nil {
		t.Fatal("session lost its own terminal")
	}
	primary := strings.Split(writeIdentity("main.id"), "\n")
	if primary[0] == firstID[0] || primary[0] == secondID[0] || primary[0] == reconnected[0] {
		t.Fatalf("session terminal is not independent: %v", primary)
	}
}
