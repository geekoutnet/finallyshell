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

// sessionTabsH is the height of the row of session tabs.
const sessionTabsH = 34

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

// The widths of a session tab: they share the room of the row between
// sessionTabMin and sessionTabMax, past which the row scrolls.
const (
	sessionTabMin = 120
	sessionTabMax = 220
	sessionTabGap = 2
)

// sessionTabs is the row of the servers' tabs, after the button that
// folds the left column away: as many as fit share its width, and more
// scroll sideways, with a menu of them all at its end, beside the button
// that opens a new tab.
func (a *App) sessionTabs(c *ui.Context) {
	cur := a.current()
	var shown *Session
	if cur != nil {
		shown = sessionRoot(cur)
		shown.lastRow = cur
	}
	var roots []*Session
	for _, s := range a.sessions {
		if !s.isPanelTab() {
			roots = append(roots, s)
		}
	}
	// The tabs share the room, short of a DIP or two that rounding would
	// otherwise take past its end; past their narrowest they scroll.
	width := float32(sessionTabMax)
	fits := true
	if n := float32(len(roots)); n > 0 && a.tabsRoom > 0 {
		share := (a.tabsRoom - 2 - sessionTabGap*(n-1)) / n
		fits = share >= sessionTabMin
		width = min(max(share, sessionTabMin), sessionTabMax)
	}
	if fits {
		a.tabsScroll.X = 0 // nothing to scroll to
	}
	overflow := !fits || a.tabsScroll.MaxX > 0.5
	var closing *Session
	ui.Row(c).FillWidth().Shrink(0).Height(sessionTabsH).AlignItems(ui.End).PaddingX(4).Gap(4).
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
		// No scrollbar, which would cover the names: the wheel scrolls the
		// row, and the menu of the tabs reaches those out of view.
		strip := ui.ScrollHorizontal(c.Key("session-tabs")).Grow(1).MinWidth(0).Height(33).
			TrackScroll(&a.tabsScroll).Scrollbars(false)
		if w := strip.Bounds().W; w > 0 && w != a.tabsRoom {
			a.tabsRoom = w
			c.Invalidate()
		}
		strip.Children(func() {
			ui.Row(c).Height(33).AlignItems(ui.End).Gap(sessionTabGap).Children(func() {
				for _, s := range roots {
					tab, closed := a.sessionTab(c.Key(s.ID), s, s == shown, width)
					if closed {
						closing = s
					}
					// The chosen server's tab scrolls into view as it is chosen.
					if s == shown && a.tabsShown != s {
						a.tabsShown = s
						tab.ScrollIntoView(ui.ScrollIntoViewOptions{Inline: ui.ScrollNearest})
					}
				}
			})
		})
		ui.Row(c).Height(33).AlignItems(ui.Center).Gap(4).Shrink(0).Children(func() {
			if overflow {
				widgets.IconButton(c, "chevron-down", locales.AllTabs()).PopoverMenuButton(func(m *ui.Menu) {
					for _, s := range roots {
						if m.Item(s.label()).Checked(s == shown).Chosen() {
							// Its tab scrolls into view even when it is the one
							// shown, scrolled away by hand.
							a.tabsShown = nil
							a.selectServer(s)
						}
					}
				})
			}
			a.newTabButton(c)
		})
	})
	if closing != nil {
		a.requestCloseSession(closing)
	}
}

// newTabButton opens a new tab: tinted with the accent, as the one way to
// add a tab there, which the eye finds at once.
func (a *App) newTabButton(c *ui.Context) {
	label := locales.NewTab() + shortcutHint(a.shortcutLabel(scNewTab))
	b := ui.Box(c.Key("new-tab")).Size(26, 26).Radius(7).Center().Shrink(0).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(locales.NewTab()).Tooltip(label).Border(1, theme.Accent.Alpha(0.45))
	if b.Hovered() {
		b.Background(theme.Accent.Alpha(0.2))
	} else {
		b.Background(theme.Accent.Alpha(0.1))
	}
	b.Children(func() { ui.Icon(c, icons.Get("plus")).FontSize(15).TextColor(theme.Accent) })
	if b.Clicked() {
		a.NewTab()
	}
}

// shortcutHint is a tooltip's " (key)", or nothing without a key.
func shortcutHint(key string) string {
	if key == "" {
		return ""
	}
	return " (" + key + ")"
}

// sessionTab is the tab of the server s, width wide. It reports whether
// the user closed it.
func (a *App) sessionTab(c *ui.Context, s *Session, active bool, width float32) (tab ui.Element, closed bool) {
	tab = ui.Row(c).Width(width).Shrink(0).Gap(6).PaddingX(10).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Label(s.label()).Tooltip(listLabel(s)).Radius(6)
	hovered := tab.Hovered()
	switch {
	case active:
		// Joined to the area under it, with the accent on its top.
		tab.Height(30).BorderWidth(1, 1, 0, 1).Background(theme.Background).BorderColor(colors.frame).Checked(true).
			DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.Fill(ui.Rect{X: r.X + 6, Y: r.Y, W: r.W - 12, H: 2}, theme.Accent, 1)
			})
	default:
		// An open server that is not shown: a tile of its own, above the
		// line under the row.
		tab.Height(27).Margin(0, 0, 3, 0).Border(1, theme.Border.Alpha(0.7))
		if hovered {
			tab.Background(theme.Text.Alpha(0.09))
		} else {
			tab.Background(theme.Text.Alpha(0.045))
		}
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
			name.TextColor(theme.Text.Alpha(0.82))
		}
		close := widgets.IconButton(c, "x", locales.CloseSession()).Size(18, 18)
		if !hovered && !active {
			close.Invisible()
		}
		if close.Clicked() {
			closed = true
		}
	})
	return tab, closed
}
