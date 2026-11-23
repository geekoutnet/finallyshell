package app

import (
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// connInfoApp is splitApp with the session's host in a folder, on port
// 2222, through a SOCKS5 proxy with a user, its password and the proxy's
// known.
func connInfoApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *config.Host) {
	a, p, tt := splitApp(t)
	h := a.sessions[0].Host
	f := a.hosts.AddFolder("Production", "")
	h.Folder, h.Port, h.Encoding = f.ID, 2222, config.EncodingGBK
	h.Proxy = &config.ProxyConfig{Kind: config.ProxySOCKS5, Addr: "proxy.example.com", Port: 1080, User: "alice"}
	a.passwords[h.ID] = "s3cret!"
	a.proxySecrets()[proxySecretKey(h.ID)] = "pr0xy"
	tt.Frame()
	return a, p, tt, h
}

func TestConnInfoDialog(t *testing.T) {
	a, _, tt, _ := connInfoApp(t)
	rowMenu(t, tt, "root@45.56.78.90:2222", locales.ViewConnInfo())
	if a.connTree.info == nil {
		t.Fatal("no dialog")
	}
	for _, want := range []string{
		locales.ConnInfo(), "web-1", "Production", "45.56.78.90", "2222", "root", locales.AuthPassword(),
		"SOCKS5", "proxy.example.com:1080", "alice", "GBK",
		"ssh -p 2222 root@45.56.78.90", "ssh://root@45.56.78.90:2222",
	} {
		if !tt.HasText(want) {
			t.Errorf("missing %q; texts %q", want, tt.Texts())
		}
	}
	// The passwords are masked.
	if tt.HasText("s3cret!") || tt.HasText("pr0xy") {
		t.Error("a password shows before Show")
	}
	saveShot(t, tt, "conninfo-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "conninfo-dark-en.png")
	tt.SetDark(false)

	clickButton(t, tt, locales.ShowSecret()+" "+locales.FieldPassword())
	if !tt.HasText("s3cret!") || tt.HasText("pr0xy") {
		t.Errorf("shown: texts %q", tt.Texts())
	}
	saveShot(t, tt, "conninfo-shown-en.png")
	clickButton(t, tt, locales.HideSecret()+" "+locales.FieldPassword())
	if tt.HasText("s3cret!") {
		t.Error("Hide left the password")
	}

	// Each line copies its own value; the passwords' copy the password.
	clickButton(t, tt, locales.Copy()+" "+locales.ProxyPasswordTitle())
	if got := tt.Clipboard(); got != "pr0xy" {
		t.Errorf("proxy password copied %q", got)
	}
	clickButton(t, tt, locales.Copy()+" "+locales.ConnInfoSSHCommand())
	if got := tt.Clipboard(); got != "ssh -p 2222 root@45.56.78.90" {
		t.Errorf("command copied %q", got)
	}
	clickButton(t, tt, locales.CopyAll())
	all := tt.Clipboard()
	for _, want := range []string{
		"Name: web-1\n", "Folder: Production\n", "Host: 45.56.78.90\n", "Port: 2222\n", "User: root\n",
		"Authentication: Password\n", "Password: s3cret!\n", "Proxy type: SOCKS5\n",
		"Proxy address: proxy.example.com:1080\n", "Proxy user: alice\n", "Proxy password: pr0xy\n",
		"SSH command: ssh -p 2222 root@45.56.78.90\n", "SSH URL: ssh://root@45.56.78.90:2222\n",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("copy all lacks %q:\n%s", want, all)
		}
	}

	clickButton(t, tt, locales.Close())
	if a.connTree.info != nil {
		t.Error("Close left the dialog")
	}

	// The menu's Copy copies the same, in the app's language.
	a.setLanguage("zh-Hans")
	tt.Frame()
	rowMenu(t, tt, "root@45.56.78.90:2222", "复制连接信息")
	got := tt.Clipboard()
	for _, want := range []string{"名称: web-1\n", "密码: s3cret!\n", "SSH 命令: ssh -p 2222 root@45.56.78.90\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu copy lacks %q:\n%s", want, got)
		}
	}
}

// A password not known this run is read from the credential store
// first; one the store does not have either is said to be missing.
func TestConnInfoStoredSecrets(t *testing.T) {
	a, p, tt := splitApp(t)
	h := a.sessions[0].Host
	p.stored[hostSecretKey(h.ID, secretPassword)] = "fr0m-store"
	rowMenu(t, tt, "root@45.56.78.90", locales.CopyConnInfo())
	p.until(t, "the copy", func() bool { return strings.Contains(tt.Clipboard(), "fr0m-store") })

	// A key without a saved passphrase, through two jump hosts.
	j1 := &config.Host{ID: "j1", Name: "bastion", Addr: "1.2.3.4", User: "ops", Auth: config.AuthAgent}
	j2 := &config.Host{ID: "j2", Addr: "10.0.0.2", Port: 2200, User: "admin", Auth: config.AuthAgent,
		Proxy: &config.ProxyConfig{Kind: config.ProxyJump, JumpHost: "j1"}}
	a.hosts.Add(j1)
	a.hosts.Add(j2)
	k := &config.Host{ID: "k", Name: "db", Addr: "10.0.0.9", User: "pg", Auth: config.AuthKey, KeyPath: "/home/me/.ssh/id ed25519",
		Proxy: &config.ProxyConfig{Kind: config.ProxyJump, JumpHost: "j2"}}
	a.hosts.Add(k)
	a.openConnInfo(&a.connTree, k)
	p.until(t, "the passphrase read", func() bool { return a.connTree.info.reading == 0 })
	for _, want := range []string{
		locales.ConnInfoNoPassphrase(), "/home/me/.ssh/id ed25519",
		"bastion (ops@1.2.3.4) → admin@10.0.0.2:2200",
		"ssh -p 22 -i '/home/me/.ssh/id ed25519' -J ops@1.2.3.4:22,admin@10.0.0.2:2200 pg@10.0.0.9",
	} {
		if !tt.HasText(want) {
			t.Errorf("missing %q; texts %q", want, tt.Texts())
		}
	}
	saveShot(t, tt, "conninfo-key-en.png")
	if b, ok := tt.AccessByRole(ui.RoleButton, locales.CopyAll()); !ok || b.Disabled {
		t.Error("no Copy all")
	}
}

// The connection manager's host menu has the items too, and shows the
// dialog in its own window.
func TestConnInfoManager(t *testing.T) {
	a, p, _, _ := connInfoApp(t)
	a.openManager()
	mt := p.show(t, WindowManager)
	mt.Frame()
	rowMenu(t, mt, "web-1", locales.ViewConnInfo())
	if a.manager.info == nil || !mt.HasText("proxy.example.com:1080") {
		t.Fatalf("no dialog in the manager; texts %q", mt.Texts())
	}
	saveShot(t, mt, "conninfo-manager-en.png")
	clickButton(t, mt, locales.CopyAll())
	if got := mt.Clipboard(); !strings.Contains(got, "Password: s3cret!\n") {
		t.Errorf("copy all: %q", got)
	}
}

func TestSSHCommand(t *testing.T) {
	a, _ := testApp(t)
	for _, tc := range []struct {
		h        config.Host
		cmd, url string
	}{
		{config.Host{Addr: "example.com", User: "root"}, "ssh -p 22 root@example.com", "ssh://root@example.com:22"},
		{config.Host{Addr: "::1", Port: 2222, User: "me"}, "ssh -p 2222 me@::1", "ssh://me@[::1]:2222"},
		{config.Host{Addr: "h", User: "u", Auth: config.AuthKey, KeyPath: `C:\keys\id_rsa`}, `ssh -p 22 -i 'C:\keys\id_rsa' u@h`, "ssh://u@h:22"},
	} {
		if got := a.sshCommand(&tc.h); got != tc.cmd {
			t.Errorf("sshCommand(%+v) = %q, want %q", tc.h, got, tc.cmd)
		}
		if got := sshURL(&tc.h); got != tc.url {
			t.Errorf("sshURL(%+v) = %q, want %q", tc.h, got, tc.url)
		}
	}
}
