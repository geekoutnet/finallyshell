package app

// Sync: the connections, the settings that travel and, if the user
// wants, the saved passwords, kept in one file on storage the user has (a
// GitHub gist, a WebDAV folder, an S3-compatible bucket), encrypted on
// this device with the sync password (internal/cloudsync). The Sync page
// of the options is options_sync.go.
//
// A sync builds this device's payload on the UI thread, reads the saved
// passwords on the secret queue when they are included, then reads,
// merges and writes the remote file on a goroutine, and applies the
// result back on the UI thread, merging again what changed here
// meanwhile.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// The keys of the sync's own secrets in the credential store.
const syncPasswordKey = "sync/password"

// syncCredentialKey is the key of the provider's credential: a GitHub
// token, a WebDAV password, an S3 secret key.
func syncCredentialKey(p config.SyncProvider) string { return "sync/" + string(p) + "/credential" }

// How long a sync may take, and how often automatic sync looks for local
// changes.
const (
	syncTimeout    = 2 * time.Minute
	syncCheckEvery = time.Minute
)

// syncState is the app's side of sync. Its fields belong to the UI
// thread.
type syncState struct {
	// The sync password and the provider's credential, read from the
	// credential store once (loaded), or typed this run.
	password   string
	credential string
	credFor    config.SyncProvider // the provider credential is of
	loaded     bool
	loading    bool

	running bool
	// lastTry is when the last sync started; nextCheck is when automatic
	// sync looks for local changes next.
	lastTry, nextCheck time.Time
	// err is why the last sync failed, nil when it worked.
	err error
	// done says what the last sync that worked did.
	done *cloudsync.Result

	// state is what sync-state.json keeps, read with the first sync.
	state       config.SyncState
	stateLoaded bool
	// synced is this device's payload, without secrets, as last synced:
	// automatic sync syncs again when it changes.
	synced *cloudsync.Payload

	// confirmReplace asks before replacing the remote file.
	confirmReplace bool
	// again: something changed here during the last sync that it did
	// not carry, as a password saved meanwhile; automatic sync syncs
	// again at its next check.
	again         bool
	guard         *cloudsync.DeletionGuard
	guardRun      syncOptions
	backups       *versionsState
	restorePaused bool
}

type syncOptions struct {
	force, keepServer, manualVersion       bool
	approval, expectedServer, reason, note string
	priorGuard                             *cloudsync.DeletionGuard
}

// syncRun is what a sync started with, for syncDone.
type syncRun struct {
	// start is the Sync settings it started with: a result for another
	// destination than the current one is dropped.
	start config.SyncSettings
	// snapshot is this device's payload, without secrets; secrets are
	// the saved passwords it included, nil for none.
	snapshot *cloudsync.Payload
	secrets  map[string]string
	// gistID is the gist the sync found or made, "" for another service.
	gistID  string
	options syncOptions
}

// syncRemote makes the remote of the settings, with the provider's
// credential. Tests replace App.newRemote.
func syncRemote(s config.SyncSettings, credential string) (cloudsync.Remote, error) {
	switch s.Provider {
	case config.SyncGist:
		return &cloudsync.Gist{Token: credential, ID: s.GistID}, nil
	case config.SyncWebDAV:
		return &cloudsync.WebDAV{URL: s.WebDAVURL, User: s.WebDAVUser, Password: credential}, nil
	case config.SyncS3:
		return &cloudsync.S3{
			Endpoint: s.S3Endpoint, Region: s.S3Region, Bucket: s.S3Bucket, Key: s.S3Key,
			AccessKeyID: s.S3AccessKeyID, SecretAccessKey: credential,
		}, nil
	}
	return nil, errors.New("sync is off")
}

// syncReady says whether every field the provider needs is filled in,
// the sync password and credential included.
func (a *App) syncReady() bool {
	s, st := a.settings.Sync, &a.sync
	if st.password == "" || st.credential == "" || st.credFor != s.Provider {
		return false
	}
	switch s.Provider {
	case config.SyncGist:
		return true
	case config.SyncWebDAV:
		return strings.TrimSpace(s.WebDAVURL) != "" && strings.TrimSpace(s.WebDAVUser) != ""
	case config.SyncS3:
		return strings.TrimSpace(s.S3Endpoint) != "" && strings.TrimSpace(s.S3Bucket) != "" && strings.TrimSpace(s.S3AccessKeyID) != ""
	}
	return false
}

// loadSyncSecrets reads the sync password and the provider's credential
// from the credential store, once per provider.
func (a *App) loadSyncSecrets() {
	st, p := &a.sync, a.settings.Sync.Provider
	if p == config.SyncOff || st.loading || (st.loaded && st.credFor == p) {
		return
	}
	st.loading = true
	pending := 2
	done := func() {
		if pending--; pending == 0 {
			st.loading, st.loaded = false, true
			if st.backups != nil {
				a.refreshVersions(st.backups)
			}
			a.redraw()
		}
	}
	if !st.loaded {
		a.secret(syncPasswordKey, func(v string, ok bool) {
			if ok && st.password == "" {
				st.password = v
			}
			done()
		})
	} else {
		pending--
	}
	a.secret(syncCredentialKey(p), func(v string, ok bool) {
		if st.credFor != p {
			st.credential, st.credFor = "", p
		}
		if ok && st.credential == "" {
			st.credential = v
		}
		done()
	})
}

// setSyncPassword keeps the sync password typed on the Sync page.
func (a *App) setSyncPassword(pw string) {
	a.sync.password = pw
	a.keepSyncSecret(syncPasswordKey, pw)
}

// setSyncCredential keeps the provider's credential typed on the Sync
// page.
func (a *App) setSyncCredential(v string) {
	a.sync.credential, a.sync.credFor = v, a.settings.Sync.Provider
	a.keepSyncSecret(syncCredentialKey(a.settings.Sync.Provider), v)
}

func (a *App) keepSyncSecret(key, v string) {
	if v == "" {
		a.deleteSecret(key)
	} else {
		a.setSecret(key, v)
	}
}

// localPayload is this device's payload, without secrets: its folders
// and hosts, each without the time it last connected, which stays on this
// device, and the settings that are synced.
func (a *App) localPayload() *cloudsync.Payload {
	return backupPayload(a.hosts, a.settings, a.quick())
}

func backupPayload(hosts *config.Store, settings config.Settings, quick *config.QuickCommands) *cloudsync.Payload {
	p := &cloudsync.Payload{Schema: cloudsync.PayloadSchema, Settings: config.SyncedSettings(settings), Proxies: cloudsync.NewProxies()}
	for _, x := range hosts.Proxies {
		if b, err := json.Marshal(x); err == nil {
			p.Proxies = append(p.Proxies, b)
		}
	}
	for _, f := range hosts.Folders {
		if b, err := json.Marshal(f); err == nil {
			p.Folders = append(p.Folders, b)
		}
	}
	for _, h := range hosts.Hosts {
		c := h.Clone()
		c.LastConnectedAt = time.Time{}
		if b, err := json.Marshal(c); err == nil {
			p.Hosts = append(p.Hosts, b)
		}
	}
	if quick != nil {
		data, err := json.Marshal(quick)
		if err == nil {
			p.Quick = &cloudsync.QuickCommands{}
			if json.Unmarshal(data, p.Quick) != nil {
				p.Quick = nil
			}
		}
	}
	return p
}

// syncSecretKeys are the keys of the secrets of the hosts that remember
// theirs.
func (a *App) syncSecretKeys() []string {
	var keys []string
	for _, h := range a.hosts.Hosts {
		if !h.Deleted() {
			for _, name := range hostSecretNames {
				if name == secretTOTP && h.IsRDP() || name != secretTOTP && !h.RemembersPassword() {
					continue
				}
				keys = append(keys, hostSecretKey(h.ID, name))
			}
		}
	}
	for _, p := range a.hosts.Proxies {
		if libTakesPassword(p) {
			keys = append(keys, libSecretKey(p.ID))
		}
	}
	return keys
}

// syncTick runs automatic sync, from the main window's view: at start,
// when the payload changed since the last sync, and every interval. It
// asks for the frame of its next check.
func (a *App) syncTick(c *ui.Context) {
	s, st := a.settings.Sync, &a.sync
	if s.Provider == config.SyncOff || !s.Auto {
		return
	}
	a.loadSyncSecrets()
	a.loadSyncState()
	if st.running || st.guard != nil || st.restorePaused || !st.loaded {
		return
	}
	now := c.Now()
	if wait := st.nextCheck.Sub(now); wait > 0 {
		c.After(wait)
		return
	}
	st.nextCheck = now.Add(syncCheckEvery)
	c.After(syncCheckEvery)
	if !a.syncReady() {
		return
	}
	interval := time.Duration(s.IntervalOrDefault()) * time.Minute
	due := st.lastTry.IsZero() || now.Sub(st.lastTry) >= interval
	if !due && st.err == nil && (st.again || st.synced == nil || !cloudsync.Equal(a.localPayload(), st.synced)) {
		due = true
	}
	if due {
		a.startSyncWith(syncOptions{reason: cloudsync.ReasonAuto}, now)
	}
}

// startSync syncs now, the frame's time; with force, it replaces the
// remote file with this device's payload.
func (a *App) startSync(force bool, now time.Time) {
	if a.sync.guard != nil && !force {
		a.openSyncGuard()
		return
	}
	a.startSyncWith(syncOptions{force: force, reason: cloudsync.ReasonManual}, now)
}

func (a *App) startSyncWith(options syncOptions, now time.Time) {
	st := &a.sync
	if st.running || !a.syncReady() {
		return
	}
	a.loadSyncState()
	if st.restorePaused && !options.force {
		return
	}
	s := a.settings.Sync
	remote, err := a.newRemote(s, st.credential)
	if err != nil {
		st.err = err
		return
	}
	st.running, st.err, st.lastTry = true, nil, now
	st.confirmReplace, st.again = false, false
	st.guard = nil
	snapshot := a.localPayload()
	req := cloudsync.Request{
		Remote: remote, Password: st.password, Local: snapshot, Force: options.force,
		Device: deviceName(), AppVersion: mygo.App.Version(), Reason: options.reason, Note: options.note,
		Retention: s.RemoteRetention(), Approval: options.approval, KeepServer: options.keepServer,
		ManualVersion: options.manualVersion, ExpectedServer: options.expectedServer,
	}
	if st.state.Remote == s.Remote() {
		req.Base, req.BaseSecrets = st.state.Base, st.state.Secrets
	}
	run := func(secrets map[string]string) {
		if secrets != nil {
			req.Local = snapshot.Clone()
			req.Local.Secrets, req.Secrets = secrets, true
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
			defer cancel()
			res, err := cloudsync.Sync(ctx, req)
			run := syncRun{start: s, snapshot: snapshot, secrets: secrets, options: options}
			if g, ok := remote.(*cloudsync.Gist); ok {
				run.gistID = g.ID
			}
			a.post(func() { a.syncDone(res, err, run) })
		}()
	}
	a.readSyncSecrets(s.Passwords, run)
	a.redraw()
}

func (a *App) readSyncSecrets(enabled bool, run func(map[string]string)) {
	if !enabled || a.secretsUnavailable() {
		run(nil)
		return
	}
	// The saved passwords, read in one job of the queue. If any read
	// fails, they are left out of this sync rather than taken for
	// deleted.
	keys := a.syncSecretKeys()
	a.secrets.queue.do(func() {
		secrets := map[string]string{}
		var failed error
		for _, k := range keys {
			v, ok, err := a.plat.SecretGet(k)
			if err != nil {
				failed = err
				break
			}
			if ok {
				secrets[k] = v
			}
		}
		a.post(func() {
			a.noteStore(failed)
			if failed != nil {
				secrets = nil
			}
			run(secrets)
		})
	})
}

// syncDone applies what a sync brought.
func (a *App) syncDone(res *cloudsync.Result, err error, run syncRun) {
	st := &a.sync
	st.running = false
	defer a.redraw()
	s := &a.settings.Sync
	if s.Remote() != run.start.Remote() {
		// The destination changed meanwhile: what the sync brought, and
		// its file as a base, belong to the old one.
		st.err, st.done = nil, nil
		return
	}
	if err != nil {
		st.err = err
		if errors.As(err, &st.guard) {
			run.options.priorGuard = nil
			st.guardRun = run.options
			a.recordSync(run.options.reason, "paused", nil, true)
			a.notify(locales.BackupGuardTitle())
			a.openSyncGuard()
		} else {
			if run.options.priorGuard != nil {
				st.guard = run.options.priorGuard
			}
			a.recordSync(run.options.reason, "failed", nil, false)
		}
		if run.options.manualVersion && st.backups != nil {
			st.backups.err = err
		}
		return
	}
	failApply := func(err error) {
		st.err = err
		if run.options.priorGuard != nil {
			st.guard = run.options.priorGuard
		}
		a.recordSync(run.options.reason, "failed", res, run.options.priorGuard != nil)
	}
	if s.Provider == config.SyncGist && run.gistID != "" && s.GistID != run.gistID {
		s.GistID = run.gistID
		if err := a.saveSettings(a.settings); err != nil {
			failApply(err)
			return
		}
	}
	snapshot := run.snapshot
	merged := res.Merged
	if res.Changed {
		current := a.localPayload()
		if !cloudsync.Equal(current, snapshot) {
			// Changed here while the sync was away: merge again, and sync
			// those changes soon.
			m, _ := cloudsync.Merge(snapshot, current, merged)
			m.Secrets = merged.Secrets
			merged = m
			if impact := cloudsync.Deletions(current, merged); impact.Risky() {
				st.guard = &cloudsync.DeletionGuard{Local: impact}
				st.guardRun = run.options
				st.err = st.guard
				a.recordSync(run.options.reason, "paused", res, true)
				a.notify(locales.BackupGuardTitle())
				a.openSyncGuard()
				return
			}
			st.nextCheck = time.Time{}
		}
	}
	if res.Changed || run.secrets != nil && !sameSyncSecrets(run.secrets, res.Merged.Secrets) {
		reason := cloudsync.ReasonPreSync
		if run.options.keepServer {
			reason = cloudsync.ReasonPreRestore
		}
		if _, err := a.saveLocalBackup(reason, "", time.Now()); err != nil {
			failApply(err)
			return
		}
	}
	if res.Changed {
		kept := make(map[string]bool, len(merged.Hosts))
		for _, raw := range merged.Hosts {
			var host struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &host) == nil {
				kept[host.ID] = true
			}
		}
		var deleted []string
		for _, host := range a.hosts.Hosts {
			if !kept[host.ID] && !run.options.keepServer {
				deleted = append(deleted, host.ID)
			}
		}
		if err := config.RememberPermanentDeletes(deleted); err != nil {
			failApply(err)
			return
		}
		if err := a.applyPayload(merged); err != nil {
			failApply(err)
			return
		}
	}
	if run.secrets != nil {
		a.applySyncedSecrets(run.secrets, res.Merged.Secrets)
	}
	st.err, st.done = nil, res
	nextState := config.SyncState{
		Remote: s.Remote(), Base: res.Envelope, At: time.Now(), Device: res.RemoteDevice,
		Secrets: run.secrets != nil,
	}
	if res.Uploaded {
		nextState.Device = deviceName()
	}
	if err := config.SaveSyncState(nextState); err != nil {
		failApply(err)
		return
	}
	st.state, st.restorePaused = nextState, false
	// What the remote now has: automatic sync syncs again when this
	// device's payload differs, as when it changed during this sync.
	st.synced = res.Merged.Clone()
	st.synced.Secrets = nil
	result := "ok"
	if res.HistoryError != nil {
		result = "history-warning"
	}
	a.recordSync(run.options.reason, result, res, run.options.priorGuard != nil)
	if st.backups != nil && (res.Uploaded || res.Changed) {
		a.refreshVersions(st.backups)
	}
}

// applyPayload makes this device's folders, hosts and synced settings
// those of p. Hosts keep the time they last connected here, and their
// Host values, which sessions point at. hosts.json is copied first.
func (a *App) applyPayload(p *cloudsync.Payload) error {
	local := a.localPayload()
	// A payload with no library (a backup older than it) leaves this
	// device's as it is.
	proxies := a.hosts.Proxies
	if p.Proxies != nil {
		proxies = nil
		kept := map[string]bool{}
		for _, raw := range p.Proxies {
			x := &config.Proxy{}
			if json.Unmarshal(raw, x) != nil || x.ID == "" || kept[x.ID] {
				continue
			}
			kept[x.ID] = true
			proxies = append(proxies, x)
		}
	}
	if !sameList(local.Folders, p.Folders) || !sameList(local.Hosts, p.Hosts) || p.Proxies != nil && !sameList(local.Proxies, p.Proxies) {
		config.BackupHostsBeforeSync(a.hosts)
		var folders []*config.Folder
		for _, raw := range p.Folders {
			f := &config.Folder{}
			if json.Unmarshal(raw, f) == nil && f.ID != "" {
				folders = append(folders, f)
			}
		}
		var hosts []*config.Host
		kept := map[string]bool{}
		for _, raw := range p.Hosts {
			h := &config.Host{}
			if json.Unmarshal(raw, h) != nil || h.ID == "" || kept[h.ID] {
				continue
			}
			kept[h.ID] = true
			if old := a.hosts.Host(h.ID); old != nil {
				h.LastConnectedAt = old.LastConnectedAt
			}
			hosts = append(hosts, h)
		}
		next := *a.hosts
		next.Folders, next.Hosts, next.Proxies = folders, hosts, proxies
		if err := a.saveHosts(&next); err != nil {
			return err
		}
		for index, host := range hosts {
			if old := a.hosts.Host(host.ID); old != nil {
				*old = *host
				hosts[index] = old
			}
		}
		for _, h := range a.hosts.Hosts {
			if !kept[h.ID] {
				a.deleteHostSecrets(h.ID) // deleted for good on another device
			}
		}
		a.hosts.Folders, a.hosts.Hosts, a.hosts.Proxies = folders, hosts, proxies
	}
	if !cloudsync.Equal(&cloudsync.Payload{Settings: local.Settings}, &cloudsync.Payload{Settings: p.Settings}) {
		before := a.settings
		next := config.WithSyncedSettings(a.settings, p.Settings)
		if err := a.saveSettings(next); err != nil {
			return err
		}
		a.settings = next
		if a.settings.Language != before.Language {
			a.plat.SetLocale(a.settings.Language)
		}
		if a.settings.Appearance.Normal() != before.Appearance.Normal() {
			a.plat.SetAppearance(a.settings.Appearance.Normal())
		}
		a.applyTerminalSettings()
		a.applyAgentIcons()
	}
	if p.Quick != nil && !sameQuickPayload(local.Quick, p.Quick) {
		data, err := json.Marshal(p.Quick)
		if err != nil {
			return err
		}
		quick, err := config.ParseQuickCommands(data)
		if err != nil {
			return err
		}
		if a.quickUI.noSave {
			return a.quickUI.loadErr
		}
		if err := config.SaveQuickCommands(quick); err != nil {
			return err
		}
		a.quickUI.store = quick
	}
	return nil
}

// sameList says whether two lists hold the same objects in any order.
func sameList(x, y []json.RawMessage) bool {
	return cloudsync.Equal(&cloudsync.Payload{Hosts: x}, &cloudsync.Payload{Hosts: y})
}

// applySyncedSecrets keeps in the credential store, and for this run,
// the passwords the merge changed: before is what this device had as the
// sync started. Each is changed only if the store still has what it had
// then, in one job of the queue, so that a password saved during the
// sync is kept, and synced next; one saved after the job comes after it
// in the queue, and wins too.
func (a *App) applySyncedSecrets(before, after map[string]string) {
	allowed := make(map[string]bool)
	for _, key := range a.syncSecretKeys() {
		allowed[key] = true
	}
	type change struct {
		key, from, to string
		hadFrom, del  bool
	}
	var changes []change
	for k, v := range after {
		if !allowed[k] {
			continue
		}
		if from, had := before[k]; !had || from != v {
			changes = append(changes, change{key: k, from: from, hadFrom: had, to: v})
		}
	}
	for k, from := range before {
		if _, ok := after[k]; !ok {
			changes = append(changes, change{key: k, from: from, hadFrom: true, del: true})
		}
	}
	if len(changes) == 0 || a.secretsUnavailable() {
		return
	}
	for _, ch := range changes {
		a.secrets.markRead(ch.key)
	}
	a.secrets.queue.do(func() {
		var applied []change
		kept, storeErr := false, error(nil)
		for _, ch := range changes {
			cur, ok, err := a.plat.SecretGet(ch.key)
			if err != nil {
				storeErr = err
				break
			}
			if ok != ch.hadFrom || cur != ch.from {
				kept = true // changed here during the sync
				continue
			}
			if ch.del {
				err = a.plat.SecretDelete(ch.key)
			} else {
				err = a.plat.SecretSet(ch.key, ch.to)
			}
			if err != nil {
				storeErr = err
				break
			}
			applied = append(applied, ch)
		}
		a.post(func() {
			a.noteStore(storeErr)
			for _, ch := range applied {
				if id, ok := totpIDFromKey(ch.key); ok {
					delete(a.totpStatus, id)
				}
				a.rememberSynced(ch.key, ch.from, ch.hadFrom, ch.to, !ch.del)
			}
			if kept || storeErr != nil {
				a.sync.again = true
			}
		})
	})
}

// rememberSynced keeps, or forgets, for this run the secret the store
// keeps under key, as the app keeps a host's login and its proxy's
// password in memory: only while what the app knows is still what the
// store had before (from, or nothing), as one typed since is newer.
func (a *App) rememberSynced(key, from string, hadFrom bool, v string, keep bool) {
	if a.rememberSyncedProxy(key, v, keep) {
		return
	}
	rest, ok := strings.CutPrefix(key, "host/")
	if !ok {
		return
	}
	id, name, ok := strings.Cut(rest, "/")
	h := a.hosts.Host(id)
	if !ok || h == nil {
		return
	}
	mem := ""
	switch {
	case name == secretProxyPassword:
		mem = proxySecretKey(id)
	case name == secretGatewayPassword:
		mem = gatewaySecretKey(id)
	case loginSecretKey(h) == key:
		mem = id
	default:
		return
	}
	if cur, known := a.passwords[mem]; known && (!hadFrom || cur != from) {
		return
	}
	if keep {
		a.passwords[mem] = v
	} else {
		delete(a.passwords, mem)
	}
}

// deviceName names this device in the file it writes.
func deviceName() string {
	if n, err := os.Hostname(); err == nil {
		return n
	}
	return ""
}

// syncErrorText says why a sync failed, in the language shown.
func syncErrorText(err error) string {
	switch {
	case errors.Is(err, cloudsync.ErrWrongPassword):
		return locales.SyncErrWrongPassword()
	case errors.Is(err, cloudsync.ErrAuth):
		return locales.SyncErrAuth()
	case errors.Is(err, cloudsync.ErrNotEnvelope):
		return locales.SyncErrNotEnvelope()
	case errors.Is(err, cloudsync.ErrNewerFormat):
		return locales.SyncErrNewerFormat()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.SyncErrTimeout()
	}
	return locales.SyncErrOther(err.Error())
}
