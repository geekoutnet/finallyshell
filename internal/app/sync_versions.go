package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const WindowBackups WindowKind = "backups"
const WindowSyncGuard WindowKind = "sync-deletion-guard"

type versionEntry struct {
	cloudsync.Snapshot
	remote bool
}

type versionPreview struct {
	entry   versionEntry
	payload *cloudsync.Payload
	local   *config.LocalBackup
}

type versionsState struct {
	entries                      []versionEntry
	events                       []config.SyncEvent
	log, busy, listing, reloaded bool
	list, logList                ui.ListState
	row                          int
	preview                      *versionPreview
	selected                     versionEntry
	confirm, note, password      string
	err, listErr                 error
	message                      func() string
	remoteKey                    string
	source                       int
	// t is the sync whose versions these are: the Sync page's, or the
	// account's (openVersionsOn).
	t syncTarget
}

// WindowAccountBackups is the account's versions window.
const WindowAccountBackups WindowKind = "account-backups"

// versionsWindow is the versions window of the sync t.
func versionsWindow(t syncTarget) WindowKind {
	if t.account {
		return WindowAccountBackups
	}
	return WindowBackups
}

// nativeRevisions says whether the sync t keeps its versions as a gist's
// revisions, which cannot be pinned nor deleted.
func nativeRevisions(t syncTarget) bool {
	p := t.settings().Provider
	return p == config.SyncGist || p == config.SyncAccount
}

func (a *App) openVersions(parent WindowKind) { a.openVersionsOn(a.pageSync(), parent) }

// openVersionsOn opens the versions window of the sync t: its server
// versions beside this device's backups, each to preview against this
// device's data, restore or merge.
func (a *App) openVersionsOn(t syncTarget, parent WindowKind) {
	kind := versionsWindow(t)
	if a.windowOpen(kind) {
		a.plat.FocusWindow(kind)
		return
	}
	st := &versionsState{row: -1, t: t}
	t.st.backups = st
	title := locales.BackupsTitle
	if t.account {
		title = locales.AccountVersionsTitle
	}
	a.openWindow(WindowSpec{Kind: kind, Title: title, Parent: parent,
		Width: 1000, Height: 620, MinWidth: 920, MinHeight: 480,
		Closed: func() { t.st.backups = nil },
	}, func(c *ui.Context) { a.versionsView(c, st) })
	if t.account {
		a.loadAccount()
	} else {
		a.loadSyncSecrets()
	}
	a.loadSyncStateOn(t)
	a.refreshVersions(st)
}

// versionsLog is the sync log the versions window shows: the Sync
// page's; the account's is in its own window.
func (a *App) versionsLog(st *versionsState) ([]config.SyncEvent, error) {
	if st.t.account {
		return nil, nil
	}
	return config.ReadSyncLog()
}

func (a *App) refreshVersions(st *versionsState) {
	if st.listing {
		st.reloaded = true
		return
	}
	st.listing, st.listErr = true, nil
	local, diskErr := config.ListBackups()
	st.events, st.err = a.versionsLog(st)
	key := st.t.settings().Remote()
	var remote cloudsync.Remote
	var setupErr error
	if a.syncReadyOn(st.t) {
		remote, setupErr = a.newRemote(st.t.settings(), st.t.st.credential)
	}
	apply := func(server []cloudsync.Snapshot, err error) {
		st.listing = false
		if st.t.st.backups != st {
			return
		}
		if st.t.settings().Remote() != key {
			st.selected, st.preview, st.row = versionEntry{}, nil, -1
			a.refreshVersions(st)
			return
		}
		if st.remoteKey != key && st.selected.remote {
			st.selected, st.preview, st.row = versionEntry{}, nil, -1
		}
		st.remoteKey = key
		st.entries = nil
		for _, version := range local {
			st.entries = append(st.entries, versionEntry{Snapshot: version})
		}
		for _, version := range server {
			st.entries = append(st.entries, versionEntry{Snapshot: version, remote: true})
		}
		slices.SortStableFunc(st.entries, func(first, second versionEntry) int {
			if order := second.At.Compare(first.At); order != 0 {
				return order
			}
			return strings.Compare(second.ID, first.ID)
		})
		st.row = slices.IndexFunc(st.visibleVersions(), func(entry versionEntry) bool { return entry.ID == st.selected.ID && entry.remote == st.selected.remote })
		if st.row < 0 {
			st.preview, st.selected = nil, versionEntry{}
		} else {
			st.selected = st.visibleVersions()[st.row]
		}
		st.listErr = errors.Join(diskErr, err)
		if st.reloaded {
			st.reloaded = false
			a.refreshVersions(st)
		}
	}
	if remote == nil {
		apply(nil, setupErr)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		server, err := remote.List(ctx)
		a.post(func() { apply(server, err) })
	}()
}

func (a *App) selectVersion(st *versionsState, entry versionEntry, password string) {
	st.err, st.preview, st.message, st.password = nil, nil, nil, ""
	st.selected = entry
	if !entry.remote {
		backup, err := config.ReadBackup(entry.ID)
		if err != nil {
			st.err = err
			return
		}
		st.preview = &versionPreview{entry: entry, payload: backupPayload(backup.Hosts, backup.Settings, backup.Quick), local: &backup}
		if st.preview.payload.Quick == nil {
			st.preview.payload.Quick = &cloudsync.QuickCommands{}
		}
		return
	}
	if password == "" {
		password = st.t.st.password
	}
	a.versionRemoteTask(st, func(ctx context.Context, remote cloudsync.Remote) (func(), error) {
		data, err := remote.GetVersion(ctx, entry.ID)
		if err != nil {
			return nil, err
		}
		payload, err := cloudsync.OpenSnapshot(password, data)
		metadata := cloudsync.DescribeRevision(entry.Snapshot, data)
		if err != nil {
			return nil, err
		}
		metadata.Note = payload.Note
		if !metadata.Counted {
			metadata.Counts, metadata.Counted = payload.Counts(), true
			if payload.Device != "" {
				metadata.Device = payload.Device
			}
		}
		return func() {
			entry.Snapshot = metadata
			st.preview = &versionPreview{entry: entry, payload: payload}
			if index := slices.IndexFunc(st.entries, func(item versionEntry) bool { return item.ID == entry.ID && item.remote }); index >= 0 {
				st.entries[index] = entry
			}
			st.selected = entry
		}, nil
	})
}

func (a *App) versionRemoteTask(st *versionsState, work func(context.Context, cloudsync.Remote) (func(), error)) {
	key := st.t.settings().Remote()
	if !a.syncReadyOn(st.t) || st.remoteKey != key {
		st.err = cloudsync.ErrConflict
		return
	}
	remote, err := a.newRemote(st.t.settings(), st.t.st.credential)
	if err != nil {
		st.err = err
		return
	}
	st.busy, st.err = true, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		apply, err := work(ctx, remote)
		a.post(func() {
			st.busy = false
			if st.t.st.backups != st {
				return
			}
			if st.t.settings().Remote() != key {
				err, apply = cloudsync.ErrConflict, nil
			}
			st.err = err
			if err == nil && apply != nil {
				apply()
			}
		})
	}()
}

func (a *App) changeVersion(st *versionsState, remove bool) {
	entry := st.selected
	if !entry.remote {
		var err error
		if remove {
			err = config.DeleteBackup(entry.ID)
		} else {
			err = config.PinBackup(entry.ID, !entry.Pinned)
		}
		if err == nil {
			a.refreshVersions(st)
		}
		st.err = err
		return
	}
	a.versionRemoteTask(st, func(ctx context.Context, remote cloudsync.Remote) (func(), error) {
		var err error
		if remove {
			err = remote.DeleteVersion(ctx, entry.ID)
		} else {
			err = remote.PinVersion(ctx, entry.ID, !entry.Pinned)
		}
		return func() { a.refreshVersions(st) }, err
	})
}

func (a *App) manualBackup(st *versionsState, now time.Time) {
	st.err, st.message = nil, nil
	note := strings.TrimSpace(st.note)
	if utf8.RuneCountInString(note) > 200 {
		st.message = locales.BackupNoteTooLong
		return
	}
	_, err := a.saveLocalBackup(cloudsync.ReasonManual, note, now)
	if err != nil {
		st.err = err
		return
	}
	st.note = ""
	st.message = locales.BackupSaved
	a.refreshVersions(st)
	if t := st.t; t.st.guard == nil && t.st.choice == nil && !t.st.restorePaused && !a.syncBusy() && a.syncReadyOn(t) {
		a.startSyncOn(t, syncOptions{reason: cloudsync.ReasonManual, note: note, manualVersion: true}, now)
	}
}

func (a *App) mergeVersion(st *versionsState) {
	before := a.localPayload()
	deleted, err := config.PermanentlyDeletedIDs()
	if err != nil {
		st.err = err
		return
	}
	merged := cloudsync.AddMissing(before, st.preview.payload, deleted)
	if cloudsync.Equal(before, merged) {
		st.message = locales.BackupNothingToMerge
		return
	}
	if _, err := a.saveLocalBackup(cloudsync.ReasonPreRestore, "", time.Now()); err != nil {
		st.err = err
		return
	}
	if err := a.applyPayload(merged); err != nil {
		st.err = err
		return
	}
	st.message, st.t.st.nextCheck = locales.BackupMerged, time.Time{}
	a.refreshVersions(st)
	a.redraw()
}

func (a *App) exportVersion(st *versionsState) {
	preview := st.preview
	var value any = preview.payload
	if preview.local != nil {
		value = preview.local
	}
	data, err := jsonBackup(value)
	if err != nil {
		st.err = err
		return
	}
	st.busy = true
	title := locales.BackupExport()
	go func() {
		path, err := a.plat.SaveFile(versionsWindow(st.t), title, "finallyshell-backup.json", "JSON", []string{"json"})
		if err == nil && path != "" {
			file, openErr := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			err = openErr
			if err == nil {
				_, err = file.Write(data)
				err = errors.Join(err, file.Close())
			}
		}
		a.post(func() {
			st.busy, st.err = false, err
			if err == nil && path != "" {
				st.message = locales.BackupExported
			}
		})
	}()
}

func (a *App) versionsView(c *ui.Context, st *versionsState) {
	ui.Column(c).Fill().Gap(10).Padding(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, locales.BackupVersions()).Clicked() {
				st.log = false
			}
			if !st.t.account && ui.Button(c, locales.BackupLog()).Clicked() {
				st.log = true
				st.events, st.err = config.ReadSyncLog()
			}
			ui.Spacer(c)
			if !st.log {
				sources := []string{locales.BackupAllSources(), locales.BackupServer(), locales.BackupLocal()}
				selected := sources[st.source]
				if ui.Select(c, &selected, sources).Label(locales.BackupSource()).Width(132).Disabled(st.busy).Changed() {
					st.source = max(0, slices.Index(sources, selected))
					st.row, st.selected, st.preview = -1, versionEntry{}, nil
					st.err = nil
				}
			}
			if st.listing || st.busy {
				ui.Spinner(c)
			}
			if ui.Button(c, locales.BackupRefresh()).Disabled(st.listing || st.busy).Clicked() {
				a.refreshVersions(st)
			}
			if ui.PrimaryButton(c, locales.BackupNow()).Disabled(st.busy).Clicked() {
				st.confirm, st.note = "manual", ""
			}
		})
		if st.t.account {
			widgets.Muted(c, locales.AccountVersionsHint()).FontSize(12)
		} else if nativeRevisions(st.t) {
			widgets.Muted(c, locales.BackupGistLimit()).FontSize(12)
		}
		if st.t.settings().Provider != config.SyncOff && !a.syncReadyOn(st.t) {
			widgets.Muted(c, locales.BackupServerUnavailable()).FontSize(12)
		}
		if st.log {
			a.versionsLogView(c, st)
		} else {
			a.versionsListView(c, st)
		}
		if st.listErr != nil {
			ui.Text(c, versionError(st.listErr)).TextColor(theme.Danger).SingleLine().MinWidth(0)
		}
		if st.err != nil && !errors.Is(st.err, cloudsync.ErrWrongPassword) {
			ui.Text(c, versionError(st.err)).TextColor(theme.Danger)
		}
		if st.message != nil {
			widgets.Muted(c, st.message())
		}
	})
	a.versionConfirm(c, st)
}

func (a *App) versionsListView(c *ui.Context, st *versionsState) {
	entries := st.visibleVersions()
	cols := []ui.TableColumn{
		{Title: locales.BackupTime(), Width: 138}, {Title: locales.BackupSource(), Width: 104},
		{Title: locales.BackupDevice(), MinWidth: 108}, {Title: locales.BackupCounts(), Width: 224},
		{Title: locales.BackupReason(), MinWidth: 124}, {Title: locales.BackupSize(), Width: 80}, {Title: locales.BackupPinned(), Width: 74},
	}
	if len(entries) == 0 {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			widgets.Muted(c, locales.BackupEmpty())
		})
		return
	}
	st.list.Selected = &st.row
	st.list.Label = func(row int) string {
		return versionCell(entries[row], 0) + " · " + versionCell(entries[row], 1)
	}
	table := ui.Table(c, &st.list, cols, len(entries), func(row, col int) {
		entry := entries[row]
		color := theme.Text
		if row == st.row {
			color = theme.AccentText
		}
		text := versionCell(entry, col)
		ui.Text(c, text).TextColor(color).SingleLine().MinWidth(0).Tooltip(text)
	}).Label(locales.BackupVersions()).Grow(1).MinHeight(120).Border(1, theme.Border).Radius(4)
	if row := st.row; !st.busy && row >= 0 && row < len(entries) && (entries[row].ID != st.selected.ID || entries[row].remote != st.selected.remote) {
		st.selected = entries[row]
		st.preview, st.err, st.message = nil, nil, nil
	}
	if table.Submitted() && !st.busy && st.selected.ID != "" {
		a.selectVersion(st, st.selected, "")
	}
	if st.selected.ID == "" {
		widgets.Muted(c, locales.BackupChoose())
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, locales.BackupPreview()).Disabled(st.busy).Clicked() {
			a.selectVersion(st, st.selected, "")
		}
		a.versionActions(c, st)
	})
	if errors.Is(st.err, cloudsync.ErrWrongPassword) {
		ui.Text(c, locales.BackupWrongPassword()).TextColor(theme.Danger)
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &st.password).Password().Width(230).Label(locales.SyncPassword())
			if ui.Button(c, locales.BackupOpen()).Disabled(st.password == "" || st.busy).Clicked() {
				a.selectVersion(st, st.selected, st.password)
			}
		})
	}
	if st.preview != nil {
		a.versionPreviewView(c, st.preview)
	}
}

func (st *versionsState) visibleVersions() []versionEntry {
	if st.source == 0 {
		return st.entries
	}
	var visible []versionEntry
	for _, entry := range st.entries {
		if entry.remote == (st.source == 1) {
			visible = append(visible, entry)
		}
	}
	return visible
}

func (a *App) versionActions(c *ui.Context, st *versionsState) {
	if st.preview != nil {
		if ui.PrimaryButton(c, locales.BackupRestore()).Disabled(st.busy || a.syncBusy()).Clicked() {
			st.confirm = "restore"
		}
		if ui.Button(c, locales.BackupMerge()).Disabled(st.busy || a.syncBusy()).Clicked() {
			a.mergeVersion(st)
		}
		if ui.Button(c, locales.BackupExport()).Disabled(st.busy).Clicked() {
			st.confirm = "export"
		}
	}
	ui.Spacer(c)
	if !st.selected.remote || !nativeRevisions(st.t) {
		pin := locales.BackupPin()
		if st.selected.Pinned {
			pin = locales.BackupUnpin()
		}
		if ui.Button(c, pin).Disabled(st.busy).Clicked() {
			a.changeVersion(st, false)
		}
		if ui.Button(c, locales.Delete()).Disabled(st.busy || st.selected.Pinned).Clicked() {
			st.confirm = "delete"
		}
	}
}

func (a *App) versionPreviewView(c *ui.Context, preview *versionPreview) {
	changes := cloudsync.Compare(a.localPayload(), preview.payload)
	added, removed, changed := changes.Total()
	ui.Column(c).Gap(7).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.BackupPreviewTitle()).Bold()
			widgets.Muted(c, locales.BackupPreviewTotal(added, removed, changed)).FontSize(12).Shrink(0)
			if preview.entry.Note != "" {
				widgets.Muted(c, preview.entry.Note).SingleLine().MinWidth(0)
			}
		})
		if removed > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Padding(6, 8).Radius(4).Background(colors.warningBanner).Children(func() {
				ui.Icon(c, icons.Get("triangle-alert")).FontSize(13).TextColor(theme.Warning).Shrink(0)
				ui.Text(c, locales.BackupPreviewLoses(removed)).FontSize(12).TextColor(colors.warningText).Grow(1).MinWidth(0)
			})
		}
		if added+removed+changed == 0 {
			widgets.Muted(c, locales.BackupPreviewSame()).FontSize(12)
			return
		}
		ui.Scroll(c).FillWidth().Height(200).Children(func() {
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				for _, g := range []struct {
					name    string
					changes cloudsync.ChangeNames
					always  bool
				}{
					{locales.BackupConnections(), changes.Hosts, true},
					{locales.BackupFolders(), changes.Folders, true},
					{locales.BackupCommands(), changes.Commands, true},
					{locales.BackupCommandGroups(), changes.Groups, false},
					{locales.BackupSettingsDiff(), changes.Settings, false},
					{locales.ProxyMgrTitle(), changes.Proxies, false},
					{locales.BackupDatabasesDiff(), changes.Databases, false},
					{locales.BackupKnownHostsDiff(), changes.KnownHosts, false},
					{locales.BackupHistoryDiff(), a.historyNames(changes.History), false},
				} {
					if g.always || len(g.changes.Added)+len(g.changes.Removed)+len(g.changes.Changed) != 0 {
						a.versionDiffGroup(c, g.name, g.changes)
					}
				}
			})
		})
	})
}

// historyNames are the hosts of command histories, by their name rather
// than their ID.
func (a *App) historyNames(changes cloudsync.ChangeNames) cloudsync.ChangeNames {
	name := func(ids []string) []string {
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			switch h := a.hosts.Host(id); {
			case id == localHistoryID:
				out = append(out, locales.LocalTerminal())
			case h != nil && h.Name != "":
				out = append(out, h.Name)
			default:
				out = append(out, id)
			}
		}
		return out
	}
	return cloudsync.ChangeNames{Added: name(changes.Added), Removed: name(changes.Removed), Changed: name(changes.Changed)}
}

func (a *App) versionDiffGroup(c *ui.Context, name string, changes cloudsync.ChangeNames) {
	ui.Text(c, locales.BackupDiff(name, len(changes.Added), len(changes.Removed), len(changes.Changed))).Bold()
	for _, group := range []struct {
		label string
		names []string
	}{
		{locales.BackupAdded(), changes.Added}, {locales.BackupRemoved(), changes.Removed}, {locales.BackupChanged(), changes.Changed},
	} {
		if len(group.names) != 0 {
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				widgets.Muted(c, group.label).FontSize(12).Shrink(0)
				widgets.Muted(c, strings.Join(group.names, locales.ListSeparator())).FontSize(12).Grow(1).MinWidth(0)
			})
		}
	}
}

func versionCell(entry versionEntry, col int) string {
	switch col {
	case 0:
		return entry.At.Local().Format("2006-01-02 15:04")
	case 1:
		if entry.remote {
			return locales.BackupServer()
		}
		return locales.BackupLocal()
	case 2:
		if entry.Device != "" {
			return entry.Device
		}
	case 3:
		if entry.Counted {
			count := entry.Counts
			return fmt.Sprintf("%d / %d / %d / %d", count.Live, count.Deleted, count.Folders, count.Commands)
		}
	case 4:
		return versionReason(entry.Reason)
	case 5:
		if entry.Size > 0 {
			return fmt.Sprintf("%.1f KiB", float64(entry.Size)/1024)
		}
	case 6:
		if entry.Pinned {
			return locales.BackupPinned()
		}
	}
	return "—"
}

func versionReason(reason string) string {
	switch reason {
	case cloudsync.ReasonAuto:
		return locales.BackupReasonAuto()
	case cloudsync.ReasonManual:
		return locales.BackupReasonManual()
	case cloudsync.ReasonRestore:
		return locales.BackupRestore()
	case cloudsync.ReasonPreRestore:
		return locales.BackupReasonPreRestore()
	case cloudsync.ReasonPreSync:
		return locales.BackupReasonPreSync()
	case cloudsync.ReasonBulkDelete:
		return locales.BackupReasonDelete()
	case cloudsync.ReasonImport:
		return locales.BackupReasonImport()
	}
	return "—"
}

func (a *App) versionsLogView(c *ui.Context, st *versionsState) {
	if len(st.events) == 0 {
		widgets.Muted(c, locales.BackupLogEmpty()).Grow(1)
		return
	}
	cols := []ui.TableColumn{
		{Title: locales.BackupTime(), Width: 128}, {Title: locales.BackupDevice(), Width: 130}, {Title: locales.BackupReason(), Width: 116},
		{Title: locales.BackupLogResult(), Width: 140}, {Title: locales.BackupLocal(), Width: 100},
		{Title: locales.BackupServer(), Width: 100}, {Title: locales.BackupLogConflicts(), Width: 78},
		{Title: locales.BackupLogGuard(), Width: 94},
	}
	ui.Table(c, &st.logList, cols, len(st.events), func(row, col int) {
		event := st.events[len(st.events)-1-row]
		value := ""
		switch col {
		case 0:
			value = event.At.Local().Format("2006-01-02 15:04")
		case 1:
			value = event.Device
		case 2:
			value = versionReason(event.Reason)
		case 3:
			switch event.Result {
			case "paused":
				value = locales.BackupGuardTitle()
			case "ok":
				value = locales.BackupLogOK()
			case "history-warning":
				value = locales.BackupLogWarning()
			default:
				value = locales.BackupLogFailed()
			}
		case 4:
			value = fmt.Sprintf("+%d / −%d / ~%d", event.Local[0], event.Local[1], event.Local[2])
		case 5:
			value = fmt.Sprintf("+%d / −%d / ~%d", event.Server[0], event.Server[1], event.Server[2])
		case 6:
			value = fmt.Sprint(event.Conflicts)
		case 7:
			value = "—"
			if event.Guard {
				value = locales.BackupLogGuarded()
			}
		}
		ui.Text(c, value).SingleLine().MinWidth(0)
	}).Label(locales.BackupLog()).Grow(1).MinHeight(0)
}

func (a *App) versionConfirm(c *ui.Context, st *versionsState) {
	if st.confirm == "" {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(430).Gap(12).Children(func() {
			var title, description string
			var confirm func()
			switch st.confirm {
			case "manual":
				title, description = locales.BackupNow(), locales.BackupManualBody()
				confirm = func() { a.manualBackup(st, c.Now()) }
			case "restore":
				title, description = locales.BackupRestore(), locales.BackupRestoreConfirm()
				if st.preview != nil {
					if _, removed, _ := cloudsync.Compare(a.localPayload(), st.preview.payload).Total(); removed > 0 {
						description += "\n\n" + locales.BackupPreviewLoses(removed)
					}
				}
				if st.t.settings().Provider != config.SyncOff && !a.syncReadyOn(st.t) && st.preview != nil && st.preview.local != nil {
					description = locales.BackupRestoreOfflineConfirm()
				}
				confirm = func() { a.restoreVersion(st) }
			case "export":
				title, description, confirm = locales.BackupExport(), locales.BackupExportWarning(), func() { a.exportVersion(st) }
			case "delete":
				title, description, confirm = locales.Delete(), locales.BackupDeleteConfirm(), func() { a.changeVersion(st, true) }
			}
			ui.Text(c, title).Bold().FontSize(15)
			ui.Text(c, description)
			if st.confirm == "manual" {
				ui.TextInput(c, &st.note).Placeholder(locales.BackupNote()).Label(locales.BackupNote()).FillWidth()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, title).Clicked() && confirm != nil {
					open = false
					confirm()
				}
			})
		})
	})
	if !open {
		st.confirm = ""
	}
}

func (a *App) openSyncGuard() { a.openSyncGuardOn(a.pageSync()) }

// guardWindow is the window of the deletion guard of the sync t.
func guardWindow(t syncTarget) WindowKind {
	if t.account {
		return WindowAccountGuard
	}
	return WindowSyncGuard
}

// openSyncGuardOn opens the deletion guard of the sync t.
func (a *App) openSyncGuardOn(t syncTarget) {
	if t.st.guard == nil {
		return
	}
	height := 145
	for _, impact := range []cloudsync.DeleteImpact{t.st.guard.Server, t.st.guard.Local} {
		if impact.Risky() {
			height += 35
			if impact.PermanentRemoved != 0 {
				height += 27
			}
			if impact.FoldersRemoved != 0 {
				height += 27
			}
		}
	}
	kind := guardWindow(t)
	a.openWindow(WindowSpec{Kind: kind, Title: locales.BackupGuardTitle, Width: 740, Height: height, MinWidth: 665, MinHeight: height}, func(c *ui.Context) { a.syncGuardView(c, t) })
}

func (a *App) syncGuardView(c *ui.Context, t syncTarget) {
	guard := t.st.guard
	if guard == nil {
		return
	}
	kind := guardWindow(t)
	ui.Column(c).Fill().Gap(12).Padding(18).Children(func() {
		ui.Text(c, locales.BackupGuardTitle()).Bold().FontSize(16).TextColor(theme.Danger)
		if guard.Server.Risky() {
			a.guardImpactView(c, locales.BackupServer(), guard.Server)
		}
		if guard.Local.Risky() {
			a.guardImpactView(c, locales.BackupLocal(), guard.Local)
		}
		widgets.Muted(c, locales.BackupGuardWait())
		ui.Spacer(c)
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, locales.BackupReview()).Clicked() {
				a.closeWindow(kind)
				a.openVersionsOn(t, WindowMain)
			}
			if ui.Button(c, locales.BackupKeepServer()).Disabled(guard.ServerVersion == "" || !a.syncReadyOn(t)).Clicked() {
				a.closeWindow(kind)
				a.startSyncOn(t, syncOptions{keepServer: true, expectedServer: guard.ServerVersion, reason: cloudsync.ReasonRestore, priorGuard: guard}, c.Now())
			}
			if ui.Button(c, locales.BackupContinue()).Disabled(!a.syncReadyOn(t)).Clicked() {
				options := t.st.guardRun
				options.approval = guard.Approval
				options.priorGuard = guard
				a.closeWindow(kind)
				a.startSyncOn(t, options, c.Now())
			}
			if ui.Button(c, locales.Cancel()).Clicked() {
				a.closeWindow(kind)
			}
		})
	})
}

func (a *App) guardImpactView(c *ui.Context, source string, impact cloudsync.DeleteImpact) {
	if impact.LiveRemoved != 0 {
		ui.Text(c, locales.BackupGuardHosts(source, impact.LiveRemoved, impact.LiveTotal))
	}
	if impact.PermanentRemoved != 0 {
		ui.Text(c, locales.BackupGuardPermanent(source, impact.PermanentRemoved, impact.PermanentTotal))
	}
	if impact.FoldersRemoved != 0 {
		ui.Text(c, locales.BackupGuardFolders(source, impact.FoldersRemoved, impact.LargestSubtree))
	}
}

func (a *App) restoreVersion(st *versionsState) {
	preview := st.preview
	if preview == nil || a.syncBusy() || st.busy {
		return
	}
	t := st.t
	settings, password, credential := t.settings(), t.st.password, t.st.credential
	if preview.entry.remote && st.remoteKey != settings.Remote() {
		st.err = cloudsync.ErrConflict
		return
	}
	var remote cloudsync.Remote
	if a.syncReadyOn(t) {
		var err error
		remote, err = a.newRemote(settings, credential)
		if err != nil && preview.local == nil {
			st.err = err
			return
		}
		if err != nil {
			remote = nil
		}
	}
	before, beforeSettings := a.localPayload(), a.settings
	st.busy, st.err, t.st.running = true, nil, true
	finish := func(err error) {
		st.busy, st.err, t.st.running = false, err, false
		if err != nil {
			a.recordSyncOn(t, cloudsync.ReasonRestore, "failed", nil, false)
		}
	}
	a.readSyncSecrets(settings.Passwords, func(secrets map[string]string) {
		apply := func(base []byte, err error) {
			if err != nil {
				finish(err)
				return
			}
			if !cloudsync.Equal(before, a.localPayload()) || !reflect.DeepEqual(a.settings, beforeSettings) || t.st.password != password || t.st.credential != credential {
				finish(cloudsync.ErrConflict)
				return
			}
			if _, err := a.saveLocalBackup(cloudsync.ReasonPreRestore, "", time.Now()); err != nil {
				finish(err)
				return
			}
			if settings.Provider != config.SyncOff {
				if err := a.pauseRestoreUploadOn(t, settings.Remote()); err != nil {
					finish(err)
					return
				}
			}
			if preview.local != nil {
				err = a.applyLocalBackup(*preview.local, preview.payload)
			} else {
				err = a.applyPayload(preview.payload)
			}
			if err != nil {
				finish(err)
				return
			}
			t.st.guard, t.st.choice = nil, nil
			a.closeWindow(guardWindow(t))
			a.closeWindow(choiceWindow(t))
			if remote == nil {
				finish(nil)
				result := "ok"
				st.message = locales.BackupRestored
				if settings.Provider != config.SyncOff {
					result = "failed"
					st.message = locales.BackupRestoreLocalOnly
				}
				a.recordSyncOn(t, cloudsync.ReasonRestore, result, &cloudsync.Result{LocalImpact: cloudsync.Compare(before, a.localPayload())}, false)
				a.refreshVersions(st)
				return
			}
			current := a.localPayload()
			req := cloudsync.Request{Remote: remote, Local: current, Base: base, Force: true, StrictBase: true,
				Password: password, Device: deviceName(), AppVersion: mygo.App.Version(), Reason: cloudsync.ReasonRestore,
				Retention: settings.RemoteRetention(), AllowDeletion: true, Secrets: secrets != nil}
			if t.account {
				a.accountPasswordRequest(&req)
				req.AltPasswords = nil // restoring seals with this device's password
			}
			if secrets != nil {
				current.Secrets = map[string]string{}
				for _, key := range a.syncSecretKeys() {
					if value, ok := preview.payload.Secrets[key]; ok {
						current.Secrets[key] = value
					} else if value, ok := secrets[key]; ok && len(preview.payload.Secrets) == 0 {
						current.Secrets[key] = value
					}
				}
				a.applySyncedSecrets(secrets, current.Secrets, t.st)
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
				defer cancel()
				res, err := cloudsync.Sync(ctx, req)
				run := syncRun{start: settings, snapshot: current, options: syncOptions{reason: cloudsync.ReasonRestore}, target: t, password: password}
				if gist, ok := remote.(*cloudsync.Gist); ok {
					run.gistID = gist.ID
				}
				a.post(func() {
					st.busy = false
					if st.t.settings().Remote() != settings.Remote() {
						finish(cloudsync.ErrConflict)
						return
					}
					if err != nil {
						st.message = locales.BackupRestoreLocalOnly
						finish(err)
						return
					}
					run.secrets = current.Secrets
					res.LocalImpact = cloudsync.Compare(before, current)
					st.message = locales.BackupRestored
					a.syncDone(res, nil, run)
					if t.st.err != nil {
						st.err = t.st.err
					}
				})
			}()
		}
		if remote == nil {
			apply(nil, nil)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
			defer cancel()
			base, _, err := remote.Get(ctx)
			if errors.Is(err, cloudsync.ErrNotFound) {
				err = nil
			}
			a.post(func() {
				if err != nil && preview.local != nil {
					remote = nil
					apply(nil, nil)
					return
				}
				apply(base, err)
			})
		}()
	})
}
