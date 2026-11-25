package app

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

const caddySiteMain = "# keep the header\nimport sites/*.caddy\n\nstatic.example.test {\n\troot * /srv/www\n\tfile_server\n}\n"
const caddySiteImported = "# keep this file comment\r\nproxy.example.test {\r\n\treverse_proxy localhost:8080\r\n} # keep the trailing comment\r\n\r\nraw.example.test {\r\n\tcustom_module keep\r\n}\r\n"
const caddySiteMainPath = "/etc/caddy/Caddyfile"
const caddySiteImportedPath = "/etc/caddy/sites/proxy.caddy"

type caddySiteFixture struct {
	changes    chan monitor.CaddyChange
	checks     chan []monitor.CaddyFile
	passwords  chan string
	applyError error
	checkError error
}

func caddySiteUI(t *testing.T, applyError, checkError error) (*App, *fakePlatform, *ui.Tester, *Session, caddySiteFixture) {
	t.Helper()
	a, platform, tester, _ := overviewApp(t)
	session := a.sessions[0]
	page := &session.caddy.sites
	fixture := caddySiteFixture{make(chan monitor.CaddyChange, 4), make(chan []monitor.CaddyFile, 4), make(chan string, 4), applyError, checkError}
	page.backend = caddySectionBackend{
		detect: func(context.Context, monitor.CaddyRunner) (monitor.CaddyInstall, error) {
			return monitor.CaddyInstall{Method: monitor.CaddyAPT, Config: caddySiteMainPath, Managed: true, SitesDir: "/etc/caddy/sites"}, nil
		},
		read: func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]monitor.CaddyFile, error) {
			return []monitor.CaddyFile{{Path: caddySiteMainPath, Content: caddySiteMain}, {Path: caddySiteImportedPath, Content: caddySiteImported}}, nil
		},
		apply: func(ctx context.Context, run monitor.CaddyRunner, _ monitor.CaddyInstall, change monitor.CaddyChange) (monitor.CaddyApplyResult, error) {
			_, _, _ = run(ctx, "fixture transaction", nil, nil)
			fixture.changes <- change
			response := monitor.CaddyApplyResult{Backup: "/etc/caddy/.backups/test", Reloaded: fixture.applyError == nil}
			if fixture.applyError != nil {
				response.Validate, response.RolledBack = "validation output from fixture", true
			}
			return response, fixture.applyError
		},
		runner: func(_ context.Context, script string, _ io.Reader, progress func(string)) (string, string, error) {
			if script == "fixture transaction" && progress != nil {
				progress("CADDY_TX out")
				progress("put encoded-sensitive-marker\nCADDY_TX vali")
				progress("dated\n")
			}
			return "", "", nil
		},
		validate: func(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, files []monitor.CaddyFile) (string, error) {
			fixture.checks <- files
			return "separate validation output", fixture.checkError
		},
		hash: func(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, plain string) (string, error) {
			fixture.passwords <- plain
			return caddySitePreviewHash, nil
		},
	}
	a.openCaddyPage(session, caddySecSites)
	tester.Frame()
	platform.until(t, "Caddy sites", func() bool { return page.loaded && !page.reading })
	tester.Frame()
	return a, platform, tester, session, fixture
}

func TestCaddySitesInventoryAndMenu(t *testing.T) {
	a, _, tester, session, _ := caddySiteUI(t, nil, nil)
	page := &session.caddy.sites
	wantTexts(t, tester, "Caddy sites from local fixtures", "static.example.test", "proxy.example.test", "raw.example.test")
	if len(page.rows) != 3 {
		t.Fatalf("inventory = %#v", page.rows)
	}
	for _, row := range page.rows {
		actions := a.caddySiteActions(session, &row)
		if row.node.Name == "raw.example.test" && (!row.raw || actions[0].reason == "" || actions[4].reason != "") {
			t.Fatalf("raw row should only allow editor, not form: %+v", actions)
		}
	}
	tester.RightClickAt(rowCenter(t, tester, "raw.example.test"))
	tester.Frame()
	blocked, ok := tester.AccessByRole(ui.RoleMenuItem, locales.Edit())
	if !ok || !blocked.Disabled {
		t.Fatal("unknown directives do not disable form editing in the row menu")
	}
	allowed, ok := tester.AccessByRole(ui.RoleMenuItem, locales.CsiteCode())
	if !ok || allowed.Disabled {
		t.Fatal("unknown directives should allow the code editor in the row menu")
	}
	tester.Key(0, ui.KeyEscape)
	page.table.query = "localhost:8080"
	tester.Frame()
	if !tester.HasText("proxy.example.test") || tester.HasText("static.example.test") {
		t.Fatalf("filter did not match upstream: %q", tester.Texts())
	}
}

func TestCaddySitesToggleConfirmation(t *testing.T) {
	a, platform, tester, session, fixture := caddySiteUI(t, nil, nil)
	page := &session.caddy.sites
	row := &page.rows[0]
	a.caddySiteConfirm(session, row, locales.CsiteDisable(), false)
	tester.Frame()
	systemClick(t, tester, locales.Cancel())
	select {
	case <-fixture.changes:
		t.Fatal("cancel sent a disable change")
	default:
	}
	a.caddySiteConfirm(session, row, locales.CsiteDisable(), false)
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "disabled site", func() bool { return page.dialog != nil && page.dialog.done && !page.reading })
	disabled := <-fixture.changes
	page.files = []monitor.CaddyFile{{Path: caddySiteMainPath, Content: caddySiteMain}, disabled.Writes[0]}
	page.inventory()
	row = &page.rows[0]
	if row.active() || row.name() != "proxy.example.test" {
		t.Fatalf("wrong disabled row: %+v", row)
	}
	a.caddySiteConfirm(session, row, locales.CsiteEnable(), false)
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "enabled site", func() bool { return page.dialog != nil && page.dialog.done && !page.reading })
	enabled := <-fixture.changes
	if !enabled.Reload || !reflect.DeepEqual(enabled.Writes, []monitor.CaddyFile{{Path: caddySiteImportedPath, Content: caddySiteImported}}) || enabled.Expect[caddySiteImportedPath] != disabled.Writes[0].Content {
		t.Fatalf("enable did not restore the original bytes: %+v", enabled)
	}
}

func TestCaddySitesInventoryExcludesDirectiveImports(t *testing.T) {
	page := caddySitesPage{caddySectionFiles: caddySectionFiles{
		inst: monitor.CaddyInstall{Method: monitor.CaddyDocker, Config: caddySiteMainPath},
		files: []monitor.CaddyFile{
			{Path: caddySiteMainPath, Content: "safe.example.test {\nimport fragments/proxy\n}\n"},
			{Path: "/etc/caddy/fragments/proxy", Content: "reverse_proxy localhost:8080 {\nflush_interval -1\n}\n"},
		},
	}}
	page.inventory()
	if len(page.rows) != 1 || page.rows[0].name() != "safe.example.test" || !page.rows[0].raw {
		t.Fatalf("the directive fragment must not be editable as a site: %#v", page.rows)
	}
}

func TestCaddySitesDeleteConfirmationAndRollback(t *testing.T) {
	a, platform, tester, session, fixture := caddySiteUI(t, monitor.ErrCaddyInvalid, nil)
	page := &session.caddy.sites
	row := &page.rows[0]
	if row.file.Path != caddySiteImportedPath {
		t.Fatalf("unexpected fixture row %s", row.file.Path)
	}
	a.caddySiteConfirm(session, row, locales.Delete(), true)
	tester.Frame()
	wantTexts(t, tester, "delete confirmation", row.name(), locales.CsiteDeleteWarning())
	systemClick(t, tester, locales.Cancel())
	select {
	case change := <-fixture.changes:
		t.Fatalf("cancel applied a change: %+v", change)
	default:
	}
	a.caddySiteConfirm(session, row, locales.Delete(), true)
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "Caddy rollback", func() bool { return !page.work && page.dialog != nil && page.dialog.failure != "" })
	want := monitor.CaddyChange{
		Writes: []monitor.CaddyFile{{Path: caddySiteImportedPath, Content: strings.Replace(caddySiteImported, "proxy.example.test {\r\n\treverse_proxy localhost:8080\r\n}", "", 1)}},
		Expect: map[string]string{caddySiteMainPath: caddySiteMain, caddySiteImportedPath: caddySiteImported}, Reload: true,
	}
	if change := <-fixture.changes; !reflect.DeepEqual(change, want) {
		t.Fatalf("apply = %#v; want %#v", change, want)
	}
	if !strings.Contains(page.dialog.output, "validation output from fixture") || !strings.Contains(page.dialog.output, locales.CsiteRollback()) {
		t.Fatalf("validation and rollback details missing: %q", page.dialog.output)
	}
}

func TestCaddySitesFormValidateAndHash(t *testing.T) {
	a, platform, tester, session, fixture := caddySiteUI(t, nil, errors.New("rejected fixture config"))
	page := &session.caddy.sites
	a.caddySiteOpenForm(session, nil, false, false)
	form := page.form
	form.addresses, form.spec.Type, form.upstreams = "new.example.test", caddyfile.Proxy, "localhost:9090"
	const password = "do-not-include-this-password"
	form.auth = []caddySiteFormAuth{{name: "user", password: password}}
	tester.Frame()
	if preview := form.value(page.inst, false).Text; preview == "" || strings.Contains(preview, password) || strings.Contains(preview, caddySitePreviewHash) {
		t.Fatalf("unsafe or missing preview: %q", preview)
	}
	systemClick(t, tester, locales.CsiteValidate())
	platform.until(t, "validation response", func() bool { return !page.work && page.dialog != nil && page.dialog.failure != "" })
	if secret := <-fixture.passwords; secret != password {
		t.Fatal("wrong password passed to hash")
	}
	files := <-fixture.checks
	if len(files) != 1 || !strings.Contains(files[0].Content, caddySitePreviewHash) || strings.Contains(files[0].Content, password) || !strings.Contains(page.dialog.output, "separate validation output") {
		t.Fatal("wrong validation payload or output")
	}
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.Cancel())
	select {
	case <-fixture.passwords:
		t.Fatal("hashed for a cancelled save")
	default:
	}
	select {
	case <-fixture.changes:
		t.Fatal("applied a cancelled save")
	default:
	}
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "saved Caddy site", func() bool { return page.dialog != nil && page.dialog.done })
	<-fixture.passwords
	change := <-fixture.changes
	want := caddySiteMain + "\nnew.example.test {\n\tbasic_auth {\n\t\tuser " + caddySitePreviewHash + "\n\t}\n\treverse_proxy localhost:9090\n}\n"
	if !change.Reload || change.Expect[caddySiteMainPath] != caddySiteMain || !reflect.DeepEqual(change.Writes, []monitor.CaddyFile{{Path: caddySiteMainPath, Content: want}}) {
		t.Fatalf("wrong save payload: %+v", change)
	}
	if form.auth[0].password != "" || strings.Contains(page.dialog.output+page.dialog.failure, password) {
		t.Fatal("password retained after save")
	}
	if !strings.Contains(page.dialog.output, locales.CsiteValidated()) || strings.Contains(page.dialog.output, "encoded-sensitive-marker") {
		t.Fatalf("transaction progress was not decoded safely: %q", page.dialog.output)
	}
}

func TestCaddySitesChangedConfigAsksToRefresh(t *testing.T) {
	a, platform, tester, session, fixture := caddySiteUI(t, monitor.ErrCaddyChanged, nil)
	page := &session.caddy.sites
	a.caddySiteConfirm(session, &page.rows[0], locales.Delete(), true)
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "stale configuration", func() bool { return !page.work && page.dialog != nil && page.dialog.failure != "" })
	<-fixture.changes
	if page.dialog.failure != locales.CsiteChanged() {
		t.Fatalf("wrong stale-configuration feedback: %q", page.dialog.failure)
	}
}

func TestCaddySitesProbeArguments(t *testing.T) {
	addresses := []string{"http://plain.example.test", "tls.example.test:8443", "*.example.test", ":8080", "https://user:x@example.test", "file:///etc/passwd", "{$DOMAIN}", "[::1]:8443"}
	want := []string{"http://plain.example.test", "https://tls.example.test:8443", "https://[::1]:8443"}
	if urls := caddySiteURLs(addresses); !reflect.DeepEqual(urls, want) {
		t.Fatalf("probe URLs = %v; want %v", urls, want)
	}
	if script := monitor.SystemCommand(caddySiteCurlArgs("https://site.example.test/that's")...); script != `'curl' '-q' '-sSI' '--connect-timeout' '5' '--max-time' '12' '--proto' '=http,https' '--' 'https://site.example.test/that'\''s'` {
		t.Fatalf("curl shell script: %s", script)
	}
	if got := caddySiteParsePHPSockets("/run/php/php8.3-fpm.sock\r\n/run/php/php8.3-fpm.sock\n/run/../etc/ignored.sock\n/tmp/ignored.sock"); !reflect.DeepEqual(got, []string{"unix//run/php/php8.3-fpm.sock"}) {
		t.Fatalf("PHP sockets = %v", got)
	}
}

func TestCaddySitesScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				a, platform, tester, session, _ := caddySiteUI(t, nil, nil)
				platform.SetLocale(language)
				tester.SetDark(mode == "dark")
				tester.SetSize(1280, 820)
				shot := func(scene string) {
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "csite-"+scene+"-"+language+"-"+mode+".png")
				}
				shot("short")
				a.setPanelOnRight(true)
				shot("right")
				a.setPanelOnRight(false)
				a.openPanelTab(session, tabCaddy)
				shot("full")
				page := &session.caddy.sites
				a.caddySiteOpenForm(session, nil, false, false)
				form := page.form
				*form = *newCaddySiteForm(caddyfile.SiteTemplate("proxy"))
				shot("proxy-dialog")
				*form = *newCaddySiteForm(caddyfile.SiteTemplate("wordpress"))
				shot("php-dialog")
				a.caddySiteOpenForm(session, nil, false, true)
				shot("templates-dialog")
			})
		}
	}
}
