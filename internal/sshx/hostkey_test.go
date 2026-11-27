package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// TestTrustHostKey: a changed host key fails with the new key's record,
// and trusting it replaces the host's old keys, plain or hashed, keeping
// every other line.
func TestTrustHostKey(t *testing.T) {
	newKey := func() ssh.PublicKey {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		key, err := ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	host := knownhosts.Normalize("203.0.113.7:22")
	old, other, current := newKey(), newKey(), newKey()
	path := filepath.Join(t.TempDir(), "known_hosts")
	lines := []string{
		"# my servers",
		knownhosts.Line([]string{host}, old),
		knownhosts.Line([]string{knownhosts.HashHostname(host)}, old),
		knownhosts.Line([]string{"other.example"}, other),
		"@revoked " + knownhosts.Line([]string{host}, other),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	addr := &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 22}
	connect := func(key ssh.PublicKey) error {
		hostKey, err := hostKeyCallback(Options{KnownHosts: path})
		if err != nil {
			t.Fatal(err)
		}
		return hostKey("203.0.113.7:22", addr, key)
	}
	err := connect(current)
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("a changed key: %v", err)
	}
	if changed.Host != host || changed.Line != knownhosts.Line([]string{host}, current) {
		t.Fatalf("the new key's record: %q %q", changed.Host, changed.Line)
	}

	if err := TrustHostKey(changed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{lines[0], lines[3], lines[4], changed.Line}
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("known_hosts after trusting:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := connect(current); err != nil {
		t.Fatalf("the trusted key: %v", err)
	}
}
