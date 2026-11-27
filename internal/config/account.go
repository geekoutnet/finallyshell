package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AccountSettings are the account the user signed in with (Account
// window), and its sync: a gist of the account's own, apart from the
// Sync page's. They stay on this device. The account's tokens and sync
// password are in the system's credential store.
type AccountSettings struct {
	// Provider is the service signed in with (account.Provider.ID):
	// "github"; "" when signed out.
	Provider string `json:"provider,omitempty"`
	// Who signed in: the provider's ID of the user, the user name, the
	// display name, the primary address and the user's page.
	UserID string `json:"userId,omitempty"`
	Login  string `json:"login,omitempty"`
	Name   string `json:"name,omitempty"`
	Email  string `json:"email,omitempty"`
	// Emails are the account's verified addresses, one a line (a string,
	// so that Settings stays comparable), which a device tries as the
	// sync password when its own no longer opens the gist: EmailList.
	Emails string `json:"emails,omitempty"`
	URL    string `json:"url,omitempty"`
	// Expired: the sign-in no longer works (revoked, or not used for
	// months); the user signs in again. The rest is kept for it.
	Expired bool `json:"expired,omitempty"`

	// GistID is the account's sync gist, "" to find or make it.
	GistID string `json:"gistId,omitempty"`
	// CustomPassword: the account's sync password is one the user set,
	// not the account's email address, the default.
	CustomPassword bool `json:"customPassword,omitempty"`
	// CustomSync uses Passwords, Auto and Interval, as the Sync page's
	// (its own, not the Sync page's); without it the account syncs as
	// DefaultAccountSync says.
	CustomSync bool `json:"customSync,omitempty"`
	Passwords  bool `json:"passwords,omitempty"`
	Auto       bool `json:"auto,omitempty"`
	Interval   int  `json:"interval,omitempty"`
}

// DefaultAccountSync is how the account syncs without CustomSync:
// automatically, every DefaultSyncInterval minutes, saved passwords
// included.
var DefaultAccountSync = AccountSettings{Auto: true, Passwords: true, Interval: DefaultSyncInterval}

// EmailList is Emails, one address an item.
func (s AccountSettings) EmailList() []string {
	if s.Emails == "" {
		return nil
	}
	return strings.Split(s.Emails, "\n")
}

// SignedIn says whether an account is signed in, its sign-in working or
// not.
func (s AccountSettings) SignedIn() bool { return s.Provider != "" && s.UserID != "" }

// SyncAccount is the provider of the account's sync: SyncSettings of it
// are those AccountSettings.SyncSettings makes, never saved.
const SyncAccount SyncProvider = "account"

// SyncSettings are the account's sync as the sync driver takes them.
func (s AccountSettings) SyncSettings() SyncSettings {
	if !s.SignedIn() {
		return SyncSettings{}
	}
	opts := s
	if !s.CustomSync {
		opts = DefaultAccountSync
	}
	return SyncSettings{
		Provider: SyncAccount, Account: s.Provider + ":" + s.UserID, GistID: s.GistID,
		Passwords: opts.Passwords, Auto: opts.Auto, Interval: opts.Interval,
	}
}

// AccountSyncStateName is the account's sync-state.json.
const AccountSyncStateName = "account-sync-state.json"

// AccountEvent is a line of the account's log, account-log.json: a
// sign-in, a sign-out, a renewed or expired sign-in, or a sync and what
// it did.
type AccountEvent struct {
	At       time.Time `json:"at"`
	Provider string    `json:"provider"`
	Login    string    `json:"login,omitempty"`
	Device   string    `json:"device,omitempty"`
	// Action is one of the Account* actions; Reason, of a sync, is
	// cloudsync's (auto, manual, restore).
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	// Result is "ok", "failed", "paused" (the deletion guard stopped the
	// sync) or "history-warning".
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`

	// What a sync did: the steps it went through, the items it added,
	// removed and changed here and on the server, and the conflicts.
	Steps     []string `json:"steps,omitempty"`
	Local     [3]int   `json:"local"`
	Server    [3]int   `json:"server"`
	Conflicts int      `json:"conflicts,omitempty"`
}

// The actions of AccountEvent.
const (
	AccountSignIn   = "sign-in"
	AccountSignOut  = "sign-out"
	AccountRefresh  = "refresh"
	AccountExpired  = "expired"
	AccountPassword = "password" // changed the sync password
	AccountChoice   = "choice"   // chose how a first or stale sync goes
	AccountSync     = "sync"
	AccountReplace  = "replace"
)

// The steps of a sync, in AccountEvent.Steps, in the order they run.
const (
	StepRefresh  = "refresh"  // renewed the sign-in first
	StepRead     = "read"     // read the gist
	StepCreate   = "create"   // made the gist, none being there
	StepMerge    = "merge"    // merged it with this device's
	StepUpload   = "upload"   // wrote the result to the gist
	StepBackup   = "backup"   // saved a local backup before applying
	StepApply    = "apply"    // applied the result here
	StepUpToDate = "uptodate" // nothing to do
	StepReseal   = "reseal"   // sealed the gist's file with the new password
	// StepAutoPassword: the gist opened with the account's email address,
	// another device's choice, which this one now uses.
	StepAutoPassword = "autopassword"
)

const accountLogLimit = 200

var accountLogMu sync.Mutex

func accountLogPath() string { return filepath.Join(Dir(), "account-log.json") }

// ReadAccountLog reads account-log.json, oldest first.
func ReadAccountLog() ([]AccountEvent, error) {
	accountLogMu.Lock()
	defer accountLogMu.Unlock()
	return readAccountLog()
}

func readAccountLog() ([]AccountEvent, error) {
	data, err := os.ReadFile(accountLogPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []AccountEvent
	err = json.Unmarshal(data, &events)
	return events, err
}

// AppendAccountEvent adds an event to account-log.json, which keeps the
// last 200.
func AppendAccountEvent(event AccountEvent) error {
	accountLogMu.Lock()
	defer accountLogMu.Unlock()
	events, err := readAccountLog()
	if err != nil {
		events = nil // a damaged log starts again
	}
	events = append(events, event)
	data, err := json.MarshalIndent(events[max(0, len(events)-accountLogLimit):], "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(accountLogPath(), append(data, '\n'))
}

// ClearAccountLog deletes account-log.json.
func ClearAccountLog() error {
	accountLogMu.Lock()
	defer accountLogMu.Unlock()
	err := os.Remove(accountLogPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
