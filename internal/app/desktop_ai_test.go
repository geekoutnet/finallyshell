package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func TestDesktopAIChatPrivacy(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	window := a.desktopOpen(session, appAI, nil)
	tester.Frame()
	if !tester.HasText(locales.DesktopAISetupTitle()) {
		t.Fatal(tester.Texts())
	}
	var calls atomic.Int32
	bodies := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Look at **disk usage**.\\n\\n```sh\\ndf -h\\n```\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	state := window.state.(*desktopAIState)
	platform.until(t, "history loaded", func() bool { return a.ai.loaded })
	state.draft = "hello"
	a.desktopAISend(window, state)
	if calls.Load() != 0 || len(a.ai.chats) != 0 {
		t.Fatal("unconfigured send did work")
	}
	a.settings.AI = &config.AISettings{Endpoint: server.URL + "/v1", Model: "demo"}
	a.desktopAISend(window, state)
	platform.until(t, "missing credential", func() bool { return state.cancel == nil })
	if calls.Load() != 0 || state.status != locales.AiKeyRequired() {
		t.Fatalf("missing credential: calls %d, status %q", calls.Load(), state.status)
	}
	key := "artificial-provider-key"
	if err := platform.SecretSet(desktopAISecret(a.settings.AI.Endpoint), key); err != nil {
		t.Fatal(err)
	}
	a.desktopAISend(window, state)
	platform.until(t, "chat reply", func() bool { return state.cancel == nil })
	body := aiTestBody(t, bodies, state.status)
	if strings.Contains(body, "messages-for-preview") || strings.Contains(body, key) || !strings.Contains(body, "hello") {
		t.Fatalf("unexpected body: %s", body)
	}
	tester.Frame()
	if !tester.HasText("df -h") {
		t.Fatal(tester.Texts())
	}
	state.draft = "next question"
	state.attach = true
	state.preview = "messages-for-preview"
	a.desktopAISend(window, state)
	platform.until(t, "attached reply", func() bool { return state.cancel == nil })
	body = aiTestBody(t, bodies, state.status)
	if !strings.Contains(body, "messages-for-preview") || strings.Contains(body, "disk usage") {
		t.Fatalf("unexpected attachment body: %s", body)
	}
	platform.until(t, "chat history saved", a.ai.disk.idle)
	data, err := os.ReadFile(filepath.Join(config.Dir(), "chats.json"))
	if err != nil || strings.Contains(string(data), "messages-for-preview") || strings.Contains(string(data), key) || !strings.Contains(string(data), "next question") {
		t.Fatalf("history = %s: %v", data, err)
	}
	chats, err := readAIChats(filepath.Join(config.Dir(), "chats.json"))
	if err != nil || len(chats) != 1 || len(chats[0].Messages) != 3 || chats[0].Messages[2].Text != "next question" {
		t.Fatalf("could not reopen the local chat: %v, %v", chats, err)
	}
}

func aiTestBody(t *testing.T, bodies <-chan string, status string) string {
	t.Helper()
	select {
	case body := <-bodies:
		return body
	default:
		t.Fatalf("no request, status %q", status)
		return ""
	}
}

func aiTestTerminal(t *testing.T, session *Session) (*terminal.Terminal, *keyConn) {
	t.Helper()
	conn := &keyConn{done: make(chan struct{})}
	term, err := terminal.New(terminal.Options{Conn: conn})
	if err != nil {
		close(conn.done)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(conn.done)
		term.Close()
	})
	session.setTerm(term)
	term.Feed([]byte("dev@web-1:~$ df -h\r\nFilesystem      Size  Used Avail Use% Mounted on\r\n/dev/vda1        24G   19G    5G  80% /\r\ndev@web-1:~$ "))
	return term, conn
}

func aiTestConfigured(a *App, platform *fakePlatform) {
	a.settings.AI = &config.AISettings{Provider: "custom", Endpoint: "https://models.invalid/v1", Model: "demo-model"}
	platform.SecretSet(desktopAISecret(a.settings.AI.Endpoint), "test-only-key")
}

func aiTestClick(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts %q", label, err, tester.Texts())
	}
	tester.Frame()
}

func TestDesktopAIRunConfirmation(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	term, conn := aiTestTerminal(t, session)
	aiTestConfigured(a, platform)
	window := a.desktopOpen(session, appAI, nil)
	state := window.state.(*desktopAIState)
	state.chat = &desktopAIChat{Messages: []*desktopAIMessage{{Role: "assistant", Text: "```sh\ndf -h\n```"}}}
	platform.until(t, "AI ready", func() bool { return a.ai.ready })
	tester.Frame()
	if got := conn.sent(t, term); got != "" {
		t.Fatalf("opening chat sent %q", got)
	}
	aiTestClick(t, tester, locales.DesktopAIRun())
	if state.confirm == nil || state.confirm.command != "df -h" || !tester.HasText(locales.DesktopAIRunTitle()) {
		t.Fatalf("missing confirmation: %+v, %q", state.confirm, tester.Texts())
	}
	aiTestClick(t, tester, locales.Cancel())
	if state.confirm != nil || conn.sent(t, term) != "" {
		t.Fatal("cancel did not close or sent input")
	}
	aiTestClick(t, tester, locales.DesktopAIRun())
	aiTestClick(t, tester, locales.DesktopAIRunConfirm())
	if got := conn.sent(t, term); got != "df -h\r" {
		t.Fatalf("confirm sent %q", got)
	}
	a.openDesktop(session)
	state.chat.Messages[0].Text = "```sh\nmkfs.ext4 /dev/fake-fixture\n```"
	tester.Frame()
	aiTestClick(t, tester, locales.DesktopAIRun())
	if state.confirm == nil || !state.confirm.highRisk || !tester.HasText(locales.DesktopAIHighRisk()) {
		t.Fatalf("missing extra confirmation: %q", tester.Texts())
	}
	_ = tester.Click(locales.DesktopAIRunConfirm())
	if got := conn.sent(t, term); got != "" || state.confirm == nil {
		t.Fatalf("sent high-risk command without typing RUN: %q", got)
	}
	aiTestClick(t, tester, locales.Cancel())
	aiTestClick(t, tester, locales.DesktopAIInsert())
	if got := conn.sent(t, term); got != "mkfs.ext4 /dev/fake-fixture" {
		t.Fatalf("insert sent %q", got)
	}
}

func TestDesktopAIOptionsAndCommand(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	term, conn := aiTestTerminal(t, session)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-fixture-key" {
			t.Error("incorrect bearer credential")
		}
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"data":[{"id":"demo-model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "UNREVIEWED") {
			t.Error("unexpected context")
		}
		if strings.Contains(string(body), "return a command") {
			io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"```sh\\ndf -h\\n```\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	a.openAIOptions()
	options := platform.show(t, WindowOptions)
	state := a.ai.options
	state.endpoint, state.key, state.draft.Model = server.URL+"/v1", "fake-fixture-key", "demo-model"
	options.Frame()
	aiTestClick(t, options, locales.AiFetchModels())
	platform.until(t, "model list", func() bool { return state.cancel == nil })
	if len(state.models) != 1 || state.models[0] != "demo-model" {
		t.Fatalf("models: %v; status: %s", state.models, state.status)
	}
	a.aiOptionsRequest(state, false)
	platform.until(t, "model test", func() bool { return state.cancel == nil })
	if !strings.Contains(state.status, "\nOK") || !strings.Contains(state.status, " ms") {
		t.Fatalf("test result: %q", state.status)
	}
	a.aiSaveOptions(state)
	platform.until(t, "AI settings saved", func() bool { return !state.writing })
	data, err := os.ReadFile(filepath.Join(config.Dir(), "settings.json"))
	if err != nil || strings.Contains(string(data), "fake-fixture-key") || !strings.Contains(string(data), "demo-model") {
		t.Fatalf("settings = %s: %v", data, err)
	}
	if key, ok, err := platform.SecretGet(desktopAISecret(state.endpoint)); err != nil || !ok || key != "fake-fixture-key" {
		t.Fatal("key not saved in credential store")
	}
	a.closeWindow(WindowOptions)
	a.active = 0
	tester.Frame()
	key := a.shortcut(scGenerateAI)
	tester.Key(key.Mods, key.Key)
	platform.until(t, "command generator", func() bool { return a.ai.ready })
	tester.Frame()
	if !tester.HasText(locales.AiGenerate()) || a.ai.command == nil {
		t.Fatalf("no terminal popover: %q", tester.Texts())
	}
	tester.Type("return a command")
	tester.Frame()
	aiTestClick(t, tester, locales.AiGenerateButton())
	platform.until(t, "command result", func() bool { return a.ai.command.cancel == nil })
	if a.ai.command.result != "df -h" || calls.Load() != 3 {
		t.Fatalf("command: %q, status: %q, calls: %d", a.ai.command.result, a.ai.command.status, calls.Load())
	}
	if got := conn.sent(t, term); got != "" {
		t.Fatalf("generation sent %q to the terminal before review", got)
	}
	aiTestClick(t, tester, locales.AiInsertCommand())
	if got := conn.sent(t, term); got != "df -h" {
		t.Fatalf("generation inserted %q", got)
	}
}

func TestDesktopAIContextPreviewAndStop(t *testing.T) {
	a, platform, tester, session := desktopTestApp(t)
	term, _ := aiTestTerminal(t, session)
	key := "arbitrary-fixture-credential"
	term.Feed([]byte("\r\nfixture output: " + key + "\r\nAuthorization: Bearer fake-bearer-value\r\n"))
	requests := make(chan string, 1)
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "show failure") {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"fake quota error"}}`)
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial fixture response\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	a.settings.AI = &config.AISettings{Endpoint: server.URL + "/v1", Model: "demo"}
	platform.SecretSet(desktopAISecret(a.settings.AI.Endpoint), key)
	window := a.desktopOpen(session, appAI, nil)
	state := window.state.(*desktopAIState)
	defer a.desktopAIStop(state)
	platform.until(t, "chat and history ready", func() bool { return a.ai.ready && a.ai.loaded })
	tester.Frame()
	aiTestClick(t, tester, locales.DesktopAIQuickSlow())
	platform.until(t, "context available", func() bool { return !state.previewBusy })
	if !state.attach || !strings.Contains(state.preview, "java") || !strings.Contains(state.preview, "fixture output: [redacted]") || strings.Contains(state.preview, key) || strings.Contains(state.preview, "fake-bearer-value") {
		t.Fatalf("unexpected attachment: %q", state.preview)
	}
	aiTestClick(t, tester, locales.DesktopAISend())
	platform.until(t, "partial response", func() bool {
		return state.activeReply != nil && state.activeReply.Text == "partial fixture response"
	})
	if body := aiTestBody(t, requests, state.status); !strings.Contains(body, "fixture output: [redacted]") || strings.Contains(body, key) {
		t.Fatalf("bad request: %q", body)
	}
	aiTestClick(t, tester, locales.DesktopAIStop())
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel the request")
	}
	if state.attach || state.cancel != nil {
		t.Fatal("one-shot attachment or request was left active")
	}
	state.draft = "show failure"
	tester.Frame()
	aiTestClick(t, tester, locales.DesktopAISend())
	platform.until(t, "server error", func() bool { return state.cancel == nil })
	if !state.failed || !strings.Contains(state.status, "HTTP 429: fake quota error") {
		t.Fatalf("error not shown: %q", state.status)
	}
}

func TestDesktopAIOnlyLocalOllamaWithoutKey(t *testing.T) {
	a, platform := testApp(t)
	settings := config.AISettings{Endpoint: "http://localhost:11434/v1", Provider: "ollama", Model: "demo"}
	result := false
	checked := false
	check := func(expected bool) {
		t.Helper()
		checked = false
		a.aiCredential(t.Context(), settings, "", false, func(client ai.Client, err error) {
			result, checked = err == nil && client.Key == "", true
		})
		platform.until(t, "local Ollama authorization", func() bool { return checked })
		if result != expected {
			t.Fatalf("local authorization = %v, want %v", result, expected)
		}
	}
	check(false)
	state := &aiOptionsState{draft: settings, endpoint: settings.Endpoint}
	a.aiSaveOptions(state)
	platform.until(t, "saved local endpoint", func() bool { return !state.writing })
	check(true)
	settings.Provider = "custom"
	check(false)
}

func TestDesktopAIDesktopTerminalHook(t *testing.T) {
	a, platform, _, session := desktopTestApp(t)
	aiTestConfigured(a, platform)
	main, mainConn := aiTestTerminal(t, session)
	other := newSession(session.Host)
	term, conn := aiTestTerminal(t, other)
	window := a.desktopOpen(session, appTerminal, nil)
	tester := ui.NewTester(func(c *ui.Context) {
		a.frame++
		ui.Box(c).Fill().Children(func() {
			view := terminal.View(c, term).Fill().AutoFocus()
			a.desktopAITerminal(c, session, window, term, view)
		})
	}, 520, 320)
	platform.tt = tester
	tester.Frame()
	key := a.shortcut(scGenerateAI)
	tester.Key(key.Mods, key.Key)
	platform.until(t, "desktop terminal model ready", func() bool { return a.ai.ready })
	if a.ai.command == nil || a.ai.command.target.term != term {
		t.Fatal("shortcut did not capture the desktop terminal")
	}
	a.ai.command.result = "df -h"
	tester.Frame()
	aiTestClick(t, tester, locales.AiInsertCommand())
	if got := conn.sent(t, term); got != "df -h" {
		t.Fatalf("desktop terminal input = %q", got)
	}
	if got := mainConn.sent(t, main); got != "" {
		t.Fatalf("input went to main terminal: %q", got)
	}
	a.desktopClose(window)
	if a.aiDeliver(session, aiTerminalTarget{window, term}, "df -h", true) {
		t.Fatal("delivered to a stale desktop terminal")
	}
}

func TestDesktopAIShots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			for _, sizes := range []string{"wide", "small"} {
				suffix := language + "-" + appearance + "-" + sizes + ".png"
				t.Run(suffix, func(t *testing.T) {
					a, platform, tester, session := desktopTestApp(t)
					term, _ := aiTestTerminal(t, session)
					platform.SetLocale(language)
					tester.SetDark(appearance == "dark")
					window := a.desktopOpen(session, appAI, nil)
					if sizes == "small" {
						window.w, window.h = window.app.minSize.W, window.app.minSize.H+desktopTitleHeight
						window.x, window.y = 112, 105
					}
					tester.Frame()
					saveShot(t, tester, "ai-setup-"+suffix)
					aiTestConfigured(a, platform)
					state := window.state.(*desktopAIState)
					question, answer := "How can I see which volume is filling up?", "Check the `Use%` column first. This command reads disk usage without changing anything.\n\n```bash\ndf -h\n```\n\n- Look for a mount with little space left.\n- Check that mount before removing anything."
					if language == "zh-Hans" {
						question, answer = "如何找出快满的磁盘？", "先看 `Use%` 这一列。这条命令只读取磁盘使用情况，不会修改服务器。\n\n```bash\ndf -h\n```\n\n- 找到剩余空间最少的挂载点。\n- 清理前先核对挂载点。"
					}
					state.chat = &desktopAIChat{Host: aiHost(session), Title: question, Messages: []*desktopAIMessage{{Role: "user", Text: question}, {Role: "assistant", Text: answer}}}
					tester.Frame()
					platform.until(t, "AI ready", func() bool { return a.ai.ready })
					tester.Frame()
					if sizes == "small" {
						state.scroll.Y = 80
						tester.Frame()
					}
					saveShot(t, tester, "ai-chat-"+suffix)
					a.openAIOptions()
					options := platform.show(t, WindowOptions)
					options.SetDark(appearance == "dark")
					if sizes == "small" {
						options.SetSize(560, 400)
					} else {
						options.SetSize(1280, 820)
						options.SetScale(1)
					}
					options.Frame()
					saveShot(t, options, "ai-options-"+suffix)
					a.closeWindow(WindowOptions)
					a.active = 0
					term.Feed([]byte("\r\n            dev@web-1:~$ "))
					if sizes == "small" {
						tester.SetSize(760, 520)
					}
					tester.Frame()
					key := a.shortcut(scGenerateAI)
					tester.Key(key.Mods, key.Key)
					if a.ai.command == nil {
						t.Fatal("command popover did not open")
					}
					a.ai.command.prompt, a.ai.command.result = question, "df -h"
					tester.Advance(200 * time.Millisecond)
					tester.Frame()
					saveShot(t, tester, "ai-command-"+suffix)
				})
			}
		}
	}
}
