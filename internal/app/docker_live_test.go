package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func requireDockerLiveApp(t *testing.T, user string) {
	t.Helper()
	spec := os.Getenv("FINALLYSHELL_TEST_SSH")
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" || !strings.HasPrefix(spec, user+":") || spec[strings.LastIndexByte(spec, '@')+1:] != "127.0.0.1:2222" {
		t.Skip("requires mutation opt-in and the local testbox account " + user)
	}
}

func TestDockerLiveTester(t *testing.T) {
	requireDockerLiveApp(t, "tester")
	app, platform, session := connectTestServer(t)
	if host, _, err := monitor.RunScript(t.Context(), session.client, "hostname"); err != nil || strings.TrimSpace(host) != "fs-testbox" {
		t.Fatal("not the local testbox")
	}
	secret := app.passwords[session.Host.ID]
	delete(app.passwords, session.Host.ID)
	tester := ui.NewTester(app.View, 1440, 950)
	platform.tt = tester
	app.openPanelTab(session, tabDocker)
	tester.Frame()
	state := &session.overview.docker
	platform.until(t, "Docker root requirement", func() bool {
		return state.d.State == monitor.DockerNeedsRoot && tester.HasText(locales.DockerPrivNeedsRoot("Docker"))
	})
	clickButton(t, tester, locales.DockerPrivUseRoot())
	window := privilegeWindow(session)
	platform.until(t, "sudo prompt", func() bool { return app.windowOpen(window) })
	dialog := platform.show(t, window)
	typeInto(t, dialog, locales.PrivPassword(), secret)
	clickButton(t, dialog, locales.PrivRun())
	platform.until(t, "privileged container list", func() bool { return state.d.Root && tester.HasText("web1") })

	name := fmt.Sprintf("fslive%x", time.Now().UnixNano())
	cmd := monitor.CmdOf(&state.d)
	script, err := cmd.CtrRunScript(monitor.CtrConfig{Name: name, Image: "alpine:3.20", Command: []string{"sh", "-c", "echo fslive-sudo; trap 'exit 0' TERM; while :; do sleep 1; done"}})
	if err != nil {
		t.Fatal(err)
	}
	privilege := app.sessionPrivilege(session)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cleanup, _ := cmd.RemoveScript(true, true, name)
		if out, err := monitor.RunDockerRoot(ctx, privilege.runner.RunCached, cleanup, nil); err != nil && !strings.Contains(out, "No such container") {
			t.Errorf("sudo cleanup failed: %v", err)
		}
	})
	run := app.dockerExec(session, monitor.RunDockerProgress, nil, false).command
	if out, err := run(t.Context(), session.client, script, nil); err != nil {
		t.Fatalf("sudo container fixture: %v: %s", err, out)
	}
	app.readDockerOf(session)
	platform.until(t, "new container row", func() bool { return !state.loading && tester.HasText(name) })
	rowMenu(t, tester, name, locales.DockerStop())
	clickButton(t, tester, locales.DockerStop())
	platform.until(t, "container stopped through sudo", func() bool {
		return !state.loading && dockerLiveContainerState(state.d.Containers, name) == "exited"
	})
	if tester.HasText(locales.Close()) {
		clickButton(t, tester, locales.Close())
	}
	app.openDockerPage(session, dockerSecCompose)
	page := &session.docker.compose
	platform.until(t, "Compose CLI over sudo", func() bool { return page.cli.Flavor == monitor.ComposeV2 && tester.HasText("demo") })
	rowMenu(t, tester, "demo", composeActionLabel(monitor.ComposeLogs))
	platform.until(t, "Compose logs over sudo", func() bool { return page.dialog != nil && page.dialog.work != nil && page.dialog.work.view.done })
	if work := page.dialog.work.view; work.err != nil || !strings.Contains(work.output, "app") {
		t.Fatalf("sudo Compose logs did not stream: %v: %s", work.err, work.output)
	}
	composeClickButton(t, tester, locales.Close())
	app.openDockerPage(session, dockerSecSystem)
	resources := app.dockerResources(session)
	resources.system.mode = dockerSystemEvents
	tester.Frame()
	events := &resources.system.events
	platform.until(t, "event from privileged container operation", func() bool {
		for _, event := range events.rows {
			if event.Name == name && event.Type == "container" && event.Action == "stop" {
				return true
			}
		}
		return false
	})
	resources.system.mode = dockerSystemRegistries
	tester.Frame()
	platform.until(t, "privileged events stopped", func() bool { return events.poll.run == nil })
	if app.windowOpen(window) {
		t.Fatal("cached sudo operations prompted again")
	}
}

func dockerLiveContainerState(containers []monitor.Container, name string) string {
	for _, container := range containers {
		if container.Name == name {
			return container.Status
		}
	}
	return ""
}

func TestDockerLiveAppComposeFilesAndImageGuard(t *testing.T) {
	requireDockerLiveApp(t, "root")
	app, platform, session := connectTestServer(t)
	if host, _, err := monitor.RunScript(t.Context(), session.client, "hostname"); err != nil || strings.TrimSpace(host) != "fs-testbox" {
		t.Fatal("not the local testbox")
	}
	name := fmt.Sprintf("fslive%x", time.Now().UnixNano())
	dir := "/tmp/" + name
	project, reason := composeNewProject(name, dir)
	if reason != "" {
		t.Fatal(reason)
	}
	yamlPath := dir + "/" + project.Files[0]
	store := app.composeFilesOf(session)
	inspected, err := store.inspect(t.Context(), dir)
	if err != nil || inspected.exists {
		t.Fatalf("new project directory: %+v, %v", inspected, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := monitor.RunDocker(ctx, session.client, "rm -f -- "+composeShellArg(yamlPath)+" "+composeShellArg(dir+"/.env")+" "+composeShellArg(dir+"/"+composeMarker(name))+"; rmdir -- "+composeShellArg(dir))
		if err != nil {
			t.Errorf("project cleanup: %v", err)
		}
	})
	yaml := []byte("services:\n  worker:\n    image: alpine:3.20\n")
	env := []byte("FSLIVE=yes\n")
	if _, written, err := createComposeFiles(t.Context(), store, dir, name, inspected, yaml, env); err != nil || !written {
		t.Fatalf("create compose files: written=%v: %v", written, err)
	}
	if out, err := monitor.RunDocker(t.Context(), session.client, "cat -- "+composeShellArg(yamlPath)+" "+composeShellArg(dir+"/.env")); err != nil || out != string(yaml)+string(env) {
		t.Fatalf("read created project files: %s, %v", out, err)
	}
	if err := monitor.CheckComposeProject(project.Compose); err != nil {
		t.Fatal(err)
	}
	tester := ui.NewTester(app.View, 1440, 950)
	platform.tt = tester
	app.openDockerPage(session, dockerSecImages)
	tester.Frame()
	images := &session.docker.images
	platform.until(t, "images and container usage", func() bool { return images.loaded && dockerImgUsageKnown(session) && !session.overview.docker.loading })
	for _, row := range images.rows {
		if row.Ref == "alpine:3.20" {
			if reason := dockerImgRemoveReason(session, []monitor.ImageRow{row}, false); reason != locales.DockerImgUsedReason() {
				t.Fatalf("image used by a container was not guarded: %q", reason)
			}
			return
		}
	}
	t.Fatal("baseline image not listed")
}
