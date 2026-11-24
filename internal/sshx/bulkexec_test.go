package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestBulkStrictKnownHosts(t *testing.T) {
	key := func() ssh.PublicKey {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		result, err := ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if _, err := bulkHostKeyCallback(path); !errors.Is(err, ErrBulkHostKey) {
		t.Fatalf("missing file: %v", err)
	}
	trusted, changed := key(), key()
	line := knownhosts.Line([]string{"example.invalid"}, trusted) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	check, err := bulkHostKeyCallback(path)
	if err != nil {
		t.Fatal(err)
	}
	address := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 22}
	for _, test := range []struct {
		name, host string
		key        ssh.PublicKey
		want       error
	}{
		{"known", "example.invalid:22", trusted, nil},
		{"changed", "example.invalid:22", changed, ErrBulkHostKey},
		{"unknown", "unknown.invalid:22", trusted, ErrBulkHostKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := check(test.host, address, test.key); !errors.Is(err, test.want) {
				t.Fatalf("check: %v, want %v", err, test.want)
			}
		})
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != line {
		t.Fatal("strict check modified known_hosts")
	}
}

func TestBulkAddressValidation(t *testing.T) {
	for _, test := range []struct {
		host string
		port int
		ok   bool
	}{
		{"example.invalid", 22, true},
		{"2001:db8::1", 2222, true},
		{"fe80::1%eth0", 22, true},
		{"", 22, false},
		{"example.invalid\r\nX-Injected: test", 22, false},
		{"example.invalid/proxy", 22, false},
		{"example.invalid", 65536, false},
	} {
		if got := bulkAddressValid(test.host, test.port); got != test.ok {
			t.Errorf("address %q, port %d: %v", test.host, test.port, got)
		}
	}
}
