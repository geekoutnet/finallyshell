package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/locales"

	"golang.org/x/crypto/ssh"
)

// The AI assistant's agent: the model runs commands on the server with the
// run_command tool (internal/ai/agent.go), each on an SSH channel of its
// own, apart from the terminal. Read-only commands run at once; the others
// wait for the user to allow them, unless the conversation runs everything
// automatically, which high-risk commands still ask for.

const (
	// aiOutputLimit is how much of a command's output the model and the
	// window get: its start and its end.
	aiOutputLimit = 16 << 10
	// aiMaxSteps is how many rounds of commands one question may run.
	aiMaxSteps = 12
)

// aiCommandTimeout is how long a command may run; tests shorten it.
var aiCommandTimeout = 60 * time.Second

// runAICommand runs the agent's commands; tests replace it.
var runAICommand = aiRunCommand

// aiCommandStatus is where a command the model called is.
type aiCommandStatus int

const (
	aiCmdWaiting   aiCommandStatus = iota // for the user's approval
	aiCmdQueued                           // to run when its turn comes
	aiCmdRunning                          // on the server
	aiCmdDone                             // with an exit code
	aiCmdTimedOut                         // stopped after aiCommandTimeout
	aiCmdRefused                          // by the user
	aiCmdCancelled                        // with the request
	aiCmdFailed                           // could not run
)

// finished reports whether the command ran or will not.
func (s aiCommandStatus) finished() bool { return s >= aiCmdDone }

// aiRun is a question being answered: the model's replies and the
// commands it runs, round after round, until it answers without one.
type aiRun struct {
	window     *desktopWindow
	state      *desktopAIState
	chat       *desktopAIChat
	client     ai.Client
	server     *ssh.Client // nil: the model gets no tools
	ctx        context.Context
	cancel     context.CancelFunc
	generation int
	// messages are the request's, growing with each round.
	messages []ai.Message
	steps    int
	// private keeps the replies out of the saved history, as they may
	// quote the attached context.
	private bool
	// cards are this round's commands, in the order the model called them.
	cards []*desktopAIMessage
}

// current reports whether the run is still the window's, and the window
// still open.
func (a *App) aiRunCurrent(run *aiRun) bool {
	desktop := a.desktopOf(run.window.session)
	return run.state.run == run && run.state.generation == run.generation && desktop != nil && slices.Contains(desktop.windows, run.window) && !a.Locked()
}

// aiServer is the SSH connection the assistant runs commands on, or nil
// where there is none.
func aiServer(session *Session) *ssh.Client {
	if session == nil || session.isLocal() || session.isDeviceSession() || session.status != statusConnected {
		return nil
	}
	return session.client
}

// aiToolsKey names the model client calls, for the models that take no
// tools.
func aiToolsKey(client ai.Client) string { return client.Endpoint + "\x00" + client.Model }

// aiToolsRefused reports whether a request failed for offering tools: the
// providers that do not take them answer it as a bad request.
func aiToolsRefused(err error) bool {
	text := err.Error()
	return strings.HasPrefix(text, "HTTP 400") || strings.HasPrefix(text, "HTTP 422") || strings.HasPrefix(text, "HTTP 501")
}

// aiTurn asks the model for its next reply in run.
func (a *App) aiTurn(run *aiRun) {
	state := run.state
	reply := &desktopAIMessage{Role: "assistant", Model: run.client.Model, private: true}
	run.chat.Messages = append(run.chat.Messages, reply)
	state.activeReply = reply
	var tools []ai.Tool
	if run.server != nil {
		tools = []ai.Tool{ai.RunCommandTool}
	}
	a.aiStreamTools(run.ctx, run.client, run.messages, tools, aiReplyLimit, func(text string, calls []ai.ToolCall, done bool, err error) {
		if !a.aiRunCurrent(run) {
			reply.partial = true
			return
		}
		reply.Text = text
		if state.scroll.Y >= state.scroll.MaxY-8 {
			state.scroll.Y = math.MaxFloat32
		}
		if !done {
			return
		}
		state.activeReply = nil
		reply.partial = err != nil
		reply.private = run.private || err != nil
		if text == "" {
			run.chat.removeMessage(reply)
		}
		switch {
		case err != nil && len(tools) > 0 && run.steps == 0 && text == "" && aiToolsRefused(err):
			// The model or the provider takes no tools: the question again,
			// for advice alone, as for every question to it from now on.
			if a.ai.noTools == nil {
				a.ai.noTools = map[string]bool{}
			}
			a.ai.noTools[aiToolsKey(run.client)] = true
			run.server, run.messages[0].Content = nil, ai.ChatPrompt
			state.status = locales.DesktopAINoTools()
			a.aiTurn(run)
		case err != nil:
			a.aiFinish(run, locales.AiRequestError()+"\n"+err.Error(), true)
		case len(calls) == 0 && text == "":
			a.aiFinish(run, locales.AiEmptyReply(), true)
		case len(calls) == 0:
			a.aiFinish(run, "", false)
		default:
			run.messages = append(run.messages, ai.Message{Role: "assistant", Content: text, ToolCalls: calls})
			run.steps++
			a.aiCommands(run, calls)
			a.aiSaveChats()
			a.aiNext(run)
		}
	})
}

// aiCommands adds the commands the model called to the chat, read-only
// ones and, when the conversation runs everything, the others that are
// not high-risk queued to run, the rest waiting for the user.
func (a *App) aiCommands(run *aiRun, calls []ai.ToolCall) {
	run.cards = run.cards[:0]
	for _, call := range calls {
		card := &desktopAIMessage{Role: "command", callID: call.ID}
		args, err := ai.ParseCommandCall(call)
		card.Text, card.Reason = args.Command, args.Reason
		switch {
		case err != nil:
			card.Status, card.err = aiCmdFailed, err.Error()
			if card.Text == "" {
				card.Text = ai.Limit(call.Function.Name, 64)
			}
		case run.client.Key != "" && strings.Contains(args.Command, run.client.Key):
			card.Status, card.err, card.Text = aiCmdFailed, locales.AiKeyFieldOnly(), run.client.SafeText(args.Command)
		case ai.ReadOnly(args.Command):
			card.Status, card.Auto = aiCmdQueued, true
		case run.state.autoRun && !ai.HighRisk(args.Command):
			card.Status = aiCmdQueued
		default:
			card.Status, card.highRisk = aiCmdWaiting, ai.HighRisk(args.Command)
		}
		run.chat.Messages = append(run.chat.Messages, card)
		run.cards = append(run.cards, card)
	}
	run.chat.trim()
	run.state.scroll.Y = math.MaxFloat32
}

// aiNext runs the round's next command, or waits for the user's answer to
// one; with every command of the round finished, it sends their results to
// the model for its next reply.
func (a *App) aiNext(run *aiRun) {
	if !a.aiRunCurrent(run) {
		return
	}
	state := run.state
	for _, card := range run.cards {
		switch card.Status {
		case aiCmdWaiting:
			state.status, state.failed = locales.DesktopAICmdWaiting(), false
			return
		case aiCmdQueued:
			a.aiExec(run, card)
			return
		case aiCmdRunning:
			return
		}
	}
	for _, card := range run.cards {
		run.messages = append(run.messages, ai.Message{Role: "tool", ToolCallID: card.callID, Content: card.toolResult()})
	}
	run.cards = run.cards[:0]
	if run.steps >= aiMaxSteps {
		a.aiFinish(run, locales.DesktopAITooManySteps(strconv.Itoa(aiMaxSteps)), false)
		return
	}
	state.status, state.failed = locales.DesktopAIReceiving(), false
	a.aiTurn(run)
}

// toolResult is what the model learns of the command.
func (card *desktopAIMessage) toolResult() string {
	switch card.Status {
	case aiCmdDone:
		return ai.CommandResult(*card.Exit, card.output, false)
	case aiCmdTimedOut:
		return ai.CommandResult(-1, card.output, true)
	case aiCmdRefused:
		return "The user refused to run this command. Do not run it another way; ask them what they want instead."
	default:
		return "The command could not run: " + card.err
	}
}

// aiAnswer is the user allowing or refusing a command that waits.
func (a *App) aiAnswer(state *desktopAIState, card *desktopAIMessage, allow bool) {
	run := state.run
	if run == nil || card.Status != aiCmdWaiting || allow && card.highRisk && card.confirm != "RUN" {
		return
	}
	card.Status = aiCmdRefused
	if allow {
		card.Status = aiCmdQueued
	}
	a.aiNext(run)
}

// aiExec runs card's command on the server.
func (a *App) aiExec(run *aiRun, card *desktopAIMessage) {
	card.Status = aiCmdRunning
	run.state.status, run.state.failed = locales.DesktopAICmdRunning(), false
	if !card.Auto {
		a.desktopLog(run.window.session, appAI, locales.DesktopAIAgentRan(ai.Limit(card.Text, 200)), nil)
	}
	start, command, server, ctx := time.Now(), card.Text, run.server, run.ctx
	go func() {
		exit, output, timedOut, err := runAICommand(ctx, server, command)
		a.post(func() {
			if !a.aiRunCurrent(run) || card.Status != aiCmdRunning {
				return
			}
			card.Millis = time.Since(start).Milliseconds()
			card.output = run.client.SafeText(output)
			switch {
			case err != nil:
				card.Status, card.err = aiCmdFailed, err.Error()
			case timedOut:
				card.Status = aiCmdTimedOut
			default:
				card.Status, card.Exit = aiCmdDone, &exit
			}
			if run.state.scroll.Y >= run.state.scroll.MaxY-8 {
				run.state.scroll.Y = math.MaxFloat32
			}
			a.aiSaveChats()
			a.aiNext(run)
		})
	}()
}

// aiFinish ends run, with a status line for the window.
func (a *App) aiFinish(run *aiRun, status string, failed bool) {
	state := run.state
	run.cancel()
	state.run, state.cancel, state.activeReply = nil, nil, nil
	state.status, state.failed = status, failed
	a.aiSaveChats()
}

// aiRunCommand runs command on server, with no terminal and no input,
// stopping it after aiCommandTimeout or when ctx ends. It returns its exit
// code and its output, stdout and stderr together, cut to aiOutputLimit.
func aiRunCommand(ctx context.Context, server *ssh.Client, command string) (exit int, output string, timedOut bool, err error) {
	if server == nil {
		return 0, "", false, errors.New("not connected")
	}
	session, err := server.NewSession()
	if err != nil {
		return 0, "", false, err
	}
	defer session.Close()
	var out aiOutput
	session.Stdout, session.Stderr = &out, &out
	limit, cancel := context.WithTimeout(ctx, aiCommandTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err = <-done:
	case <-limit.Done():
		// The server stops the command if it honors signals; closing the
		// channel ends the wait either way.
		_ = session.Signal(ssh.SIGKILL)
		session.Close()
		<-done
		if ctx.Err() != nil {
			return 0, out.String(), false, ctx.Err()
		}
		return 0, out.String(), true, nil
	}
	var exitErr *ssh.ExitError
	switch {
	case err == nil:
		return 0, out.String(), false, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitStatus(), out.String(), false, nil
	default:
		return 0, out.String(), false, err
	}
}

// aiOutput keeps the start and the end of a command's output, as it
// comes on stdout and stderr at once.
type aiOutput struct {
	mu         sync.Mutex
	head, tail []byte
	total      int
}

const (
	aiOutputHead = aiOutputLimit * 3 / 8
	aiOutputTail = aiOutputLimit - aiOutputHead
)

func (o *aiOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	o.total += n
	if room := aiOutputHead - len(o.head); room > 0 {
		k := min(room, len(p))
		o.head, p = append(o.head, p[:k]...), p[k:]
	}
	o.tail = append(o.tail, p...)
	if len(o.tail) > 4*aiOutputTail {
		o.tail = append([]byte(nil), o.tail[len(o.tail)-aiOutputTail:]...)
	}
	return n, nil
}

func (o *aiOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	tail := o.tail
	if len(tail) > aiOutputTail {
		tail = tail[len(tail)-aiOutputTail:]
	}
	text := string(o.head) + string(tail)
	if cut := o.total - len(o.head) - len(tail); cut > 0 {
		text = string(o.head) + fmt.Sprintf("\n[… %d bytes left out …]\n", cut) + string(tail)
	}
	return strings.ToValidUTF8(text, "�")
}
