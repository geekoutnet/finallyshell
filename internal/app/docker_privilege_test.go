package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const dockerPrivFixture = `---FINALLYSHELL:tool---
docker
---FINALLYSHELL:info---
{"ServerVersion":"27.3.1","Containers":2,"ContainersRunning":1,"ContainersStopped":1}
---FINALLYSHELL:status---
0
---FINALLYSHELL:inspect---
{"Id":"a12345","Name":"/web-demo","Config":{"Image":"example/web:1"},"State":{"Status":"running","Running":true}}
{"Id":"b12345","Name":"/retired-job","Config":{"Image":"example/job:1"},"State":{"Status":"exited"}}
---FINALLYSHELL:end---
`

func dockerPrivilegeApp(t *testing.T) (*App, *fakePlatform, *Session, *ui.Tester, *atomic.Int32) {
	t.Helper()
	a, p, tt, _ := overviewApp(t)
	tt.SetSize(1280, 820)
	s := a.sessions[0]
	s.Host.User, s.Host.Addr = "operator", "workshop.example"
	s.ip = "192.0.2.42"
	s.ctx, s.cancel = context.WithCancel(t.Context())
	s.overview.info, s.overview.read = demoSysInfo(), true
	s.overview.info.Addrs = []monitor.Addr{{Iface: "eth0", Addr: "192.0.2.42/24"}, {Iface: "eth0", Addr: "2001:db8::42/64"}}
	var reads atomic.Int32
	a.readDocker = func(context.Context, *ssh.Client) (monitor.Docker, error) {
		reads.Add(1)
		return monitor.Docker{Tool: "docker", State: monitor.DockerNeedsRoot}, nil
	}
	s.overview.docker.run = func(_ context.Context, _ *ssh.Client, script string, _ func(string)) (string, error) {
		t.Errorf("unelevated command: %q", script)
		return "", errors.New("unelevated Docker command")
	}
	a.terminalPct = 62
	a.openDockerPage(s, dockerSecContainers)
	return a, p, s, tt, &reads
}

func TestDockerPrivilegePasswordAndActions(t *testing.T) {
	const sudoPassword = "fixture-privilege-password"
	a, p, s, tt, reads := dockerPrivilegeApp(t)
	state := &s.overview.docker
	var probes, rootReads atomic.Int32
	actions := make(chan string, 8)
	registryInput := make(chan string, 1)
	eventCanceled := make(chan struct{}, 1)
	s.overview.memory.privilegeRunner = func(ctx context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			probes.Add(1)
			return monitor.PrivilegeResult{Stdout: "sudo"}, nil
		}
		if string(request.Password) != sudoPassword || strings.Contains(request.Command, sudoPassword) || strings.Contains(request.Action, "sudo -n") {
			t.Error("unexpected credentials or nested sudo")
		}
		result := monitor.PrivilegeResult{Started: true, Authenticated: true}
		script := request.Action
		switch {
		case strings.Contains(script, "m tool; echo"):
			rootReads.Add(1)
			result.Stdout = dockerPrivFixture
		case strings.Contains(script, "'events'"):
			request.ReportProgress(`{"Type":"container","Action":"start","time":1700000000,"Actor":{"ID":"a12345","Attributes":{"name":"web-demo"}}}` + "\n")
			<-ctx.Done()
			eventCanceled <- struct{}{}
			return result, ctx.Err()
		case strings.Contains(script, "'--password-stdin'"):
			data, err := io.ReadAll(request.Stdin)
			if err != nil || len(data) == 0 || strings.Contains(request.Command, strings.TrimSuffix(string(data), "\n")) {
				t.Error("registry password is not isolated on stdin")
			}
			registryInput <- string(data)
			result.Stdout, result.Stderr = string(data), string(data)
		case strings.Contains(script, "'rm' 'b12345'"):
			actions <- script
			result.Stdout = "fixture engine refused removal"
			return result, &monitor.DockerExit{Status: 1}
		case strings.Contains(script, "Unable to read registry metadata."):
			result.Stdout = `{"registries":["docker.io"],"store":"pass"}`
		case strings.Contains(script, "'info'"):
			result.Stdout = `{"OperatingSystem":"Linux","DockerRootDir":"/var/lib/docker"}`
		case strings.Contains(script, "'version'"):
			result.Stdout = `{"Server":{"Version":"27.3.1","ApiVersion":"1.47"}}`
		case strings.Contains(script, "'system' 'df'"):
			result.Stdout = `{"Type":"Images","TotalCount":"2","Active":"1","Size":"25MB"}`
		default:
			t.Errorf("unexpected root script %q", script)
		}
		return result, nil
	}
	tt.Frame()
	p.until(t, "root required", func() bool { return tt.HasText(locales.DockerPrivNeedsRoot("Docker")) })
	a.readDockerOf(s)
	p.until(t, "ordinary refresh", func() bool { return !state.loading })
	if probes.Load() != 0 || a.windowOpen(privilegeWindow(s)) || reads.Load() != 2 || tt.HasText(locales.DockerDenied()) {
		t.Fatal("refresh attempted authorization or displayed a final denial")
	}
	clickButton(t, tt, locales.DockerPrivUseRoot())
	kind := privilegeWindow(s)
	p.until(t, "sudo prompt", func() bool { return a.windowOpen(kind) })
	clickButton(t, p.show(t, kind), locales.Cancel())
	p.until(t, "cancellation goes back to root required", func() bool { return !state.loading && tt.HasText(locales.DockerPrivNeedsRoot("Docker")) })
	if rootReads.Load() != 0 || tt.HasText(locales.DockerDenied()) {
		t.Fatal("cancel ran a root command or displayed a final denial")
	}
	clickButton(t, tt, locales.DockerPrivUseRoot())
	p.until(t, "retry sudo prompt", func() bool { return a.windowOpen(kind) })
	dialog := p.show(t, kind)
	typeInto(t, dialog, locales.PrivPassword(), sudoPassword)
	if dialog.HasText(sudoPassword) {
		t.Fatal("sudo password shown")
	}
	clickButton(t, dialog, locales.PrivRun())
	p.until(t, "privileged container list", func() bool { return state.d.Root && tt.HasText("retired-job") })
	a.openPanelTab(s, tabOverview)
	tt.Frame()
	box, ok := tt.AccessByRole(ui.RoleMenuButton, locales.DockerMore("retired-job"))
	if !ok || box.Bounds.Y > 820 {
		tt.Scroll(780, 700, 0, 5000)
		tt.Frame()
	}
	rowMenu(t, tt, "retired-job", locales.DockerRemove())
	clickButton(t, tt, locales.Cancel())
	select {
	case <-actions:
		t.Fatal("cancel issued a mutating command")
	default:
	}
	rowMenu(t, tt, "retired-job", locales.DockerRemove())
	clickButton(t, tt, locales.DockerRemove())
	select {
	case script := <-actions:
		if script != "export LC_ALL=C\nexec 2>&1\ndocker 'rm' 'b12345'\n" {
			t.Fatalf("wrong action script %q", script)
		}
	case <-time.After(time.Second):
		t.Fatal("no privileged removal")
	}
	p.until(t, "failure and privileged reread", func() bool {
		return rootReads.Load() == 2 && !state.loading && tt.HasText("fixture engine refused removal")
	})
	clickButton(t, tt, locales.Close())
	a.openDockerPage(s, dockerSecSystem)
	page := a.dockerResources(s)
	page.system.mode = dockerSystemEvents
	tt.Frame()
	p.until(t, "privileged live events and concurrent system reads", func() bool {
		return len(page.system.events.rows) == 1 && page.system.disk.read && page.system.info.read
	})
	page.system.mode = dockerSystemRegistries
	tt.Frame()
	select {
	case <-eventCanceled:
	case <-time.After(time.Second):
		t.Fatal("leaving events did not cancel the privileged stream")
	}
	p.until(t, "privileged registry metadata", func() bool { return page.system.registries.read })
	a.openDockerLogin(s, page)
	tt.Frame()
	resourceType(t, tt, locales.FieldUser(), "fixture-user")
	const registryPassword = "fixture-registry-password"
	resourceType(t, tt, locales.FieldPassword(), registryPassword)
	resourceClickButton(t, tt, locales.DockerSysLogin())
	select {
	case input := <-registryInput:
		if input != registryPassword+"\n" {
			t.Fatal("registry stdin was changed")
		}
	case <-time.After(time.Second):
		t.Fatal("missing privileged registry stdin")
	}
	p.until(t, "registry operation completed", func() bool { return page.work == nil })
	if tt.HasText(registryPassword) || page.lastWork.work.output != "" || probes.Load() != 2 || reads.Load() != 2 || a.windowOpen(kind) {
		t.Fatal("root mode reprompted, used an ordinary refresh, or exposed registry input")
	}
}

func TestDockerPrivilegeCachedOrDenied(t *testing.T) {
	for _, mode := range []string{"cached", "refused"} {
		t.Run(mode, func(t *testing.T) {
			a, p, s, tt, _ := dockerPrivilegeApp(t)
			var probes atomic.Int32
			s.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
				if request.Probe {
					probes.Add(1)
					if mode == "refused" {
						return monitor.PrivilegeResult{Stdout: "unavailable"}, nil
					}
					return monitor.PrivilegeResult{Stdout: "root"}, nil
				}
				out := ""
				if strings.Contains(request.Action, "m tool; echo") {
					out = dockerPrivFixture
				}
				return monitor.PrivilegeResult{Stdout: out, Started: true}, nil
			}
			tt.Frame()
			p.until(t, "needs root", func() bool { return tt.HasText(locales.DockerPrivNeedsRoot("Docker")) })
			if mode == "cached" {
				if _, _, err := a.sessionPrivilege(s).runner.Run(t.Context(), "true", monitor.PrivilegeOptions{}); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				p.until(t, "automatic privileged read", func() bool { return s.overview.docker.d.Root })
			} else {
				clickButton(t, tt, locales.DockerPrivUseRoot())
				p.until(t, "final denial", func() bool { return s.overview.docker.d.State == monitor.DockerDenied })
				wantTexts(t, tt, "denial help", locales.DockerDenied(), "usermod -aG sudo operator", "usermod -aG wheel operator", "usermod -aG docker operator")
				a.readDockerOf(s)
				p.until(t, "denied refresh", func() bool { return !s.overview.docker.loading })
				if s.overview.docker.d.State != monitor.DockerDenied {
					t.Fatal("ordinary refresh lost the explicit denial")
				}
			}
			if probes.Load() != 1 || a.windowOpen(privilegeWindow(s)) {
				t.Fatal("unexpected prompt or repeated authentication probe")
			}
		})
	}
}

func TestDockerPrivilegeTerminal(t *testing.T) {
	for _, mode := range []string{"sudo", "su"} {
		t.Run(mode, func(t *testing.T) {
			server := sshtest.Start(t)
			a, p, tt, _, _ := actionDockerApp(t, server)
			s := a.sessions[0]
			t.Cleanup(func() {
				for _, pane := range s.panes.panes() {
					pane.end()
				}
			})
			s.overview.docker.d.Root = true
			runner := monitor.NewPrivilege(t.Context(), func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
				if request.Probe {
					return monitor.PrivilegeResult{Stdout: mode}, nil
				}
				return monitor.PrivilegeResult{Started: true, Authenticated: true}, nil
			}, monitor.PrivilegeCredentials{Ask: func(_ context.Context, method monitor.PrivilegeMethod, _ bool) (monitor.PrivilegeAnswer, error) {
				return monitor.PrivilegeAnswer{Method: method, Password: []byte("fixture-only")}, nil
			}})
			if _, _, err := runner.Run(t.Context(), "true", monitor.PrivilegeOptions{}); err != nil {
				t.Fatal(err)
			}
			s.overview.memory.privilege = &privilegeSession{client: s.client, life: t.Context(), runner: runner}
			rowMenu(t, tt, "gitea", locales.DockerTerminal())
			command, err := (monitor.DockerCmd{Tool: "docker"}).ExecShell(s.overview.docker.d.Containers[3].ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "sudo " + command + "\r"
			if mode == "su" {
				want = "su '-s' '/bin/sh' '-c' " + composeShellArg(command) + " 'root'\r"
			}
			p.until(t, "interactive root command", func() bool { return strings.Contains(server.ShellInput(0), want) })
			if strings.Contains(server.ShellInput(0), "fixture-only") || a.windowOpen(privilegeWindow(s)) {
				t.Fatal("interactive terminal used saved credentials")
			}
		})
	}
}

func TestDockerPrivilegeScreenshots(t *testing.T) {
	dir := os.Getenv("SHOT_DIR")
	if dir == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				a, p, s, tt, _ := dockerPrivilegeApp(t)
				p.SetLocale(language)
				tt.SetDark(appearance == "dark")
				s.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
					if !request.Probe {
						t.Error("screenshot initiated a privileged action")
					}
					return monitor.PrivilegeResult{Stdout: "sudo"}, nil
				}
				shot := func(scene string) {
					tt.Frame()
					saveShot(t, tt, fmt.Sprintf("docker-priv-%s-%s-%s.png", scene, language, appearance))
				}
				tt.Frame()
				p.until(t, "needs root", func() bool { return tt.HasText(locales.DockerPrivNeedsRoot("Docker")) })
				shot("short")
				a.bottomSide = config.PanelRight
				shot("right")
				a.bottomSide = ""
				a.openPanelTab(s, tabDocker)
				shot("full")
				clickButton(t, tt, locales.DockerPrivUseRoot())
				kind := privilegeWindow(s)
				p.until(t, "password window", func() bool { return a.windowOpen(kind) })
				dialog := p.show(t, kind)
				dialog.SetScale(1)
				dialog.SetDark(appearance == "dark")
				dialog.Frame()
				background, foreground := tt.Image(), dialog.Image()
				bounds := background.Bounds()
				overlay := image.NewRGBA(bounds)
				draw.Draw(overlay, bounds, background, bounds.Min, draw.Src)
				dest := foreground.Bounds().Add(bounds.Min.Add(bounds.Size().Sub(foreground.Bounds().Size()).Div(2)))
				frame := dest.Inset(-1)
				frame.Min.Y -= 1
				border := color.RGBA{R: 161, G: 170, B: 184, A: 255}
				if appearance == "dark" {
					border = color.RGBA{R: 82, G: 89, B: 101, A: 255}
				}
				draw.Draw(overlay, frame, image.NewUniform(border), image.Point{}, draw.Src)
				draw.Draw(overlay, dest, foreground, foreground.Bounds().Min, draw.Src)
				file, err := os.Create(filepath.Join(dir, fmt.Sprintf("docker-priv-password-%s-%s.png", language, appearance)))
				if err != nil {
					t.Fatal(err)
				}
				if err := errors.Join(png.Encode(file, overlay), file.Close()); err != nil {
					t.Fatal(err)
				}
				clickButton(t, dialog, locales.Cancel())
				p.until(t, "password canceled", func() bool { return !s.overview.docker.loading })
				a.openBottomTab(tabOverview)
				tt.Frame()
				tt.Scroll(780, 670, 0, 5000)
				tt.Frame()
				shot("overview")
				clickButton(t, tt, locales.DockerPrivUseRoot())
				p.until(t, "Overview button opens password", func() bool { return a.windowOpen(kind) })
				clickButton(t, p.show(t, kind), locales.Cancel())
				p.until(t, "Overview password canceled", func() bool { return !s.overview.docker.loading })
				a.backToTerminal(a.current())
				a.openBottomTab(tabOverview)
				tt.Frame()
				tt.Scroll(780, 670, 0, 5000)
				tt.Frame()
				tt.Scroll(780, 670, 0, -52)
				shot("overview-short")
			})
		}
	}
}
