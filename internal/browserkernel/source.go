package browserkernel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-]{0,63}$`)

// Supported reports whether the kernel has a build for the manager's
// system.
func (m *Manager) Supported(k Kind) bool {
	switch k {
	case Chrome:
		return m.chromePlatform() != ""
	case Firefox:
		_, _, _, ok := m.firefoxPlatform("1")
		return ok
	case Camoufox:
		return m.camoufoxPlatform() != ""
	}
	return false
}

// Latest finds the kernel's newest release for the manager's system,
// asking its source over c (nil for http.DefaultClient).
func (m *Manager) Latest(ctx context.Context, c *http.Client, k Kind) (Release, error) {
	switch k {
	case Chrome:
		return m.latestChrome(ctx, c)
	case Firefox:
		return m.latestFirefox(ctx, c)
	case Camoufox:
		return m.latestCamoufox(ctx, c)
	}
	return Release{}, ErrUnsupported
}

func (m *Manager) chromePlatform() string {
	switch m.GOOS + "/" + m.GOARCH {
	case "windows/amd64", "windows/arm64":
		return "win64"
	case "windows/386":
		return "win32"
	case "darwin/arm64":
		return "mac-arm64"
	case "darwin/amd64":
		return "mac-x64"
	case "linux/amd64":
		return "linux64"
	case "linux/arm64":
		return "linux-arm64"
	}
	return ""
}

func (m *Manager) latestChrome(ctx context.Context, c *http.Client) (Release, error) {
	platform := m.chromePlatform()
	if platform == "" {
		return Release{}, ErrUnsupported
	}
	var index struct {
		Channels map[string]struct {
			Version   string `json:"version"`
			Downloads struct {
				Chrome []struct{ Platform, URL string } `json:"chrome"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := getJSON(ctx, c, m.ChromeIndex, &index); err != nil {
		return Release{}, err
	}
	stable := index.Channels["Stable"]
	if !versionPattern.MatchString(stable.Version) {
		return Release{}, errors.New("chrome for testing: no stable version")
	}
	for _, download := range stable.Downloads.Chrome {
		if download.Platform == platform {
			if err := httpsURL(download.URL); err != nil {
				return Release{}, err
			}
			return Release{Kind: Chrome, Version: stable.Version, URL: download.URL, Format: "zip"}, nil
		}
	}
	return Release{}, ErrUnsupported
}

// firefoxPlatform is the folder and file name of version's download for
// the manager's system, and how it is unpacked.
func (m *Manager) firefoxPlatform(version string) (folder, file, format string, ok bool) {
	switch m.GOOS {
	case "windows":
		folder = map[string]string{"amd64": "win64", "386": "win32", "arm64": "win64-aarch64"}[m.GOARCH]
		file, format = "Firefox Setup "+version+".exe", "exe"
	case "darwin":
		folder, file, format = "mac", "Firefox "+version+".dmg", "dmg"
	case "linux":
		folder = map[string]string{"amd64": "linux-x86_64", "386": "linux-i686", "arm64": "linux-aarch64"}[m.GOARCH]
		file, format = "firefox-"+version+".tar.xz", "tar.xz"
	}
	return folder, file, format, folder != ""
}

func (m *Manager) latestFirefox(ctx context.Context, c *http.Client) (Release, error) {
	if _, _, _, ok := m.firefoxPlatform("1"); !ok {
		return Release{}, ErrUnsupported
	}
	var versions struct {
		Latest string `json:"LATEST_FIREFOX_VERSION"`
	}
	if err := getJSON(ctx, c, m.FirefoxVersions, &versions); err != nil {
		return Release{}, err
	}
	version := versions.Latest
	if !versionPattern.MatchString(version) || strings.Contains(version, "..") {
		return Release{}, errors.New("firefox: no release version")
	}
	folder, file, format, _ := m.firefoxPlatform(version)
	base := strings.TrimSuffix(m.FirefoxReleases, "/") + "/" + version + "/"
	sums, err := get(ctx, c, base+"SHA256SUMS", 8<<20)
	if err != nil {
		return Release{}, err
	}
	want := folder + "/en-US/" + file
	sum := ""
	scanner := bufio.NewScanner(strings.NewReader(string(sums)))
	for scanner.Scan() {
		if hash, name, ok := strings.Cut(scanner.Text(), "  "); ok && name == want {
			sum = hash
		}
	}
	if !sha256Pattern.MatchString(sum) {
		return Release{}, fmt.Errorf("firefox: no checksum for %s", want)
	}
	return Release{Kind: Firefox, Version: version, URL: base + folder + "/en-US/" + url.PathEscape(file), SHA256: sum, Format: format}, nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (m *Manager) camoufoxPlatform() string {
	system := map[string]string{"windows": "win", "darwin": "mac", "linux": "lin"}[m.GOOS]
	arch := map[string]string{"amd64": "x86_64", "386": "i686", "arm64": "arm64"}[m.GOARCH]
	switch {
	case system == "" || arch == "":
		return ""
	case system == "win" && arch == "arm64":
		arch = "x86_64"
	case system == "mac" && arch == "i686":
		return ""
	}
	return system + "." + arch
}

func (m *Manager) latestCamoufox(ctx context.Context, c *http.Client) (Release, error) {
	platform := m.camoufoxPlatform()
	if platform == "" {
		return Release{}, ErrUnsupported
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, c, m.CamoufoxLatest, &release); err != nil {
		return Release{}, err
	}
	version := strings.TrimPrefix(release.Tag, "v")
	if !versionPattern.MatchString(version) {
		return Release{}, errors.New("camoufox: no release version")
	}
	for _, asset := range release.Assets {
		if !strings.HasPrefix(asset.Name, "camoufox-") || !strings.HasSuffix(asset.Name, "-"+platform+".zip") {
			continue
		}
		if err := httpsURL(asset.URL); err != nil {
			return Release{}, err
		}
		sum := strings.TrimPrefix(asset.Digest, "sha256:")
		if !sha256Pattern.MatchString(sum) {
			sum = ""
		}
		return Release{Kind: Camoufox, Version: version, URL: asset.URL, Size: asset.Size, SHA256: sum, Format: "zip"}, nil
	}
	return Release{}, ErrUnsupported
}

func httpsURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" && !(parsed.Scheme == "http" && testHTTP) || parsed.Host == "" {
		return fmt.Errorf("unexpected download address %q", raw)
	}
	return nil
}

// testHTTP lets tests serve downloads over plain HTTP.
var testHTTP = false

func get(ctx context.Context, c *http.Client, address string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	response, err := client(c).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", address, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: response too large", address)
	}
	return data, nil
}

func getJSON(ctx context.Context, c *http.Client, address string, value any) error {
	data, err := get(ctx, c, address, 8<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
