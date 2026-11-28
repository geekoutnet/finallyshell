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

// requestCloseSessions closes the servers list, asking once first when
// any of them is still connected or connecting.
func (a *App) requestCloseSessions(list []*Session) {
	if len(list) == 1 {
		a.requestCloseSession(list[0])
		return
	}
	if liveSessions(list) > 0 {
		a.closeAskMany = list
		return
	}
	for _, s := range list {
		a.closeSession(s)
	}
}

// liveSessions is how many of list are connected or connecting.
func liveSessions(list []*Session) int {
	n := 0
	for _, s := range list {
		if s.status == statusConnected || s.status == statusConnecting {
			n++
		}
	}
	return n
}

// closeAskView asks before closing a connected server, or tabs some of
// which are.
func (a *App) closeAskView(c *ui.Context) {
	a.closeAskMany = slices.DeleteFunc(a.closeAskMany, func(s *Session) bool { return !slices.Contains(a.sessions, s) })
	if len(a.closeAskMany) > 0 {
		list := a.closeAskMany
		a.closeQuestion(c, locales.CloseTabsTitle(len(list)), locales.CloseTabsBody(liveSessions(list)), func() {
			for _, s := range list {
				a.closeSession(s)
			}
		}, func() { a.closeAskMany = nil })
		return
	}
	s := a.closeAsk
	if s == nil {
		return
	}
	if !slices.Contains(a.sessions, s) {
		a.closeAsk = nil
		return
	}
	a.closeQuestion(c, locales.CloseSessionTitle(s.label()), locales.CloseSessionBody(), func() { a.closeSession(s) },
		func() { a.closeAsk = nil })
}

// closeQuestion is the dialog asking title, with body under it: closing
// calls yes, and done once it is answered either way.
func (a *App) closeQuestion(c *ui.Context, title, body string, yes, done func()) {
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(420).MaxWidthPercent(90).Gap(10).Children(func() {
			ui.Text(c, title).FontSize(16).Bold().MaxWidthPercent(100)
			widgets.Muted(c, body).MaxWidthPercent(100)
			ui.Row(c).Gap(7).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.CloseSessionConfirm()).Clicked() {
					yes()
					open = false
				}
			})
		})
	})
	if !open {
		done()
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
		a.noteEntryMode(shown, cur)
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
	var closing []*Session
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
					tab, closed := a.sessionTab(c.Key(s.ID), s, s == shown, width, roots)
					if closed != nil {
						closing = closed
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
		a.requestCloseSessions(closing)
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

// sessionTab is the tab of the server s, width wide, among the tabs of
// roots. It returns the tabs the user closed from it: its own, or those
// its menu closes.
func (a *App) sessionTab(c *ui.Context, s *Session, active bool, width float32, roots []*Session) (tab ui.Element, closing []*Session) {
	tab = ui.Row(c).Width(width).Shrink(0).Gap(6).PaddingX(10).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleTab).Label(s.label()).Radius(6)
	row := a.tabRow(s)
	if s.isNewTab() {
		tab.Tooltip(listLabel(s))
	} else {
		tab.Tooltip(listLabel(s) + "\n" + locales.TabMode(tabModeName(row)))
	}
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
	tab.ContextMenu(func(m *ui.Menu) { closing = a.sessionTabMenu(c, m, s, roots) })
	tab.Children(func() {
		if s.isNewTab() {
			ui.Icon(c, icons.Get("app-window")).FontSize(13).TextColor(theme.TextMuted).Shrink(0)
		} else {
			tabModeMark(c, s, row, active)
		}
		if a.renaming(s) && a.rename.inTab {
			ui.Row(c).Grow(1).Shrink(1).MinWidth(0).Children(func() { a.renameField(c, active) })
		} else {
			name := ui.Text(c, s.label()).FontSize(13).SingleLine().MinWidth(0).Grow(1).Shrink(1)
			if active {
				name.FontWeight(600)
			} else {
				name.TextColor(theme.Text.Alpha(0.82))
			}
		}
		close := widgets.IconButton(c, "x", locales.CloseSession()).Size(18, 18)
		if !hovered && !active {
			close.Invisible()
		}
		if close.Clicked() {
			closing = []*Session{s}
		}
	})
	return tab, closing
}

// sessionTabMenu is the context menu of s's tab, roots the servers the
// tabs show: what the session list's menu has for s, then closing it or
// the tabs around it, as a browser's or an editor's tabs. It returns the
// tabs to close.
func (a *App) sessionTabMenu(c *ui.Context, m *ui.Menu, s *Session, roots []*Session) (closing []*Session) {
	if !s.isNewTab() {
		a.sessionMenuItems(c, m, s)
		if !s.isDeviceSession() && canSplit(s) {
			m.Separator()
			if m.Item(locales.SplitRight()).Chosen() {
				a.splitPane(s, splitRight)
			}
			if m.Item(locales.SplitDown()).Chosen() {
				a.splitPane(s, splitDown)
			}
		}
		if canRename(s) {
			m.Separator()
			if m.Item(locales.Rename()).Chosen() {
				a.startRename(s)
				a.rename.inTab = true
			}
			if s.name != "" && m.Item(locales.ResetName()).Chosen() {
				a.endRename(false)
				s.name = ""
			}
		}
	}
	m.Separator()
	if m.Item(locales.Close()).Chosen() {
		closing = []*Session{s}
	}
	i := slices.Index(roots, s)
	others := slices.DeleteFunc(slices.Clone(roots), func(o *Session) bool { return o == s })
	var gone []*Session
	for _, o := range roots {
		if !o.isNewTab() && o.status != statusConnected && o.status != statusConnecting {
			gone = append(gone, o)
		}
	}
	for _, item := range []struct {
		label string
		tabs  []*Session
	}{
		{locales.CloseOtherTabs(), others},
		{locales.CloseTabsRight(), roots[i+1:]},
		{locales.CloseTabsLeft(), roots[:i]},
		{locales.CloseDisconnectedTabs(), gone},
	} {
		if m.Item(item.label).Disabled(len(item.tabs) == 0).Chosen() {
			closing = item.tabs
		}
	}
	m.Separator()
	if m.Item(locales.CloseAllTabs()).Danger().Chosen() {
		closing = roots
	}
	return closing
}

// sessionMenuItems are the items of a server's menu in the session list
// and on its tab: switching it between its terminal and its desktop,
// connecting again, another session to the host, disconnecting and its
// connection info.
func (a *App) sessionMenuItems(c *ui.Context, m *ui.Menu, s *Session) {
	if canOpenDesktop(s) {
		a.desktopModeItem(m, s)
		m.Separator()
	}
	if m.Item(locales.Reconnect()).Chosen() {
		a.reconnect(s)
	}
	if m.Item(locales.DuplicateSession()).Chosen() {
		// In a tab of its own, though a new tab is selected.
		if s.isLocal() {
			a.openLocal(nil)
		} else {
			a.openSessionIn(nil, s.Host)
		}
	}
	if m.Item(locales.Disconnect()).Disabled(s.status != statusConnected && s.status != statusConnecting).Chosen() {
		a.transfers.cancelSession(s)
		s.disconnect()
		s.status, s.err = statusClosed, nil
		a.syncAlertSessions()
	}
	if !s.isLocal() {
		m.Separator()
		a.connInfoMenuItems(c, m, &a.connTree, s.Host, func(fn func()) { fn() })
	}
}

// tabModeMark marks a server's tab with what it shows, in the room of the
// status dot it takes the place of, a few DIPs wider: an icon for the
// terminal, the desktop or the panel row the tab is on (row), with the
// connection's status as a dot on its upper corner, as an app's badge,
// where it covers none of the icon, ringed in the tab's color.
func tabModeMark(c *ui.Context, s, row *Session, active bool) {
	icon := tabModeIcon(s, row)
	color := theme.Text.Alpha(0.62)
	if active {
		color = theme.Text.Alpha(0.85)
	}
	ring := colors.sidebar
	if active {
		ring = theme.Background
	}
	status := s.statusColor(theme)
	ui.Box(c).Size(15, 14).Shrink(0).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(color)
	}).DrawOver(func(p *ui.Painter, r ui.Rect) {
		const d, w = 7, 1.5
		x, y := r.X+r.W-d+1, r.Y-1
		p.Fill(ui.Rect{X: x - w, Y: y - w, W: d + 2*w, H: d + 2*w}, ring, d/2+w)
		p.Fill(ui.Rect{X: x, Y: y, W: d, H: d}, status, d/2)
	})
}

// tabModeIcon is the icon of what the tab of s shows, row.
func tabModeIcon(s, row *Session) string {
	switch {
	case row.isDesktopRow():
		return desktopRowIcon
	case row.isPanelTab() && row.panelTab >= 0 && row.panelTab < len(panelTabIcons):
		return panelTabIcons[row.panelTab]
	case s.isLocal():
		return "laptop"
	}
	return "terminal"
}

// tabModeName names what a tab shows, row, for its tooltip.
func tabModeName(row *Session) string {
	switch {
	case row.isDesktopRow():
		return locales.DesktopTab()
	case row.isPanelTab():
		return panelTabName(row)
	}
	return locales.TabModeTerminal()
}
