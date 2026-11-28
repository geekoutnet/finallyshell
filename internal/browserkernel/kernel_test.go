package browserkernel

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ulikunitz/xz"
)

func init() { testHTTP = true }

type entry struct {
	name, body, link string
	mode             fs.FileMode
}

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.link != "":
			header.SetMode(fs.ModeSymlink | 0o777)
		case strings.HasSuffix(e.name, "/"):
			header.SetMode(fs.ModeDir | 0o755)
		default:
			header.SetMode(e.mode | 0o644)
		}
		file, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if e.link != "" {
			file.Write([]byte(e.link))
		} else {
			file.Write([]byte(e.body))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tarXZOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	compressed, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(compressed)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: int64(e.mode | 0o644), Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, e.link, 0
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	w.Close()
	compressed.Close()
	return buf.Bytes()
}

func sum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// serve serves files by path; a path mapped to nil blocks until the
// request ends.
func serve(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		data, ok := files[r.URL.Path]
		mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, r)
		case data == nil:
			w.Header().Set("Content-Length", "1000000")
			w.Write(make([]byte, 1000))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Write(data)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func testManager(t *testing.T, goos, goarch string) *Manager {
	m := New(filepath.Join(t.TempDir(), "browser-kernels"))
	m.GOOS, m.GOARCH = goos, goarch
	return m
}

func leftovers(t *testing.T, m *Manager) []string {
	t.Helper()
	entries, _ := os.ReadDir(m.Root)
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestLatest(t *testing.T) {
	chromeIndex := `{"channels":{"Stable":{"version":"158.0.8127.88","downloads":{"chrome":[
		{"platform":"win64","url":"https://cft.example/158/win64/chrome-win64.zip"},
		{"platform":"mac-arm64","url":"https://cft.example/158/mac-arm64/chrome-mac-arm64.zip"},
		{"platform":"linux64","url":"https://cft.example/158/linux64/chrome-linux64.zip"}]}}}}`
	sums := strings.Join([]string{
		strings.Repeat("a", 64) + "  win64/en-US/Firefox Setup 161.0.exe",
		strings.Repeat("b", 64) + "  mac/en-US/Firefox 161.0.dmg",
		strings.Repeat("c", 64) + "  linux-x86_64/en-US/firefox-161.0.tar.xz",
		strings.Repeat("d", 64) + "  update/linux-x86_64/en-US/firefox-161.0.tar.xz",
	}, "\n")
	camoufox := `{"tag_name":"v159.0-beta.43","assets":[
		{"name":"camoufox-159.0-beta.43-lin.x86_64.zip","browser_download_url":"https://gh.example/lin.zip","size":10,"digest":"sha256:` + strings.Repeat("e", 64) + `"},
		{"name":"camoufox-159.0-beta.43-win.x86_64.zip","browser_download_url":"https://gh.example/win.zip","size":11,"digest":"sha256:` + strings.Repeat("f", 64) + `"},
		{"name":"camoufox-159.0-beta.43-mac.arm64.zip","browser_download_url":"https://gh.example/mac.zip","size":12}]}`
	server := serve(t, map[string][]byte{
		"/chrome.json": []byte(chromeIndex), "/versions.json": []byte(`{"LATEST_FIREFOX_VERSION":"161.0"}`),
		"/releases/161.0/SHA256SUMS": []byte(sums), "/camoufox.json": []byte(camoufox),
	})
	for _, test := range []struct {
		goos, goarch string
		kind         Kind
		want         Release
	}{
		{"windows", "amd64", Chrome, Release{Kind: Chrome, Version: "158.0.8127.88", URL: "https://cft.example/158/win64/chrome-win64.zip", Format: "zip"}},
		{"darwin", "arm64", Chrome, Release{Kind: Chrome, Version: "158.0.8127.88", URL: "https://cft.example/158/mac-arm64/chrome-mac-arm64.zip", Format: "zip"}},
		{"windows", "amd64", Firefox, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/releases/161.0/win64/en-US/Firefox%20Setup%20161.0.exe", SHA256: strings.Repeat("a", 64), Format: "exe"}},
		{"darwin", "amd64", Firefox, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/releases/161.0/mac/en-US/Firefox%20161.0.dmg", SHA256: strings.Repeat("b", 64), Format: "dmg"}},
		{"linux", "amd64", Firefox, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/releases/161.0/linux-x86_64/en-US/firefox-161.0.tar.xz", SHA256: strings.Repeat("c", 64), Format: "tar.xz"}},
		{"linux", "amd64", Camoufox, Release{Kind: Camoufox, Version: "159.0-beta.43", URL: "https://gh.example/lin.zip", Size: 10, SHA256: strings.Repeat("e", 64), Format: "zip"}},
		{"windows", "arm64", Camoufox, Release{Kind: Camoufox, Version: "159.0-beta.43", URL: "https://gh.example/win.zip", Size: 11, SHA256: strings.Repeat("f", 64), Format: "zip"}},
		{"darwin", "arm64", Camoufox, Release{Kind: Camoufox, Version: "159.0-beta.43", URL: "https://gh.example/mac.zip", Size: 12, Format: "zip"}},
	} {
		m := testManager(t, test.goos, test.goarch)
		m.ChromeIndex, m.FirefoxVersions, m.FirefoxReleases, m.CamoufoxLatest = server.URL+"/chrome.json", server.URL+"/versions.json", server.URL+"/releases", server.URL+"/camoufox.json"
		got, err := m.Latest(t.Context(), nil, test.kind)
		if err != nil || got != test.want {
			t.Errorf("%s/%s %s = %+v, %v; want %+v", test.goos, test.goarch, test.kind, got, err, test.want)
		}
	}
	m := testManager(t, "linux", "arm64")
	m.ChromeIndex, m.CamoufoxLatest = server.URL+"/chrome.json", server.URL+"/camoufox.json"
	for _, kind := range []Kind{Chrome, Camoufox} {
		if _, err := m.Latest(t.Context(), nil, kind); !errors.Is(err, ErrUnsupported) {
			t.Errorf("linux/arm64 %s without a build: %v", kind, err)
		}
	}
	if testManager(t, "plan9", "amd64").Supported(Firefox) || !testManager(t, "linux", "arm64").Supported(Firefox) {
		t.Error("Supported is wrong")
	}
	bad := serve(t, map[string][]byte{"/chrome.json": []byte(`{"channels":{"Stable":{"version":"../x"}}}`), "/versions.json": []byte(`{"LATEST_FIREFOX_VERSION":"1/../../x"}`)})
	m = testManager(t, "linux", "amd64")
	m.ChromeIndex, m.FirefoxVersions = bad.URL+"/chrome.json", bad.URL+"/versions.json"
	for _, kind := range []Kind{Chrome, Firefox} {
		if _, err := m.Latest(t.Context(), nil, kind); err == nil {
			t.Errorf("%s accepted a bad version", kind)
		}
	}
}

// chromeZip is a Chrome for Testing download for this system.
func chromeZip(t *testing.T, version string) ([]byte, string) {
	exe := map[string]string{
		"windows": "chrome-win64/chrome.exe",
		"darwin":  "chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
	}[runtime.GOOS]
	if exe == "" {
		exe = "chrome-linux64/chrome"
	}
	entries := []entry{{name: "chrome-x/"}, {name: exe, body: "binary " + version, mode: 0o755}, {name: "chrome-x/resources.pak", body: strings.Repeat("r", 4000)}}
	if runtime.GOOS != "windows" {
		entries = append(entries, entry{name: "chrome-x/current", link: "resources.pak"})
	}
	return zipOf(t, entries...), exe
}

func TestInstallUpdateRemove(t *testing.T) {
	m := testManager(t, runtime.GOOS, "amd64")
	v1, exe := chromeZip(t, "1")
	v2, _ := chromeZip(t, "2")
	server := serve(t, map[string][]byte{"/v1.zip": v1, "/v2.zip": v2})
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("before install: %v", err)
	}
	var phases []Phase
	installed, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "1.0", URL: server.URL + "/v1.zip", Format: "zip"}, func(p Progress) {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phases, []Phase{Resolving, Downloading, Extracting}) {
		t.Errorf("phases = %v", phases)
	}
	if installed.Exe != exe || installed.Version != "1.0" || installed.Size < 4000 || installed.Dir != m.Dir(Chrome) {
		t.Errorf("installed = %+v", installed)
	}
	if data, err := os.ReadFile(installed.Path()); err != nil || string(data) != "binary 1" {
		t.Fatalf("executable %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if target, err := os.Readlink(filepath.Join(m.Dir(Chrome), "app", "chrome-x", "current")); err != nil || target != "resources.pak" {
			t.Errorf("link %q, %v", target, err)
		}
	}
	read, err := m.Installed(Chrome)
	if err != nil || read != installed {
		t.Fatalf("Installed() = %+v, %v; want %+v", read, err, installed)
	}
	// An update replaces it whole.
	if _, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2.0", URL: server.URL + "/v2.zip", SHA256: sum(v2), Format: "zip"}, nil); err != nil {
		t.Fatal(err)
	}
	if read, err := m.Installed(Chrome); err != nil || read.Version != "2.0" {
		t.Fatalf("after update: %+v, %v", read, err)
	}
	if data, _ := os.ReadFile(read.Path()); string(data) != "binary 2" {
		t.Errorf("executable after update: %q", data)
	}
	if names := leftovers(t, m); len(names) != 0 {
		t.Errorf("left behind: %v", names)
	}
	if err := m.Remove(Chrome); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("after remove: %v", err)
	}
	if err := m.Remove(Chrome); err != nil {
		t.Errorf("removing again: %v", err)
	}
	if names := leftovers(t, m); len(names) != 0 {
		t.Errorf("left behind: %v", names)
	}
}

func TestInstallFailuresKeepTheOldKernel(t *testing.T) {
	savedDelay := retryDelay
	retryDelay = time.Millisecond
	defer func() { retryDelay = savedDelay }()
	m := testManager(t, runtime.GOOS, "amd64")
	good, _ := chromeZip(t, "1")
	server := serve(t, map[string][]byte{
		"/good.zip": good, "/hang.zip": nil, "/notzip.zip": []byte("not a zip"),
		"/noexe.zip": zipOf(t, entry{name: "readme.txt", body: "x"}),
	})
	if _, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "1", URL: server.URL + "/good.zip", Format: "zip"}, nil); err != nil {
		t.Fatal(err)
	}
	check := func(name string, err, want error) {
		t.Helper()
		if err == nil || want != nil && !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}
		if read, err := m.Installed(Chrome); err != nil || read.Version != "1" {
			t.Errorf("%s: old kernel %+v, %v", name, read, err)
		}
		if names := leftovers(t, m); len(names) != 0 {
			t.Errorf("%s: left behind %v", name, names)
		}
	}
	_, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/good.zip", SHA256: strings.Repeat("0", 64), Format: "zip"}, nil)
	check("checksum", err, ErrChecksum)
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/good.zip", Size: 5, Format: "zip"}, nil)
	check("size", err, nil)
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/missing.zip", Format: "zip"}, nil)
	check("not found", err, nil)
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/notzip.zip", Format: "zip"}, nil)
	check("not a zip", err, nil)
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/noexe.zip", Format: "zip"}, nil)
	check("no executable", err, nil)
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: "file:///etc/passwd", Format: "zip"}, nil)
	check("file URL", err, nil)

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	var once sync.Once
	go func() {
		<-started
		cancel()
	}()
	_, err = m.Install(ctx, nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/hang.zip", Format: "zip"}, func(p Progress) {
		if p.Phase == Downloading && p.Done > 0 {
			once.Do(func() { close(started) })
		}
	})
	check("cancel", err, context.Canceled)

	saved := stallTimeout
	stallTimeout = 200 * time.Millisecond
	defer func() { stallTimeout = saved }()
	_, err = m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/hang.zip", Format: "zip"}, nil)
	check("stall", err, ErrStalled)
}

func TestUnpackRefusesEscapes(t *testing.T) {
	for name, archive := range map[string][]byte{
		"parent":       zipOf(t, entry{name: "../evil", body: "x"}),
		"absolute":     zipOf(t, entry{name: "/evil", body: "x"}),
		"backslash":    zipOf(t, entry{name: `a\..\..\evil`, body: "x"}),
		"link out":     zipOf(t, entry{name: "a/link", link: "../../outside"}),
		"link abs":     zipOf(t, entry{name: "link", link: "/etc"}),
		"duplicate":    zipOf(t, entry{name: "a", body: "1"}, entry{name: "a", body: "2"}),
		"through link": zipOf(t, entry{name: "a", link: "."}, entry{name: "a", body: "2"}),
		"link chain":   zipOf(t, entry{name: "d/"}, entry{name: "d/l", link: ".."}, entry{name: "d/l/l2", link: ".."}, entry{name: "d/l/l2/evil", body: "x"}),
		"write via":    zipOf(t, entry{name: "d/"}, entry{name: "d/l", link: ".."}, entry{name: "d/l/x", body: "x"}),
	} {
		dir := t.TempDir()
		dest := filepath.Join(dir, "app")
		path := filepath.Join(dir, "a.zip")
		os.WriteFile(path, archive, 0o600)
		if err := unzip(t.Context(), path, dest, func(Progress) {}); err == nil {
			t.Errorf("%s: unpacked", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
			t.Errorf("%s: wrote outside", name)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.tar.xz")
	os.WriteFile(path, tarXZOf(t, entry{name: "../evil", body: "x"}), 0o600)
	if err := untarXZ(t.Context(), path, filepath.Join(dir, "app"), func(Progress) {}); err == nil {
		t.Error("tar.xz escape unpacked")
	}
}

func TestInstallFormats(t *testing.T) {
	// Linux Firefox: a tar.xz with firefox/firefox.
	tarball := tarXZOf(t, entry{name: "firefox/firefox", body: "fx", mode: 0o755}, entry{name: "firefox/libxul.so", body: "lib"})
	camoufox := zipOf(t, entry{name: "camoufox-bin", body: "cf", mode: 0o755}, entry{name: "camoufox.cfg", body: "cfg"}, entry{name: "fonts/a.ttf", body: "font"})
	server := serve(t, map[string][]byte{"/fx.tar.xz": tarball, "/cf.zip": camoufox, "/setup.exe": []byte("installer"), "/fx.dmg": []byte("image")})
	m := testManager(t, "linux", "amd64")
	installed, err := m.Install(t.Context(), nil, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/fx.tar.xz", SHA256: sum(tarball), Format: "tar.xz"}, nil)
	if err != nil || installed.Exe != "firefox/firefox" {
		t.Fatalf("tar.xz: %+v, %v", installed, err)
	}
	installed, err = m.Install(t.Context(), nil, Release{Kind: Camoufox, Version: "159.0-beta.43", URL: server.URL + "/cf.zip", Format: "zip"}, nil)
	if err != nil || installed.Exe != "camoufox-bin" {
		t.Fatalf("camoufox: %+v, %v", installed, err)
	}

	// Windows Firefox: the installer unpacks itself with /ExtractDir.
	m = testManager(t, "windows", "amd64")
	var calls [][]string
	m.run = func(ctx context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{filepath.Base(name)}, args...))
		out, ok := strings.CutPrefix(args[0], "/ExtractDir=")
		if !ok {
			return errors.New("unexpected arguments")
		}
		os.MkdirAll(filepath.Join(out, "core"), 0o755)
		return os.WriteFile(filepath.Join(out, "core", "firefox.exe"), []byte("fx"), 0o755)
	}
	installed, err = m.Install(t.Context(), nil, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/setup.exe", Format: "exe"}, nil)
	if err != nil || installed.Exe != "firefox.exe" || len(calls) != 1 || calls[0][0] != "download.exe" {
		t.Fatalf("exe: %+v, %v, calls %q", installed, err, calls)
	}

	// macOS Firefox: the image is mounted, its app copied with ditto, and
	// detached.
	m = testManager(t, "darwin", "arm64")
	calls = nil
	m.run = func(ctx context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		switch name {
		case "hdiutil":
			if args[0] == "attach" {
				mount := args[slices.Index(args, "-mountpoint")+1]
				macos := filepath.Join(mount, "Firefox.app", "Contents", "MacOS")
				os.MkdirAll(macos, 0o755)
				return os.WriteFile(filepath.Join(macos, "firefox"), []byte("fx"), 0o755)
			}
			return nil
		case "ditto":
			return os.CopyFS(args[1], os.DirFS(args[0]))
		}
		return errors.New("unexpected tool")
	}
	installed, err = m.Install(t.Context(), nil, Release{Kind: Firefox, Version: "161.0", URL: server.URL + "/fx.dmg", Format: "dmg"}, nil)
	if err != nil || installed.Exe != "Firefox.app/Contents/MacOS/firefox" {
		t.Fatalf("dmg: %+v, %v", installed, err)
	}
	if len(calls) != 3 || calls[0][1] != "attach" || calls[1][0] != "ditto" || calls[2][1] != "detach" {
		t.Errorf("dmg tools: %q", calls)
	}
}

func TestBusyInUseDamagedAndClean(t *testing.T) {
	m := testManager(t, runtime.GOOS, "amd64")
	good, _ := chromeZip(t, "1")
	server := serve(t, map[string][]byte{"/good.zip": good, "/hang.zip": nil})
	release := Release{Kind: Chrome, Version: "1", URL: server.URL + "/good.zip", Format: "zip"}
	if _, err := m.Install(t.Context(), nil, release, nil); err != nil {
		t.Fatal(err)
	}
	done, err := m.Acquire(Chrome)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(Chrome); !errors.Is(err, ErrInUse) {
		t.Errorf("remove while running: %v", err)
	}
	if _, err := m.Install(t.Context(), nil, release, nil); !errors.Is(err, ErrInUse) {
		t.Errorf("update while running: %v", err)
	}
	done()
	done()
	ctx, cancel := context.WithCancel(t.Context())
	downloading := make(chan struct{})
	finished := make(chan error, 1)
	var once sync.Once
	go func() {
		_, err := m.Install(ctx, nil, Release{Kind: Chrome, Version: "2", URL: server.URL + "/hang.zip", Format: "zip"}, func(p Progress) {
			if p.Phase == Downloading {
				once.Do(func() { close(downloading) })
			}
		})
		finished <- err
	}()
	<-downloading
	if _, err := m.Acquire(Chrome); !errors.Is(err, ErrBusy) {
		t.Errorf("run while installing: %v", err)
	}
	if err := m.Remove(Chrome); !errors.Is(err, ErrBusy) {
		t.Errorf("remove while installing: %v", err)
	}
	if _, err := m.Install(t.Context(), nil, release, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("second install: %v", err)
	}
	cancel()
	<-finished
	if done, err := m.Acquire(Chrome); err != nil {
		t.Errorf("run after the install ended: %v", err)
	} else {
		done()
	}

	installed, _ := m.Installed(Chrome)
	os.Remove(installed.Path())
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrDamaged) {
		t.Errorf("executable gone: %v", err)
	}
	os.WriteFile(filepath.Join(m.Dir(Chrome), manifestName), []byte("{"), 0o600)
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrDamaged) {
		t.Errorf("bad kernel.json: %v", err)
	}
	escape, _ := json.Marshal(Installed{Kind: Chrome, Version: "1", Exe: "../../outside"})
	os.WriteFile(filepath.Join(m.Dir(Chrome), manifestName), escape, 0o600)
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrDamaged) {
		t.Errorf("executable outside: %v", err)
	}
	os.Remove(filepath.Join(m.Dir(Chrome), manifestName))
	if _, err := m.Installed(Chrome); !errors.Is(err, ErrDamaged) {
		t.Errorf("folder without kernel.json: %v", err)
	}
	if err := m.Remove(Chrome); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{".trash-chrome-1", ".tmp-chrome-old", ".tmp-chrome-new", "firefox"} {
		os.MkdirAll(filepath.Join(m.Root, name, "x"), 0o755)
	}
	os.Chtimes(filepath.Join(m.Root, ".tmp-chrome-old"), old, old)
	m.Clean()
	entries, _ := os.ReadDir(m.Root)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{".tmp-chrome-new", "firefox"}) {
		t.Errorf("after Clean: %v", names)
	}
}

// flaky serves data, cutting the first answers short: drops is how many
// answers end after half of what they owe, ranges whether it honors
// Range, failFirst how many answers are a 503 first.
func flaky(t *testing.T, data []byte, drops, failFirst int, ranges bool) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Range"))
		fail := failFirst > 0
		if fail {
			failFirst--
		}
		drop := !fail && drops > 0
		if drop {
			drops--
		}
		mu.Unlock()
		if fail {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		start := 0
		if header := r.Header.Get("Range"); ranges && header != "" {
			fmt.Sscanf(header, "bytes=%d-", &start)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)-start))
			w.WriteHeader(http.StatusPartialContent)
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		}
		body := data[start:]
		if drop {
			w.Write(body[:len(body)/2])
			w.(http.Flusher).Flush()
			hijacker, _ := w.(http.Hijacker)
			if conn, _, err := hijacker.Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func TestDownloadResumes(t *testing.T) {
	saved := retryDelay
	retryDelay = time.Millisecond
	defer func() { retryDelay = saved }()
	archive, _ := chromeZip(t, "1")
	for _, test := range []struct {
		name             string
		drops, failFirst int
		ranges           bool
		wantErr          bool
		wantRanges       int
	}{
		{"dropped twice, resumed", 2, 0, true, false, 2},
		{"range ignored, restarted", 2, 0, false, false, 2},
		{"busy, then whole", 0, 2, true, false, 0},
		{"drops every time", 9, 0, true, true, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := testManager(t, runtime.GOOS, "amd64")
			server, seen := flaky(t, archive, test.drops, test.failFirst, test.ranges)
			_, err := m.Install(t.Context(), nil, Release{Kind: Chrome, Version: "1", URL: server.URL + "/c.zip", SHA256: sum(archive), Format: "zip"}, nil)
			if test.wantErr != (err != nil) {
				t.Fatalf("err = %v", err)
			}
			ranges := 0
			for _, header := range *seen {
				if header != "" {
					ranges++
				}
			}
			if ranges != test.wantRanges || len(*seen) > downloadAttempts {
				t.Errorf("requests %q", *seen)
			}
			if !test.wantErr {
				if installed, err := m.Installed(Chrome); err != nil || installed.Version != "1" {
					t.Errorf("installed %+v, %v", installed, err)
				}
			}
			if names := leftovers(t, m); len(names) != 0 {
				t.Errorf("left behind %v", names)
			}
		})
	}
}
