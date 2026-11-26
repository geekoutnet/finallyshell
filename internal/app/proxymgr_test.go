package app

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/proxycheck"
	"finallyshell/locales"
)

// secretOf is what the fake credential store keeps under key, once the
// queue of writes is done.
func secretOf(t *testing.T, a *App, p *fakePlatform, key string) (string, bool) {
	t.Helper()
	p.until(t, "secrets written", a.secrets.queue.idle)
	v, ok, _ := p.SecretGet(key)
	return v, ok
}

// TestProxyLibraryInApp saves a proxy with its password in the manager,
// lets a host use it, edits it and deletes it: the host's copy and its
// copy of the password follow, as builds without the library read them.
func TestProxyLibraryInApp(t *testing.T) {
	a, p := testApp(t)
	web := demoSession(a, "web-1", "10.0.0.11")
	a.openProxyManager()
	tt := p.show(t, windowProxies)
	tt.Frame()
	if !tt.HasText(locales.ProxyMgrEmptyTitle()) {
		t.Fatalf("no empty state: %q", tt.Texts())
	}
	a.editProxy(nil, nil)
	e := a.proxyMgr.edit
	e.name, e.addr, e.port, e.user, e.password = "Office", "10.0.0.1", "1080", "bob", "pw-1"
	tt.Frame()
	if err := tt.Click(locales.ProxyMgrSave()); err != nil {
		t.Fatalf("%v; %q", err, tt.Texts())
	}
	if len(a.hosts.Proxies) != 1 || a.proxyMgr.edit != nil {
		t.Fatalf("saved %d proxies", len(a.hosts.Proxies))
	}
	lib := a.hosts.Proxies[0]
	if pw, _ := secretOf(t, a, p, libSecretKey(lib.ID)); pw != "pw-1" {
		t.Fatalf("library password %q", pw)
	}
	tt.Frame()
	if !tt.HasText("Office") || !tt.HasText("10.0.0.1:1080") {
		t.Fatalf("the table: %q", tt.Texts())
	}

	// The host uses it: a whole copy, and a copy of the password.
	a.useLibraryProxy(web.Host, lib)
	if err := a.saveHosts(a.hosts); err != nil {
		t.Fatal(err)
	}
	hp := web.Host.Proxy
	if hp.Kind != config.ProxySOCKS5 || hp.Addr != "10.0.0.1" || hp.User != "bob" || hp.ProxyID != lib.ID {
		t.Fatalf("host's copy %+v", hp)
	}
	if pw, _ := secretOf(t, a, p, hostSecretKey(web.Host.ID, secretProxyPassword)); pw != "pw-1" {
		t.Fatalf("host's copy of the password %q", pw)
	}
	if a.proxySecrets()[proxySecretKey(web.Host.ID)] != "pw-1" {
		t.Fatal("the host's proxy password is not known for connecting")
	}

	// Edited: address and password follow to the host.
	a.editProxy(lib, nil)
	a.proxyMgr.edit.addr, a.proxyMgr.edit.password = "10.0.0.2", "pw-2"
	a.saveProxyEdit()
	if web.Host.Proxy.Addr != "10.0.0.2" || a.hosts.LinkedProxy(web.Host) == nil {
		t.Fatalf("the copy did not follow: %+v", web.Host.Proxy)
	}
	if pw, _ := secretOf(t, a, p, hostSecretKey(web.Host.ID, secretProxyPassword)); pw != "pw-2" {
		t.Fatalf("host's password %q after the edit", pw)
	}
	// Edited without a password: the saved one stays.
	a.editProxy(a.hosts.Proxies[0], nil)
	a.proxyMgr.edit.note = "VPN"
	a.saveProxyEdit()
	if pw, ok := secretOf(t, a, p, libSecretKey(lib.ID)); !ok || pw != "pw-2" {
		t.Fatalf("an empty field changed the password to %q", pw)
	}

	// Deleted: the host keeps connecting through its copy.
	a.proxyMgr.deleting = a.hosts.Proxies[0]
	tt.Frame()
	if !tt.HasText(locales.ProxyMgrDeleteUsed(1)) {
		t.Fatalf("deleting does not say a host uses it: %q", tt.Texts())
	}
	if err := tt.Click(locales.Delete()); err != nil {
		t.Fatal(err)
	}
	if len(a.hosts.Proxies) != 0 || web.Host.Proxy.Addr != "10.0.0.2" || web.Host.Proxy.ProxyID != "" {
		t.Fatalf("after deleting: %+v", web.Host.Proxy)
	}
	if _, ok := secretOf(t, a, p, libSecretKey(lib.ID)); ok {
		t.Fatal("the deleted proxy's password stayed")
	}
	if pw, _ := secretOf(t, a, p, hostSecretKey(web.Host.ID, secretProxyPassword)); pw != "pw-2" {
		t.Fatal("the host lost its proxy's password")
	}
}

// TestProxyLibrarySync writes the library into the payload, its key even
// empty, and applies a payload's library; one with none, as a backup
// older than the library, leaves it.
func TestProxyLibrarySync(t *testing.T) {
	a, _ := testApp(t)
	a.saveHosts = config.SaveHosts // to read hosts.json back
	data, err := json.Marshal(a.localPayload())
	if err != nil || !strings.Contains(string(data), `"proxies":[]`) {
		t.Fatalf("payload %s: %v", data, err)
	}
	a.hosts.Proxies = []*config.Proxy{{ID: "p1", Name: "Office", Kind: config.ProxyHTTP, Addr: "10.0.0.1", User: "bob"}}
	local := a.localPayload()
	if len(local.Proxies) != 1 {
		t.Fatalf("%d proxies in the payload", len(local.Proxies))
	}
	if !strings.Contains(strings.Join(a.syncSecretKeys(), " "), libSecretKey("p1")) {
		t.Fatal("the library's password does not sync")
	}
	old := local.Clone()
	old.Proxies = nil
	if err := a.applyPayload(old); err != nil || len(a.hosts.Proxies) != 1 {
		t.Fatalf("a payload without a library deleted it: %v", err)
	}
	next := local.Clone()
	next.Proxies = append(cloudsync.NewProxies(), json.RawMessage(`{"id":"p2","name":"Home","kind":"socks5","addr":"192.168.1.1"}`))
	if err := a.applyPayload(next); err != nil || len(a.hosts.Proxies) != 1 || a.hosts.Proxies[0].ID != "p2" {
		t.Fatalf("applied %v: %v", a.hosts.Proxies, err)
	}
	if saved, err := config.LoadHosts(); err != nil || len(saved.Proxies) != 1 || saved.Proxies[0].Name != "Home" {
		t.Fatalf("hosts.json: %+v, %v", saved, err)
	}
}

// connectProxy is an HTTP proxy for a test that answers CONNECT.
func connectProxy(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				r := bufio.NewReader(c)
				req, err := http.ReadRequest(r)
				if err != nil || req.Method != http.MethodConnect {
					c.Close()
					return
				}
				out, err := net.Dial("tcp", req.Host)
				if err != nil {
					io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
					c.Close()
					return
				}
				io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
				go func() { io.Copy(out, r); out.Close() }()
				io.Copy(c, out)
				c.Close()
			}()
		}
	}()
	h, ps, _ := net.SplitHostPort(ln.Addr().String())
	port, _ = strconv.Atoi(ps)
	return h, port
}

// TestProxyManagerTest tests a proxy of the library through a real
// proxy, to services in the test: the latency, the address and the place
// show in its row.
func TestProxyManagerTest(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"success","country":"Japan","regionName":"Tokyo","city":"Tokyo","isp":"Example KK","query":"203.0.113.7"}`))
	}))
	defer api.Close()
	old := proxycheck.IPAPIURL
	proxycheck.IPAPIURL = api.URL + "/json/?lang="
	t.Cleanup(func() { proxycheck.IPAPIURL = old })

	a, p := testApp(t)
	host, port := connectProxy(t)
	if _, err := a.saveLibraryProxy(&config.Proxy{ID: "p1", Name: "Tokyo", Kind: config.ProxyHTTP, Addr: host, Port: port}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveLibraryProxy(&config.Proxy{ID: "p2", Name: "Dead", Kind: config.ProxySOCKS5, Addr: "127.0.0.1", Port: closedTCPPort(t)}, "", false); err != nil {
		t.Fatal(err)
	}
	a.openProxyManager()
	tt := p.show(t, windowProxies)
	tt.Frame()
	if err := tt.Click(locales.ProxyMgrTestAll()); err != nil {
		t.Fatal(err)
	}
	p.until(t, "the tests", func() bool { return !a.proxyTesting("p1") && !a.proxyTesting("p2") })
	tt.Frame()
	ok := a.proxyTests["p1"]
	if ok.err != nil || ok.res.IP != "203.0.113.7" || ok.res.Latency <= 0 {
		t.Fatalf("the working proxy: %+v, %v", ok.res, ok.err)
	}
	if !tt.HasText(proxyLatencyText(ok.res.Latency) + " · 203.0.113.7 · Japan Tokyo Example KK") {
		t.Fatalf("the row: %q", tt.Texts())
	}
	if a.proxyTests["p2"].err == nil || !tt.HasText(locales.ErrProxyRefused()) {
		t.Fatalf("the dead proxy: %v; %q", a.proxyTests["p2"].err, tt.Texts())
	}
	saveShot(t, tt, "proxy-manager.png")
}

func closedTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// TestProxyOptionsPage manages the library on Options' Proxies page: its
// form shows there, without opening the proxy manager.
func TestProxyOptionsPage(t *testing.T) {
	a, p := testApp(t)
	p.SetLocale("zh-Hans")
	a.optionsNav.Page = pageProxies
	a.openOptions()
	tt := p.show(t, WindowOptions)
	tt.SetSize(980, 720)
	tt.Frame()
	if !tt.HasText(locales.ProxyMgrEmptyTitle()) {
		t.Fatalf("no library on the page: %q", tt.Texts())
	}
	if err := tt.Click(locales.ProxyMgrNew()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.proxyMgr.edit == nil || a.proxyMgr.owner != WindowOptions || a.windowOpen(windowProxies) {
		t.Fatalf("the form: owner %q, manager open %v", a.proxyMgr.owner, a.windowOpen(windowProxies))
	}
	e := a.proxyMgr.edit
	e.name, e.addr, e.port = "香港节点", "203.0.113.9", "1080"
	tt.Frame()
	if err := tt.Click(locales.ProxyMgrSave()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.hosts.Proxies) != 1 || !tt.HasText("香港节点") {
		t.Fatalf("saved %d; %q", len(a.hosts.Proxies), tt.Texts())
	}
	saveShot(t, tt, "options-proxies-zh-Hans.png")
}
