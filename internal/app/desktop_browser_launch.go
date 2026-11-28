package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"finallyshell/internal/browserkernel"
	"finallyshell/internal/config"
	"finallyshell/internal/sshx"

	"golang.org/x/crypto/ssh"
	"golang.org/x/net/proxy"
)

var errDesktopBrowserURL = errors.New("invalid HTTP or HTTPS URL")

type desktopBrowserChoice struct {
	name, path string
	firefox    bool
	// kernel is the downloaded kernel it is, "" for a browser installed
	// on this computer.
	kernel browserkernel.Kind
}

type desktopBrowserProduct struct {
	name, windows, windowsPath, mac string
	linux                           []string
}

var desktopBrowserProducts = []desktopBrowserProduct{
	{"Google Chrome", "chrome.exe", `Google/Chrome/Application/chrome.exe`, "Google Chrome.app/Contents/MacOS/Google Chrome", []string{"google-chrome", "google-chrome-stable"}},
	{"Microsoft Edge", "msedge.exe", `Microsoft/Edge/Application/msedge.exe`, "Microsoft Edge.app/Contents/MacOS/Microsoft Edge", []string{"microsoft-edge", "microsoft-edge-stable"}},
	{"Brave", "brave.exe", `BraveSoftware/Brave-Browser/Application/brave.exe`, "Brave Browser.app/Contents/MacOS/Brave Browser", []string{"brave-browser", "brave"}},
	{"Chromium", "chromium.exe", `Chromium/Application/chrome.exe`, "Chromium.app/Contents/MacOS/Chromium", []string{"chromium", "chromium-browser"}},
	{"Firefox", "firefox.exe", `Mozilla Firefox/firefox.exe`, "Firefox.app/Contents/MacOS/firefox", []string{"firefox"}},
}

func desktopFindBrowsers(platform string, env func(string) string, registry func(string) []string, lookPath func(string) (string, error), exists func(string) bool) []desktopBrowserChoice {
	var choices []desktopBrowserChoice
	seen := make(map[string]bool)
	for _, product := range desktopBrowserProducts {
		var candidates []string
		switch platform {
		case "windows":
			for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
				if root := env(key); root != "" {
					candidates = append(candidates, filepath.Join(root, product.windowsPath))
				}
			}
			candidates = append(candidates, registry(product.windows)...)
		case "darwin":
			candidates = append(candidates, filepath.Join("/Applications", product.mac))
			if home := env("HOME"); home != "" {
				candidates = append(candidates, filepath.Join(home, "Applications", product.mac))
			}
		default:
			for _, name := range product.linux {
				if path, err := lookPath(name); err == nil {
					candidates = append(candidates, path)
				}
			}
		}
		for _, candidate := range candidates {
			key := filepath.Clean(candidate)
			if platform == "windows" {
				key = strings.ToLower(key)
			}
			if !seen[key] && exists(candidate) {
				seen[key] = true
				choices = append(choices, desktopBrowserChoice{product.name, candidate, product.name == "Firefox", ""})
			}
		}
	}
	return choices
}

func desktopBrowserExecutable(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return info.Mode().Perm()&0111 != 0
}

func desktopBrowserURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.ContainsRune(raw, '\\') || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errDesktopBrowserURL
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errDesktopBrowserURL
	}
	return parsed.String(), nil
}

func desktopBrowserArgs(choice desktopBrowserChoice, profile, addr, target string, newTab bool) []string {
	if choice.firefox {
		if newTab {
			return []string{"--profile", profile, "--new-tab", target}
		}
		return []string{"--new-instance", "--profile", profile, "--new-window", target}
	}
	args := []string{
		"--user-data-dir=" + profile, "--proxy-server=socks5://" + addr,
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE 127.0.0.1", "--proxy-bypass-list=<-loopback>",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp", "--disable-quic", "--no-first-run", "--no-default-browser-check",
	}
	if !newTab {
		args = append(args, "--new-window")
	}
	return append(args, target)
}

func desktopBrowserFirefoxPrefs(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("browser proxy must use a loopback address")
	}
	return fmt.Sprintf(`user_pref("network.proxy.type", 1);
user_pref("network.proxy.socks", %q);
user_pref("network.proxy.socks_port", %s);
user_pref("network.proxy.socks_version", 5);
user_pref("network.proxy.socks_remote_dns", true);
user_pref("network.proxy.no_proxies_on", "");
user_pref("network.proxy.allow_hijacking_localhost", true);
user_pref("network.trr.mode", 5);
user_pref("media.peerconnection.enabled", false);
user_pref("network.http.http3.enable", false);
user_pref("browser.shell.checkDefaultBrowser", false);
`, host, port), nil
}

var desktopBrowserProfiles = struct {
	sync.Mutex
	active map[string]bool
}{active: make(map[string]bool)}

func desktopBrowserProfile(choice desktopBrowserChoice, root, hostID, addr string) (string, func(), error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	if choice.firefox {
		profile, err := os.MkdirTemp(root, "firefox-")
		if err != nil {
			return "", nil, err
		}
		cleanup := func() {
			resolved, resolveErr := filepath.Abs(profile)
			parent, parentErr := filepath.Abs(root)
			if resolveErr == nil && parentErr == nil && filepath.Dir(resolved) == parent && strings.HasPrefix(filepath.Base(resolved), "firefox-") {
				_ = os.RemoveAll(resolved)
			}
		}
		prefs, err := desktopBrowserFirefoxPrefs(addr)
		if err == nil {
			err = os.WriteFile(filepath.Join(profile, "user.js"), []byte(prefs), 0600)
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
		return profile, cleanup, nil
	}
	profile := desktopBrowserProfileDir(root, hostID)
	desktopBrowserProfiles.Lock()
	if desktopBrowserProfiles.active[profile] {
		desktopBrowserProfiles.Unlock()
		return "", nil, errDesktopBrowserProfileInUse
	}
	desktopBrowserProfiles.active[profile] = true
	desktopBrowserProfiles.Unlock()
	cleanup := func() {
		desktopBrowserProfiles.Lock()
		delete(desktopBrowserProfiles.active, profile)
		desktopBrowserProfiles.Unlock()
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		cleanup()
		return "", nil, err
	}
	return profile, cleanup, nil
}

var errDesktopBrowserProfileInUse = errors.New("browser profile in use")

// desktopBrowserProfileDir is the host's Chromium profile, kept between
// runs; Firefox's are temporary.
func desktopBrowserProfileDir(root, hostID string) string {
	if !desktopBrowserProfileID(hostID) {
		hostID = fmt.Sprintf("host-%x", sha256.Sum256([]byte(hostID)))
	}
	return filepath.Join(root, hostID)
}

// desktopBrowserResetProfile deletes the host's Chromium profile: its
// logins, cookies and cache. It refuses while a browser runs it.
func desktopBrowserResetProfile(root, hostID string) error {
	profile := desktopBrowserProfileDir(root, hostID)
	desktopBrowserProfiles.Lock()
	defer desktopBrowserProfiles.Unlock()
	if desktopBrowserProfiles.active[profile] {
		return errDesktopBrowserProfileInUse
	}
	return os.RemoveAll(profile)
}

func desktopBrowserProfileID(id string) bool {
	if id == "" || len(id) > 96 || strings.ContainsAny(id, `/\:.`) {
		return false
	}
	if slices.Contains([]string{"CON", "PRN", "AUX", "NUL"}, strings.ToUpper(id)) || strings.HasPrefix(strings.ToUpper(id), "COM") || strings.HasPrefix(strings.ToUpper(id), "LPT") {
		return false
	}
	return strings.IndexFunc(id, func(character rune) bool {
		return !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_')
	}) == -1
}

func desktopBrowserProxy(client *ssh.Client) (*sshx.Tunnel, error) {
	return sshx.StartTunnel(client, config.Tunnel{Kind: config.TunnelDynamic, ListenHost: "127.0.0.1"}, nil)
}

func desktopBrowserDialer(addr string) proxy.ContextDialer {
	dialer, _ := proxy.SOCKS5("tcp", addr, nil, &net.Dialer{Timeout: 8 * time.Second})
	return dialer.(proxy.ContextDialer)
}

func (choice desktopBrowserChoice) command(ctx context.Context, profile, addr, target string, newTab bool) *exec.Cmd {
	args := desktopBrowserArgs(choice, profile, addr, target, newTab)
	if choice.firefox && !newTab && desktopBrowserWaitFlag != "" {
		args = append([]string{desktopBrowserWaitFlag}, args...)
	}
	return exec.CommandContext(ctx, choice.path, args...)
}

type desktopBrowserRun struct {
	choice        desktopBrowserChoice
	profile, addr string
	ctx           context.Context
}

type desktopBrowserCommand struct{ *exec.Cmd }

func (cmd desktopBrowserCommand) Quit() { desktopBrowserQuit(cmd.Process) }
func (cmd desktopBrowserCommand) Kill() { desktopBrowserKill(cmd.Process) }

func desktopLaunchBrowser(run *desktopBrowserRun, target string) (desktopBrowserProcess, error) {
	cmd := run.choice.command(context.Background(), run.profile, run.addr, target, false)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return desktopBrowserCommand{cmd}, nil
}

func desktopBrowserOpenTab(ctx context.Context, run *desktopBrowserRun, target string) error {
	return run.choice.command(ctx, run.profile, run.addr, target, true).Run()
}
