package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// Installed copies accept only updates signed with the key whose public
// half is in mygo.json, read from the latest release of the repository
// there: changing either strands them at their version.
func TestUpdatesConfig(t *testing.T) {
	data, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Updates struct {
			PublicKey  string `json:"publicKey"`
			GitHub     string `json:"github"`
			PrivateKey string `json:"privateKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	u := cfg.Updates
	if u.PublicKey != "fwcqse2POLiSdWytl0BHeURrztxcDObwmos+4IlTqHQ=" || u.GitHub != "TooonyChen/finallyshell" {
		t.Errorf("updates %+v", u)
	}
	if u.PrivateKey != "" {
		t.Errorf("the secret key is MYGO_UPDATER_PRIVATE_KEY in CI, not a file: %q", u.PrivateKey)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.0.2", "2.0.1", true},
		{"v2.0.10", "2.0.9", true},
		{"2.1.0", "2.0.99", true},
		{"3.0.0", "2.9.9", true},
		{"2.0.1", "2.0.1", false},
		{"2.0.0", "2.0.1", false},
		{"0.4.0", "2.0.1", false},
		{"2.0.2-preview.1", "2.0.1", true},
		{"latest", "2.0.1", false},
		{"2.0.2", "", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestLatestRelease reads releaseRepo's latest release on GitHub, when
// FINALLYSHELL_TEST_NET is set.
func TestLatestRelease(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_NET") == "" {
		t.Skip("FINALLYSHELL_TEST_NET is not set")
	}
	v, err := latestRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseVersion(v); !ok {
		t.Fatalf("latest release %q is not a version", v)
	}
	t.Logf("latest release of %s: %s", releaseRepo, v)
}
