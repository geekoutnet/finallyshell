package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerActionCall struct {
	script   string
	reply    chan string
	progress func(string)
}

func actionDockerApp(t *testing.T, servers ...*sshtest.Server) (*App, *fakePlatform, *ui.Tester, *fakeDocker, <-chan dockerActionCall) {
	t.Helper()
	a, platform := testApp(t)
	session := demoSession(a, "demo", "192.0.2.3")
	if len(servers) == 0 {
		servers = append(servers, sshtest.Start(t))
	}
	session.client = servers[0].Dial(t)
	done := make(chan struct{})
	term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
	if err != nil {
		close(done)
		t.Fatal(err)
	}
	t.Cleanup(func() { close(done); term.Close() })
	session.setTerm(term)
	data := demoDocker()
	for index := range data.Containers {
		ct := &data.Containers[index]
		if ct.Project != "" {
			ct.ComposeFiles = "compose.yaml,compose.prod.yaml"
		}
		ct.Inspect = json.RawMessage(`{"Config":{"Image":"nginx:latest"},"HostConfig":{},"State":{"Running":true}}`)
	}
	state := &session.overview.docker
	state.d, state.read, state.at = data, true, time.Now()
	state.client, state.dataClient = session.client, session.client
	calls := make(chan dockerActionCall, 8)
	state.run = func(ctx context.Context, _ *ssh.Client, script string, progress func(string)) (string, error) {
		call := dockerActionCall{script: script, reply: make(chan string, 1), progress: progress}
		calls <- call
		select {
		case output := <-call.reply:
			if output == "engine refused" {
				return output, &monitor.DockerExit{Status: 1}
			}
			return output, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	reader := newFakeDocker(a)
	tester := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		c.SetMenuPresentation(ui.PopoverMenus)
		ui.Column(c).Fill().Padding(24).Gap(16).Children(func() {
			a.dockerContainers(c, session, 1050)
		})
		a.containerDialogView(c)
	}, 1120, 720)
	platform.tt = tester
	tester.Frame()
	return a, platform, tester, reader, calls
}

func TestDockerTerminalPane(t *testing.T) {
	server := sshtest.Start(t)
	a, platform, tester, _, _ := actionDockerApp(t, server)
	session := a.sessions[0]
	state := &session.overview.docker
	state.d.Sudo = true
	t.Cleanup(func() {
		for _, pane := range session.panes.panes() {
			pane.end()
		}
	})
	rowMenu(t, tester, "gitea", locales.DockerTerminal())
	id := state.d.Containers[3].ID
	command := "sudo -n docker 'exec' '-it' '" + id + "' 'sh' '-c' 'command -v bash >/dev/null && exec bash || exec sh'\r"
	platform.until(t, "the container shell", func() bool {
		return session.panes.count() == 2 && strings.Contains(server.ShellInput(0), command)
	})
	if server.Logins() != 1 || a.current() != session {
		t.Fatal("container shell did not reuse the selected SSH session")
	}
}

func waitDockerAction(t *testing.T, calls <-chan dockerActionCall, want string) dockerActionCall {
	t.Helper()
	select {
	case call := <-calls:
		if !strings.Contains(call.script, want) {
			t.Fatalf("missing %q in script %s", want, call.script)
		}
		return call
	case <-time.After(time.Second):
		t.Fatal("no Docker action call")
		return dockerActionCall{}
	}
}

func chooseDockerAction(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	if err := tester.ChooseMenuItem(name); err != nil {
		t.Fatalf("%v; menu %q", err, tester.Menu())
	}
	tester.Frame()
}

func dockerActionShots(t *testing.T, tester *ui.Tester, scene string) {
	t.Helper()
	saveShot(t, tester, "docker-"+scene+"-light.png")
	tester.SetDark(true)
	tester.Frame()
	saveShot(t, tester, "docker-"+scene+"-dark.png")
	tester.SetDark(false)
	tester.Frame()
}

func TestDockerContainerActions(t *testing.T) {
	a, platform, tester, reader, calls := actionDockerApp(t)
	state := &a.sessions[0].overview.docker
	id := state.d.Containers[3].ID
	tester.RightClickAt(rowCenter(t, tester, "gitea"))
	for _, label := range []string{locales.DockerStart(), locales.DockerStop(), locales.DockerRestart(), locales.DockerKill(), locales.DockerPause(), locales.DockerLogs(), locales.DockerTerminal(), locales.DockerRemove(), locales.DockerForceRemove(), locales.DockerDetails()} {
		if !slices.Contains(tester.Menu(), label) {
			t.Errorf("missing %q from %q", label, tester.Menu())
		}
	}
	for _, disabled := range []string{locales.DockerStart(), locales.DockerRemove()} {
		if err := tester.ChooseMenuItem(disabled); err == nil || !strings.Contains(err.Error(), "is disabled") {
			t.Fatalf("running container item %q: %v", disabled, err)
		}
	}
	if item, ok := tester.AccessByRole(ui.RoleMenuItem, locales.DockerTerminal()); !ok || item.Disabled {
		t.Fatalf("terminal is not enabled: %+v", item)
	}
	dockerActionShots(t, tester, "menu")
	chooseDockerAction(t, tester, locales.DockerRestart())
	clickButton(t, tester, locales.DockerRestart())
	call := waitDockerAction(t, calls, "docker 'restart' '"+id+"'")
	wantTexts(t, tester, "restart", locales.DockerActionRestarting())
	call.reply <- "ok"
	platform.until(t, "a reread", func() bool { return state.loading })
	select {
	case request := <-reader.reads:
		request.reply <- dockerReply{d: state.d}
	case <-time.After(time.Second):
		t.Fatal("no Docker reread")
	}
	platform.until(t, "Docker read to finish", func() bool { return !state.loading })
	button, ok := tester.AccessByRole(ui.RoleMenuButton, locales.DockerMore("report-worker"))
	if !ok {
		t.Fatal("no row menu button")
	}
	tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	if err := tester.ChooseMenuItem(locales.DockerTerminal()); err == nil || !strings.Contains(err.Error(), "is disabled") {
		t.Fatalf("terminal state for stopped worker: %v; menu %q", err, tester.Menu())
	}
	tester.CloseMenu()
}

func TestDockerRemoveConfirmation(t *testing.T) {
	a, platform, tester, reader, calls := actionDockerApp(t)
	state := &a.sessions[0].overview.docker
	worker := state.d.Containers[4].ID
	rowMenu(t, tester, "report-worker", locales.DockerRemove())
	wantTexts(t, tester, "remove confirmation", "report-worker", locales.DockerRemoveWarning(), locales.DockerRemoveVolumes())
	dockerActionShots(t, tester, "confirmation")
	clickButton(t, tester, locales.Cancel())
	select {
	case call := <-calls:
		t.Fatalf("cancel ran %q", call.script)
	default:
	}
	for _, volumes := range []bool{false, true} {
		rowMenu(t, tester, "report-worker", locales.DockerRemove())
		command := "docker 'rm'"
		if volumes {
			clickNamed(t, tester, locales.DockerRemoveVolumes())
			command += " '-v'"
		}
		clickButton(t, tester, locales.DockerRemove())
		call := waitDockerAction(t, calls, command+" '"+worker+"'")
		call.reply <- "engine refused"
		platform.until(t, "remove error", func() bool { return tester.HasText("engine refused") })
		request := <-reader.reads
		request.reply <- dockerReply{d: state.d}
		clickButton(t, tester, locales.Close())
		platform.until(t, "Docker read to finish", func() bool { return !state.loading })
	}
	rowMenu(t, tester, "gitea", locales.DockerForceRemove())
	wantTexts(t, tester, "force confirmation", locales.DockerForceRemoveWarning())
	clickButton(t, tester, locales.DockerForceRemove())
	call := waitDockerAction(t, calls, "docker 'rm' '-f' '"+state.d.Containers[3].ID+"'")
	call.reply <- "ok"
	platform.until(t, "force remove reread", func() bool { return state.loading })
	select {
	case request := <-reader.reads:
		request.reply <- dockerReply{d: state.d}
	case <-time.After(time.Second):
		t.Fatal("no Docker reread")
	}
	platform.until(t, "force remove read to finish", func() bool { return !state.loading })
}

func TestDockerRecreateAndProjectMenu(t *testing.T) {
	for _, test := range []struct{ row, command string }{
		{"shop-web-1", "'up' '-d' '--force-recreate' '--no-deps' 'web'"},
		{"gitea", "'/containers/create?name=gitea'"},
		{"shop", "'up' '-d' '--force-recreate'\n"},
	} {
		t.Run(test.row, func(t *testing.T) {
			_, platform, tester, reader, calls := actionDockerApp(t)
			tester.RightClickAt(rowCenter(t, tester, test.row))
			item := locales.DockerRecreate()
			if test.row == "shop" {
				for _, label := range []string{locales.DockerProjectStart(), locales.DockerProjectStop(), locales.DockerProjectRestart(), locales.DockerProjectDown(), locales.DockerLogs()} {
					if !slices.Contains(tester.Menu(), label) {
						t.Errorf("no project menu item %q", label)
					}
				}
				item = locales.DockerProjectRecreate()
			}
			chooseDockerAction(t, tester, item)
			clickButton(t, tester, item)
			call := waitDockerAction(t, calls, test.command)
			if call.progress == nil {
				t.Fatal("recreate did not request progress")
			}
			call.progress("Pulling image…")
			platform.until(t, "live recreate output", func() bool { return tester.HasText("Pulling image…") })
			call.reply <- "replacement started"
			platform.until(t, "recreate output", func() bool { return tester.HasText("replacement started") })
			(<-reader.reads).reply <- dockerReply{}
			clickButton(t, tester, locales.Close())
		})
	}
}

func TestDockerProjectActionsIgnoreRowFilter(t *testing.T) {
	a, platform, tester, reader, calls := actionDockerApp(t)
	state := &a.sessions[0].overview.docker
	state.filter = "shop-web-1"
	tester.Frame()
	rowMenu(t, tester, "shop", locales.DockerProjectDown())
	wantTexts(t, tester, "down confirmation", locales.DockerDownWarning())
	clickButton(t, tester, locales.Cancel())
	rowMenu(t, tester, "shop", locales.DockerProjectRestart())
	clickButton(t, tester, locales.DockerProjectRestart())
	call := waitDockerAction(t, calls, "docker 'restart'")
	for _, ct := range state.d.Containers[:3] {
		if !strings.Contains(call.script, "'"+ct.ID+"'") {
			t.Fatalf("hidden project member %s was not restarted", ct.Name)
		}
	}
	call.reply <- "ok"
	platform.until(t, "group reread", func() bool { return state.loading })
	select {
	case request := <-reader.reads:
		request.reply <- dockerReply{d: state.d}
	case <-time.After(time.Second):
		t.Fatal("no Docker reread")
	}
	platform.until(t, "group read to finish", func() bool { return !state.loading })
	rowMenu(t, tester, "shop", locales.DockerLogs())
	call = waitDockerAction(t, calls, "'logs' '--no-color' '--timestamps' '--tail' '200'")
	call.reply <- "web | example"
	platform.until(t, "project logs", func() bool { return tester.HasText("web | example") })
	clickButton(t, tester, locales.Close())
}

func TestDockerPublishedPortAndCopy(t *testing.T) {
	_, _, tester, _, _ := actionDockerApp(t)
	rowMenu(t, tester, "shop-web-1", locales.Copy(), locales.DockerColIP())
	if got := tester.Clipboard(); !strings.Contains(got, "172.20.0.3") {
		t.Errorf("copied IP: %q", got)
	}
	const address = "http://192.0.2.3:8080"
	rowMenu(t, tester, "shop-web-1", locales.DockerOpenPort(), address)
	if !slices.Equal(tester.OpenedURLs(), []string{address}) {
		t.Errorf("opened %v", tester.OpenedURLs())
	}
	ports := []monitor.PortBinding{
		{Proto: "udp", HostIP: "0.0.0.0", HostPort: 80},
		{Proto: "tcp", HostIP: "127.0.0.1", HostPort: 81},
		{Proto: "tcp", HostIP: "::", HostPort: 82},
		{Proto: "tcp", HostIP: "2001:db8::1", HostPort: 83},
	}
	want := []string{"http://[2001:db8::1]:82", "http://[2001:db8::1]:83"}
	if got := dockerPortURLs("2001:db8::1", "", ports); !slices.Equal(got, want) {
		t.Errorf("ports: %v, want %v", got, want)
	}
}

func TestDockerLogs(t *testing.T) {
	_, platform, tester, _, calls := actionDockerApp(t)
	rowMenu(t, tester, "gitea", locales.DockerLogs())
	call := waitDockerAction(t, calls, "'logs' '--tail' '200' '--timestamps'")
	output := "2026-11-24T12:00:00Z Listening on port 3000\n2026-11-24T12:00:01Z WARNING: disk nearly full\n"
	call.reply <- output
	platform.until(t, "initial logs", func() bool { return tester.HasText(output) })
	dockerActionShots(t, tester, "logs")
	clickNamed(t, tester, locales.DockerLogFilter())
	tester.Type("warning")
	tester.Frame()
	wantTexts(t, tester, "filtered logs", "2026-11-24T12:00:01Z WARNING: disk nearly full\n")
	clickButton(t, tester, locales.CopyAll())
	if tester.Clipboard() != output {
		t.Errorf("copy all = %q", tester.Clipboard())
	}
	clickNamed(t, tester, locales.DockerLogTail())
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("500")
	clickButton(t, tester, locales.Refresh())
	call = waitDockerAction(t, calls, "'logs' '--tail' '500'")
	call.reply <- output + "2026-11-24T12:00:02Z WARNING: refreshed\n"
	platform.until(t, "refreshed logs", func() bool { return tester.HasText("WARNING: refreshed") })
	clickNamed(t, tester, locales.DockerLogFollow())
	tester.Advance(2 * time.Second)
	tester.Frame()
	call = waitDockerAction(t, calls, "'logs' '--tail' '500'")
	call.reply <- output
	clickButton(t, tester, locales.Close())
	tester.Advance(4 * time.Second)
	tester.Frame()
	select {
	case <-calls:
		t.Fatal("closed logs dialog polled again")
	default:
	}
}
