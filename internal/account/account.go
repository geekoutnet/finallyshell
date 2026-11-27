// Package account signs the user in with a third-party account through
// the OAuth 2.0 device authorization grant (RFC 8628), which a desktop
// app can use without a client secret or a redirect: the app shows a
// code, the user enters it on the provider's site, and the app polls for
// the token. A Provider is one such service; GitHub is the first.
//
// The package keeps nothing itself: the app stores the Token in the
// system's credential store and the Profile in its settings.
package account

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Errors of the device flow and of using a token.
var (
	// ErrDenied: the user declined the request on the provider's site.
	ErrDenied = errors.New("account: the authorization was declined")
	// ErrExpired: the code expired before the user entered it.
	ErrExpired = errors.New("account: the code expired")
	// ErrRevoked: the token, or its refresh token, is no longer valid:
	// the user revoked it or it expired. The user signs in again.
	ErrRevoked = errors.New("account: the sign-in is no longer valid")
)

// DeviceCode is what the provider gave to start a sign-in: the user
// enters UserCode at VerificationURI, while the app polls with Code.
type DeviceCode struct {
	Code            string
	UserCode        string
	VerificationURI string
	// ExpiresAt is when the code stops working; Interval is how long to
	// wait between polls.
	ExpiresAt time.Time
	Interval  time.Duration
}

// Token is what a sign-in gives: the access token, and when the provider
// makes it expire, when it does and the token that renews it.
type Token struct {
	Access  string    `json:"access"`
	Refresh string    `json:"refresh,omitempty"`
	Expires time.Time `json:"expires,omitzero"` // zero: never
	// RefreshExpires is when Refresh stops working, zero for unknown.
	RefreshExpires time.Time `json:"refreshExpires,omitzero"`
	Scope          string    `json:"scope,omitempty"`
}

// refreshMargin is how long before it expires a token is renewed.
const refreshMargin = 5 * time.Minute

// NeedsRefresh says whether t expires within a few minutes of now, and
// can be renewed.
func (t Token) NeedsRefresh(now time.Time) bool {
	return t.Refresh != "" && !t.Expires.IsZero() && now.Add(refreshMargin).After(t.Expires)
}

// Profile is who signed in.
type Profile struct {
	// ID is the provider's stable ID of the user; Login the user name,
	// Name the display name, Email the primary address ("" when the
	// provider gave none).
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	// Emails are all the user's verified addresses, the primary one
	// among them.
	Emails []string `json:"emails,omitempty"`
	// URL is the user's page on the provider's site.
	URL string `json:"url,omitempty"`
}

// Provider is a service the user can sign in with.
type Provider interface {
	// ID names the provider in settings and credential keys: "github".
	ID() string
	// RequestCode starts a sign-in.
	RequestCode(ctx context.Context) (DeviceCode, error)
	// Poll asks once whether the user has entered the code: the token
	// when they have; ErrPending while they have not; ErrSlowDown when
	// the app polls too often; ErrDenied or ErrExpired when the sign-in
	// is over.
	Poll(ctx context.Context, code DeviceCode) (Token, error)
	// Refresh renews a token that expires: ErrRevoked when its refresh
	// token no longer works.
	Refresh(ctx context.Context, t Token) (Token, error)
	// Profile reads who the token belongs to: ErrRevoked when the token
	// no longer works.
	Profile(ctx context.Context, t Token) (Profile, error)
	// ManageURL is the page of the provider's site where the user can
	// see or revoke the app's access.
	ManageURL() string
}

// Providers are the services the app can sign in with, in the order it
// shows them. A new one is a Provider of its own, added here.
func Providers() []Provider { return []Provider{&GitHub{}} }

// Lookup is the provider of id, nil for none.
func Lookup(id string) Provider {
	for _, p := range Providers() {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// Errors of a single poll, which Wait handles.
var (
	ErrPending  = errors.New("account: waiting for the user")
	ErrSlowDown = errors.New("account: polling too often")
)

// Wait polls until the user has entered the code, it expires, or ctx is
// done, waiting code.Interval between polls, longer when told to slow
// down.
func Wait(ctx context.Context, p Provider, code DeviceCode) (Token, error) {
	interval := code.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if !code.ExpiresAt.IsZero() && time.Now().After(code.ExpiresAt) {
			return Token{}, ErrExpired
		}
		select {
		case <-ctx.Done():
			return Token{}, ctx.Err()
		case <-time.After(interval):
		}
		t, err := p.Poll(ctx, code)
		switch {
		case errors.Is(err, ErrPending):
			continue
		case errors.Is(err, ErrSlowDown):
			interval += 5 * time.Second
			continue
		}
		return t, err
	}
}

// MaskEmail shows enough of an address to recognize it: the first two
// characters of the name, then "***", and the domain.
// "geekoutnet@gmail.com" is "ge***@gmail.com".
func MaskEmail(email string) string {
	name, domain, ok := strings.Cut(email, "@")
	if !ok || name == "" {
		return ""
	}
	r := []rune(name)
	keep := min(2, len(r))
	if len(r) <= 2 {
		keep = 1
	}
	return string(r[:keep]) + "***@" + domain
}
