package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"finallyshell/internal/browserkernel"
	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// desktopKernelZip is a Chrome for Testing download for this system.
func desktopKernelZip(t *testing.T, version string) []byte {
	t.Helper()
	exe := map[string]string{
		"windows": "chrome-win64/chrome.exe",
		"darwin":  "chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
	}[runtime.GOOS]
	if exe == "" {
		exe = "chrome-linux64/chrome"
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: exe, Method: zip.Deflate}
	header.SetMode(0o755)
	file, _ := w.CreateHeader(header)
	file.Write([]byte("chrome " + version))
	w.Close()
	return buf.Bytes()
}

type desktopKernelServer struct {
	*httptest.Server
	mu       sync.Mutex
	version  string
	archives map[string][]byte
}

func (s *desktopKernelServer) publish(t *testing.T, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = version
	s.archives["/chrome-"+version+".zip"] = desktopKernelZip(t, version)
}

func newDesktopKernelServer(t *testing.T) *desktopKernelServer {
	s := &desktopKernelServer{archives: map[string][]byte{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/index.json" {
			var downloads []map[string]string
			for _, platform := range []string{"win64", "win32", "mac-arm64", "mac-x64", "linux64", "linux-arm64"} {
				downloads = append(downloads, map[string]string{"platform": platform, "url": s.URL + "/chrome-" + s.version + ".zip"})
			}
			json.NewEncoder(w).Encode(map[string]any{"channels": map[string]any{"Stable": map[string]any{"version": s.version, "downloads": map[string]any{"chrome": downloads}}}})
			return
		}
		if data, ok := s.archives[r.URL.Path]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestDesktopToolsBrowserKernels(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	kernels := newDesktopKernelServer(t)
	kernels.publish(t, "1.0")
	m := a.kernelManager()
	m.ChromeIndex = kernels.URL + "/index.json"
	a.browserKernels.client = kernels.Client()

	server := sshtest.Start(t)
	session.client, session.Host.NoMonitor = server.Dial(t), true
	window := a.desktopOpen(session, appBrowser, nil)
	window.x, window.y, window.h = 130, 10, 800
	state := window.state.(*desktopBrowserState)
	platform.until(t, "kernels read", func() bool { return a.browserKernels.loaded && !state.discovering })
	state.choices = nil // only the kernels, whatever this computer has
	tester.Frame()
	if !tester.HasText(locales.DesktopBrowserKernels()) || !tester.HasText(locales.DesktopKernelNotInstalled()+" · "+locales.DesktopKernelChromeNote()) {
		t.Fatalf("kernels card missing: %q", tester.Texts())
	}

	// Download: the first Download is Chrome's.
	systemClick(t, tester, locales.Download())
	platform.until(t, "Chrome downloaded", func() bool {
		_, ok := a.browserKernels.installed[browserkernel.Chrome]
		return ok && a.browserKernels.jobs[browserkernel.Chrome] == nil
	})
	installed := a.browserKernels.installed[browserkernel.Chrome]
	if data, err := os.ReadFile(installed.Path()); err != nil || string(data) != "chrome 1.0" {
		t.Fatalf("kernel executable %q, %v", data, err)
	}
	if filepath.Dir(installed.Dir) != filepath.Join(config.Dir(), "browser-kernels") {
		t.Errorf("kernel outside the config folder: %s", installed.Dir)
	}
	tester.Frame()
	if state.choice.kernel != browserkernel.Chrome || !tester.HasText(locales.DesktopKernelUpToDate()+" · "+formatSize(installed.Size)+" · "+locales.DesktopKernelChromeNote()) {
		t.Fatalf("choice %+v; visible %q", state.choice, tester.Texts())
	}

	// Picking it saves it as this host's browser, on this device only.
	a.desktopBrowserPick(window, state, state.choice)
	if got := config.LoadSettings().Desktop.Hosts[session.Host.ID].Browser; got != "kernel:chrome" {
		t.Errorf("saved browser = %q", got)
	}
	other := demoSession(a, "web-2", "10.0.0.12")
	if a.desktopHost(other).Browser != "" {
		t.Error("another host got the browser")
	}

	// Running it holds the kernel: no update or deletion under it.
	process := &desktopTestBrowserProcess{done: make(chan struct{})}
	var launched *desktopBrowserRun
	state.launch = func(run *desktopBrowserRun, _ string) (desktopBrowserProcess, error) {
		launched = run
		return process, nil
	}
	systemClick(t, tester, locales.DesktopBrowserStart())
	platform.until(t, "browser running", func() bool { return state.run != nil })
	if launched.choice.path != installed.Path() || launched.choice.firefox || !m.InUse(browserkernel.Chrome) {
		t.Fatalf("launched %+v, in use %v", launched.choice, m.InUse(browserkernel.Chrome))
	}
	if item, ok := tester.AccessByRole(ui.RoleButton, locales.Delete()); !ok || !item.Disabled {
		t.Error("Delete enabled while the kernel runs")
	}
	if err := m.Remove(browserkernel.Chrome); !errors.Is(err, browserkernel.ErrInUse) {
		t.Errorf("removed while running: %v", err)
	}
	if item, ok := tester.AccessByRole(ui.RoleButton, locales.DesktopBrowserResetProfile()); !ok || !item.Disabled {
		t.Error("Reset browser data enabled while running")
	}
	systemClick(t, tester, locales.DesktopBrowserStop())
	platform.until(t, "browser stopped", func() bool { return state.run == nil && state.stop == nil })
	if m.InUse(browserkernel.Chrome) {
		t.Fatal("kernel still held after the browser stopped")
	}

	// A newer release: check, then update in place.
	kernels.publish(t, "2.0")
	systemClick(t, tester, locales.DesktopKernelCheckUpdate())
	platform.until(t, "update found", func() bool { return tester.HasText(locales.DesktopKernelUpdate("2.0")) })
	systemClick(t, tester, locales.DesktopKernelUpdate("2.0"))
	platform.until(t, "Chrome updated", func() bool {
		return a.browserKernels.installed[browserkernel.Chrome].Version == "2.0" && a.browserKernels.jobs[browserkernel.Chrome] == nil
	})
	if data, _ := os.ReadFile(a.browserKernels.installed[browserkernel.Chrome].Path()); string(data) != "chrome 2.0" {
		t.Errorf("after update: %q", data)
	}
	if state.choice.kernel != browserkernel.Chrome {
		t.Errorf("choice after update: %+v", state.choice)
	}

	// A failed download keeps the kernel there is.
	kernels.mu.Lock()
	kernels.version = "3.0" // published nowhere
	kernels.mu.Unlock()
	systemClick(t, tester, locales.DesktopKernelReinstall())
	platform.until(t, "download failed", func() bool { return tester.HasText(locales.DesktopKernelFailed()) })
	if a.browserKernels.installed[browserkernel.Chrome].Version != "2.0" {
		t.Error("failed download removed the kernel")
	}

	// Deleting asks first, then the choice falls back.
	systemClick(t, tester, locales.Delete())
	if !tester.HasText(locales.DesktopKernelRemoveConfirm("Chrome for Testing", formatSize(a.browserKernels.installed[browserkernel.Chrome].Size))) {
		t.Fatalf("no confirmation: %q", tester.Texts())
	}
	systemClick(t, tester, locales.Cancel())
	if _, ok := a.browserKernels.installed[browserkernel.Chrome]; !ok {
		t.Fatal("cancel deleted")
	}
	systemClick(t, tester, locales.Delete())
	systemClick(t, tester, locales.Delete())
	platform.until(t, "Chrome deleted", func() bool {
		_, ok := a.browserKernels.installed[browserkernel.Chrome]
		return !ok && a.browserKernels.jobs[browserkernel.Chrome] == nil
	})
	if _, err := os.Stat(m.Dir(browserkernel.Chrome)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("kernel folder still there: %v", err)
	}
	tester.Frame()
	if state.choice != (desktopBrowserChoice{}) || !tester.HasText(locales.DesktopBrowserNoChoice()) {
		t.Errorf("choice after delete: %+v", state.choice)
	}
	// The host keeps its pick: downloading it again brings it back.
	if got := config.LoadSettings().Desktop.Hosts[session.Host.ID].Browser; got != "kernel:chrome" {
		t.Errorf("saved browser after delete = %q", got)
	}
	kernels.publish(t, "2.0")
	systemClick(t, tester, locales.Download())
	platform.until(t, "Chrome back", func() bool { return state.choice.kernel == browserkernel.Chrome })

	// Reset browser data deletes this host's profile.
	profile := desktopBrowserProfileDir(filepath.Join(config.Dir(), "browser-profiles"), session.Host.ID)
	os.MkdirAll(filepath.Join(profile, "Default"), 0o700)
	systemClick(t, tester, locales.DesktopBrowserResetProfile())
	if !tester.HasText(locales.DesktopBrowserResetConfirm()) {
		t.Fatalf("no reset confirmation: %q", tester.Texts())
	}
	systemClick(t, tester, locales.DesktopBrowserResetProfile())
	platform.until(t, "profile reset", func() bool { return tester.HasText(locales.DesktopBrowserResetDone()) })
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("profile still there: %v", err)
	}
}

func TestDesktopToolsBrowserKernelCancel(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	started := make(chan struct{})
	var once sync.Once
	hang := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			w.Write([]byte(`{"channels":{"Stable":{"version":"1.0","downloads":{"chrome":[` +
				`{"platform":"win64","url":"https://` + r.Host + `/c.zip"},{"platform":"win32","url":"https://` + r.Host + `/c.zip"},` +
				`{"platform":"mac-arm64","url":"https://` + r.Host + `/c.zip"},{"platform":"mac-x64","url":"https://` + r.Host + `/c.zip"},` +
				`{"platform":"linux64","url":"https://` + r.Host + `/c.zip"},{"platform":"linux-arm64","url":"https://` + r.Host + `/c.zip"}]}}}}`))
			return
		}
		w.Header().Set("Content-Length", "100000000")
		w.Write(make([]byte, 64<<10))
		w.(http.Flusher).Flush()
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer hang.Close()
	m := a.kernelManager()
	m.ChromeIndex = hang.URL + "/index.json"
	a.browserKernels.client = hang.Client()
	window := a.desktopOpen(session, appBrowser, nil)
	window.x, window.y, window.h = 130, 10, 800
	platform.until(t, "kernels read", func() bool { return a.browserKernels.loaded })
	systemClick(t, tester, locales.Download())
	<-started
	platform.until(t, "progress shown", func() bool {
		job := a.browserKernels.jobs[browserkernel.Chrome]
		return job != nil && job.progress.Phase == browserkernel.Downloading && job.progress.Done > 0
	})
	if !slices.ContainsFunc(tester.Texts(), func(text string) bool {
		return strings.HasPrefix(text, locales.DesktopKernelDownloading("")) && strings.HasSuffix(text, " / "+formatSize(100000000))
	}) {
		t.Errorf("no download progress: %q", tester.Texts())
	}
	systemClick(t, tester, locales.Cancel())
	platform.until(t, "download cancelled", func() bool { return a.browserKernels.jobs[browserkernel.Chrome] == nil })
	tester.Frame()
	if _, ok := a.browserKernels.installed[browserkernel.Chrome]; ok || tester.HasText(locales.DesktopKernelFailed()) {
		t.Errorf("cancel left a kernel or an error: %q", tester.Texts())
	}
	entries, _ := os.ReadDir(m.Root)
	if len(entries) != 0 {
		t.Errorf("cancel left files: %v", entries)
	}
}
