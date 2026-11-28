// Package browserkernel keeps the browsers the desktop's server browser
// can run, downloaded on request into a folder of their own instead of
// bundled with the app: Chrome for Testing, Firefox and Camoufox. Each
// kernel is one folder, Root/<kind>, holding kernel.json and the unpacked
// browser in app/. Installing unpacks into a temporary folder next to it
// and swaps it in whole, so a kernel is either complete or absent.
package browserkernel

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kind is a kernel: which browser it is.
type Kind string

const (
	Chrome   Kind = "chrome"
	Firefox  Kind = "firefox"
	Camoufox Kind = "camoufox"
)

// Kinds are the kernels the manager can download, in the order shown.
var Kinds = []Kind{Chrome, Firefox, Camoufox}

// Valid reports whether k is one of Kinds.
func (k Kind) Valid() bool { return slices.Contains(Kinds, k) }

// Name is the kernel's product name.
func (k Kind) Name() string {
	switch k {
	case Chrome:
		return "Chrome for Testing"
	case Firefox:
		return "Firefox"
	case Camoufox:
		return "Camoufox"
	}
	return string(k)
}

// FirefoxBased reports whether the kernel takes Firefox's command line and
// reads its settings from the profile's user.js.
func (k Kind) FirefoxBased() bool { return k == Firefox || k == Camoufox }

var (
	ErrUnsupported  = errors.New("no build of this browser for this system")
	ErrNotInstalled = errors.New("browser kernel not installed")
	ErrDamaged      = errors.New("browser kernel damaged")
	ErrBusy         = errors.New("browser kernel is being installed or removed")
	ErrInUse        = errors.New("browser kernel is running")
	ErrChecksum     = errors.New("download does not match its checksum")
	ErrStalled      = errors.New("download stalled")
)

// Release is a kernel's download.
type Release struct {
	Kind    Kind
	Version string
	URL     string
	// Size is the download's size in bytes: 0 when the source does not
	// say.
	Size int64
	// SHA256 is the download's checksum in hex: "" when the source
	// publishes none (Chrome for Testing).
	SHA256 string
	// Format is how the download is unpacked: "zip", "tar.xz", "exe"
	// (Firefox's Windows installer, run with /ExtractDir) or "dmg".
	Format string
}

// Installed is a downloaded kernel, from its kernel.json.
type Installed struct {
	Kind        Kind      `json:"kind"`
	Version     string    `json:"version"`
	Platform    string    `json:"platform"`
	Exe         string    `json:"exe"`
	Size        int64     `json:"size"`
	InstalledAt time.Time `json:"installedAt"`
	// Dir is the kernel's folder.
	Dir string `json:"-"`
}

// Path is the kernel's executable.
func (i Installed) Path() string {
	return filepath.Join(i.Dir, "app", filepath.FromSlash(i.Exe))
}

// Phase is what an installation is doing.
type Phase int

const (
	Resolving Phase = iota
	Downloading
	Extracting
)

// Progress is an installation's progress: Done of Total bytes, Total 0
// when unknown.
type Progress struct {
	Phase       Phase
	Done, Total int64
}

const manifestName = "kernel.json"

// Manager downloads, lists and removes the kernels in Root. One Manager
// serves the whole app: it refuses to install or remove a kernel that is
// being installed, removed or run.
type Manager struct {
	Root         string
	GOOS, GOARCH string
	// The sources, replaced in tests.
	ChromeIndex, FirefoxVersions, FirefoxReleases, CamoufoxLatest string
	// run runs an unpacking tool: Firefox's Windows installer, hdiutil
	// and ditto on macOS.
	run func(ctx context.Context, name string, args ...string) error

	mu    sync.Mutex
	busy  map[Kind]bool
	inUse map[Kind]int
}

// New returns a manager for root, for this system.
func New(root string) *Manager {
	return &Manager{
		Root: root, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		ChromeIndex:     "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json",
		FirefoxVersions: "https://product-details.mozilla.org/1.0/firefox_versions.json",
		FirefoxReleases: "https://download-installer.cdn.mozilla.net/pub/firefox/releases",
		CamoufoxLatest:  "https://api.github.com/repos/daijro/camoufox/releases/latest",
		run:             runTool,
		busy:            make(map[Kind]bool),
		inUse:           make(map[Kind]int),
	}
}

// Dir is the kernel's folder.
func (m *Manager) Dir(k Kind) string { return filepath.Join(m.Root, string(k)) }

// Installed reads the kernel's kernel.json: ErrNotInstalled when there is
// none, ErrDamaged, with what was read, when it is unreadable or its
// executable is gone.
func (m *Manager) Installed(k Kind) (Installed, error) {
	if !k.Valid() {
		return Installed{}, ErrNotInstalled
	}
	dir := m.Dir(k)
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, fs.ErrNotExist) {
		if _, statErr := os.Stat(dir); statErr == nil {
			return Installed{Kind: k, Dir: dir}, ErrDamaged
		}
		return Installed{}, ErrNotInstalled
	}
	installed := Installed{Kind: k, Dir: dir}
	if err != nil || json.Unmarshal(data, &installed) != nil {
		return Installed{Kind: k, Dir: dir}, ErrDamaged
	}
	installed.Kind, installed.Dir = k, dir
	if !filepath.IsLocal(filepath.FromSlash(installed.Exe)) || !executable(installed.Path()) {
		return installed, ErrDamaged
	}
	return installed, nil
}

// Acquire marks the kernel as running until release is called, so that
// it is not removed or replaced under the browser: ErrBusy while it is
// being installed or removed.
func (m *Manager) Acquire(k Kind) (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy[k] {
		return nil, ErrBusy
	}
	m.inUse[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.inUse[k]--
			m.mu.Unlock()
		})
	}, nil
}

// InUse reports whether a browser runs the kernel.
func (m *Manager) InUse(k Kind) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inUse[k] > 0
}

// begin claims the kernel for an installation or removal.
func (m *Manager) begin(k Kind) (end func(), err error) {
	if !k.Valid() {
		return nil, ErrUnsupported
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.busy[k]:
		return nil, ErrBusy
	case m.inUse[k] > 0:
		return nil, ErrInUse
	}
	m.busy[k] = true
	return func() {
		m.mu.Lock()
		delete(m.busy, k)
		m.mu.Unlock()
	}, nil
}

// Remove deletes the kernel: moved aside first, so that a kernel some
// other program still holds open stays whole.
func (m *Manager) Remove(k Kind) error {
	end, err := m.begin(k)
	if err != nil {
		return err
	}
	defer end()
	dir := m.Dir(k)
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	trash, err := m.trashName(k)
	if err != nil {
		return err
	}
	if err := rename(dir, trash); err != nil {
		return err
	}
	return removeAll(trash)
}

func (m *Manager) trashName(k Kind) (string, error) {
	file, err := os.CreateTemp(m.Root, ".trash-"+string(k)+"-")
	if err != nil {
		return "", err
	}
	name := file.Name()
	file.Close()
	return name, os.Remove(name)
}

// Clean removes what interrupted installations and removals left: moved
// aside kernels, and unpacking folders untouched for a day (younger ones
// may be another instance's download).
func (m *Manager) Clean() {
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		stale := strings.HasPrefix(name, ".trash-")
		if strings.HasPrefix(name, ".tmp-") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				stale = true
			}
		}
		if stale {
			_ = os.RemoveAll(filepath.Join(m.Root, name))
		}
	}
}

func executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}

func client(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return http.DefaultClient
}
