package app

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/net/webdav"
)

func setupRestoreVersions(t *testing.T) (*device, *cloudsync.WebDAV, cloudsync.Snapshot) {
	t.Helper()
	server := httptest.NewServer(&webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()})
	t.Cleanup(server.Close)
	remote := &cloudsync.WebDAV{URL: server.URL + "/sync/"}
	d := newDevice(t, remote)
	d.a.settings.Sync.WebDAVURL = remote.URL
	root := &config.Folder{ID: "prod", Name: "Production"}
	child := &config.Folder{ID: "db", Parent: root.ID, Name: "Databases"}
	d.a.hosts.Folders = append(d.a.hosts.Folders, root, child)
	old := d.addHost("pg-main", "Primary database")
	old.Folder = child.ID
	d.a.quick().Commands = append(d.a.quick().Commands, &config.QuickCommand{ID: "status", Name: "Database status", Text: "pg_isready"})
	d.sync(t)
	history, err := remote.List(context.Background())
	if err != nil || len(history) != 1 {
		t.Fatalf("first history: %+v, %v", history, err)
	}
	return d, remote, history[0]
}

func TestBackupsRestoreOlderWebDAVVersion(t *testing.T) {
	d, remote, old := setupRestoreVersions(t)
	a := d.a
	a.hosts.RemoveFolder("db")
	a.hosts.RemoveFolder("prod")
	a.hosts.SoftDelete("pg-main", time.Now())
	a.quick().Commands = nil
	d.addHost("new", "New machine")
	d.sync(t)
	a.openVersions(WindowMain)
	st := a.sync.backups
	d.p.until(t, "versions list", func() bool { return !st.listing })
	a.selectVersion(st, versionEntry{Snapshot: old, remote: true}, "")
	d.p.until(t, "old snapshot read", func() bool { return !st.busy })
	if st.err != nil || st.preview == nil {
		t.Fatalf("preview: %v", st.err)
	}
	a.restoreVersion(st)
	d.p.until(t, "restore and upload", func() bool { return !a.sync.running && !st.listing })
	if st.err != nil {
		t.Fatal(st.err)
	}
	restored := a.hosts.Host("pg-main")
	if restored == nil || restored.Deleted() || restored.Folder != "db" || a.hosts.Folder("db").Parent != "prod" || a.hosts.Host("new") != nil || len(a.quick().Commands) != 1 {
		t.Fatalf("restore lost folders, assignments or commands: %+v", a.hosts)
	}
	local, err := config.ListBackups()
	if err != nil || len(local) == 0 || local[0].Reason != cloudsync.ReasonPreRestore {
		t.Fatalf("pre-restore backup: %+v, %v", local, err)
	}
	before, err := config.ReadBackup(local[0].ID)
	if err != nil || before.Hosts.Host("new") == nil || !before.Hosts.Host("pg-main").Deleted() {
		t.Fatalf("pre-restore contents: %+v, %v", before.Hosts, err)
	}
	history, err := remote.List(context.Background())
	if err != nil || len(history) != 4 {
		t.Fatalf("restore history: %+v, %v", history, err)
	}
	preRestore := false
	for _, version := range history {
		if version.Reason == cloudsync.ReasonPreRestore {
			data, err := remote.GetVersion(context.Background(), version.ID)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := cloudsync.OpenSnapshot(a.sync.password, data)
			if err != nil || payload.Counts().Deleted != 1 {
				t.Fatalf("pre-restore server contents: %v", err)
			}
			preRestore = true
			break
		}
	}
	if !preRestore {
		t.Fatal("missing pre-restore server version")
	}
	data, _, err := remote.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	meta := cloudsync.DescribeRevision(cloudsync.Snapshot{}, data)
	if meta.Reason != cloudsync.ReasonRestore || meta.Counts.Live != 1 || meta.Counts.Folders != 2 || meta.Counts.Commands != 1 {
		t.Fatalf("restored server snapshot: %+v", meta)
	}
}

func TestBackupsBeforeDeleteAndImport(t *testing.T) {
	a, p, table := openTestManager(t)
	managerClickWith(t, table, 0, "web-1")
	managerClickWith(t, table, ui.Cmd, "web-2")
	table.Key(0, ui.KeyDelete)
	managerClick(t, table, locales.Delete())
	entries, err := config.ListBackups()
	if err != nil || len(entries) != 1 || entries[0].Reason != cloudsync.ReasonBulkDelete {
		t.Fatalf("before multi-delete: %+v, %v", entries, err)
	}
	before, err := config.ReadBackup(entries[0].ID)
	if err != nil || before.Hosts.Host("web-1").Deleted() || before.Hosts.Host("web-2").Deleted() {
		t.Fatalf("backup after mutation: %v", err)
	}
	a.deleteItem(&a.manager, managerItem{folder: "lab"})
	if a.manager.question == nil {
		for _, folder := range a.hosts.Folders {
			a.deleteItem(&a.manager, managerItem{folder: folder.ID})
			break
		}
	}
	a.manager.question.yes()
	entries, err = config.ListBackups()
	if err != nil || len(entries) != 2 || entries[0].Reason != cloudsync.ReasonBulkDelete {
		t.Fatalf("before folder deletion: %+v, %v", entries, err)
	}
	a.manager.question = nil
	findFinalShell(a, func() string { return finalShellSample(t, "finalshell") })
	a.openImport()
	p.until(t, "sample import read", func() bool { return a.imp != nil && !a.imp.busy && a.imp.src != nil })
	importState := a.imp
	statuses := importState.statuses(a.hosts)
	importState.checkAll(statuses, true)
	count := len(a.hosts.Hosts)
	a.importChosen(importState, statuses)
	entries, err = config.ListBackups()
	if err != nil || len(entries) != 3 || entries[0].Reason != cloudsync.ReasonImport {
		t.Fatalf("before import: %+v, %v", entries, err)
	}
	before, err = config.ReadBackup(entries[0].ID)
	if err != nil || len(before.Hosts.Hosts) != count || len(a.hosts.Hosts) <= count {
		t.Fatalf("import backup did not capture prior state: %v", err)
	}
}

func TestBackupsPasswordRetry(t *testing.T) {
	d, _, old := setupRestoreVersions(t)
	a := d.a
	a.sync.password = "wrong password"
	a.openVersions(WindowMain)
	st := a.sync.backups
	d.p.until(t, "remote list", func() bool { return !st.listing })
	a.selectVersion(st, versionEntry{Snapshot: old, remote: true}, "")
	d.p.until(t, "wrong password", func() bool { return !st.busy })
	if !errors.Is(st.err, cloudsync.ErrWrongPassword) || st.preview != nil {
		t.Fatalf("wrong password result: %v", st.err)
	}
	a.selectVersion(st, versionEntry{Snapshot: old, remote: true}, "correct horse")
	d.p.until(t, "old password", func() bool { return !st.busy })
	if st.err != nil || st.preview == nil || a.sync.password != "wrong password" {
		t.Fatalf("old password was not one-off: %v", st.err)
	}
}

type changedBeforeRestore struct {
	*memRemote
	reads int
}

func (r *changedBeforeRestore) Get(ctx context.Context) ([]byte, cloudsync.Version, error) {
	r.reads++
	data, version, err := r.memRemote.Get(ctx)
	if r.reads == 2 && err == nil {
		data = append(data[:len(data):len(data)], '\n')
	}
	return data, version, err
}

func TestBackupsRestoreConflictPausesAcrossRestart(t *testing.T) {
	remote := &memRemote{}
	d := newDevice(t, remote)
	a := d.a
	d.addHost("before", "Before")
	d.sync(t)
	a.hosts.Remove("before")
	d.addHost("replacement", "Replacement")
	entry, err := a.saveLocalBackup(cloudsync.ReasonManual, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.openVersions(WindowMain)
	st := a.sync.backups
	d.p.until(t, "list", func() bool { return !st.listing })
	a.selectVersion(st, versionEntry{Snapshot: entry}, "")
	a.newRemote = func(config.SyncSettings, string) (cloudsync.Remote, error) {
		return &changedBeforeRestore{memRemote: remote}, nil
	}
	a.restoreVersion(st)
	d.p.until(t, "restore conflict", func() bool { return !a.sync.running })
	if !errors.Is(st.err, cloudsync.ErrConflict) || !a.sync.restorePaused || !config.LoadSyncState().RestorePaused || remote.writes() != 1 {
		t.Fatalf("unsafe restore conflict: %v; paused=%v; writes=%d", st.err, a.sync.restorePaused, remote.writes())
	}
	reopened := New(d.p)
	reopened.settings.Sync = a.settings.Sync
	reopened.loadSyncState()
	if !reopened.sync.restorePaused {
		t.Fatal("restore guard lost across restart")
	}
}

func TestBackupsMergeDoesNotResurrectPermanentlyDeletedHost(t *testing.T) {
	a, _ := testApp(t)
	a.hosts.Add(&config.Host{ID: "purged", Name: "Purged"})
	a.hosts.Add(&config.Host{ID: "keep", Name: "Old name"})
	a.hosts.Add(&config.Host{ID: "missing", Name: "Missing"})
	backup, err := a.saveLocalBackup(cloudsync.ReasonManual, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.hosts.SoftDelete("purged", time.Now())
	a.deleteItem(&a.manager, managerItem{host: "purged"})
	a.manager.question.yes()
	a.hosts.Remove("missing")
	a.hosts.Host("keep").Name = "Current name"
	a.openVersions(WindowMain)
	st := a.sync.backups
	a.selectVersion(st, versionEntry{Snapshot: backup}, "")
	a.mergeVersion(st)
	if st.err != nil || a.hosts.Host("purged") != nil || a.hosts.Host("missing") == nil || a.hosts.Host("keep").Name != "Current name" {
		t.Fatalf("merge overwrote present or resurrected removed: %v, %v", hostNames(a), st.err)
	}
}

func TestBackupsRestoreWithoutSync(t *testing.T) {
	a, _ := testApp(t)
	a.hosts.Add(&config.Host{ID: "saved", Name: "Saved"})
	if err := config.SaveCommandEditorText("original draft"); err != nil {
		t.Fatal(err)
	}
	entry, err := a.saveLocalBackup(cloudsync.ReasonManual, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a.hosts.Remove("saved")
	a.cmdCode().Buffer.Set("new draft")
	a.openVersions(WindowMain)
	st := a.sync.backups
	a.selectVersion(st, versionEntry{Snapshot: entry}, "")
	a.restoreVersion(st)
	if st.err != nil || a.hosts.Host("saved") == nil || a.cmdCode().Buffer.String() != "original draft" {
		t.Fatalf("local-only restore: %v", st.err)
	}
	versions, err := config.ListBackups()
	if err != nil || len(versions) != 2 || versions[0].Reason != cloudsync.ReasonPreRestore {
		t.Fatalf("pre-restore snapshot: %v", err)
	}
	prior, err := config.ReadBackup(versions[0].ID)
	if err != nil || string(prior.Draft) != "new draft" {
		t.Fatalf("unsaved draft not backed up: %v", err)
	}
}

func TestBackupsGuardWaitsAndKeepsServer(t *testing.T) {
	remote := &memRemote{}
	d := newDevice(t, remote)
	a := d.a
	a.settings.Sync.Auto = true
	for index := range 384 {
		d.addHost(fmt.Sprint(index), fmt.Sprintf("host-%03d", index))
	}
	for index := range 27 {
		a.hosts.Folders = append(a.hosts.Folders, &config.Folder{ID: fmt.Sprint(index), Name: fmt.Sprintf("folder-%02d", index)})
	}
	d.sync(t)
	for _, host := range a.hosts.Hosts[:374] {
		host.DeletedAt = time.Now()
	}
	a.hosts.Folders = nil
	a.startSyncWith(syncOptions{reason: cloudsync.ReasonAuto}, time.Now())
	d.p.until(t, "guard", func() bool { return a.sync.guard != nil })
	if remote.writes() != 1 {
		t.Fatal("bulk deletion reached remote")
	}
	guard := d.p.show(t, WindowSyncGuard)
	managerShows(t, guard, locales.BackupGuardTitle(), locales.BackupGuardHosts(locales.BackupServer(), 374, 384))
	managerClick(t, guard, locales.Cancel())
	main := ui.NewTester(a.View, 1000, 680)
	main.Advance(30 * time.Minute)
	if a.sync.running || remote.writes() != 1 || a.sync.guard == nil {
		t.Fatal("automatic sync bypassed the pause")
	}
	a.openSyncGuard()
	guard = d.p.show(t, WindowSyncGuard)
	managerClick(t, guard, locales.BackupKeepServer())
	d.p.until(t, "keep server", func() bool { return !a.sync.running })
	if a.sync.err != nil || a.sync.guard != nil || remote.writes() != 1 || len(a.hosts.Live()) != 384 || len(a.hosts.Folders) != 27 {
		t.Fatalf("keep server: %v, %d hosts, %d folders", a.sync.err, len(a.hosts.Live()), len(a.hosts.Folders))
	}
	log, err := config.ReadSyncLog()
	if err != nil || len(log) < 3 || !log[len(log)-2].Guard || log[len(log)-2].Server[1] != 374 {
		t.Fatalf("guard log: %+v, %v", log, err)
	}
}

func TestBackupsScreenshots(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				backupsScreenshot(t, language, mode)
			})
		}
	}
}

func backupsScreenshot(t *testing.T, language, mode string) {
	t.Helper()
	remote := &memRemote{}
	d := newDevice(t, remote)
	a, p := d.a, d.p
	p.SetLocale(language)
	a.settings.Language, a.settings.Appearance = language, config.Appearance(mode)
	root := &config.Folder{ID: "prod", Name: "Production"}
	child := &config.Folder{ID: "db", Name: "Databases", Parent: root.ID}
	a.hosts.Folders = []*config.Folder{root, child}
	d.addHost("pg", "Primary database").Folder = child.ID
	d.addHost("cache", "Cache node").Folder = root.ID
	a.quick().Commands = []*config.QuickCommand{{ID: "status", Name: "Database status", Text: "pg_isready"}}
	ctx := context.Background()
	clock := time.Date(2026, 11, 24, 2, 30, 0, 0, time.UTC)
	_, err := cloudsync.Sync(ctx, cloudsync.Request{Remote: remote, Local: a.localPayload(), Password: a.sync.password, Device: "Office PC", Reason: cloudsync.ReasonAuto, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := a.saveLocalBackup(cloudsync.ReasonManual, "Before the maintenance window", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.PinBackup(entry.ID, true); err != nil {
		t.Fatal(err)
	}
	a.hosts.SoftDelete("pg", time.Now())
	a.hosts.RemoveFolder("db")
	a.quick().Commands = nil
	d.addHost("edge", "New edge node")
	if _, err := a.saveLocalBackup(cloudsync.ReasonImport, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	a.openManager()
	manager := p.show(t, WindowManager)
	managerClick(t, manager, locales.BackupsTitle())
	st := a.sync.backups
	p.until(t, "version list", func() bool { return !st.listing })
	window := p.show(t, WindowBackups)
	frameBackupShot(t, window, "versions-list", language, mode)
	managerShows(t, window, locales.BackupServer(), locales.BackupLocal(), locales.BackupPinned(), locales.BackupCounts())
	for index, item := range st.entries {
		if item.remote {
			st.row = index
			break
		}
	}
	window.Frame()
	managerClick(t, window, locales.BackupPreview())
	p.until(t, "preview", func() bool { return !st.busy && st.preview != nil })
	frameBackupShot(t, window, "versions-preview", language, mode)
	managerShows(t, window, locales.BackupDiff(locales.BackupConnections(), 1, 1, 0), locales.BackupPreviewTitle())
	managerClick(t, window, locales.BackupRestore())
	managerShows(t, window, locales.BackupRestoreConfirm())
	frameBackupShot(t, window, "versions-restore-confirm", language, mode)
	managerClick(t, window, locales.Cancel())
	if !a.hosts.Host("pg").Deleted() {
		t.Fatal("cancel changed data")
	}
	a.recordSync(cloudsync.ReasonAuto, "paused", nil, true)
	managerClick(t, window, locales.BackupLog())
	managerShows(t, window, locales.BackupLogGuarded())
	a.closeWindow(WindowBackups)
	a.openOptions()
	a.optionsNav.Page = pageSync
	options := p.show(t, WindowOptions)
	options.SetSize(900, 915)
	frameBackupShot(t, options, "options-sync-retention", language, mode)
	managerShows(t, options, locales.BackupRetention(), locales.BackupRetainMax())
	a.sync.guard = &cloudsync.DeletionGuard{Server: cloudsync.DeleteImpact{LiveRemoved: 374, LiveTotal: 384, FoldersRemoved: 27, LargestSubtree: 45}, ServerVersion: "fake"}
	a.openSyncGuard()
	guard := p.show(t, WindowSyncGuard)
	frameBackupShot(t, guard, "sync-deletion-guard", language, mode)
	managerShows(t, guard, locales.BackupGuardHosts(locales.BackupServer(), 374, 384), locales.BackupReview())
}

func frameBackupShot(t *testing.T, tester *ui.Tester, scene, language, mode string) {
	t.Helper()
	tester.SetDark(mode == "dark")
	tester.SetLocale(language)
	if os.Getenv("SHOT_DIR") != "" {
		tester.SetScale(2)
	}
	tester.Frame()
	saveShot(t, tester, scene+"-"+mode+"-"+language+".png")
}
