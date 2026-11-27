package app

// The account's bar at the bottom of the left panel (account_sync.go):
// signed out, it offers to sign in; signed in, it shows who and how the
// account's sync stands, and opens the account's card above it.

import (
	"errors"
	"strings"
	"time"

	"finallyshell/internal/account"
	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// accountBarHeight is the bar's height, which the session panel keeps
// besides its title and a row (monitorLimits).
const accountBarHeight = 28

// accountAvatar is the account's initial in a circle of size DIPs, with
// a dot of color at its corner, none for the zero color.
func accountAvatar(c *ui.Context, s config.AccountSettings, size float32, dot ui.Color) {
	initial := "?"
	if r := []rune(s.Login); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	ui.Box(c).Size(size, size).Shrink(0).Children(func() {
		ui.Text(c, initial).FontSize(size*0.46).Bold().TextColor(theme.AccentText).Background(theme.Accent).
			Size(size, size).Radius(size/2).TextAlign(ui.Center).Padding(size*0.2, 0).AccessHidden(true)
		if dot != (ui.Color{}) {
			d := max(7, size*0.3)
			ui.Box(c).Size(d, d).Radius(d/2).Background(dot).Border(1.5, colors.sidebar).
				Absolute().Right(-1).Bottom(-1).ZIndex(1).AccessHidden(true)
		}
	})
}

// accountDisplayName is the account's display name, its user name when
// it has none.
func accountDisplayName(s config.AccountSettings) string {
	if name := strings.TrimSpace(s.Name); name != "" {
		return name
	}
	return s.Login
}

// accountSyncState is how the account's sync stands, for the bar and the
// card: an icon, the text, its color, and whether it is busy (a spinner
// instead of the icon).
func (a *App) accountSyncState(now time.Time) (icon, text string, color ui.Color, busy bool) {
	s, ac := a.settings.Account, &a.account
	st := &ac.sync
	switch {
	case s.Expired:
		return "cloud-alert", locales.AccountExpiredShort(), theme.Danger, false
	case st.running || ac.renewing:
		return "", locales.SyncRunning(), theme.TextMuted, true
	case !st.loaded:
		return "", "", theme.TextMuted, true
	case st.password == "":
		return "key-round", locales.AccountNoPassword(), theme.Warning, false
	case errors.Is(st.err, cloudsync.ErrWrongPassword):
		return "key-round", locales.AccountNeedPassword(), theme.Warning, false
	case st.choice != nil:
		return "cloud-alert", locales.AccountChoiceShort(), theme.Warning, false
	case st.guard != nil:
		return "cloud-alert", locales.AccountGuardShort(), theme.Warning, false
	case st.err != nil:
		return "cloud-alert", locales.AccountSyncFailed(), theme.Danger, false
	case !st.state.At.IsZero():
		return "cloud-check", locales.AccountSyncedAgo(agoText(now, st.state.At)), theme.Success, false
	}
	return "cloud-check", locales.AccountNotSyncedYet(), theme.TextMuted, false
}

// agoText says how long ago at was, roughly.
func agoText(now, at time.Time) string {
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return locales.AccountAgoNow()
	case d < time.Hour:
		return locales.AccountAgoMinutes(int(d.Minutes()))
	case d < 24*time.Hour:
		return locales.AccountAgoHours(int(d.Hours()))
	}
	return locales.AccountAgoDays(int(d.Hours() / 24))
}

// accountBar is the bar at the bottom of the left panel.
func (a *App) accountBar(c *ui.Context) {
	s := a.settings.Account
	if s.SignedIn() {
		a.loadAccount()
		a.loadSyncStateOn(a.accountSync())
	}
	now := c.Now()
	bar := ui.Row(c.Key("account-bar")).Height(accountBarHeight).Shrink(0).Padding(0, 8).Gap(6).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(colors.frame).Role(ui.RoleButton)
	if bar.Hovered() || a.account.view.cardOpen {
		bar.Background(theme.Text.Alpha(0.05))
	}
	if !s.SignedIn() {
		bar.Label(locales.AccountSignInBar()).Tooltip(locales.AccountSignInGitHub())
		bar.Children(func() {
			ui.Icon(c, icons.Get("github")).FontSize(14).TextColor(theme.Text).Shrink(0)
			ui.Text(c, locales.AccountSignInBar()).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
			ui.Spacer(c)
			ui.Icon(c, icons.Get("chevron-right")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
		})
		if bar.Clicked() {
			a.openAccountLogin()
		}
		return
	}
	icon, status, color, busy := a.accountSyncState(now)
	dot := theme.Success
	switch {
	case s.Expired || color == theme.Danger:
		dot = theme.Danger
	case color == theme.Warning:
		dot = theme.Warning
	case busy:
		dot = theme.Accent
	}
	name := accountDisplayName(s)
	bar.Label(name + " · " + status).Tooltip(locales.AccountButtonSignedIn(s.Login))
	bar.Children(func() {
		accountAvatar(c, s, 18, dot)
		ui.Text(c, name).FontSize(12).Bold().SingleLine().MinWidth(0).Shrink(1)
		ui.Icon(c, icons.Get("github")).FontSize(11).TextColor(theme.TextMuted).Shrink(0)
		ui.Spacer(c)
		ui.Row(c).Gap(4).AlignItems(ui.Center).MinWidth(0).Shrink(1).Children(func() {
			switch {
			case busy:
				ui.Spinner(c).Size(11, 11).Label(status)
			case icon != "":
				ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(color).Shrink(0)
			}
			ui.Text(c, status).FontSize(11).TextColor(theme.TextMuted).SingleLine().MinWidth(0).Shrink(1)
		})
		ui.Icon(c, icons.Get("chevron-up")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
	})
	if bar.Clicked() {
		a.account.view.cardOpen = !a.account.view.cardOpen
		a.account.view.cardConfirmOut = false
	}
	if a.account.sync.running || a.account.renewing || !a.account.sync.state.At.IsZero() {
		c.After(time.Minute) // the time ago
	}
	ui.Popover(c, bar, &a.account.view.cardOpen, func() { a.accountCard(c, now) })
}

// accountCard is the card the bar opens: who signed in, the sync, and
// what to do.
func (a *App) accountCard(c *ui.Context, now time.Time) {
	s, ac, v := a.settings.Account, &a.account, &a.account.view
	ui.Column(c).Width(300).Padding(14).Gap(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			dot := theme.Success
			if s.Expired {
				dot = theme.Danger
			}
			accountAvatar(c, s, 40, dot)
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				name := accountDisplayName(s)
				ui.Text(c, name).Bold().FontSize(14).SingleLine().MinWidth(0)
				widgets.Muted(c, "@"+s.Login).FontSize(12).SingleLine()
			})
		})
		a.accountEmailRow(c, s)
		color, text := theme.Success, locales.AccountSignedInGitHub()
		if s.Expired {
			color, text = theme.Danger, locales.AccountExpiredBadge()
		}
		ui.Row(c).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(3, 10).Radius(11).Background(color.Alpha(0.12)).Children(func() {
				ui.Icon(c, icons.Get("github")).FontSize(12).TextColor(color)
				ui.Text(c, text).FontSize(12).Bold().TextColor(color)
			})
		})
		ui.Divider(c)
		icon, status, scolor, busy := a.accountSyncState(now)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			switch {
			case busy:
				ui.Spinner(c).Size(14, 14).Label(status)
			case icon != "":
				ui.Icon(c, icons.Get(icon)).FontSize(15).TextColor(scolor).Shrink(0)
			}
			ui.Text(c, status).FontSize(12).SingleLine().MinWidth(0)
		})
		opts := s.SyncSettings()
		summary := locales.AccountAutoOff()
		if opts.Auto {
			summary = locales.AccountAutoEvery(opts.IntervalOrDefault())
		}
		if opts.Passwords {
			summary += " · " + locales.AccountWithPasswords()
		}
		widgets.Muted(c, summary).FontSize(11).PaddingStart(23)
		if errors.Is(ac.sync.err, cloudsync.ErrWrongPassword) && !s.Expired {
			if ui.PrimaryButton(c, locales.AccountEnterPassword()).Clicked() {
				v.cardOpen = false
				a.openAccountWindow()
			}
		}
		if ac.sync.choice != nil && !s.Expired {
			if ui.PrimaryButton(c, locales.AccountChooseButton()).Clicked() {
				v.cardOpen = false
				a.openSyncChoice(a.accountSync())
			}
		}
		if s.Expired {
			if ui.PrimaryButton(c, locales.AccountSignInAgain()).Clicked() {
				v.cardOpen = false
				a.openAccountLogin()
			}
		} else {
			ui.Row(c).Gap(8).Children(func() {
				if ui.PrimaryButton(c, locales.SyncNow()).Disabled(!a.accountReady() || a.syncBusy() || ac.renewing).Clicked() {
					a.syncAccountNow(false, c.Now())
				}
				if ui.Button(c, locales.AccountSettingsButton()).Grow(1).Clicked() {
					v.cardOpen = false
					a.openAccountWindow()
				}
			})
		}
		ui.Divider(c)
		if v.cardConfirmOut {
			widgets.Muted(c, locales.AccountSignOutConfirm(s.Login)).FontSize(12)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, locales.AccountSignOut()).Disabled(ac.sync.running).Clicked() {
					v.cardOpen, v.cardConfirmOut = false, false
					if err := a.signOut(); err != nil {
						v.signOutFailed = err
					}
				}
				if ui.Button(c, locales.Cancel()).Clicked() {
					v.cardConfirmOut = false
				}
			})
			return
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			if linkButton(c, locales.AccountLog(), theme.Text).Clicked() {
				v.cardOpen = false
				a.openAccountWindow()
			}
			if linkButton(c, locales.AccountVersionsShort(), theme.Text).Clicked() {
				v.cardOpen = false
				a.openVersionsOn(a.accountSync(), WindowMain)
			}
			ui.Spacer(c)
			if linkButton(c, locales.AccountSignOut(), theme.Danger).Disabled(ac.sync.running).Clicked() {
				v.cardConfirmOut = true
			}
		})
	})
}

// accountEmailRow is the account's email address, masked until shown.
func (a *App) accountEmailRow(c *ui.Context, s config.AccountSettings) {
	v := &a.account.view
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Get("mail")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
		if s.Email == "" {
			widgets.Muted(c, locales.AccountNoEmail()).FontSize(12)
			return
		}
		email := account.MaskEmail(s.Email)
		if v.showEmail {
			email = s.Email
		}
		widgets.Muted(c, email).FontSize(12).SingleLine().MinWidth(0).Shrink(1).Selectable()
		ui.Spacer(c)
		label, icon := locales.AccountShowEmail(), "eye"
		if v.showEmail {
			label, icon = locales.AccountHideEmail(), "eye-off"
		}
		b := ui.Row(c.Key("email-toggle")).Gap(4).AlignItems(ui.Center).Role(ui.RoleButton).Label(label).Shrink(0)
		b.Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(theme.Accent)
			ui.Text(c, label).FontSize(12).TextColor(theme.Accent)
		})
		if b.Clicked() {
			v.showEmail = !v.showEmail
		}
	})
}

// linkButton is text that acts as a button, in color.
func linkButton(c *ui.Context, label string, color ui.Color) ui.Element {
	b := ui.Text(c.Key("link|"+label), label).FontSize(12).TextColor(color).Role(ui.RoleButton).Label(label)
	if b.Hovered() {
		b.Underline()
	}
	return b
}
