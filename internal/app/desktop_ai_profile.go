package app

import (
	"context"
	"time"

	"finallyshell/internal/ai"
	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// The server profile (internal/ai/profile.go): what the server is and
// runs, read once per connection when the AI window opens, or before the
// first question, so that the model's commands fit the server. Each
// conversation keeps the profile it started with (desktopAIChat.Profile),
// and sends it after the system prompts, the same each time, which keeps
// the providers' prompt caches; Refresh in the window reads the server
// again and gives the window's conversations the new one. A question never
// waits for the profile more than aiProfileWait: it goes without it.

const aiProfileTimeout = 10 * time.Second

// aiProfileWait is how long a question waits for a profile being read;
// tests change it.
var aiProfileWait = 4 * time.Second

// readAIProfile reads the server's profile, the text for the model; tests
// replace it.
var readAIProfile = func(ctx context.Context, client *ssh.Client) (string, error) {
	out, _, err := monitor.RunScript(ctx, client, ai.ProfileScript)
	// What it read before a time limit is still worth sending.
	if text := ai.ParseProfile(out).Text(); text != "" {
		return text, nil
	}
	if err == nil {
		err = monitor.ErrScriptOutput
	}
	return "", err
}

// aiProfile is a session's server profile, for the connection it was read
// over.
type aiProfile struct {
	client *ssh.Client
	text   string
	at     time.Time
	busy   bool
	failed bool
	// waiting are the questions that wait for the read under way.
	waiting []func()
}

// aiProfileOn reports whether questions in session's AI windows go with
// its profile: the assistant is set up and sends it, and the session is a
// connected SSH server whose host lets the app run its read-only scripts.
func (a *App) aiProfileOn(session *Session) bool {
	settings := a.aiSettings()
	return settings.Endpoint != "" && !settings.NoServerProfile && aiServer(session) != nil && execAllowed(session.Host)
}

// aiProfileOf is session's profile, nil when none was read over its
// connection.
func (a *App) aiProfileOf(session *Session) *aiProfile {
	if p := a.ai.profiles[session]; p != nil && p.client == session.client {
		return p
	}
	return nil
}

// aiProfileRead reads session's profile, unless it is read for its
// connection already, or being read; refresh reads it again. done, if not
// nil, gets the result.
func (a *App) aiProfileRead(session *Session, refresh bool, done func(*aiProfile)) {
	if !a.aiProfileOn(session) {
		return
	}
	for other, p := range a.ai.profiles {
		if p.client != other.client {
			delete(a.ai.profiles, other) // reconnected, or closed
		}
	}
	p := a.aiProfileOf(session)
	if p != nil && (p.busy || !refresh) {
		return
	}
	if p == nil {
		p = &aiProfile{client: session.client}
		if a.ai.profiles == nil {
			a.ai.profiles = map[*Session]*aiProfile{}
		}
		a.ai.profiles[session] = p
	}
	p.busy, p.failed = true, false
	client, read := p.client, readAIProfile
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), aiProfileTimeout)
		text, err := read(ctx, client)
		cancel()
		a.post(func() {
			p.busy, p.failed = false, err != nil || text == ""
			if !p.failed {
				p.text, p.at = text, time.Now()
			}
			waiting := p.waiting
			p.waiting = nil
			for _, wake := range waiting {
				wake()
			}
			if done != nil {
				done(p)
			}
		})
	}()
}

// aiProfileReady calls next, on the UI goroutine, once session's profile
// is read: at once unless it is being read, else when the read ends or
// after aiProfileWait, whichever comes first.
func (a *App) aiProfileReady(session *Session, next func()) {
	p := a.aiProfileOf(session)
	if p == nil || !p.busy {
		next()
		return
	}
	called := false
	once := func() {
		if !called {
			called = true
			next()
		}
	}
	p.waiting = append(p.waiting, once)
	time.AfterFunc(aiProfileWait, func() { a.post(once) })
}

// aiProfileMessage is the profile message a question in chat sends: the
// one the conversation started with, else the session's now, which the
// conversation keeps from then on; "" when the profile is off or unknown.
func (a *App) aiProfileMessage(session *Session, chat *desktopAIChat) string {
	if !a.aiProfileOn(session) {
		return ""
	}
	if p := a.aiProfileOf(session); chat.Profile == "" && p != nil && p.text != "" {
		chat.Profile = ai.ProfileMessage(p.text, p.at)
	}
	return chat.Profile
}

// aiProfileRefresh reads window's server again, and gives the
// conversations open in the window the new profile.
func (a *App) aiProfileRefresh(window *desktopWindow) {
	a.aiProfileRead(window.session, true, func(p *aiProfile) {
		if p.failed {
			return
		}
		message := ai.ProfileMessage(p.text, p.at)
		for _, state := range a.aiConvs(window) {
			if state.chat != nil && state.chat.Profile != message {
				state.chat.Profile = message
				a.aiSaveChats()
			}
		}
	})
}

// desktopAIProfileChip is the top bar's Server profile button, its
// popover showing the profile the conversation sends, with Refresh.
func (a *App) desktopAIProfileChip(c *ui.Context, window *desktopWindow, state *desktopAIState) {
	session := window.session
	if !a.aiProfileOn(session) {
		return
	}
	p := a.aiProfileOf(session)
	icon, tint := "info", theme.TextMuted
	switch {
	case p != nil && p.busy:
		icon = "refresh-cw"
	case p != nil && p.failed && p.text == "":
		icon, tint = "triangle-alert", theme.Warning
	}
	chip := ui.Row(c.Key("profile")).Gap(4).AlignItems(ui.Center).Padding(3, 8).Radius(6).Border(1, theme.Border).Background(colors.card).
		Shrink(1).MinWidth(0).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.AiProfileChip())
	if !state.profileOpen {
		chip.Tooltip(locales.AiProfileTip())
	}
	if chip.Clicked() {
		state.profileOpen = !state.profileOpen
	}
	chip.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(tint).Shrink(0)
		ui.Text(c, locales.AiProfileChip()).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
	})
	ui.Popover(c, chip, &state.profileOpen, func() {
		ui.Column(c).Width(420).Padding(8, 10, 10, 10).Gap(6).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, locales.AiProfileChip()).Bold().Grow(1)
				busy := p != nil && p.busy
				if iconTextButton(c, "refresh-cw", locales.AiProfileRefresh()).Disabled(busy).Clicked() && !busy {
					a.aiProfileRefresh(window)
				}
			})
			// The profile the conversation sends, or will send.
			text, latest := "", ""
			if p != nil && p.text != "" {
				latest = ai.ProfileMessage(p.text, p.at)
			}
			if text = latest; state.chat != nil && state.chat.Profile != "" {
				text = state.chat.Profile
			}
			switch {
			case p != nil && p.busy:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Label(locales.AiProfileReading())
					widgets.Muted(c, locales.AiProfileReading())
				})
			case p != nil && p.failed:
				ui.Text(c, locales.AiProfileFailed()).TextColor(theme.Danger)
			}
			if text != "" {
				if text == latest {
					widgets.Muted(c, locales.AiProfileCollected(p.at.Format("2006-01-02 15:04"))).FontSize(12)
				}
				ui.Scroll(c).MaxHeight(300).FillWidth().Children(func() {
					ui.Text(c, text).FontSize(12).Selectable().MaxWidthPercent(100)
				})
				widgets.Muted(c, locales.AiProfileNote()).FontSize(11)
			}
		})
	})
}
