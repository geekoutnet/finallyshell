package app

// The account: the user signs in with a third-party account
// (internal/account; GitHub for now) through the device flow, and the
// account then syncs this device's connections and settings to a gist of
// its own, apart from the Sync page's: its own settings
// (settings.Account), sync password, state file
// (account-sync-state.json), log (account-log.json) and window. The
// sync itself is the Sync page's driver (sync.go), run on the account's
// target, so it merges the same way: three ways against the last synced
// file, again over what changed here meanwhile, with the deletion guard
// and a local backup before it changes anything.
//
// The account's token expires after hours when the provider says so,
// and is renewed with its refresh token, which works once: before a
// sync, when it is about to expire, and after the remote refuses it.
// Another copy of the app may have renewed it already, so the stored one
// is read first.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/account"
	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"
)

// The file of the account's gist, and its description.
const (
	accountSyncFile        = "finallyshell-account.json"
	accountSyncDescription = "Finallyshell account sync (encrypted, do not edit)"
)

// How long a renewal or a profile read may take.
const accountTimeout = 30 * time.Second

// accountState is the app's side of the account. Its fields belong to
// the UI thread.
type accountState struct {
	// sync is the account's sync: password is its sync password,
	// credential the access token (credFor config.SyncAccount).
	sync syncState
	// token is the whole token, of which sync.credential is Access.
	token account.Token
	// renewing: a renewal runs; renewErr is why the last failed.
	renewing bool
	renewErr error
	// renewed: the next sync's log lists the renewal as its first step.
	renewed bool
	// syncAfterRenew is the reason of a sync to start once the renewal
	// ends, "" for none.
	syncAfterRenew string
	// replacing: the running sync replaces the gist's file.
	replacing bool
	// profileAt is when the profile was last read, profileBusy while it
	// is (refreshAccountProfile).
	profileAt   time.Time
	profileBusy bool
	// authRetried: the remote refused the token, which was renewed or
	// checked, and the sync tried once more; set until a sync works, so
	// that a token the remote keeps refusing is not renewed in a loop.
	authRetried bool

	login *loginState
	view  accountView
}

// accountTokenKey and accountPasswordKey are the keys of the account's
// token, of the provider, and of its sync password, of the user.
func accountTokenKey(provider string) string { return "account/" + provider + "/token" }
func accountPasswordKey(provider, user string) string {
	return "account/" + provider + "/" + user + "/sync-password"
}

// accountSync is the account's sync.
func (a *App) accountSync() syncTarget {
	return syncTarget{
		st:        &a.account.sync,
		settings:  func() config.SyncSettings { return a.settings.Account.SyncSettings() },
		stateFile: config.AccountSyncStateName,
		account:   true,
	}
}

// accountProvider is the provider of the signed-in account, nil for
// none. Tests replace App.newProvider.
func (a *App) accountProvider() account.Provider {
	return a.newProvider(a.settings.Account.Provider)
}

// accountSecretKeys are the keys of the signed-in account's secrets.
func (a *App) accountSecretKeys() []string {
	s := a.settings.Account
	if s.Provider == "" {
		return nil
	}
	return []string{accountTokenKey(s.Provider), accountPasswordKey(s.Provider, s.UserID)}
}

// forgetAccountSecrets drops the account's secrets from memory, as the
// app lock's reset deletes them from the store: the account then needs
// signing in again.
func (a *App) forgetAccountSecrets() {
	ac := &a.account
	ac.sync.password, ac.sync.credential, ac.sync.loaded = "", "", false
	ac.token = account.Token{}
}

// loadAccount reads the account's token and sync password from the
// credential store, once. A signed-in account without a token (deleted
// from the store) needs signing in again.
func (a *App) loadAccount() {
	s, st := a.settings.Account, &a.account.sync
	if !s.SignedIn() || st.loading || st.loaded {
		return
	}
	st.loading = true
	provider, user := s.Provider, s.UserID
	pending := 2
	done := func() {
		if pending--; pending > 0 {
			return
		}
		st.loading = false
		cur := a.settings.Account
		if cur.Provider != provider || cur.UserID != user {
			return // signed out or in again meanwhile, which loads its own
		}
		st.loaded = true
		// No password kept: the default, the account's email address.
		if st.password == "" && cur.Email != "" && !cur.CustomPassword {
			a.setAccountPassword(cur.Email)
		}
		// No token here (the store lost it, or refused to give it): the
		// account waits for a new sign-in, in this run only, as the store
		// may give it again next time. Nothing is deleted.
		if a.account.token.Access == "" {
			a.settings.Account.Expired = true
		}
		a.redraw()
	}
	a.secret(accountTokenKey(provider), func(v string, ok bool) {
		var tok account.Token
		if ok && json.Unmarshal([]byte(v), &tok) == nil && tok.Access != "" && a.account.token.Access == "" {
			a.useAccountToken(tok)
			// A token is there again: one the provider revoked is deleted
			// as it is, so this one is tried, and checked if refused.
			if cur := a.settings.Account; cur.Provider == provider && cur.UserID == user {
				a.settings.Account.Expired = false
			}
		}
		done()
	})
	a.secret(accountPasswordKey(provider, user), func(v string, ok bool) {
		if ok && st.password == "" {
			st.password = v
		}
		done()
	})
}

// useAccountToken makes tok the account's token for this run.
func (a *App) useAccountToken(tok account.Token) {
	ac := &a.account
	ac.token = tok
	ac.sync.credential, ac.sync.credFor = tok.Access, config.SyncAccount
}

// saveAccountToken keeps tok in the credential store.
func (a *App) saveAccountToken(provider string, tok account.Token) {
	if data, err := json.Marshal(tok); err == nil {
		a.setSecret(accountTokenKey(provider), string(data))
	}
}

// How often the account's profile is read again, and the log's reason
// of sealing the gist with a new primary address.
const (
	accountProfileEvery = 6 * time.Hour
	accountReasonEmail  = "email-changed"
)

// refreshAccountProfile reads who signed in again, at start and every
// accountProfileEvery: the name and addresses change on GitHub. When the
// primary address changed while the account syncs with its email
// address, the gist is sealed with the new one, so that a computer
// signing in now opens it with the default; the account's other
// computers follow by themselves, their old address no longer opening
// it and the new one among those they try.
func (a *App) refreshAccountProfile(now time.Time) {
	ac := &a.account
	s := a.settings.Account
	p := a.accountProvider()
	if p == nil || !s.SignedIn() || s.Expired || ac.profileBusy || ac.token.Access == "" ||
		!ac.profileAt.IsZero() && now.Sub(ac.profileAt) < accountProfileEvery {
		return
	}
	ac.profileBusy, ac.profileAt = true, now
	tok := ac.token
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), accountTimeout)
		defer cancel()
		prof, err := p.Profile(ctx, tok)
		a.post(func() {
			ac.profileBusy = false
			cur := a.settings.Account
			if err != nil || cur.Provider != s.Provider || cur.UserID != prof.ID {
				return
			}
			next := a.settings
			acc := &next.Account
			oldEmail := acc.Email
			acc.Login, acc.Name, acc.Email, acc.Emails, acc.URL = prof.Login, prof.Name, prof.Email, strings.Join(prof.Emails, "\n"), prof.URL
			if next.Account != cur {
				if a.saveSettings(next) != nil {
					return
				}
				a.settings = next
				a.redraw()
			}
			pw := ac.sync.password
			if prof.Email != "" && prof.Email != oldEmail && !cur.CustomPassword && pw != "" && pw != prof.Email {
				a.changeAccountPasswordFor(prof.Email, accountReasonEmail)
			}
		})
	}()
}

// isAccountEmail says whether pw is one of the account's addresses.
func isAccountEmail(s config.AccountSettings, pw string) bool {
	return pw != "" && (pw == s.Email || slices.Contains(s.EmailList(), pw))
}

// accountPasswordRequest says in the request which kind of password the
// account syncs with, written in the gist's header for the other
// devices, and gives the account's addresses to try when this device's
// password no longer opens the gist, as when another device went back to
// the email address.
func (a *App) accountPasswordRequest(req *cloudsync.Request) {
	s := a.settings.Account
	req.KeyHint = cloudsync.KeyEmail
	if s.CustomPassword {
		req.KeyHint = cloudsync.KeyCustom
	}
	for _, e := range append([]string{s.Email}, s.EmailList()...) {
		if e != "" && e != req.Password && !slices.Contains(req.AltPasswords, e) {
			req.AltPasswords = append(req.AltPasswords, e)
		}
	}
}

// adoptAccountPassword keeps pw, which opened the gist where this
// device's password no longer did: another device changed it to the
// account's email address.
func (a *App) adoptAccountPassword(pw string) {
	if pw == a.account.sync.password {
		return
	}
	a.setAccountPassword(pw)
	a.account.sync.basePassword = pw
	a.logAccount(config.AccountEvent{Action: config.AccountPassword, Reason: "auto", Result: "ok", Steps: []string{config.StepAutoPassword}})
}

// setAccountPassword keeps the account's sync password on this device,
// without changing the gist's: the default (the account's email
// address), or one typed, as the gist's current password. A sync that
// failed for the password tries again at once.
func (a *App) setAccountPassword(pw string) {
	s, st := a.settings.Account, &a.account.sync
	if st.basePassword == "" {
		st.basePassword = st.password
	}
	st.password = pw
	a.keepSyncSecret(accountPasswordKey(s.Provider, s.UserID), pw)
	if custom := pw != "" && !isAccountEmail(s, pw); custom != s.CustomPassword {
		next := a.settings
		next.Account.CustomPassword = custom
		if a.saveSettings(next) == nil {
			a.settings = next
		}
	}
	if errors.Is(st.err, cloudsync.ErrWrongPassword) {
		st.err, st.nextCheck = nil, time.Time{}
	}
	a.redraw()
}

// changeAccountPassword changes the account's sync password to pw: the
// gist's file is sealed again with it (cloudsync.Reencrypt), as is this
// device's base, whose content stays what was last synced here, so that
// the next merge counts no change made elsewhere as one undone here.
// Nothing synced yet, the password just changes.
func (a *App) changeAccountPassword(pw string) { a.changeAccountPasswordFor(pw, "") }

// changeAccountPasswordFor is changeAccountPassword, logged with reason:
// "" for the user's, accountReasonEmail for a new primary address.
func (a *App) changeAccountPasswordFor(pw, reason string) {
	ac, v := &a.account, &a.account.view
	s := a.settings.Account
	if pw == "" || pw == ac.sync.password || a.syncBusy() || ac.renewing || !s.SignedIn() || s.Expired {
		return
	}
	t := a.accountSync()
	a.loadSyncStateOn(t)
	start := t.settings()
	old := ac.sync.password
	v.pwErr, v.pwMessage = nil, nil
	done := func(steps []string) {
		v.pwOpen, v.pwNew, v.pwConfirm = false, "", ""
		if reason == "" {
			v.pwMessage = locales.AccountPasswordChanged
		}
		a.logAccount(config.AccountEvent{Action: config.AccountPassword, Reason: reason, Result: "ok", Steps: steps})
	}
	if start.GistID == "" && ac.sync.state.Base == nil {
		a.setAccountPassword(pw)
		done(nil)
		return
	}
	remote, err := a.newRemote(start, ac.sync.credential)
	if err != nil {
		v.pwErr = err
		return
	}
	ac.sync.running, v.pwBusy = true, true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		hint := cloudsync.KeyCustom
		if isAccountEmail(s, pw) {
			hint = cloudsync.KeyEmail
		}
		env, err := cloudsync.Reencrypt(ctx, remote, old, pw, hint, deviceName())
		gistID := ""
		if g, ok := remote.(*cloudsync.Gist); ok {
			gistID = g.ID
		}
		a.post(func() {
			ac.sync.running, v.pwBusy = false, false
			defer a.redraw()
			if t.settings().Remote() != start.Remote() || ac.sync.password != old {
				return // signed out, in as another, or the password changed meanwhile
			}
			if errors.Is(err, cloudsync.ErrNotFound) {
				err, env = nil, nil // gone: the next sync makes it with pw
			}
			if err != nil {
				v.pwErr = err
				a.logAccount(config.AccountEvent{Action: config.AccountPassword, Result: "failed", Error: syncErrorText(err)})
				if errors.Is(err, cloudsync.ErrAuth) {
					a.checkAccountAfterAuthError()
				}
				return
			}
			if gistID != "" && start.GistID != gistID {
				if err := a.keepSyncGistID(t, gistID); err != nil {
					v.pwErr = err
				}
			}
			// The base, sealed again with the same content.
			state := ac.sync.state
			if state.Base != nil {
				if plain, err := cloudsync.Open(old, state.Base); err == nil {
					state.Base, _ = cloudsync.Seal(pw, plain, nil)
				} else {
					state.Base = nil // a first sync then keeps what either side has
				}
			}
			state.Remote = t.settings().Remote()
			if err := config.SaveSyncStateFile(t.stateFile, state); err != nil {
				v.pwErr = err
			}
			ac.sync.state = state
			a.setAccountPassword(pw)
			ac.sync.basePassword = pw
			ac.sync.err = nil
			steps := []string{config.StepRead, config.StepReseal}
			if env != nil {
				steps = append(steps, config.StepUpload)
			}
			done(steps)
		})
	}()
}

// accountReady says whether the account can sync now.
func (a *App) accountReady() bool {
	return a.settings.Account.SignedIn() && !a.settings.Account.Expired && a.syncReadyOn(a.accountSync())
}

// renewAccountFirst renews the account's token when it is about to
// expire, then starts the sync of reason: true while it does, so the
// caller does not sync with the old token.
func (a *App) renewAccountFirst(now time.Time, reason string) bool {
	ac := &a.account
	if ac.renewing {
		return true
	}
	if !ac.token.NeedsRefresh(now) {
		return false
	}
	ac.syncAfterRenew = reason
	a.renewAccount()
	return true
}

// syncAccountNow syncs the account at once, renewing its token first
// when it is about to expire; force replaces the gist's file.
func (a *App) syncAccountNow(force bool, now time.Time) {
	if !a.accountReady() || a.syncBusy() {
		return
	}
	t := a.accountSync()
	if force {
		a.startSyncOn(t, syncOptions{force: true, reason: cloudsync.ReasonManual}, now)
		a.account.replacing = t.st.running
		return
	}
	if t.st.guard != nil {
		a.openSyncGuardOn(t)
		return
	}
	if t.st.choice != nil {
		a.openSyncChoice(t)
		return
	}
	if a.renewAccountFirst(now, cloudsync.ReasonManual) {
		return
	}
	a.startSyncOn(t, syncOptions{reason: cloudsync.ReasonManual}, now)
}

// renewAccount renews the account's token. The stored token is read
// first: another copy of the app may have renewed it, which made this
// one's refresh token stop working.
func (a *App) renewAccount() {
	ac := &a.account
	s := a.settings.Account
	p := a.accountProvider()
	if ac.renewing || p == nil || !s.SignedIn() {
		return
	}
	ac.renewing, ac.renewErr = true, nil
	old := ac.token
	same := func() bool {
		cur := a.settings.Account
		return cur.Provider == s.Provider && cur.UserID == s.UserID
	}
	finish := func(reason string) {
		ac.renewing = false
		ac.syncAfterRenew = ""
		if reason != "" && a.accountReady() {
			a.startSyncOn(a.accountSync(), syncOptions{reason: reason}, time.Now())
		}
		a.redraw()
	}
	a.secret(accountTokenKey(s.Provider), func(v string, ok bool) {
		if !same() {
			finish("")
			return
		}
		var stored account.Token
		if ok && json.Unmarshal([]byte(v), &stored) == nil && stored.Access != "" && stored.Access != old.Access {
			a.useAccountToken(stored) // renewed by another copy
			finish(ac.syncAfterRenew)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), accountTimeout)
			defer cancel()
			tok, err := p.Refresh(ctx, old)
			a.post(func() {
				if !same() {
					finish("")
					return
				}
				reason := ac.syncAfterRenew
				switch {
				case err == nil:
					a.useAccountToken(tok)
					a.saveAccountToken(s.Provider, tok)
					ac.renewed = true
					ac.sync.err = nil
					a.logAccount(config.AccountEvent{Action: config.AccountRefresh, Result: "ok"})
					finish(reason)
				case errors.Is(err, account.ErrRevoked):
					// Once more from the store: another copy may have
					// renewed it while this one asked.
					a.secret(accountTokenKey(s.Provider), func(v string, ok bool) {
						var again account.Token
						if same() && ok && json.Unmarshal([]byte(v), &again) == nil && again.Access != "" && again.Access != old.Access {
							a.useAccountToken(again)
							finish(reason)
							return
						}
						if same() {
							a.markAccountExpired()
						}
						finish("")
					})
				default:
					ac.renewErr = err
					a.logAccount(config.AccountEvent{Action: config.AccountRefresh, Result: "failed", Error: err.Error()})
					finish("")
				}
			})
		}()
	})
}

// checkAccountAfterAuthError is what a sync the remote refused does: the
// token is renewed, or, one that does not expire, checked, and the
// sign-in marked expired when it no longer works.
func (a *App) checkAccountAfterAuthError() {
	ac := &a.account
	if ac.authRetried {
		return // the error stays shown; the next interval tries again
	}
	ac.authRetried = true
	if ac.token.Refresh != "" {
		ac.syncAfterRenew = cloudsync.ReasonAuto
		a.renewAccount()
		return
	}
	p, tok := a.accountProvider(), ac.token
	if p == nil || ac.renewing {
		return
	}
	ac.renewing = true
	s := a.settings.Account
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), accountTimeout)
		defer cancel()
		_, err := p.Profile(ctx, tok)
		a.post(func() {
			ac.renewing = false
			cur := a.settings.Account
			if errors.Is(err, account.ErrRevoked) && cur.Provider == s.Provider && cur.UserID == s.UserID && ac.token.Access == tok.Access {
				a.markAccountExpired()
			}
			a.redraw()
		})
	}()
}

// markAccountExpired keeps that the sign-in no longer works: the account
// stops syncing until the user signs in again.
func (a *App) markAccountExpired() {
	ac := &a.account
	ac.sync.credential = ""
	ac.token = account.Token{}
	if a.settings.Account.Expired {
		return
	}
	a.logAccount(config.AccountEvent{Action: config.AccountExpired, Result: "failed"})
	next := a.settings
	next.Account.Expired = true
	if a.saveSettings(next) == nil {
		a.settings = next
	}
	a.deleteSecret(accountTokenKey(next.Account.Provider))
}

// signIn keeps the account the device flow signed in. The same account
// again keeps its gist and options; another one starts its own, and its
// first sync, with no base, keeps what either side has.
func (a *App) signIn(p account.Provider, tok account.Token, prof account.Profile) error {
	prev := a.settings.Account
	same := prev.Provider == p.ID() && prev.UserID == prof.ID
	next := a.settings
	acc := config.AccountSettings{
		Provider: p.ID(), UserID: prof.ID, Login: prof.Login, Name: prof.Name, Email: prof.Email, Emails: strings.Join(prof.Emails, "\n"), URL: prof.URL,
		Auto: true,
	}
	if same {
		acc.GistID, acc.Passwords, acc.Auto, acc.Interval = prev.GistID, prev.Passwords, prev.Auto, prev.Interval
		acc.CustomSync, acc.CustomPassword = prev.CustomSync, prev.CustomPassword
	}
	next.Account = acc
	if err := a.saveSettings(next); err != nil {
		return err
	}
	if !same {
		config.DeleteSyncStateFile(config.AccountSyncStateName)
	}
	a.settings = next
	ac := &a.account
	ac.sync = syncState{}
	ac.renewing, ac.renewErr, ac.renewed, ac.syncAfterRenew, ac.authRetried = false, nil, false, "", false
	a.useAccountToken(tok)
	a.saveAccountToken(p.ID(), tok)
	a.closeWindow(WindowAccountGuard)
	a.closeWindow(choiceWindow(a.accountSync()))
	a.logAccount(config.AccountEvent{Action: config.AccountSignIn, Result: "ok"})
	// The token is known; read the user's sync password, if this device
	// kept it, after the token's write in the queue.
	ac.sync.loading = true
	a.secret(accountPasswordKey(acc.Provider, acc.UserID), func(v string, ok bool) {
		ac.sync.loading = false
		cur := a.settings.Account
		if cur.Provider != acc.Provider || cur.UserID != acc.UserID {
			return
		}
		if ok && ac.sync.password == "" {
			ac.sync.password = v
		}
		// None kept here: the default, the account's email address, so
		// that signing in is all a sync needs.
		if ac.sync.password == "" && cur.Email != "" {
			a.setAccountPassword(cur.Email)
		}
		ac.sync.loaded = true
		a.redraw()
	})
	a.redraw()
	return nil
}

// signOut forgets the account on this device: its token and sync
// password, its settings and state. This device's connections stay, and
// so does the gist, for signing in again here or elsewhere.
func (a *App) signOut() error {
	if a.account.sync.running {
		return nil
	}
	s := a.settings.Account
	if !s.SignedIn() {
		return nil
	}
	a.logAccount(config.AccountEvent{Action: config.AccountSignOut, Result: "ok"})
	next := a.settings
	next.Account = config.AccountSettings{}
	if err := a.saveSettings(next); err != nil {
		return err
	}
	a.settings = next
	a.deleteSecret(accountTokenKey(s.Provider))
	a.deleteSecret(accountPasswordKey(s.Provider, s.UserID))
	config.DeleteSyncStateFile(config.AccountSyncStateName)
	a.account.sync = syncState{}
	a.account.token = account.Token{}
	a.account.renewing, a.account.renewErr, a.account.renewed, a.account.syncAfterRenew, a.account.authRetried = false, nil, false, "", false
	a.closeWindow(WindowAccountGuard)
	a.closeWindow(choiceWindow(a.accountSync()))
	a.redraw()
	return nil
}

// logAccount adds an event to the account's log, of the signed-in
// account and this device.
func (a *App) logAccount(e config.AccountEvent) { a.writeAccountEvent(e, nil) }

// writeAccountEvent is logAccount, counting the event in the last one
// when same says it is the same.
func (a *App) writeAccountEvent(e config.AccountEvent, same func(config.AccountEvent) bool) {
	s := a.settings.Account
	e.At, e.Provider, e.Login, e.Device = time.Now().UTC(), s.Provider, s.Login, deviceName()
	if err := config.AppendAccountEventCoalesced(e, same); err != nil {
		a.account.view.logErr = err
	}
	a.account.view.events = nil // read again when shown
}

// quietSync says whether e is an automatic sync that found nothing to do:
// the log counts those in one event rather than one every interval.
func quietSync(e config.AccountEvent) bool {
	n := len(e.Steps)
	return e.Action == config.AccountSync && e.Reason == cloudsync.ReasonAuto && e.Result == "ok" &&
		n > 0 && e.Steps[n-1] == config.StepUpToDate && !slices.Contains(e.Steps, config.StepRefresh) &&
		e.Conflicts == 0 && e.Error == ""
}

// recordAccountSync logs what a sync of the account did, step by step,
// and has the account checked when GitHub refused its token.
func (a *App) recordAccountSync(reason, result string, res *cloudsync.Result) {
	ac := &a.account
	e := config.AccountEvent{Action: config.AccountSync, Reason: reason, Result: result}
	if ac.renewed {
		e.Steps = append(e.Steps, config.StepRefresh)
		ac.renewed = false
	}
	e.Steps = append(e.Steps, config.StepRead)
	if err := ac.sync.err; err != nil && result != "ok" && result != "history-warning" {
		e.Error = syncErrorText(err)
	}
	if res != nil {
		switch {
		case res.Created:
			e.Steps = append(e.Steps, config.StepCreate)
		default:
			e.Steps = append(e.Steps, config.StepMerge)
		}
		if res.Uploaded && !res.Created {
			e.Steps = append(e.Steps, config.StepUpload)
		}
		if res.Changed && (result == "ok" || result == "history-warning") {
			e.Steps = append(e.Steps, config.StepBackup, config.StepApply)
		}
		if !res.Uploaded && !res.Changed {
			e.Steps = append(e.Steps, config.StepUpToDate)
		}
		e.Conflicts = res.Conflicts
		e.Local, e.Server = changeCounts(res.LocalImpact), changeCounts(res.ServerImpact)
		if res.HistoryError != nil {
			e.Error = versionError(res.HistoryError)
		}
	}
	if ac.replacing {
		e.Action, ac.replacing = config.AccountReplace, false
	}
	if ac.sync.guard != nil && result == "paused" {
		e.Error = locales.BackupGuardTitle()
	}
	if c := ac.sync.choice; c != nil && result == "paused" {
		e.Error = locales.AccountChoiceLogged(c.Local, c.Remote)
	}
	if quietSync(e) {
		device, login := deviceName(), a.settings.Account.Login
		a.writeAccountEvent(e, func(last config.AccountEvent) bool {
			return quietSync(last) && last.Device == device && last.Login == login
		})
	} else {
		a.logAccount(e)
	}
	switch {
	case result == "failed" && errors.Is(ac.sync.err, cloudsync.ErrAuth):
		a.checkAccountAfterAuthError()
	case result == "ok" || result == "history-warning":
		ac.authRetried = false
	}
}
