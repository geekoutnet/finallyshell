package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/config"
	"finallyshell/internal/monitor"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

func init() {
	registerDesktopApp(desktopApp{
		id: appAI, icon: "sparkles", tint: ui.Hex("#c026d3"), name: locales.DesktopAppAI,
		category: desktopCatTools, order: 80, onDesktop: true, pinned: true,
		size: desktopSize{W: 760, H: 590}, minSize: desktopSize{W: 400, H: 320},
		single: true, view: (*App).desktopAIView, opened: (*App).desktopAIOpened, closing: (*App).desktopAIClosing,
	})
}

type desktopAIRuntime struct {
	options  *aiOptionsState
	readyFor config.AISettings
	checking bool
	ready    bool
	keyError string
	// noTools are the models, by aiToolsKey, that refused the agent's
	// tools: they get questions for advice alone while the app runs.
	noTools map[string]bool

	loaded, loading, saved bool
	disk                   secretQueue
	chats                  []*desktopAIChat
	storeError             bool

	command *aiCommandState
	targets map[*Session]aiTerminalTarget
	// requests are the conversations answering, with their windows;
	// convs the conversations open in each window (desktop_ai_convs.go).
	requests map[*desktopAIState]*desktopWindow
	convs    map[*desktopWindow][]*desktopAIState
}

type desktopAIState struct {
	chat        *desktopAIChat
	draft       string
	attach      bool
	useHistory  bool
	preview     string
	previewBusy bool
	previewGen  int
	extra       string
	performance bool
	status      string
	failed      bool
	cancel      context.CancelFunc
	requestFor  config.AISettings
	generation  int
	activeReply *desktopAIMessage
	scroll      ui.ScrollState
	confirm     *aiRunConfirmation
	// run is the question being answered, with the commands it runs.
	run *aiRun
	// model is the model the conversation uses, "" for the default.
	model string
	// autoRun runs the commands that change the system without asking,
	// in this conversation, but for high-risk ones.
	autoRun bool
	// sidebar shows the conversations beside the chat; showContext the
	// whole context to attach instead of its first line.
	sidebar, showContext bool
	// submit sends the draft in the next frame, as Enter asks.
	submit bool
	// deleting is the conversation the user asked to delete, while the
	// window asks first.
	deleting *desktopAIChat
	// inputH is the height the user dragged the question's field to, 0
	// for one that grows with its text.
	inputH float32
	// prompts shows the common tasks (desktop_ai_prompts.go), in the
	// category promptCat ("" for all), found by promptQuery.
	prompts                bool
	promptCat, promptQuery string
	// editing is the question the user is editing in the field: sending
	// replaces it, and all that came after it, with the draft.
	editing *desktopAIMessage
	// retry is the question whose answer failed, which Retry asks again.
	retry *desktopAIMessage
	// copied is the message just copied, whose button shows that it was.
	copied *desktopAIMessage
	// focusInput gives the question's field the keyboard in the next frame.
	focusInput bool
}

type desktopAISelection struct {
	text string
}

type desktopAIChat struct {
	Host  string `json:"host"`
	Title string `json:"title"`
	// Model is the model the conversation last used.
	Model    string              `json:"model,omitempty"`
	Messages []*desktopAIMessage `json:"messages"`
}

// removeMessage takes message out of the chat.
func (chat *desktopAIChat) removeMessage(message *desktopAIMessage) {
	chat.Messages = slices.DeleteFunc(chat.Messages, func(m *desktopAIMessage) bool { return m == message })
}

// trim keeps the chat's last aiMaxMessages messages.
func (chat *desktopAIChat) trim() {
	chat.Messages = chat.Messages[max(0, len(chat.Messages)-aiMaxMessages):]
}

// desktopAIMessage is a message of a chat: the user's question, the
// model's reply, or a command the model ran ("command", its command line
// in Text).
type desktopAIMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
	// Model is the model that wrote a reply.
	Model string `json:"model,omitempty"`
	// A command's reason, status, exit code, time in milliseconds, and
	// whether it ran without asking, being read-only. Its output is not
	// kept.
	Reason string          `json:"reason,omitempty"`
	Status aiCommandStatus `json:"status,omitempty"`
	Exit   *int            `json:"exit,omitempty"`
	Millis int64           `json:"ms,omitempty"`
	Auto   bool            `json:"auto,omitempty"`

	preview string
	private bool
	partial bool
	// A command's call, output, error, whether it is high-risk, what the
	// user typed to confirm one that is, and whether the output shows.
	callID, output, err string
	highRisk            bool
	confirm             string
	showOutput          bool
	// timeout is how long the model let the command run, in seconds, 0
	// for aiCommandTimeout; deadline, while it runs, when it stops.
	timeout  int
	deadline *aiDeadline
}

const (
	aiQuestionLimit = 12 << 10
	aiReplyLimit    = 48 << 10
	aiContextLimit  = 6 << 10
	aiMaxChats      = 24
	aiMaxMessages   = 160
	aiTerminalLines = 20
)

func (a *App) desktopAIOpened(window *desktopWindow, arg any) {
	state, ok := window.state.(*desktopAIState)
	if !ok {
		state = &desktopAIState{attach: a.aiSettings().SendContext, sidebar: true}
		window.state = state
	}
	a.aiLoadChats()
	a.aiCheckReady()
	if selection, ok := arg.(desktopAISelection); ok {
		a.desktopAIStop(state)
		state.draft, state.extra, state.attach = locales.DesktopAIExplainSelectionQuestion(), selection.text, true
	}
	if state.attach {
		a.desktopAIPreview(window.session, state)
	}
}

func (a *App) desktopAIClosing(window *desktopWindow) bool {
	for _, state := range a.aiConvs(window) {
		a.desktopAIStop(state)
	}
	delete(a.ai.convs, window)
	return true
}

func (a *App) aiCheckReady() {
	settings := a.aiSettings()
	if a.ai.readyFor.Equal(settings) {
		return
	}
	a.ai.readyFor, a.ai.ready, a.ai.checking, a.ai.keyError = settings, false, false, ""
	if aiValidate(settings, true) != nil {
		return
	}
	a.ai.checking = true
	a.aiCredential(context.Background(), settings, "", false, func(_ ai.Client, err error) {
		if !a.ai.readyFor.Equal(settings) || !a.aiSettings().Equal(settings) {
			return
		}
		a.ai.ready, a.ai.checking = err == nil, false
		if err != nil {
			a.ai.keyError = err.Error()
		}
	})
}

func aiHost(session *Session) string {
	if session.Host != nil {
		return session.Host.ID
	}
	return ""
}

// desktopAIDelete removes chat from the conversations kept on this
// computer, starting a new one where window had it open. A conversation
// answering a question is not deleted; the others are, while it answers.
func (a *App) desktopAIDelete(window *desktopWindow, chat *desktopAIChat) {
	if chat == nil || a.aiChatBusy(chat) {
		return
	}
	a.ai.chats = slices.DeleteFunc(a.ai.chats, func(candidate *desktopAIChat) bool { return candidate == chat })
	for _, state := range a.aiConvs(window) {
		if state.chat == chat {
			a.desktopAISelect(window.session, state, nil)
		}
	}
	a.aiSaveChats()
}

func (a *App) desktopAIChats(session *Session) []*desktopAIChat {
	host := aiHost(session)
	var chats []*desktopAIChat
	for _, chat := range a.ai.chats {
		if chat.Host == host {
			chats = append(chats, chat)
		}
	}
	return chats
}

func (a *App) desktopAISelect(session *Session, state *desktopAIState, chat *desktopAIChat) {
	a.desktopAIStop(state)
	state.chat, state.draft, state.extra, state.preview, state.status = chat, "", "", "", ""
	// An earlier conversation goes on where it was, with its model.
	state.attach, state.failed, state.performance, state.useHistory = a.aiSettings().SendContext, false, false, chat != nil
	state.model, state.autoRun = "", false
	if chat != nil {
		state.model = chat.Model
	}
	state.scroll.Y = math.MaxFloat32
	a.desktopAIPreview(session, state)
}

// aiModelOf is the model the conversation in state uses: the one chosen,
// while the settings still list it, or the default.
func aiModelOf(settings config.AISettings, state *desktopAIState) string {
	if settings.HasModel(state.model) {
		return state.model
	}
	return settings.Model
}

func (a *App) desktopAIStop(state *desktopAIState) {
	if state.cancel == nil {
		return
	}
	state.cancel()
	state.cancel = nil
	state.generation++
	state.status, state.failed = locales.DesktopAIStopped(), false
	if run := state.run; run != nil {
		for _, card := range run.cards {
			if !card.Status.finished() {
				card.Status = aiCmdCancelled
			}
		}
		state.run = nil
	}
	if state.activeReply != nil {
		state.activeReply.private = true
		state.activeReply.partial = true
		state.activeReply = nil
	}
	a.aiSaveChats()
}

func (a *App) aiStopUnseenChats() {
	for state, window := range a.ai.requests {
		desktop := a.desktopOf(window.session)
		if state.cancel != nil && (a.Locked() || !a.aiSettings().Equal(state.requestFor) || desktop == nil || !slices.Contains(desktop.windows, window)) {
			a.desktopAIStop(state)
		}
		if state.cancel == nil {
			delete(a.ai.requests, state)
		}
	}
}

// desktopAISend asks the question in the field, with the context attached
// if it is; one being edited (state.editing) replaces the question edited.
func (a *App) desktopAISend(window *desktopWindow, state *desktopAIState) {
	attachment := ""
	if state.attach {
		attachment = state.preview
	}
	a.desktopAIAsk(window, state, state.draft, attachment, state.editing, true)
}

// desktopAIRedo asks question again, with the context it was sent with:
// the new answer takes the place of all that came after it.
func (a *App) desktopAIRedo(window *desktopWindow, state *desktopAIState, question *desktopAIMessage) {
	a.desktopAIAsk(window, state, question.Text, question.preview, question, false)
}

// desktopAIAsk asks text, with attachment, in state's chat, in place of
// redo and what came after it when redo is not nil. fromDraft is set for
// the field's question, which empties the field once sent.
func (a *App) desktopAIAsk(window *desktopWindow, state *desktopAIState, text, attachment string, redo *desktopAIMessage, fromDraft bool) {
	settings := a.aiSettings()
	question := strings.TrimSpace(text)
	if window == nil || a.Locked() || state.cancel != nil || state.previewBusy || a.ai.loading || question == "" {
		return
	}
	if err := aiValidate(settings, true); err != nil {
		state.status, state.failed = err.Error(), true
		return
	}
	if len(question) > aiQuestionLimit {
		state.status, state.failed = locales.DesktopAIQuestionTooLong(), true
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.generation++
	generation := state.generation
	state.cancel, state.failed, state.status = cancel, false, locales.AiBusy()
	state.requestFor = settings
	if a.ai.requests == nil {
		a.ai.requests = make(map[*desktopAIState]*desktopWindow)
	}
	a.ai.requests[state] = window
	current := func() bool {
		desktop := a.desktopOf(window.session)
		return state.generation == generation && desktop != nil && slices.Contains(desktop.windows, window)
	}
	a.aiCredential(ctx, settings, "", false, func(client ai.Client, err error) {
		if !current() {
			cancel()
			return
		}
		if err == nil && (!a.aiSettings().Equal(settings) || a.Locked()) {
			err = errors.New(locales.AiSettingsChanged())
		}
		if err != nil {
			cancel()
			state.cancel, state.failed, state.status = nil, true, err.Error()
			return
		}
		if client.Key != "" && (strings.Contains(question, client.Key) || strings.Contains(attachment, client.Key) || strings.Contains(settings.SystemPrompt, client.Key)) {
			cancel()
			state.cancel, state.failed, state.status = nil, true, locales.AiKeyFieldOnly()
			return
		}
		chat := state.chat
		if chat == nil {
			chat = &desktopAIChat{Host: aiHost(window.session), Title: ai.Limit(strings.Join(strings.Fields(client.SafeText(question)), " "), 110)}
			state.chat = chat
		}
		client.Model = aiModelOf(settings, state)
		chat.Model = client.Model
		a.ai.chats = append([]*desktopAIChat{chat}, slices.DeleteFunc(a.ai.chats, func(candidate *desktopAIChat) bool { return candidate == chat })...)
		a.ai.chats = a.ai.chats[:min(aiMaxChats, len(a.ai.chats))]
		// Asked again: the question and what came after it give way, out
		// of the history too.
		if i := slices.Index(chat.Messages, redo); redo != nil && i >= 0 {
			chat.Messages = chat.Messages[:i]
		}
		server := aiServer(window.session)
		if a.ai.noTools[aiToolsKey(client)] {
			server = nil
		}
		prompt := ai.ChatPrompt
		if server != nil {
			prompt = ai.AgentPrompt
		}
		messages := []ai.Message{{Role: "system", Content: prompt}}
		if settings.SystemPrompt != "" {
			messages = append(messages, ai.Message{Role: "system", Content: ai.Limit(settings.SystemPrompt, aiQuestionLimit)})
		}
		if state.useHistory {
			messages = append(messages, aiHistory(client, chat)...)
		}
		content := question
		if attachment != "" {
			content += "\n\n[Untrusted server context]\n" + attachment
		}
		messages = append(messages, ai.Message{Role: "user", Content: content})
		asked := &desktopAIMessage{Role: "user", Text: client.SafeText(question), preview: attachment}
		chat.Messages = append(chat.Messages, asked)
		chat.trim()
		state.editing, state.retry = nil, nil
		if fromDraft {
			state.draft, state.extra, state.preview, state.performance = "", "", "", false
			state.attach, state.useHistory = settings.SendContext, true
		}
		state.scroll.Y = math.MaxFloat32
		state.status = locales.DesktopAIReceiving()
		if fromDraft {
			a.desktopAIPreview(window.session, state)
		}
		run := &aiRun{window: window, state: state, chat: chat, client: client, server: server, ctx: ctx, cancel: cancel,
			generation: generation, messages: messages, private: attachment != "", question: asked}
		state.run = run
		a.aiTurn(run)
	})
}

// aiHistory is the end of the conversation, for a question that goes on
// from it: the questions and replies, and the commands the model ran, but
// none sent with or written from attached context.
func aiHistory(client ai.Client, chat *desktopAIChat) []ai.Message {
	var out []ai.Message
	for _, past := range chat.Messages[max(0, len(chat.Messages)-16):] {
		switch {
		case past.private || past.preview != "" || past.Text == "":
		case past.Role == "command":
			if past.Status == aiCmdDone && past.Exit != nil {
				out = append(out, ai.Message{Role: "assistant", Content: fmt.Sprintf("(Ran on the server: %s, exit code %d.)", ai.Limit(client.SafeText(past.Text), 512), *past.Exit)})
			}
		default:
			out = append(out, ai.Message{Role: past.Role, Content: ai.Limit(client.SafeText(past.Text), 4096)})
		}
	}
	return out
}

func (a *App) aiStream(ctx context.Context, client ai.Client, messages []ai.Message, limit int, update func(string, bool, error)) {
	a.aiStreamTools(ctx, client, messages, nil, limit, func(text string, _ []ai.ToolCall, done bool, err error) { update(text, done, err) })
}

// aiStreamTools streams the model's reply to messages, offering it tools:
// update gets the text so far, masked, and at the end the tools it calls.
func (a *App) aiStreamTools(ctx context.Context, client ai.Client, messages []ai.Message, tools []ai.Tool, limit int, update func(string, []ai.ToolCall, bool, error)) {
	go func() {
		stream, cancel := context.WithCancel(ctx)
		defer cancel()
		var reply strings.Builder
		var last time.Time
		truncated := false
		calls, err := client.StreamTools(stream, messages, tools, func(delta string) {
			remain := limit - reply.Len()
			if len(delta) > remain {
				reply.WriteString(ai.Limit(delta, remain))
				truncated = true
				cancel()
				return
			}
			reply.WriteString(delta)
			if time.Since(last) < 40*time.Millisecond {
				return
			}
			last = time.Now()
			safe := client.SafeText(ai.HidePartialKey(reply.String(), client.Key))
			a.post(func() { update(safe, nil, false, nil) })
		})
		text := reply.String()
		if err != nil || truncated {
			text = ai.HidePartialKey(text, client.Key)
		}
		safe := client.SafeText(text)
		a.post(func() {
			if truncated {
				err, calls = errors.New(locales.DesktopAIReplyTooLong()), nil
			}
			update(safe, calls, true, err)
		})
	}()
}

func aiOS(session *Session) string {
	if session.overview.read && session.overview.client == session.client && session.overview.info.OS != "" {
		return ai.Limit(session.overview.info.OS, 256)
	}
	if session.stats.OK {
		return "Linux"
	}
	return ""
}

func (a *App) desktopAIPreview(session *Session, state *desktopAIState) {
	state.previewGen++
	generation := state.previewGen
	state.preview, state.previewBusy = "", false
	if !state.attach {
		return
	}
	var parts []string
	if name := aiOS(session); name != "" {
		parts = append(parts, locales.DesktopAIContextOS()+": "+name)
	}
	target := a.desktopAITarget(session)
	term := target.term
	dir := ""
	if target.window == nil {
		dir = session.shellDir()
	} else if term != nil {
		dir = term.Dir()
	}
	if path.IsAbs(dir) && !strings.ContainsAny(dir, "\x00\r\n") {
		parts = append(parts, locales.DesktopAIContextDir()+": "+ai.Limit(dir, 512))
	}
	if state.performance && session.stats.OK {
		stats := session.stats
		parts = append(parts, fmt.Sprintf("%s: %.2f / %.2f / %.2f\nCPU: %.1f%%\n%s: %s / %s", locales.DesktopAIContextLoad(), stats.Load[0], stats.Load[1], stats.Load[2], stats.CPU, locales.DesktopAIContextMemory(), formatBytes(float64(stats.MemUsed)), formatBytes(float64(stats.MemTotal))))
		processes := aiStatsByCPU(stats.Procs)
		for _, process := range processes[:min(5, len(processes))] {
			parts = append(parts, fmt.Sprintf("%s: %s · %.1f%% CPU · %s RSS", locales.DesktopAIContextProcess(), ai.Limit(process.Name, 80), process.CPU, formatBytes(float64(process.RSS))))
		}
	}
	selected, selectedLabel := state.extra, locales.DesktopAIContextSelected()
	if selected == "" {
		selected, selectedLabel = aiSelectedLogs(&session.server.logs), locales.DesktopAIContextLogs()
	}
	state.previewBusy = true
	recent := locales.DesktopAIContextTerminal()
	settings := a.aiSettings()
	stateExtra := state.extra
	go func() {
		output := ""
		if term != nil && stateExtra == "" {
			output = aiRecentTerminal(term)
		}
		a.secrets.queue.do(func() {
			key := ""
			if settings.Endpoint != "" {
				if secret, found, err := a.plat.SecretGet(desktopAISecret(settings.Endpoint)); err == nil && found {
					key = secret
				}
			}
			mask := ai.Client{Key: key}.SafeText
			if selected != "" {
				parts = append(parts, selectedLabel+":\n"+ai.Limit(mask(selected), 2048))
			}
			if output != "" {
				parts = append(parts, recent+":\n"+ai.Limit(mask(output), 3072))
			}
			preview := ai.Limit(mask(strings.Join(parts, "\n\n")), aiContextLimit)
			a.post(func() {
				if state.previewGen == generation {
					state.preview, state.previewBusy = preview, false
				}
			})
		})
	}()
}

func aiSelectedLogs(logs *serverLogsPage) string {
	if logs.picked.Len() == 0 {
		return ""
	}
	var out strings.Builder
	for _, line := range logs.shown() {
		if logs.picked.Has(line.id) {
			out.WriteString(line.text)
			out.WriteByte('\n')
			if out.Len() >= 16<<10 {
				break
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func aiRecentTerminal(term *terminal.Terminal) string {
	text := strings.TrimRight(term.Text(), "\r\n ")
	start := len(text)
	for range aiTerminalLines {
		index := strings.LastIndexByte(text[:start], '\n')
		if index < 0 {
			start = -1
			break
		}
		start = index
	}
	if index := strings.LastIndex(text[:start+1], "-----BEGIN "); index >= 0 && !strings.Contains(text[index:start+1], "-----END ") {
		start = index - 1
	}
	return text[start+1:]
}

func aiStatsByCPU(procs []monitor.Proc) []monitor.Proc {
	copy := slices.Clone(procs)
	slices.SortStableFunc(copy, func(left, right monitor.Proc) int {
		switch {
		case left.CPU > right.CPU:
			return -1
		case left.CPU < right.CPU:
			return 1
		}
		return 0
	})
	return copy
}
