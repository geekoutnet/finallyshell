package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/certs"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"software.sslmate.com/src/go-pkcs12"
)

// certsTestNow is the tests' clock.
var certsTestNow = time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

// certsTestApp is a desktop with the SSL certificates app open, whose
// name servers, HTTP check and CA the test gives.
func certsTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session) {
	t.Helper()
	// lego follows a record's CNAMEs over the network: not in these tests.
	t.Setenv("LEGO_DISABLE_CNAME_SUPPORT", "true")
	a, p, tt, s := desktopTestApp(t)
	a.certs.now = func() time.Time { return certsTestNow }
	a.certs.nameservers = func(context.Context, string) ([]string, error) { return []string{"ns1.example.net"}, nil }
	a.certs.issue = func(context.Context, bool, certs.Request) (certs.Bundle, error) {
		return certs.Bundle{}, errors.New("no CA in this test")
	}
	if a.desktopOpen(s, appCerts, nil) == nil {
		t.Fatal("the app did not open")
	}
	p.until(t, "the DNS credentials", func() bool { return a.certs.dnsLoaded })
	tt.Frame()
	return a, p, tt, s
}

func TestCertsEmpty(t *testing.T) {
	a, _, tt, s := certsTestApp(t)
	wantTexts(t, tt, "empty list", locales.CertsApp(), locales.CertsEmptyTitle(), locales.CertsEmptyNote())
	if len(a.certs.list) != 0 {
		t.Fatalf("list %v", a.certs.list)
	}
	// In the app center, with its description.
	if got := desktopCenterDescription(desktopAppByID(appCerts)); got != locales.CertsCenter() {
		t.Fatalf("center description %q", got)
	}
	_ = s
	saveShot(t, tt, "certs-empty-en.png")
}

// certsTestBundle is a certificate for domains, as a CA would give, valid
// for 90 days from the tests' clock.
func certsTestBundle(t *testing.T, domains []string) certs.Bundle {
	t.Helper()
	b, err := certs.SelfSigned(domains, certs.KeyEC256, certsTestNow, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// readZip is the files of the ZIP at path.
func readZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		r, _ := f.Open()
		files[f.Name], _ = io.ReadAll(r)
		r.Close()
	}
	return files
}

func TestCertsSelfSigned(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	clickButton(t, tt, locales.CertsSelfSigned())
	tt.Frame()
	wantTexts(t, tt, "self-signed form", locales.CertsSelfSignedTitle(), locales.CertsSelfSignedWarning())
	typeInto(t, tt, locales.CertsDomains(), "lan.example 192.168.1.10")
	tt.Frame()
	clickButton(t, tt, locales.CertsGenerate())
	tt.Frame()
	if len(a.certs.list) != 1 {
		t.Fatalf("list %+v", a.certs.list)
	}
	cert := a.certs.list[0]
	if cert.Method != certs.MethodSelfSigned || !slices.Equal(cert.Domains, []string{"lan.example", "192.168.1.10"}) || a.certs.selected != cert.ID {
		t.Fatalf("saved %+v, selected %q", cert, a.certs.selected)
	}
	// Its download opens at once, the password shown.
	d := a.certs.dialog
	if d == nil || d.kind != certsDialogDownload || d.password != cert.Password {
		t.Fatalf("dialog %+v", d)
	}
	wantTexts(t, tt, "download", locales.CertsDownloadTitle("lan.example, 192.168.1.10"), cert.Password, locales.CertsFormatIIS())
	zipPath := filepath.Join(t.TempDir(), "lan.zip")
	p.pickSave(zipPath, nil)
	clickButton(t, tt, locales.CertsSaveZip())
	p.until(t, "the ZIP", func() bool { return d.savedTo == zipPath })
	tt.Frame()
	wantTexts(t, tt, "saved", locales.CertsSaved(zipPath))
	if asked := p.saveAsked[len(p.saveAsked)-1]; asked.path != "lan.example.zip" {
		t.Fatalf("asked to save %q", asked.path)
	}
	files := readZip(t, zipPath)
	for _, name := range []string{"README.txt", "nginx/fullchain.pem", "nginx/privkey.pem", "apache/lan.example.crt", "apache/lan.example.key", "iis/lan.example.pfx", "tomcat/lan.example.jks"} {
		if _, ok := files[name]; !ok {
			t.Errorf("no %s in %q", name, slices.Sorted(func(yield func(string) bool) {
				for n := range files {
					if !yield(n) {
						return
					}
				}
			}))
		}
	}
	if _, err := certs.ParseBundle(files["nginx/fullchain.pem"], files["nginx/privkey.pem"]); err != nil {
		t.Fatalf("nginx files: %v", err)
	}
	if _, leaf, _, err := pkcs12.DecodeChain(files["iis/lan.example.pfx"], cert.Password); err != nil || leaf.Subject.CommonName != "lan.example" {
		t.Fatalf("pfx: %v", err)
	}
	readme := string(files["README.txt"])
	if strings.Contains(readme, cert.Password) || !strings.Contains(readme, locales.CertsReadmePassword()) || !strings.Contains(readme, locales.CertsSelfSignedWarning()) {
		t.Fatalf("README:\n%s", readme)
	}
	for name, data := range files {
		if bytes.Contains(data, []byte(cert.Password)) {
			t.Fatalf("the password is in %s", name)
		}
	}
	// Only the Nginx files, the password then hidden.
	for _, f := range []certs.Format{certs.FormatApache, certs.FormatIIS, certs.FormatTomcat} {
		d.formats[f] = false
	}
	tt.Frame()
	if tt.HasText(cert.Password) {
		t.Fatal("the password shows with no PFX or JKS")
	}
	nginxOnly := filepath.Join(t.TempDir(), "nginx.zip")
	p.pickSave(nginxOnly, nil)
	clickButton(t, tt, locales.CertsSaveZip())
	p.until(t, "the second ZIP", func() bool { return d.savedTo == nginxOnly })
	if files := readZip(t, nginxOnly); len(files) != 3 {
		t.Fatalf("nginx only: %d files", len(files))
	}
	// Canceled: nothing written, nothing said.
	p.pickSave("", nil)
	clickButton(t, tt, locales.CertsSaveZip())
	p.until(t, "the canceled save", func() bool { return !d.saving })
	if d.saveErr != "" {
		t.Fatalf("canceled save: %q", d.saveErr)
	}
	clickButton(t, tt, locales.Close())
	tt.Frame()
	if a.certs.dialog != nil {
		t.Fatal("the dialog stayed open")
	}
	wantTexts(t, tt, "list", "lan.example, 192.168.1.10", locales.CertsSelfSignedBadge(), locales.CertsDaysLeft(365))
}

func TestCertsRenewAndDelete(t *testing.T) {
	a, _, tt, _ := certsTestApp(t)
	self := certsTestBundle(t, []string{"lan.example"})
	saved, err := certsStore().Save(certs.Cert{Domains: []string{"lan.example"}, Method: certs.MethodSelfSigned}, self, certsTestNow)
	if err != nil {
		t.Fatal(err)
	}
	a.certsReload()
	tt.Frame()
	wantTexts(t, tt, "list", locales.CertsDaysLeft(90))
	clickNamed(t, tt, "lan.example")
	tt.Frame()
	clickButton(t, tt, locales.CertsRenew())
	tt.Frame()
	renewed, b, err := certsStore().Get(saved.ID)
	if err != nil || renewed.Password != saved.Password || b.Leaf().SerialNumber.Cmp(self.Leaf().SerialNumber) == 0 {
		t.Fatalf("renewed %+v, %v", renewed, err)
	}
	wantTexts(t, tt, "renewed", locales.CertsDaysLeft(365))

	clickButton(t, tt, locales.Delete())
	tt.Frame()
	wantTexts(t, tt, "delete", locales.CertsDeleteTitle(), locales.CertsDeleteNote("lan.example"))
	clickButton(t, tt, locales.CertsDeleteTitle()) // the dialog's button
	tt.Frame()
	if len(a.certs.list) != 0 || a.certs.dialog != nil {
		t.Fatalf("after delete: %+v", a.certs.list)
	}
	if _, _, err := certsStore().Get(saved.ID); !errors.Is(err, certs.ErrNotFound) {
		t.Fatalf("still stored: %v", err)
	}
}

func TestCertsDNSSettings(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	clickNamed(t, tt, locales.CertsDNSSettings())
	tt.Frame()
	wantTexts(t, tt, "dns", locales.CertsDNSNote(), locales.CertsDNSGetKey("Cloudflare"))
	// Nothing typed: what is missing.
	clickButton(t, tt, locales.Save())
	tt.Frame()
	wantTexts(t, tt, "missing", locales.CertsDNSMissing("API Token"))
	typeInto(t, tt, "API Token", "cf-t0ken")
	tt.Frame()
	clickButton(t, tt, locales.Save())
	tt.Frame()
	wantTexts(t, tt, "saved", locales.CertsDNSSaved("Cloudflare"))
	p.until(t, "the store", storeSettled(a, p))
	var stored map[string]string
	json.Unmarshal([]byte(p.stored[certsDNSKey("cloudflare")]), &stored)
	if stored["token"] != "cf-t0ken" || !a.certs.certsDNSReady(certs.FindDNSProvider("cloudflare")) {
		t.Fatalf("stored %v", stored)
	}
	if tt.HasText("cf-t0ken") {
		t.Fatal("the token shows")
	}
	// Read back by a new run.
	a.certs = certsState{now: a.certs.now}
	a.certsLoadDNS()
	p.until(t, "the credentials again", func() bool { return a.certs.dnsLoaded })
	if a.certs.dns["cloudflare"]["token"] != "cf-t0ken" {
		t.Fatalf("read back %v", a.certs.dns)
	}
	a.certsOpenDNS("cloudflare")
	tt.Frame()
	clickButton(t, tt, locales.CertsDNSClear())
	tt.Frame()
	p.until(t, "the deletion", storeSettled(a, p))
	if _, ok := p.stored[certsDNSKey("cloudflare")]; ok || a.certs.dns["cloudflare"] != nil {
		t.Fatal("the key is still kept")
	}
	wantTexts(t, tt, "cleared", locales.CertsDNSCleared())
}

// certsFakeIssue puts each domain's proof in place with the provider r
// gives, as lego does, then gives a certificate: unless block, which the
// test closes, or fail.
func certsFakeIssue(t *testing.T, block chan struct{}, fail error) func(context.Context, bool, certs.Request) (certs.Bundle, error) {
	return func(ctx context.Context, staging bool, r certs.Request) (certs.Bundle, error) {
		r.Progress(certs.Progress{Step: certs.StepOrder})
		for _, d := range r.Domains {
			// lego proves a wildcard's domain, without "*.".
			d = strings.TrimPrefix(d, "*.")
			r.Progress(certs.Progress{Step: certs.StepProve, Detail: d})
			var err error
			if r.DNS != nil {
				err = r.DNS.Present(d, "token0123456789", "token0123456789.thumbprint01234")
			} else {
				err = r.HTTP.Present(d, "token0123456789", "token0123456789.thumbprint01234")
			}
			if err != nil {
				return certs.Bundle{}, err
			}
		}
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				return certs.Bundle{}, ctx.Err()
			}
		}
		if fail != nil {
			return certs.Bundle{}, fail
		}
		r.Progress(certs.Progress{Step: certs.StepIssue})
		return certsTestBundle(t, r.Domains), nil
	}
}

// certsGenerate types domains in a new certificate's form and starts it.
func certsGenerate(t *testing.T, tt *ui.Tester, domains string) {
	t.Helper()
	clickButton(t, tt, locales.CertsNew())
	tt.Frame()
	typeInto(t, tt, locales.CertsDomains(), domains)
	tt.Frame()
	clickButton(t, tt, locales.CertsGenerate())
	tt.Frame()
}

// A domain hosted where a key is kept is proved through the API.
func TestCertsJobDNS(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	a.certs.dns["cloudflare"] = map[string]string{"token": "cf-t0ken"}
	a.certs.nameservers = func(context.Context, string) ([]string, error) {
		return []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}, nil
	}
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		t.Error("the server was asked")
		return certHTTPFound{}
	}
	var gotDNS bool
	a.certs.issue = func(ctx context.Context, staging bool, r certs.Request) (certs.Bundle, error) {
		// The real Cloudflare provider, wrapped: not called here.
		gotDNS = r.DNS != nil && r.HTTP == nil
		if _, manual := r.DNS.(certsWatchedTimeout); manual {
			if _, isManual := r.DNS.(certsWatchedTimeout).p.(certs.ManualDNS); isManual {
				t.Error("records by hand")
			}
		}
		r.Progress(certs.Progress{Step: certs.StepIssue})
		return certsTestBundle(t, r.Domains), nil
	}
	certsGenerate(t, tt, "*.example.com example.com")
	p.until(t, "the issuance", func() bool { return a.certs.job.stage == certsStageDone })
	tt.Frame()
	job := a.certs.job
	if !gotDNS || job.via != certsViaDNS || job.reason != locales.CertsWhyDNS("Cloudflare") {
		t.Fatalf("job %+v", job)
	}
	saved := a.certs.list[0]
	if saved.Method != certs.MethodDNS || saved.Provider != "cloudflare" || !slices.Equal(saved.Domains, []string{"*.example.com", "example.com"}) {
		t.Fatalf("saved %+v", saved)
	}
	wantTexts(t, tt, "done", locales.CertsIssued(saved.NotAfter.Local().Format("2006-01-02")), locales.CertsWhyDNS("Cloudflare"))
	clickButton(t, tt, locales.CertsDownload())
	tt.Frame()
	if a.certs.dialog == nil || a.certs.dialog.kind != certsDialogDownload {
		t.Fatalf("dialog %+v", a.certs.dialog)
	}
}

// The connected server proves the domain: the challenge's file written
// through it, by the job's provider.
func TestCertsJobHTTP(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	var scripts []string
	run := func(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
		scripts = append(scripts, script)
		return "", "", nil
	}
	closed := false
	a.certs.findHTTP = func(_ context.Context, s *Session, domains []string) certHTTPFound {
		return certHTTPFound{h: &certHTTP{ctx: context.Background(), run: run, mode: certHTTPWebroot, roots: map[string]string{"web.example.com": "/var/www/html"}}}
	}
	a.certs.issue = certsFakeIssue(t, nil, nil)
	certsGenerate(t, tt, "web.example.com")
	p.until(t, "the issuance", func() bool { return a.certs.job.stage == certsStageDone })
	tt.Frame()
	_ = closed
	job := a.certs.job
	if job.via != certsViaHTTP || job.httpMode != certHTTPWebroot || job.reason != locales.CertsWhyWebroot() {
		t.Fatalf("job %+v", job)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "/var/www/html") || !strings.Contains(scripts[0], "token0123456789.thumbprint01234") {
		t.Fatalf("scripts %q", scripts)
	}
	saved := a.certs.list[0]
	if saved.Method != certs.MethodHTTP || saved.Server != "web-1" {
		t.Fatalf("saved %+v", saved)
	}
	wantTexts(t, tt, "done", locales.CertsWhyWebroot(), locales.CertsStepDone())
	clickButton(t, tt, locales.Done())
	tt.Frame()
	wantTexts(t, tt, "list", locales.CertsMethodHTTPOn("web-1"), locales.CertsDaysLeft(90))
}

// A change to the server waits for the user: agreed to, it is made; not,
// the domain is proved by hand.
func TestCertsJobConfirm(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	applied := 0
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		return certHTTPFound{servers: []string{"nginx"}, confirm: &certHTTPConfirm{kind: certConfirmSplit, target: "nft",
			apply: func(context.Context) (*certHTTP, error) {
				applied++
				return &certHTTP{ctx: context.Background(), mode: certHTTPSplit, roots: map[string]string{"busy.example.com": "/tmp/finallyshell-acme.abcdEFGH"},
					run: func(context.Context, string, io.Reader, func(string)) (string, string, error) { return "", "", nil }}, nil
			}}}
	}
	a.certs.issue = certsFakeIssue(t, nil, nil)
	certsGenerate(t, tt, "busy.example.com")
	p.until(t, "the question", func() bool { return a.certs.job.stage == certsStageConfirm })
	tt.Frame()
	wantTexts(t, tt, "confirm", locales.CertsConfirmTitle(), locales.CertsConfirmSplit("nft"), locales.CertsConfirmPort80By("nginx"), locales.CertsConfirmUndo())
	saveShot(t, tt, "certs-confirm-en.png")
	clickButton(t, tt, locales.CertsConfirmApply())
	p.until(t, "the issuance", func() bool { return a.certs.job.stage == certsStageDone })
	if applied != 1 || a.certs.job.httpMode != certHTTPSplit || a.certs.job.reason != locales.CertsWhySplit() {
		t.Fatalf("applied %d, job %+v", applied, a.certs.job)
	}
	clickButton(t, tt, locales.Done())
	tt.Frame()

	// Declined: TXT records instead.
	block := make(chan struct{})
	a.certs.issue = certsFakeIssue(t, block, nil)
	certsGenerate(t, tt, "busy2.example.com")
	p.until(t, "the question again", func() bool { return a.certs.job.stage == certsStageConfirm })
	tt.Frame()
	clickButton(t, tt, locales.CertsConfirmUseDNS())
	p.until(t, "the records", func() bool { return len(a.certs.job.records) == 1 })
	tt.Frame()
	if applied != 1 || a.certs.job.via != certsViaManual || a.certs.job.reason != locales.CertsWhyDeclined() {
		t.Fatalf("applied %d, job %+v", applied, a.certs.job)
	}
	close(block)
	p.until(t, "the issuance by hand", func() bool { return a.certs.job.stage == certsStageDone })
	if m := a.certs.list; len(m) != 2 || !slices.ContainsFunc(m, func(c certs.Cert) bool { return c.Method == certs.MethodDNSManual }) {
		t.Fatalf("list %+v", m)
	}
}

// TXT records by hand: each part copied, the wait canceled.
func TestCertsJobManual(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	block := make(chan struct{})
	a.certs.issue = certsFakeIssue(t, block, nil)
	// A wildcard is never asked of the server.
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		t.Error("the server was asked for a wildcard")
		return certHTTPFound{}
	}
	certsGenerate(t, tt, "*.lab.example.com")
	p.until(t, "the record", func() bool { return len(a.certs.job.records) == 1 })
	tt.Frame()
	job := a.certs.job
	want := dns01.GetChallengeInfo("lab.example.com", "token0123456789.thumbprint01234")
	rec := job.records[0]
	if rec.name != strings.TrimSuffix(want.FQDN, ".") || rec.value != want.Value || job.reason != locales.CertsWhyWildcard() {
		t.Fatalf("record %+v, reason %q", rec, job.reason)
	}
	wantTexts(t, tt, "records", locales.CertsRecordsTitle(), rec.name, rec.value, locales.CertsWhyWildcard())
	saveShot(t, tt, "certs-records-en.png")
	clickNamed(t, tt, locales.CertsCopy(locales.CertsRecordValue()))
	if got := tt.Clipboard(); got != rec.value {
		t.Fatalf("copied %q", got)
	}
	clickNamed(t, tt, locales.CertsCopy(locales.CertsRecordName()))
	if got := tt.Clipboard(); got != rec.name {
		t.Fatalf("copied %q", got)
	}
	clickButton(t, tt, locales.Cancel())
	p.until(t, "the cancel", func() bool { return job.stage == certsStageCanceled })
	tt.Frame()
	wantTexts(t, tt, "canceled", locales.CertsCanceled())
	if len(a.certs.list) != 0 {
		t.Fatal("saved after a cancel")
	}
}

// The server cannot prove it: why, and the provider that would.
func TestCertsJobFallback(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	a.certs.nameservers = func(context.Context, string) ([]string, error) { return []string{"dns9.hichina.com"}, nil }
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		return certHTTPFound{err: errCertHTTPPort80, servers: []string{"docker-proxy"}}
	}
	block := make(chan struct{})
	defer close(block)
	a.certs.issue = certsFakeIssue(t, block, nil)
	certsGenerate(t, tt, "app.example.cn")
	p.until(t, "the record", func() bool { return len(a.certs.job.records) == 1 })
	tt.Frame()
	job := a.certs.job
	if job.via != certsViaManual || job.reason != locales.CertsWhyPort80By("docker-proxy") || job.hintProvider == nil || job.hintProvider.ID != "alidns" {
		t.Fatalf("job %+v", job)
	}
	wantTexts(t, tt, "fallback", locales.CertsWhyPort80By("docker-proxy"), locales.CertsHintProvider("Alibaba Cloud DNS"))
	clickButton(t, tt, locales.CertsAddKey())
	tt.Frame()
	if d := a.certs.dialog; d == nil || d.kind != certsDialogDNS || d.provider != "alidns" {
		t.Fatalf("dialog %+v", d)
	}
	// The issuance goes on meanwhile, shown above the list.
	clickButton(t, tt, locales.Close())
	tt.Frame()
	wantTexts(t, tt, "banner", locales.CertsShow())
	clickButton(t, tt, locales.CertsShow())
	tt.Frame()
	if d := a.certs.dialog; d == nil || d.kind != certsDialogJob {
		t.Fatalf("dialog %+v", d)
	}

	// The reasons of the other failures.
	for err, want := range map[error]string{
		errCertHTTPNoServer: locales.CertsWhyNoResponder(), errCertHTTPFirewall: locales.CertsWhyFirewall(),
		errCertHTTPICP: locales.CertsWhyICP(), errCertHTTPProbe: locales.CertsWhyUnreachable(),
		errors.New("other"): locales.CertsWhyHTTPFailed(), errCertHTTPPort80: locales.CertsWhyPort80(),
	} {
		if got := certsWhyNotHTTP(certHTTPFound{err: err}); got != want {
			t.Errorf("%v: %q, want %q", err, got, want)
		}
	}
}

// A failed issuance says where and why, and tries again with the same
// domains.
func TestCertsJobFailure(t *testing.T) {
	a, p, tt, _ := certsTestApp(t)
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		return certHTTPFound{h: &certHTTP{ctx: context.Background(), mode: certHTTPResponder, roots: map[string]string{"fail.example.com": "/tmp/finallyshell-acme.abcdEFGH"},
			run: func(context.Context, string, io.Reader, func(string)) (string, string, error) { return "", "", nil }}}
	}
	a.certs.issue = certsFakeIssue(t, nil, errors.New("acme: error: 403 :: urn:ietf:params:acme:error:unauthorized :: Invalid response"))
	certsGenerate(t, tt, "fail.example.com")
	p.until(t, "the failure", func() bool { return a.certs.job.stage == certsStageFailed })
	tt.Frame()
	job := a.certs.job
	if job.failedAt != certsStageWait {
		t.Fatalf("failed at %v", job.failedAt)
	}
	wantTexts(t, tt, "failed", locales.CertsFailedAt(locales.CertsStepWait()), locales.CertsWhyResponder())
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "Invalid response") {
		t.Fatalf("no CA error in %q", tt.Texts())
	}
	saveShot(t, tt, "certs-failed-en.png")
	clickButton(t, tt, locales.CertsRetry())
	tt.Frame()
	if d := a.certs.dialog; d == nil || d.kind != certsDialogNew || d.text != "fail.example.com" || a.certs.job != nil {
		t.Fatalf("retry dialog %+v", d)
	}
	// An IP address is for a self-signed certificate.
	typeInto(t, tt, locales.CertsDomains(), "203.0.113.7")
	clickButton(t, tt, locales.CertsGenerate())
	tt.Frame()
	wantTexts(t, tt, "ip", locales.CertsIPNeedsSelfSigned("203.0.113.7"))
	if a.certs.job != nil {
		t.Fatal("started for an IP address")
	}
}

// TestCertsShots are the app's screenshots: the list, a new certificate,
// its progress and a download, in English and Chinese, light and dark.
func TestCertsShots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	a, p, tt, _ := certsTestApp(t)
	store := certsStore()
	for _, c := range []struct {
		domains []string
		method  certs.Method
		days    int
		staging bool
	}{
		{[]string{"example.com", "www.example.com"}, certs.MethodHTTP, 76, false},
		{[]string{"*.example.org", "example.org"}, certs.MethodDNS, 21, false},
		{[]string{"api.example.net"}, certs.MethodDNSManual, 4, false},
		{[]string{"lan.example", "192.168.1.10"}, certs.MethodSelfSigned, 300, false},
		{[]string{"try.example.com"}, certs.MethodHTTP, 89, true},
	} {
		b, err := certs.SelfSigned(c.domains, certs.KeyEC256, certsTestNow, time.Duration(c.days)*24*time.Hour+time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		cert := certs.Cert{Domains: c.domains, Method: c.method, Staging: c.staging, Server: "web-1"}
		if c.method == certs.MethodDNS {
			cert.Provider = "cloudflare"
		}
		if _, err := store.Save(cert, b, certsTestNow); err != nil {
			t.Fatal(err)
		}
	}
	a.certsReload()
	a.certs.selected = "example.com"
	block := make(chan struct{})
	defer close(block)
	a.certs.findHTTP = func(context.Context, *Session, []string) certHTTPFound {
		return certHTTPFound{h: &certHTTP{ctx: context.Background(), mode: certHTTPResponder,
			roots: map[string]string{"shop.example.com": "/tmp/finallyshell-acme.abcdEFGH", "www.shop.example.com": "/tmp/finallyshell-acme.abcdEFGH"},
			run:   func(context.Context, string, io.Reader, func(string)) (string, string, error) { return "", "", nil }}}
	}
	a.certs.issue = certsFakeIssue(t, block, nil)
	for _, lang := range []string{"en", "zh-Hans"} {
		p.SetLocale(lang)
		for _, dark := range []bool{false, true} {
			tt.SetDark(dark)
			suffix := "-" + lang
			if dark {
				suffix += "-dark"
			}
			a.certs.dialog, a.certs.job = nil, nil
			tt.Frame()
			saveShot(t, tt, "certs-list"+suffix+".png")
			a.certsOpenNew(nil, false)
			a.certs.dialog.text, a.certs.dialog.advanced = "shop.example.com, www.shop.example.com", true
			tt.Frame()
			saveShot(t, tt, "certs-new"+suffix+".png")
			a.certs.dialog.started = true
			a.certsStart(a.sessions[0], []string{"shop.example.com", "www.shop.example.com"}, certs.KeyEC256, false, "")
			job := a.certs.job
			p.until(t, "the wait", func() bool { return job.stage == certsStageWait })
			tt.Frame()
			saveShot(t, tt, "certs-progress"+suffix+".png")
			job.cancel()
			p.until(t, "the cancel", func() bool { return !job.running() })
			cert := a.certs.list[0]
			a.certsOpenDownload(cert)
			tt.Frame()
			saveShot(t, tt, "certs-download"+suffix+".png")
		}
	}
	tt.SetDark(false)
}
