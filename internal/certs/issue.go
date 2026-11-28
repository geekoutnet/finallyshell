package certs

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/lego"
	legolog "github.com/go-acme/lego/v4/log"
	"github.com/go-acme/lego/v4/registration"
)

// The directories of Let's Encrypt: the real one, and the test one, whose
// certificates nothing trusts but whose limits are far higher.
const (
	LetsEncrypt        = lego.LEDirectoryProduction
	LetsEncryptStaging = lego.LEDirectoryStaging
)

// Issuer gets certificates from an ACME CA, Let's Encrypt by default, for
// an account it keeps in Dir.
type Issuer struct {
	// Dir keeps the accounts, one for each CA: the store's folder.
	Dir string
	// CA is the CA's directory URL, "" for LetsEncrypt.
	CA string
	// Transport makes the requests to the CA, nil for the default one;
	// tests trust their CA with it.
	Transport http.RoundTripper

	// precheck, when not nil, replaces lego's check that a TXT record has
	// spread: Pebble's test DNS answers no SOA query, which lego's asks.
	precheck func(fqdn, value string) (bool, error)
}

// Step is how far an issuance has gone.
type Step string

const (
	StepAccount Step = "account" // the account at the CA, made on the first issuance
	StepOrder   Step = "order"   // asking for the certificate
	StepProve   Step = "prove"   // proving a domain: Detail names it
	StepRecord  Step = "record"  // a TXT record to add by hand: Detail is its name, Value its value
	StepWait    Step = "wait"    // waiting for the CA to check the proofs
	StepIssue   Step = "issue"   // the proofs checked, the certificate being issued
)

// Progress is an issuance's news.
type Progress struct {
	Step   Step
	Detail string
	Value  string
}

// Request is a certificate to get.
type Request struct {
	Domains []string
	KeyType KeyType
	// One of HTTP and DNS proves the domains: a file served at
	// http://<domain>/.well-known/acme-challenge/<token>, or a TXT record
	// at _acme-challenge.<domain>. A wildcard needs DNS.
	HTTP challenge.Provider
	DNS  challenge.Provider
	// Resolvers are the name servers, "host:port", that tell when a DNS
	// record has spread, before the CA is asked to check it; none for the
	// system's, then public ones.
	Resolvers []string
	// Progress, when not nil, hears how far it has gone; Log, the CA
	// client's messages. Neither may block.
	Progress func(Progress)
	Log      func(string)
}

// issueMu makes issuances one at a time: lego logs through a package
// variable, which an issuance points at its own Log.
var issueMu sync.Mutex

// Issue gets a certificate for r, with a new key. Cancelling ctx stops it
// at the next request to the CA, and stops a provider that watches it.
func (is *Issuer) Issue(ctx context.Context, r Request) (Bundle, error) {
	domains, err := NormalizeDomains(r.Domains, false)
	if err != nil {
		return Bundle{}, err
	}
	if (r.HTTP == nil) == (r.DNS == nil) {
		return Bundle{}, errors.New("certs: give one of HTTP and DNS")
	}
	if r.HTTP != nil {
		for _, d := range domains {
			if IsWildcard(d) {
				return Bundle{}, ErrWildcardNeedsDNS
			}
		}
	}
	progress := func(p Progress) {
		if r.Progress != nil {
			r.Progress(p)
		}
	}
	issueMu.Lock()
	defer issueMu.Unlock()
	legolog.Logger = log.New(logWriter(r.Log), "", 0)
	defer func() { legolog.Logger = log.New(io.Discard, "", 0) }()

	progress(Progress{Step: StepAccount})
	user, err := is.account()
	if err != nil {
		return Bundle{}, err
	}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = is.ca()
	cfg.UserAgent = "Finallyshell"
	base := is.Transport
	if base == nil {
		base = cfg.HTTPClient.Transport
	}
	cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second, Transport: ctxTransport{ctx: ctx, base: base}}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return Bundle{}, caError(ctx, err)
	}
	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return Bundle{}, caError(ctx, err)
		}
		user.Registration = reg
		if err := is.saveAccount(user); err != nil {
			return Bundle{}, err
		}
	}
	if r.HTTP != nil {
		err = client.Challenge.SetHTTP01Provider(watched{r.HTTP, progress})
	} else {
		// The wait for a record to spread asks no CA, so it watches ctx
		// itself: lego's wait goes on through errors, and stops only when
		// told to.
		opts := []dns01.ChallengeOption{dns01.WrapPreCheck(func(_, fqdn, value string, check dns01.PreCheckFunc) (bool, error) {
			if err := ctx.Err(); err != nil {
				return true, err
			}
			if is.precheck != nil {
				return is.precheck(fqdn, value)
			}
			return check(fqdn, value)
		})}
		if len(r.Resolvers) > 0 {
			opts = append(opts, dns01.AddRecursiveNameservers(r.Resolvers))
		}
		err = client.Challenge.SetDNS01Provider(watched{r.DNS, progress}, opts...)
	}
	if err != nil {
		return Bundle{}, err
	}
	key, err := NewKey(r.KeyType)
	if err != nil {
		return Bundle{}, err
	}
	progress(Progress{Step: StepOrder})
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, PrivateKey: key, Bundle: true})
	if err != nil {
		return Bundle{}, caError(ctx, err)
	}
	chain, err := ParseChain(res.Certificate)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Key: key, Chain: chain}, nil
}

// ErrWildcardNeedsDNS says that a wildcard is proved by a TXT record alone.
var ErrWildcardNeedsDNS = errors.New("certs: a wildcard domain is proved with a DNS record")

func (is *Issuer) ca() string {
	if is.CA == "" {
		return LetsEncrypt
	}
	return is.CA
}

// caError is err, or ctx's error once ctx is done: what stopped it.
func caError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// ctxTransport sends requests with ctx, so that cancelling it stops them.
type ctxTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t ctxTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req.WithContext(t.ctx))
}

// watched tells progress what a provider does.
type watched struct {
	p        challenge.Provider
	progress func(Progress)
}

func (w watched) Present(domain, token, keyAuth string) error {
	w.progress(Progress{Step: StepProve, Detail: domain})
	return w.p.Present(domain, token, keyAuth)
}

func (w watched) CleanUp(domain, token, keyAuth string) error {
	w.progress(Progress{Step: StepIssue, Detail: domain})
	return w.p.CleanUp(domain, token, keyAuth)
}

func (w watched) Timeout() (timeout, interval time.Duration) {
	if t, ok := w.p.(challenge.ProviderTimeout); ok {
		return t.Timeout()
	}
	return 2 * time.Minute, 4 * time.Second
}

// logWriter hands each line lego logs to f, nil for none.
func logWriter(f func(string)) io.Writer {
	if f == nil {
		return io.Discard
	}
	return lineWriter(f)
}

type lineWriter func(string)

func (w lineWriter) Write(p []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			w(line)
		}
	}
	return len(p), nil
}

// ManualDNS is the TXT records to add by hand. Each one goes to Record as
// it is needed; Issue then waits, up to Wait (30 minutes when 0), until
// the domain's name servers give it, and checks it again every 10 seconds.
type ManualDNS struct {
	Record func(name, value string)
	Wait   time.Duration
}

func (m ManualDNS) Present(domain, _, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	if m.Record != nil {
		m.Record(strings.TrimSuffix(info.FQDN, "."), info.Value)
	}
	return nil
}

func (ManualDNS) CleanUp(string, string, string) error { return nil }

func (m ManualDNS) Timeout() (timeout, interval time.Duration) {
	if m.Wait > 0 {
		return m.Wait, 10 * time.Second
	}
	return 30 * time.Minute, 10 * time.Second
}

// account is the Issuer's account at its CA, the one kept or a new one
// (with no Registration until it is registered).
func (is *Issuer) account() (*acmeUser, error) {
	data, err := os.ReadFile(is.accountPath())
	if errors.Is(err, os.ErrNotExist) {
		key, err := NewKey(KeyEC256)
		if err != nil {
			return nil, err
		}
		return &acmeUser{key: key}, nil
	}
	if err != nil {
		return nil, err
	}
	var saved savedAccount
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("certs: the account: %w", err)
	}
	block, _ := pem.Decode([]byte(saved.Key))
	if block == nil {
		return nil, errors.New("certs: the account has no key")
	}
	key, err := parseKey(block)
	if err != nil {
		return nil, err
	}
	return &acmeUser{key: key, Registration: saved.Registration}, nil
}

func (is *Issuer) saveAccount(u *acmeUser) error {
	der, err := x509.MarshalPKCS8PrivateKey(u.key)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(savedAccount{
		CA:           is.ca(),
		Key:          string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		Registration: u.Registration,
	}, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(is.accountPath(), append(data, '\n'))
}

// accountPath is the file of the account at the Issuer's CA: one for each
// CA, named after its host and path.
func (is *Issuer) accountPath() string {
	name := "account"
	if u, err := url.Parse(is.ca()); err == nil {
		name = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
				return r
			}
			return '_'
		}, u.Host+u.Path)
	}
	return filepath.Join(is.Dir, "accounts", name+".json")
}

type savedAccount struct {
	CA           string                 `json:"ca"`
	Key          string                 `json:"key"`
	Registration *registration.Resource `json:"registration"`
}

// acmeUser is lego's account: no email address, which Let's Encrypt no
// longer needs.
type acmeUser struct {
	key          crypto.Signer
	Registration *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return "" }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }
