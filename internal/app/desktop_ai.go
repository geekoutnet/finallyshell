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

	loaded, loading, saved bool
	disk                   secretQueue
	chats                  []*desktopAIChat
	storeError             bool

	command  *aiCommandState
	targets  map[*Session]aiTerminalTarget
	requests map[*desktopWindow]*desktopAIState
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
}

type desktopAISelection struct {
	text string
}

type desktopAIChat struct {
	Host     string              `json:"host"`
	Title    string              `json:"title"`
	Messages []*desktopAIMessage `json:"messages"`
}

type desktopAIMessage struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	preview string
	private bool
	partial bool
}

const (
	aiQuestionLimit = 12 << 10
	aiReplyLimit    = 48 << 10
	aiContextLimit  = 6 << 10
	aiMaxChats      = 24
	aiMaxMessages   = 48
	aiTerminalLines = 20
)

func (a *App) desktopAIOpened(window *desktopWindow, arg any) {
	state, ok := window.state.(*desktopAIState)
	if !ok {
		state = &desktopAIState{attach: a.aiSettings().SendContext}
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
	if state, ok := window.state.(*desktopAIState); ok {
		a.desktopAIStop(state)
	}
	return true
}

func (a *App) aiCheckReady() {
	settings := a.aiSettings()
	if a.ai.readyFor == settings {
		return
	}
	a.ai.readyFor, a.ai.ready, a.ai.checking, a.ai.keyError = settings, false, false, ""
	if aiValidate(settings, true) != nil {
		return
	}
	a.ai.checking = true
	a.aiCredential(context.Background(), settings, "", false, func(_ ai.Client, err error) {
		if a.ai.readyFor != settings || a.aiSettings() != settings {
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
	state.attach, state.failed, state.performance, state.useHistory = a.aiSettings().SendContext, false, false, false
	state.scroll.Y = math.MaxFloat32
	a.desktopAIPreview(session, state)
}

func (a *App) desktopAIStop(state *desktopAIState) {
	if state.cancel == nil {
		return
	}
	state.cancel()
	state.cancel = nil
	state.generation++
	state.status, state.failed = locales.DesktopAIStopped(), false
	if state.activeReply != nil {
		state.activeReply.private = true
		state.activeReply.partial = true
		state.activeReply = nil
		a.aiSaveChats()
	}
}

func (a *App) aiStopUnseenChats() {
	for window, state := range a.ai.requests {
		desktop := a.desktopOf(window.session)
		if state.cancel != nil && (a.Locked() || a.aiSettings() != state.requestFor || desktop == nil || !slices.Contains(desktop.windows, window)) {
			a.desktopAIStop(state)
		}
		if state.cancel == nil {
			delete(a.ai.requests, window)
		}
	}
}

func (a *App) desktopAISend(window *desktopWindow, state *desktopAIState) {
	settings := a.aiSettings()
	question := strings.TrimSpace(state.draft)
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
	attachment := ""
	if state.attach {
		attachment = state.preview
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.generation++
	generation := state.generation
	state.cancel, state.failed, state.status = cancel, false, locales.AiBusy()
	state.requestFor = settings
	if a.ai.requests == nil {
		a.ai.requests = make(map[*desktopWindow]*desktopAIState)
	}
	a.ai.requests[window] = state
	current := func() bool {
		desktop := a.desktopOf(window.session)
		return state.generation == generation && desktop != nil && slices.Contains(desktop.windows, window)
	}
	a.aiCredential(ctx, settings, "", false, func(client ai.Client, err error) {
		if !current() {
			cancel()
			return
		}
		if err == nil && (a.aiSettings() != settings || a.Locked()) {
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
		a.ai.chats = append([]*desktopAIChat{chat}, slices.DeleteFunc(a.ai.chats, func(candidate *desktopAIChat) bool { return candidate == chat })...)
		a.ai.chats = a.ai.chats[:min(aiMaxChats, len(a.ai.chats))]
		messages := []ai.Message{{Role: "system", Content: ai.ChatPrompt}}
		if settings.SystemPrompt != "" {
			messages = append(messages, ai.Message{Role: "system", Content: ai.Limit(settings.SystemPrompt, aiQuestionLimit)})
		}
		if state.useHistory {
			for _, past := range chat.Messages[max(0, len(chat.Messages)-8):] {
				if !past.private && past.Text != "" && past.preview == "" {
					messages = append(messages, ai.Message{Role: past.Role, Content: ai.Limit(client.SafeText(past.Text), 4096)})
				}
			}
		}
		content := question
		if attachment != "" {
			content += "\n\n[Untrusted server context]\n" + attachment
		}
		messages = append(messages, ai.Message{Role: "user", Content: content})
		reply := &desktopAIMessage{Role: "assistant", private: true}
		chat.Messages = append(chat.Messages[max(0, len(chat.Messages)-aiMaxMessages+2):],
			&desktopAIMessage{Role: "user", Text: client.SafeText(question), preview: attachment}, reply)
		state.activeReply = reply
		state.draft, state.extra, state.preview, state.performance = "", "", "", false
		state.attach = settings.SendContext
		state.scroll.Y = math.MaxFloat32
		state.status = locales.DesktopAIReceiving()
		a.desktopAIPreview(window.session, state)
		a.aiStream(ctx, client, messages, aiReplyLimit, func(text string, done bool, err error) {
			if !current() || a.Locked() {
				cancel()
				if state.generation == generation {
					state.cancel, state.activeReply = nil, nil
					reply.partial = true
				}
				return
			}
			reply.Text = text
			if state.scroll.Y >= state.scroll.MaxY-8 {
				state.scroll.Y = math.MaxFloat32
			}
			if done {
				cancel()
				state.cancel, state.activeReply = nil, nil
				state.status, state.failed = "", err != nil || text == ""
				reply.private = attachment != "" || err != nil
				reply.partial = err != nil
				if err != nil {
					reply.private = true
					state.status = locales.AiRequestError() + "\n" + err.Error()
				} else if text == "" {
					state.status = locales.AiEmptyReply()
				}
				a.aiSaveChats()
			}
		})
	})
}

func (a *App) aiStream(ctx context.Context, client ai.Client, messages []ai.Message, limit int, update func(string, bool, error)) {
	go func() {
		stream, cancel := context.WithCancel(ctx)
		defer cancel()
		var reply strings.Builder
		var last time.Time
		truncated := false
		err := client.Stream(stream, messages, func(delta string) {
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
			a.post(func() { update(safe, false, nil) })
		})
		text := reply.String()
		if err != nil || truncated {
			text = ai.HidePartialKey(text, client.Key)
		}
		safe := client.SafeText(text)
		a.post(func() {
			if truncated {
				err = errors.New(locales.DesktopAIReplyTooLong())
			}
			update(safe, true, err)
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
