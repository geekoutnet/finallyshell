package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func openPrivilegeMemory(t *testing.T, p *fakePlatform, tt *ui.Tester, requests chan memoryRequest) {
	t.Helper()
	clickButton(t, tt, locales.MemoryFree())
	clickButton(t, tt, locales.MemoryDropCaches())
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: memoryBefore}
}

func TestPrivilegeMemoryPassword(t *testing.T) {
	const secret = "fixture-not-a-secret"
	a, p, tt, requests := memoryApp(t, 1040)
	session := a.current()
	session.Host.User, session.Host.Addr = "operator", "workshop.example"
	session.ctx, session.cancel = context.WithCancel(t.Context())
	commands := make(chan monitor.PrivilegeRequest, 4)
	probes := make(chan struct{}, 4)
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			probes <- struct{}{}
			return monitor.PrivilegeResult{Stdout: "sudo"}, nil
		}
		commands <- monitor.PrivilegeRequest{Command: request.Command, PTY: request.PTY}
		if string(request.Password) != secret {
			return monitor.PrivilegeResult{Stderr: "sudo: 1 incorrect password attempt"}, errors.New("exit status 1")
		}
		return monitor.PrivilegeResult{Started: true, Authenticated: true}, nil
	}
	openPrivilegeMemory(t, p, tt, requests)
	kind := privilegeWindow(session)
	p.until(t, "sudo password window", func() bool { return a.windowOpen(kind) })
	dialog := p.show(t, kind)
	wantTexts(t, dialog, "sudo password", locales.PrivSudoPrompt("operator@workshop.example"))
	typeInto(t, dialog, locales.PrivPassword(), "fixture-wrong")
	clickButton(t, dialog, locales.PrivRun())
	p.until(t, "wrong-password window", func() bool { return a.windowOpen(kind) })
	dialog = p.show(t, kind)
	wantTexts(t, dialog, "wrong password", locales.PrivWrongPassword())
	typeInto(t, dialog, locales.PrivPassword(), secret)
	if dialog.HasText(secret) {
		t.Fatal("password is visible")
	}
	if err := dialog.Click(locales.PrivRemember()); err != nil {
		t.Fatal(err)
	}
	clickButton(t, dialog, locales.PrivRun())
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: strings.Replace(memoryBefore, "184320 kB", "606208 kB", 1)}
	p.until(t, "result and saved credential", func() bool {
		key := privilegeSecretKey(session.Host, session.Host.User)
		return tt.HasText(locales.MemoryAvailableChange("180 MB", "592 MB")) && p.secrets()[key] == "sudo\n"+secret
	})
	if len(commands) != 2 || strings.Contains((<-commands).Command, secret) || strings.Contains((<-commands).Command, secret) {
		t.Fatal("unexpected action calls or password in argv")
	}
	openPrivilegeMemory(t, p, tt, requests)
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: memoryBefore}
	p.until(t, "second result", func() bool { return tt.HasText(locales.MemoryAvailableChange("180 MB", "180 MB")) })
	if a.windowOpen(kind) || len(commands) != 1 || len(probes) != 1 {
		t.Fatal("second action did not reuse the cached credential")
	}
	previous := session.overview.memory.privilege
	session.disconnect()
	if previous.life.Err() == nil {
		t.Fatal("disconnect did not cancel the privilege cache")
	}
	session.ctx, session.cancel = context.WithCancel(t.Context())
	defer session.cancel()
	openPrivilegeMemory(t, p, tt, requests)
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: memoryBefore}
	p.until(t, "reconnected result", func() bool { return tt.HasText(locales.MemoryAvailableChange("180 MB", "180 MB")) })
	if a.windowOpen(kind) || len(commands) != 2 || len(probes) != 2 || session.overview.memory.privilege == previous {
		t.Fatal("new connection did not detect privileges and use the saved credential")
	}
}

func TestPrivilegeDisconnectClosesPrompt(t *testing.T) {
	a, p, tt, requests := memoryApp(t, 1040)
	session := a.current()
	session.ctx, session.cancel = context.WithCancel(t.Context())
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if !request.Probe {
			t.Error("action ran without a password")
		}
		return monitor.PrivilegeResult{Stdout: "sudo"}, nil
	}
	openPrivilegeMemory(t, p, tt, requests)
	p.until(t, "privilege window", func() bool { return a.windowOpen(privilegeWindow(session)) })
	session.disconnect()
	p.until(t, "prompt closed on disconnect", func() bool { return !a.windowOpen(privilegeWindow(session)) })
}

func TestPrivilegeMemoryScreenshotsAndCancel(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				a, p, tt, requests := memoryApp(t, 460)
				p.SetLocale(language)
				tt.SetDark(appearance == "dark")
				tt.Frame()
				session := a.current()
				session.Host.User, session.Host.Addr = "operator", "workshop.example"
				calls := make(chan struct{}, 1)
				session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
					if request.Probe {
						return monitor.PrivilegeResult{Stdout: "sudo"}, nil
					}
					calls <- struct{}{}
					return monitor.PrivilegeResult{}, errors.New("unexpected action")
				}
				openPrivilegeMemory(t, p, tt, requests)
				kind := privilegeWindow(session)
				p.until(t, "privilege window", func() bool { return a.windowOpen(kind) })
				dialog := p.show(t, kind)
				dialog.SetDark(appearance == "dark")
				dialog.Frame()
				saveShot(t, dialog, fmt.Sprintf("priv-sudo-%s-%s.png", language, appearance))
				clickButton(t, dialog, locales.PrivUseSu())
				wantTexts(t, dialog, "su mode", locales.PrivSuPrompt("workshop.example"))
				saveShot(t, dialog, fmt.Sprintf("priv-su-%s-%s.png", language, appearance))
				clickButton(t, dialog, locales.Cancel())
				p.until(t, "root instructions", func() bool { return tt.HasText(locales.PrivNeedsRoot()) })
				if len(calls) != 0 || len(requests) != 0 {
					t.Fatal("cancel ran the action")
				}
				commands := []string{"usermod -aG sudo operator", "usermod -aG wheel operator"}
				wantTexts(t, tt, "root instructions", commands[0], commands[1])
				saveShot(t, tt, fmt.Sprintf("priv-denied-%s-%s.png", language, appearance))
				clickButton(t, tt, locales.PrivCopyCommands())
				if tt.Clipboard() != strings.Join(commands, "\n") {
					t.Fatal("commands not copied")
				}
			})
		}
	}
}
