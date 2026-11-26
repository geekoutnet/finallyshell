package cloudsync

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawProxy(id, addr string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","name":"` + id + `","kind":"socks5","addr":"` + addr + `"}`)
}

// TestMergeProxiesWithOldBuilds: a payload a build without the proxy
// library wrote (no "proxies" key) changes nothing of it, nor of its
// passwords, while one with the library adds, changes and deletes them.
func TestMergeProxiesWithOldBuilds(t *testing.T) {
	secret := ProxySecretPrefix + "p1/password"
	base := &Payload{Proxies: []json.RawMessage{rawProxy("p1", "10.0.0.1")}, Secrets: map[string]string{secret: "pw", "host/h1/password": "a"}}
	local := base.Clone()

	// An old build synced in between: no proxies key, nor their passwords.
	var old Payload
	if err := json.Unmarshal([]byte(`{"schema":1,"folders":[],"hosts":[],"settings":{},"secrets":{"host/h1/password":"a"}}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Proxies != nil {
		t.Fatal("a payload without the key read as a library")
	}
	merged, conflicts := Merge(base, local, &old)
	if conflicts != 0 || len(merged.Proxies) != 1 || merged.Secrets[secret] != "pw" {
		t.Fatalf("an old build's payload deleted the library: %d proxies, secrets %v", len(merged.Proxies), merged.Secrets)
	}
	// This device added one meanwhile: kept.
	local.Proxies = append(local.Proxies, rawProxy("p2", "10.0.0.2"))
	if merged, _ = Merge(base, local, &old); len(merged.Proxies) != 2 {
		t.Fatalf("%d proxies", len(merged.Proxies))
	}

	// A build with the library deleted p1 on the other device: deleted.
	remote := base.Clone()
	remote.Proxies = NewProxies()
	delete(remote.Secrets, secret)
	merged, _ = Merge(base, base.Clone(), remote)
	if len(merged.Proxies) != 0 || merged.Proxies == nil {
		t.Fatalf("a deletion on the other side: %v", merged.Proxies)
	}
	if _, ok := merged.Secrets[secret]; ok {
		t.Fatal("the deleted proxy's password stayed")
	}
	// It changed p1's address there: taken.
	remote = base.Clone()
	remote.Proxies = []json.RawMessage{rawProxy("p1", "10.0.0.9")}
	if merged, _ = Merge(base, base.Clone(), remote); !strings.Contains(string(merged.Proxies[0]), "10.0.0.9") {
		t.Fatalf("a change on the other side: %s", merged.Proxies[0])
	}

	// What a build with the library writes always has the key, which an
	// old build ignores.
	data, err := json.Marshal(merged)
	if err != nil || !strings.Contains(string(data), `"proxies":[`) {
		t.Fatalf("written %s: %v", data, err)
	}
	empty, _ := json.Marshal(&Payload{Proxies: NewProxies()})
	if !strings.Contains(string(empty), `"proxies":[]`) {
		t.Fatalf("an empty library is written %s", empty)
	}
	var back Payload
	if json.Unmarshal(empty, &back) != nil || back.Proxies == nil {
		t.Fatal("an empty library read back as none")
	}
}

// TestAddMissingProxies brings back the library's proxies of a backup
// that are gone.
func TestAddMissingProxies(t *testing.T) {
	local := &Payload{Proxies: []json.RawMessage{rawProxy("p1", "10.0.0.1")}}
	backup := &Payload{Proxies: []json.RawMessage{rawProxy("p1", "10.0.0.5"), rawProxy("p2", "10.0.0.2")}}
	out := AddMissing(local, backup, nil)
	if len(out.Proxies) != 2 || !strings.Contains(string(out.Proxies[0]), "10.0.0.1") {
		t.Fatalf("%d proxies: %s", len(out.Proxies), out.Proxies)
	}
}
