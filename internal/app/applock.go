package app

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"runtime"
	"strings"
	"time"

	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/argon2"
)

const lockVerifierKey = "app/lock/verifier"

type appLock struct {
	locked, busy, confirming, resetting bool
	input                               map[WindowKind]*string
	lastActivity, retry                 time.Time
	idleTimer                           *time.Timer
	idleGeneration                      uint64
	failures                            uint
	generation                          uint64
	message                             func() string
	now                                 func() time.Time
	options                             *lockOptions
}

func lockDigest(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
}

func newLockVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(salt) + ":" + base64.RawStdEncoding.EncodeToString(lockDigest(password, salt)), nil
}

func verifyLockPassword(verifier, password string) bool {
	version, rest, _ := strings.Cut(verifier, ":")
	encodedSalt, encodedDigest, _ := strings.Cut(rest, ":")
	salt, saltErr := base64.RawStdEncoding.DecodeString(encodedSalt)
	digest, digestErr := base64.RawStdEncoding.DecodeString(encodedDigest)
	return version == "v1" && saltErr == nil && digestErr == nil && len(salt) == 16 && len(digest) == 32 &&
		subtle.ConstantTimeCompare(lockDigest(password, salt), digest) == 1
}

func (state *appLock) clock() time.Time {
	if state.now != nil {
		return state.now()
	}
	return time.Now()
}

func (state *appLock) delay() time.Duration { return max(0, state.retry.Sub(state.clock())) }

func (state *appLock) wrongPassword() {
	seconds := min(uint(1)<<min(state.failures, 5), 30)
	state.failures = min(state.failures+1, 5)
	state.retry = state.clock().Add(time.Duration(seconds) * time.Second)
	state.message = locales.LockWrongPassword
}

func (a *App) Locked() bool { return a.lock.locked }

func (a *App) CanLock() bool { return a.settings.LockEnabled && !a.lock.locked }

func (a *App) LockNow() {
	if !a.CanLock() {
		return
	}
	a.lock.locked = true
	a.stopIdleLock()
	a.lock.generation++
	a.lock.options = nil
	a.notices = nil
	a.pendingCopy = nil
	clear(a.totpCopies)
	a.pwText = ""
	if a.editor != nil && !a.editor.totpBusy {
		a.editor.totpText = ""
	}
	for kind := range a.windows {
		a.plat.RetitleWindow(kind)
	}
	a.redraw()
}

func (a *App) WindowMinimized() {
	if a.settings.LockOnMinimize {
		a.LockNow()
	}
}

func (a *App) unlockApp() {
	state := &a.lock
	state.locked, state.input, state.message = false, nil, nil
	state.failures, state.retry = 0, time.Time{}
	state.lastActivity = state.clock()
	a.scheduleIdleLock()
	for kind := range a.windows {
		a.plat.RetitleWindow(kind)
	}
	a.redraw()
}

var lockIdleChoices = []int{0, 1, 5, 15, 30, 60}

func lockIdleDuration(minutes int) time.Duration {
	for _, choice := range lockIdleChoices {
		if minutes == choice {
			return time.Duration(minutes) * time.Minute
		}
	}
	return 0
}

func (a *App) stopIdleLock() {
	state := &a.lock
	state.idleGeneration++
	if state.idleTimer != nil {
		state.idleTimer.Stop()
		state.idleTimer = nil
	}
}

func (a *App) scheduleIdleLock() {
	state := &a.lock
	interval := lockIdleDuration(a.settings.LockIdleMinutes)
	if !a.CanLock() || interval == 0 {
		a.stopIdleLock()
		return
	}
	if state.lastActivity.IsZero() {
		state.lastActivity = state.clock()
	}
	remaining := interval - state.clock().Sub(state.lastActivity)
	if remaining <= 0 {
		a.LockNow()
	} else if state.idleTimer == nil && state.now == nil {
		generation := state.idleGeneration
		state.idleTimer = time.AfterFunc(remaining, func() {
			a.post(func() {
				if generation == state.idleGeneration {
					state.idleTimer = nil
					a.scheduleIdleLock()
				}
			})
		})
	}
}

func lockShortcut() keyCombo {
	mods := ui.Ctrl | ui.Alt
	if runtime.GOOS == "darwin" {
		mods = ui.Super | ui.Shift | ui.Alt
	}
	return keyCombo{mods, ui.KeyL}
}

func (a *App) lockGate(c *ui.Context, kind WindowKind) bool {
	if a.settings.LockEnabled || kind == WindowMain || kind == WindowManager {
		c.SetMenuPresentation(ui.PopoverMenus)
	}
	if a.CanLock() {
		state := &a.lock
		key := lockShortcut()
		if c.Shortcut(key.Mods, key.Key) {
			a.LockNow()
		}
		if lockIdleDuration(a.settings.LockIdleMinutes) > 0 {
			a.scheduleIdleLock()
			if !a.Locked() {
				if state.now != nil {
					c.After(lockIdleDuration(a.settings.LockIdleMinutes) - state.clock().Sub(state.lastActivity))
				}
				c.Root().ObserveInput(func(ev ui.InputEvent) {
					switch ev.Kind {
					case ui.InputKeyDown, ui.InputPointerDown, ui.InputPointerMove, ui.InputScroll, ui.InputText, ui.InputCommand:
						state.lastActivity = state.clock()
					}
				})
			}
		}
	}
	if a.Locked() {
		if generation := ui.Local(c.Root(), "app-lock-toasts-cleared", func() uint64 { return 0 }); *generation != a.lock.generation+1 {
			*generation = a.lock.generation + 1
			c.CloseToast("")
		}
		a.lockView(c, kind)
		return true
	}
	a.writeTOTPCopy(c, kind)
	return false
}

func (a *App) tryUnlock(password string) {
	state := &a.lock
	if !a.Locked() || state.busy || state.confirming || password == "" || len(password) > 1024 || state.delay() > 0 {
		return
	}
	state.busy, state.message = true, nil
	clear(state.input)
	a.secrets.queue.do(func() {
		verifier, ok, err := a.plat.SecretGet(lockVerifierKey)
		correct := err == nil && ok && verifyLockPassword(verifier, password)
		a.post(func() {
			state.busy = false
			switch {
			case correct:
				a.unlockApp()
			case err != nil || !ok:
				state.message = locales.LockVerifierUnavailable
			default:
				state.wrongPassword()
			}
		})
	})
}

func (a *App) lockView(c *ui.Context, kind WindowKind) {
	state := &a.lock
	if state.input == nil {
		state.input = map[WindowKind]*string{}
	}
	input := state.input[kind]
	if input == nil {
		input = new(string)
		state.input[kind] = input
	}
	delay := state.delay()
	if delay > 0 {
		c.After(min(delay, time.Second))
	}
	disabled := state.busy || state.confirming || delay > 0
	ui.Column(c.Key("locked")).Fill().Background(colors.overviewPage).AlignItems(ui.Center).Justify(ui.Center).Padding(12).Children(func() {
		ui.Column(c).Width(350).MaxWidthPercent(100).Padding(18, 22).Radius(10).Border(1, colors.frame).Background(colors.card).Gap(8).Children(func() {
			ui.Text(c, locales.AppTitle()).FontSize(21).Bold()
			ui.Text(c, locales.LockHidden()).TextColor(theme.TextMuted)
			submit := ui.TextInput(c.Key(input), input).Password().Label(locales.LockPassword()).Placeholder(locales.LockPassword()).FillWidth().Disabled(disabled).AutoFocus().Submitted()
			switch {
			case delay > 0:
				ui.Text(c, locales.LockRetry(int((delay+time.Second-1)/time.Second))).TextColor(theme.Danger)
			case state.message != nil:
				ui.Text(c, state.message()).TextColor(theme.Danger)
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
				if ui.Button(c, locales.LockReset()).Disabled(state.busy || state.confirming).Clicked() {
					a.confirmLockReset(kind)
				}
				ui.Spacer(c)
				if ui.PrimaryButton(c, locales.LockUnlock()).Disabled(disabled || *input == "" || len(*input) > 1024).Clicked() || submit && !disabled {
					a.tryUnlock(*input)
					*input = ""
				}
			})
		})
	})
}
