package app

import (
	"time"

	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The connection's state on the desktop: a small card in its bottom-right
// corner, above the dock, as the desktop has no terminal to show it on.
// While the session connects it says so with a spinner; when the
// connection fails or is lost, or the user disconnected it, it says so,
// with Reconnect, until the user acts on it or closes it; once a
// connection being made is up it says Connected for a moment.

// desktopConnConfirm is how long the card says Connected.
const desktopConnConfirm = 2 * time.Second

// desktopConnState is what the card knows of the session: the status it
// saw last, whether the user closed the card on it, and when a connection
// being made came up.
type desktopConnState struct {
	seen      bool
	last      sessionStatus
	dismissed bool
	connected time.Time
}

// desktopConnCard shows the card of s's connection on the desktop d.
func (a *App) desktopConnCard(c *ui.Context, d *desktopState, s *Session) {
	if s == nil || s.Host == nil {
		return
	}
	cs, now := &d.shell.conn, c.Now()
	if !cs.seen || s.status != cs.last {
		if cs.seen && cs.last == statusConnecting && s.status == statusConnected {
			cs.connected = now
		} else {
			cs.connected = time.Time{}
		}
		cs.seen, cs.last, cs.dismissed = true, s.status, false
	}
	switch s.status {
	case statusConnecting:
		desktopConnBox(c, ui.RoleStatus).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14).Shrink(0)
				ui.Text(c, locales.ConnectingTo(s.target())).FontSize(12).SingleLine().MinWidth(0)
			})
		})
	case statusConnected:
		left := desktopConnConfirm - now.Sub(cs.connected)
		if cs.connected.IsZero() || left <= 0 {
			cs.connected = time.Time{}
			return
		}
		c.After(left)
		desktopConnBox(c, ui.RoleStatus).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("circle-check")).FontSize(15).TextColor(theme.Success).Shrink(0)
				ui.Text(c, locales.StatusConnected()).FontSize(12).SingleLine()
			})
		})
	default: // failed, lost, or disconnected by the user (closed, no error)
		if cs.dismissed {
			return
		}
		lost := s.status == statusFailed || s.err != nil
		icon, color, role := "unplug", theme.TextMuted, ui.RoleStatus
		if lost {
			icon, color, role = "triangle-alert", theme.Danger, ui.RoleAlert
		}
		title := s.statusText()
		detail := describeError(s.err)
		desktopConnBox(c, role).Width(280).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get(icon)).FontSize(15).TextColor(color).Shrink(0)
				ui.Text(c, title).Bold().FontSize(13).SingleLine().MinWidth(0).Grow(1)
				dismiss := ui.Box(c).Size(22, 22).Center().Radius(6).Shrink(0).Role(ui.RoleButton).
					Label(locales.DesktopConnDismiss()).Tooltip(locales.DesktopConnDismiss())
				if dismiss.Hovered() {
					dismiss.Background(theme.Text.Alpha(.09))
				}
				dismiss.Children(func() { ui.Icon(c, icons.Get("x")).FontSize(13).TextColor(theme.TextMuted) })
				if dismiss.Clicked() {
					cs.dismissed = true
					c.Invalidate()
				}
			})
			if detail != "" && detail != title {
				ui.Text(c, detail).FontSize(12).TextColor(theme.TextMuted).FillWidth()
			}
			ui.Row(c).Gap(6).Children(func() {
				if ui.Button(c, locales.Reconnect()).FontSize(12).Clicked() {
					a.reconnect(s)
				}
				if ui.Button(c, locales.SwitchToTerminalMode()).FontSize(12).Clicked() {
					a.showTerminalMode(s)
				}
			})
		})
	}
}

// desktopConnBox is the card, in the look of the dock's popovers.
func desktopConnBox(c *ui.Context, role ui.Role) ui.Element {
	return ui.Column(c.Key("conn-card")).Absolute().Right(16).Bottom(desktopDockHeight+4).ZIndex(5).
		MaxWidth(320).Padding(10, 12).Gap(8).Radius(12).Background(theme.Background).Border(1, theme.Border).
		Shadow(0, 8, 25, 0, ui.RGBA(0, 0, 0, .37)).Role(role)
}
