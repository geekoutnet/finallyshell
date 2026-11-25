package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const cglobFixture = `# global settings
{
	debug # preserve inline comment
	servers :443 {
		protocols h1 h2 h3
		trusted_proxies static private_ranges # preserve proxy comment
		# keep this timeout
		timeouts {
			read_header 5s
		}
	}
	some_custom_option yes # preserve unknown options
}
(common) {
	encode zstd gzip
}
example.test {
	import common
}
`

func TestCaddyGlobalEntryEditsAreLocal(t *testing.T) {
	doc, err := caddyfile.Parse(cglobFixture)
	if err != nil {
		t.Fatal(err)
	}
	ports, _ := caddyGlobalOptionFor("http_port")
	text, err := caddyGlobalOptionEdit(doc, nil, ports, "http_port 8080")
	if err != nil || strings.Replace(text, "\thttp_port 8080\n", "", 1) != cglobFixture {
		t.Fatalf("inserting an option changed unrelated bytes: %v\n%s", err, text)
	}
	if _, err = caddyGlobalOptionEdit(doc, nil, ports, "http_port 8080\nadmin off"); err == nil {
		t.Fatal("accepted a second directive")
	}
	if _, err = caddyGlobalOptionEdit(doc, nil, ports, "https_port 8443"); err == nil {
		t.Fatal("accepted a different option's name")
	}
	if _, err = caddyGlobalOptionEdit(doc, nil, ports, "http_port 8080\n}\nexternal.test {\nrespond hi"); err == nil {
		t.Fatal("escaped the global block")
	}
	serversOption, _ := caddyGlobalOptionFor("servers")
	text, err = caddyGlobalOptionEdit(doc, nil, serversOption, "servers :8443 {\n\tprotocols h2\n}")
	if err != nil || strings.Replace(text, "\tservers :8443 {\n\t\tprotocols h2\n\t}\n", "", 1) != cglobFixture {
		t.Fatalf("adding a second servers block changed unrelated bytes: %v %q", err, text)
	}
	updated, err := caddyGlobalProxyPreset("servers :443 {\n\tprotocols h2 h3\n\ttrusted_proxies static private_ranges # keep\n\ttimeouts {\n\t\tread_header 3s\n\t}\n}", []string{"192.0.2.0/24", "2001:db8::/32"})
	if err != nil || updated != "servers :443 {\n\tprotocols h2 h3\n\ttrusted_proxies static 192.0.2.0/24 2001:db8::/32 # keep\n\ttimeouts {\n\t\tread_header 3s\n\t}\n}" {
		t.Fatalf("proxy preset: %v\n%s", err, updated)
	}
	if _, err = caddyGlobalProxyPreset("servers {\ntrusted_proxies static private_ranges\ntrusted_proxies custom\n}", []string{"private_ranges"}); err == nil {
		t.Fatal("replaced an ambiguous proxy source")
	}
	servers := doc.GlobalOptions("servers")[0]
	draft := caddyGlobalDedent(doc, servers)
	if caddyGlobalRestoreIndent(doc, servers, draft) != doc.Text(servers) {
		t.Fatal("dedenting and restoring changed existing text")
	}
	updated, err = caddyGlobalNestedEdit(draft, "servers", "protocols", "protocols h2")
	if err != nil || updated != strings.Replace(draft, "protocols h1 h2 h3", "protocols h2", 1) {
		t.Fatalf("setting protocols changed unrelated bytes: %v %q", err, updated)
	}
	updated, err = caddyGlobalNestedEdit(draft, "servers", "strict_sni_host", "strict_sni_host on")
	if err != nil || strings.Replace(updated, "\tstrict_sni_host on\n", "", 1) != draft {
		t.Fatalf("adding nested option changed unrelated bytes: %v %q", err, updated)
	}
}

func TestCaddyGlobalSecretsExcludeEnvironmentDefaults(t *testing.T) {
	for _, value := range []string{"{$DNS_TOKEN:plaintext}", "{env.DNS_TOKEN:plaintext}", "literal"} {
		if !caddyGlobalSecret("acme_dns", "acme_dns provider "+value) {
			t.Fatalf("unprotected value %q", value)
		}
	}
	for _, value := range []string{"{$DNS_TOKEN}", "{env.DNS_TOKEN}"} {
		if caddyGlobalSecret("acme_dns", "acme_dns provider "+value) {
			t.Fatalf("rejected environment reference %q", value)
		}
	}
}

type caddyGlobalTestTransport func(*http.Request) (*http.Response, error)

func (transport caddyGlobalTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestCaddyGlobalCloudflarePresets(t *testing.T) {
	for _, test := range []struct {
		name string
		ipv6 string
		want []string
	}{
		{"valid", "2001:db8::/32\n", []string{"192.0.2.0/24", "2001:db8::/32"}},
		{"malformed", "<html>error</html>", nil},
		{"wrong-family", "192.0.2.0/24", nil},
		{"empty", "\n", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: caddyGlobalTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "www.cloudflare.com" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
				}
				var value string
				switch req.URL.Path {
				case "/ips-v4":
					value = "192.0.2.0/24\r\n192.0.2.0/24\r\n"
				case "/ips-v6":
					value = test.ipv6
				default:
					t.Fatalf("unexpected request path: %s", req.URL.Path)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(value))}, nil
			})}
			got, err := caddyGlobalCloudflareRanges(context.Background(), client)
			if (err != nil) != (test.want == nil) || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("presets = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func caddyGlobalUITest(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, <-chan monitor.CaddyChange) {
	t.Helper()
	a, platform, tester, _ := overviewApp(t)
	session := a.sessions[0]
	changes := make(chan monitor.CaddyChange, 5)
	page := &session.caddy.global
	page.source.backend = caddySectionBackend{
		detect: func(context.Context, monitor.CaddyRunner) (monitor.CaddyInstall, error) {
			return monitor.CaddyInstall{Method: monitor.CaddyAPT, Config: "/etc/caddy/Caddyfile"}, nil
		},
		read: func(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]monitor.CaddyFile, error) {
			return []monitor.CaddyFile{{Path: "/etc/caddy/Caddyfile", Content: cglobFixture}}, nil
		},
		apply: func(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, change monitor.CaddyChange) (monitor.CaddyApplyResult, error) {
			changes <- change
			return monitor.CaddyApplyResult{Validate: "invalid fixture", RolledBack: true}, monitor.ErrCaddyInvalid
		},
	}
	a.openCaddyPage(session, caddySecGlobal)
	tester.Frame()
	platform.until(t, "global options loaded", func() bool { return page.source.loaded && !page.source.reading })
	tester.Frame()
	return a, platform, tester, session, changes
}

func TestCaddyGlobalUIConfirmsAndShowsRollback(t *testing.T) {
	_, platform, tester, session, changes := caddyGlobalUITest(t)
	page := &session.caddy.global
	wantTexts(t, tester, "global options", "debug", locales.CglobSet(), locales.CglobUnset())
	page.options[caddyGlobGeneral].selected.Add("debug")
	tester.Frame()
	systemClick(t, tester, locales.Delete())
	if _, ok := tester.AccessByRole(ui.RoleButton, locales.SvcConfirm()); !ok {
		t.Fatal("missing confirmation")
	}
	systemClick(t, tester, locales.Cancel())
	select {
	case change := <-changes:
		t.Fatalf("cancel sent change: %+v", change)
	default:
	}
	systemClick(t, tester, locales.Delete())
	systemClick(t, tester, locales.SvcConfirm())
	platform.until(t, "global validation output", func() bool { return page.dialog != nil && page.dialog.done })
	tester.Frame()
	if !strings.Contains(page.dialog.output, "invalid fixture") || !strings.Contains(page.dialog.output, locales.CglobRollback()) {
		t.Fatalf("validation output = %q", page.dialog.output)
	}
	change := <-changes
	if len(change.Writes) != 1 || !change.Reload || len(change.Deletes) != 0 || change.Expect["/etc/caddy/Caddyfile"] != cglobFixture ||
		change.Writes[0].Path != "/etc/caddy/Caddyfile" || strings.Contains(change.Writes[0].Content, "\tdebug") ||
		!strings.Contains(change.Writes[0].Content, "# preserve inline comment") ||
		!strings.Contains(change.Writes[0].Content, "some_custom_option yes # preserve unknown options") {
		t.Fatalf("unexpected change: %+v", change)
	}
	systemClick(t, tester, locales.Close())
	page.group = caddyGlobOther
	page.options[caddyGlobOther].selected.Add(page.rows[caddyGlobOther][0].key)
	tester.Frame()
	b, ok := tester.AccessByRole(ui.RoleButton, locales.Edit())
	if !ok || !b.Disabled {
		t.Fatal("unknown option not shown read-only")
	}
	tester.RightClickAt(rowCenter(t, tester, "some_custom_option"))
	tester.Frame()
	systemWantMenuDisabled(t, tester, locales.Edit())
	systemWantMenuDisabled(t, tester, locales.Delete())
}

func TestCaddyGlobalSnippetRules(t *testing.T) {
	sources := []monitor.CaddyFile{{Path: "/etc/caddy/Caddyfile", Content: cglobFixture}, {Path: "/etc/caddy/other.caddy", Content: "(shared) {\n\trespond ok\n}\n"}}
	if name := caddyGlobalSnippetName("(name) {\n\trespond x\n}"); name != "name" {
		t.Fatalf("snippet name: %q", name)
	}
	if name := caddyGlobalSnippetName("(name) {\n\trespond x\n}\nextra.test {\n}"); name != "" {
		t.Fatalf("accepted extra configuration: %q", name)
	}
	rows := caddyGlobalSnippetRows(sources)
	if !caddyGlobalSnippetConflict(rows, nil, "shared") {
		t.Fatal("missed duplicate name in imported file")
	}
	if caddyGlobalSnippetConflict(rows, &rows[1], "shared") {
		t.Fatal("conflicted with snippet being edited")
	}
	if got, err := caddyGlobalWriteSnippet(rows[0].doc, &rows[0], "(common) {\n\tencode gzip\n}"); err != nil || got != strings.Replace(cglobFixture, "zstd gzip", "gzip", 1) {
		t.Fatalf("editing snippet changed other text: %v %q", err, got)
	}
	without := "# file\nexample.test\n\trespond ok\n"
	doc, err := caddyfile.Parse(without)
	if err != nil {
		t.Fatal(err)
	}
	added, err := caddyGlobalWriteSnippet(doc, nil, "(helper) {\n\tencode gzip\n}")
	if err != nil || strings.Replace(added, "(helper) {\n\tencode gzip\n}\n\n", "", 1) != without {
		t.Fatalf("inserting snippet changed implicit site: %v %q", err, added)
	}
}

func TestCaddyGlobalScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				a, platform, tester, session, _ := caddyGlobalUITest(t)
				tester.SetSize(1280, 820)
				platform.SetLocale(language)
				tester.SetDark(mode == "dark")
				shot := func(scene string) {
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "cglob-"+scene+"-"+language+"-"+mode+".png")
				}
				shot("short")
				a.setPanelOnRight(true)
				shot("right")
				a.setPanelOnRight(false)
				a.openPanelTab(session, tabCaddy)
				shot("full")
				page := &session.caddy.global
				page.group = caddyGlobServers
				page.options[caddyGlobServers].selected.Add("servers")
				tester.Frame()
				systemClick(t, tester, locales.Edit())
				shot("servers-dialog")
				page.dialog = nil
				page.group = caddyGlobSnippets
				shot("snippets")
				systemClick(t, tester, locales.CglobSnippetNew())
				shot("snippet-dialog")
			})
		}
	}
}
