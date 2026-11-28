package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"finallyshell/internal/account"
	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// fakeProvider is a sign-in service in memory: each sign-in or renewal
// issues a new pair of tokens, and only the newest works, as GitHub's
// renewals do.
type fakeProvider struct {
	mu         sync.Mutex
	profile    account.Profile
	pending    bool          // Poll waits for the user forever
	expiresIn  time.Duration // how long a token works, 0 for ever
	refreshErr error
	profileErr error
	access     string
	refresh    string
	gen        int
	refreshes  int
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{profile: account.Profile{ID: "42", Login: "octo", Name: "Octo Cat", Email: "octocat@example.com", URL: "https://github.com/octo"}}
}

func (f *fakeProvider) ID() string { return "github" }
func (f *fakeProvider) ManageURL() string {
	return "https://github.com/settings/connections/applications/x"
}

func (f *fakeProvider) RequestCode(context.Context) (account.DeviceCode, error) {
	return account.DeviceCode{Code: "dev", UserCode: "WDJB-MJHT", VerificationURI: "https://github.com/login/device",
		ExpiresAt: time.Now().Add(15 * time.Minute), Interval: time.Millisecond}, nil
}

func (f *fakeProvider) Poll(context.Context, account.DeviceCode) (account.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending {
		return account.Token{}, account.ErrPending
	}
	return f.issue(), nil
}

func (f *fakeProvider) issue() account.Token {
	f.gen++
	f.access, f.refresh = fmt.Sprintf("gho_%d", f.gen), fmt.Sprintf("ghr_%d", f.gen)
	t := account.Token{Access: f.access}
	if f.expiresIn > 0 {
		t.Refresh, t.Expires = f.refresh, time.Now().Add(f.expiresIn)
	}
	return t
}

func (f *fakeProvider) Refresh(_ context.Context, t account.Token) (account.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if f.refreshErr != nil {
		return account.Token{}, f.refreshErr
	}
	if t.Refresh == "" || t.Refresh != f.refresh {
		return account.Token{}, account.ErrRevoked
	}
	return f.issue(), nil
}

func (f *fakeProvider) Profile(_ context.Context, t account.Token) (account.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.profileErr != nil {
		return account.Profile{}, f.profileErr
	}
	return f.profile, nil
}

// renewals is how many times Refresh was asked, read under the lock as
// the renewal's goroutine writes it.
func (f *fakeProvider) renewals() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

func (f *fakeProvider) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// newAccountDevice is a device whose Sync page syncs with page, and
// whose account, signed in with prov, with the remote of the account's
// user in remotes.
func newAccountDevice(t *testing.T, prov *fakeProvider, page cloudsync.Remote, remotes map[string]cloudsync.Remote) *device {
	d := newDevice(t, page)
	d.a.newRemote = func(s config.SyncSettings, credential string) (cloudsync.Remote, error) {
		if s.Provider == config.SyncAccount {
			if credential == "" {
				t.Error("an account sync without a token")
			}
			if r := remotes[s.Account]; r != nil {
				return r, nil
			}
			return nil, errors.New("no remote for " + s.Account)
		}
		return page, nil
	}
	d.a.newProvider = func(string) account.Provider { return prov }
	return d
}

// signIn signs d in through the sign-in window, with sync password pw.
func (d *device) signIn(t *testing.T, pw string) {
	t.Helper()
	d.use(t)
	d.a.openAccountLogin()
	d.p.until(t, "the sign-in", func() bool {
		return d.a.account.login == nil && d.a.settings.Account.SignedIn() && d.a.account.sync.loaded
	})
	if pw != "" {
		d.a.setAccountPassword(pw)
	}
	d.p.until(t, "the store", storeSettled(d.a, d.p))
}

// syncAccount syncs d's account and waits for it, renewal included.
func (d *device) syncAccount(t *testing.T) {
	t.Helper()
	d.use(t)
	if !d.a.accountReady() {
		t.Fatal("the account is not ready to sync")
	}
	d.a.syncAccountNow(false, time.Now())
	if !d.a.account.sync.running && !d.a.account.renewing {
		t.Fatal("the account's sync did not start")
	}
	d.p.until(t, "the account's sync", func() bool { return !d.a.account.sync.running && !d.a.account.renewing })
	d.p.until(t, "the store", storeSettled(d.a, d.p))
}

// chooseMerge syncs d's account, which asks how, as d has connections
// the gist lacks and no base, then merges as chosen.
func (d *device) chooseMerge(t *testing.T) {
	t.Helper()
	d.syncAccount(t)
	if d.a.account.sync.choice == nil {
		t.Fatalf("the sync did not ask; err %v", d.a.account.sync.err)
	}
	d.choose(t, choiceMerge)
}

// choose syncs d's account as chosen in the choice's window.
func (d *device) choose(t *testing.T, choice string) {
	t.Helper()
	d.use(t)
	d.a.chooseSync(d.a.accountSync(), choice, time.Now())
	d.p.until(t, "the chosen sync", func() bool { return !d.a.account.sync.running })
	d.p.until(t, "the store", storeSettled(d.a, d.p))
	if err := d.a.account.sync.err; err != nil {
		t.Fatalf("the chosen sync: %v", err)
	}
}

func accountActions(t *testing.T) []string {
	t.Helper()
	events, err := config.ReadAccountLog()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, e.Action+":"+e.Result)
	}
	return out
}

func lastAccountEvent(t *testing.T) config.AccountEvent {
	t.Helper()
	events, _ := config.ReadAccountLog()
	if len(events) == 0 {
		t.Fatal("no account events")
	}
	return events[len(events)-1]
}

func TestAccountSignInSyncsAcrossDevices(t *testing.T) {
	prov := newFakeProvider()
	page, acc := &memRemote{}, &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": acc}

	one := newAccountDevice(t, prov, page, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "account pw")
	s := one.a.settings.Account
	if s.Provider != "github" || s.UserID != "42" || s.Login != "octo" || s.Email != "octocat@example.com" || !s.Auto || s.Expired {
		t.Fatalf("account settings %+v", s)
	}
	if !one.a.windowOpen(WindowAccount) || one.a.windowOpen(WindowAccountLogin) {
		t.Fatal("the account window did not replace the sign-in window")
	}
	var stored account.Token
	if json.Unmarshal([]byte(one.p.stored[accountTokenKey("github")]), &stored) != nil || stored.Access != "gho_1" {
		t.Fatalf("stored token %q", one.p.stored[accountTokenKey("github")])
	}
	if one.p.stored[accountPasswordKey("github", "42")] != "account pw" {
		t.Fatalf("store %v", one.p.stored)
	}
	saved := config.LoadSettings()
	if saved.Account.Login != "octo" {
		t.Fatalf("settings.json account %+v", saved.Account)
	}

	one.syncAccount(t)
	if err := one.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if acc.writes() != 1 || page.writes() != 0 {
		t.Fatalf("writes: account %d, Sync page %d", acc.writes(), page.writes())
	}
	if st := config.LoadSyncStateFile(config.AccountSyncStateName); !strings.HasPrefix(st.Remote, "account github:42") || st.Base == nil {
		t.Fatalf("account state %+v", st)
	}
	if st := config.LoadSyncState(); st.Remote != "" {
		t.Fatalf("the Sync page's state was written: %+v", st)
	}
	if events, _ := config.ReadSyncLog(); len(events) != 0 {
		t.Fatalf("the account's sync went to the Sync page's log: %+v", events)
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "sync:ok"}) {
		t.Fatalf("log %v", got)
	}
	if e := lastAccountEvent(t); !slices.Equal(e.Steps, []string{config.StepRead, config.StepCreate}) || e.Reason != cloudsync.ReasonManual || e.Login != "octo" {
		t.Fatalf("first sync %+v", e)
	}

	// Another computer signs in to the same account and gets the hosts.
	two := newAccountDevice(t, prov, page, remotes)
	two.addHost("h2", "db")
	two.signIn(t, "account pw")
	two.chooseMerge(t)
	if got := hostNames(two.a); !slices.Equal(got, []string{"db", "web"}) {
		t.Fatalf("second device: %v", got)
	}
	e := lastAccountEvent(t)
	if !slices.Equal(e.Steps, []string{config.StepRead, config.StepMerge, config.StepUpload, config.StepBackup, config.StepApply}) {
		t.Fatalf("second device's steps %v", e.Steps)
	}
	if e.Local != [3]int{1, 0, 0} || e.Server != [3]int{1, 0, 0} {
		t.Fatalf("counts local %v server %v", e.Local, e.Server)
	}
	one.syncAccount(t)
	if got := hostNames(one.a); !slices.Equal(got, []string{"db", "web"}) {
		t.Fatalf("first device: %v", got)
	}
}

// The Sync page's sync and the account's keep their own remote, state
// and log, and never run at once.
func TestAccountSeparateFromSyncPage(t *testing.T) {
	prov := newFakeProvider()
	page, acc := &memRemote{}, &memRemote{}
	d := newAccountDevice(t, prov, page, map[string]cloudsync.Remote{"github:42": acc})
	d.addHost("h1", "web")
	d.signIn(t, "account pw")

	d.use(t)
	d.a.startSync(false, time.Now())
	if !d.a.sync.running {
		t.Fatal("the Sync page's sync did not start")
	}
	d.a.syncAccountNow(false, time.Now())
	if d.a.account.sync.running {
		t.Fatal("the account synced while the Sync page's sync ran")
	}
	d.p.until(t, "the sync", func() bool { return !d.a.sync.running })
	d.syncAccount(t)
	if page.writes() != 1 || acc.writes() != 1 {
		t.Fatalf("writes: page %d, account %d", page.writes(), acc.writes())
	}
	if a, b := config.LoadSyncState(), config.LoadSyncStateFile(config.AccountSyncStateName); a.Remote == b.Remote || a.Base == nil || b.Base == nil {
		t.Fatalf("states %q and %q", a.Remote, b.Remote)
	}
	if events, _ := config.ReadSyncLog(); len(events) != 1 {
		t.Fatalf("Sync page's log: %+v", events)
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "sync:ok"}) {
		t.Fatalf("account log %v", got)
	}

	// Each password opens only its own file.
	if _, err := cloudsync.Open("account pw", acc.data); err != nil {
		t.Fatalf("the account's file: %v", err)
	}
	if _, err := cloudsync.Open("account pw", page.data); !errors.Is(err, cloudsync.ErrWrongPassword) {
		t.Fatalf("the Sync page's file opened with the account's password: %v", err)
	}
}

// Automatic sync syncs the account at start and after changes.
func TestAccountSyncsAutomatically(t *testing.T) {
	prov := newFakeProvider()
	acc := &memRemote{}
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": acc})
	d.addHost("h1", "web")
	d.signIn(t, "pw")
	d.a.closeWindow(WindowAccount)
	tt := ui.NewTester(d.a.View, 1000, 700)
	d.p.tt = tt
	tt.Frame()
	d.p.until(t, "the first automatic sync", func() bool { return acc.writes() == 1 && !d.a.account.sync.running })
	d.addHost("h2", "db")
	tt.Advance(time.Minute)
	d.p.until(t, "the sync of the change", func() bool { return acc.writes() == 2 && !d.a.account.sync.running })
	if e := lastAccountEvent(t); e.Reason != cloudsync.ReasonAuto {
		t.Fatalf("reason %q", e.Reason)
	}
}

// A host added while the account's sync is away is kept, and synced
// next.
func TestAccountLocalChangeDuringSync(t *testing.T) {
	prov := newFakeProvider()
	acc := &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": acc}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "pw")
	one.syncAccount(t)

	two := newAccountDevice(t, prov, &memRemote{}, remotes)
	two.signIn(t, "pw")
	two.syncAccount(t)
	two.addHost("h2", "db")
	two.use(t)
	two.a.syncAccountNow(false, time.Now())
	two.addHost("h3", "added meanwhile")
	two.p.until(t, "the sync", func() bool { return !two.a.account.sync.running })
	if err := two.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if got := hostNames(two.a); !slices.Equal(got, []string{"added meanwhile", "db", "web"}) {
		t.Fatalf("hosts %v", got)
	}
	if !two.a.account.sync.nextCheck.IsZero() || cloudsync.Equal(two.a.localPayload(), two.a.account.sync.synced) {
		t.Fatal("the change made meanwhile is not due to sync")
	}
	two.syncAccount(t)
	one.syncAccount(t)
	if got := hostNames(one.a); len(got) != 3 {
		t.Fatalf("the first device has %v", got)
	}
}

// A token about to expire is renewed before the sync, which lists it.
func TestAccountRenewsBeforeSync(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = 2 * time.Minute // within the margin: renewed at once
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "pw")
	d.syncAccount(t)
	if prov.renewals() != 1 || d.a.account.token.Access != "gho_2" {
		t.Fatalf("refreshes %d, token %+v", prov.renewals(), d.a.account.token)
	}
	var stored account.Token
	json.Unmarshal([]byte(d.p.stored[accountTokenKey("github")]), &stored)
	if stored.Access != "gho_2" || stored.Refresh != "ghr_2" {
		t.Fatalf("stored %+v", stored)
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "refresh:ok", "sync:ok"}) {
		t.Fatalf("log %v", got)
	}
	if e := lastAccountEvent(t); len(e.Steps) == 0 || e.Steps[0] != config.StepRefresh {
		t.Fatalf("steps %v", e.Steps)
	}
}

// Another copy of the app renewed the token, which made this one's
// refresh token stop working: the stored token is used.
func TestAccountRenewedByAnotherCopy(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = 2 * time.Minute
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "pw")
	other, err := prov.Refresh(context.Background(), d.a.account.token)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(other)
	d.p.stored[accountTokenKey("github")] = string(data)
	d.syncAccount(t)
	if prov.renewals() != 1 || d.a.account.token.Access != other.Access || d.a.settings.Account.Expired {
		t.Fatalf("refreshes %d, token %+v, expired %v", prov.renewals(), d.a.account.token, d.a.settings.Account.Expired)
	}
	if d.a.account.sync.err != nil {
		t.Fatal(d.a.account.sync.err)
	}
}

// A refresh token that no longer works signs the account out until the
// user signs in again, which keeps its gist and options.
func TestAccountRevokedSignInExpires(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = 2 * time.Minute
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "pw")
	d.a.settings.Account.GistID, d.a.settings.Account.Interval = "g1", 30
	// Revoked: neither the refresh token nor the access token works.
	prov.set(func() { prov.refreshErr, prov.profileErr = account.ErrRevoked, account.ErrRevoked })
	d.use(t)
	d.a.syncAccountNow(false, time.Now())
	d.p.until(t, "the renewal", func() bool { return !d.a.account.renewing })
	d.p.until(t, "the store", storeSettled(d.a, d.p))
	if !d.a.settings.Account.Expired || d.a.accountReady() || d.a.account.sync.running {
		t.Fatalf("expired %v, ready %v", d.a.settings.Account.Expired, d.a.accountReady())
	}
	if _, ok := d.p.stored[accountTokenKey("github")]; ok {
		t.Fatal("the dead token is still stored")
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "expired:failed"}) {
		t.Fatalf("log %v", got)
	}

	prov.set(func() { prov.refreshErr, prov.profileErr = nil, nil; prov.expiresIn = 0 })
	d.signIn(t, "")
	s := d.a.settings.Account
	if s.Expired || s.GistID != "g1" || s.Interval != 30 || !d.a.accountReady() {
		t.Fatalf("signed in again: %+v, ready %v", s, d.a.accountReady())
	}
}

// authFailRemote refuses the token, as GitHub does a revoked one.
type authFailRemote struct{ memRemote }

func (*authFailRemote) Get(context.Context) ([]byte, cloudsync.Version, error) {
	return nil, cloudsync.Version{}, cloudsync.ErrAuth
}

// A token that does not expire, refused by the remote, is checked, and
// the sign-in marked expired when GitHub no longer knows it.
func TestAccountRefusedTokenChecked(t *testing.T) {
	prov := newFakeProvider()
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &authFailRemote{}})
	d.signIn(t, "pw")
	prov.set(func() { prov.profileErr = account.ErrRevoked })
	d.use(t)
	d.a.syncAccountNow(false, time.Now())
	d.p.until(t, "the check", func() bool { return d.a.settings.Account.Expired })
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "sync:failed", "expired:failed"}) {
		t.Fatalf("log %v", got)
	}
	events, _ := config.ReadAccountLog()
	if events[1].Error != locales.SyncErrAuth() {
		t.Fatalf("error %q", events[1].Error)
	}
}

// The OAuth app stops making tokens expire: a renewal gives one that does
// not, and one renewed no more goes on; a refused refresh token, its
// access token still working, goes on too, as one that does not expire.
func TestAccountTokenModes(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = 2 * time.Minute
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "pw")
	prov.set(func() { prov.expiresIn = 0 }) // opted out
	d.syncAccount(t)
	tok := d.a.account.token
	if prov.renewals() != 1 || tok.Refresh != "" || !tok.Expires.IsZero() || d.a.account.sync.err != nil {
		t.Fatalf("renewals %d, token %+v, err %v", prov.renewals(), tok, d.a.account.sync.err)
	}
	d.syncAccount(t)
	if prov.renewals() != 1 {
		t.Fatal("a token that does not expire was renewed")
	}

	// A refresh token refused while the access token still works.
	prov2 := newFakeProvider()
	prov2.expiresIn = 2 * time.Minute
	d2 := newAccountDevice(t, prov2, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d2.signIn(t, "pw")
	prov2.set(func() { prov2.refreshErr = account.ErrRevoked })
	d2.syncAccount(t)
	if d2.a.settings.Account.Expired || d2.a.account.sync.err != nil {
		t.Fatalf("expired %v, err %v", d2.a.settings.Account.Expired, d2.a.account.sync.err)
	}
	if tok := d2.a.account.token; tok.Refresh != "" || !tok.Expires.IsZero() || tok.Access == "" {
		t.Fatalf("token %+v", tok)
	}
	if !hasAccountEvent(t, config.AccountRefresh, "no-expiry") {
		t.Fatalf("log %v", accountActions(t))
	}
}

// A remote that keeps refusing a renewed token renews it once, not in a
// loop.
func TestAccountRefusedRenewedOnce(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = time.Hour
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &authFailRemote{}})
	d.signIn(t, "pw")
	d.use(t)
	d.a.syncAccountNow(false, time.Now())
	d.p.until(t, "the renewal and the retry", func() bool {
		return prov.renewals() == 1 && !d.a.account.renewing && !d.a.account.sync.running
	})
	d.p.until(t, "the store", storeSettled(d.a, d.p))
	for range 20 {
		d.p.drain()
		time.Sleep(5 * time.Millisecond)
	}
	if prov.renewals() != 1 || d.a.settings.Account.Expired {
		t.Fatalf("refreshes %d, expired %v", prov.renewals(), d.a.settings.Account.Expired)
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "sync:failed", "refresh:ok", "sync:failed"}) {
		t.Fatalf("log %v", got)
	}
}

// A renewal GitHub answers with a token that does not work, as it does for
// an expiring token after the OAuth app opted out of expiry: the refused
// renewed token is checked, not renewed again, and the sign-in marked
// expired.
func TestAccountDeadRenewedTokenExpires(t *testing.T) {
	prov := newFakeProvider()
	prov.expiresIn = 2 * time.Minute // within the margin: renewed at once
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &authFailRemote{}})
	d.signIn(t, "pw")
	prov.set(func() { prov.profileErr = account.ErrRevoked })
	d.use(t)
	d.a.syncAccountNow(false, time.Now())
	d.p.until(t, "the check", func() bool { return d.a.settings.Account.Expired })
	d.p.until(t, "the store", storeSettled(d.a, d.p))
	if prov.renewals() != 1 {
		t.Fatalf("refreshes %d, want 1", prov.renewals())
	}
	if got := accountActions(t); !slices.Equal(got, []string{"sign-in:ok", "refresh:ok", "sync:failed", "expired:failed"}) {
		t.Fatalf("log %v", got)
	}
	if _, ok := d.p.stored[accountTokenKey("github")]; ok {
		t.Fatal("the dead token is still stored")
	}
}

// A token the store does not give at start leaves the account waiting
// for a sign-in in this run, without deleting anything; given again, it
// syncs.
func TestAccountTokenMissingAtStart(t *testing.T) {
	prov := newFakeProvider()
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "pw")
	tokenJSON := d.p.stored[accountTokenKey("github")]
	delete(d.p.stored, accountTokenKey("github"))
	d.a.forgetAccountSecrets()
	d.a.loadAccount()
	d.p.until(t, "the load", func() bool { return d.a.account.sync.loaded })
	if !d.a.settings.Account.Expired || d.a.accountReady() {
		t.Fatal("ready without a token")
	}
	if saved := config.LoadSettings(); saved.Account.Expired {
		t.Fatal("saved as expired")
	}
	d.p.stored[accountTokenKey("github")] = tokenJSON
	d.a.forgetAccountSecrets()
	d.a.loadAccount()
	d.p.until(t, "the load", func() bool { return d.a.account.sync.loaded })
	if d.a.settings.Account.Expired || !d.a.accountReady() {
		t.Fatal("not ready with the token back")
	}
	d.syncAccount(t)
}

// Another account starts its own gist; the first sync keeps what this
// computer has, as nothing was synced there yet.
func TestAccountSwitchUser(t *testing.T) {
	prov := newFakeProvider()
	first, second := &memRemote{}, &memRemote{}
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": first, "github:77": second})
	d.addHost("h1", "web")
	d.signIn(t, "pw one")
	d.syncAccount(t)
	d.a.settings.Account.GistID = "g1"

	prov.set(func() { prov.profile = account.Profile{ID: "77", Login: "other"} })
	d.signIn(t, "")
	s := d.a.settings.Account
	if s.UserID != "77" || s.GistID != "" || d.a.account.sync.password != "" || d.a.accountReady() {
		t.Fatalf("the other account: %+v, password %q", s, d.a.account.sync.password)
	}
	if st := config.LoadSyncStateFile(config.AccountSyncStateName); st.Base != nil {
		t.Fatal("the first account's base was kept")
	}
	d.a.setAccountPassword("pw two")
	d.syncAccount(t)
	if second.writes() != 1 || first.writes() != 1 || !d.a.account.sync.done.Created {
		t.Fatalf("writes: first %d, second %d", first.writes(), second.writes())
	}
	if got := hostNames(d.a); !slices.Equal(got, []string{"web"}) {
		t.Fatalf("hosts %v", got)
	}
	// Each user's sync password stays under its own key.
	if d.p.stored[accountPasswordKey("github", "42")] != "pw one" || d.p.stored[accountPasswordKey("github", "77")] != "pw two" {
		t.Fatalf("store %v", d.p.stored)
	}
}

// Signing in is all it takes: the email address is the sync password,
// the default sync settings sync automatically with saved passwords, and
// another computer signing in to the account gets the hosts and their
// passwords.
func TestAccountEmailPasswordAndDefaults(t *testing.T) {
	prov := newFakeProvider()
	acc := &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": acc}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.p.stored[hostSecretKey("h1", secretPassword)] = "s3cret"
	one.signIn(t, "")
	s := one.a.settings.Account
	if one.a.account.sync.password != "octocat@example.com" || s.CustomPassword || s.CustomSync {
		t.Fatalf("password %q, settings %+v", one.a.account.sync.password, s)
	}
	if opts := s.SyncSettings(); !opts.Auto || !opts.Passwords || opts.IntervalOrDefault() != 15 {
		t.Fatalf("default sync %+v", opts)
	}
	one.syncAccount(t)
	if _, err := cloudsync.Open("octocat@example.com", acc.data); err != nil {
		t.Fatalf("the gist does not open with the email: %v", err)
	}

	two := newAccountDevice(t, prov, &memRemote{}, remotes)
	two.signIn(t, "")
	two.syncAccount(t)
	if got := hostNames(two.a); !slices.Equal(got, []string{"web"}) {
		t.Fatalf("hosts %v", got)
	}
	if got := two.p.stored[hostSecretKey("h1", secretPassword)]; got != "s3cret" {
		t.Fatalf("the password did not come: %q", got)
	}

	// Custom settings are the account's own, apart from the Sync page's.
	two.a.settings.Sync.Interval = 60
	two.a.settings.Account.CustomSync, two.a.settings.Account.Interval = true, 5
	if got := two.a.settings.Account.SyncSettings().IntervalOrDefault(); got != 5 {
		t.Fatalf("custom interval %d", got)
	}
}

// Changing the sync password seals the gist's file again, with what other
// computers wrote kept; they ask for the new password, and sync once it
// is typed.
func TestAccountChangePassword(t *testing.T) {
	prov := newFakeProvider()
	acc := &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": acc}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "")
	one.syncAccount(t)
	two := newAccountDevice(t, prov, &memRemote{}, remotes)
	two.addHost("h2", "db")
	two.signIn(t, "")
	two.chooseMerge(t) // the gist has both now; one has not seen db

	one.use(t)
	one.a.changeAccountPassword("correct horse")
	one.p.until(t, "the change", func() bool { return !one.a.account.view.pwBusy })
	one.p.until(t, "the store", storeSettled(one.a, one.p))
	if err := one.a.account.view.pwErr; err != nil {
		t.Fatal(err)
	}
	if !one.a.settings.Account.CustomPassword || one.p.stored[accountPasswordKey("github", "42")] != "correct horse" {
		t.Fatalf("custom %v, store %v", one.a.settings.Account.CustomPassword, one.p.stored)
	}
	plain, err := cloudsync.Open("correct horse", acc.data)
	if err != nil {
		t.Fatalf("the gist: %v", err)
	}
	if !strings.Contains(string(plain), "db") {
		t.Fatal("re-encrypting lost what the other computer wrote")
	}
	if _, err := cloudsync.Open("octocat@example.com", acc.data); !errors.Is(err, cloudsync.ErrWrongPassword) {
		t.Fatalf("the old password still opens it: %v", err)
	}
	// The base was sealed again: the next sync merges, and gets db.
	one.syncAccount(t)
	if err := one.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if got := hostNames(one.a); !slices.Equal(got, []string{"db", "web"}) {
		t.Fatalf("after the change: %v", got)
	}
	if got := accountActions(t); !slices.Contains(got, "password:ok") {
		t.Fatalf("log %v", got)
	}

	// The other computer: the wrong password, then the new one typed.
	two.use(t)
	two.a.syncAccountNow(false, time.Now())
	two.p.until(t, "the sync", func() bool { return !two.a.account.sync.running })
	if !errors.Is(two.a.account.sync.err, cloudsync.ErrWrongPassword) {
		t.Fatalf("err %v", two.a.account.sync.err)
	}
	two.a.setAccountPassword("correct horse")
	two.syncAccount(t)
	if err := two.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if !two.a.settings.Account.CustomPassword {
		t.Fatal("not marked custom")
	}

	// Back to the email address.
	one.use(t)
	one.a.changeAccountPassword("octocat@example.com")
	one.p.until(t, "the change", func() bool { return !one.a.account.view.pwBusy })
	if one.a.settings.Account.CustomPassword {
		t.Fatal("still custom")
	}
	if _, err := cloudsync.Open("octocat@example.com", acc.data); err != nil {
		t.Fatal(err)
	}
}

// The bar at the bottom of the left panel signs in, or shows the account
// and opens its card, whose email address shows whole on request.
func TestAccountBar(t *testing.T) {
	prov := newFakeProvider()
	prov.pending = true
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	tt := ui.NewTester(d.a.View, 1000, 700)
	d.p.tt = tt
	tt.Frame()
	if !tt.HasText(locales.AccountSignInBar()) {
		t.Fatalf("no sign-in bar; texts %q", tt.Texts())
	}
	tt.Click(locales.AccountSignInBar())
	if !d.a.windowOpen(WindowAccountLogin) {
		t.Fatal("the bar did not open the sign-in window")
	}
	prov.set(func() { prov.pending = false })
	d.p.until(t, "the sign-in", func() bool { return d.a.settings.Account.SignedIn() && d.a.account.sync.loaded })
	d.a.closeWindow(WindowAccount)
	tt.Frame()
	if !tt.HasText("Octo Cat") { // the display name, before the user name
		t.Fatalf("no account in the bar; texts %q", tt.Texts())
	}
	d.a.account.view.cardOpen = true
	tt.Frame()
	for _, s := range []string{"Octo Cat", "@octo", "oc***@example.com", locales.AccountSignedInGitHub(), locales.AccountSettingsButton(), locales.AccountSignOut()} {
		if !tt.HasText(s) {
			t.Errorf("card: no %q; texts %q", s, tt.Texts())
		}
	}
	tt.Click(locales.AccountShowEmail())
	tt.Frame()
	if !tt.HasText("octocat@example.com") {
		t.Errorf("the whole address is not shown; texts %q", tt.Texts())
	}
}

// staleDevices are a busy computer whose gist has 5 connections, and an
// old one with an old copy: web as it was before the busy one renamed
// it, and a connection of its own.
func staleDevices(t *testing.T) (busy, old *device, gist *memRemote) {
	prov := newFakeProvider()
	gist = &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": gist}
	busy = newAccountDevice(t, prov, &memRemote{}, remotes)
	busy.addHost("h1", "web")
	for _, n := range []string{"db", "cache", "mq", "search"} {
		busy.addHost("h-"+n, n)
	}
	busy.signIn(t, "")
	busy.syncAccount(t)
	busy.a.hosts.Host("h1").Name = "web-renamed"
	busy.a.saveHosts(busy.a.hosts)
	busy.syncAccount(t)

	old = newAccountDevice(t, prov, &memRemote{}, remotes)
	old.addHost("h1", "web")
	old.addHost("h-old", "legacy")
	old.signIn(t, "")
	return busy, old, gist
}

// An old computer's first sync asks before anything changes, and merging
// keeps both sides with the newer, cloud's, version of what differs.
func TestAccountStaleChoiceMerge(t *testing.T) {
	busy, old, gist := staleDevices(t)
	writes := gist.writes()
	old.syncAccount(t)
	ch := old.a.account.sync.choice
	if ch == nil {
		t.Fatalf("no choice; err %v", old.a.account.sync.err)
	}
	if ch.Local != 2 || ch.Remote != 5 || ch.OnlyLocal != 1 || ch.OnlyRemote != 4 || ch.Differ != 1 || ch.Merged != 6 {
		t.Fatalf("choice %+v", *ch)
	}
	if gist.writes() != writes || !slices.Equal(hostNames(old.a), []string{"legacy", "web"}) {
		t.Fatal("asking changed something")
	}
	if !old.a.windowOpen(WindowAccountChoice) {
		t.Fatal("the choice's window is not open")
	}
	tt := old.p.show(t, WindowAccountChoice)
	tt.Frame()
	for _, s := range []string{locales.AccountChoiceMergeTitle(), locales.AccountChoiceRemoteTitle(), locales.AccountChoiceLocalTitle(), locales.AccountChoiceCount(5)} {
		if !tt.HasText(s) {
			t.Errorf("no %q; texts %q", s, tt.Texts())
		}
	}
	// Automatic sync waits for the choice.
	old.a.syncTickOn(nil, old.a.accountSync())
	if old.a.account.sync.running {
		t.Fatal("automatic sync ran while the choice waits")
	}
	old.choose(t, choiceMerge)
	want := []string{"cache", "db", "legacy", "mq", "search", "web-renamed"}
	if got := hostNames(old.a); !slices.Equal(got, want) {
		t.Fatalf("merged %v", got)
	}
	busy.syncAccount(t)
	if got := hostNames(busy.a); !slices.Equal(got, want) {
		t.Fatalf("the busy computer has %v", got)
	}
	old.use(t)
	if got := accountActions(t); !slices.Contains(got, "choice:ok") || !slices.Contains(got, "sync:paused") {
		t.Fatalf("log %v", got)
	}
}

// Taking the cloud's: the old computer gets the gist's 5, after a backup.
func TestAccountStaleChoiceRemote(t *testing.T) {
	_, old, gist := staleDevices(t)
	old.syncAccount(t)
	writes := gist.writes()
	old.choose(t, choiceRemote)
	if got := hostNames(old.a); !slices.Equal(got, []string{"cache", "db", "mq", "search", "web-renamed"}) {
		t.Fatalf("hosts %v", got)
	}
	if gist.writes() != writes {
		t.Fatal("taking the cloud's wrote the gist")
	}
	if backups, _ := config.ListBackups(); len(backups) == 0 {
		t.Fatal("no backup before the cloud's replaced this computer's")
	}
}

// Keeping this computer's: the gist gets its 2, and the busy computer
// follows at its next sync.
func TestAccountStaleChoiceLocal(t *testing.T) {
	busy, old, _ := staleDevices(t)
	old.syncAccount(t)
	old.choose(t, choiceLocal)
	busy.syncAccount(t)
	if got := hostNames(busy.a); !slices.Equal(got, []string{"legacy", "web"}) {
		t.Fatalf("the busy computer has %v", got)
	}
}

// A computer that synced before, then not for long, asks again; one that
// synced recently merges three ways, its untouched old copy updated.
func TestAccountStaleAfterAMonth(t *testing.T) {
	busy, old, _ := staleDevices(t)
	old.syncAccount(t)
	old.choose(t, choiceMerge)
	busy.a.hosts.Host("h-db").Name = "db-2"
	busy.a.saveHosts(busy.a.hosts)
	busy.syncAccount(t)
	old.addHost("h-new", "new-here")
	old.syncAccount(t) // recent: merges, no question
	if old.a.account.sync.choice != nil || !slices.Contains(hostNames(old.a), "db-2") {
		t.Fatalf("a recent sync: choice %v, hosts %v", old.a.account.sync.choice, hostNames(old.a))
	}
	busy.a.hosts.Host("h-db").Name = "db-3"
	busy.a.saveHosts(busy.a.hosts)
	busy.syncAccount(t)
	old.a.hosts.Host("h-db").Name = "db-old-edit"
	old.a.saveHosts(old.a.hosts)
	old.a.account.sync.state.At = time.Now().Add(-40 * 24 * time.Hour)
	old.syncAccount(t)
	if old.a.account.sync.choice == nil {
		t.Fatal("40 days without a sync did not ask")
	}
}

// Devices of one account with different kinds of password: one changes
// to a password of its own, and another, on the email address, is told
// where and asked for it, keeping its base, so its own changes merge
// three ways; when the first goes back to the email address, the other
// follows by itself.
func TestAccountMixedPasswords(t *testing.T) {
	prov := newFakeProvider()
	gist := &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": gist}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "")
	one.syncAccount(t)
	two := newAccountDevice(t, prov, &memRemote{}, remotes)
	two.signIn(t, "")
	two.syncAccount(t)

	one.use(t)
	one.a.changeAccountPassword("correct horse")
	one.p.until(t, "the change", func() bool { return !one.a.account.view.pwBusy })
	if meta := cloudsync.ReadSnapshot(gist.data); meta.KeyHint != cloudsync.KeyCustom {
		t.Fatalf("header %+v", meta)
	}

	// Two, still on the email address, made a change meanwhile.
	two.a.hosts.Host("h1").Name = "web-two"
	two.a.saveHosts(two.a.hosts)
	two.use(t)
	two.a.syncAccountNow(false, time.Now())
	two.p.until(t, "the sync", func() bool { return !two.a.account.sync.running })
	var pe *cloudsync.PasswordError
	if !errors.As(two.a.account.sync.err, &pe) || pe.Hint != cloudsync.KeyCustom || pe.Device == "" {
		t.Fatalf("err %v", two.a.account.sync.err)
	}
	if _, text, _, _ := two.a.accountSyncState(time.Now()); text != locales.AccountNeedPassword() {
		t.Fatalf("bar says %q", text)
	}
	two.a.setAccountPassword("correct horse")
	two.syncAccount(t)
	if two.a.account.sync.choice != nil || two.a.account.sync.err != nil {
		t.Fatalf("after typing it: choice %v, err %v", two.a.account.sync.choice, two.a.account.sync.err)
	}
	one.syncAccount(t)
	if got := hostNames(one.a); !slices.Equal(got, []string{"web-two"}) {
		t.Fatalf("two's change did not merge: %v", got)
	}

	// One goes back to the email address; two follows by itself.
	one.use(t)
	one.a.changeAccountPassword("octocat@example.com")
	one.p.until(t, "the change", func() bool { return !one.a.account.view.pwBusy })
	two.syncAccount(t)
	if err := two.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if two.a.account.sync.password != "octocat@example.com" || two.a.settings.Account.CustomPassword {
		t.Fatalf("two did not follow: %q", two.a.account.sync.password)
	}
	if got := two.p.stored[accountPasswordKey("github", "42")]; got != "octocat@example.com" {
		t.Fatalf("stored %q", got)
	}
	two.use(t)
	if !hasAccountEvent(t, config.AccountPassword, "auto") {
		t.Fatalf("log %v", accountActions(t))
	}
}

// hasAccountEvent says whether the account's log has an event of action
// and reason.
func hasAccountEvent(t *testing.T, action, reason string) bool {
	t.Helper()
	events, _ := config.ReadAccountLog()
	for _, e := range events {
		if e.Action == action && e.Reason == reason {
			return true
		}
	}
	return false
}

// A primary address changed since the gist was sealed: another verified
// address of the account still opens it.
func TestAccountEmailChanged(t *testing.T) {
	prov := newFakeProvider()
	prov.profile.Emails = []string{"octocat@example.com"}
	remotes := map[string]cloudsync.Remote{"github:42": &memRemote{}}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "")
	one.syncAccount(t)

	prov.set(func() {
		prov.profile.Email = "new@example.com"
		prov.profile.Emails = []string{"new@example.com", "octocat@example.com"}
	})
	two := newAccountDevice(t, prov, &memRemote{}, remotes)
	two.signIn(t, "")
	if two.a.account.sync.password != "new@example.com" {
		t.Fatalf("password %q", two.a.account.sync.password)
	}
	two.syncAccount(t)
	if err := two.a.account.sync.err; err != nil {
		t.Fatal(err)
	}
	if got := hostNames(two.a); !slices.Equal(got, []string{"web"}) {
		t.Fatalf("hosts %v", got)
	}
	if two.a.account.sync.password != "octocat@example.com" || two.a.settings.Account.CustomPassword {
		t.Fatalf("followed %q, custom %v", two.a.account.sync.password, two.a.settings.Account.CustomPassword)
	}
}

// The primary address changes on GitHub, the old one removed: a computer
// that reads the profile seals the gist with the new address; one signing
// in now opens it with the default, and an old one, still on the old
// address, follows by itself. Nobody types anything.
func TestAccountEmailMigrates(t *testing.T) {
	prov := newFakeProvider()
	gist := &memRemote{}
	remotes := map[string]cloudsync.Remote{"github:42": gist}
	one := newAccountDevice(t, prov, &memRemote{}, remotes)
	one.addHost("h1", "web")
	one.signIn(t, "")
	one.syncAccount(t)
	old := newAccountDevice(t, prov, &memRemote{}, remotes)
	old.signIn(t, "")
	old.syncAccount(t)

	prov.set(func() {
		prov.profile.Email = "new@example.com"
		prov.profile.Emails = []string{"new@example.com"}
	})
	one.use(t)
	one.a.account.profileAt = time.Time{}
	one.a.refreshAccountProfile(time.Now())
	one.p.until(t, "the migration", func() bool {
		return !one.a.account.profileBusy && !one.a.account.sync.running && one.a.account.sync.password == "new@example.com"
	})
	if _, err := cloudsync.Open("new@example.com", gist.data); err != nil {
		t.Fatalf("the gist: %v", err)
	}
	if one.a.settings.Account.Email != "new@example.com" || one.a.settings.Account.CustomPassword {
		t.Fatalf("settings %+v", one.a.settings.Account)
	}
	if !hasAccountEvent(t, config.AccountPassword, accountReasonEmail) {
		t.Fatalf("log %v", accountActions(t))
	}
	// Again: nothing to do.
	one.a.account.profileAt = time.Time{}
	one.a.refreshAccountProfile(time.Now())
	one.p.until(t, "the profile", func() bool { return !one.a.account.profileBusy })

	fresh := newAccountDevice(t, prov, &memRemote{}, remotes)
	fresh.signIn(t, "")
	fresh.syncAccount(t)
	if got := hostNames(fresh.a); !slices.Equal(got, []string{"web"}) || fresh.a.account.sync.err != nil {
		t.Fatalf("a new computer: %v, %v", got, fresh.a.account.sync.err)
	}

	// The old computer has not read the profile: its address no longer
	// opens the gist; the new one, read with the profile, does.
	old.use(t)
	old.a.account.profileAt = time.Time{}
	old.a.refreshAccountProfile(time.Now())
	old.p.until(t, "the profile", func() bool { return !old.a.account.profileBusy && !old.a.account.sync.running })
	old.syncAccount(t)
	if err := old.a.account.sync.err; err != nil || old.a.account.sync.password != "new@example.com" {
		t.Fatalf("the old computer: %v, %q", err, old.a.account.sync.password)
	}
}

// Automatic syncs that find nothing to do are one entry of the log,
// counting them; one that changes something is an entry of its own.
func TestAccountLogCoalescesQuietSyncs(t *testing.T) {
	prov := newFakeProvider()
	acc := &memRemote{}
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": acc})
	d.addHost("h1", "web")
	d.signIn(t, "")
	d.syncAccount(t)
	auto := func() {
		t.Helper()
		d.use(t)
		d.a.startSyncOn(d.a.accountSync(), syncOptions{reason: cloudsync.ReasonAuto}, time.Now())
		d.p.until(t, "the sync", func() bool { return !d.a.account.sync.running })
	}
	for range 3 {
		auto()
	}
	events, _ := config.ReadAccountLog()
	last := events[len(events)-1]
	if len(events) != 3 || last.Repeat != 2 || last.Since.IsZero() || !quietSync(last) {
		t.Fatalf("%d events, last %+v", len(events), last)
	}
	d.addHost("h2", "db")
	auto()
	auto()
	if got := accountActions(t); len(got) != 5 {
		t.Fatalf("log %v", got)
	}

	// The page: the count and the latest; the whole log, a table, in a
	// window of its own, filtered.
	tt := d.p.show(t, WindowAccount)
	tt.Frame()
	for _, s := range []string{locales.AccountLogCount(5), locales.AccountLogAllButton(), locales.AccountLogRepeat(3, last.Since.Local().Format("15:04"))} {
		if !tt.HasText(s) {
			t.Errorf("no %q; texts %q", s, tt.Texts())
		}
	}
	d.a.openAccountLog()
	lt := d.p.show(t, WindowAccountLog)
	lt.Frame()
	for _, s := range []string{locales.AccountLogShown(5, 5), locales.AccountLogColTrigger(), locales.AccountLogRepeatShort(3), "+1 −0 ~0"} {
		if !lt.HasText(s) {
			t.Errorf("log window: no %q; texts %q", s, lt.Texts())
		}
	}
	d.a.account.view.logRow = 0
	lt.Frame()
	if !lt.HasText("1. " + locales.AccountStepRead()) {
		t.Errorf("the chosen entry's steps; texts %q", lt.Texts())
	}
	d.a.account.view.logFilter = 3 // the account's own
	lt.Frame()
	if !lt.HasText(locales.AccountLogShown(1, 5)) || !lt.HasText(locales.AccountActSignIn("octo")) {
		t.Errorf("the account filter; texts %q", lt.Texts())
	}
	saveShot(t, lt, "account-log-window.png")
}

// Quiet automatic syncs an older build logged one by one show as one.
func TestAccountLogCompactsOldEntries(t *testing.T) {
	d := newAccountDevice(t, newFakeProvider(), &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "")
	quiet := config.AccountEvent{Action: config.AccountSync, Reason: cloudsync.ReasonAuto, Result: "ok",
		Steps: []string{config.StepRead, config.StepMerge, config.StepUpToDate}, Login: "octo", Device: "pc"}
	for range 12 {
		if err := config.AppendAccountEvent(quiet); err != nil {
			t.Fatal(err)
		}
	}
	d.a.account.view.events = nil
	d.a.loadAccountLog()
	events := d.a.account.view.events
	if len(events) != 2 || events[0].Repeat != 11 || !quietSync(events[0]) {
		t.Fatalf("%d events, first %+v", len(events), events[0])
	}
}

// Options has an Account page while an account is signed in, the account
// window's, and none signed out; the bar shows the display name.
func TestAccountOptionsPage(t *testing.T) {
	d := newAccountDevice(t, newFakeProvider(), &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.a.openOptions()
	tt := d.p.show(t, WindowOptions)
	tt.Frame()
	if tt.HasText(locales.AccountWindowTitle()) {
		t.Fatal("an Account page while signed out")
	}
	d.signIn(t, "")
	d.a.optionsNav.Page = pageAccount
	tt.Frame()
	for _, s := range []string{"Octo Cat", "@octo", locales.AccountSyncTitle(), locales.AccountLog(), locales.AccountCurrentPassword()} {
		if !tt.HasText(s) {
			t.Errorf("no %q; texts %q", s, tt.Texts())
		}
	}
	saveShot(t, tt, "options-account.png")
	if got := accountDisplayName(d.a.settings.Account); got != "Octo Cat" {
		t.Fatalf("display name %q", got)
	}
	d.a.settings.Account.Name = " "
	if got := accountDisplayName(d.a.settings.Account); got != "octo" {
		t.Fatalf("no display name: %q", got)
	}
	d.a.signOut()
	tt.Frame()
	if d.a.optionsNav.Page == pageAccount || tt.HasText(locales.AccountSyncTitle()) {
		t.Fatal("the Account page stays after signing out")
	}
}

// The account window shows the sync password on request, and copies it.
func TestAccountPasswordReveal(t *testing.T) {
	d := newAccountDevice(t, newFakeProvider(), &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.signIn(t, "")
	tt := d.p.show(t, WindowAccount)
	tt.Frame()
	if !tt.HasText(locales.AccountCurrentPassword()) || tt.HasText("octocat@example.com") {
		t.Fatalf("texts %q", tt.Texts())
	}
	d.a.account.view.showPassword = true
	tt.Frame()
	if !tt.HasText("octocat@example.com") {
		t.Fatalf("not shown; texts %q", tt.Texts())
	}
}

// Signing out forgets the account here, and keeps the connections.
func TestAccountSignOut(t *testing.T) {
	prov := newFakeProvider()
	d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
	d.addHost("h1", "web")
	d.signIn(t, "pw")
	d.syncAccount(t)
	if err := d.a.signOut(); err != nil {
		t.Fatal(err)
	}
	d.p.until(t, "the store", storeSettled(d.a, d.p))
	if d.a.settings.Account.SignedIn() || d.a.account.token.Access != "" || d.a.account.sync.password != "" {
		t.Fatalf("still signed in: %+v", d.a.settings.Account)
	}
	for _, k := range []string{accountTokenKey("github"), accountPasswordKey("github", "42")} {
		if _, ok := d.p.stored[k]; ok {
			t.Errorf("%s still stored", k)
		}
	}
	if st := config.LoadSyncStateFile(config.AccountSyncStateName); st.Remote != "" {
		t.Fatal("the account's state is kept")
	}
	if got := hostNames(d.a); !slices.Equal(got, []string{"web"}) {
		t.Fatalf("hosts %v", got)
	}
	if got := accountActions(t); got[len(got)-1] != "sign-out:ok" {
		t.Fatalf("log %v", got)
	}
}

// The sign-in window shows the page and the code while it waits, and
// the account window who signed in, its sync and its log.
func TestAccountWindows(t *testing.T) {
	saved := deviceName
	deviceName = func() string { return "my-laptop" }
	t.Cleanup(func() { deviceName = saved })
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			prov := newFakeProvider()
			prov.pending = true
			d := newAccountDevice(t, prov, &memRemote{}, map[string]cloudsync.Remote{"github:42": &memRemote{}})
			d.p.SetLocale(lang)
			d.addHost("h1", "web")
			d.use(t)
			d.a.openAccountLogin()
			tt := d.p.show(t, WindowAccountLogin)
			d.p.until(t, "the code", func() bool { tt.Frame(); return d.a.account.login != nil && d.a.account.login.phase == loginWaiting })
			tt.Frame()
			for _, s := range []string{locales.AccountLoginStep1(), locales.AccountLoginStep2(), "WDJB-MJHT", "https://github.com/login/device", locales.AccountOpenAndCopy()} {
				if !tt.HasText(s) {
					t.Errorf("sign-in window: no %q; texts %q", s, tt.Texts())
				}
			}
			saveShot(t, tt, "account-login-"+lang+".png")

			prov.set(func() { prov.pending = false })
			d.p.until(t, "the sign-in", func() bool { return d.a.settings.Account.SignedIn() && d.a.account.sync.loaded })
			d.syncAccount(t) // the default password, the email address
			at := d.p.show(t, WindowAccount)
			at.Frame()
			last := lastAccountEvent(t)
			d.a.account.view.open = map[time.Time]bool{last.At: true}
			at.Frame()
			for _, s := range []string{"1. " + locales.AccountStepRead(), "2. " + locales.AccountStepCreate()} {
				if !at.HasText(s) {
					t.Errorf("the open sync shows no %q; texts %q", s, at.Texts())
				}
			}
			for _, s := range []string{"Octo Cat", "@octo", "oc***@example.com", locales.AccountSignedInGitHub(), locales.AccountSyncTitle(), locales.AccountLog(), locales.AccountActSync(), locales.AccountSignOut()} {
				if !at.HasText(s) {
					t.Errorf("account window: no %q; texts %q", s, at.Texts())
				}
			}
			if at.HasText("octocat@example.com") {
				t.Error("the whole address is shown")
			}
			saveShot(t, at, "account-"+lang+".png")
		})
	}
}

// The account's versions: each sync's, previewed against this computer
// with what restoring would remove, then restored here and on the gist,
// this computer's data backed up first; or only what is missing added.
func TestAccountVersionsRestore(t *testing.T) {
	gist := &memRemote{}
	d := newAccountDevice(t, newFakeProvider(), &memRemote{}, map[string]cloudsync.Remote{"github:42": gist})
	d.addHost("h1", "web")
	d.addHost("h2", "db")
	d.signIn(t, "")
	d.syncAccount(t)
	d.a.hosts.SoftDelete("h2", time.Now())
	d.addHost("h3", "cache")
	d.syncAccount(t)

	a := d.a
	t2 := a.accountSync()
	a.openVersionsOn(t2, WindowMain)
	if !a.windowOpen(WindowAccountBackups) || a.windowOpen(WindowBackups) {
		t.Fatal("the account's versions window is not the one open")
	}
	st := a.account.sync.backups
	d.p.until(t, "the versions", func() bool { return !st.listing })
	var oldest versionEntry
	for _, e := range st.entries {
		if e.remote && e.Counted && e.Counts.Live == 2 && (oldest.ID == "" || e.At.Before(oldest.At)) {
			oldest = e
		}
	}
	if oldest.ID == "" {
		t.Fatalf("no first version among %+v", st.entries)
	}
	a.selectVersion(st, oldest, "")
	d.p.until(t, "the preview", func() bool { return !st.busy })
	if st.err != nil || st.preview == nil {
		t.Fatalf("preview: %v", st.err)
	}
	changes := cloudsync.Compare(a.localPayload(), st.preview.payload)
	if !slices.Contains(changes.Hosts.Removed, "cache") || !slices.Contains(changes.Hosts.Added, "db") {
		t.Fatalf("diff %+v", changes.Hosts)
	}
	wt := d.p.show(t, WindowAccountBackups)
	wt.Frame()
	if _, removed, _ := changes.Total(); !wt.HasText(locales.BackupPreviewLoses(removed)) {
		t.Errorf("no warning of what restoring removes; texts %q", wt.Texts())
	}
	saveShot(t, wt, "account-versions.png")

	a.restoreVersion(st)
	d.p.until(t, "the restore", func() bool { return !a.account.sync.running && !st.busy && !st.listing })
	if st.err != nil {
		t.Fatal(st.err)
	}
	if got := hostNames(a); !slices.Contains(got, "db") || a.hosts.Host("h3") != nil || a.hosts.Host("h2").Deleted() {
		t.Fatalf("restored %v", got)
	}
	if backups, _ := config.ListBackups(); len(backups) == 0 || backups[0].Reason != cloudsync.ReasonPreRestore {
		t.Fatalf("no backup before the restore: %+v", backups)
	}
	p, err := cloudsync.OpenSnapshot("octocat@example.com", gist.data)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.Counts().Live; n != 2 {
		t.Fatalf("the gist has %d connections", n)
	}
	if a.account.sync.restorePaused {
		t.Fatal("automatic sync stays paused after a restore that reached the gist")
	}
}
