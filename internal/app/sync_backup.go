package app

import (
	"encoding/json"
	"errors"
	"maps"
	"time"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo"
)

func (a *App) saveLocalBackup(reason, note string, at time.Time) (cloudsync.Snapshot, error) {
	var draft []byte
	if code := a.quickUI.box.code; code != nil {
		draft = []byte(code.Buffer.String())
	}
	quick := a.quickUI.store
	if a.quickUI.noSave || a.quickUI.loadErr != nil {
		quick = nil
	}
	entry, err := config.SaveBackup(a.hosts, a.settings, quick, draft, at, deviceName(), mygo.App.Version(), reason, note)
	if err != nil {
		return entry, err
	}
	if err := config.PruneBackups(at, a.settings.Sync); err != nil && a.sync.backups != nil {
		a.sync.backups.err = err
	}
	return entry, nil
}

func (a *App) backupBeforeDelete(m *managerState) bool {
	if _, err := a.saveLocalBackup(cloudsync.ReasonBulkDelete, "", time.Now()); err != nil {
		m.saveErr = func() string { return locales.BackupFailure(err.Error()) }
		return false
	}
	return true
}

func (a *App) rememberManagerDeletes(m *managerState, ids []string) bool {
	if err := config.RememberPermanentDeletes(ids); err != nil {
		m.saveErr = func() string { return locales.BackupFailure(err.Error()) }
		return false
	}
	return true
}

func sameSyncSecrets(first, second map[string]string) bool { return maps.Equal(first, second) }

func sameQuickPayload(first, second *cloudsync.QuickCommands) bool {
	return cloudsync.Equal(&cloudsync.Payload{Quick: first}, &cloudsync.Payload{Quick: second})
}

func (a *App) recordSync(reason, result string, res *cloudsync.Result, guard bool) {
	event := config.SyncEvent{At: time.Now().UTC(), Device: deviceName(), Reason: reason, Result: result, Guard: guard}
	if res != nil {
		event.Conflicts = res.Conflicts
		event.Local = changeCounts(res.LocalImpact)
		event.Server = changeCounts(res.ServerImpact)
	}
	if guard && a.sync.guard != nil && res == nil {
		event.Local[1] = max(a.sync.guard.Local.LiveRemoved, a.sync.guard.Local.PermanentRemoved)
		event.Server[1] = max(a.sync.guard.Server.LiveRemoved, a.sync.guard.Server.PermanentRemoved)
	}
	if err := config.AppendSyncEvent(event); err != nil && a.sync.backups != nil {
		a.sync.backups.err = err
	}
	if st := a.sync.backups; st != nil && st.log {
		st.events, st.err = config.ReadSyncLog()
	}
}

func (a *App) loadSyncState() {
	st := &a.sync
	if !st.stateLoaded {
		st.state, st.stateLoaded = config.LoadSyncState(), true
		st.restorePaused = st.state.RestorePaused && st.state.Remote == a.settings.Sync.Remote()
	}
}

func (a *App) pauseRestoreUpload(remote string) error {
	a.loadSyncState()
	st := &a.sync
	if st.state.Remote != remote {
		st.state = config.SyncState{Remote: remote}
	}
	st.restorePaused, st.state.RestorePaused = true, true
	return config.SaveSyncState(st.state)
}

func changeCounts(changes cloudsync.Changes) (count [3]int) {
	for _, group := range []cloudsync.ChangeNames{changes.Hosts, changes.Folders, changes.Commands, changes.Groups} {
		count[0] += len(group.Added)
		count[1] += len(group.Removed)
		count[2] += len(group.Changed)
	}
	return count
}

func (a *App) applyLocalBackup(backup config.LocalBackup, payload *cloudsync.Payload) error {
	if payload.Quick == nil {
		payload = payload.Clone()
		payload.Quick = &cloudsync.QuickCommands{}
	}
	if err := a.applyPayload(payload); err != nil {
		return err
	}
	next := backup.Settings
	next.Sync = a.settings.Sync
	if err := a.saveSettings(next); err != nil {
		return err
	}
	before := a.settings
	a.settings = next
	if before.Language != next.Language {
		a.plat.SetLocale(next.Language)
	}
	if before.Appearance.Normal() != next.Appearance.Normal() {
		a.plat.SetAppearance(next.Appearance.Normal())
	}
	a.restoreCmdEditor()
	if backup.Quick != nil {
		if err := config.SaveQuickCommands(backup.Quick); err != nil {
			return err
		}
		a.quickUI.store = backup.Quick
	}
	if err := config.SaveCommandEditorText(string(backup.Draft)); err != nil {
		return err
	}
	if editor := &a.quickUI.box; editor.code != nil {
		editor.code.Buffer.Set(string(backup.Draft))
		editor.saved, editor.seen = editor.code.Buffer.Version(), editor.code.Buffer.Version()
	}
	return nil
}

func versionError(err error) string {
	switch {
	case errors.Is(err, cloudsync.ErrWrongPassword):
		return locales.BackupWrongPassword()
	case errors.Is(err, cloudsync.ErrPinned):
		return locales.BackupPinnedError()
	case errors.Is(err, cloudsync.ErrHistoryUnsupported):
		return locales.BackupGistLimit()
	case errors.Is(err, cloudsync.ErrConflict):
		return locales.BackupConflict()
	default:
		return locales.BackupFailure(err.Error())
	}
}

func jsonBackup(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}
