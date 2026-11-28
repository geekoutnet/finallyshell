package certs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/challenge/dns01"
)

// The issuance tests run against Pebble, Let's Encrypt's test CA, and its
// challenge test server, which are not here unless PEBBLE_DIRECTORY (the
// directory URL) and CHALLTESTSRV (the test server's management URL) say
// where; the test server's DNS answers on CHALLTESTSRV_DNS, and Pebble
// checks HTTP challenges on port 5002 of the domain, which the test server
// points at this machine. tools/pebble-test.sh runs them in Docker.
func pebble(t *testing.T) (*Issuer, string, string) {
	t.Helper()
	dir, mgmt, dns := os.Getenv("PEBBLE_DIRECTORY"), os.Getenv("CHALLTESTSRV"), os.Getenv("CHALLTESTSRV_DNS")
	if dir == "" || mgmt == "" || dns == "" {
		t.Skip("no Pebble: set PEBBLE_DIRECTORY, CHALLTESTSRV and CHALLTESTSRV_DNS")
	}
	// Pebble's TLS certificate is its own test CA's.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	// The test DNS answers TXT queries, not the SOA ones lego's check of a
	// record asks first: the record is looked up there directly.
	res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, dns)
	}}
	precheck := func(fqdn, value string) (bool, error) {
		txts, err := res.LookupTXT(context.Background(), fqdn)
		return slices.Contains(txts, value), err
	}
	return &Issuer{Dir: t.TempDir(), CA: dir, Transport: tr, precheck: precheck}, mgmt, dns
}

// challtest calls the test server's management API.
func challtest(t *testing.T, mgmt, path string, body map[string]string) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(mgmt+path, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Errorf("challtestsrv %s: %v", path, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("challtestsrv %s: %s", path, resp.Status)
	}
}

// testDNS adds and removes TXT records on the test server, as a DNS
// provider's API does.
type testDNS struct {
	t    *testing.T
	mgmt string
}

func (d testDNS) Present(domain, _, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	challtest(d.t, d.mgmt, "/set-txt", map[string]string{"host": info.EffectiveFQDN, "value": info.Value})
	return nil
}

func (d testDNS) CleanUp(domain, _, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)
	challtest(d.t, d.mgmt, "/clear-txt", map[string]string{"host": info.EffectiveFQDN})
	return nil
}

// testHTTP serves HTTP challenges on port 5002, as the connected server
// does for the app.
type testHTTP struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (h *testHTTP) Present(_, token, keyAuth string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens[token] = keyAuth
	return nil
}

func (h *testHTTP) CleanUp(_, token, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tokens, token)
	return nil
}

func (h *testHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	keyAuth, ok := h.tokens[strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")]
	h.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Write([]byte(keyAuth))
}

func checkIssued(t *testing.T, b Bundle, domains []string) {
	t.Helper()
	leaf := b.Leaf()
	if leaf == nil || len(b.Chain) < 2 {
		t.Fatalf("chain of %d", len(b.Chain))
	}
	if !slices.Equal(leaf.DNSNames, domains) && !slices.Equal(slices.Sorted(slices.Values(leaf.DNSNames)), slices.Sorted(slices.Values(domains))) {
		t.Fatalf("names %q, want %q", leaf.DNSNames, domains)
	}
	if err := leaf.CheckSignatureFrom(b.Chain[1]); err != nil {
		t.Fatalf("not signed by the next one: %v", err)
	}
	// The key is the certificate's.
	if _, err := tls.X509KeyPair(b.FullChainPEM(), must(b.KeyPEM())); err != nil {
		t.Fatalf("key and certificate: %v", err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestIssueHTTP(t *testing.T) {
	is, _, _ := pebble(t)
	h := &testHTTP{tokens: map[string]string{}}
	srv := &http.Server{Addr: ":5002", Handler: h}
	go srv.ListenAndServe()
	defer srv.Close()

	var steps []Step
	var mu sync.Mutex
	b, err := is.Issue(context.Background(), Request{
		Domains: []string{"web.example.test", "www.web.example.test"}, KeyType: KeyRSA2048, HTTP: h,
		Progress: func(p Progress) { mu.Lock(); steps = append(steps, p.Step); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	checkIssued(t, b, []string{"web.example.test", "www.web.example.test"})
	for _, want := range []Step{StepAccount, StepOrder, StepProve, StepIssue} {
		if !slices.Contains(steps, want) {
			t.Errorf("no step %q in %q", want, steps)
		}
	}
	// The account is kept, and used again.
	acct, err := os.ReadFile(is.accountPath())
	if err != nil || !bytes.Contains(acct, []byte(`"uri"`)) {
		t.Fatalf("account file: %v", err)
	}
	if _, err := is.Issue(context.Background(), Request{Domains: []string{"again.example.test"}, HTTP: h}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(is.accountPath()); !bytes.Equal(again, acct) {
		t.Fatal("a second issuance made another account")
	}

	if _, err := is.Issue(context.Background(), Request{Domains: []string{"*.example.test"}, HTTP: h}); !errors.Is(err, ErrWildcardNeedsDNS) {
		t.Fatalf("wildcard by HTTP: %v", err)
	}
}

func TestIssueDNS(t *testing.T) {
	is, mgmt, dns := pebble(t)
	domains := []string{"dns.example.test", "*.dns.example.test"}
	b, err := is.Issue(context.Background(), Request{Domains: domains, DNS: testDNS{t, mgmt}, Resolvers: []string{dns}})
	if err != nil {
		t.Fatal(err)
	}
	checkIssued(t, b, domains)
}

// The records to add by hand are given; once added, the issuance goes on.
func TestIssueManualDNS(t *testing.T) {
	is, mgmt, dns := pebble(t)
	var mu sync.Mutex
	var records []string
	manual := ManualDNS{Wait: time.Minute, Record: func(name, value string) {
		mu.Lock()
		records = append(records, name)
		mu.Unlock()
		// The user adds it a moment later.
		go func() {
			time.Sleep(2 * time.Second)
			challtest(t, mgmt, "/set-txt", map[string]string{"host": name + ".", "value": value})
		}()
	}}
	b, err := is.Issue(context.Background(), Request{Domains: []string{"manual.example.test"}, DNS: manual, Resolvers: []string{dns}})
	if err != nil {
		t.Fatal(err)
	}
	checkIssued(t, b, []string{"manual.example.test"})
	if !slices.Equal(records, []string{"_acme-challenge.manual.example.test"}) {
		t.Fatalf("records %q", records)
	}
}

func TestIssueCancel(t *testing.T) {
	is, _, dns := pebble(t)
	ctx, cancel := context.WithCancel(context.Background())
	manual := ManualDNS{Wait: time.Minute, Record: func(string, string) { cancel() }} // never added
	start := time.Now()
	_, err := is.Issue(ctx, Request{Domains: []string{"cancel.example.test"}, DNS: manual, Resolvers: []string{dns}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled issuance: %v", err)
	}
	// It stops at the next check of the record, 10 seconds apart.
	if time.Since(start) > 25*time.Second {
		t.Fatalf("stopped after %v", time.Since(start))
	}
}
