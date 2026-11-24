package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"finallyshell/internal/cloudsync"
)

func TestLocalBackupPinAndRetention(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FINALLYSHELL_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, CommandEditorFile), []byte("echo draft"), 0600); err != nil {
		t.Fatal(err)
	}
	hosts := NewStore()
	hosts.Hosts = []*Host{{ID: "host", Name: "Host", Folder: "folder"}}
	hosts.Folders = []*Folder{{ID: "folder", Name: "Folder"}}
	settings := Settings{Sync: SyncSettings{LocalBackupCount: 1, LocalBackupDays: 1}}
	now := time.Now().UTC()
	old, err := SaveBackup(hosts, settings, nil, nil, now.AddDate(0, 0, -2), "pc", "v1", cloudsync.ReasonManual, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if err := PinBackup(old.ID, true); err != nil {
		t.Fatal(err)
	}
	latest, err := SaveBackup(hosts, settings, nil, nil, now, "pc", "v1", cloudsync.ReasonImport, "")
	if err != nil {
		t.Fatal(err)
	}
	list, err := ListBackups()
	if err != nil || len(list) != 2 || list[0].ID != latest.ID || !list[1].Pinned || list[1].Note != "daily" {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if !errors.Is(DeleteBackup(old.ID), cloudsync.ErrPinned) {
		t.Fatal("pinned backup was not protected")
	}
	backup, err := ReadBackup(old.ID)
	if err != nil || backup.Hosts.Hosts[0].Folder != "folder" || string(backup.Draft) != "echo draft" {
		t.Fatalf("backup: %+v, %v", backup, err)
	}
	if err := PinBackup(old.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := PruneBackups(now, settings.Sync); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBackup(old.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired backup remains: %v", err)
	}
	if _, err := ReadBackup("../hosts.json"); err == nil {
		t.Fatal("accepted a path as a backup ID")
	}
}

func TestLocalBackupSyncLogBound(t *testing.T) {
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	for index := range syncLogLimit + 3 {
		event := SyncEvent{At: time.Unix(int64(index), 0), Device: "pc", Result: "ok", Conflicts: index}
		if err := AppendSyncEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := ReadSyncLog()
	if err != nil || len(events) != syncLogLimit || events[0].Conflicts != 3 || events[len(events)-1].Conflicts != syncLogLimit+2 {
		t.Fatalf("bounded log: %d events, %v", len(events), err)
	}
}
