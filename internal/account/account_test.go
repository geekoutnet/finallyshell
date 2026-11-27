package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is GitHub's device flow and user API, in memory.
type fakeGitHub struct {
	t          *testing.T
	mu         sync.Mutex
	polls      int
	pendingFor int    // polls that answer authorization_pending
	pollError  string // an error the token endpoint gives instead
	access     string // the access token that works now
	refresh    string
	emails     int // 200, or the status of /user/emails
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	r.ParseForm()
	switch r.URL.Path {
	case "/login/device/code":
		if r.Form.Get("client_id") != "cid" || r.Form.Get("scope") != GitHubScopes {
			f.t.Errorf("device code request: %v", r.Form)
		}
		reply(map[string]any{"device_code": "dev", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 0})
	case "/login/oauth/access_token":
		if r.Form.Get("client_secret") != "" {
			f.t.Error("a client secret was sent")
		}
		switch r.Form.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			f.polls++
			if f.polls <= f.pendingFor {
				reply(map[string]string{"error": "authorization_pending"})
				return
			}
			if f.pollError != "" {
				reply(map[string]string{"error": f.pollError})
				return
			}
			f.access, f.refresh = "gho_one", "ghr_one"
			reply(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "expires_in": 28800, "refresh_token_expires_in": 15897600, "scope": "gist,user:email", "token_type": "bearer"})
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.refresh {
				reply(map[string]string{"error": "bad_refresh_token"})
				return
			}
			f.access, f.refresh = "gho_two", "ghr_two"
			reply(map[string]any{"access_token": f.access, "refresh_token": f.refresh, "expires_in": 28800})
		}
	case "/user":
		if r.Header.Get("Authorization") != "Bearer "+f.access {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reply(map[string]any{"id": 13740941, "login": "octo", "name": "Octo Cat", "email": nil, "html_url": "https://github.com/octo"})
	case "/user/emails":
		if f.emails != 0 && f.emails != http.StatusOK {
			w.WriteHeader(f.emails)
			return
		}
		reply([]map[string]any{
			{"email": "other@example.com", "primary": false, "verified": true},
			{"email": "octocat@example.com", "primary": true, "verified": true},
		})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newFake(t *testing.T) (*fakeGitHub, *GitHub) {
	f := &fakeGitHub{t: t}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, &GitHub{ClientID: "cid", Client: srv.Client(), WebURL: srv.URL, APIURL: srv.URL}
}

func TestGitHubDeviceFlow(t *testing.T) {
	f, g := newFake(t)
	f.pendingFor = 2
	ctx := context.Background()
	code, err := g.RequestCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if code.UserCode != "ABCD-1234" || code.VerificationURI != "https://github.com/login/device" || code.Code != "dev" {
		t.Fatalf("code: %+v", code)
	}
	code.Interval = time.Millisecond
	tok, err := Wait(ctx, g, code)
	if err != nil {
		t.Fatal(err)
	}
	if f.polls != 3 || tok.Access != "gho_one" || tok.Refresh != "ghr_one" || tok.Expires.IsZero() || tok.RefreshExpires.IsZero() {
		t.Fatalf("after %d polls: %+v", f.polls, tok)
	}
	if tok.NeedsRefresh(time.Now()) || !tok.NeedsRefresh(time.Now().Add(8*time.Hour)) {
		t.Fatal("NeedsRefresh")
	}

	p, err := g.Profile(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	want := Profile{ID: "13740941", Login: "octo", Name: "Octo Cat", Email: "octocat@example.com", URL: "https://github.com/octo",
		Emails: []string{"other@example.com", "octocat@example.com"}}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("profile: %+v", p)
	}

	renewed, err := g.Refresh(ctx, tok)
	if err != nil || renewed.Access != "gho_two" || renewed.Refresh != "ghr_two" {
		t.Fatalf("refresh: %+v %v", renewed, err)
	}
	if _, err := g.Profile(ctx, tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("the old token: %v, want ErrRevoked", err)
	}
	if _, err := g.Refresh(ctx, tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("the used refresh token: %v, want ErrRevoked", err)
	}
}

func TestGitHubDeviceFlowEnds(t *testing.T) {
	for _, c := range []struct {
		error string
		want  error
	}{{"access_denied", ErrDenied}, {"expired_token", ErrExpired}} {
		f, g := newFake(t)
		f.pollError = c.error
		code, err := g.RequestCode(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		code.Interval = time.Millisecond
		if _, err := Wait(context.Background(), g, code); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.error, err)
		}
	}
	// Canceled while waiting.
	f, g := newFake(t)
	f.pendingFor = 1 << 30
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Wait(ctx, g, DeviceCode{Code: "dev", Interval: time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("canceled: %v", err)
	}
	// A code past its time.
	if _, err := Wait(context.Background(), g, DeviceCode{Code: "dev", ExpiresAt: time.Now().Add(-time.Second)}); !errors.Is(err, ErrExpired) {
		t.Errorf("expired code: %v", err)
	}
}

// TestGitHubProfileWithoutEmails: a token without user:email still signs
// in, with no address.
func TestGitHubProfileWithoutEmails(t *testing.T) {
	f, g := newFake(t)
	f.access, f.emails = "gho_x", http.StatusForbidden
	p, err := g.Profile(context.Background(), Token{Access: "gho_x"})
	if err != nil || p.Login != "octo" || p.Email != "" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"geekoutnet@gmail.com": "ge***@gmail.com",
		"ab@x.io":              "a***@x.io",
		"a@x.io":               "a***@x.io",
		"张三丰@例子.cn":            "张三***@例子.cn",
		"":                     "",
		"nodomain":             "",
	} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLookup(t *testing.T) {
	if p := Lookup("github"); p == nil || p.ID() != "github" {
		t.Fatal("github")
	}
	if Lookup("nope") != nil {
		t.Fatal("unknown provider")
	}
}
