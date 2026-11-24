package sshx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestManagedKeyRoundTrip(t *testing.T) {
	for _, kind := range []string{"ed25519", "rsa"} {
		t.Run(kind, func(t *testing.T) {
			private, public, err := GenerateKey(kind, "fixture@workstation", "fixture passphrase")
			if err != nil {
				t.Fatal(err)
			}
			var missing *ssh.PassphraseMissingError
			if _, err := ssh.ParsePrivateKey(private); !errors.As(err, &missing) {
				t.Fatalf("expected encrypted key, got %v", err)
			}
			signer, err := ssh.ParsePrivateKeyWithPassphrase(private, []byte("fixture passphrase"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ssh.ParsePrivateKeyWithPassphrase(private, []byte("wrong")); err == nil {
				t.Fatal("wrong passphrase accepted")
			}
			path := filepath.Join(t.TempDir(), "id_fixture")
			if err := WriteKeyPair(path, private, public, false, time.Now()); err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" {
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("unexpected file info: %v, %v", info, err)
				}
			}
			key, err := InspectKey(path)
			if err != nil || !key.Encrypted || key.Comment != "fixture@workstation" || key.Fingerprint() != ssh.FingerprintSHA256(signer.PublicKey()) || !strings.Contains(key.RandomArt(), "E") {
				t.Fatalf("unexpected metadata: %+v, %v", key, err)
			}
			if want := map[string]int{"ed25519": 256, "rsa": 4096}[kind]; key.Bits() != want {
				t.Fatalf("bits: %d, want %d", key.Bits(), want)
			}
			stamp := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
			if err := WriteKeyPair(path, private, public, false, stamp); !errors.Is(err, os.ErrExist) {
				t.Fatalf("overwrote without confirmation: %v", err)
			}
			if err := WriteKeyPair(path, private, public, true, stamp); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path + ".bak-20260304050607"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedKeyNames(t *testing.T) {
	for _, name := range []string{"../id_key", "id_key.pub", "id_key.bak-123", `folder\key`, "a:b", "NUL.pem", "foo.", ""} {
		if ValidKeyName(name) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"id_ed25519", "work-key.pem", "Key_123"} {
		if !ValidKeyName(name) {
			t.Errorf("rejected %q", name)
		}
	}
}

func TestManagedKeyImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id_import")
	private, public, err := GenerateKey("ed25519", "fixture", "secret fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteKeyPair(path, private, public, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportKey(path, ""); !errors.Is(err, ErrNeedPassphrase) {
		t.Fatalf("encrypted import unexpectedly allowed: %v", err)
	}
	if _, _, err := ImportKey(path, "wrong fixture"); err == nil {
		t.Fatal("wrong passphrase was allowed")
	}
	if restored, line, err := ImportKey(path, "secret fixture"); err != nil || string(restored) != string(private) || string(line) != string(public) {
		t.Fatalf("import did not preserve the key pair: %v", err)
	}
	_, mismatch, err := GenerateKey("ed25519", "other fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".pub", mismatch, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ImportKey(path, "secret fixture"); err == nil {
		t.Fatal("import accepted an unrelated public key")
	}
}
