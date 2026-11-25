package app

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func TestCaddySiteFormRenderSecrets(t *testing.T) {
	form := newCaddySiteForm(caddyfile.SiteSpec{
		Addresses: []string{"files.example.com"}, Type: caddyfile.Browser, Root: "/srv/files",
		Auth: []caddyfile.AuthUser{{Name: "existing", Hash: caddySitePreviewHash}},
	})
	const password = "do-not-output-this-password"
	hash := base64.StdEncoding.EncodeToString([]byte("$2a$14$bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	form.auth = append(form.auth, caddySiteFormAuth{name: "new-user", password: password})
	preview := form.value(monitor.CaddyInstall{}, false)
	if preview.Err != nil || strings.Contains(preview.Text, password) || strings.Contains(preview.Text, caddySitePreviewHash) || len(preview.passwords) != 0 || !strings.Contains(preview.Text, "new-user") {
		t.Fatalf("preview did not safely represent authentication: %v", preview.Err)
	}
	snapshot := form.snapshot(monitor.CaddyInstall{})
	form.clearPasswords()
	form.addresses = "changed.example.com"
	calls := 0
	text, err := snapshot.render(context.Background(), func(_ context.Context, plain string) (string, error) {
		calls++
		if plain != password {
			t.Fatal("hash runner received the wrong input")
		}
		return hash, nil
	})
	if err != nil || calls != 1 || !strings.Contains(text, "files.example.com") || !strings.Contains(text, hash) || !strings.Contains(text, caddySitePreviewHash) || strings.Contains(text, password) || form.auth[1].password != "" {
		t.Fatalf("invalid password hashing or snapshot: %v", err)
	}
	_, err = snapshot.render(context.Background(), func(_ context.Context, plain string) (string, error) { return "", errors.New(plain) })
	if err == nil || strings.Contains(err.Error(), password) {
		t.Fatal("unsafe hash error")
	}
}

func TestCaddySiteFormTemplateAndTypeUI(t *testing.T) {
	form := newCaddySiteTemplates()
	var value caddySiteFormValue
	tester := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		ui.Column(c).Width(680).Gap(8).Children(func() {
			value = form.view(c, 820, monitor.CaddyInstall{}, caddySiteFormOptions{})
		})
	}, 1280, 820)
	if err := tester.Click(caddySiteFormText("WordPress")); err != nil {
		t.Fatal(err)
	}
	if form.templates || form.spec.Type != caddyfile.PHP || value.Err == nil {
		t.Fatal("WordPress template did not open an incomplete PHP form")
	}
	typeInto(t, tester, caddySiteFormText("Addresses"), "blog.example.com")
	if value.Err != nil || !strings.Contains(value.Text, "php_fastcgi unix//run/php/php-fpm.sock") {
		t.Fatalf("unexpected PHP preview: %q, %v", value.Text, value.Err)
	}
	choose(t, tester, caddySiteFormText("Type"), caddySiteFormText("Respond"))
	if form.spec.Type != caddyfile.Respond || value.Err != nil || !strings.Contains(value.Text, "respond") || strings.Contains(value.Text, "php_fastcgi") {
		t.Fatalf("site type switch did not update preview: %q, %v", value.Text, value.Err)
	}
}

func TestCaddySiteFormTypePreviews(t *testing.T) {
	for _, entry := range []struct {
		kind        caddyfile.SiteType
		directive   string
		destination string
	}{
		{caddyfile.Static, "file_server", ""},
		{caddyfile.Proxy, "reverse_proxy", ""},
		{caddyfile.PHP, "php_fastcgi", ""},
		{caddyfile.SPA, "try_files", ""},
		{caddyfile.Redirect, "redir", "https://example.com{uri}"},
		{caddyfile.Respond, "respond", ""},
		{caddyfile.Browser, "browse", ""},
	} {
		spec := caddyfile.SiteSpec{
			Addresses: []string{"www.example.com"}, Type: entry.kind, Root: "/srv/www",
			Upstreams: []string{"127.0.0.1:8080"}, PHPUpstream: "unix//run/php/php-fpm.sock",
			RedirectURL: entry.destination, RedirectCode: "308", Status: "200", Body: "Hello world",
		}
		form := newCaddySiteForm(spec)
		value := form.snapshot(monitor.CaddyInstall{})
		if value.Err != nil || !strings.Contains(value.Text, entry.directive) {
			t.Errorf("%s: preview %q: %v", entry.kind, value.Text, value.Err)
		}
	}
}

func TestCaddySiteFormScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, platform := testApp(t)
			platform.SetLocale(language)
			session := demoSession(a, "Caddy", "192.0.2.10")
			for _, mode := range []string{"light", "dark"} {
				for _, size := range []struct {
					name          string
					width, height int
				}{{"full", 1280, 820}, {"short", 460, 350}} {
					for _, scene := range []string{"proxy", "php", "templates"} {
						form := newCaddySiteTemplates()
						if scene != "templates" {
							kind := caddyfile.Proxy
							if scene == "php" {
								kind = caddyfile.PHP
							}
							form = newCaddySiteForm(caddyfile.SiteSpec{
								Addresses: []string{"app.example.com", "www.app.example.com"}, Type: kind,
								Root: "/srv/www", PHPUpstream: "unix//run/php/php-fpm.sock", Upstreams: []string{"127.0.0.1:8080"},
								Encode: []string{"zstd", "gzip"}, SecurityHeaders: true,
							})
						}
						dialog := &systemDialog{title: caddySiteFormText("Setup"), body: func(c *ui.Context, height float32) {
							value := form.view(c, height, monitor.CaddyInstall{}, caddySiteFormOptions{
								PHPSockets: []string{"unix//run/php/php-fpm.sock"},
							})
							if !form.templates {
								ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
									ui.Button(c, locales.CsiteValidate()).Disabled(value.Err != nil)
									ui.PrimaryButton(c, locales.Save()).Disabled(value.Err != nil)
								})
							}
						}}
						view := func(c *ui.Context) { useTheme(c); a.systemDialogView(c, session, &dialog) }
						tester := ui.NewTester(view, size.width, size.height)
						tester.SetLocale(language)
						tester.SetDark(mode == "dark")
						tester.Frame()
						tester.Frame()
						saveShot(t, tester, "csite-"+scene+"-"+size.name+"-"+language+"-"+mode+".png")
					}
				}
			}
		})
	}
}
