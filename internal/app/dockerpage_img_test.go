package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const imgTestID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const imgTestOtherID = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const imgTestLooseID = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func imagesTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *fakeDocker, <-chan dockerActionCall) {
	t.Helper()
	engine := demoDocker()
	engine.Containers[0].ImageID = imgTestID
	a, platform, tester, reader := dockerPageApp(t, engine)
	session := a.sessions[0]
	session.Host.Addr = "192.0.2.10"
	session.ip = "192.0.2.10"
	calls := make(chan dockerActionCall, 8)
	session.overview.docker.run = func(ctx context.Context, _ *ssh.Client, script string, progress func(string)) (string, error) {
		if strings.Contains(script, "'dangling=true'") {
			return fmt.Sprintf(`{"ID":%q,"Repository":"<none>","Tag":"<none>","Size":"4MB"}`, imgTestLooseID), nil
		}
		if strings.Contains(script, "'image' 'ls'") {
			return fmt.Sprintf(`{"ID":%q,"Repository":"library/demo","Tag":"stable","Size":"51.3MB","CreatedAt":"2026-09-04 13:42:00 +0000 UTC"}
{"ID":%q,"Repository":"library/demo","Tag":"previous","Size":"51.3MB"}
{"ID":%q,"Repository":"registry.example/team/worker","Tag":"release","Size":"110MB"}
{"ID":%q,"Repository":"<none>","Tag":"<none>","Size":"4MB"}`, imgTestID, imgTestID, imgTestOtherID, imgTestLooseID), nil
		}
		if strings.Contains(script, "'--format' '{{.Os}}/{{.Architecture}}'") {
			return "linux/amd64\nlinux/arm64\nlinux/amd64\n", nil
		}
		if strings.Contains(script, "'image' 'inspect' '--' ") {
			return fmt.Sprintf(`[{"Id":%q,"RepoTags":["library/demo:stable","library/demo:previous"],"RepoDigests":["library/demo@%s"],"Os":"linux","Architecture":"amd64","Created":"2026-09-04T13:42:00Z","Author":"Example Team","Size":51300000,"RootFS":{"Layers":["%s"]},"Config":{"Env":["TZ=UTC"],"ExposedPorts":{"80/tcp":{}},"Entrypoint":["/entrypoint"],"Cmd":["serve"],"WorkingDir":"/srv/demo","User":"1000","Labels":{"org.example.version":"stable"}}}]`, imgTestID, imgTestID, imgTestOtherID), nil
		}
		if strings.Contains(script, "'history'") {
			return `{"ID":"<missing>","CreatedBy":"/bin/sh -c #(nop) CMD [\"demo\"]","Size":"0B","Comment":"buildkit.dockerfile.v0"}
{"ID":"<missing>","CreatedBy":"COPY ./demo /usr/local/bin/demo # buildkit","Size":"14.1MB","Comment":""}`, nil
		}
		if strings.Contains(script, "'ps' '--all' ") {
			var ids []string
			for _, container := range engine.Containers {
				ids = append(ids, container.ID)
			}
			return strings.Join(ids, "\n"), nil
		}
		call := dockerActionCall{script: script, reply: make(chan string, 1), progress: progress}
		calls <- call
		select {
		case output := <-call.reply:
			if strings.HasPrefix(output, "unauthorized") {
				return output, &monitor.DockerExit{Status: 1}
			}
			return output, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a.openDockerPage(session, dockerSecImages)
	tester.Frame()
	platform.until(t, "images", func() bool { return session.docker.images.loaded })
	tester.Frame()
	return a, platform, tester, reader, calls
}

func clickImgButton(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	button, ok := tester.AccessByRole(ui.RoleButton, label)
	if !ok || button.Disabled {
		t.Fatalf("button %q unavailable (%+v); texts %q", label, button, tester.Texts())
	}
	tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	tester.Frame()
}

func TestDockerImagesList(t *testing.T) {
	a, _, tester, _, _ := imagesTestApp(t)
	session := a.sessions[0]
	page := &session.docker.images
	wantTexts(t, tester, "images", "library/demo:stable", "registry.example/team/worker:release", "linux/arm64")
	if got := page.usage(&session.overview.docker.d)[monitor.ImageIDKey(imgTestID)]; len(got) != 1 || got[0] != session.overview.docker.d.Containers[0].Name {
		t.Fatalf("image users: %v", got)
	}
	page.filter = "WORKER"
	tester.Frame()
	if tester.HasText("library/demo:stable") || !tester.HasText("registry.example/team/worker:release") {
		t.Fatal("filter did not narrow the image list")
	}
	page.filter = ""
	page.sort = ui.SortOrder{Column: "size", Descending: true}
	tester.Frame()
	if rows := page.shown(page.usage(&session.overview.docker.d)); len(rows) != 4 || rows[0].ID != imgTestOtherID || rows[3].ID != imgTestLooseID {
		t.Fatalf("unexpected size order: %+v", rows)
	}
	page.unusedOnly = true
	tester.Frame()
	if tester.HasText("library/demo:stable") {
		t.Fatal("unused filter kept an image held by a container")
	}
	page.danglingOnly = true
	tester.Frame()
	wantTexts(t, tester, "dangling", locales.DockerImgDangling())
}

func TestDockerImagesActions(t *testing.T) {
	a, platform, tester, reader, calls := imagesTestApp(t)
	session := a.sessions[0]
	page := &session.docker.images
	used := page.rows[0]
	tester.RightClickAt(rowCenter(t, tester, used.Ref))
	if err := tester.ChooseMenuItem(locales.DockerImgRemove()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("remove menu should be disabled: %v", err)
	}
	for _, name := range []string{locales.DockerDetails(), locales.DockerImgHistory(), locales.DockerForceRemove(), locales.DockerImgPush()} {
		if !slices.Contains(tester.Menu(), name) {
			t.Errorf("missing menu item %q in %v", name, tester.Menu())
		}
	}
	if reason := dockerImgRemoveReason(session, []monitor.ImageRow{used}, false); reason != locales.DockerImgUsedReason() {
		t.Fatalf("used image remove: %q", reason)
	}
	chooseDockerAction(t, tester, locales.DockerForceRemove())
	tester.Frame()
	if button, ok := tester.AccessByRole(ui.RoleButton, locales.DockerImgRemove()); !ok || !button.Disabled {
		t.Fatal("force removal must require the checkbox")
	}
	clickNamed(t, tester, locales.Cancel())
	select {
	case call := <-calls:
		t.Fatalf("cancel ran %s", call.script)
	default:
	}
	a.openDockerImgDialog(session, imgRemove, []monitor.ImageRow{used})
	page.dialog.force = true
	tester.Frame()
	clickImgButton(t, tester, locales.DockerImgRemove())
	call := waitDockerAction(t, calls, "docker 'image' 'rm' '--no-prune' '--force' '--' 'library/demo:stable'\n")
	call.reply <- "Untagged: library/demo:stable"
	platform.until(t, "remove and overview refresh", func() bool { return page.dialog.work.done && len(reader.reads) > 0 && !page.loading })
	reader.next(t).reply <- dockerReply{d: session.overview.docker.d}
	clickNamed(t, tester, locales.Close())
	a.openDockerImgDialog(session, imgPull, nil)
	page.dialog.value = "library/demo:stable"
	page.dialog.platform = "linux/arm64"
	tester.Frame()
	clickImgButton(t, tester, locales.DockerImgPull())
	call = waitDockerAction(t, calls, "docker 'image' 'pull' '--platform' 'linux/arm64' '--' 'library/demo:stable'\n")
	call.progress("Downloading 50%\n")
	platform.until(t, "pull progress", func() bool { return strings.Contains(page.dialog.work.output, "Downloading 50%") })
	tester.Frame()
	wantTexts(t, tester, "pull output", "Downloading 50%")
	clickNamed(t, tester, locales.DockerImgStop())
	platform.until(t, "pull cancelled", func() bool { return page.dialog.work.done && len(reader.reads) > 0 && !page.loading })
	reader.next(t).reply <- dockerReply{d: session.overview.docker.d}
	clickNamed(t, tester, locales.Close())
	a.openDockerImgDialog(session, imgPush, []monitor.ImageRow{used})
	tester.Frame()
	clickImgButton(t, tester, locales.DockerImgPush())
	call = waitDockerAction(t, calls, "docker 'image' 'push' '--' 'library/demo:stable'\n")
	call.reply <- "unauthorized: authentication required"
	platform.until(t, "push error", func() bool { return page.dialog.work.done })
	tester.Frame()
	wantTexts(t, tester, "push denied", locales.DockerImgPushAuth(), "unauthorized: authentication required")
}

func TestDockerImagesConfirmCommands(t *testing.T) {
	tests := []struct {
		name      string
		op        dockerImgOp
		configure func(*dockerImgDialog)
		line      string
	}{
		{"tag", imgTag, func(dialog *dockerImgDialog) { dialog.value = "team/demo:new" }, "docker 'image' 'tag' '--' '" + imgTestID + "' 'team/demo:new'\n"},
		{"build", imgBuild, func(dialog *dockerImgDialog) {
			dialog.value = "/srv/project"
			dialog.build.Tags = []string{"team/demo:new", "team/demo:preview"}
			dialog.build.Args = []string{"TITLE=demo version"}
		}, "docker 'build' '-f' '/srv/project/Dockerfile' '-t' 'team/demo:new' '-t' 'team/demo:preview' '--build-arg' 'TITLE=demo version' '--' '/srv/project'\n"},
		{"load", imgLoad, func(dialog *dockerImgDialog) { dialog.value = "/var/backups/image.tar.gz" }, "docker 'image' 'load' '-i' '/var/backups/image.tar.gz'\n"},
		{"import", imgLoad, func(dialog *dockerImgDialog) {
			dialog.value, dialog.platform, dialog.importing = "/var/backups/rootfs.tar", "team/demo:new", true
		}, "docker 'image' 'import' '--' '/var/backups/rootfs.tar' 'team/demo:new'\n"},
		{"save", imgSave, func(dialog *dockerImgDialog) { dialog.value = "/var/backups/image.tar" }, "ln -T -- \"$temp/archive.tar\" '/var/backups/image.tar'\n"},
		{"prune", imgPrune, func(dialog *dockerImgDialog) {
			dialog.all = true
			dialog.snapshotPrune(dialog.target.session)
		}, "docker 'image' 'prune' '--force' '--all'\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, platform, tester, reader, calls := imagesTestApp(t)
			session := a.sessions[0]
			page := &session.docker.images
			var rows []monitor.ImageRow
			if test.op == imgTag || test.op == imgSave {
				rows = []monitor.ImageRow{page.rows[0]}
			}
			a.openDockerImgDialog(session, test.op, rows)
			test.configure(page.dialog)
			tester.Frame()
			clickImgButton(t, tester, locales.Cancel())
			if page.dialog != nil || len(calls) != 0 {
				t.Fatal("cancelling the dialog must not run anything")
			}
			a.openDockerImgDialog(session, test.op, rows)
			test.configure(page.dialog)
			tester.Frame()
			clickImgButton(t, tester, page.dialog.title())
			call := waitDockerAction(t, calls, test.line)
			call.reply <- "Total reclaimed space: 4MB"
			platform.until(t, "confirmed command and refresh", func() bool {
				return page.dialog.work.done && (test.op == imgSave || len(reader.reads) > 0 && !page.loading)
			})
			if test.op != imgSave {
				reader.next(t).reply <- dockerReply{d: session.overview.docker.d}
			}
			if test.op == imgPrune {
				tester.Frame()
				wantTexts(t, tester, "prune result", "Total reclaimed space: 4MB")
			}
		})
	}
}

func TestDockerImagesBatchRemove(t *testing.T) {
	a, platform, tester, reader, calls := imagesTestApp(t)
	session := a.sessions[0]
	if err := tester.Click("registry.example/team/worker:release"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ClickWith(ui.Shift, locales.DockerImgDangling()); err != nil {
		t.Fatal(err)
	}
	if count := session.docker.images.selected.Len(); count != 2 {
		t.Fatalf("selected %d rows", count)
	}
	tester.RightClickAt(rowCenter(t, tester, "registry.example/team/worker:release"))
	chooseDockerAction(t, tester, locales.DockerImgRemoveMany())
	clickImgButton(t, tester, locales.DockerImgRemoveMany())
	call := waitDockerAction(t, calls, "docker 'image' 'rm' '--no-prune' '--' 'registry.example/team/worker:release' '"+imgTestLooseID+"'\n")
	call.reply <- "Deleted: " + imgTestLooseID
	platform.until(t, "batch refresh", func() bool { return len(reader.reads) > 0 })
	reader.next(t).reply <- dockerReply{d: session.overview.docker.d}
}

func TestDockerImagesPruneRejectsChangedScope(t *testing.T) {
	a, platform, tester, reader, calls := imagesTestApp(t)
	session := a.sessions[0]
	page := &session.docker.images
	run := session.overview.docker.run
	session.overview.docker.run = func(ctx context.Context, client *ssh.Client, script string, progress func(string)) (string, error) {
		if strings.Contains(script, "'ps' '--all' ") {
			return "container list changed", nil
		}
		return run(ctx, client, script, progress)
	}
	a.openDockerImgDialog(session, imgPrune, nil)
	tester.Frame()
	clickImgButton(t, tester, locales.DockerImgPrune())
	platform.until(t, "aborted prune", func() bool { return page.dialog.work.done && len(reader.reads) > 0 })
	reader.next(t).reply <- dockerReply{d: session.overview.docker.d}
	if len(calls) != 0 {
		t.Fatal("prune ran without a matching container snapshot")
	}
	wantTexts(t, tester, "changed scope", locales.DockerImgPruneChanged())
}

func TestDockerImagesUploadCleanupOnFailure(t *testing.T) {
	local := filepath.Join(t.TempDir(), "example's image.tar")
	if err := os.WriteFile(local, []byte("mock archive"), 0600); err != nil {
		t.Fatal(err)
	}
	client := sshtest.Start(t).Dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var scripts []string
	run := func(_ context.Context, _ *ssh.Client, script string, _ func(string)) (string, error) {
		scripts = append(scripts, script)
		return "", nil
	}
	_, cleanup, err := dockerImgUpload(ctx, client, monitor.DockerCmd{Tool: "docker"}, run, local, false, "", func(string) {}, "Uploaded:")
	if err == nil || cleanup == nil || len(scripts) != 1 || !strings.HasPrefix(scripts[0], "umask 077\nmkdir -- '/tmp/finallyshell-img-") {
		t.Fatalf("expected a failed upload and private directory cleanup: %v, %q", err, scripts)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 2 || !strings.Contains(scripts[1], `example'\''s image.tar'`) || !strings.Contains(scripts[1], "\nrmdir -- '/tmp/finallyshell-img-") {
		t.Fatalf("unquoted cleanup or missing directory removal: %q", scripts)
	}
}

func TestDockerImagesShots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("no screenshot destination")
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			t.Run(lang+"-"+mode, func(t *testing.T) {
				a, platform, tester, _, _ := imagesTestApp(t)
				platform.SetLocale(lang)
				tester.SetDark(dark)
				tester.SetSize(1280, 820)
				session := a.sessions[0]
				tester.Frame()
				saveShot(t, tester, "docker-images-short-"+mode+"-"+lang+".png")
				a.bottomSide, a.rightWidth = config.PanelRight, 460
				tester.Frame()
				saveShot(t, tester, "docker-images-narrow-"+mode+"-"+lang+".png")
				a.bottomSide = ""
				a.openPanelTab(session, tabDocker)
				tester.Frame()
				saveShot(t, tester, "docker-images-full-"+mode+"-"+lang+".png")
				for _, shot := range []struct {
					name string
					op   dockerImgOp
				}{{"details", imgDetails}, {"history", imgHistory}, {"pull", imgPull}, {"build", imgBuild}, {"prune", imgPrune}} {
					page := &session.docker.images
					a.openDockerImgDialog(session, shot.op, []monitor.ImageRow{page.rows[0]})
					platform.until(t, "dialog response", func() bool { return !page.dialog.loading })
					tester.Frame()
					saveShot(t, tester, "docker-images-"+shot.name+"-"+mode+"-"+lang+".png")
					label := locales.Cancel()
					if shot.op == imgDetails || shot.op == imgHistory {
						label = locales.Close()
					}
					clickNamed(t, tester, label)
				}
			})
		}
	}
}
