package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const composeDemoList = `[{"Name":"shop","Status":"running(2), exited(1)","ConfigFiles":"/srv/shop/compose.yaml,/srv/shop/prod.yml"},{"Name":"warehouse","Status":"exited(1)","ConfigFiles":"/srv/warehouse/compose.yaml"}]`

const composeDemoPS = `[{"ID":"aaa1","Name":"shop-api-1","Project":"shop","Service":"api","State":"running","Health":"healthy","Image":"example/api:2","CreatedAt":"2026-05-01 10:01:02 +0000 UTC"},{"ID":"bbb2","Name":"shop-db-1","Project":"shop","Service":"db","State":"exited","Image":"postgres:16","CreatedAt":"2026-04-02 10:01:02 +0000 UTC"},{"ID":"ccc3","Name":"shop-web-1","Project":"shop","Service":"web","State":"running","Image":"nginx:alpine","CreatedAt":"2026-05-01 10:01:02 +0000 UTC","Publishers":[{"URL":"127.0.0.1","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}]`

func composePageTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *fakeDocker, <-chan dockerActionCall) {
	t.Helper()
	data := demoDocker()
	data.Containers = nil
	a, platform, tester, reader := dockerApp(t, data)
	s := a.sessions[0]
	s.Host.Addr, s.ip = "192.0.2.10", "192.0.2.10"
	if err := updateComposeRegistry(composeRegistryPath(s), composeRegistration{Project: "staging", Dir: "/opt/compose/staging", Marker: "test-marker"}, false); err != nil {
		t.Fatal(err)
	}
	calls := make(chan dockerActionCall, 8)
	s.overview.docker.run = func(ctx context.Context, _ *ssh.Client, script string, progress func(string)) (string, error) {
		switch {
		case strings.Contains(script, "'version'"):
			return "Docker Compose version v2.33.0", nil
		case strings.Contains(script, "'ls' '-a'"):
			return composeDemoList, nil
		case strings.Contains(script, "'volume' 'ls'"):
			return "shop_db_data\nshop_cache_data\n", nil
		case strings.Contains(script, "'ps' '-a'"):
			if strings.Contains(script, "'-p' 'shop'") {
				return composeDemoPS, nil
			}
			return "[]", nil
		}
		call := dockerActionCall{script: script, reply: make(chan string, 1), progress: progress}
		calls <- call
		select {
		case output := <-call.reply:
			if strings.HasPrefix(output, "error:") {
				return output, &monitor.DockerExit{Status: 1}
			}
			return output, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a.openDockerPage(s, dockerSecCompose)
	tester.SetSize(1280, 820)
	a.openPanelTab(s, tabDocker)
	tester.Frame()
	platform.until(t, "Compose projects and services", func() bool { return len(s.docker.compose.serviceRows) == 3 })
	tester.Frame()
	return a, platform, tester, reader, calls
}

func composeClickButton(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	button, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok || button.Disabled {
		t.Fatalf("no enabled button %q; texts %q", label, tester.Texts())
	}
	tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	tester.Frame()
}

func TestDockerComposeSection(t *testing.T) {
	a, _, tester, _, _ := composePageTestApp(t)
	page := &a.sessions[0].docker.compose
	wantTexts(t, tester, "project and service rows", "staging", "warehouse", "shop-api-1", "shop-db-1", "shop-web-1", "nginx:alpine", locales.DockerCmpProjectCount(3))
	clickNamed(t, tester, locales.DockerCmpFilter())
	tester.Type("POSTGRES")
	tester.Frame()
	if tester.HasText("shop-web-1") || !tester.HasText("shop-db-1") {
		t.Fatalf("filter: %q", tester.Texts())
	}
	page.filter = ""
	tester.Frame()
	rows := composeVisibleServices(page.serviceRows, "", ui.SortOrder{Column: "created"}, time.Now())
	if len(rows) != 3 || rows[0].Service != "db" {
		t.Fatalf("created sort: %+v", rows)
	}
	tester.RightClickAt(rowCenter(t, tester, "shop-web-1"))
	if err := tester.ChooseMenuItem(locales.DockerStart()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("starting a running service: %v; menu %q", err, tester.Menu())
	}
	if err := tester.ChooseMenuItem(locales.DockerStop()); err != nil {
		t.Fatalf("stop unavailable: %v; menu %q", err, tester.Menu())
	}
}

func TestDockerComposeConfirmAndRefresh(t *testing.T) {
	a, platform, tester, reader, calls := composePageTestApp(t)
	s, page := a.sessions[0], &a.sessions[0].docker.compose
	openDown := func() {
		if err := tester.Click(locales.DockerCmpActions()); err != nil {
			t.Fatal(err)
		}
		chooseDockerAction(t, tester, locales.DockerCmpDown())
		clickNamed(t, tester, locales.DockerCmpVolumes())
		wantTexts(t, tester, "volume warning", locales.DockerCmpVolumeLoss("shop"))
		platform.until(t, "volume names", func() bool { return len(page.dialog.volumeNames) == 2 })
	}
	openDown()
	composeClickButton(t, tester, locales.Cancel())
	select {
	case call := <-calls:
		t.Fatalf("cancel ran %s", call.script)
	default:
	}
	openDown()
	composeClickButton(t, tester, locales.DockerCmpDown())
	const want = "export LC_ALL=C\nexec 2>&1\ncd '/srv/shop' || exit 1\ndocker 'compose' '-p' 'shop' '-f' '/srv/shop/compose.yaml' '-f' '/srv/shop/prod.yml' 'down' '-v'\n"
	call := waitDockerAction(t, calls, "'down' '-v'")
	if call.script != want || call.progress == nil {
		t.Fatalf("down = %q", call.script)
	}
	call.progress("Removing volumes…\n")
	platform.until(t, "progress", func() bool { return strings.Contains(page.dialog.work.view.output, "Removing") })
	call.reply <- "error: project refused"
	platform.until(t, "finished and refreshing", func() bool { return page.dialog.work.view.done && s.overview.docker.loading })
	reader.next(t).reply <- dockerReply{d: s.overview.docker.d}
	platform.until(t, "section and Overview re-read", func() bool { return !page.reading && !s.overview.docker.loading && !page.serviceReading })
	wantTexts(t, tester, "action output", "error: project refused")
}

func TestDockerComposeActionTargets(t *testing.T) {
	a, platform, tester, reader, calls := composePageTestApp(t)
	s := a.sessions[0]
	page := &s.docker.compose
	project := page.projects(&s.overview.docker.d)[0]
	project.Services = page.serviceRows
	for _, test := range []struct {
		action monitor.ComposeAction
		rows   []monitor.Container
		want   string
	}{
		{monitor.ComposeUp, nil, "'up' '-d'"},
		{monitor.ComposeStart, project.Services[1:2], "'start' 'db'"},
		{monitor.ComposeStop, project.Services, "'stop' 'api' 'db' 'web'"},
		{monitor.ComposeRestart, project.Services[:1], "'restart' 'api'"},
		{monitor.ComposePull, project.Services[:1], "'pull' 'api'"},
		{monitor.ComposeRecreate, project.Services[:1], "'up' '-d' '--force-recreate' '--no-deps' 'api'"},
		{monitor.ComposeDown, nil, "'down'"},
	} {
		t.Run(string(test.action), func(t *testing.T) {
			for _, confirm := range []bool{false, true} {
				a.openComposeAction(s, project, test.action, test.rows, false)
				tester.Frame()
				if !confirm {
					composeClickButton(t, tester, locales.Cancel())
					select {
					case call := <-calls:
						t.Fatalf("cancel ran %s", call.script)
					default:
					}
					continue
				}
				composeClickButton(t, tester, composeActionLabel(test.action))
				call := waitDockerAction(t, calls, test.want)
				call.reply <- "done"
				platform.until(t, "overview refresh", func() bool { return s.overview.docker.loading })
				reader.next(t).reply <- dockerReply{d: s.overview.docker.d}
				platform.until(t, "section refresh", func() bool { return !page.reading && !s.overview.docker.loading })
				composeClickButton(t, tester, locales.Close())
			}
		})
	}
}

type fakeComposeFiles struct {
	directory composeDirectory
	files     map[string]string
	made      bool
	removed   string
	marker    string
}

func (remote *fakeComposeFiles) inspect(context.Context, string) (composeDirectory, error) {
	return remote.directory, nil
}

func (remote *fakeComposeFiles) mkdir(context.Context, string, bool) (bool, error) {
	remote.made, remote.directory.exists = true, true
	return false, nil
}

func (remote *fakeComposeFiles) create(_ context.Context, file string, data []byte, privileged bool) (bool, error) {
	if _, exists := remote.files[file]; exists {
		return false, fs.ErrExist
	}
	remote.files[file] = string(data)
	return privileged, nil
}

func (remote *fakeComposeFiles) remove(_ context.Context, dir, marker string) error {
	remote.removed, remote.marker = dir, marker
	return nil
}

func TestDockerComposeCreateFiles(t *testing.T) {
	ctx := context.Background()
	const dir = "/opt/compose/web"
	remote := &fakeComposeFiles{files: make(map[string]string)}
	if _, _, err := createComposeFiles(ctx, remote, dir, "uid", composeDirectory{}, []byte(composeStarter), []byte("PORT=8080\n")); err != nil {
		t.Fatal(err)
	}
	if !remote.made || remote.files[dir+"/compose.yaml"] != composeStarter || remote.files[dir+"/.env"] != "PORT=8080\n" || len(remote.files) != 3 {
		t.Fatalf("files: %+v", remote)
	}
	if _, _, err := createComposeFiles(ctx, remote, dir, "", composeDirectory{}, []byte("override"), nil); !errors.Is(err, errComposeDirectoryChanged) {
		t.Fatalf("new directory appeared: %v", err)
	}
	if _, _, err := createComposeFiles(ctx, remote, dir, "", remote.directory, []byte("override"), nil); !errors.Is(err, fs.ErrExist) || remote.files[dir+"/compose.yaml"] != composeStarter {
		t.Fatalf("existing file: %v, %+v", err, remote)
	}
	if _, reason := composeNewProject("Bad", ""); reason != locales.DockerCmpInvalidName() {
		t.Fatal("accepted uppercase name")
	}
	if project, reason := composeNewProject("web", ""); reason != "" || project.Dir != dir {
		t.Fatalf("default directory: %+v, %q", project, reason)
	}
}

func TestDockerComposeNewAndDelete(t *testing.T) {
	a, platform, tester, reader, calls := composePageTestApp(t)
	s, page := a.sessions[0], &a.sessions[0].docker.compose
	remote := &fakeComposeFiles{directory: composeDirectory{exists: true, nonEmpty: true}, files: make(map[string]string)}
	page.files = remote
	composeClickButton(t, tester, locales.DockerCmpNew())
	page.dialog.name, page.dialog.env = "preview", "PORT=8080\n"
	page.dialog.deploy = true
	tester.Frame()
	composeClickButton(t, tester, locales.DockerCmpCreate())
	platform.until(t, "directory confirmation", func() bool { return page.dialog.directory != nil })
	wantTexts(t, tester, "nonempty directory", locales.DockerCmpDirConfirm("/opt/compose/preview"))
	if len(remote.files) != 0 {
		t.Fatal("wrote to nonempty directory without confirmation")
	}
	composeClickButton(t, tester, locales.DockerCmpCreateAnyway())
	validation := waitDockerAction(t, calls, "'config' '-q'")
	validation.reply <- "error: invalid yaml"
	platform.until(t, "validation failure", func() bool { return page.dialog.work.view.done })
	select {
	case call := <-calls:
		t.Fatalf("ran %s after invalid config", call.script)
	default:
	}
	if remote.files["/opt/compose/preview/compose.yaml"] != composeStarter || remote.files["/opt/compose/preview/.env"] != "PORT=8080\n" {
		t.Fatalf("files: %+v", remote.files)
	}
	reader.next(t).reply <- dockerReply{d: s.overview.docker.d}
	platform.until(t, "created project recorded", func() bool { return !page.reading && !s.overview.docker.loading })
	if composeOwnedDirectory(s, page.dialog.project) != "" {
		t.Fatal("previously existing directory may be removed")
	}
	composeClickButton(t, tester, locales.Close())
	var staging monitor.ComposeProject
	for _, project := range page.projects(&s.overview.docker.d) {
		if project.Project == "staging" {
			staging = project
		}
	}
	a.openComposeAction(s, staging, monitor.ComposeDown, nil, true)
	tester.Frame()
	clickNamed(t, tester, locales.DockerCmpRemoveDir())
	if button, ok := tester.AccessByRole(ui.RoleButton, locales.DockerCmpDelete()); !ok || !button.Disabled {
		t.Fatalf("delete directory must require typing its name: %+v", button)
	}
	clickNamed(t, tester, locales.DockerCmpDeleteType("staging"))
	tester.Type("staging")
	tester.Frame()
	composeClickButton(t, tester, locales.DockerCmpDelete())
	call := waitDockerAction(t, calls, "'-p' 'staging' '-f' 'compose.yaml' 'down'")
	if remote.removed != "" {
		t.Fatal("removed the directory before down succeeded")
	}
	call.reply <- "done"
	platform.until(t, "directory removed", func() bool { return page.dialog.work.view.done })
	reader.next(t).reply <- dockerReply{d: s.overview.docker.d}
	platform.until(t, "removed from projects", func() bool { return !page.reading && !s.overview.docker.loading })
	if remote.removed != staging.Dir || remote.marker != "test-marker" || composeOwnedDirectory(s, staging) != "" {
		t.Fatalf("delete: %+v, managed: %+v", remote, page.managed)
	}
}

func composeTestEditor(t *testing.T, a *App, platform *fakePlatform, s *Session) (*ui.Tester, *editTab, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(file, []byte(composeStarter), 0o600); err != nil {
		t.Fatal(err)
	}
	s.sftp = localSFTP(t)
	a.openRemoteFile(s, filepath.ToSlash(file))
	tab := a.fe.current()
	platform.until(t, "editor load", func() bool { return tab.state != tabLoading })
	if tab.state != tabReady {
		t.Fatalf("editor state %d: %v", tab.state, tab.err)
	}
	tester := platform.show(t, WindowFileEditor)
	tester.SetScale(1)
	tester.Frame()
	return tester, tab, file
}

func TestDockerComposeEditorConflict(t *testing.T) {
	a, platform, tester, _, _ := composePageTestApp(t)
	s := a.sessions[0]
	_, tab, file := composeTestEditor(t, a, platform, s)
	s.docker.compose.edit = &composeEditOffer{tab: tab, project: "shop"}
	if err := os.WriteFile(file, []byte("# other change\n"+composeStarter), 0o600); err != nil {
		t.Fatal(err)
	}
	tab.code.Buffer.Set(strings.ReplaceAll(composeStarter, "alpine", "stable"))
	a.saveTab(tab, false)
	platform.until(t, "editor conflict", func() bool { return !tab.saving })
	if !tab.conflict {
		t.Fatalf("missing save conflict: %v", tab.saveErr)
	}
	a.saveTab(tab, true)
	platform.until(t, "confirmed save", func() bool { return !tab.saving })
	if tab.saveErr != nil || tab.savedAt.IsZero() {
		t.Fatalf("save failed: %v", tab.saveErr)
	}
	tester.Frame()
	if button, ok := tester.AccessByRole(ui.RoleButton, locales.DockerCmpValidate()); !ok || button.Disabled {
		t.Fatalf("post-save validation not offered: %+v", button)
	}
}

func TestDockerComposeScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, platform, tester, reader, calls := composePageTestApp(t)
			s := a.sessions[0]
			platform.SetLocale(language)
			done := make(chan struct{})
			term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
			if err != nil {
				close(done)
				t.Fatal(err)
			}
			t.Cleanup(func() { close(done); term.Close() })
			s.setTerm(term)
			term.Feed([]byte("\x1b[32mops@demo\x1b[0m:~$ "))
			for _, mode := range []string{"light", "dark"} {
				tester.SetDark(mode == "dark")
				shot := func(screen *ui.Tester, scene string) {
					screen.Frame()
					screen.Frame()
					saveShot(t, screen, "compose-"+scene+"-"+language+"-"+mode+".png")
				}
				a.openPanelTab(s, tabDocker)
				shot(tester, "full")
				a.active = 0
				a.openDockerPage(s, dockerSecCompose)
				shot(tester, "short")
				a.setPanelOnRight(true)
				shot(tester, "right")
				a.setPanelOnRight(false)
				a.openPanelTab(s, tabDocker)
				tester.Frame()
				composeClickButton(t, tester, locales.DockerCmpNew())
				s.docker.compose.dialog.name = "edge-proxy"
				shot(tester, "new")
				composeClickButton(t, tester, locales.Cancel())
				if err := tester.Click(locales.DockerCmpActions()); err != nil {
					t.Fatal(err)
				}
				chooseDockerAction(t, tester, locales.DockerCmpDown())
				clickNamed(t, tester, locales.DockerCmpVolumes())
				platform.until(t, "volume names", func() bool { return len(s.docker.compose.dialog.volumeNames) == 2 })
				shot(tester, "down-volumes")
				composeClickButton(t, tester, locales.Cancel())
				composeClickButton(t, tester, locales.DockerCmpDeploy())
				clickNamed(t, tester, locales.DockerCmpBuild())
				composeClickButton(t, tester, locales.DockerCmpDeploy())
				call := waitDockerAction(t, calls, "'up' '-d' '--build'")
				call.progress("[+] Building 1.2s\n => [web] resolving image nginx:alpine\n => [web] exporting layers\n => [web] starting container shop-web-1\n")
				platform.until(t, "deploy output", func() bool { return s.docker.compose.dialog.work.view.output != "" })
				shot(tester, "deploy")
				composeClickButton(t, tester, locales.Cancel())
				platform.until(t, "cancel deploy", func() bool { return s.docker.compose.dialog.work.view.done })
				composeClickButton(t, tester, locales.Close())
				reader.next(t).reply <- dockerReply{d: s.overview.docker.d}
				platform.until(t, "refresh after cancellation", func() bool {
					return !s.docker.compose.reading && !s.overview.docker.loading && !s.docker.compose.serviceReading
				})
				ed, _, _ := composeTestEditor(t, a, platform, s)
				ed.SetDark(mode == "dark")
				shot(ed, "editor")
				a.closeWindow(WindowFileEditor)
			}
		})
	}
}
