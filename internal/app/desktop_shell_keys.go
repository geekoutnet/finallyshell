package app

import (
	"slices"

	"finallyshell/internal/config"
	"github.com/egoist/mygo/ui"
)

func desktopShortcutAction(id shortcutID, title func() string, field func(*config.Shortcuts) *string, other, mac ui.Key) shortcutAction {
	return shortcutAction{id: id, title: title, field: field, mac: keyCombo{ui.Super | ui.Shift, mac}, other: keyCombo{ui.Ctrl | ui.Shift, other}}
}

func (a *App) desktopKeys(c *ui.Context, d *desktopState, pressed func(shortcutID) bool) bool {
	sh := &d.shell
	w := d.focused
	if w == nil {
		w = d.topWindow()
	}
	switch {
	case (sh.launcher || sh.folder != "" && sh.rename == "" && sh.deleteFolder == "") && c.Shortcut(0, ui.KeyEscape):
		if sh.launcher {
			sh.launcher = false
		} else {
			sh.folder = ""
		}
	case pressed(scDesktopLauncher):
		a.desktopToggleLauncher(d)
	case pressed(scDesktopShow):
		a.desktopShow(d)
	case len(d.windows) > 0 && pressed(scDesktopCycle):
		a.desktopCycle(d)
	case w != nil && pressed(scDesktopClose):
		a.desktopRequestClose(w)
	case w != nil && pressed(scDesktopMinimize):
		a.desktopMinimize(d, w)
	case w != nil && pressed(scDesktopMaximize):
		a.desktopToggleMaximize(d, w)
	case w != nil && pressed(scDesktopSnapLeft):
		a.desktopSetWindowMode(d, w, "left")
	case w != nil && pressed(scDesktopSnapRight):
		a.desktopSetWindowMode(d, w, "right")
	default:
		return false
	}
	return true
}

func (a *App) desktopCycle(d *desktopState) {
	order := d.shell.cycle
	if len(order) != len(d.windows) || slices.ContainsFunc(order, func(w *desktopWindow) bool { return !slices.Contains(d.windows, w) }) {
		order = slices.Clone(d.windows)
		slices.SortFunc(order, func(first, second *desktopWindow) int { return desktopWindowOrder(second, first) })
	}
	idx := slices.Index(order, d.focused)
	a.desktopFocus(order[(idx+1)%len(order)])
	d.shell.cycle = order
}

func (a *App) desktopFromHost(m *managerState, h *config.Host) {
	if !h.IsSSH() || h.Deleted() {
		return
	}
	if m.win == WindowManager {
		if !a.settings.KeepManagerOpen {
			a.closeWindow(WindowManager)
		}
		a.plat.FocusWindow(WindowMain)
	}
	for _, s := range a.sessions {
		if canOpenDesktop(s) && s.Host.ID == h.ID && (s.status == statusConnected || s.status == statusConnecting) {
			a.openDesktop(s)
			a.redraw()
			return
		}
	}
	a.openSessionThen(a.selectedNewTab(), h, func(s *Session) { a.openDesktop(s); a.redraw() })
}
