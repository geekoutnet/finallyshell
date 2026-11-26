package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Installed copies accept only updates signed with the key whose public
// half is in mygo.json, read from the latest release of the repository
// there: changing either strands them at their version.
func TestUpdatesConfig(t *testing.T) {
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Updates struct {
			PublicKey  string `json:"publicKey"`
			GitHub     string `json:"github"`
			PrivateKey string `json:"privateKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	u := cfg.Updates
	if u.PublicKey != "fwcqse2POLiSdWytl0BHeURrztxcDObwmos+4IlTqHQ=" || u.GitHub != "TooonyChen/finallyshell" {
		t.Errorf("updates %+v", u)
	}
	if u.PrivateKey != "" {
		t.Errorf("the secret key is MYGO_UPDATER_PRIVATE_KEY in CI, not a file: %q", u.PrivateKey)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.0.2", "2.0.1", true},
		{"v2.0.10", "2.0.9", true},
		{"2.1.0", "2.0.99", true},
		{"3.0.0", "2.9.9", true},
		{"2.0.1", "2.0.1", false},
		{"2.0.0", "2.0.1", false},
		{"0.4.0", "2.0.1", false},
		{"2.0.2-preview.1", "2.0.1", true},
		{"latest", "2.0.1", false},
		{"2.0.2", "", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestLatestRelease reads the latest release through the real mirrors,
// when FINALLYSHELL_TEST_NET is set, and downloads its SHA256SUMS.
func TestLatestRelease(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_NET") == "" {
		t.Skip("FINALLYSHELL_TEST_NET is not set")
	}
	start := time.Now()
	rel, err := latestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("latest release %s from %q in %v", rel.Version, rel.Source, time.Since(start))
	if rel.Source == "" {
		t.Errorf("no mirror answered: GitHub served it")
	}
	sums, err := fetchSums(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sums["finallyshell-setup-"+rel.Version+"-amd64.exe"]; !ok {
		t.Errorf("SHA256SUMS has no setup program: %v", sums)
	}
}

// fakeReleases is a mirror, or GitHub with github, serving one release
// whose setup program is setup.
func fakeReleases(t *testing.T, version string, setup []byte, github bool) *httptest.Server {
	sum := sha256.Sum256(setup)
	name := "finallyshell-setup-" + version + "-amd64.exe"
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest" && !github:
			if r.URL.Query().Get("t") == "" || r.Header.Get("Cache-Control") != "no-cache" {
				http.Error(w, "asked without the time or no-cache", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"tag":"v%s","assets":[{"name":%q,"download":%q},{"name":"SHA256SUMS","download":%q}]}`,
				version, name, srv.URL+"/v"+version+"/"+name, srv.URL+"/v"+version+"/SHA256SUMS")
		case r.URL.Path == "/repos/"+releaseRepo+"/releases/latest" && github:
			fmt.Fprintf(w, `{"tag_name":"v%s","assets":[{"name":%q,"browser_download_url":%q}]}`,
				version, name, srv.URL+"/x/"+name)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			fmt.Fprintf(w, "%x  %s\n%x  other.zip\n", sum, name, sha256.Sum256(nil))
		case strings.HasSuffix(r.URL.Path, "/"+name):
			w.Write(setup)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// useFakeReleases points the update code at mirrors and GitHub for the
// test, with their TLS trusted.
func useFakeReleases(t *testing.T, github *httptest.Server, mirrors ...string) {
	oldM, oldA, oldD, oldC := updateMirrors, githubAPI, githubDownloads, http.DefaultClient
	t.Cleanup(func() { updateMirrors, githubAPI, githubDownloads, http.DefaultClient = oldM, oldA, oldD, oldC })
	updateMirrors, githubAPI, githubDownloads = mirrors, github.URL, github.URL
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	http.DefaultClient = &http.Client{Transport: tr}
}

func TestLatestReleaseFromMirror(t *testing.T) {
	setup := []byte("setup 2.0.9")
	good := fakeReleases(t, "2.0.9", setup, false)
	bad := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>访问受限</html>")) // a mirror that answers no release
	}))
	defer bad.Close()
	down := bad.URL + "/gone" // answers 404
	github := fakeReleases(t, "1.0.0", nil, true)
	useFakeReleases(t, github, down, bad.URL, good.URL)

	rel, err := latestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "2.0.9" || rel.Source != good.URL {
		t.Fatalf("release %+v, want 2.0.9 from the working mirror", rel)
	}
	var shares []float32
	path, err := downloadUpdate(context.Background(), rel, "finallyshell-setup-2.0.9-amd64.exe", func(p float32) { shares = append(shares, p) })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(setup) {
		t.Errorf("downloaded %q", got)
	}
	if len(shares) == 0 || shares[len(shares)-1] != 1 {
		t.Errorf("progress %v, want it to end at 1", shares)
	}
}

// With no mirror answering, GitHub serves the release.
func TestLatestReleaseFromGitHub(t *testing.T) {
	github := fakeReleases(t, "2.1.0", []byte("x"), true)
	useFakeReleases(t, github, github.URL+"/no-mirror")
	rel, err := latestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "2.1.0" || rel.Source != "" {
		t.Fatalf("release %+v, want 2.1.0 from GitHub", rel)
	}
}

// A file whose checksum is not SHA256SUMS's is not kept, and the next
// mirror serves it.
func TestDownloadUpdateChecksum(t *testing.T) {
	setup := []byte("the real setup")
	good := fakeReleases(t, "2.0.9", setup, false)
	sum := sha256.Sum256(setup)
	evil := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			fmt.Fprintf(w, "%x  finallyshell-setup-2.0.9-amd64.exe\n", sum)
			return
		}
		w.Write([]byte("tampered"))
	}))
	defer evil.Close()
	useFakeReleases(t, good, evil.URL, good.URL)
	rel := &release{Version: "2.0.9", Source: evil.URL, Assets: map[string]string{
		"finallyshell-setup-2.0.9-amd64.exe": evil.URL + "/v2.0.9/finallyshell-setup-2.0.9-amd64.exe",
	}}
	path, err := downloadUpdate(context.Background(), rel, "finallyshell-setup-2.0.9-amd64.exe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(setup) {
		t.Errorf("kept %q, want the file whose checksum matches", got)
	}

	// Served by none whole: no file.
	useFakeReleases(t, good, evil.URL)
	githubDownloads = evil.URL
	if _, err := downloadUpdate(context.Background(), rel, "finallyshell-setup-2.0.9-amd64.exe", nil); err == nil {
		t.Error("a tampered file was accepted")
	}
}

// TestUpdateDue checks at every start, then a day after the last check,
// and never once the user turned the checks off.
func TestUpdateDue(t *testing.T) {
	for _, tc := range []struct {
		automatic, started bool
		since              time.Duration
		want               bool
	}{
		{true, true, time.Minute, true},
		{true, false, time.Minute, false},
		{true, false, updateInterval, true},
		{false, true, 0, false},
		{false, false, 48 * time.Hour, false},
	} {
		if got := updateDue(tc.automatic, tc.started, tc.since); got != tc.want {
			t.Errorf("updateDue(%v, %v, %v) = %v", tc.automatic, tc.started, tc.since, got)
		}
	}
}
