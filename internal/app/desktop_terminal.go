package app

import (
	"context"
	"path"
	"strings"
	"unicode"

	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

type desktopTerminalState struct {
	pane                 *pane
	life                 context.Context
	stop                 context.CancelFunc
	dir                  string
	command              string // typed once the shell is in dir
	issue                func() string
	starting, confirming bool
	focusZ               int
	fontDelta            float32
}

func desktopTerminalDirectory(dir string) bool {
	return path.IsAbs(dir) && strings.IndexFunc(dir, unicode.IsControl) < 0
}

func desktopTerminalCD(dir string) string {
	if !desktopTerminalDirectory(dir) {
		return ""
	}
	return "cd " + relayQuote("--", dir) + " && clear\r"
}

func (a *App) desktopTerminalOpened(w *desktopWindow, arg any) {
	a.desktopToolsNoteOpened(w)
	dir, _ := arg.(string)
	state := &desktopTerminalState{dir: dir}
	if run, ok := arg.(desktopTerminalRun); ok {
		state.dir, state.command = run.dir, run.command
	}
	w.state = state
	if dir != "" && !desktopTerminalDirectory(dir) {
		state.dir, state.issue = "", locales.DesktopToolsInvalidDirectory
		return
	}
	a.desktopTerminalStart(w, state)
}

func (a *App) desktopTerminalStart(w *desktopWindow, state *desktopTerminalState) {
	s := w.session
	if s.client == nil || s.status != statusConnected || state.starting || state.pane != nil {
		return
	}
	ctx, stop := a.desktopToolsContext(w)
	state.life, state.stop, state.starting, state.issue = ctx, stop, true, nil
	client, spec, pane := s.client, a.shellSpec(s.Host), newPane()
	spec.life = ctx
	command := desktopTerminalCD(state.dir)
	command = desktopTerminalCommand(command, state)
	go func() {
		s.shellOpen.Lock()
		defer s.shellOpen.Unlock()
		var term *terminal.Terminal
		err := ctx.Err()
		if err == nil {
			term, err = a.openPaneShell(s, pane, client, spec)
		}
		if term != nil && command != "" && ctx.Err() == nil {
			term.Send([]byte(command))
		}
		a.post(func() {
			if state.life != ctx {
				return
			}
			if ctx.Err() != nil || !a.desktopToolsLive(w) || s.client != client || s.status != statusConnected {
				state.starting, state.stop, state.life = false, nil, nil
				stop()
				return
			}
			state.starting = false
			if err != nil {
				stop()
				state.stop, state.life = nil, nil
				state.issue = func() string { return paneError(err) }
				return
			}
			state.pane, pane.term = pane, term
			s.panes.opened(pane)
			a.updateTerminal(term, spec.topts)
			a.applyMouse(term)
			a.desktopTerminalFont(state)
			a.startShellDir(ctx, s, pane, client, term, spec.exec)
		})
		if term != nil {
			go func() {
				select {
				case <-ctx.Done():
				case <-term.Done():
				}
				term.Close()
				a.post(func() {
					s.panes.order = deletePane(s.panes.order, pane)
					s.panes.renumber()
					if state.life == ctx {
						state.pane, state.life, state.stop, state.starting, state.focusZ = nil, nil, nil, false, 0
					}
					stop()
				})
			}()
		}
	}()
}

func (a *App) desktopTerminalClosing(w *desktopWindow) bool {
	state := w.state.(*desktopTerminalState)
	if state.pane != nil {
		state.confirming = true
		return false
	}
	if state.stop != nil {
		state.stop()
	}
	return true
}

func (a *App) desktopTerminalFont(state *desktopTerminalState) {
	font := a.termFont
	font.Size = max(8, min(36, font.Size+state.fontDelta))
	state.pane.term.SetFont(font)
}

func (a *App) desktopTerminalView(c *ui.Context, w *desktopWindow) {
	s, state := w.session, w.state.(*desktopTerminalState)
	if pane := state.pane; pane != nil {
		dir := pane.term.Dir()
		if dir == "" {
			dir = pane.shell.dir
		}
		if desktopTerminalDirectory(dir) {
			state.dir = dir
		}
	}
	w.title = s.Host.User + "@" + s.label()
	if state.dir != "" {
		w.title += ": " + state.dir
	}
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		area := a.terminalBackdrop(c, 0).Grow(1).MinHeight(0).FillWidth().Padding(5, 6)
		area.ObserveInput(func(event ui.InputEvent) {
			if event.Kind == ui.InputPointerDown && a.desktopOf(s).focused != w {
				a.desktopFocus(w)
			}
		})
		area.Children(func() { a.desktopTerminalBody(c, w, state) })
		a.desktopTerminalToolbar(c, w, state)
	})
	if state.confirming {
		ui.Modal(c, &state.confirming, func() {
			ui.Column(c).Width(355).Gap(12).Children(func() {
				ui.Text(c, locales.DesktopToolsCloseShell()).Bold()
				ui.Text(c, w.title).SingleLine().Ellipsis("…").TextColor(theme.TextMuted)
				ui.Text(c, locales.DesktopToolsCloseShellNote())
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						state.confirming = false
					}
					if ui.PrimaryButton(c, locales.DesktopToolsCloseShell()).Clicked() {
						state.confirming = false
						if state.stop != nil {
							state.stop()
						}
						a.desktopClose(w)
					}
				})
			})
		})
	}
}

func (a *App) desktopTerminalBody(c *ui.Context, w *desktopWindow, state *desktopTerminalState) {
	if pane := state.pane; pane != nil {
		focused := a.desktopOf(w.session).focused == w
		view := terminal.View(c.Key(pane.id), pane.term).Fill().Label(locales.DesktopToolsTerminalWorkspace())
		if focused && state.focusZ != w.z {
			view.Focus()
		}
		if focused {
			state.focusZ = w.z
			if find := a.shortcut(scFind); !find.none() && view.Shortcut(find.Mods, find.Key) {
				pane.find.open = true
				pane.find.opened++
			}
			a.findBar(c, pane, view)
			a.desktopAITerminal(c, w.session, w, pane.term, view)
		} else {
			state.focusZ = 0
			view.HandleInput(func(ev ui.InputEvent) bool { return true })
		}
		return
	}
	ui.Column(c).Fill().Center().Padding(12).Gap(10).Children(func() {
		if state.starting {
			ui.Spinner(c).Label(locales.DesktopToolsShellOpening()).TextColor(a.termColors().Foreground)
		}
		message := locales.DesktopToolsShellEnded()
		switch {
		case state.issue != nil:
			message = state.issue()
		case state.starting:
			message = locales.DesktopToolsShellOpening()
		case w.session.client == nil || w.session.status != statusConnected:
			message = locales.NotConnected()
		}
		ui.Text(c, message).TextAlign(ui.Center).TextColor(a.termColors().Foreground)
	})
}

func (a *App) desktopTerminalToolbar(c *ui.Context, w *desktopWindow, state *desktopTerminalState) {
	pane := state.pane
	buttons := []struct {
		icon, label string
		enabled     bool
		run         func()
	}{
		{"folder-open", locales.DesktopToolsOpenFolder(), state.dir != "", func() { a.desktopOpen(w.session, appFiles, state.dir) }},
		{"refresh-cw", locales.Reconnect(), pane == nil && !state.starting && w.session.status == statusConnected && w.session.client != nil, func() { a.desktopTerminalStart(w, state) }},
		{"minus", locales.DesktopToolsFontSmaller(), pane != nil && a.termFont.Size+state.fontDelta > 8, func() { state.fontDelta--; a.desktopTerminalFont(state) }},
		{"plus", locales.DesktopToolsFontLarger(), pane != nil && a.termFont.Size+state.fontDelta < 36, func() { state.fontDelta++; a.desktopTerminalFont(state) }},
		{"copy", locales.DesktopToolsCopyScreen(), pane != nil, func() { c.WriteClipboard(pane.term.Text()) }},
		{"clipboard-paste", locales.ScTerminalPaste(), pane != nil, func() { pane.term.Paste(c.ReadClipboard()) }},
		{"search", locales.Find(), pane != nil, func() { pane.find.open = true; pane.find.opened++; c.Invalidate() }},
	}
	bar := ui.Row(c).FillWidth().Height(34).Shrink(0).Gap(5).PaddingX(8).AlignItems(ui.Center).Background(colors.sidebar)
	bar.ContextMenu(func(menu *ui.Menu) {
		for _, button := range buttons {
			if menu.Item(button.label).Disabled(!button.enabled).Chosen() {
				button.run()
			}
		}
	})
	bar.Children(func() {
		for _, button := range buttons {
			if widgets.IconButton(c, button.icon, button.label).KeepFocus().Disabled(!button.enabled).Clicked() {
				button.run()
			}
		}
		ui.Text(c, state.dir).Grow(1).MinWidth(0).SingleLine().Ellipsis("…").FontSize(11).TextColor(theme.TextMuted)
	})
}

// desktopTerminalRun opens a terminal that runs command in dir.
type desktopTerminalRun struct{ dir, command string }

// desktopTerminalCommand is what a new terminal types: cd, the cd into
// its folder, then its command, which never runs elsewhere than in the
// folder asked.
func desktopTerminalCommand(cd string, state *desktopTerminalState) string {
	switch {
	case state.command == "":
		return cd
	case cd != "":
		return strings.TrimSuffix(cd, "\r") + " && " + state.command + "\r"
	case state.dir == "":
		return state.command + "\r"
	}
	return ""
}
