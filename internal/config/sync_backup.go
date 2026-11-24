package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"finallyshell/internal/cloudsync"
)

const backupTimestamp = "20060102T150405.000000000Z"

var (
	backupMu     sync.Mutex
	syncLogMu    sync.Mutex
	deletedIDsMu sync.Mutex
	backupID     = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[a-z-]+-[0-9a-f]{8}$`)
	backupReason = regexp.MustCompile(`^[a-z-]{1,32}$`)
)

type LocalBackup struct {
	Hosts    *Store         `json:"hosts"`
	Settings Settings       `json:"settings"`
	Quick    *QuickCommands `json:"quickCommands,omitempty"`
	Draft    []byte         `json:"commandEditor,omitempty"`
}

type backupMeta struct {
	cloudsync.Snapshot
	Note string `json:"note,omitempty"`
}

func backupsDir() string { return filepath.Join(Dir(), "backups") }

func backupPath(id string) (string, error) {
	if !backupID.MatchString(id) {
		return "", cloudsync.ErrNotFound
	}
	path := filepath.Join(backupsDir(), id)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", cloudsync.ErrNotFound
	}
	return path, nil
}

func SaveBackup(hosts *Store, settings Settings, quick *QuickCommands, draft []byte, now time.Time, device, appVersion, reason, note string) (cloudsync.Snapshot, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	var out cloudsync.Snapshot
	if !backupReason.MatchString(reason) {
		return out, errors.New("config: invalid backup reason")
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return out, err
	}
	root := backupsDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		return out, err
	}
	stage, err := os.MkdirTemp(root, ".writing-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(stage)
	var size int64
	write := func(name string, data []byte) error {
		size += int64(len(data))
		return os.WriteFile(filepath.Join(stage, name), data, 0600)
	}
	for _, item := range []struct {
		name string
		data any
	}{{"hosts.json", hosts}, {"settings.json", settings}} {
		data, err := json.MarshalIndent(item.data, "", "  ")
		if err != nil {
			return out, err
		}
		if err := write(item.name, append(data, '\n')); err != nil {
			return out, err
		}
	}
	counts := cloudsync.Counts{Folders: len(hosts.Folders)}
	for _, host := range hosts.Hosts {
		if host.Deleted() {
			counts.Deleted++
		} else {
			counts.Live++
		}
	}
	for _, name := range []string{"quickcommands.json", CommandEditorFile} {
		data, err := os.ReadFile(filepath.Join(Dir(), name))
		if name == "quickcommands.json" && quick != nil {
			data, err = json.MarshalIndent(quick, "", "  ")
		}
		if name == CommandEditorFile && draft != nil {
			data, err = draft, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return out, err
		}
		if name == "quickcommands.json" {
			if quick, err := ParseQuickCommands(data); err == nil {
				counts.Commands = len(quick.Commands)
			}
		}
		if err := write(name, data); err != nil {
			return out, err
		}
	}
	out = cloudsync.Snapshot{ID: now.UTC().Format(backupTimestamp) + "-" + reason + "-" + hex.EncodeToString(random[:]), At: now.UTC(), Device: device, AppVersion: appVersion, Reason: reason, Note: note, Counts: counts, Counted: true, Size: size}
	data, err := json.Marshal(backupMeta{Snapshot: out, Note: note})
	if err != nil {
		return out, err
	}
	if err := write("backup.json", data); err != nil {
		return out, err
	}
	return out, os.Rename(stage, filepath.Join(root, out.ID))
}

func ListBackups() ([]cloudsync.Snapshot, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	return listBackups()
}

func listBackups() ([]cloudsync.Snapshot, error) {
	entries, err := os.ReadDir(backupsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []cloudsync.Snapshot
	var listErr error
	for _, entry := range entries {
		if !entry.IsDir() || !backupID.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(backupsDir(), entry.Name())
		data, err := os.ReadFile(filepath.Join(path, "backup.json"))
		var meta backupMeta
		if err == nil {
			err = json.Unmarshal(data, &meta)
		}
		if err != nil {
			listErr = err
			continue
		}
		meta.Snapshot.ID, meta.Snapshot.Note = entry.Name(), meta.Note
		meta.Pinned, err = backupPinned(path)
		if err != nil {
			listErr = err
			meta.Pinned = true
		}
		for _, name := range []string{"hosts.json", "settings.json", "quickcommands.json", CommandEditorFile} {
			if info, err := os.Stat(filepath.Join(path, name)); err == nil {
				meta.Size += info.Size()
			}
		}
		versions = append(versions, meta.Snapshot)
	}
	cloudsync.SortSnapshots(versions)
	return versions, listErr
}

func ReadBackup(id string) (LocalBackup, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	var result LocalBackup
	path, err := backupPath(id)
	if err != nil {
		return result, err
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(path, name)) }
	data, err := read("hosts.json")
	if err != nil {
		return result, err
	}
	result.Hosts, _, err = ParseHosts(data)
	if err != nil {
		return result, err
	}
	data, err = read("settings.json")
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result.Settings); err != nil {
		return result, err
	}
	if data, err = read("quickcommands.json"); err == nil {
		result.Quick, err = ParseQuickCommands(data)
		if err != nil {
			return result, err
		}
	} else if !os.IsNotExist(err) {
		return result, err
	}
	result.Draft, err = read(CommandEditorFile)
	if os.IsNotExist(err) {
		err = nil
	}
	return result, err
}

func backupPinned(path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(path, "pinned"))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func PinBackup(id string, pin bool) error {
	backupMu.Lock()
	defer backupMu.Unlock()
	path, err := backupPath(id)
	if err != nil {
		return err
	}
	marker := filepath.Join(path, "pinned")
	if pin {
		return writeFileAtomic(marker, []byte("pinned\n"))
	}
	if err := os.Remove(marker); !os.IsNotExist(err) {
		return err
	}
	return nil
}

func DeleteBackup(id string) error {
	backupMu.Lock()
	defer backupMu.Unlock()
	return deleteBackup(id)
}

func deleteBackup(id string) error {
	path, err := backupPath(id)
	if err != nil {
		return err
	}
	if pinned, err := backupPinned(path); err != nil {
		return err
	} else if pinned {
		return cloudsync.ErrPinned
	}
	return os.RemoveAll(path)
}

func PruneBackups(now time.Time, settings SyncSettings) error {
	backupMu.Lock()
	defer backupMu.Unlock()
	versions, err := listBackups()
	if err != nil {
		return err
	}
	count, days := settings.LocalRetention()
	retained := 0
	for _, version := range versions {
		if version.Pinned {
			continue
		}
		if retained >= count || version.At.Before(now.AddDate(0, 0, -days)) {
			if err := deleteBackup(version.ID); err != nil {
				return err
			}
		} else {
			retained++
		}
	}
	return nil
}

type SyncEvent struct {
	At        time.Time `json:"at"`
	Device    string    `json:"device"`
	Reason    string    `json:"reason"`
	Result    string    `json:"result"`
	Local     [3]int    `json:"local"`
	Server    [3]int    `json:"server"`
	Conflicts int       `json:"conflicts,omitempty"`
	Guard     bool      `json:"guard,omitempty"`
}

const syncLogLimit = 100

func syncLogPath() string { return filepath.Join(Dir(), "sync-log.json") }

func ReadSyncLog() ([]SyncEvent, error) {
	syncLogMu.Lock()
	defer syncLogMu.Unlock()
	return readSyncLog()
}

func readSyncLog() ([]SyncEvent, error) {
	data, err := os.ReadFile(syncLogPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []SyncEvent
	err = json.Unmarshal(data, &events)
	return events, err
}

func AppendSyncEvent(event SyncEvent) error {
	syncLogMu.Lock()
	defer syncLogMu.Unlock()
	events, err := readSyncLog()
	if err != nil {
		return err
	}
	events = append(events, event)
	data, err := json.MarshalIndent(events[max(0, len(events)-syncLogLimit):], "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(syncLogPath(), append(data, '\n'))
}

func readDeletedIDs() (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(Dir(), "backup-deleted.json"))
	ids := map[string]bool{}
	if os.IsNotExist(err) {
		return ids, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &ids)
	}
	if ids == nil {
		ids = map[string]bool{}
	}
	return ids, err
}

func PermanentlyDeletedIDs() (map[string]bool, error) {
	deletedIDsMu.Lock()
	defer deletedIDsMu.Unlock()
	return readDeletedIDs()
}

func RememberPermanentDeletes(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	deletedIDsMu.Lock()
	defer deletedIDsMu.Unlock()
	deleted, err := readDeletedIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		deleted[id] = true
	}
	data, err := json.Marshal(deleted)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(Dir(), "backup-deleted.json"), data)
}
