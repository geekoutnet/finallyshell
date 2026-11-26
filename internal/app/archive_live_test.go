package app

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"finallyshell/internal/remotefs"
)

// With "Archive transfer" on, on the testbox, the plain upload of a
// folder (as a drop sends it) and the plain download of it go as one
// archive transfer each, and the files arrive whole; so does a single
// file, compressed. The desktop's Files app packs its uploads and
// downloads the same way.
func TestArchiveModeLive(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	a, p, s := live.App, live.p, live.s
	a.settings.ArchiveTransfers = true
	remote := live.scratch(t)

	local := t.TempDir()
	tree := filepath.Join(local, "assets")
	writeTree(t, tree, 40)
	text := strings.Repeat("a log line that compresses well\n", 4000)
	os.WriteFile(filepath.Join(local, "one.log"), []byte(text), 0o644)
	last := func() *transferJob { return a.transfers.jobs[len(a.transfers.jobs)-1] }

	// The file panel: a dropped folder, then a single file.
	a.upload(s, []string{tree}, remote)
	p.until(t, "the archive upload", func() bool { return len(a.transfers.jobs) == 1 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone {
		t.Fatalf("upload %+v: %v", j, j.err)
	}
	if got := live.run(t, "find "+remotefs.ShellQuote(path.Join(remote, "assets"))+" -type f | wc -l"); got != "41" {
		t.Errorf("%s files on the server, want 41", got)
	}
	a.upload(s, []string{filepath.Join(local, "one.log")}, remote)
	p.until(t, "the file upload", func() bool { return len(a.transfers.jobs) == 2 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone {
		t.Fatalf("single file %+v: %v", j, j.err)
	}
	if got := live.run(t, "wc -c < "+remotefs.ShellQuote(path.Join(remote, "one.log"))); got != strconv.Itoa(len(text)) {
		t.Errorf("one.log has %s bytes on the server, want %d", got, len(text))
	}

	a.openDir(s, remote)
	p.until(t, "the listing", func() bool { return s.files.cwd == remote && !s.files.loading && len(s.files.entries) == 2 })
	back := t.TempDir()
	a.download(s, []string{"assets"}, back)
	p.until(t, "the archive download", func() bool { return len(a.transfers.jobs) == 3 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone {
		t.Fatalf("download %+v: %v", j, j.err)
	}
	sameTree(t, tree, filepath.Join(back, "assets"))
	a.download(s, []string{"one.log"}, back)
	p.until(t, "the file download", func() bool { return len(a.transfers.jobs) == 4 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone {
		t.Fatalf("single file download %+v: %v", j, j.err)
	}
	if data, _ := os.ReadFile(filepath.Join(back, "one.log")); string(data) != text {
		t.Errorf("downloaded one.log has %d bytes, want %d", len(data), len(text))
	}

	// The desktop's Files app.
	w := live.files(t, remote)
	from := t.TempDir()
	writeTree(t, filepath.Join(from, "docs"), 10)
	a.desktopFilesEnqueueUpload(w, s.sftp, remote, []string{filepath.Join(from, "docs")}, []string{"docs"})
	p.until(t, "the desktop upload", func() bool { return len(a.transfers.jobs) == 5 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone {
		t.Fatalf("desktop upload %+v: %v", j, j.err)
	}
	live.fileWait(t, w, "the folder after the upload")
	var docs []desktopFilesItem
	p.until(t, "docs listed", func() bool {
		i := slices.IndexFunc(w.explorer().entries, func(e desktopFilesItem) bool { return e.Name == "docs" })
		if i >= 0 {
			docs = []desktopFilesItem{w.explorer().entries[i]}
		}
		return i >= 0
	})
	down := t.TempDir()
	a.setDownloadDir(down)
	a.desktopFilesDownload(w, docs)
	p.until(t, "the desktop download", func() bool { return len(a.transfers.jobs) == 6 && idle(a)() })
	if j := last(); !j.archive || j.state != transferDone || path.Base(j.src) != "docs" {
		t.Fatalf("desktop download %+v: %v", j, j.err)
	}
	sameTree(t, filepath.Join(from, "docs"), filepath.Join(down, "docs"))
}
