package app

import (
	"path/filepath"
	"runtime"
	"slices"
	"time"
	"unicode/utf8"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const pageSecurity = "security"

type lockOptions struct {
	current, password, repeat string
	remove                    bool
	message                   func() string
}

func (a *App) securityPage(c *ui.Context) {
	state := &a.lock
	enabled := a.settings.LockEnabled
	ui.Column(c).Gap(12).Children(func() {
		ui.Text(c, locales.LockSetting()).Bold()
		widgets.Muted(c, locales.LockHidden())
		ui.Row(c).Gap(8).Wrap().Children(func() {
			label := locales.LockSetPassword()
			if enabled {
				label = locales.LockChangePassword()
			}
			if ui.Button(c, label).Disabled(state.busy).Clicked() {
				state.options = &lockOptions{}
			}
			if enabled && ui.Button(c, locales.LockRemovePassword()).Disabled(state.busy).Clicked() {
				state.options = &lockOptions{remove: true}
			}
		})
		ui.Field(c, locales.LockIdle(), func() {
			names := make([]string, len(lockIdleChoices))
			names[0] = locales.LockOff()
			for index, minutes := range lockIdleChoices[1:] {
				names[index+1] = locales.LockMinutes(minutes)
			}
			selected := names[max(slices.Index(lockIdleChoices, a.settings.LockIdleMinutes), 0)]
			if ui.Select(c, &selected, names).Width(136).Disabled(!enabled || state.busy || state.confirming).Changed() {
				settings := a.settings
				settings.LockIdleMinutes = lockIdleChoices[slices.Index(names, selected)]
				a.confirmLockOption(settings, locales.LockIdle(), selected)
			}
		})
		minimize := a.settings.LockOnMinimize
		if ui.Checkbox(c, &minimize, locales.LockMinimize()).Disabled(!enabled || state.busy || state.confirming).Changed() {
			settings := a.settings
			settings.LockOnMinimize = minimize
			choice := locales.LockOff()
			if minimize {
				choice = locales.LockOn()
			}
			a.confirmLockOption(settings, locales.LockMinimize(), choice)
		}
		if enabled {
			key := lockShortcut().label(runtime.GOOS, false)
			if ui.Button(c, locales.LockNow()).AlignSelf(ui.Start).Clicked() {
				a.LockNow()
			}
			widgets.Muted(c, locales.LockShortcutHint(key))
		}
		if state.message != nil {
			ui.Text(c, state.message()).TextColor(theme.Danger)
		}
	})
	if state.options != nil {
		a.lockOptionsView(c, state.options)
	}
}

func (a *App) lockOptionsView(c *ui.Context, form *lockOptions) {
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(370).MaxWidthPercent(100).Gap(10).Children(func() {
			title, detail := locales.LockSetPassword(), locales.LockSetDetail()
			if form.remove {
				title, detail = locales.LockRemovePassword(), locales.LockRemoveDetail()
			} else if a.settings.LockEnabled {
				title, detail = locales.LockChangePassword(), locales.LockChangeDetail()
			}
			ui.Text(c, title).Bold().FontSize(16)
			widgets.Muted(c, detail)
			submit := false
			if a.settings.LockEnabled {
				submit = ui.TextInput(c, &form.current).Label(locales.LockCurrentPassword()).Placeholder(locales.LockCurrentPassword()).Password().AutoFocus().Disabled(a.lock.busy).Submitted()
			}
			if !form.remove {
				field := ui.TextInput(c, &form.password).Label(locales.LockNewPassword()).Placeholder(locales.LockNewPassword()).Password().Disabled(a.lock.busy)
				if !a.settings.LockEnabled {
					field.AutoFocus()
				}
				submit = ui.TextInput(c, &form.repeat).Label(locales.LockRepeatPassword()).Placeholder(locales.LockRepeatPassword()).Password().Disabled(a.lock.busy).Submitted() || submit
			}
			if delay := a.lock.delay(); delay > 0 {
				c.After(min(delay, time.Second))
				ui.Text(c, locales.LockRetry(int((delay+time.Second-1)/time.Second))).TextColor(theme.Danger)
			} else if form.message != nil {
				ui.Text(c, form.message()).TextColor(theme.Danger)
			}
			ui.Row(c).Gap(8).Wrap().Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Disabled(a.lock.busy).Clicked() {
					open = false
				}
				disabled := a.lock.busy || a.lock.delay() > 0
				if ui.PrimaryButton(c, title).Disabled(disabled).Clicked() || submit && !disabled {
					a.saveLockPassword(form)
				}
			})
		})
	})
	if !open && !a.lock.busy {
		a.lock.options = nil
	}
}

func (a *App) saveLockSettings(settings config.Settings) error {
	if err := backupKeyHostConfig(filepath.Join(config.Dir(), "settings.json"), a.lock.clock()); err != nil {
		return err
	}
	if err := a.saveSettings(settings); err != nil {
		return err
	}
	a.settings = settings
	return nil
}

func (a *App) updateLockOptions(settings config.Settings) {
	state := &a.lock
	state.message = nil
	if err := a.saveLockSettings(settings); err != nil {
		state.message = locales.LockSettingsFailed
	} else {
		state.lastActivity = state.clock()
		a.stopIdleLock()
		a.scheduleIdleLock()
	}
	a.redraw()
}

func (a *App) confirmLockOption(proposed config.Settings, title, choice string) {
	state := &a.lock
	if !a.CanLock() || state.busy || state.confirming {
		return
	}
	state.confirming = true
	generation := state.generation
	question := Confirmation{Title: locales.LockSecurity(), Message: locales.LockOptionChange(title, choice), Detail: locales.LockOptionDetail(), OK: locales.Save(), Cancel: locales.Cancel(), RequireExplicit: true, Parent: WindowOptions}
	go func() {
		confirmed := a.plat.Confirm(question)
		a.post(func() {
			state.confirming = false
			if confirmed && a.CanLock() && state.generation == generation && a.windowOpen(WindowOptions) {
				settings := a.settings
				settings.LockIdleMinutes, settings.LockOnMinimize = proposed.LockIdleMinutes, proposed.LockOnMinimize
				a.updateLockOptions(settings)
			}
		})
	}()
}

func (a *App) saveLockPassword(form *lockOptions) {
	state := &a.lock
	if state.busy || a.Locked() || state.options != form {
		return
	}
	if state.delay() > 0 {
		return
	}
	wasEnabled := a.settings.LockEnabled
	if !form.remove && (utf8.RuneCountInString(form.password) < 8 || len(form.password) > 1024 || form.password != form.repeat) {
		form.message = locales.LockPasswordRules
		return
	}
	if wasEnabled && (form.current == "" || len(form.current) > 1024) {
		form.message = locales.LockCurrentRequired
		return
	}
	current, password, remove := form.current, form.password, form.remove
	form.current, form.password, form.repeat, form.message = "", "", "", nil
	state.busy = true
	a.secrets.queue.do(func() {
		verifier, ok, err := a.plat.SecretGet(lockVerifierKey)
		wrong := wasEnabled && err == nil && ok && !verifyLockPassword(verifier, current)
		if err == nil && !wrong && (!wasEnabled || ok) {
			if remove {
				err = a.plat.SecretDelete(lockVerifierKey)
			} else if next, makeErr := newLockVerifier(password); makeErr != nil {
				err = makeErr
			} else {
				err = a.plat.SecretSet(lockVerifierKey, next)
			}
		}
		a.post(func() {
			state.busy = false
			switch {
			case wrong:
				state.wrongPassword()
				form.message = locales.LockWrongPassword
			case err != nil || wasEnabled && !ok:
				form.message = locales.LockVerifierUnavailable
				if a.Locked() {
					state.message = form.message
				}
			default:
				if wasEnabled == remove {
					settings := a.settings
					settings.LockEnabled = !remove
					if remove {
						settings.LockIdleMinutes, settings.LockOnMinimize = 0, false
					}
					if err := a.saveLockSettings(settings); err != nil {
						a.rollbackLockVerifier(verifier, ok, form)
						return
					}
				}
				state.failures, state.retry = 0, time.Time{}
				state.lastActivity, state.options, state.message = state.clock(), nil, nil
				a.stopIdleLock()
				a.scheduleIdleLock()
				if remove && a.Locked() {
					a.unlockApp()
				}
				if !remove && !wasEnabled {
					a.redraw()
				}
			}
		})
	})
}

func (a *App) rollbackLockVerifier(verifier string, hadVerifier bool, form *lockOptions) {
	a.lock.busy = true
	a.secrets.queue.do(func() {
		var err error
		if hadVerifier {
			err = a.plat.SecretSet(lockVerifierKey, verifier)
		} else {
			err = a.plat.SecretDelete(lockVerifierKey)
		}
		a.post(func() {
			a.lock.busy = false
			form.message = locales.LockSettingsFailed
			if err != nil {
				form.message = locales.LockVerifierUnavailable
			}
			a.lock.message = form.message
		})
	})
}

func (a *App) confirmLockReset(parent WindowKind) {
	state := &a.lock
	if !a.Locked() || state.busy || state.confirming {
		return
	}
	if a.syncBusy() || a.sync.loading || a.account.sync.loading {
		state.message = locales.LockSyncBusy
		return
	}
	state.confirming = true
	question := Confirmation{Title: locales.LockReset(), Message: locales.LockResetTitle(), Detail: locales.LockResetDetail(), OK: locales.LockResetConfirm(), Cancel: locales.Cancel(), RequireExplicit: true, Parent: parent}
	go func() {
		confirmed := a.plat.Confirm(question)
		a.post(func() {
			state.confirming = false
			if confirmed && a.Locked() {
				a.resetLock()
			}
		})
	}()
}

func (a *App) resetLock() {
	state := &a.lock
	if !a.Locked() || state.busy || a.syncBusy() || a.sync.loading || a.account.sync.loading {
		return
	}
	state.busy, state.resetting, state.message = true, true, nil
	keys := []string{syncPasswordKey}
	for _, provider := range []config.SyncProvider{config.SyncGist, config.SyncWebDAV, config.SyncS3} {
		keys = append(keys, syncCredentialKey(provider))
	}
	keys = append(keys, a.accountSecretKeys()...)
	for _, host := range a.hosts.Hosts {
		for _, name := range hostSecretNames {
			keys = append(keys, hostSecretKey(host.ID, name))
		}
		keys = append(keys, privilegeSecretKey(host, host.User))
	}
	for _, session := range a.sessions {
		if session.Host != nil && session.client != nil {
			keys = append(keys, privilegeSecretKey(session.Host, session.client.User()))
		}
	}
	a.secrets.queue.do(func() {
		privilegeKeys, err := a.privilegeCredentialKeys()
		if err == nil {
			for _, key := range append(keys, privilegeKeys...) {
				if err = a.plat.SecretDelete(key); err != nil {
					break
				}
			}
		}
		if err == nil {
			err = a.plat.SecretDelete(privilegeResetIndexKey)
		}
		if err == nil {
			err = a.plat.SecretDelete(lockVerifierKey)
		}
		a.post(func() {
			state.busy, state.resetting = false, false
			clear(a.passwords)
			clear(a.secrets.read)
			clear(a.secrets.untried)
			a.pwOpen, a.pwText, a.pwNeed, a.pwThen, a.pwCancel, a.pwErr = false, "", nil, nil, nil, nil
			a.sync.password, a.sync.credential = "", ""
			a.sync.loaded = false
			a.forgetAccountSecrets()
			clear(a.totpStatus)
			a.closeWindow(WindowEditor)
			a.closeWindow(WindowOptions)
			if err != nil {
				state.message = locales.LockResetFailed
				return
			}
			settings := a.settings
			settings.LockEnabled, settings.LockOnMinimize, settings.LockIdleMinutes = false, false, 0
			if err := a.saveLockSettings(settings); err != nil {
				state.message = locales.LockSettingsFailed
				return
			}
			a.quitApproved = true
			a.CloseAll()
			a.plat.Quit()
		})
	})
}
