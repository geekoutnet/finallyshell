package app

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func TestCaddyPanelServiceCommand(t *testing.T) {
	install := monitor.CaddyInstall{Method: monitor.CaddyAPT, Unit: "caddy.service", ServiceManager: "systemd", Active: "active"}
	command, err := caddyServiceCommand(install, "stop")
	if err != nil || command != "'systemctl' 'stop' 'caddy.service'" {
		t.Fatalf("stop = %q, %v", command, err)
	}
	install.Unit = "caddy.service\nother.service"
	if _, err := caddyServiceCommand(install, "stop"); err == nil {
		t.Fatal("accepted invalid unit")
	}
	for _, file := range []string{"relative", "/a/../b", "/a\nb", "/a\x00b", "/"} {
		if caddyValidPath(file) {
			t.Errorf("accepted %q", file)
		}
	}
	for _, test := range []struct {
		install         monitor.CaddyInstall
		action, command string
	}{
		{monitor.CaddyInstall{Method: monitor.CaddyDocker, Container: "caddy-main"}, "enable", "'docker' 'update' '--restart' 'unless-stopped' 'caddy-main'"},
		{monitor.CaddyInstall{Method: monitor.CaddyAPK, ServiceManager: "openrc", Unit: "caddy"}, "disable", "'rc-update' 'del' 'caddy' 'default'"},
		{monitor.CaddyInstall{Method: monitor.CaddyBinary, Binary: "/opt/caddy's/caddy", Config: "/etc/caddy/main file", Adapter: "caddyfile"}, "validate", "'/opt/caddy'\\''s/caddy' 'validate' '--config' '/etc/caddy/main file' '--adapter' 'caddyfile'"},
	} {
		if got, err := caddyServiceCommand(test.install, test.action); err != nil || got != test.command {
			t.Errorf("%s command = %q, %v; want %q", test.action, got, err, test.command)
		}
	}
}

func TestCaddyPanelReadCommands(t *testing.T) {
	var calls []string
	run := func(_ context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		if input != nil || progress != nil {
			t.Fatal("read unexpectedly set stdin or progress")
		}
		calls = append(calls, script)
		if strings.HasPrefix(script, "'systemctl'") {
			return "Id=caddy.service\nMainPID=28\nMemoryCurrent=12345\nActiveEnterTimestamp=timestamp\n", "", nil
		}
		return `{"apps":{}}`, "", nil
	}
	backend := caddyMonitorBackend{}
	install := caddyPanelDemo
	install.Config = "/etc/caddy/it's a file"
	text, err := backend.adapt(context.Background(), run, install)
	if err != nil || text != `{"apps":{}}` || len(calls) != 1 || calls[0] != "'/usr/bin/caddy' 'adapt' '--config' '/etc/caddy/it'\\''s a file' '--adapter' 'caddyfile' '--pretty'" {
		t.Fatalf("adapt = %q, %v; calls %v", text, err, calls)
	}
	install.Config = "/etc/bad\npath"
	if _, err := backend.adapt(context.Background(), run, install); err == nil || len(calls) != 1 {
		t.Fatalf("invalid path was not rejected: %v, %v", err, calls)
	}
	if got, err := backend.serviceInfo(context.Background(), run, install); err != nil || got != (caddyServiceInfo{PID: 28, Memory: 12345, Since: "timestamp"}) {
		t.Fatalf("service info = %+v, %v", got, err)
	}
}

var caddyPanelDemo = monitor.CaddyInstall{
	Method: monitor.CaddyAPT, Binary: "/usr/bin/caddy", Version: "v2.10.0", PackageLatest: "2.11.1", Config: "/etc/caddy/Caddyfile", Adapter: "caddyfile",
	Unit: "caddy.service", Active: "active", Enabled: true, ServiceManager: "systemd", User: "caddy", AdminAddr: "localhost:2019",
	DataDir: "/var/lib/caddy/.local/share/caddy", Envfile: "/etc/caddy/caddy.env", OS: "debian", Listening: []string{":80", ":443"}, Modules: []string{"dns.providers.demo"},
}

const caddyPanelAccessLog = `{"level":"info","ts":1700000000.125,"request":{"remote_ip":"192.0.2.42","host":"app.example.test","method":"GET","uri":"/health"},"status":200,"duration":0.005,"size":1024}`

type fakeCaddyPanel struct {
	caddyMonitorBackend
	found    monitor.CaddyInstall
	calls    chan any
	applyErr error
}

func (fake fakeCaddyPanel) detect(context.Context, monitor.CaddyRunner) (monitor.CaddyInstall, error) {
	return fake.found, nil
}

func (fakeCaddyPanel) files(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]caddyFileInfo, error) {
	return caddyTestFiles(), nil
}

func caddyTestFiles() []caddyFileInfo {
	modified := time.Date(2026, 11, 25, 13, 46, 0, 0, time.Local)
	return []caddyFileInfo{
		{monitor.CaddyFile{Path: caddyPanelDemo.Config, Content: "import sites/*.caddy\n"}, modified},
		{monitor.CaddyFile{Path: "/etc/caddy/sites/app.caddy", Content: "app.example.test {\nreverse_proxy 127.0.0.1:3020\n}\n"}, modified},
	}
}

func (fakeCaddyPanel) admin(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, method, endpoint, body string) (string, error) {
	if method != "GET" || body != "" {
		return "", errors.New("unexpected request")
	}
	switch endpoint {
	case "/config/":
		return `{"apps":{"http":{"servers":{"srv0":{"routes":[{"match":[{"host":["app.example.test"]}]}]}}}}}`, nil
	case "/reverse_proxy/upstreams":
		return `[{"address":"127.0.0.1:3020","fails":1},{"address":"127.0.0.1:3021","fails":0}]`, nil
	default:
		return "", errors.New("unexpected endpoint")
	}
}

func (fakeCaddyPanel) certs(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyCertificateInfo, error) {
	now := time.Now()
	return caddyCertificateInfo{Rows: []caddyCertificate{
		{Domains: "app.example.test", Issuer: "Let's Encrypt", Key: "ECDSA P-256", Path: "/data/certificates/app.example.test.crt", Expires: now.AddDate(0, 0, 4), PEM: "PUBLIC CERTIFICATE FIXTURE"},
		{Domains: "docs.example.test", Issuer: "ZeroSSL", Key: "RSA 2048", Path: "/data/certificates/docs.example.test.crt", Expires: now.AddDate(0, 0, 20)},
		{Domains: "internal.example.test", Issuer: "Caddy Local CA", Key: "ECDSA P-256", Path: "/data/certificates/internal.example.test.crt", Expires: now.AddDate(0, 0, 60)},
	}, Root: caddyCertificate{Subject: "Caddy Local Authority", PEM: "PUBLIC ROOT CERTIFICATE FIXTURE", Path: "/data/pki/authorities/local/root.crt", Expires: now.AddDate(2, 0, 0)}, OnDemand: true}, nil
}

func (fakeCaddyPanel) serviceInfo(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyServiceInfo, error) {
	return caddyServiceInfo{PID: 1028, Memory: 12 << 20, Since: "2026-11-25 10:14:20"}, nil
}

func (fake fakeCaddyPanel) service(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, action string, progress func(string)) (string, error) {
	command, err := caddyServiceCommand(install, action)
	if err != nil {
		return "", err
	}
	fake.calls <- action
	output, _, err := run(ctx, command, nil, progress)
	return output, err
}

func (fake fakeCaddyPanel) validate(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, files []monitor.CaddyFile) (string, error) {
	fake.calls <- files
	return "Valid configuration", nil
}

func (fakeCaddyPanel) format(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, text string) (string, error) {
	return strings.ReplaceAll(text, "\nreverse_proxy", "\n\treverse_proxy"), nil
}

func (fake fakeCaddyPanel) apply(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, change monitor.CaddyChange) (monitor.CaddyApplyResult, error) {
	fake.calls <- change
	return monitor.CaddyApplyResult{Backup: "/etc/caddy/.backups/20261125120100", Validate: "Valid configuration", Reloaded: fake.applyErr == nil, RolledBack: fake.applyErr != nil}, fake.applyErr
}

func (fakeCaddyPanel) backups(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]caddyBackup, error) {
	return []caddyBackup{{Path: "/etc/caddy/.backups/20261124070100"}}, nil
}

func (fakeCaddyPanel) backupPreview(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, _ string) (caddyBackupPreview, error) {
	file := caddyTestFiles()[1].CaddyFile
	modified := monitor.CaddyFile{Path: file.Path, Content: strings.ReplaceAll(file.Content, ":3020", ":3010")}
	return caddyBackupPreview{Files: []monitor.CaddyFile{file}, Change: monitor.CaddyChange{Writes: []monitor.CaddyFile{modified}, Expect: map[string]string{file.Path: file.Content}}}, nil
}

func (fake fakeCaddyPanel) managedPreview(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyBackupPreview, error) {
	return fake.backupPreview(ctx, run, install, "")
}

func (fakeCaddyPanel) installFacts(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyInstallFacts, error) {
	return caddyInstallFacts{Docker: true, Firewall: "ufw", Versions: []string{"v2.11.1", "v2.10.0"}}, nil
}

func (fake fakeCaddyPanel) install(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, options caddyInstallOptions, _ func(string)) (string, error) {
	fake.calls <- options
	return "Installed", nil
}

func (fake fakeCaddyPanel) update(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, version string, repair bool, _ func(string)) (string, error) {
	fake.calls <- []any{version, repair}
	return "Updated", nil
}

func (fake fakeCaddyPanel) uninstall(_ context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall, purge bool, _ func(string)) (string, error) {
	fake.calls <- purge
	return "Uninstalled", nil
}

func (fakeCaddyPanel) logFiles([]monitor.CaddyFile) []string {
	return []string{"/var/log/caddy/access.log"}
}

func newCaddyPanelTest(t *testing.T, installed monitor.CaddyInstall) (*App, *Session, *fakePlatform, *ui.Tester, fakeCaddyPanel, <-chan string) {
	t.Helper()
	a, platform, tester, _ := overviewApp(t)
	s := a.sessions[0]
	fake := fakeCaddyPanel{found: installed, calls: make(chan any, 32)}
	scripts := make(chan string, 32)
	s.caddy.state.backend = fake
	s.caddy.state.run = func(ctx context.Context, script string, _ io.Reader, progress func(string)) (string, string, error) {
		scripts <- script
		if strings.HasPrefix(script, "exec ") {
			progress(caddyPanelAccessLog + "\n" + strings.ReplaceAll(caddyPanelAccessLog, `"status":200`, `"status":503`) + "\n")
			<-ctx.Done()
			return "", "", ctx.Err()
		}
		if progress != nil {
			progress("completed\n")
		}
		return "", "", nil
	}
	t.Cleanup(func() { a.stopPoll(&s.caddy.logs.poll) })
	a.openPanelTab(s, tabCaddy)
	return a, s, platform, tester, fake, scripts
}

func caddyTestCall(t *testing.T, calls <-chan any) any {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not call fake")
		return nil
	}
}

func TestCaddyPanelOverview(t *testing.T) {
	for _, method := range []string{monitor.CaddyAPT, monitor.CaddyDNF, monitor.CaddyPacman, monitor.CaddyAPK, monitor.CaddyBinary, monitor.CaddyDocker, ""} {
		t.Run(method, func(t *testing.T) {
			install := caddyPanelDemo
			install.Method = method
			if method == monitor.CaddyDocker {
				install.Container, install.Unit, install.Active = "caddy", "", "running"
			}
			if method == monitor.CaddyBinary {
				install.Active, install.Conflicts = "failed", []string{"nginx"}
			}
			_, s, platform, tester, _, _ := newCaddyPanelTest(t, install)
			tester.Frame()
			platform.until(t, "detection", func() bool { return s.caddy.state.loaded })
			if method == "" {
				wantTexts(t, tester, "missing", locales.CpanelNotInstalled(), locales.CpanelInstall())
				clickNamed(t, tester, locales.CpanelInstall())
				if s.caddy.section != caddySecInstall {
					t.Fatal("install shortcut did not open wizard")
				}
				return
			}
			platform.until(t, "overview details", func() bool { return s.caddy.overview.data.certs == 3 })
			wantTexts(t, tester, "overview", caddyInstallName(method), caddyActiveName(install.Active), locales.CpanelUpdateAvailable("2.11.1"))
			if method == monitor.CaddyBinary {
				wantTexts(t, tester, "conflict", locales.CpanelConflicts("nginx"))
			}
			if s.caddy.overview.data.sites != 1 || s.caddy.overview.data.unhealthy != 1 || s.caddy.overview.data.expiring != 2 {
				t.Fatalf("overview metrics: %+v", s.caddy.overview.data)
			}
		})
	}
}

func TestCaddyPanelConfirmService(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "reload", "validate", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			install := caddyPanelDemo
			if action == "start" {
				install.Active = "failed"
			}
			if action == "enable" {
				install.Enabled = false
			}
			a, s, platform, tester, fake, scripts := newCaddyPanelTest(t, install)
			tester.Frame()
			platform.until(t, "detection", func() bool { return s.caddy.state.loaded })
			index := slices.Index([]string{"start", "stop", "restart", "reload", "validate", "enable", "disable"}, action)
			operation := a.caddyServiceActions(s, install)[index]
			if operation.reason != "" {
				t.Fatal(operation.reason)
			}
			operation.invoke()
			tester.Frame()
			expected, _ := caddyServiceCommand(install, action)
			if s.caddy.state.dialog.target != expected {
				t.Fatal("wrong displayed command")
			}
			clickNamed(t, tester, locales.Cancel())
			select {
			case called := <-fake.calls:
				t.Fatalf("cancel ran %v", called)
			default:
			}
			if s.caddy.state.dialog != nil {
				t.Fatal("dialog not dismissed")
			}
			operation.invoke()
			tester.Frame()
			clickNamed(t, tester, locales.SvcConfirm())
			call := caddyTestCall(t, fake.calls)
			if action == "validate" {
				if files, ok := call.([]monitor.CaddyFile); !ok || len(files) != 0 {
					t.Fatalf("validate called with %#v", call)
				}
			} else {
				if call != action {
					t.Fatalf("operation = %v", call)
				}
				select {
				case script := <-scripts:
					if script != expected {
						t.Fatalf("command = %s, want %s", script, expected)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("no fake runner call")
				}
			}
			platform.until(t, "operation finished", func() bool { return s.caddy.state.dialog.done })
		})
	}
}

func TestCaddyPanelServiceMenu(t *testing.T) {
	a, s, platform, tester, _, _ := newCaddyPanelTest(t, caddyPanelDemo)
	tester.Frame()
	platform.until(t, "installation", func() bool { return s.caddy.state.loaded })
	a.active = 0
	a.openCaddyPage(s, caddySecOverview)
	a.setPanelOnRight(true)
	tester.Frame()
	tester.Frame()
	clickNamed(t, tester, locales.SvcActions())
	if err := tester.ChooseMenuItem(locales.SvcStart()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("running Caddy must disable Start: %v; menu: %v", err, tester.Menu())
	}
	if err := tester.ChooseMenuItem(locales.SvcStop()); err != nil {
		t.Fatal(err)
	}
	if s.caddy.state.dialog == nil || s.caddy.state.dialog.target != "'systemctl' 'stop' 'caddy.service'" || s.caddy.state.dialog.consequence != locales.CpanelStopWarning() {
		t.Fatalf("missing stop warning: %+v", s.caddy.state.dialog)
	}
}

func TestCaddyPanelConfigEditor(t *testing.T) {
	_, s, platform, tester, fake, _ := newCaddyPanelTest(t, caddyPanelDemo)
	s.caddy.section = caddySecConfig
	var spec CodeEditorSpec
	previous := openCodeEditorImpl
	openCodeEditorImpl = func(_ *App, opened CodeEditorSpec) { spec = opened }
	t.Cleanup(func() { openCodeEditorImpl = previous })
	tester.Frame()
	platform.until(t, "files", func() bool { return len(s.caddy.config.files) == 2 })
	tester.Frame()
	wantTexts(t, tester, "files", caddyPanelDemo.Config, "/etc/caddy/sites/app.caddy")
	clickNamed(t, tester, locales.CpanelEditor())
	if !spec.Root || spec.Session != s || spec.Language != "caddyfile" || len(spec.Files) != 2 || spec.Files[0].Path != caddyPanelDemo.Config || spec.Validate == nil || spec.Format == nil || spec.Save == nil || spec.Saved == nil {
		t.Fatalf("bad editor spec: %+v", spec)
	}
	old := spec.Files[1]
	files := []monitor.CaddyFile{{Path: old.Path, Content: "app.example.test {\n\trespond 200\n}\n"}}
	if _, err := spec.Save(context.Background(), files, nil); !errors.Is(err, monitor.ErrCaddyChanged) {
		t.Fatalf("save with no expectation: %v", err)
	}
	expect := map[string]string{old.Path: old.Content}
	if _, err := spec.Save(context.Background(), files, expect); err != nil {
		t.Fatal(err)
	}
	if call := caddyTestCall(t, fake.calls); !reflect.DeepEqual(call, monitor.CaddyChange{Writes: files, Expect: expect, Reload: true}) {
		t.Fatalf("save: %#v", call)
	}
}

func TestCaddyPanelRestoreAndRollback(t *testing.T) {
	_, s, platform, tester, fake, _ := newCaddyPanelTest(t, caddyPanelDemo)
	fake.applyErr = monitor.ErrCaddyInvalid
	s.caddy.state.backend = fake
	s.caddy.section = caddySecConfig
	tester.Frame()
	platform.until(t, "config files", func() bool { return len(s.caddy.config.files) > 0 })
	clickNamed(t, tester, locales.CpanelBackups())
	platform.until(t, "backup list", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.body != nil })
	clickNamed(t, tester, locales.CpanelRestore())
	platform.until(t, "restore diff", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.confirm != nil })
	select {
	case called := <-fake.calls:
		t.Fatalf("preview mutated: %v", called)
	default:
	}
	clickNamed(t, tester, locales.SvcConfirm())
	change := caddyTestCall(t, fake.calls).(monitor.CaddyChange)
	if !change.Reload || len(change.Writes) != 1 || !strings.Contains(change.Writes[0].Content, ":3010") || len(change.Expect) != 1 {
		t.Fatalf("restore: %+v", change)
	}
	platform.until(t, "rollback output", func() bool { return s.caddy.state.dialog.done })
	if !strings.Contains(s.caddy.state.dialog.output, locales.CpanelRolledBack()) || s.caddy.state.dialog.failure == "" {
		t.Fatalf("missing rollback/error: %+v", s.caddy.state.dialog)
	}
}

func TestCaddyPanelLogAndCertificateHelpers(t *testing.T) {
	line, ok := caddyParseAccess("2026-11-25 caddy[200]: " + caddyPanelAccessLog)
	if !ok || line.Status != 200 || line.Request.IP != "192.0.2.42" || line.Timestamp != 1700000000.125 || line.Size != 1024 {
		t.Fatalf("JSON log: %+v %v", line, ok)
	}
	var page serverLogsPage
	page.append([]string{caddyPanelAccessLog, "WARN: interrupted"})
	page.filter, page.regex = `(?i)^warn`, true
	if rows := page.shown(); len(rows) != 1 || rows[0].text != "WARN: interrupted" {
		t.Fatalf("regex result: %v", rows)
	}
	sources := caddyLogSources(caddyPanelDemo, []string{"/var/log/caddy/it's.log", "/etc/shadow", "/bad\nlog"})
	if len(sources) != 2 || !strings.Contains(sources[1].script, "'tail' '-n' '300' '-F' '--' '/var/log/caddy/it'\\''s.log'") {
		t.Fatalf("log commands: %+v", sources)
	}
	now := time.Now()
	for _, days := range []int{-1, 0, 6, 7, 29, 30} {
		if got := caddyDaysLeft(now.Add(time.Duration(days)*24*time.Hour), now); got != days {
			t.Errorf("days = %d, want %d", got, days)
		}
	}
	if caddyExpiryColor(6) != theme.Danger || caddyExpiryColor(7) != theme.Warning || caddyExpiryColor(30) != theme.Text {
		t.Fatal("wrong certificate thresholds")
	}
}

func TestCaddyPanelInstallAndMigrate(t *testing.T) {
	for osID, want := range map[string]string{"debian": "apt", "ubuntu": "apt", "fedora": "dnf", "rhel": "dnf", "arch": "pacman", "alpine": "apk", "unknown": "binary"} {
		if choices := caddyInstallMethods(osID, true); choices[0] != want || choices[len(choices)-1] != "docker" {
			t.Errorf("%s choices: %v", osID, choices)
		}
	}
	missing := monitor.CaddyInstall{OS: "debian", Conflicts: []string{"nginx"}}
	a, s, platform, tester, fake, _ := newCaddyPanelTest(t, missing)
	s.caddy.section = caddySecInstall
	tester.Frame()
	platform.until(t, "install facts", func() bool { return s.caddy.install.facts.Docker })
	clickNamed(t, tester, locales.CpanelReviewInstall())
	if target := s.caddy.state.dialog.target; !strings.Contains(target, "Cloudsmith") || !strings.Contains(target, locales.CpanelStartOption(locales.CpanelNo())) {
		t.Fatalf("review: %s", target)
	}
	clickNamed(t, tester, locales.Cancel())
	select {
	case called := <-fake.calls:
		t.Fatalf("cancel ran %v", called)
	default:
	}
	a.caddyConfirmInstall(s, missing, caddyInstallOptions{Method: "apt", Start: true, Enable: true}, s.caddy.install.facts)
	tester.Frame()
	clickNamed(t, tester, locales.SvcConfirm())
	if call := caddyTestCall(t, fake.calls); call != (caddyInstallOptions{Method: "apt"}) {
		t.Fatalf("unsafe install options: %#v", call)
	}
	platform.until(t, "install completion", func() bool { return s.caddy.state.dialog.done })
	if s.caddy.section != caddySecOverview {
		t.Fatal("overview not opened on install")
	}
	change, count := caddyNginxChange(monitor.CaddyFile{Path: "/etc/caddy/Caddyfile", Content: "# original\n"}, []caddyNginxSite{{Content: "first.example {\n respond 200\n}"}, {Content: "not-selected"}}, []bool{true, false}, false)
	if count != 1 || change.Reload || !strings.Contains(change.Writes[0].Content, "first.example") || strings.Contains(change.Writes[0].Content, "not-selected") || change.Expect["/etc/caddy/Caddyfile"] != "# original\n" {
		t.Fatalf("migration: %#v", change)
	}
}

func TestCaddyPanelUpdateUninstallAndManaged(t *testing.T) {
	a, s, platform, tester, fake, _ := newCaddyPanelTest(t, caddyPanelDemo)
	s.caddy.section = caddySecInstall
	tester.Frame()
	platform.until(t, "installed", func() bool { return s.caddy.state.loaded })
	for _, step := range []struct {
		open func()
		want any
	}{
		{func() { a.caddyConfirmUpdate(s, "2.11.1", false) }, []any{"2.11.1", false}},
		{func() { a.caddyConfirmUninstall(s, false) }, false},
	} {
		step.open()
		tester.Frame()
		clickNamed(t, tester, locales.SvcConfirm())
		if call := caddyTestCall(t, fake.calls); !reflect.DeepEqual(call, step.want) {
			t.Fatalf("called %#v, want %#v", call, step.want)
		}
		platform.until(t, "action completion", func() bool { return s.caddy.state.dialog.done && s.caddy.state.loaded })
		clickNamed(t, tester, locales.Close())
	}
	a.caddyConfirmUninstall(s, true)
	tester.Frame()
	wantTexts(t, tester, "purge", locales.CpanelTypePurge())
	if s.caddy.state.dialog.confirm != nil {
		t.Fatal("purge available without typing")
	}
	clickNamed(t, tester, locales.CpanelTypePurge())
	tester.Type("caddy")
	tester.Frame()
	clickNamed(t, tester, locales.SvcConfirm())
	if purge := caddyTestCall(t, fake.calls); purge != true {
		t.Fatalf("purge = %#v", purge)
	}
	platform.until(t, "purge completion", func() bool { return s.caddy.state.dialog.done && s.caddy.state.loaded })
	clickNamed(t, tester, locales.Close())
	clickNamed(t, tester, locales.CpanelConvertManaged())
	platform.until(t, "migration preview", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.confirm != nil })
	clickNamed(t, tester, locales.SvcConfirm())
	if change := caddyTestCall(t, fake.calls).(monitor.CaddyChange); !change.Reload || len(change.Writes) != 1 {
		t.Fatalf("managed migration: %+v", change)
	}
	platform.until(t, "migration done", func() bool { return s.caddy.state.dialog.done })
}

func TestCaddyPanelScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				a, s, platform, tester, fake, _ := newCaddyPanelTest(t, caddyPanelDemo)
				platform.SetLocale(language)
				tester.SetDark(mode == "dark")
				shot := func(name string) {
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "caddy-"+name+"-"+language+"-"+mode+".png")
				}
				panel := func(section int, name string, ready func() bool) {
					a.openPanelTab(s, tabCaddy)
					s.caddy.section = section
					tester.Frame()
					platform.until(t, name, ready)
					shot(name + "-full")
					a.active = 0
					a.openCaddyPage(s, section)
					shot(name + "-short")
					a.setPanelOnRight(true)
					shot(name + "-right")
					a.setPanelOnRight(false)
				}
				panel(caddySecOverview, "overview", func() bool { return s.caddy.overview.data.certs == 3 })
				panel(caddySecConfig, "config", func() bool { return len(s.caddy.config.files) == 2 })
				panel(caddySecCerts, "certs", func() bool { return len(s.caddy.certs.data.Rows) == 3 })
				panel(caddySecLogs, "logs", func() bool { return len(s.caddy.logs.viewer.lines) >= 2 })
				panel(caddySecInstall, "migrate", func() bool { return s.caddy.install.facts.Docker })
				a.openPanelTab(s, tabCaddy)
				a.caddyConfirmUninstall(s, true)
				shot("purge-dialog")
				s.caddy.state.dialog = nil
				tester.Frame()
				a.caddyConfirmUpdate(s, "2.11.1", false)
				shot("update-dialog")
				s.caddy.state.dialog = nil
				tester.Frame()
				s.caddy.section = caddySecConfig
				a.caddyFormatAll(s)
				platform.until(t, "format diff", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.confirm != nil })
				shot("format-dialog")
				s.caddy.state.dialog = nil
				tester.Frame()
				a.caddyBackups(s)
				platform.until(t, "backup list", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.body != nil })
				clickNamed(t, tester, locales.CpanelRestore())
				platform.until(t, "restore diff", func() bool { return s.caddy.state.dialog != nil && s.caddy.state.dialog.confirm != nil })
				shot("restore-dialog")
				s.caddy.state.dialog = nil
				s.caddy.state.install.Active, s.caddy.state.install.Conflicts = "failed", []string{"nginx"}
				panel(caddySecOverview, "failed", func() bool { return true })
				fake.found = monitor.CaddyInstall{OS: "ubuntu", Conflicts: []string{"nginx"}}
				s.caddy.state.backend = fake
				a.refreshCaddyOverview(s)
				panel(caddySecOverview, "missing", func() bool { return s.caddy.state.loaded })
				panel(caddySecInstall, "wizard", func() bool { return s.caddy.install.facts.Docker })
				a.openPanelTab(s, tabCaddy)
				a.caddyConfirmInstall(s, fake.found, caddyInstallOptions{Method: "apt", OpenPorts: true}, s.caddy.install.facts)
				shot("install-dialog")
			})
		}
	}
}
