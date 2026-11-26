package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxyLibrary keeps the hosts' copies of the library's proxies, which
// builds without the library connect through, in step with it.
func TestProxyLibrary(t *testing.T) {
	s := &Store{}
	office := &Proxy{ID: NewProxyID(), Name: "Office", Kind: ProxySOCKS5, Addr: "10.0.0.1", Port: 1080, User: "bob"}
	s.UpdateProxy(office)
	web := &Host{ID: "h1", Name: "web", Addr: "web.example"}
	db := &Host{ID: "h2", Name: "db", Addr: "db.example"}
	own := &Host{ID: "h3", Name: "own", Addr: "own.example", Proxy: &ProxyConfig{Kind: ProxyHTTP, Addr: "10.9.9.9"}}
	s.Hosts = []*Host{web, db, own}
	UseProxy(web, s.Proxy(office.ID))
	UseProxy(db, s.Proxy(office.ID))

	// The copy is the whole proxy, as a build without the library reads it.
	if p := ProxyOf(web); p == nil || p.Kind != ProxySOCKS5 || p.Addr != "10.0.0.1" || p.Port != 1080 || p.User != "bob" || p.ProxyID != office.ID {
		t.Fatalf("copy %+v", web.Proxy)
	}
	if s.LinkedProxy(web) == nil || s.LinkedProxy(own) != nil {
		t.Fatal("LinkedProxy")
	}
	// db's copy changed on its own, as a build without the library does.
	db.Proxy.Addr = "10.0.0.2"
	if s.LinkedProxy(db) != nil {
		t.Fatal("a copy changed on its own is still linked")
	}
	moved := *office
	moved.Addr, moved.Port = "10.0.0.5", 1081
	changed := s.UpdateProxy(&moved)
	if len(changed) != 2 {
		t.Fatalf("changed %d hosts", len(changed))
	}
	if web.Proxy.Addr != "10.0.0.5" || web.Proxy.Port != 1081 || s.LinkedProxy(web) == nil {
		t.Fatalf("web's copy did not follow: %+v", web.Proxy)
	}
	if db.Proxy.Addr != "10.0.0.2" || db.Proxy.ProxyID != "" {
		t.Fatalf("db's own proxy was overwritten: %+v", db.Proxy)
	}
	if own.Proxy.Addr != "10.9.9.9" {
		t.Fatal("a host's own proxy changed")
	}
	// Removed from the library, web keeps connecting through its copy.
	if users := s.RemoveProxy(office.ID); len(users) != 1 || users[0] != web {
		t.Fatalf("users %v", users)
	}
	if s.Proxy(office.ID) != nil || web.Proxy.Addr != "10.0.0.5" || web.Proxy.ProxyID != "" {
		t.Fatalf("after removing: %+v", web.Proxy)
	}
}

// TestProxyLibraryJSON keeps the library in hosts.json, the hosts' links
// in their proxies, and what a newer build wrote that this one does not
// know.
func TestProxyLibraryJSON(t *testing.T) {
	data := `{"version":1,"folders":[],"hosts":[{"id":"h1","name":"web","addr":"web.example",
		"proxy":{"kind":"socks5","addr":"10.0.0.1","port":1080,"user":"bob","proxyId":"p1","newer":1}}],
		"proxies":[{"id":"p1","name":"Office","kind":"socks5","addr":"10.0.0.1","port":1080,"user":"bob","note":"VPN","later":"x"}],
		"elsewhere":true}`
	var s Store
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Proxies) != 1 || s.Proxies[0].Note != "VPN" || s.LinkedProxy(s.Hosts[0]) != s.Proxies[0] {
		t.Fatalf("read %+v", s.Proxies)
	}
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"proxyId":"p1"`, `"newer":1`, `"later":"x"`, `"elsewhere":true`, `"note":"VPN"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("lost %s: %s", want, out)
		}
	}
	// No library, no key: a file that never had one is written as before.
	out, _ = json.Marshal(Store{Hosts: []*Host{{ID: "h1"}}})
	if strings.Contains(string(out), "proxies") {
		t.Fatalf("an empty library wrote %s", out)
	}
}
