package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/app"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo"
)

// releaseRepo is the GitHub repository whose latest release is the newest
// version. Its builds are not signed, and MyGo's updater installs only
// signed ones, so the app tells of a newer version and links to its
// release page (options_updates.go) rather than updating itself.
const releaseRepo = "geekoutnet/finallyshell"

// updateInterval is how long apart the checks in the background are.
const updateInterval = 24 * time.Hour

// updateChecker reads the latest release of releaseRepo, as the user asks
// or a day apart in the background, unless the user turned that off
// (Options › General). The choice and when it last checked are kept in
// update-check.json in the config folder.
type updateChecker struct {
	mu        sync.Mutex
	saved     updateState
	checking  bool
	upToDate  bool // the user's last check found no newer version
	failed    bool // the last check could not read the release
	available string
	redraw    func()
}

// updateState is update-check.json.
type updateState struct {
	Automatic *bool     `json:"automatic,omitempty"` // nil: on
	LastCheck time.Time `json:"lastCheck"`
}

var updates updateChecker

func updateStatePath() string { return filepath.Join(config.Dir(), "update-check.json") }

// useUpdater reads the kept choice; watchUpdates starts the checks.
func useUpdater() {
	if data, err := os.ReadFile(updateStatePath()); err == nil {
		json.Unmarshal(data, &updates.saved)
	}
}

// watchUpdates draws the windows again when what Updates reports changed,
// and checks in the background, a day after the last check.
func (p *platform) watchUpdates() {
	updates.mu.Lock()
	updates.redraw = p.Redraw
	updates.mu.Unlock()
	if mygo.IsDev() {
		return
	}
	go func() {
		for {
			updates.mu.Lock()
			due := updates.automatic() && time.Since(updates.saved.LastCheck) >= updateInterval
			updates.mu.Unlock()
			if due {
				updates.check(false)
			}
			time.Sleep(time.Hour)
		}
	}()
}

func (u *updateChecker) automatic() bool { return u.saved.Automatic == nil || *u.saved.Automatic }

// save writes update-check.json; u.mu is held.
func (u *updateChecker) save() {
	if data, err := json.Marshal(u.saved); err == nil {
		os.MkdirAll(config.Dir(), 0o700)
		os.WriteFile(updateStatePath(), data, 0o600)
	}
}

// changed redraws the windows; u.mu is not held.
func (u *updateChecker) changed() {
	u.mu.Lock()
	redraw := u.redraw
	u.mu.Unlock()
	if redraw != nil {
		redraw()
	}
}

// check reads the latest release and keeps a version newer than this one.
// asked is a check the user asked for, which says when there is none.
func (u *updateChecker) check(asked bool) {
	u.mu.Lock()
	if u.checking {
		u.mu.Unlock()
		return
	}
	u.checking, u.upToDate, u.failed = true, false, false
	u.mu.Unlock()
	u.changed()

	latest, err := latestRelease(context.Background())

	u.mu.Lock()
	u.checking = false
	switch {
	case err != nil:
		u.failed = asked
	case newerVersion(latest, mygo.App.Version()):
		u.available = latest
	default:
		u.available, u.upToDate = "", asked
	}
	if err == nil {
		u.saved.LastCheck = time.Now()
		u.save()
	}
	u.mu.Unlock()
	u.changed()
}

// latestRelease is the version of releaseRepo's latest release, without
// the tag's "v".
func latestRelease(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+releaseRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "finallyshell")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// newerVersion reports whether version a (MAJOR.MINOR.PATCH) is newer
// than b; one that is not such a version is not.
func newerVersion(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	s, _, _ = strings.Cut(strings.TrimPrefix(s, "v"), "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func (p *platform) Updates() app.UpdateStatus {
	if mygo.IsDev() {
		return app.UpdateStatus{Blocked: app.UpdatesDevelopment}
	}
	updates.mu.Lock()
	defer updates.mu.Unlock()
	return app.UpdateStatus{
		Version:   mygo.App.Version(),
		Automatic: updates.automatic(),
		Available: updates.available,
		Checking:  updates.checking,
		UpToDate:  updates.upToDate,
		Failed:    updates.failed,
	}
}

func (p *platform) CheckForUpdates() { go updates.check(true) }

func (p *platform) SetAutomaticUpdateChecks(on bool) {
	updates.mu.Lock()
	updates.saved.Automatic = &on
	updates.save()
	updates.mu.Unlock()
}

// setAppMenu sets the macOS menu bar: MyGo's default but its File menu,
// whose Close Window takes Cmd+W before the windows (the main one closes
// the tab with it, a secondary one itself: openWindow in internal/app),
// with "Check for Updates…" after About, whose result Options › General
// shows. The item says it in the language the app shows when it is made,
// so main sets the menu again when the language changes.
func setAppMenu() {
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			{Label: locales.CheckForUpdates(), Click: func(*mygo.MenuItem, *mygo.Window) { go updates.check(true) }},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu}, {Role: mygo.RoleViewMenu}, {Role: mygo.RoleWindowMenu},
	}))
}
