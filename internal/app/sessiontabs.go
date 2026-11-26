package app

import (
	"slices"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The session tabs: a row of tabs over the main area, one per server, in
// the session list's order, as a browser's tabs. A server's tab stands for
// its row and the rows under it (its panel row and its desktop): a click
// goes back to the one of them the user was on last, so that the terminal,
// the panel, the desktop and the monitor all switch with it. The list and
// the tabs are two views of a.sessions and a.active, never out of step.
// Closing a server's tab closes it with its rows, asking first while it is
// connected. With the left column folded away (sidebarHidden), the tabs
// are the way between servers.

// sessionRoot is the server row s belongs to: s, or a panel row's session.
func sessionRoot(s *Session) *Session {
	if s.isPanelTab() {
		return s.panelOf
	}
	return s
}

// mainColumn is the main area under the session tabs.
func (a *App) mainColumn(c *ui.Context) {
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Children(func() {
		a.sessionTabs(c)
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() { a.mainArea(c) })
	})
}

// toggleSidebar folds the left column away, or shows it, and keeps the
// choice.
func (a *App) toggleSidebar() {
	a.sidebarHidden = !a.sidebarHidden
	a.saveLayout()
}

// selectServer selects the row of root's group the user was on last.
func (a *App) selectServer(root *Session) {
	target := root
	if last := root.lastRow; last != nil && sessionRoot(last) == root && slices.Contains(a.sessions, last) {
		target = last
	}
	if i := slices.Index(a.sessions, target); i >= 0 {
		a.active = i
	}
}

// requestCloseSession closes s, asking first when it is a server still
// connected or connecting: closing it ends the connection, its desktop,
// its panel and its transfers.
func (a *App) requestCloseSession(s *Session) {
	if !s.isPanelTab() && (s.status == statusConnected || s.status == statusConnecting) {
		a.closeAsk = s
		return
	}
	a.closeSession(s)
}

// closeAskView asks before closing a connected server.
func (a *App) closeAskView(c *ui.Context) {
	s := a.closeAsk
	if s == nil {
		return
	}
	if !slices.Contains(a.sessions, s) {
		a.closeAsk = nil
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(420).MaxWidthPercent(90).Gap(10).Children(func() {
			ui.Text(c, locales.CloseSessionTitle(s.label())).FontSize(16).Bold().MaxWidthPercent(100)
			widgets.Muted(c, locales.CloseSessionBody()).MaxWidthPercent(100)
			ui.Row(c).Gap(7).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.CloseSessionConfirm()).Clicked() {
					a.closeSession(s)
					open = false
				}
			})
		})
	})
	if !open {
		a.closeAsk = nil
	}
}

// sessionTabs is the row of the servers' tabs, after the button that
// folds the left column away and before the one that opens a new tab.
func (a *App) sessionTabs(c *ui.Context) {
	cur := a.current()
	if cur != nil {
		sessionRoot(cur).lastRow = cur
	}
	var closing *Session
	ui.Row(c).FillWidth().Shrink(0).Height(34).AlignItems(ui.End).PaddingX(4).Gap(2).
		Background(colors.sidebar).BorderWidth(0, 0, 1, 0).BorderColor(colors.frame).Children(func() {
		icon, label := "chevrons-left", locales.HideSidebar()
		if a.sidebarHidden {
			icon, label = "chevrons-right", locales.ShowSidebar()
		}
		ui.Row(c).Height(33).AlignItems(ui.Center).Shrink(0).Children(func() {
			if widgets.IconButton(c, icon, label+shortcutHint(a.shortcutLabel(scSidebar))).Label(label).Clicked() {
				a.toggleSidebar()
			}
		})
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Height(33).Children(func() {
			ui.Row(c).Height(33).AlignItems(ui.End).Gap(2).Children(func() {
				for _, s := range a.sessions {
					if s.isPanelTab() {
						continue
					}
					if a.sessionTab(c.Key(s.ID), s, cur != nil && sessionRoot(cur) == s) {
						closing = s
					}
				}
			})
		})
		ui.Row(c).Height(33).AlignItems(ui.Center).Shrink(0).Children(func() {
			if widgets.IconButton(c, "plus", locales.NewTab()).Clicked() {
				a.NewTab()
			}
		})
	})
	if closing != nil {
		a.requestCloseSession(closing)
	}
}

// shortcutHint is a tooltip's " (key)", or nothing without a key.
func shortcutHint(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// sessionTab is the tab of the server s. It reports whether the user
// closed it.
func (a *App) sessionTab(c *ui.Context, s *Session, active bool) (closed bool) {
	tab := ui.Row(c).Height(30).MinWidth(96).MaxWidth(220).Shrink(0).Gap(6).PaddingX(10).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Label(s.label()).Tooltip(listLabel(s)).
		Radius(6).BorderWidth(1, 1, 0, 1)
	hovered := tab.Hovered()
	switch {
	case active:
		// Joined to the area under it, with the accent on its top.
		tab.Background(theme.Background).BorderColor(colors.frame).Checked(true).DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + 6, Y: r.Y, W: r.W - 12, H: 2}, theme.Accent, 1)
		})
	case hovered:
		tab.Background(theme.Text.Alpha(0.06)).BorderColor(ui.Transparent)
	default:
		tab.BorderColor(ui.Transparent)
	}
	if tab.Clicked() {
		a.selectServer(s)
	}
	tab.ContextMenu(func(m *ui.Menu) {
		if !s.isNewTab() {
			if m.Item(locales.Reconnect()).Chosen() {
				a.reconnect(s)
			}
			if m.Item(locales.Disconnect()).Disabled(s.status != statusConnected && s.status != statusConnecting).Chosen() {
				a.transfers.cancelSession(s)
				s.disconnect()
				s.status, s.err = statusClosed, nil
				a.syncAlertSessions()
			}
			m.Separator()
		}
		if m.Item(locales.Close()).Danger().Chosen() {
			closed = true
		}
	})
	tab.Children(func() {
		if s.isNewTab() {
			ui.Icon(c, icons.Get("app-window")).FontSize(13).TextColor(theme.TextMuted).Shrink(0)
		} else {
			sessionMark(c, s, false)
		}
		name := ui.Text(c, s.label()).FontSize(13).SingleLine().MinWidth(0).Grow(1).Shrink(1)
		if active {
			name.FontWeight(600)
		} else {
			name.TextColor(theme.TextMuted)
		}
		close := widgets.IconButton(c, "x", locales.Close()).Size(18, 18)
		if !hovered && !active {
			close.Invisible()
		}
		if close.Clicked() {
			closed = true
		}
	})
	return closed
}
