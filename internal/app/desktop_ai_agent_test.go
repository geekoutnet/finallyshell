package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
	"finallyshell/internal/sshtest"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

// aiTestOutputSecret is a made-up token a command prints, which the model
// and the window must only see masked.
const aiTestOutputSecret = "fake-output-token-for-masking-test"

// aiToolCallEvent is a chat completion event calling run_command.
func aiToolCallEvent(id, command string) string {
	args, _ := json.Marshal(map[string]string{"command": command, "reason": "testing"})
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"index": 0, "finish_reason": "tool_calls",
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": id, "type": "function",
			"function": map[string]any{"name": "run_command", "arguments": string(args)},
		}}},
	}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

func aiTextEvent(text string) string {
	chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "delta": map[string]any{"content": text}}}})
	return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
}

// aiAgentTest is a desktop with the AI assistant open on a connected
// server, a model that answers as script says, and commands that run in
// memory.
type aiAgentTest struct {
	a        *App
	platform *fakePlatform
	session  *Session
	state    *desktopAIState
	window   *desktopWindow
	mu       sync.Mutex
	bodies   []string
	commands []string
}

func newAIAgentTest(t *testing.T, script func(body string) string) *aiAgentTest {
	t.Helper()
	a, platform, tester, session := desktopTestApp(t)
	at := &aiAgentTest{a: a, platform: platform, session: session}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		at.mu.Lock()
		at.bodies = append(at.bodies, string(body))
		at.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		out := script(string(body))
		// "400 …" answers a bad request, as a provider refusing tools does.
		if rest, ok := strings.CutPrefix(out, "400 "); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			out = rest
		}
		// "500 …" fails, as a provider that is down does.
		if rest, ok := strings.CutPrefix(out, "500 "); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			out = rest
		}
		io.WriteString(w, out)
	}))
	t.Cleanup(server.Close)
	runner := runAICommand
	runAICommand = func(ctx context.Context, _ *ssh.Client, command string, _ *aiDeadline) (int, string, bool, error) {
		at.mu.Lock()
		at.commands = append(at.commands, command)
		at.mu.Unlock()
		return 0, "Filesystem Size Used\n/dev/vda1 24G 19G\ntoken=" + aiTestOutputSecret, false, nil
	}
	t.Cleanup(func() { runAICommand = runner })
	session.client = sshtest.Start(t).Dial(t)
	a.settings.AI = &config.AISettings{Endpoint: server.URL + "/v1", Model: "demo", Models: []string{"other-model"}}
	platform.SecretSet(desktopAISecret(a.settings.AI.Endpoint), "test-only-key")
	at.window = a.desktopOpen(session, appAI, nil)
	tester.Frame()
	at.state = at.window.state.(*desktopAIState)
	platform.until(t, "AI ready", func() bool { return a.ai.ready && a.ai.loaded })
	return at
}

func (at *aiAgentTest) ask(question string) {
	at.state.draft = question
	at.a.desktopAISend(at.window, at.state)
}

func (at *aiAgentTest) cards() []*desktopAIMessage {
	var out []*desktopAIMessage
	if at.state.chat != nil {
		for _, m := range at.state.chat.Messages {
			if m.Role == "command" {
				out = append(out, m)
			}
		}
	}
	return out
}

func (at *aiAgentTest) ran() []string {
	at.mu.Lock()
	defer at.mu.Unlock()
	return slices.Clone(at.commands)
}

func (at *aiAgentTest) body(i int) string {
	at.mu.Lock()
	defer at.mu.Unlock()
	if i >= len(at.bodies) {
		return ""
	}
	return at.bodies[i]
}

// TestDesktopAIAgentRunsCommands has the model run a read-only command at
// once and a restart once the user allows it, reading their output, and
// keeps the commands, not the output, in the history.
func TestDesktopAIAgentRunsCommands(t *testing.T) {
	at := newAIAgentTest(t, func(body string) string {
		switch {
		case strings.Contains(body, `"tool_call_id":"call_2"`):
			return aiTextEvent("Done: nginx restarted.")
		case strings.Contains(body, `"tool_call_id":"call_1"`):
			return aiToolCallEvent("call_2", "systemctl restart nginx")
		default:
			return aiToolCallEvent("call_1", "df -h")
		}
	})
	at.state.model = "other-model"
	at.ask("why is nginx down?")
	at.platform.until(t, "the restart waits", func() bool {
		cards := at.cards()
		return len(cards) == 2 && cards[1].Status == aiCmdWaiting
	})
	cards := at.cards()
	if first := cards[0]; first.Status != aiCmdDone || !first.Auto || first.Exit == nil || *first.Exit != 0 || first.Text != "df -h" {
		t.Fatalf("read-only command: %+v", first)
	}
	if got := at.ran(); !slices.Equal(got, []string{"df -h"}) {
		t.Fatalf("ran %q before the user allowed the restart", got)
	}
	first, second := at.body(0), at.body(1)
	if !strings.Contains(first, `"tools":[`) || !strings.Contains(first, "run_command") || !strings.Contains(first, `"model":"other-model"`) {
		t.Fatalf("first request: %s", first)
	}
	if !strings.Contains(second, `"role":"tool"`) || !strings.Contains(second, "Exit code 0.") || strings.Contains(second, aiTestOutputSecret) {
		t.Fatalf("the command's result went back as %s", second)
	}
	if strings.Contains(at.cards()[0].output, aiTestOutputSecret) {
		t.Fatal("the window shows the secret in the output")
	}
	at.cards()[0].showOutput = true
	at.platform.tt.Frame()
	saveShot(t, at.platform.tt, "ai-agent-waiting.png")
	if err := at.platform.tt.Click(locales.DesktopAICmdAllow()); err != nil {
		t.Fatalf("allow: %v; %q", err, at.platform.tt.Texts())
	}
	at.platform.until(t, "the answer", func() bool { return at.state.cancel == nil })
	at.platform.tt.Frame()
	saveShot(t, at.platform.tt, "ai-agent-done.png")
	if got := at.ran(); !slices.Equal(got, []string{"df -h", "systemctl restart nginx"}) {
		t.Fatalf("ran %q", got)
	}
	messages := at.state.chat.Messages
	if last := messages[len(messages)-1]; last.Role != "assistant" || last.Text != "Done: nginx restarted." || last.Model != "other-model" {
		t.Fatalf("last message %+v, status %q", last, at.state.status)
	}
	logged := 0
	for _, entry := range at.a.desktopOf(at.session).log {
		if strings.Contains(entry.text, "systemctl restart nginx") {
			logged++
		}
		if strings.Contains(entry.text, "df -h") {
			t.Error("a read-only command went to the operation log")
		}
	}
	if logged != 1 {
		t.Errorf("the restart is logged %d times", logged)
	}
	at.platform.until(t, "history saved", at.a.ai.disk.idle)
	data, err := os.ReadFile(filepath.Join(config.Dir(), "chats.json"))
	if err != nil || !strings.Contains(string(data), "systemctl restart nginx") || strings.Contains(string(data), "/dev/vda1") || !strings.Contains(string(data), `"model":"other-model"`) {
		t.Fatalf("history = %s: %v", data, err)
	}
	chats, err := readAIChats(filepath.Join(config.Dir(), "chats.json"))
	if err != nil || len(chats) != 1 || chats[0].Model != "other-model" {
		t.Fatalf("reopened %v: %v", chats, err)
	}
	if reopened := chats[0].Messages; len(reopened) != 4 || reopened[1].Role != "command" || reopened[1].Status != aiCmdDone || reopened[1].Exit == nil {
		t.Fatalf("reopened messages %+v", reopened)
	}
}

// TestDesktopAIAgentAsksForHighRisk has a high-risk command wait for RUN
// even when the conversation runs everything, and tells the model the
// user refused it.
func TestDesktopAIAgentAsksForHighRisk(t *testing.T) {
	at := newAIAgentTest(t, func(body string) string {
		if strings.Contains(body, "refused") {
			return aiTextEvent("Understood, I will not.")
		}
		return aiToolCallEvent("call_1", "rm -rf /")
	})
	at.state.autoRun = true
	at.ask("clean everything")
	at.platform.until(t, "the command waits", func() bool {
		cards := at.cards()
		return len(cards) == 1 && cards[0].Status == aiCmdWaiting
	})
	card := at.cards()[0]
	if !card.highRisk {
		t.Fatal("rm -rf / is not high-risk")
	}
	at.a.aiAnswer(at.state, card, true)
	if card.Status != aiCmdWaiting {
		t.Fatal("allowed without typing RUN")
	}
	at.a.aiAnswer(at.state, card, false)
	at.platform.until(t, "the answer", func() bool { return at.state.cancel == nil })
	if card.Status != aiCmdRefused || len(at.ran()) != 0 {
		t.Fatalf("status %v, ran %q", card.Status, at.ran())
	}
	if !strings.Contains(at.body(1), "refused") {
		t.Fatalf("the model was not told: %s", at.body(1))
	}
}

// TestDesktopAIAgentAutoRun runs a command that changes the system without
// asking in a conversation that runs everything, and asks again in a new
// one.
func TestDesktopAIAgentAutoRun(t *testing.T) {
	at := newAIAgentTest(t, func(body string) string {
		if strings.Contains(body, `"role":"tool"`) {
			return aiTextEvent("Restarted.")
		}
		return aiToolCallEvent("call_1", "systemctl restart nginx")
	})
	at.state.autoRun = true
	at.ask("restart nginx")
	at.platform.until(t, "the answer", func() bool { return at.state.cancel == nil })
	if got := at.ran(); !slices.Equal(got, []string{"systemctl restart nginx"}) {
		t.Fatalf("ran %q", got)
	}
	at.a.desktopAISelect(at.session, at.state, nil)
	if at.state.autoRun {
		t.Fatal("a new conversation runs everything")
	}
}

// TestDesktopAIAgentStop cancels the commands waiting when the user stops.
func TestDesktopAIAgentStop(t *testing.T) {
	at := newAIAgentTest(t, func(string) string { return aiToolCallEvent("call_1", "systemctl restart nginx") })
	at.ask("restart nginx")
	at.platform.until(t, "the command waits", func() bool { return len(at.cards()) == 1 && at.cards()[0].Status == aiCmdWaiting })
	at.a.desktopAIStop(at.state)
	if card := at.cards()[0]; card.Status != aiCmdCancelled || at.state.run != nil || at.state.cancel != nil {
		t.Fatalf("after stop: %+v", card)
	}
	at.a.aiAnswer(at.state, at.cards()[0], true)
	time.Sleep(50 * time.Millisecond)
	if len(at.ran()) != 0 {
		t.Fatal("a cancelled command ran")
	}
}

// TestAIRunCommand stops a command at the timeout and when the request
// ends, on a real SSH connection.
func TestAIRunCommand(t *testing.T) {
	server := sshtest.Start(t)
	client := server.Dial(t)
	if _, _, _, err := aiRunCommand(context.Background(), client, "true", nil); err == nil {
		t.Fatal("a refused exec did not fail")
	}
	server.HangExec()
	timeout := aiCommandTimeout
	aiCommandTimeout = 200 * time.Millisecond
	t.Cleanup(func() { aiCommandTimeout = timeout })
	start := time.Now()
	_, _, timedOut, err := aiRunCommand(context.Background(), client, "sleep 100", nil)
	if err != nil || !timedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("timed out %v, err %v, after %v", timedOut, err, time.Since(start))
	}
	// Put off as it runs, it goes on past the first deadline.
	start = time.Now()
	deadline := newAIDeadline(start, 200*time.Millisecond)
	time.AfterFunc(100*time.Millisecond, func() { deadline.extend(400 * time.Millisecond) })
	_, _, timedOut, err = aiRunCommand(context.Background(), client, "sleep 100", deadline)
	if err != nil || !timedOut || time.Since(start) < 550*time.Millisecond {
		t.Fatalf("extended: timed out %v, err %v, after %v", timedOut, err, time.Since(start))
	}
	if limit := newAIDeadline(start, time.Minute); limit.extend(2*aiMaxRunTime) && limit.limit() != aiMaxRunTime {
		t.Fatalf("extended past the longest run: %v", limit.limit())
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, _, timedOut, err = aiRunCommand(ctx, client, "sleep 100", nil)
	if !errors.Is(err, context.Canceled) || timedOut {
		t.Fatalf("cancelled: timed out %v, err %v", timedOut, err)
	}
	if _, _, _, err := aiRunCommand(context.Background(), nil, "true", nil); err == nil {
		t.Fatal("ran without a connection")
	}
}

func TestAIOutputKeepsStartAndEnd(t *testing.T) {
	var out aiOutput
	for i := range 5000 {
		fmt.Fprintf(&out, "line %d\n", i)
	}
	text := out.String()
	if !strings.HasPrefix(text, "line 0\n") || !strings.HasSuffix(text, "line 4999\n") || !strings.Contains(text, "bytes left out") || len(text) > aiOutputLimit+100 {
		t.Fatalf("output %d bytes: %q … %q", len(text), text[:40], text[len(text)-40:])
	}
}

// TestAIModelList reads the other models one per line, without blanks,
// repeats or the default, and settings that list them compare by content.
func TestAIModelList(t *testing.T) {
	got := aiModelList(" gpt-4o \n\nclaude-sonnet\ngpt-4o\ndemo\r\n", "demo")
	if !slices.Equal(got, []string{"gpt-4o", "claude-sonnet"}) {
		t.Fatalf("aiModelList = %q", got)
	}
	settings := config.AISettings{Model: "demo", Models: got}
	if !settings.HasModel("claude-sonnet") || !settings.HasModel("demo") || settings.HasModel("") || settings.HasModel("gone") {
		t.Fatal("HasModel")
	}
	other := settings
	other.Models = slices.Clone(got)
	if !settings.Equal(other) {
		t.Fatal("settings listing the same models differ")
	}
	other.Models[1] = "x"
	if settings.Equal(other) {
		t.Fatal("settings listing other models are equal")
	}
	if aiModelOf(settings, &desktopAIState{model: "gone"}) != "demo" || aiModelOf(settings, &desktopAIState{model: "gpt-4o"}) != "gpt-4o" {
		t.Fatal("aiModelOf")
	}
	tooMany := config.AISettings{Endpoint: "https://api.openai.com/v1", Model: "demo", Models: make([]string, 33)}
	if aiValidate(tooMany, true) == nil {
		t.Fatal("33 other models were accepted")
	}
}

// TestDesktopAIAgentWithoutTools asks again without tools a model that
// refuses them, and asks it without them from then on.
func TestDesktopAIAgentWithoutTools(t *testing.T) {
	at := newAIAgentTest(t, func(body string) string {
		if strings.Contains(body, `"tools":`) {
			return `400 {"error":{"message":"tools are not supported by this model"}}`
		}
		return aiTextEvent("Run df -h to check.")
	})
	at.ask("is the disk full?")
	at.platform.until(t, "the answer", func() bool { return at.state.cancel == nil })
	messages := at.state.chat.Messages
	if last := messages[len(messages)-1]; last.Text != "Run df -h to check." || at.state.failed {
		t.Fatalf("last %+v, status %q", last, at.state.status)
	}
	if !strings.Contains(at.body(1), "You have no tools") || strings.Contains(at.body(1), `"tools":`) {
		t.Fatalf("the second request: %s", at.body(1))
	}
	at.ask("and the memory?")
	at.platform.until(t, "the second answer", func() bool { return at.state.cancel == nil })
	if strings.Contains(at.body(2), `"tools":`) || at.body(3) != "" {
		t.Fatalf("the model was offered tools again: %s", at.body(2))
	}
	at.platform.tt.Frame()
	if !at.platform.tt.HasText(locales.DesktopAINoTools()) {
		t.Fatal("the window does not say why the assistant cannot run commands")
	}
}

// TestDesktopAIDeleteAndResize deletes a conversation from the sidebar,
// after asking, and drags the question's field taller and back.
func TestDesktopAIDeleteAndResize(t *testing.T) {
	at := newAIAgentTest(t, func(string) string { return aiTextEvent("ok") })
	at.ask("first question")
	at.platform.until(t, "first answer", func() bool { return at.state.cancel == nil })
	at.a.desktopAISelect(at.session, at.state, nil)
	at.ask("second question")
	at.platform.until(t, "second answer", func() bool { return at.state.cancel == nil })
	second := at.state.chat
	tt := at.platform.tt
	tt.Frame()
	if len(at.a.ai.chats) != 2 {
		t.Fatalf("%d chats", len(at.a.ai.chats))
	}
	// The open conversation's delete button, then the question.
	if err := tt.Click(locales.DesktopAIDeleteChat()); err != nil {
		t.Fatalf("delete: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if at.state.deleting == nil || !tt.HasText(locales.DesktopAIDeleteChatTitle()) {
		t.Fatal("deleting did not ask first")
	}
	deleting := at.state.deleting
	if err := tt.Click(locales.Delete()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if slices.Contains(at.a.ai.chats, deleting) || len(at.a.ai.chats) != 1 {
		t.Fatalf("chats after deleting: %d", len(at.a.ai.chats))
	}
	if deleting == second && at.state.chat != nil {
		t.Fatal("the deleted conversation is still shown")
	}
	at.platform.until(t, "history saved", at.a.ai.disk.idle)
	saved, err := readAIChats(filepath.Join(config.Dir(), "chats.json"))
	if err != nil || len(saved) != 1 {
		t.Fatalf("saved %d chats: %v", len(saved), err)
	}
	// A conversation answering is not deleted.
	at.state.cancel = func() {}
	at.state.chat = at.a.ai.chats[0]
	at.a.desktopAIDelete(at.window, at.state.chat)
	if len(at.a.ai.chats) != 1 {
		t.Fatal("deleted a conversation while it answers")
	}
	at.state.cancel = nil
	// Dragging the bar above the field up makes it taller.
	grip, ok := tt.Find(locales.DesktopAIResizeInput())
	if !ok {
		t.Fatalf("no resize bar: %q", tt.Texts())
	}
	x, y := grip.X+grip.W/2, grip.Y+grip.H/2
	tt.Press(x, y)
	tt.Move(x, y-60)
	tt.Release(x, y-60)
	tt.Frame()
	if at.state.inputH < 90 {
		t.Fatalf("input height %v after dragging up 60", at.state.inputH)
	}
	tt.Move(x, y-60)
	tt.Frame()
	grip, _ = tt.Find(locales.DesktopAIResizeInput())
	x, y = grip.X+grip.W/2, grip.Y+grip.H/2
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	tt.Frame()
	if at.state.inputH != 0 {
		t.Fatalf("a double click left the height at %v", at.state.inputH)
	}
}

// TestDesktopAIMessageActions copies a reply, asks for another in its
// place, edits a question to send it again, and retries one that failed.
func TestDesktopAIMessageActions(t *testing.T) {
	var calls atomic.Int32
	at := newAIAgentTest(t, func(string) string {
		switch calls.Add(1) {
		case 1:
			return aiTextEvent("first answer")
		case 2:
			return aiTextEvent("second answer")
		case 3:
			return aiTextEvent("edited answer")
		case 4:
			return `500 {"error":{"message":"upstream down"}}`
		}
		return aiTextEvent("retried answer")
	})
	tt := at.platform.tt
	idle := func(what string) {
		t.Helper()
		at.platform.until(t, what, func() bool { return at.state.cancel == nil })
		tt.Frame()
	}
	texts := func() []string {
		var out []string
		for _, m := range at.state.chat.Messages {
			out = append(out, m.Role+":"+m.Text)
		}
		return out
	}
	at.ask("disk usage?")
	idle("first answer")
	// Copy, on the question and on the reply.
	if err := tt.Click(locales.DesktopAICopyMessage()); err != nil {
		t.Fatalf("copy: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if at.state.copied == nil {
		t.Fatal("copying did not show that it did")
	}
	// Regenerate replaces the reply.
	if err := tt.Click(locales.DesktopAIRegenerate()); err != nil {
		t.Fatalf("regenerate: %v; %q", err, tt.Texts())
	}
	idle("second answer")
	if got := texts(); !slices.Equal(got, []string{"user:disk usage?", "assistant:second answer"}) {
		t.Fatalf("after regenerating: %q", got)
	}
	if strings.Contains(at.body(1), "first answer") {
		t.Fatal("regenerating sent the reply it replaces")
	}
	// Editing puts the question in the field; sending replaces it.
	if err := tt.Click(locales.DesktopAIEditQuestion()); err != nil {
		t.Fatalf("edit: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if at.state.draft != "disk usage?" || !tt.HasText(locales.DesktopAIEditing()) {
		t.Fatalf("editing: draft %q", at.state.draft)
	}
	saveShot(t, tt, "ai-message-actions.png")
	at.state.draft = "memory usage?"
	at.a.desktopAISend(at.window, at.state)
	idle("edited answer")
	if got := texts(); !slices.Equal(got, []string{"user:memory usage?", "assistant:edited answer"}) {
		t.Fatalf("after editing: %q", got)
	}
	// A failed answer offers Retry, which asks the question again.
	at.ask("load?")
	idle("failure")
	if !at.state.failed || at.state.retry == nil || !tt.HasText(locales.Retry()) {
		t.Fatalf("no retry after a failure: %q", at.state.status)
	}
	saveShot(t, tt, "ai-retry.png")
	if err := tt.Click(locales.Retry()); err != nil {
		t.Fatal(err)
	}
	idle("retried answer")
	if got := texts(); !slices.Equal(got, []string{"user:memory usage?", "assistant:edited answer", "user:load?", "assistant:retried answer"}) {
		t.Fatalf("after retrying: %q", got)
	}
}

// TestDesktopAIParallelChats answers in a new conversation while another
// still answers behind it, deletes an old one meanwhile, and finds the
// first answered where it was.
func TestDesktopAIParallelChats(t *testing.T) {
	release := make(chan struct{})
	at := newAIAgentTest(t, func(body string) string {
		if strings.Contains(body, "slow question") {
			<-release
			return aiTextEvent("slow answer")
		}
		return aiTextEvent("quick answer")
	})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	tt := at.platform.tt
	at.ask("old question")
	at.platform.until(t, "old answer", func() bool { return at.state.cancel == nil })
	old := at.state.chat
	at.a.desktopAIOpenChat(at.window, nil)
	at.ask("slow question")
	slow := at.state
	at.platform.until(t, "slow chat started", func() bool { return slow.chat != nil })
	tt.Frame()
	// New chat while it answers: the slow one goes on behind.
	if err := tt.Click(locales.DesktopAINew()); err != nil {
		t.Fatalf("new chat while answering: %v", err)
	}
	tt.Frame()
	quick := at.window.state.(*desktopAIState)
	if quick == slow || slow.cancel == nil {
		t.Fatal("a new conversation stopped the one answering")
	}
	at.state = quick
	at.ask("quick question")
	at.platform.until(t, "quick answer", func() bool { return quick.cancel == nil })
	tt.Frame()
	if !tt.HasText("quick answer") || len(at.a.aiConvs(at.window)) != 2 {
		t.Fatalf("conversations %d; %q", len(at.a.aiConvs(at.window)), tt.Texts())
	}
	saveShot(t, tt, "ai-parallel-chats.png")
	// The history is not locked: an old conversation deletes, the one
	// answering does not.
	at.a.desktopAIDelete(at.window, slow.chat)
	if !slices.Contains(at.a.ai.chats, slow.chat) {
		t.Fatal("deleted the conversation answering")
	}
	at.a.desktopAIDelete(at.window, old)
	if slices.Contains(at.a.ai.chats, old) {
		t.Fatal("the old conversation did not delete while another answered")
	}
	close(release)
	at.platform.until(t, "slow answer", func() bool { return slow.cancel == nil })
	at.a.desktopAIOpenChat(at.window, slow.chat)
	tt.Frame()
	if at.window.state != slow || !tt.HasText("slow answer") {
		t.Fatalf("the slow conversation did not show its answer: %q", tt.Texts())
	}
}

// TestDesktopAIAgentTimeoutRetries stops a slow scan at its limit, tells
// the model how to get it done, and runs its retry with the longer limit
// it asks for; a running command is put off by the user's Wait longer.
func TestDesktopAIAgentTimeoutRetries(t *testing.T) {
	var round atomic.Int32
	at := newAIAgentTest(t, func(body string) string {
		switch round.Add(1) {
		case 1:
			return aiToolCallEvent("call_1", "du -x -d 3 / | sort -nr | head")
		case 2:
			if !strings.Contains(body, "Stopped at its time limit of 120 seconds") || !strings.Contains(body, "timeout_seconds") {
				return aiTextEvent("no guidance in " + body)
			}
			args, _ := json.Marshal(map[string]any{"command": "nice -n 19 du -x -d 2 / | sort -nr | head", "reason": "again", "timeout_seconds": 600})
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"index": 0, "finish_reason": "tool_calls",
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_2", "type": "function",
					"function": map[string]any{"name": "run_command", "arguments": string(args)},
				}}},
			}}})
			return "data: " + string(chunk) + "\n\ndata: [DONE]\n\n"
		}
		return aiTextEvent("the largest directory is /var")
	})
	var limits []time.Duration
	var mu sync.Mutex
	runAICommand = func(_ context.Context, _ *ssh.Client, command string, deadline *aiDeadline) (int, string, bool, error) {
		mu.Lock()
		limits = append(limits, deadline.limit())
		first := len(limits) == 1
		mu.Unlock()
		if first {
			return 0, "", true, nil // the scan's sort prints nothing before it ends
		}
		return 0, "12G /var\n", false, nil
	}
	at.ask("which directory is largest?")
	at.platform.until(t, "answer after the retry", func() bool { return at.state.cancel == nil })
	cards := at.cards()
	if len(cards) != 2 || cards[0].Status != aiCmdTimedOut || cards[1].Status != aiCmdDone {
		t.Fatalf("cards: %+v", cards)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(limits) != 2 || limits[0] != aiCommandTimeout || limits[1] != 600*time.Second {
		t.Fatalf("limits %v", limits)
	}
	if last := at.state.chat.Messages[len(at.state.chat.Messages)-1]; last.Text != "the largest directory is /var" {
		t.Fatalf("answer %q", last.Text)
	}
	// Wait longer puts a running command off by five minutes.
	d := newAIDeadline(time.Now(), aiCommandTimeout)
	if !d.extend(aiWaitLonger) || d.limit() != aiCommandTimeout+aiWaitLonger {
		t.Fatalf("waited longer: %v", d.limit())
	}
}

// TestDesktopAIContextCompaction summarizes the earlier part of a
// conversation past three quarters of the budget, keeping the last three
// questions, and sends the summary in its place; a summary that fails
// leaves the question to go on.
func TestDesktopAIContextCompaction(t *testing.T) {
	var fail atomic.Bool
	at := newAIAgentTest(t, func(body string) string {
		if strings.Contains(body, "You summarize the earlier part") {
			if fail.Load() {
				return `500 {"error":{"message":"down"}}`
			}
			return aiTextEvent("SUMMARY: the user is cleaning /var/log")
		}
		return aiTextEvent("ok")
	})
	at.a.settings.AI.ContextTokens = 8000 // the smallest budget: 6000 tokens of history trigger it
	long := strings.Repeat("磁盘", 600)     // about 1200 tokens
	chat := &desktopAIChat{Host: aiHost(at.session), Title: "disk"}
	for i := range 6 {
		chat.Messages = append(chat.Messages,
			&desktopAIMessage{Role: "user", Text: fmt.Sprintf("question %d %s", i, long)},
			&desktopAIMessage{Role: "assistant", Text: fmt.Sprintf("answer %d %s", i, long)})
	}
	at.a.ai.chats = append(at.a.ai.chats, chat)
	at.a.desktopAIOpenChat(at.window, chat)
	at.state = at.window.state.(*desktopAIState)
	fail.Store(true)
	at.ask("question after a failed summary")
	at.platform.until(t, "answer without a summary", func() bool { return at.state.cancel == nil })
	if chat.Summary != "" || at.state.failed {
		t.Fatalf("a failed summary: summary %q, status %q", chat.Summary, at.state.status)
	}
	fail.Store(false)
	at.ask("question 7")
	at.platform.until(t, "answer with a summary", func() bool { return at.state.cancel == nil })
	if !strings.HasPrefix(chat.Summary, "SUMMARY:") {
		t.Fatalf("summary %q", chat.Summary)
	}
	var notices, compacted int
	for _, m := range chat.Messages {
		if m.Role == "notice" {
			notices++
		}
		if m.Compacted {
			compacted++
		}
	}
	if notices != 1 || compacted == 0 {
		t.Fatalf("%d notices, %d messages compacted", notices, compacted)
	}
	// The question's request: the summary, the last questions, not the
	// first ones.
	at.mu.Lock()
	last := at.bodies[len(at.bodies)-1]
	at.mu.Unlock()
	if !strings.Contains(last, "SUMMARY: the user is cleaning") || !strings.Contains(last, "question 7") || strings.Contains(last, "question 0 ") {
		t.Fatalf("the request after the summary: %.300s", last)
	}
	at.platform.tt.Frame()
	saveShot(t, at.platform.tt, "ai-context-compacted.png")
}

// TestAIHistoryBudget keeps the newest messages that fit the room, with
// what a command printed.
func TestAIHistoryBudget(t *testing.T) {
	exit := 0
	chat := &desktopAIChat{Messages: []*desktopAIMessage{
		{Role: "user", Text: "old " + strings.Repeat("x", 4000)},
		{Role: "assistant", Text: "old answer"},
		{Role: "command", Text: "df -h", Status: aiCmdDone, Exit: &exit, output: "/dev/vda1 80%"},
		{Role: "user", Text: "new question"},
	}}
	got := aiHistory(ai.Client{}, chat, 200)
	if len(got) != 3 || !strings.Contains(got[1].Content, "/dev/vda1 80%") || got[2].Content != "new question" {
		t.Fatalf("history %+v", got)
	}
	if all := aiHistory(ai.Client{}, chat, 100000); len(all) != 4 {
		t.Fatalf("%d messages with room for all", len(all))
	}
	if aiContextTokensOf(aiContextChoice(0)) != 0 || aiContextTokensOf("128K") != 128000 || aiContextChoice(200000) != "200K" {
		t.Fatal("the settings' choices")
	}
}

// TestDesktopAIAgentGuard makes a command that may cut the SSH connection
// wait for RUN typed, and one matching the user's rules wait for their
// approval, read-only or with everything run automatically.
func TestDesktopAIAgentGuard(t *testing.T) {
	var round atomic.Int32
	at := newAIAgentTest(t, func(body string) string {
		switch round.Add(1) {
		case 1:
			return aiToolCallEvent("call_1", "systemctl restart sshd")
		case 2:
			return aiToolCallEvent("call_2", "cat /etc/hosts")
		}
		return aiTextEvent("done")
	})
	at.a.settings.AI.DenyCommands = []string{`^cat\s+/etc/`, `([unclosed`}
	at.state.autoRun = true
	at.ask("restart ssh and read hosts")
	at.platform.until(t, "the ssh restart waits", func() bool { cards := at.cards(); return len(cards) == 1 && cards[0].Status == aiCmdWaiting })
	ssh := at.cards()[0]
	if ssh.cut != ai.CutSSH || !ssh.highRisk {
		t.Fatalf("the ssh restart: cut %v, high-risk %v", ssh.cut, ssh.highRisk)
	}
	at.platform.tt.Frame()
	if !at.platform.tt.HasText(locales.AiGuardCutConfirm()) {
		t.Fatal("no warning that it may cut the connection")
	}
	at.a.aiAnswer(at.state, ssh, true) // without RUN typed: still waits
	if ssh.Status != aiCmdWaiting {
		t.Fatal("ran without RUN typed")
	}
	at.a.aiAnswer(at.state, ssh, false)
	at.platform.until(t, "the hosts read waits", func() bool { cards := at.cards(); return len(cards) == 2 && cards[1].Status == aiCmdWaiting })
	hosts := at.cards()[1]
	if hosts.denyRule != `^cat\s+/etc/` {
		t.Fatalf("rule %q", hosts.denyRule)
	}
	if ran := at.ran(); len(ran) != 0 {
		t.Fatalf("ran before the user allowed it: %q", ran)
	}
	at.a.aiAnswer(at.state, hosts, true)
	at.platform.until(t, "answer", func() bool { return at.state.cancel == nil })
	if ran := at.ran(); len(ran) != 1 || ran[0] != "cat /etc/hosts" {
		t.Fatalf("ran %q", ran)
	}
	rules, bad := aiDenyRules(aiDenyLines("  ^rm\\s \n\n([x\n"))
	if len(rules) != 1 || len(bad) != 1 || aiDenied(rules, "rm -rf /tmp/x") == "" || aiDenied(rules, "ls") != "" {
		t.Fatalf("rules %v, bad %v", rules, bad)
	}
	a := config.AISettings{DenyCommands: []string{"x"}}
	if a.Equal(config.AISettings{}) || !a.Equal(config.AISettings{DenyCommands: []string{"x"}}) {
		t.Fatal("Equal ignores the rules")
	}
}
