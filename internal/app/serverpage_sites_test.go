package app

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func TestSitesTableAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	cert := monitor.Certificate{File: "/etc/ssl/certs/front.pem", NotAfter: now.Add(5 * 24 * time.Hour)}
	page := &serverSitesPage{
		inventory: monitor.NginxInventory{NginxTree: monitor.NginxTree{Sites: []monitor.NginxSite{
			{File: monitor.NginxEnabled + "front", Line: 7, Names: []string{"front.example.org"}, Listen: []monitor.NginxListen{{Port: 443, TLS: true}}, Certificates: []string{cert.File}},
			{File: monitor.NginxConfD + "test.conf", Line: 1, Names: []string{"test.example.org"}, Listen: []monitor.NginxListen{{Port: 80}}},
		}}, Links: map[string]string{"front": "enabled"}},
		certificates: []monitor.Certificate{cert},
	}
	rows := page.siteRows(now)
	if len(rows) != 2 || rows[0].state != "enabled" || rows[0].file != monitor.NginxAvailable+"front" || rows[0].expiry != 5 || rows[1].state != "included" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	for _, entry := range []struct{ days, level int }{{-1, 2}, {6, 2}, {7, 1}, {29, 1}, {30, 0}} {
		if got := siteExpiryLevel(entry.days); got != entry.level {
			t.Errorf("%d days: %d", entry.days, got)
		}
	}
}

const siteExampleFile = "/etc/nginx/sites-available/front.conf"
const siteExampleOriginal = "server {\n    listen 80;\n    server_name front.example.org;\n    root /srv/front;\n}\n"
const siteExampleDump = `# configuration file /etc/nginx/nginx.conf:
events {}
http {
    error_log /var/log/nginx/error.log;
    access_log /var/log/nginx/access.log;
    include /etc/nginx/sites-enabled/*;
    include /etc/nginx/conf.d/*.conf;
}
# configuration file /etc/nginx/sites-enabled/front.conf:
` + siteExampleOriginal + `
# configuration file /etc/nginx/conf.d/api.conf:
server {
    listen [::]:443 ssl http2;
    server_name api.example.org;
    ssl_certificate /etc/letsencrypt/live/api/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api/privkey.pem;
    location / { proxy_pass http://127.0.0.1:8080; }
}
server { listen 80; server_name docs.example.org; root /srv/docs; }
`

type sitesFixture struct {
	calls    chan systemFakeCall
	refresh  atomic.Int32
	probes   atomic.Int32
	failTest atomic.Bool
	enabled  atomic.Bool
	cert     monitor.Certificate
}

func (fake *sitesFixture) read(_ context.Context, script string, _ io.Reader, _ func(string)) (string, string, error) {
	switch {
	case script == monitor.NginxStatusCommand:
		return "nginx\tnginx version: nginx/1.26.2\nsystemd\nLoadState=loaded\nActiveState=active\nMainPID=421\ncertbot\nacme\nopenssl\n", "", nil
	case strings.Contains(script, "/proc/$pid/cmdline"):
		return "421\n", "", nil
	case script == monitor.CertificateRemoteCommand("api.example.org"):
		return "subject=CN = api.example.org\nissuer=CN = Test CA\nnotBefore=Nov  1 00:00:00 2026 GMT\nnotAfter=Dec 31 00:00:00 2026 GMT\n", "", nil
	default:
		return "", "unexpected command", errors.New("unexpected unprivileged execution")
	}
}

func (fake *sitesFixture) privilege(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
	if request.Probe {
		fake.probes.Add(1)
		return monitor.PrivilegeResult{Stdout: "root"}, nil
	}
	script, hasPath := strings.CutPrefix(request.Action, monitor.NginxToolPath)
	if !hasPath {
		return monitor.PrivilegeResult{Started: true}, errors.New("missing safe tool path")
	}
	result := monitor.PrivilegeResult{Started: true}
	switch {
	case strings.HasSuffix(script, monitor.NginxDumpCommand):
		fake.refresh.Add(1)
		result.Stdout = siteExampleDump
	case strings.Contains(script, "for file in '/etc/nginx/sites-available/'*"):
		result.Stdout = "debian\nenabled\tfront.conf\ndisabled\tpreview.conf\nconfd\n"
		if fake.enabled.Load() {
			result.Stdout = strings.Replace(result.Stdout, "disabled\tpreview.conf", "enabled\tpreview.conf", 1)
		}
	case strings.Contains(script, "'printf' '# configuration file %s:"):
		result.Stdout = "# configuration file /etc/nginx/sites-available/preview.conf:\nserver { listen 8081; server_name preview.example.org; root /srv/preview; }\n"
	case script == monitor.CertificateDiscoveryCommand:
		result.Stdout = fake.cert.File + "\n"
	case script == monitor.CertbotListCommand:
		result.Stdout = "Certificate Name: api.example.org\n  Key Type: ECDSA\n  Domains: api.example.org\n  Expiry Date: 2026-12-12 00:00:00+00:00 (VALID: 17 days)\n  Certificate Path: " + fake.cert.File + "\n"
	case script == monitor.CertbotPluginsCommand:
		result.Stdout = "* nginx\n"
	case script == monitor.CertbotScheduleCommand:
		result.Stdout = "certbot.timer: active\nacme.sh\n"
	case strings.Contains(script, "BEGIN CERTIFICATE-----"):
		result.Stdout = "== " + fake.cert.File + "\n"
	case strings.Contains(script, "'head' '-c' '262145' '"+siteExampleFile+"'"):
		result.Stdout = siteExampleOriginal
	default:
		var input []byte
		if request.Stdin != nil {
			input, _ = io.ReadAll(request.Stdin)
		}
		fake.calls <- systemFakeCall{script, string(input)}
		if strings.Contains(script, "finallyshell-written") {
			result.Stdout = "finallyshell-written\n"
		} else if strings.Contains(script, "'set' '--' 'preview.conf'") && strings.Contains(script, "; 'add_link';") {
			fake.enabled.Store(true)
		} else if strings.Contains(script, "'set' '--' 'front.conf'") && strings.Contains(script, "; 'remove_link';") {
			result.Stdout = "disabled front.conf\n"
		} else if strings.Contains(script, "'nginx' '-t'") && fake.failTest.Load() {
			result.Stderr = "nginx: [emerg] invalid listen port in " + siteExampleFile + ":2\nnginx: configuration file /etc/nginx/nginx.conf test failed\n"
			return result, systemFakeExit(1)
		} else if strings.Contains(script, "'certbot'") {
			request.ReportProgress("Certbot: simulated operation complete\n")
		}
	}
	return result, nil
}

func sitesTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *sitesFixture) {
	t.Helper()
	a, platform := testApp(t)
	s := demoSession(a, "web-example", "192.0.2.50")
	s.Host.User = "operator"
	fake := &sitesFixture{calls: make(chan systemFakeCall, 40), cert: monitor.Certificate{
		File: "/etc/letsencrypt/live/api/fullchain.pem", Subject: "CN=api.example.org", Issuer: "CN=Example CA", SANs: []string{"api.example.org"}, KeyType: "ECDSA",
		NotBefore: time.Now().Add(-20 * 24 * time.Hour), NotAfter: time.Now().Add(5*24*time.Hour + time.Hour),
	}}
	s.server.sites.read = fake.read
	s.server.sites.localTLS = func(_ context.Context, _ string) (monitor.Certificate, error) { return fake.cert, nil }
	s.overview.memory.privilegeRunner = fake.privilege
	tester := ui.NewTester(a.View, 1280, 820)
	platform.tt = tester
	a.active = 0
	a.openPanelTab(s, tabSystem)
	a.openServerPage(s, serverSecSites)
	tester.Frame()
	platform.until(t, "websites loaded", func() bool { return s.server.sites.loaded && !s.server.sites.reading })
	tester.Frame()
	return a, platform, tester, s, fake
}

func sitesChoose(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	if err := tester.ChooseMenuItem(name); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
}

func sitesNoCall(t *testing.T, fake *sitesFixture) {
	t.Helper()
	select {
	case call := <-fake.calls:
		t.Fatalf("unconfirmed execution: %s", call.command)
	default:
	}
}

func TestSitesListFilterSortAndToggle(t *testing.T) {
	_, platform, tester, s, fake := sitesTestApp(t)
	page := &s.server.sites
	wantTexts(t, tester, "sites", "front.example.org", "api.example.org", "preview.example.org", "PID 421")
	page.sites.query = "PREVIEW"
	page.sites.sort = ui.SortOrder{Column: "port", Descending: true}
	tester.Frame()
	if !tester.HasText("preview.example.org") || tester.HasText("front.example.org") || page.sites.list.Label(0) != "preview.example.org" {
		t.Fatalf("filter did not work: %q", tester.Texts())
	}
	tester.RightClickAt(rowCenter(t, tester, "preview.example.org"))
	tester.Frame()
	systemWantMenuDisabled(t, tester, locales.SiteDisable())
	sitesChoose(t, tester, locales.SiteEnable())
	systemClick(t, tester, locales.Cancel())
	sitesNoCall(t, fake)
	systemClick(t, tester, locales.SiteActions())
	sitesChoose(t, tester, locales.SiteEnable())
	systemClick(t, tester, locales.SvcConfirm())
	call := systemNext(t, platform, fake.calls)
	expected, err := page.inventory.ToggleCommand([]string{"preview.conf"}, true, true, true)
	if err != nil || call.command != expected || call.input != "" {
		t.Fatalf("unexpected toggle: %+v; %v", call, err)
	}
	platform.until(t, "sites reread", func() bool { return !page.reading && fake.refresh.Load() > 1 })
	if fake.probes.Load() != 1 || page.inventory.Links["preview.conf"] != "enabled" {
		t.Fatal("action did not use cached root authentication or refresh")
	}
	page.sites.query = ""
	tester.Frame()
	if page.sites.list.Label(0) != "api.example.org" {
		t.Fatalf("unexpected descending port order: %s", page.sites.list.Label(0))
	}
}

func sitesEditFailure(t *testing.T, a *App, platform *fakePlatform, tester *ui.Tester, s *Session, fake *sitesFixture) {
	t.Helper()
	page := &s.server.sites
	a.siteEditor(s, siteExampleFile, true)
	platform.until(t, "site editor", func() bool { return page.dialog != nil && page.dialog.body != nil && !page.dialog.busy })
	tester.Frame()
	clickNamed(t, tester, locales.SiteConfig())
	tester.Key(ui.Cmd, ui.KeyA)
	bad := strings.Replace(siteExampleOriginal, "listen 80", "listen invalid", 1)
	tester.Type(bad)
	tester.Frame()
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.Cancel())
	sitesNoCall(t, fake)
	systemClick(t, tester, locales.Save())
	fake.failTest.Store(true)
	systemClick(t, tester, locales.SvcConfirm())
	write, test := systemNext(t, platform, fake.calls), systemNext(t, platform, fake.calls)
	expected, _ := monitor.NginxActionCommand("reload", true, monitor.NginxMain)
	if write.input != bad || test.command != expected || !strings.Contains(write.command, "'ln' '-T'") || strings.Contains(write.command, bad) {
		t.Fatalf("wrong write or validation: input %q, test %q", write.input, test.command)
	}
	platform.until(t, "failed configuration test", func() bool { return page.recovery != nil && !page.dialog.busy })
	tester.Frame()
	if !strings.Contains(page.dialog.failure, "invalid listen port") || !strings.Contains(page.dialog.output, page.recovery.Backup) {
		t.Fatal("failed test output or backup path missing")
	}
}

func TestSitesEditorRestoreAndServiceConfirmation(t *testing.T) {
	a, platform, tester, s, fake := sitesTestApp(t)
	page := &s.server.sites
	sitesEditFailure(t, a, platform, tester, s, fake)
	fake.failTest.Store(false)
	systemClick(t, tester, locales.SiteRestore())
	systemClick(t, tester, locales.Cancel())
	sitesNoCall(t, fake)
	systemClick(t, tester, locales.SiteRestore())
	backup := page.recovery.Backup
	systemClick(t, tester, locales.SvcConfirm())
	restore, test := systemNext(t, platform, fake.calls), systemNext(t, platform, fake.calls)
	if !strings.Contains(restore.command, "'cat' '"+backup+"'") || test.command != "'nginx' '-t'" {
		t.Fatalf("unexpected restore: %q / %q", restore.command, test.command)
	}
	platform.until(t, "restored configuration", func() bool { return page.recovery == nil && !page.reading })
	tester.Frame()
	systemClick(t, tester, locales.Close())
	for _, item := range []struct{ action, label string }{{"reload", locales.SiteReload()}, {"restart", locales.SiteRestart()}} {
		systemClick(t, tester, "Nginx")
		sitesChoose(t, tester, item.label)
		systemClick(t, tester, locales.Cancel())
		sitesNoCall(t, fake)
		systemClick(t, tester, "Nginx")
		sitesChoose(t, tester, item.label)
		systemClick(t, tester, locales.SvcConfirm())
		want, _ := monitor.NginxActionCommand(item.action, true, monitor.NginxMain)
		if call := systemNext(t, platform, fake.calls); call.command != want {
			t.Fatalf("%s: %q", item.action, call.command)
		}
		platform.until(t, "action completion and refresh", func() bool { return page.dialog.done && !page.reading })
		tester.Frame()
		systemClick(t, tester, locales.Close())
	}
}

func TestSitesCreateDisableAndIssue(t *testing.T) {
	_, platform, tester, s, fake := sitesTestApp(t)
	page := &s.server.sites
	done := func() {
		t.Helper()
		platform.until(t, "mutation complete and list refreshed", func() bool { return page.dialog.done && !page.reading })
		tester.Frame()
		systemClick(t, tester, locales.Close())
	}
	selectItem := func(current, option string) {
		t.Helper()
		systemClick(t, tester, current)
		sitesChoose(t, tester, option)
	}
	fill := func(label, text string) {
		t.Helper()
		field, found := tester.AccessByRole(ui.RoleTextField, label)
		if !found {
			t.Fatalf("missing field %q", label)
		}
		tester.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
		tester.Type(text)
		tester.Frame()
	}
	recheckConfirmation := func(label string) {
		t.Helper()
		systemClick(t, tester, label)
		systemClick(t, tester, locales.Cancel())
		sitesNoCall(t, fake)
		systemClick(t, tester, label)
		systemClick(t, tester, locales.SvcConfirm())
	}

	systemClick(t, tester, locales.SiteNew())
	fill(locales.SiteName(), "cdn")
	fill(locales.SiteDomains(), "cdn.example.org")
	fill(locales.SiteDocumentRoot(), "/srv/cdn")
	recheckConfirmation(locales.SiteCreate())
	call := systemNext(t, platform, fake.calls)
	wantContent, _ := (monitor.NginxTemplate{Names: []string{"cdn.example.org"}, Root: "/srv/cdn"}).Render()
	wantScript, _ := page.inventory.CreateCommand("cdn", wantContent, true, true)
	if call.input != wantContent || call.command != wantScript {
		t.Fatalf("unexpected creation payload: %+v", call)
	}
	done()

	page.sites.selected.Add("/etc/nginx/sites-enabled/front.conf:1")
	tester.Frame()
	selectItem(locales.SiteActions(), locales.SiteDisable())
	systemClick(t, tester, locales.Cancel())
	sitesNoCall(t, fake)
	selectItem(locales.SiteActions(), locales.SiteDisable())
	systemClick(t, tester, locales.SvcConfirm())
	call = systemNext(t, platform, fake.calls)
	wantScript, _ = page.inventory.ToggleCommand([]string{"front.conf"}, false, true, true)
	if call.command != wantScript || call.input != "" {
		t.Fatalf("unexpected disabling payload: %+v", call)
	}
	done()

	page.tabs = 1
	tester.Frame()
	selectItem(locales.CertTools(), locales.CertIssue())
	fill(locales.SiteDomains(), "api.example.org")
	fill(locales.CertEmail(), "admin@example.org")
	recheckConfirmation(locales.CertIssueNext())
	call = systemNext(t, platform, fake.calls)
	issue := monitor.CertbotIssueCommand([]string{"api.example.org"}, "admin@example.org")
	if !strings.HasSuffix(call.command, issue+" 2>&1") || !strings.Contains(call.command, "'ln' '-T'") || call.input != "" {
		t.Fatalf("unexpected ACME issuance: %+v", call)
	}
	done()
}

func TestSitesCertificatesToolsAndProbes(t *testing.T) {
	_, platform, tester, s, fake := sitesTestApp(t)
	page := &s.server.sites
	page.tabs = 1
	tester.Frame()
	wantTexts(t, tester, "certificates", "api.example.org", "Certbot")
	for _, dry := range []bool{true, false} {
		label := locales.CertRenew()
		if dry {
			label = locales.CertDryRun()
		}
		systemClick(t, tester, locales.CertTools())
		sitesChoose(t, tester, label)
		systemClick(t, tester, locales.Cancel())
		sitesNoCall(t, fake)
		systemClick(t, tester, locales.CertTools())
		sitesChoose(t, tester, label)
		systemClick(t, tester, locales.SvcConfirm())
		if call := systemNext(t, platform, fake.calls); !strings.HasSuffix(call.command, monitor.CertbotRenewCommand(dry)+" 2>&1") {
			t.Fatalf("unexpected renewal: %q", call.command)
		}
		platform.until(t, "certificate refresh", func() bool { return page.dialog.done && !page.reading })
		if !strings.Contains(page.dialog.output, "simulated operation complete") {
			t.Fatalf("missing output: %q", page.dialog.output)
		}
		tester.Frame()
		systemClick(t, tester, locales.Close())
	}
	page.certbotNginx = false
	tester.Frame()
	systemClick(t, tester, locales.CertTools())
	systemWantMenuDisabled(t, tester, locales.CertIssue())
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	systemClick(t, tester, locales.CertCheck())
	clickNamed(t, tester, locales.CertDomain())
	tester.Type("api.example.org")
	tester.Frame()
	systemClick(t, tester, locales.CertCheckRun())
	platform.until(t, "dual TLS check", func() bool { return page.dialog.done })
	if !strings.Contains(page.dialog.output, locales.CertFromServer()) || !strings.Contains(page.dialog.output, locales.CertFromComputer()) || !strings.Contains(page.dialog.output, locales.CertVerified()) {
		t.Fatalf("unexpected TLS comparison: %q", page.dialog.output)
	}
	sitesNoCall(t, fake)
}

func TestSitesScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, platform, tester, s, fake := sitesTestApp(t)
			platform.SetLocale(language)
			done := make(chan struct{})
			term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
			if err != nil {
				close(done)
				t.Fatal(err)
			}
			t.Cleanup(func() { close(done); term.Close() })
			s.setTerm(term)
			term.Feed([]byte("\x1b[32moperator@web-example\x1b[0m:~$ "))
			page := &s.server.sites
			page.certificates = []monitor.Certificate{fake.cert, {
				File: "/etc/nginx/tls/docs.pem", Subject: "CN=docs.example.org", Issuer: "CN=Example CA", KeyType: "RSA", SANs: []string{"docs.example.org"},
				NotBefore: time.Now().Add(-75 * 24 * time.Hour), NotAfter: time.Now().Add(18 * 24 * time.Hour),
			}}
			page.certFailures = nil
			sitesEditFailure(t, a, platform, tester, s, fake)
			failed := page.dialog
			page.dialog = nil
			page.recovery = nil
			for _, mode := range []string{"light", "dark"} {
				tester.SetDark(mode == "dark")
				shot := func(scene string) {
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "sites-"+scene+"-"+language+"-"+mode+".png")
				}
				for _, view := range []struct {
					tab  int
					name string
				}{{0, "list"}, {1, "certificates"}} {
					page.tabs = view.tab
					a.openPanelTab(s, tabSystem)
					shot(view.name + "-full")
					a.active = 0
					a.openServerPage(s, serverSecSites)
					shot(view.name + "-short")
					a.setPanelOnRight(true)
					shot(view.name + "-right")
					a.setPanelOnRight(false)
				}
				page.tabs, page.dialog = 0, failed
				a.openPanelTab(s, tabSystem)
				shot("editor-failed-full")
				a.active = 0
				a.openServerPage(s, serverSecSites)
				shot("editor-failed-short")
				page.dialog = nil
			}
		})
	}
}
