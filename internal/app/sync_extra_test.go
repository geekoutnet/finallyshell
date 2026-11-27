package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/dbclient"
)

// useHome makes dir the home of the device the test is acting as, for
// the private keys under ~/.ssh.
func useHome(t *testing.T, dir string) {
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// Everything that syncs reaches a new computer: the command history, the
// databases and their passwords, the trusted host keys, the AI
// assistant's key and the host's private key; a computer with another key
// of the same name keeps its own, and the file settles.
func TestSyncEverything(t *testing.T) {
	r := &memRemote{}
	homes := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	key := "test key of the first computer\n"
	const aiEndpoint = "https://api.example.com/v1"

	// One: everything set up.
	one := newDevice(t, r)
	useHome(t, homes[0])
	one.a.settings.Sync.Passwords = true
	h := one.addHost("h1", "web")
	h.Auth, h.KeyPath = config.AuthKey, filepath.Join(homes[0], ".ssh", "id_test")
	one.a.saveHosts(one.a.hosts)
	if err := os.MkdirAll(filepath.Join(homes[0], ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(h.KeyPath, []byte(key), 0o600)
	for _, c := range []string{"uptime", "df -h", "systemctl status nginx"} {
		config.AddHistory("h1", c)
	}
	saveDBProfiles(one.a.dbConfigPath(), dbProfiles{"h1": {{ID: "p1", Name: "pg", Engine: dbclient.Postgres, Host: "127.0.0.1", Port: 5432, User: "app", TLS: "disable"}}})
	one.p.stored[dbSecretKey("h1", "p1")] = "db-secret"
	one.a.settings.AI = &config.AISettings{Endpoint: aiEndpoint, Model: "gpt"}
	one.p.stored[desktopAISecret(aiEndpoint)] = "sk-test"
	os.WriteFile(knownHostsPath(), []byte("web.example ssh-ed25519 AAAAC3one\n"), 0o600)
	one.sync(t)

	// Two: a new computer.
	two := newDevice(t, r)
	useHome(t, homes[1])
	two.a.settings.Sync.Passwords = true
	two.sync(t)
	two.p.until(t, "the store", storeSettled(two.a, two.p))
	useHome(t, homes[1])
	if got, _ := config.LoadHistory("h1"); !slices.Equal(got, []string{"uptime", "df -h", "systemctl status nginx"}) {
		t.Errorf("history %v", got)
	}
	if got := two.a.dbProfilesNow()["h1"]; len(got) != 1 || got[0].Name != "pg" {
		t.Errorf("databases %+v", got)
	}
	if two.p.stored[dbSecretKey("h1", "p1")] != "db-secret" || two.p.stored[desktopAISecret(aiEndpoint)] != "sk-test" {
		t.Errorf("store %v", two.p.stored)
	}
	if two.a.settings.AI == nil || two.a.settings.AI.Endpoint != aiEndpoint {
		t.Errorf("AI settings %+v", two.a.settings.AI)
	}
	if data, _ := os.ReadFile(knownHostsPath()); !strings.Contains(string(data), "web.example ssh-ed25519 AAAAC3one") {
		t.Errorf("known_hosts %q", data)
	}
	if kp := two.a.hosts.Host("h1").KeyPath; kp != "~/.ssh/id_test" {
		t.Errorf("key path %q", kp)
	}
	if data, err := os.ReadFile(filepath.Join(homes[1], ".ssh", "id_test")); err != nil || string(data) != key {
		t.Errorf("the private key: %q, %v", data, err)
	}

	// Three: another key by the same name, which stays; the file settles.
	three := newDevice(t, r)
	useHome(t, homes[2])
	three.a.settings.Sync.Passwords = true
	own := "test key of the third computer\n"
	os.MkdirAll(filepath.Join(homes[2], ".ssh"), 0o700)
	os.WriteFile(filepath.Join(homes[2], ".ssh", "id_test"), []byte(own), 0o600)
	three.sync(t)
	three.p.until(t, "the store", storeSettled(three.a, three.p))
	if data, _ := os.ReadFile(filepath.Join(homes[2], ".ssh", "id_test")); string(data) != own {
		t.Fatalf("this computer's own key was replaced: %q", data)
	}
	for _, d := range []*device{one, three, one, three} {
		useHome(t, homes[map[*device]int{one: 0, three: 2}[d]])
		d.sync(t)
		d.p.until(t, "the store", storeSettled(d.a, d.p))
	}
	writes := r.writes()
	useHome(t, homes[0])
	one.sync(t)
	useHome(t, homes[2])
	three.sync(t)
	if r.writes() != writes {
		t.Fatalf("the keys go back and forth: %d more writes", r.writes()-writes)
	}
	if data, _ := os.ReadFile(filepath.Join(homes[0], ".ssh", "id_test")); string(data) != key {
		t.Fatalf("the first computer's key changed: %q", data)
	}
}

// An unreadable db.json syncs no lists, rather than none of them: the
// databases elsewhere are not deleted.
func TestSyncListsUnreadable(t *testing.T) {
	r := &memRemote{}
	one := newDevice(t, r)
	useHome(t, t.TempDir())
	one.addHost("h1", "web")
	saveDBProfiles(one.a.dbConfigPath(), dbProfiles{"h1": {{ID: "p1", Name: "pg", Engine: dbclient.Postgres, Host: "127.0.0.1", Port: 5432, User: "app", TLS: "disable"}}})
	one.sync(t)
	two := newDevice(t, r)
	two.sync(t)
	os.WriteFile(two.a.dbConfigPath(), []byte("{not json"), 0o600)
	two.a.db.loaded = false
	if two.a.localPayload().Lists != nil {
		t.Fatal("lists synced with db.json unreadable")
	}
	two.sync(t)
	one.sync(t)
	if got := one.a.dbProfilesNow()["h1"]; len(got) != 1 {
		t.Fatalf("the databases were deleted: %+v", got)
	}
}

// A payload of an older build, without the lists, leaves this device's
// databases, history and host keys as they are.
func TestSyncListsFromAnOlderBuild(t *testing.T) {
	r := &memRemote{}
	d := newDevice(t, r)
	useHome(t, t.TempDir())
	d.addHost("h1", "web")
	config.AddHistory("h1", "uptime")
	d.sync(t)
	if err := d.a.applySyncLists(nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := config.LoadHistory("h1"); !slices.Equal(got, []string{"uptime"}) {
		t.Fatalf("history %v", got)
	}
	p := d.a.localPayload()
	if p.Lists == nil || len(p.Lists[cloudsync.ListHistoryPrefix+"h1"]) != 1 {
		t.Fatalf("lists %v", p.Lists)
	}
}

// remoteSecrets are the secrets the remote has, opened with password.
func remoteSecrets(t *testing.T, r *memRemote, password string) map[string]string {
	t.Helper()
	r.mu.Lock()
	data := r.data
	r.mu.Unlock()
	p, err := cloudsync.OpenSnapshot(password, data)
	if err != nil {
		t.Fatal(err)
	}
	return p.Secrets
}

// A host key deleted on one computer, as when its server was reinstalled
// and the new key trusted, is deleted on the others: a computer that
// still has the old one does not add it back. Comments stay.
func TestSyncHostKeyDeleted(t *testing.T) {
	r := &memRemote{}
	const (
		oldKey = "203.0.113.7 ssh-ed25519 AAAAC3old"
		newKey = "203.0.113.7 ssh-ed25519 AAAAC3new"
		other  = "other.example ssh-ed25519 AAAAC3other"
	)
	one := newDevice(t, r)
	useHome(t, t.TempDir())
	os.WriteFile(knownHostsPath(), []byte(oldKey+"\n"+other+"\n"), 0o600)
	one.sync(t)
	two := newDevice(t, r)
	two.use(t)
	os.WriteFile(knownHostsPath(), []byte("# my notes\n"), 0o600)
	two.sync(t)
	if data, _ := os.ReadFile(knownHostsPath()); string(data) != "# my notes\n"+oldKey+"\n"+other+"\n" {
		t.Fatalf("the second computer's known_hosts %q", data)
	}

	// The server is reinstalled: the first computer trusts its new key.
	one.use(t)
	os.WriteFile(knownHostsPath(), []byte(other+"\n"+newKey+"\n"), 0o600)
	one.sync(t)
	two.sync(t)
	one.sync(t)
	two.sync(t)
	for _, d := range []*device{one, two} {
		d.use(t)
		data, _ := os.ReadFile(knownHostsPath())
		if strings.Contains(string(data), oldKey) || !strings.Contains(string(data), newKey) || !strings.Contains(string(data), other) {
			t.Errorf("known_hosts of %s: %q", d.dir, data)
		}
	}
	two.use(t)
	if data, _ := os.ReadFile(knownHostsPath()); !strings.HasPrefix(string(data), "# my notes\n") {
		t.Errorf("the comment went: %q", data)
	}
}

// A host key trusted while a sync runs stays: the sync applies only what
// it changed.
func TestSyncHostKeyTrustedDuringSync(t *testing.T) {
	before := map[string]bool{"a ssh-ed25519 AAAA1": true}
	after := map[string]bool{"a ssh-ed25519 AAAA1": true, "b ssh-ed25519 AAAA2": true}
	data, changed := knownHostsChanged([]byte("a ssh-ed25519 AAAA1\nc ssh-ed25519 AAAA3\n"), after, before)
	if !changed || string(data) != "a ssh-ed25519 AAAA1\nc ssh-ed25519 AAAA3\nb ssh-ed25519 AAAA2\n" {
		t.Fatalf("%q %v", data, changed)
	}
	if _, changed := knownHostsChanged([]byte("a ssh-ed25519 AAAA1\n"), before, before); changed {
		t.Fatal("rewritten with nothing to change")
	}
}

// A private key replaced on one computer is replaced in the sync: the old
// one is deleted, and a computer that still has it does not add it back.
func TestSyncKeyFileReplaced(t *testing.T) {
	r := &memRemote{}
	homes := []string{t.TempDir(), t.TempDir()}
	oldKey, newKey := "old test key\n", "new test key\n"
	one := newDevice(t, r)
	useHome(t, homes[0])
	one.a.settings.Sync.Passwords = true
	h := one.addHost("h1", "web")
	keyPath := filepath.Join(homes[0], ".ssh", "id_test")
	h.Auth, h.KeyPath = config.AuthKey, keyPath
	one.a.saveHosts(one.a.hosts)
	os.MkdirAll(filepath.Dir(keyPath), 0o700)
	os.WriteFile(keyPath, []byte(oldKey), 0o600)
	one.sync(t)

	two := newDevice(t, r)
	useHome(t, homes[1])
	two.a.settings.Sync.Passwords = true
	two.sync(t)
	two.p.until(t, "the store", storeSettled(two.a, two.p))
	twoKey := filepath.Join(homes[1], ".ssh", "id_test")
	if data, _ := os.ReadFile(twoKey); string(data) != oldKey {
		t.Fatalf("the key was not installed: %q", data)
	}

	useHome(t, homes[0])
	os.WriteFile(keyPath, []byte(newKey), 0o600)
	one.sync(t)
	one.p.until(t, "the store", storeSettled(one.a, one.p))
	for i := 0; i < 2; i++ {
		useHome(t, homes[1])
		two.sync(t)
		two.p.until(t, "the store", storeSettled(two.a, two.p))
		useHome(t, homes[0])
		one.sync(t)
		one.p.until(t, "the store", storeSettled(one.a, one.p))
	}
	var keys []string
	for k := range remoteSecrets(t, r, one.a.sync.password) {
		if strings.HasPrefix(k, sshKeySecretPrefix) {
			keys = append(keys, k)
		}
	}
	if want := sshKeySecret("id_test", []byte(newKey)); len(keys) != 1 || keys[0] != want {
		t.Fatalf("the synced keys %v, want only %s", keys, want)
	}
	if data, _ := os.ReadFile(twoKey); string(data) != oldKey {
		t.Fatalf("the second computer's file was changed: %q", data)
	}
}
