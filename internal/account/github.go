package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GitHubClientID is the client ID of Finallyshell's OAuth app on GitHub,
// with the device flow enabled. It is public: the device flow needs no
// client secret, for signing in or renewing the token.
const GitHubClientID = "Ov23li1ygl7Wrh3Es574"

// GitHubScopes are what the app asks for: reading and writing gists, for
// the account's sync, and reading the user's addresses, to show the
// primary one.
const GitHubScopes = "gist user:email"

// GitHub signs in with a GitHub account.
type GitHub struct {
	// ClientID is the OAuth app's, "" for GitHubClientID.
	ClientID string
	// Client is the HTTP client, nil for one with a 30-second timeout.
	Client *http.Client
	// WebURL and APIURL are GitHub's, "" for github.com: tests set them.
	WebURL, APIURL string
}

func (g *GitHub) ID() string { return "github" }

func (g *GitHub) ManageURL() string {
	return g.web() + "/settings/connections/applications/" + g.clientID()
}

func (g *GitHub) clientID() string {
	if g.ClientID == "" {
		return GitHubClientID
	}
	return g.ClientID
}

func (g *GitHub) web() string {
	if g.WebURL == "" {
		return "https://github.com"
	}
	return strings.TrimRight(g.WebURL, "/")
}

func (g *GitHub) api() string {
	if g.APIURL == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(g.APIURL, "/")
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// oauthResponse is the JSON of GitHub's device and token endpoints, which
// report errors with status 200 and an "error" field.
type oauthResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`

	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`

	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// form posts form to an OAuth endpoint of GitHub's site.
func (g *GitHub) form(ctx context.Context, path string, form url.Values) (*oauthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.web()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Finallyshell")
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var out oauthResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("account: github: %s: HTTP %d", path, resp.StatusCode)
	}
	if out.Error == "" && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("account: github: %s: HTTP %d", path, resp.StatusCode)
	}
	return &out, nil
}

func (r *oauthResponse) err() error {
	switch r.Error {
	case "":
		return nil
	case "authorization_pending":
		return ErrPending
	case "slow_down":
		return ErrSlowDown
	case "access_denied":
		return ErrDenied
	case "expired_token":
		return ErrExpired
	case "bad_refresh_token", "incorrect_client_credentials":
		return ErrRevoked
	}
	if r.ErrorDescription != "" {
		return fmt.Errorf("account: github: %s: %s", r.Error, r.ErrorDescription)
	}
	return fmt.Errorf("account: github: %s", r.Error)
}

func (r *oauthResponse) token(now time.Time) Token {
	t := Token{Access: r.AccessToken, Refresh: r.RefreshToken, Scope: r.Scope}
	if r.ExpiresIn > 0 {
		t.Expires = now.Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	if r.RefreshTokenExpiresIn > 0 {
		t.RefreshExpires = now.Add(time.Duration(r.RefreshTokenExpiresIn) * time.Second)
	}
	return t
}

func (g *GitHub) RequestCode(ctx context.Context) (DeviceCode, error) {
	r, err := g.form(ctx, "/login/device/code", url.Values{"client_id": {g.clientID()}, "scope": {GitHubScopes}})
	if err != nil {
		return DeviceCode{}, err
	}
	if err := r.err(); err != nil {
		return DeviceCode{}, err
	}
	if r.DeviceCode == "" || r.UserCode == "" {
		return DeviceCode{}, errors.New("account: github: no device code in the response")
	}
	uri := r.VerificationURI
	if uri == "" {
		uri = g.web() + "/login/device"
	}
	return DeviceCode{
		Code: r.DeviceCode, UserCode: r.UserCode, VerificationURI: uri,
		ExpiresAt: time.Now().Add(time.Duration(r.ExpiresIn) * time.Second),
		Interval:  time.Duration(r.Interval) * time.Second,
	}, nil
}

func (g *GitHub) Poll(ctx context.Context, code DeviceCode) (Token, error) {
	r, err := g.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id":   {g.clientID()},
		"device_code": {code.Code},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return Token{}, err
	}
	if err := r.err(); err != nil {
		return Token{}, err
	}
	if r.AccessToken == "" {
		return Token{}, errors.New("account: github: no access token in the response")
	}
	return r.token(time.Now()), nil
}

// Refresh renews the token: GitHub needs no client secret for a token
// the device flow gave. The old access and refresh tokens stop working.
func (g *GitHub) Refresh(ctx context.Context, t Token) (Token, error) {
	if t.Refresh == "" {
		return Token{}, ErrRevoked
	}
	r, err := g.form(ctx, "/login/oauth/access_token", url.Values{
		"client_id":     {g.clientID()},
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.Refresh},
	})
	if err != nil {
		return Token{}, err
	}
	if err := r.err(); err != nil {
		return Token{}, err
	}
	if r.AccessToken == "" {
		return Token{}, errors.New("account: github: no access token in the response")
	}
	return r.token(time.Now()), nil
}

// get reads an API path with the token into out.
func (g *GitHub) get(ctx context.Context, t Token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+t.Access)
	req.Header.Set("User-Agent", "Finallyshell")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrRevoked
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("account: github: GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

func (g *GitHub) Profile(ctx context.Context, t Token) (Profile, error) {
	var user struct {
		ID      int64  `json:"id"`
		Login   string `json:"login"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		HTMLURL string `json:"html_url"`
	}
	if err := g.get(ctx, t, "/user", &user); err != nil {
		return Profile{}, err
	}
	if user.ID == 0 || user.Login == "" {
		return Profile{}, errors.New("account: github: no user in the response")
	}
	p := Profile{ID: strconv.FormatInt(user.ID, 10), Login: user.Login, Name: user.Name, Email: user.Email, URL: user.HTMLURL}
	// The public address is often empty: the primary one needs the
	// user:email scope, which a token from before it lacks.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if g.get(ctx, t, "/user/emails", &emails) == nil {
		for _, e := range emails {
			if !e.Verified {
				continue
			}
			p.Emails = append(p.Emails, e.Email)
			if e.Primary {
				p.Email = e.Email
			}
		}
	}
	return p, nil
}
