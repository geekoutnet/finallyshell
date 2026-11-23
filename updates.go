package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
// version. Its builds are not signed, which MyGo's updater requires, so
// the app updates itself here: it downloads the setup program of the
// latest release, checks it against the release's SHA256SUMS, and runs it
// silently once it has quit (selfupdate_windows.go). Where it cannot, it
// links to the release page.
const releaseRepo = "geekoutnet/finallyshell"

// updateMirrors serve releaseRepo's releases: <mirror>/latest is the
// latest one, with each file's download URL on the same host, and
// <mirror>/v<version>/<file> a file of a release. All are asked at once,
// and the first to answer serves the update; GitHub itself only when none
// does.
var updateMirrors = []string{
	"https://gh-raw.966788.xyz/finallyshell-release",
	"https://gh-raw.988669.xyz/finallyshell-release",
	"https://mirrors.966788.xyz/finallyshell-release",
	"https://mirrors.988669.xyz/finallyshell-release",
	"https://gh-raw.s03.qzz.io/finallyshell-release",
	"https://gh-raw.s04.qzz.io/finallyshell-release",
	"https://gh-raw.s05.qzz.io/finallyshell-release",
	"https://gh-raw.s06.qzz.io/finallyshell-release",
	"https://gh-raw.s07.qzz.io/finallyshell-release",
}

// githubAPI and githubDownloads are GitHub's, variables for the tests.
var (
	githubAPI       = "https://api.github.com"
	githubDownloads = "https://github.com"
)

// updateInterval is how long apart the checks in the background are.
const updateInterval = 24 * time.Hour

// updateChecker reads the latest release, as the user asks or a day apart
// in the background, unless the user turned that off (Options ›
// General), and installs it when the user asks. The choice and when it
// last checked are kept in update-check.json in the config folder.
type updateChecker struct {
	mu          sync.Mutex
	saved       updateState
	checking    bool
	upToDate    bool // the user's last check found no newer version
	failed      bool // the last check could not read the release
	latest      *release
	downloading bool
	progress    float32 // of the download, 0 to 1
	installErr  bool    // the last download or install failed
	installing  bool    // the setup program waits for the app to quit
	redraw      func()
}

// updateState is update-check.json.
type updateState struct {
	Automatic *bool     `json:"automatic,omitempty"` // nil: on
	LastCheck time.Time `json:"lastCheck"`
}

// release is a release that a mirror or GitHub described.
type release struct {
	Version string            // without the tag's "v"
	Assets  map[string]string // download URL by file name
	Source  string            // the mirror that described it; "" for GitHub
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

// check reads the latest release and keeps it when it is newer than this
// version. asked is a check the user asked for, which says when there is
// none.
func (u *updateChecker) check(asked bool) {
	u.mu.Lock()
	if u.checking || u.downloading || u.installing {
		u.mu.Unlock()
		return
	}
	u.checking, u.upToDate, u.failed, u.installErr = true, false, false, false
	u.mu.Unlock()
	u.changed()

	rel, err := latestRelease(context.Background())

	u.mu.Lock()
	u.checking = false
	switch {
	case err != nil:
		u.failed = asked
	case newerVersion(rel.Version, mygo.App.Version()):
		u.latest = rel
	default:
		u.latest, u.upToDate = nil, asked
	}
	if err == nil {
		u.saved.LastCheck = time.Now()
		u.save()
	}
	u.mu.Unlock()
	u.changed()
}

// install downloads the setup program of the newer release found, checks
// it, starts what installs it once the app has quit, and quits.
func (u *updateChecker) install(quit func()) {
	u.mu.Lock()
	rel := u.latest
	if rel == nil || u.downloading || u.installing || !canInstallUpdate() {
		u.mu.Unlock()
		return
	}
	u.downloading, u.progress, u.installErr = true, 0, false
	u.mu.Unlock()
	u.changed()

	setup, err := downloadUpdate(context.Background(), rel, installerName(rel.Version), func(p float32) {
		u.mu.Lock()
		u.progress = p
		u.mu.Unlock()
		u.changed()
	})
	if err == nil {
		err = startInstall(setup)
	}

	u.mu.Lock()
	u.downloading = false
	u.installErr = err != nil
	u.installing = err == nil
	u.mu.Unlock()
	u.changed()
	if err == nil {
		quit()
	}
}

// latestRelease asks every mirror at once for the latest release and
// returns the first answer that describes one, or GitHub's when none
// does.
func latestRelease(ctx context.Context) (*release, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	type answer struct {
		rel *release
		err error
	}
	answers := make(chan answer, len(updateMirrors))
	for _, m := range updateMirrors {
		go func() {
			rel, err := mirrorRelease(ctx, m)
			answers <- answer{rel, err}
		}()
	}
	var errs []error
	for range updateMirrors {
		a := <-answers
		if a.err == nil {
			return a.rel, nil
		}
		errs = append(errs, a.err)
	}
	rel, err := githubRelease(ctx)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	return rel, nil
}

// mirrorRelease reads <mirror>/latest.
func mirrorRelease(ctx context.Context, mirror string) (*release, error) {
	var r struct {
		Tag    string `json:"tag"`
		Assets []struct {
			Name     string `json:"name"`
			Download string `json:"download"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, mirror+"/latest", &r); err != nil {
		return nil, err
	}
	rel := &release{Version: strings.TrimPrefix(r.Tag, "v"), Assets: map[string]string{}, Source: mirror}
	for _, a := range r.Assets {
		if a.Name != "" && strings.HasPrefix(a.Download, "https://") {
			rel.Assets[a.Name] = a.Download
		}
	}
	if _, ok := parseVersion(rel.Version); !ok || len(rel.Assets) == 0 {
		return nil, fmt.Errorf("%s: no release in the answer", mirror)
	}
	return rel, nil
}

// githubRelease reads releaseRepo's latest release from GitHub's API.
func githubRelease(ctx context.Context) (*release, error) {
	var r struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, githubAPI+"/repos/"+releaseRepo+"/releases/latest", &r); err != nil {
		return nil, err
	}
	rel := &release{Version: strings.TrimPrefix(r.Tag, "v"), Assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name] = a.URL
	}
	if _, ok := parseVersion(rel.Version); !ok {
		return nil, fmt.Errorf("GitHub: release %q is not a version", r.Tag)
	}
	return rel, nil
}

func getJSON(ctx context.Context, url string, v any) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("User-Agent", "finallyshell")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

// assetURLs are where name of rel may be downloaded from, in the order to
// try: where rel says, the other mirrors, then GitHub.
func assetURLs(rel *release, name string) []string {
	var urls []string
	if u := rel.Assets[name]; u != "" {
		urls = append(urls, u)
	}
	for _, m := range updateMirrors {
		if m != rel.Source {
			urls = append(urls, m+"/v"+rel.Version+"/"+name)
		}
	}
	return append(urls, githubDownloads+"/"+releaseRepo+"/releases/download/v"+rel.Version+"/"+name)
}

// downloadUpdate downloads name of rel into the temporary folder, from the
// first of its URLs that serves it whole, and checks it against rel's
// SHA256SUMS. progress is told the share downloaded.
func downloadUpdate(ctx context.Context, rel *release, name string, progress func(float32)) (string, error) {
	if name == "" {
		return "", errors.ErrUnsupported
	}
	sums, err := fetchSums(ctx, rel)
	if err != nil {
		return "", err
	}
	want, ok := sums[name]
	if !ok {
		return "", fmt.Errorf("SHA256SUMS has no %s", name)
	}
	dir := filepath.Join(os.TempDir(), "finallyshell-update")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	var errs []error
	for _, url := range assetURLs(rel, name) {
		err := downloadFile(ctx, url, path, want, progress)
		if err == nil {
			return path, nil
		}
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}

// fetchSums reads rel's SHA256SUMS: the checksum by file name.
func fetchSums(ctx context.Context, rel *release) (map[string]string, error) {
	var errs []error
	for _, url := range assetURLs(rel, "SHA256SUMS") {
		sums, err := func() (map[string]string, error) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			resp, err := get(ctx, url)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			return parseSums(io.LimitReader(resp.Body, 1<<20))
		}()
		if err == nil {
			return sums, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// parseSums reads sha256sum's output: "<hex>  <name>" lines.
func parseSums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == sha256.Size*2 {
			sums[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	if len(sums) == 0 {
		return nil, errors.New("SHA256SUMS lists no file")
	}
	return sums, sc.Err()
}

// downloadFile downloads url into path, keeping it only when its SHA-256
// is want.
func downloadFile(ctx context.Context, url, path, want string, progress func(float32)) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	pw := &progressWriter{total: resp.ContentLength, report: progress}
	_, err = io.Copy(io.MultiWriter(f, h, pw), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("%s: the checksum is not SHA256SUMS's", url)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(path)
	return os.Rename(tmp, path)
}

// progressWriter tells report the share of total written, at most a
// hundred times.
type progressWriter struct {
	total, n int64
	last     int
	report   func(float32)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	if w.total > 0 && w.report != nil {
		if pct := int(w.n * 100 / w.total); pct != w.last {
			w.last = pct
			w.report(float32(w.n) / float32(w.total))
		}
	}
	return len(p), nil
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
	st := app.UpdateStatus{
		Version:     mygo.App.Version(),
		Automatic:   updates.automatic(),
		Checking:    updates.checking,
		UpToDate:    updates.upToDate,
		Failed:      updates.failed,
		Downloading: updates.downloading,
		Progress:    updates.progress,
		InstallErr:  updates.installErr,
		Installing:  updates.installing,
	}
	if updates.latest != nil {
		st.Available = updates.latest.Version
		st.CanInstall = canInstallUpdate()
	}
	return st
}

func (p *platform) CheckForUpdates() { go updates.check(true) }

func (p *platform) InstallUpdate() { go updates.install(func() { p.Post(p.Quit) }) }

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
