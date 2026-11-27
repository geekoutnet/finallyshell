package app

// The account's windows (account_sync.go): the button at the top of the
// left panel, the sign-in window of the device flow, and the account
// window, with who signed in, the account's sync and its log.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// The account's windows.
const (
	WindowAccount      WindowKind = "account"
	WindowAccountLogin WindowKind = "account-login"
	WindowAccountGuard WindowKind = "account-deletion-guard"
)

// The phases of a sign-in.
const (
	loginRequesting = iota // asking the provider for a code
	loginWaiting           // the user enters the code
	loginFinishing         // authorized: reading the profile
	loginFailed
)

// loginState is a sign-in in the sign-in window.
type loginState struct {
	provider account.Provider
	code     account.DeviceCode
	phase    int
	err      error
	cancel   context.CancelFunc
	// copied is what was copied last ("code" or "url"), and when.
	copied   string
	copiedAt time.Time
}

// accountView is the account window's own state.
type accountView struct {
	events []config.AccountEvent // newest first; nil to read again
	logErr error
	open   map[time.Time]bool // the log's events shown with their steps
	// cardOpen: the account bar's card is open; cardConfirmOut asks there
	// before signing out. showEmail shows the whole address.
	// showPassword shows the sync password; pwCopiedAt is when it was
	// copied.
	showPassword             bool
	pwCopiedAt               time.Time
	cardOpen, cardConfirmOut bool
	showEmail                bool
	// The sync password's form: open, the new password typed twice, the
	// current one when the gist has another, a change running, and how
	// the last change went.
	pwOpen                      bool
	pwNew, pwConfirm, pwCurrent string
	pwBusy                      bool
	pwErr                       error
	pwMessage                   func() string
	confirmOut                  bool
	confirmForce                bool
	confirmClear                bool
	signOutFailed               error
}

// openAccountLogin opens the sign-in window, starting a sign-in.
func (a *App) openAccountLogin() {
	if a.windowOpen(WindowAccountLogin) {
		a.plat.FocusWindow(WindowAccountLogin)
		return
	}
	a.startLogin()
	a.openWindow(WindowSpec{
		Kind: WindowAccountLogin, Title: locales.AccountSignInGitHub,
		Width: 500, Height: 430, MinWidth: 440, MinHeight: 400,
		Closed: a.stopLogin,
	}, a.loginView)
}

// stopLogin ends the sign-in, as its window closes.
func (a *App) stopLogin() {
	if l := a.account.login; l != nil {
		l.cancel()
		a.account.login = nil
	}
}

// startLogin asks for a code, waits for the user to enter it, reads who
// signed in and keeps the account, then shows it.
func (a *App) startLogin() {
	a.stopLogin()
	p := a.newProvider("github")
	if p == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &loginState{provider: p, cancel: cancel}
	a.account.login = l
	current := func() bool { return a.account.login == l }
	go func() {
		rctx, rcancel := context.WithTimeout(ctx, accountTimeout)
		// Each step has its own error: a posted func reads it on the UI
		// thread while this goroutine goes on.
		code, codeErr := p.RequestCode(rctx)
		rcancel()
		a.post(func() {
			if !current() {
				return
			}
			if codeErr != nil {
				l.phase, l.err = loginFailed, codeErr
			} else {
				l.code, l.phase = code, loginWaiting
			}
			a.redraw()
		})
		if codeErr != nil {
			return
		}
		tok, err := account.Wait(ctx, p, code)
		var prof account.Profile
		if err == nil {
			a.post(func() {
				if current() {
					l.phase = loginFinishing
					a.redraw()
				}
			})
			pctx, pcancel := context.WithTimeout(ctx, accountTimeout)
			prof, err = p.Profile(pctx, tok)
			pcancel()
		}
		a.post(func() {
			if !current() || ctx.Err() != nil {
				return // canceled
			}
			if err == nil {
				err = a.signIn(p, tok, prof)
			}
			if err != nil {
				l.phase, l.err = loginFailed, err
				a.redraw()
				return
			}
			a.account.login = nil
			cancel()
			a.closeWindow(WindowAccountLogin)
			a.openAccountWindow()
		})
	}()
}

// loginErrorText says why a sign-in failed.
func loginErrorText(err error) string {
	switch {
	case errors.Is(err, account.ErrDenied):
		return locales.AccountDenied()
	case errors.Is(err, account.ErrExpired):
		return locales.AccountCodeExpired()
	}
	return locales.AccountLoginFailed(err.Error())
}

// loginView is the sign-in window: the page to open and the code to
// enter there, each to open or copy, while the app waits.
func (a *App) loginView(c *ui.Context) {
	l := a.account.login
	ui.Column(c).Fill().Gap(12).Padding(18).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("github")).FontSize(24).TextColor(theme.Text)
			ui.Text(c, locales.AccountSignInGitHub()).Bold().FontSize(16)
		})
		widgets.Muted(c, locales.AccountLoginHint()).FontSize(12)
		if l == nil {
			return
		}
		now := c.Now()
		copied := func(what string) bool {
			if l.copied == what && now.Sub(l.copiedAt) < 2*time.Second {
				c.After(2*time.Second - now.Sub(l.copiedAt))
				return true
			}
			return false
		}
		copy := func(what, text string) {
			c.WriteClipboard(text)
			l.copied, l.copiedAt = what, now
		}
		switch l.phase {
		case loginRequesting:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(16, 16).Label(locales.AccountRequesting())
				widgets.Muted(c, locales.AccountRequesting())
			})
		case loginFailed:
			ui.Text(c, loginErrorText(l.err)).TextColor(theme.Danger)
			ui.Row(c).Children(func() {
				if ui.PrimaryButton(c, locales.AccountTryAgain()).Clicked() {
					a.startLogin()
				}
			})
		default:
			uri, code := l.code.VerificationURI, l.code.UserCode
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, locales.AccountLoginStep1()).Bold()
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, uri).TextColor(theme.Accent).SingleLine().MinWidth(0).Shrink(1).Tooltip(uri)
					if ui.Button(c, locales.AccountOpenPage()).Clicked() {
						c.OpenURL(uri)
					}
					label := locales.Copy()
					if copied("url") {
						label = locales.AccountCopied()
					}
					if ui.Button(c, label).Clicked() {
						copy("url", uri)
					}
				})
			})
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, locales.AccountLoginStep2()).Bold()
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, code).FontSize(28).Bold().Padding(6, 14).Radius(6).
						Background(theme.Surface).Border(1, theme.Border).Label(code)
					label := locales.Copy()
					if copied("code") {
						label = locales.AccountCopied()
					}
					if ui.Button(c, label).Clicked() {
						copy("code", code)
					}
				})
			})
			ui.Row(c).Children(func() {
				if ui.PrimaryButton(c, locales.AccountOpenAndCopy()).Disabled(l.phase != loginWaiting).Clicked() {
					copy("code", code)
					c.OpenURL(uri)
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14).Label(locales.AccountRequesting())
				if l.phase == loginFinishing {
					widgets.Muted(c, locales.AccountFinishing())
					return
				}
				left := max(l.code.ExpiresAt.Sub(now), 0).Round(time.Second)
				widgets.Muted(c, locales.AccountWaiting(fmt.Sprintf("%d:%02d", int(left.Minutes()), int(left.Seconds())%60)))
				c.After(time.Second)
			})
		}
		ui.Spacer(c)
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				a.closeWindow(WindowAccountLogin)
			}
		})
	})
}

// openAccountWindow opens the account window.
func (a *App) openAccountWindow() {
	a.account.view.events = nil
	a.openWindow(WindowSpec{
		Kind: WindowAccount, Title: locales.AccountWindowTitle,
		Width: 640, Height: 700, MinWidth: 520, MinHeight: 480,
		Closed: func() {
			v := &a.account.view
			v.confirmOut, v.confirmForce, v.confirmClear, v.signOutFailed = false, false, false, nil
		},
	}, a.accountView)
}

// accountView is the account window: who signed in, the account's sync,
// and its log.
func (a *App) accountView(c *ui.Context) {
	s := a.settings.Account
	if !s.SignedIn() {
		ui.Column(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
			widgets.Muted(c, locales.AccountSignedOut())
			if ui.PrimaryButton(c, locales.AccountSignInGitHub()).Clicked() {
				a.closeWindow(WindowAccount)
				a.openAccountLogin()
			}
		})
		return
	}
	a.loadAccount()
	a.loadSyncStateOn(a.accountSync())
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Gap(16).Padding(18).Children(func() {
			a.accountHeader(c, s)
			a.accountSyncSection(c, s)
			a.accountLogSection(c)
		})
	})
}

// accountHeader is who signed in, with what, and the account's buttons.
func (a *App) accountHeader(c *ui.Context, s config.AccountSettings) {
	v := &a.account.view
	ui.Column(c).Gap(12).Padding(14).Radius(8).Background(theme.Surface).Border(1, theme.Border).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			initial := strings.ToUpper(string([]rune(s.Login)[:1]))
			ui.Text(c, initial).FontSize(22).Bold().TextColor(theme.AccentText).Background(theme.Accent).
				Size(52, 52).Radius(26).TextAlign(ui.Center).Padding(11, 0).Shrink(0).AccessHidden(true)
			ui.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
				name := s.Name
				if name == "" {
					name = s.Login
				}
				ui.Text(c, name).Bold().FontSize(16).SingleLine().MinWidth(0)
				widgets.Muted(c, "@"+s.Login).SingleLine()
				a.accountEmailRow(c, s)
			})
			color, text := theme.Success, locales.AccountSignedInGitHub()
			if s.Expired {
				color, text = theme.Danger, locales.AccountExpiredBadge()
			}
			ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(4, 10).Radius(12).Shrink(0).Background(color.Alpha(0.12)).Children(func() {
				ui.Icon(c, icons.Get("github")).FontSize(13).TextColor(color)
				ui.Text(c, text).FontSize(12).Bold().TextColor(color)
			})
		})
		if s.Expired {
			ui.Text(c, locales.AccountExpiredHint()).TextColor(theme.Danger).FontSize(12)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if s.Expired && ui.PrimaryButton(c, locales.AccountSignInAgain()).Clicked() {
				a.openAccountLogin()
			}
			if p := a.accountProvider(); p != nil {
				ui.Link(c, locales.AccountManage(), p.ManageURL())
			}
			if s.URL != "" {
				ui.Link(c, locales.AccountProfile(), s.URL)
			}
			ui.Spacer(c)
			if !v.confirmOut && ui.Button(c, locales.AccountSignOut()).Disabled(a.account.sync.running).Clicked() {
				v.confirmOut = true
			}
		})
		if v.confirmOut {
			widgets.Muted(c, locales.AccountSignOutConfirm(s.Login)).FontSize(12)
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, locales.AccountSignOut()).Disabled(a.account.sync.running).Clicked() {
					v.confirmOut = false
					if err := a.signOut(); err != nil {
						v.signOutFailed = err
					} else {
						a.closeWindow(WindowAccount)
					}
				}
				if ui.Button(c, locales.Cancel()).Clicked() {
					v.confirmOut = false
				}
			})
		}
		if v.signOutFailed != nil {
			ui.Text(c, v.signOutFailed.Error()).TextColor(theme.Danger).FontSize(12)
		}
	})
}

// accountSyncSection is the account's sync: its password, how it syncs,
// and syncing now.
func (a *App) accountSyncSection(c *ui.Context, s config.AccountSettings) {
	ac, v := &a.account, &a.account.view
	t := a.accountSync()
	opts := s.SyncSettings()
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, locales.AccountSyncTitle()).Bold().FontSize(14)
		widgets.Muted(c, locales.AccountSyncIntro(accountSyncFile)).FontSize(12)
		a.accountPasswordSection(c, s, opts)

		def := !s.CustomSync
		if ui.Checkbox(c, &def, locales.AccountDefaultSync()).Changed() {
			a.changeSettings(func() {
				acc := &a.settings.Account
				if !def && !acc.Auto && !acc.Passwords && acc.Interval == 0 {
					d := config.DefaultAccountSync
					acc.Auto, acc.Passwords, acc.Interval = d.Auto, d.Passwords, d.Interval
				}
				acc.CustomSync = !def
			})
			ac.sync.nextCheck = time.Time{}
		}
		widgets.Muted(c, locales.AccountDefaultSyncHint(config.DefaultSyncInterval)).FontSize(12).PaddingStart(18)
		if s.CustomSync {
			ui.Column(c).Gap(8).PaddingStart(18).Children(func() {
				withPw := s.Passwords
				if ui.Checkbox(c, &withPw, locales.SyncIncludePasswords()).Changed() {
					a.changeSettings(func() { a.settings.Account.Passwords = withPw })
				}
				widgets.Muted(c, locales.SyncIncludePasswordsHint()).FontSize(12).PaddingStart(18)
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					auto := s.Auto
					if ui.Checkbox(c, &auto, locales.SyncAutomatically()).Changed() {
						a.changeSettings(func() { a.settings.Account.Auto = auto })
						ac.sync.nextCheck = ac.sync.lastTry
					}
					labels := make([]string, len(syncIntervals))
					chosen := ""
					for i, m := range syncIntervals {
						labels[i] = locales.SyncEveryMinutes(m)
						if m == opts.IntervalOrDefault() {
							chosen = labels[i]
						}
					}
					if ui.Select(c, &chosen, labels).Width(160).Label(locales.SyncAutomatically()).Disabled(!s.Auto).Changed() {
						for i, l := range labels {
							if l == chosen {
								m := syncIntervals[i]
								a.changeSettings(func() { a.settings.Account.Interval = m })
							}
						}
					}
				})
				widgets.Muted(c, locales.SyncAutomaticallyHint()).FontSize(12).PaddingStart(18)
			})
		}

		ready := a.accountReady()
		ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(4, 0, 0, 0).Children(func() {
			busy := a.syncBusy() || ac.renewing
			if ui.PrimaryButton(c, locales.SyncNow()).Disabled(!ready || busy).Clicked() {
				a.syncAccountNow(false, c.Now())
			}
			if ac.renewing {
				ui.Spinner(c).Size(14, 14).Label(locales.AccountRenewing())
				widgets.Muted(c, locales.AccountRenewing()).FontSize(12)
				return
			}
			a.syncStatusOf(c, &ac.sync, ready)
		})
		if ac.sync.guard != nil {
			ui.Row(c).Children(func() {
				if ui.Button(c, locales.BackupReviewDeletion()).Clicked() {
					a.openSyncGuardOn(t)
				}
			})
		}
		if ac.sync.choice != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("cloud-alert")).FontSize(14).TextColor(theme.Warning)
				ui.Text(c, locales.AccountChoiceShort()).FontSize(12).TextColor(theme.Warning)
				if ui.Button(c, locales.AccountChooseButton()).Clicked() {
					a.openSyncChoice(t)
				}
			})
		}
		if ac.renewErr != nil {
			ui.Text(c, locales.AccountRenewFailed(ac.renewErr.Error())).TextColor(theme.Danger).FontSize(12)
		}
		if !v.confirmForce {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, locales.SyncReplaceRemote()).Disabled(!ready || a.syncBusy()).Clicked() {
					v.confirmForce = true
				}
				if s.GistID != "" {
					gist := "https://gist.github.com/" + url.PathEscape(s.Login) + "/" + url.PathEscape(s.GistID)
					ui.Link(c, locales.AccountViewGist(), gist)
					ui.Link(c, locales.AccountRevisions(), gist+"/revisions")
				}
			})
			return
		}
		widgets.Muted(c, locales.SyncReplaceRemoteConfirm()).FontSize(12)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, locales.SyncReplaceRemoteYes()).Disabled(!ready || a.syncBusy()).Clicked() {
				v.confirmForce = false
				a.syncAccountNow(true, c.Now())
			}
			if ui.Button(c, locales.Cancel()).Clicked() {
				v.confirmForce = false
			}
		})
	})
}

// accountPasswordReveal is the sync password this device uses, hidden
// until shown: the email address, or the password the user set. It can
// be copied, to type on another computer.
func (a *App) accountPasswordReveal(c *ui.Context) {
	v, pw := &a.account.view, a.account.sync.password
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(6).Background(theme.Background).Border(1, theme.Border).Children(func() {
		widgets.Muted(c, locales.AccountCurrentPassword()).FontSize(12).Shrink(0)
		shown := strings.Repeat("•", min(12, max(8, len([]rune(pw)))))
		if v.showPassword {
			shown = pw
		}
		ui.Text(c, shown).FontSize(12).Font("monospace").SingleLine().MinWidth(0).Shrink(1).Grow(1).Selectable()
		label, icon := locales.AccountShowEmail(), "eye"
		if v.showPassword {
			label, icon = locales.AccountHideEmail(), "eye-off"
		}
		b := ui.Row(c.Key("pw-toggle")).Gap(4).AlignItems(ui.Center).Role(ui.RoleButton).Label(label).Shrink(0)
		b.Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(theme.Accent)
			ui.Text(c, label).FontSize(12).TextColor(theme.Accent)
		})
		if b.Clicked() {
			v.showPassword = !v.showPassword
		}
		now := c.Now()
		copyLabel := locales.Copy()
		if d := now.Sub(v.pwCopiedAt); !v.pwCopiedAt.IsZero() && d < 2*time.Second {
			copyLabel = locales.AccountCopied()
			c.After(2*time.Second - d)
		}
		cp := ui.Row(c.Key("pw-copy")).Gap(4).AlignItems(ui.Center).Role(ui.RoleButton).Label(locales.Copy()).Shrink(0)
		cp.Children(func() {
			ui.Icon(c, icons.Get("copy")).FontSize(12).TextColor(theme.Accent)
			ui.Text(c, copyLabel).FontSize(12).TextColor(theme.Accent)
		})
		if cp.Clicked() {
			c.WriteClipboard(pw)
			v.pwCopiedAt = now
		}
	})
}

// accountPasswordSection is the account's sync password: the email
// address by default, or one of the user's, changed with the gist's file
// sealed again; and, when the gist has another, the field to type it.
func (a *App) accountPasswordSection(c *ui.Context, s config.AccountSettings, opts config.SyncSettings) {
	ac, v := &a.account, &a.account.view
	ui.Column(c).Gap(8).Padding(10, 12).Radius(6).Background(colors.note).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("key-round")).FontSize(14).TextColor(theme.TextMuted).Shrink(0)
			switch {
			case ac.sync.password == "":
				ui.Text(c, locales.AccountNoPassword()).Bold().FontSize(12).TextColor(theme.Warning)
			case s.CustomPassword:
				ui.Text(c, locales.AccountPasswordCustom()).Bold().FontSize(12)
			default:
				ui.Text(c, locales.AccountPasswordEmail()).Bold().FontSize(12)
			}
			ui.Spacer(c)
			busy := v.pwBusy || a.syncBusy() || ac.renewing
			if ac.sync.password != "" && !v.pwOpen && ui.Button(c, locales.AccountChangePassword()).Disabled(busy || s.Expired).Clicked() {
				v.pwOpen, v.pwNew, v.pwConfirm, v.pwErr, v.pwMessage = true, "", "", nil, nil
			}
			if s.CustomPassword && s.Email != "" && !v.pwOpen && ui.Button(c, locales.AccountUseEmail()).Disabled(busy || s.Expired).Clicked() {
				v.pwErr, v.pwMessage = nil, nil
				a.changeAccountPassword(s.Email)
			}
		})
		if ac.sync.password != "" {
			a.accountPasswordReveal(c)
		}
		if !s.CustomPassword && ac.sync.password != "" {
			widgets.Muted(c, locales.AccountPasswordEmailHint()).FontSize(12)
			if opts.Passwords {
				ui.Row(c).Gap(6).AlignItems(ui.Start).Padding(6, 8).Radius(4).Background(colors.warningBanner).Children(func() {
					ui.Icon(c, icons.Get("triangle-alert")).FontSize(13).TextColor(theme.Warning).Shrink(0)
					ui.Text(c, locales.AccountPasswordRisk()).FontSize(12).TextColor(colors.warningText).Grow(1).MinWidth(0)
				})
			}
		}
		// The gist has another password, or there is none yet: type it.
		wrong := errors.Is(ac.sync.err, cloudsync.ErrWrongPassword)
		if wrong || ac.sync.password == "" {
			prompt := locales.AccountSetPassword()
			var pe *cloudsync.PasswordError
			switch {
			case errors.As(ac.sync.err, &pe) && pe.Hint == cloudsync.KeyCustom && pe.Device != "":
				prompt = locales.AccountPasswordNeededCustom(pe.Device, pe.At.Local().Format("2006-01-02 15:04"))
			case errors.As(ac.sync.err, &pe) && pe.Hint == cloudsync.KeyEmail:
				prompt = locales.AccountPasswordNeededEmail()
			case wrong:
				prompt = locales.AccountWrongPassword()
			}
			ui.Text(c, prompt).FontSize(12).TextColor(theme.Danger)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &v.pwCurrent).Password().Width(240).Label(locales.SyncPassword())
				if ui.PrimaryButton(c, locales.AccountUsePassword()).Disabled(v.pwCurrent == "").Clicked() {
					a.setAccountPassword(v.pwCurrent)
					v.pwCurrent = ""
				}
			})
		}
		if v.pwOpen {
			ui.Form(c, func() {
				ui.Field(c, locales.AccountNewPassword(), func() {
					ui.TextInput(c, &v.pwNew).Password().Width(240).Label(locales.AccountNewPassword())
				})
				ui.Field(c, locales.AccountConfirmPassword(), func() {
					ui.TextInput(c, &v.pwConfirm).Password().Width(240).Label(locales.AccountConfirmPassword())
				})
			})
			widgets.Muted(c, locales.AccountPasswordChangeHint()).FontSize(12)
			mismatch := v.pwConfirm != "" && v.pwNew != v.pwConfirm
			if mismatch {
				ui.Text(c, locales.AccountPasswordMismatch()).FontSize(12).TextColor(theme.Danger)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ok := v.pwNew != "" && v.pwNew == v.pwConfirm && !v.pwBusy && !a.syncBusy()
				if ui.PrimaryButton(c, locales.AccountPasswordSave()).Disabled(!ok).Clicked() {
					a.changeAccountPassword(v.pwNew)
				}
				if ui.Button(c, locales.Cancel()).Disabled(v.pwBusy).Clicked() {
					v.pwOpen, v.pwNew, v.pwConfirm = false, "", ""
				}
			})
		}
		switch {
		case v.pwBusy:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14).Label(locales.AccountPasswordChanging())
				widgets.Muted(c, locales.AccountPasswordChanging()).FontSize(12)
			})
		case v.pwErr != nil:
			ui.Text(c, locales.AccountPasswordChangeFailed(syncErrorText(v.pwErr))).FontSize(12).TextColor(theme.Danger)
		case v.pwMessage != nil:
			ui.Text(c, v.pwMessage()).FontSize(12).TextColor(theme.Success)
		}
	})
}

// accountLogSection is the account's log, newest first: each sync with
// the steps it went through, each sign-in, sign-out and renewal.
func (a *App) accountLogSection(c *ui.Context) {
	v := &a.account.view
	if v.events == nil {
		events, err := config.ReadAccountLog()
		v.events, v.logErr = make([]config.AccountEvent, 0, len(events)), err
		for i := len(events) - 1; i >= 0; i-- {
			v.events = append(v.events, events[i])
		}
	}
	if v.open == nil {
		v.open = map[time.Time]bool{}
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.AccountLog()).Bold().FontSize(14)
			ui.Spacer(c)
			if len(v.events) > 0 && !v.confirmClear && ui.Button(c, locales.AccountLogClear()).Clicked() {
				v.confirmClear = true
			}
			if v.confirmClear {
				if ui.Button(c, locales.AccountLogClear()).Clicked() {
					v.confirmClear = false
					v.logErr = config.ClearAccountLog()
					v.events = nil
				}
				if ui.Button(c, locales.Cancel()).Clicked() {
					v.confirmClear = false
				}
			}
		})
		if v.logErr != nil {
			ui.Text(c, v.logErr.Error()).TextColor(theme.Danger).FontSize(12)
		}
		if len(v.events) == 0 {
			widgets.Muted(c, locales.AccountLogEmpty()).FontSize(12)
			return
		}
		for _, e := range v.events {
			a.accountEventRow(c, e)
		}
	})
}

// accountEventRow is an event of the account's log: when, what, why and
// how it went, and, clicked, its steps and what it changed.
func (a *App) accountEventRow(c *ui.Context, e config.AccountEvent) {
	v := &a.account.view
	open := v.open[e.At]
	detailed := len(e.Steps) > 0 || e.Error != ""
	row := ui.Column(c.Key("account-event|"+e.At.Format(time.RFC3339Nano))).Gap(6).Padding(8, 10).Radius(6).
		Border(1, theme.Border)
	if detailed {
		row.Role(ui.RoleButton).Label(accountActionText(e))
		if row.Hovered() {
			row.Background(theme.SurfaceHover)
		}
		if row.Clicked() {
			v.open[e.At] = !open
		}
	}
	row.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			color := theme.Success
			switch e.Result {
			case "failed":
				color = theme.Danger
			case "paused", "history-warning":
				color = theme.Warning
			}
			widgets.StatusDot(c, color)
			widgets.Muted(c, e.At.Local().Format("2006-01-02 15:04:05")).FontSize(12).Shrink(0)
			ui.Text(c, accountActionText(e)).Bold().FontSize(12).SingleLine().MinWidth(0)
			if r := accountReasonText(e.Reason); r != "" {
				widgets.Muted(c, r).FontSize(12).Shrink(0)
			}
			ui.Spacer(c)
			ui.Text(c, accountResultText(e.Result)).FontSize(12).TextColor(color).Shrink(0)
			if detailed {
				chevron := "chevron-right"
				if open {
					chevron = "chevron-down"
				}
				ui.Icon(c, icons.Get(chevron)).FontSize(12).TextColor(theme.TextMuted)
			}
		})
		if !open {
			return
		}
		ui.Column(c).Gap(4).PaddingStart(16).Children(func() {
			for i, step := range e.Steps {
				widgets.Muted(c, fmt.Sprintf("%d. %s", i+1, accountStepText(step))).FontSize(12)
			}
			if e.Action == config.AccountSync || e.Action == config.AccountReplace {
				widgets.Muted(c, locales.AccountCounts(locales.BackupLocal(), e.Local[0], e.Local[1], e.Local[2])).FontSize(12)
				widgets.Muted(c, locales.AccountCounts(locales.BackupServer(), e.Server[0], e.Server[1], e.Server[2])).FontSize(12)
				if e.Conflicts > 0 {
					widgets.Muted(c, locales.SyncConflicts(e.Conflicts)).FontSize(12)
				}
			}
			if e.Device != "" {
				widgets.Muted(c, locales.AccountLogDevice(e.Device)).FontSize(12)
			}
			if e.Error != "" {
				ui.Text(c, e.Error).TextColor(theme.Danger).FontSize(12)
			}
		})
	})
}

func accountActionText(e config.AccountEvent) string {
	switch e.Action {
	case config.AccountSignIn:
		return locales.AccountActSignIn(e.Login)
	case config.AccountSignOut:
		return locales.AccountActSignOut(e.Login)
	case config.AccountRefresh:
		return locales.AccountActRefresh()
	case config.AccountExpired:
		return locales.AccountActExpired()
	case config.AccountReplace:
		return locales.AccountActReplace()
	case config.AccountPassword:
		switch e.Reason {
		case "auto":
			return locales.AccountActPasswordAuto()
		case accountReasonEmail:
			return locales.AccountActEmailChanged()
		}
		return locales.AccountActPassword()
	case config.AccountChoice:
		return locales.AccountActChoice()
	}
	return locales.AccountActSync()
}

func accountReasonText(reason string) string {
	switch reason {
	case cloudsync.ReasonAuto:
		return locales.AccountReasonAuto()
	case cloudsync.ReasonManual:
		return locales.AccountReasonManual()
	case cloudsync.ReasonRestore:
		return locales.AccountReasonRestore()
	case choiceMerge:
		return locales.AccountChoiceMerge()
	case choiceRemote:
		return locales.AccountChoiceRemote()
	case choiceLocal:
		return locales.AccountChoiceLocal()
	}
	return ""
}

func accountResultText(result string) string {
	switch result {
	case "failed":
		return locales.AccountResultFailed()
	case "paused":
		return locales.AccountResultPaused()
	case "history-warning":
		return locales.AccountResultWarning()
	}
	return locales.AccountResultOk()
}

func accountStepText(step string) string {
	switch step {
	case config.StepRefresh:
		return locales.AccountStepRefresh()
	case config.StepRead:
		return locales.AccountStepRead()
	case config.StepCreate:
		return locales.AccountStepCreate()
	case config.StepMerge:
		return locales.AccountStepMerge()
	case config.StepUpload:
		return locales.AccountStepUpload()
	case config.StepBackup:
		return locales.AccountStepBackup()
	case config.StepApply:
		return locales.AccountStepApply()
	case config.StepUpToDate:
		return locales.AccountStepUpToDate()
	case config.StepReseal:
		return locales.AccountStepReseal()
	case config.StepAutoPassword:
		return locales.AccountStepAutoPassword()
	}
	return step
}
