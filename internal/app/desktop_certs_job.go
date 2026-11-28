package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/certs"
	"finallyshell/locales"

	"github.com/go-acme/lego/v4/challenge"
)

// An issuance: the way to prove the domains chosen on its own, then the
// CA asked, step by step. It runs on a goroutine; its state changes on the
// UI thread alone, through App.post.

// certsStage is how far an issuance has gone.
type certsStage int

const (
	certsStageDetect  certsStage = iota // choosing how to prove the domains
	certsStageConfirm                   // waiting for the user to agree to a change to the server
	certsStageProve                     // the proofs being put in place (or the TXT records added by hand)
	certsStageWait                      // the CA checking them
	certsStageIssue                     // the certificate being issued
	certsStageDone
	certsStageFailed
	certsStageCanceled
)

// certsVia is how an issuance proves its domains.
type certsVia int

const (
	certsViaNone   certsVia = iota
	certsViaDNS             // a DNS provider's API
	certsViaHTTP            // the connected server
	certsViaManual          // TXT records added by hand
)

// certsJob is an issuance.
type certsJob struct {
	domains []string
	keyType certs.KeyType
	staging bool
	server  string // the session's name, for a proof over HTTP

	stage certsStage
	// failedAt is the stage it failed at.
	failedAt certsStage
	via      certsVia
	provider *certs.DNSProvider // of certsViaDNS
	httpMode certHTTPMode       // of certsViaHTTP
	// reason says, in a sentence, why it proves the domains as it does;
	// hint, when not empty, how to make it automatic next time (a DNS
	// provider to give credentials to), with hintProvider.
	reason       string
	hint         string
	hintProvider *certs.DNSProvider
	// records are the TXT records to add by hand.
	records []certsRecord
	// confirm is the change to the server waiting for the user's go-ahead,
	// answered on answer.
	confirm *certHTTPConfirm
	servers []string // the programs on port 80, which a split leaves alone
	answer  chan certsAnswer
	err     error
	saved   certs.Cert
	cancel  context.CancelFunc
	// renew is the certificate it replaces, "" for a new one.
	renew string
}

type certsRecord struct{ name, value string }

// certsAnswer is the user's answer to a change to the server.
type certsAnswer int

const (
	certsAnswerApply  certsAnswer = iota + 1 // make the change
	certsAnswerManual                        // prove the domains with TXT records instead
)

func (j *certsJob) running() bool {
	return j.stage < certsStageDone
}

// certsStart issues a certificate for domains, from the desktop of s.
func (a *App) certsStart(s *Session, domains []string, keyType certs.KeyType, staging bool, renew string) {
	st := &a.certs
	if st.job != nil && st.job.running() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &certsJob{domains: domains, keyType: keyType, staging: staging, cancel: cancel, renew: renew, answer: make(chan certsAnswer, 1)}
	st.job = job

	wildcard := slices.ContainsFunc(domains, certs.IsWildcard)
	// What the goroutine reads, taken here on the UI thread.
	dns := map[string]map[string]string{}
	for id, values := range st.dns {
		dns[id] = values
	}
	var findHTTP func(context.Context, []string) certHTTPFound
	if s != nil && s.status == statusConnected && execAllowed(s.Host) && !wildcard {
		job.server = s.label()
		if st.findHTTP != nil {
			find := st.findHTTP
			findHTTP = func(ctx context.Context, d []string) certHTTPFound { return find(ctx, s, d) }
		} else {
			run := a.sitesRoot(s)
			findHTTP = func(ctx context.Context, d []string) certHTTPFound { return certFindHTTP(ctx, run, d, certFetch) }
		}
	}
	lookup := st.nameservers
	if lookup == nil {
		lookup = func(ctx context.Context, domain string) ([]string, error) { return certs.Nameservers(ctx, nil, domain) }
	}
	issue := st.issue
	if issue == nil {
		issue = func(ctx context.Context, staging bool, r certs.Request) (certs.Bundle, error) {
			is := &certs.Issuer{Dir: certsStore().Dir}
			if staging {
				is.CA = certs.LetsEncryptStaging
			}
			return is.Issue(ctx, r)
		}
	}
	post := func(f func()) {
		a.post(func() {
			if st.job == job {
				f()
			}
			a.redraw()
		})
	}
	go a.certsRun(ctx, job, post, dns, wildcard, findHTTP, lookup, issue)
}

// certsRun is the issuance's goroutine.
func (a *App) certsRun(ctx context.Context, job *certsJob, post func(func()), dns map[string]map[string]string, wildcard bool,
	findHTTP func(context.Context, []string) certHTTPFound, lookup func(context.Context, string) ([]string, error),
	issue func(context.Context, bool, certs.Request) (certs.Bundle, error)) {
	defer job.cancel()
	fail := func(stage certsStage, err error) {
		// Whether the user stopped it, read now: the deferred cancel ends
		// ctx before the UI thread runs this.
		canceled := ctx.Err() != nil
		post(func() {
			if canceled {
				job.stage, job.failedAt = certsStageCanceled, stage
				return
			}
			job.stage, job.failedAt, job.err = certsStageFailed, stage, err
		})
	}

	// Who hosts the domain: a provider with credentials proves it on its
	// own.
	var hosted *certs.DNSProvider
	lookupCtx, lookupCancel := context.WithTimeout(ctx, certsLookupTimeout)
	if ns, err := lookup(lookupCtx, job.domains[0]); err == nil {
		hosted = certs.HostedBy(ns)
	}
	lookupCancel()
	if ctx.Err() != nil {
		fail(certsStageDetect, ctx.Err())
		return
	}
	ready := func(p *certs.DNSProvider) bool { return p != nil && certsValuesReady(p, dns[p.ID]) }

	var provider challenge.Provider
	var httpH *certHTTP
	via, reason, hint := certsViaNone, "", ""
	var hintProvider *certs.DNSProvider
	switch {
	case ready(hosted):
		p, err := hosted.Provider(dns[hosted.ID])
		if err != nil {
			fail(certsStageDetect, err)
			return
		}
		provider, via, reason = p, certsViaDNS, locales.CertsWhyDNS(hosted.Name)
	case findHTTP != nil:
		found := findHTTP(ctx, job.domains)
		if found.confirm != nil {
			// A change to the server: the user's go-ahead first.
			post(func() { job.stage, job.confirm, job.servers = certsStageConfirm, found.confirm, found.servers })
			var answer certsAnswer
			select {
			case answer = <-job.answer:
			case <-ctx.Done():
				fail(certsStageConfirm, ctx.Err())
				return
			}
			if answer == certsAnswerApply {
				post(func() { job.stage, job.confirm = certsStageProve, nil })
				h, err := found.confirm.apply(ctx)
				if err != nil {
					fail(certsStageProve, err)
					return
				}
				found.h = h
			} else {
				found.err = errCertsDeclined
			}
		}
		if found.h != nil {
			httpH, via, reason = found.h, certsViaHTTP, certsWhyHTTP(found.h.mode)
			break
		}
		reason = certsWhyNotHTTP(found)
	default:
		if wildcard {
			reason = locales.CertsWhyWildcard()
		} else {
			reason = locales.CertsWhyNoServer()
		}
	}
	if httpH != nil {
		defer httpH.close()
	}
	if via == certsViaNone {
		// A TXT record by hand; and a provider that could do it, next time.
		if hosted != nil {
			hint, hintProvider = locales.CertsHintProvider(hosted.Name), hosted
		}
		via = certsViaManual
	}
	var mode certHTTPMode
	if httpH != nil {
		mode = httpH.mode
	}
	post(func() {
		job.via, job.reason, job.hint, job.hintProvider, job.httpMode, job.stage = via, reason, hint, hintProvider, mode, certsStageProve
		if via == certsViaDNS {
			job.provider = hosted
		}
	})

	r := certs.Request{Domains: job.domains, KeyType: job.keyType}
	switch via {
	case certsViaDNS:
		r.DNS = provider
	case certsViaHTTP:
		r.HTTP = httpH
	case certsViaManual:
		r.DNS = certs.ManualDNS{Record: func(name, value string) {
			post(func() {
				if !slices.Contains(job.records, certsRecord{name, value}) {
					job.records = append(job.records, certsRecord{name, value})
				}
			})
		}}
	}
	var stageMu sync.Mutex
	stage := certsStageProve
	advance := func(next certsStage) {
		stageMu.Lock()
		stage = max(stage, next)
		s := stage
		stageMu.Unlock()
		post(func() {
			if job.running() {
				job.stage = s
			}
		})
	}
	// Once a proof is in place the CA's check, or the wait for the records
	// added by hand, starts.
	if r.DNS != nil {
		r.DNS = certsWatch(r.DNS, func() { advance(certsStageWait) })
	} else {
		r.HTTP = certsWatch(r.HTTP, func() { advance(certsStageWait) })
	}
	r.Progress = func(p certs.Progress) {
		var next certsStage
		switch p.Step {
		case certs.StepProve:
			next = certsStageProve
		case certs.StepWait:
			next = certsStageWait
		case certs.StepIssue:
			next = certsStageIssue
		default:
			return
		}
		advance(next)
	}
	b, err := issue(ctx, job.staging, r)
	if err != nil {
		stageMu.Lock()
		at := stage
		stageMu.Unlock()
		fail(at, err)
		return
	}
	cert := certs.Cert{Domains: job.domains, KeyType: job.keyType, Staging: job.staging}
	switch via {
	case certsViaDNS:
		cert.Method, cert.Provider = certs.MethodDNS, hosted.ID
	case certsViaHTTP:
		cert.Method, cert.Server = certs.MethodHTTP, job.server
	default:
		cert.Method = certs.MethodDNSManual
	}
	post(func() {
		saved, err := certsStore().Save(cert, b, a.certs.clock())
		if err != nil {
			job.stage, job.failedAt, job.err = certsStageFailed, certsStageIssue, err
			return
		}
		job.stage, job.saved = certsStageDone, saved
		a.certsReload()
		a.certs.selected = saved.ID
	})
}

// certsLookupTimeout bounds the look for the domain's name servers.
const certsLookupTimeout = 8 * time.Second

// errCertsDeclined says the user chose TXT records over a change to the
// server.
var errCertsDeclined = errors.New("declined")

// certsValuesReady says whether values has every credential p needs.
func certsValuesReady(p *certs.DNSProvider, values map[string]string) bool {
	if len(values) == 0 {
		return false
	}
	for _, f := range p.Fields {
		if strings.TrimSpace(values[f.Key]) == "" && f.Default == "" && !f.Optional {
			return false
		}
	}
	return true
}

// certsWhyHTTP says how the server proves the domains.
func certsWhyHTTP(mode certHTTPMode) string {
	switch mode {
	case certHTTPWebroot:
		return locales.CertsWhyWebroot()
	case certHTTPResponder:
		return locales.CertsWhyResponder()
	case certHTTPCaddy:
		return locales.CertsWhyCaddy()
	case certHTTPSplit:
		return locales.CertsWhySplit()
	}
	return locales.CertsWhyHTTP()
}

// certsWhyNotHTTP says why the server could not prove the domains, which
// TXT records then do.
func certsWhyNotHTTP(found certHTTPFound) string {
	switch err := found.err; {
	case errors.Is(err, errCertsDeclined):
		return locales.CertsWhyDeclined()
	case errors.Is(err, errCertHTTPPort80):
		if len(found.servers) > 0 {
			return locales.CertsWhyPort80By(strings.Join(found.servers, ", "))
		}
		return locales.CertsWhyPort80()
	case errors.Is(err, errCertHTTPNoServer):
		return locales.CertsWhyNoResponder()
	case errors.Is(err, errCertHTTPFirewall):
		return locales.CertsWhyFirewall()
	case errors.Is(err, errCertHTTPICP):
		return locales.CertsWhyICP()
	case errors.Is(err, errCertHTTPProbe):
		return locales.CertsWhyUnreachable()
	}
	return locales.CertsWhyHTTPFailed()
}

// certsAnswerConfirm answers the change the issuance waits on.
func (a *App) certsAnswerConfirm(answer certsAnswer) {
	job := a.certs.job
	if job == nil || job.stage != certsStageConfirm {
		return
	}
	job.confirm = nil
	job.stage = certsStageDetect
	select {
	case job.answer <- answer:
	default:
	}
}

// certsRenew issues the certificate cert again, for the same domains and
// key, proved as a new one would be; it replaces cert, keeping its
// download password.
func (a *App) certsRenew(s *Session, cert certs.Cert) {
	a.certs.dialog = &certsDialog{kind: certsDialogJob}
	a.certsStart(s, cert.Domains, cert.KeyType, cert.Staging, cert.ID)
}
