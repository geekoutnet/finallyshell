package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"finallyshell/internal/account"
	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/locales"
)

// TestAccountDesignShots draws the account's screens in both languages,
// as the app shows them, for review (SHOT_DIR): the left panel's bar
// signed out and signed in, the bar's card, the sign-in window waiting
// and failed, and the account window with its log open.
func TestAccountDesignShots(t *testing.T) {
	saved := deviceName
	deviceName = func() string { return "my-laptop" }
	t.Cleanup(func() { deviceName = saved })
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			// The main window, signed out: the bar offers to sign in.
			a, p, tt, _ := readmeApp(t, lang)
			if !tt.HasText(locales.AccountSignInBar()) {
				t.Errorf("no sign-in bar; texts %q", tt.Texts())
			}
			saveShot(t, tt, "design-bar-out-"+lang+".png")

			// Signed in, synced: the bar, then its card.
			prov := newFakeProvider()
			acc := &memRemote{}
			a.newProvider = func(string) account.Provider { return prov }
			a.newRemote = func(config.SyncSettings, string) (cloudsync.Remote, error) { return acc, nil }
			tok, _ := prov.Poll(context.Background(), account.DeviceCode{})
			if err := a.signIn(prov, tok, prov.profile); err != nil {
				t.Fatal(err)
			}
			p.until(t, "the account", func() bool { return a.account.sync.loaded })
			p.until(t, "the store", storeSettled(a, p))
			a.syncAccountNow(false, time.Now())
			p.until(t, "the sync", func() bool { return !a.account.sync.running })
			a.closeWindow(WindowAccount)
			tt.Frame()
			saveShot(t, tt, "design-bar-in-"+lang+".png")
			a.account.view.cardOpen = true
			tt.Frame()
			saveShot(t, tt, "design-card-"+lang+".png")
			a.account.view.cardOpen = false

			// Failing: the bar says so.
			a.account.sync.err = errors.New("dial tcp: i/o timeout")
			tt.Frame()
			saveShot(t, tt, "design-bar-failed-"+lang+".png")
			a.account.sync.err = nil

			// The account window, the last sync's steps open.
			a.openAccountWindow()
			at := p.show(t, WindowAccount)
			at.Frame()
			events, _ := config.ReadAccountLog()
			if len(events) > 0 {
				a.account.view.open = map[time.Time]bool{events[len(events)-1].At: true}
			}
			at.Frame()
			saveShot(t, at, "design-window-"+lang+".png")

			// Changing the password.
			a.account.view.pwOpen = true
			a.account.view.pwNew, a.account.view.pwConfirm = "correct horse", "correct horse"
			at.Frame()
			saveShot(t, at, "design-password-"+lang+".png")
			a.account.view.pwOpen = false
			a.closeWindow(WindowAccount)

			// An old computer's first sync: the choice, and the bar
			// waiting for it.
			a.account.sync.choice = &cloudsync.Choice{Local: 10, Remote: 50, Merged: 53, OnlyLocal: 3, OnlyRemote: 43, Differ: 4, Same: 3,
				RemoteDevice: "office-pc", RemoteAt: time.Date(2026, 11, 27, 14, 2, 0, 0, time.Local)}
			a.account.sync.state.At = time.Time{}
			tt.Frame()
			saveShot(t, tt, "design-bar-choice-"+lang+".png")
			a.openSyncChoice(a.accountSync())
			ct := p.show(t, WindowAccountChoice)
			ct.Frame()
			saveShot(t, ct, "design-choice-"+lang+".png")
			a.closeWindow(WindowAccountChoice)
			a.account.sync.choice = nil

			// The sign-in window: waiting for the code, then declined.
			wait := newFakeProvider()
			wait.pending = true
			a.newProvider = func(string) account.Provider { return wait }
			a.openAccountLogin()
			lt := p.show(t, WindowAccountLogin)
			p.until(t, "the code", func() bool { lt.Frame(); return a.account.login != nil && a.account.login.phase == loginWaiting })
			lt.Frame()
			saveShot(t, lt, "design-login-"+lang+".png")
			a.account.login.phase, a.account.login.err = loginFailed, account.ErrDenied
			lt.Frame()
			saveShot(t, lt, "design-login-denied-"+lang+".png")
			a.closeWindow(WindowAccountLogin)
		})
	}
}
