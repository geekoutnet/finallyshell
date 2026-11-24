package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/totp"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

const totpPromptTimeout = 3 * time.Minute

type totpStatus struct {
	loading, present, available, failed bool
	checked, expires                    time.Time
	period                              time.Duration
}

type totpClipboard struct {
	hostID string
	code   string
	expiry time.Time
}

func (a *App) totpVerification(ctx context.Context, host config.Host, interactive bool) func(string) (string, error) {
	return func(prompt string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, totpPromptTimeout)
		defer cancel()
		result := make(chan string, 1)
		a.post(func() {
			if ctx.Err() != nil || a.lock.resetting {
				result <- ""
				return
			}
			answer := func(secret totp.Secret, available bool) {
				if ctx.Err() != nil || a.lock.resetting {
					result <- ""
					return
				}
				if !available {
					if !interactive {
						result <- ""
					} else {
						a.openTOTPChallenge(ctx, host, prompt, result)
					}
					return
				}
				go func() {
					code, _, err := freshTOTP(ctx, secret, time.Now, waitTOTP)
					a.post(func() {
						if err != nil || a.lock.resetting || a.quitApproved {
							code = ""
						}
						result <- code
					})
				}()
			}
			if current := a.hosts.Host(host.ID); current != nil && hostMatchesPrivilege(current, &host) && !current.IsRDP() {
				a.readTOTP(host.ID, answer)
			} else {
				answer(totp.Secret{}, false)
			}
		})
		select {
		case code := <-result:
			if code != "" && ctx.Err() == nil {
				return code, nil
			}
		case <-ctx.Done():
		}
		return "", sshx.ErrVerificationCode
	}
}

func freshTOTP(ctx context.Context, secret totp.Secret, clock func() time.Time, wait func(context.Context, time.Duration) error) (string, time.Time, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", time.Time{}, err
		}
		now := clock()
		left := secret.Remaining(now)
		if left > 3*time.Second {
			return secret.Code(now), now.Add(left), nil
		}
		if err := wait(ctx, left); err != nil {
			return "", time.Time{}, err
		}
	}
}

func waitTOTP(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) readTOTP(id string, then func(totp.Secret, bool)) {
	if id == "" || a.secretsUnavailable() {
		if then != nil {
			then(totp.Secret{}, false)
		}
		return
	}
	a.secrets.queue.do(func() {
		value, found, err := a.plat.SecretGet(hostSecretKey(id, secretTOTP))
		secret, invalid := totp.Parse(value)
		a.post(func() {
			if errors.Is(err, errors.ErrUnsupported) {
				a.secrets.state = storeDown
			}
			available := err == nil && found && invalid == nil
			a.setTOTPStatus(id, secret, found, available, err != nil || found && invalid != nil)
			if then != nil {
				then(secret, available)
			}
		})
	})
}

func (a *App) setTOTPStatus(id string, secret totp.Secret, present, available, failed bool) {
	if a.totpStatus == nil {
		a.totpStatus = map[string]totpStatus{}
	}
	now := time.Now()
	status := totpStatus{checked: now, present: present, available: available, failed: failed}
	if available {
		status.expires = now.Add(secret.Remaining(now))
		status.period = secret.Remaining(status.expires)
	}
	a.totpStatus[id] = status
}

func (a *App) openTOTPChallenge(ctx context.Context, host config.Host, prompt string, result chan<- string) {
	a.totpPromptSeq++
	kind := WindowKind(fmt.Sprintf("totp-prompt-%d", a.totpPromptSeq))
	var answer string
	var input string
	lastLock := a.lock.generation
	var stop func() bool
	spec := WindowSpec{Kind: kind, Title: locales.TotpPromptTitle, Width: 460, Height: 205, MinWidth: 350, MinHeight: 205, Modal: true}
	if sheets {
		spec.Title = func() string { return "" }
	}
	spec.Closed = func() {
		if stop != nil {
			stop()
		}
		result <- answer
	}
	a.openWindow(spec, func(c *ui.Context) {
		if lastLock != a.lock.generation {
			input, lastLock = "", a.lock.generation
		}
		a.totpPromptView(c, &input, host.Target(), safeTOTPPrompt(prompt), func(code string) {
			answer = code
			a.closeWindow(kind)
		})
	})
	stop = context.AfterFunc(ctx, func() { a.post(func() { a.closeWindow(kind) }) })
}

func safeTOTPPrompt(prompt string) string {
	var text strings.Builder
	count := 0
	for _, char := range prompt {
		if count == 240 {
			text.WriteRune('…')
			break
		}
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			char = ' '
		}
		text.WriteRune(char)
		count++
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func (a *App) totpPromptView(c *ui.Context, input *string, target, prompt string, finish func(string)) {
	ui.Column(c).Fill().Padding(16, 20).Gap(10).Children(func() {
		ui.Text(c, locales.TotpPromptTitle()).Bold().FontSize(17)
		ui.Text(c, target).SingleLine().TextColor(theme.TextMuted)
		ui.Scroll(c).FillWidth().MaxHeight(70).Children(func() {
			ui.Text(c, prompt)
		})
		field := ui.TextInput(c, input).Label(locales.TotpCode()).Placeholder(locales.TotpCode()).Password().FillWidth().AutoFocus()
		submit := field.Submitted()
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				finish("")
			}
			value := strings.TrimSpace(*input)
			if ui.PrimaryButton(c, locales.TotpSubmit()).Disabled(value == "" || len(value) > 128).Clicked() || submit && value != "" && len(value) <= 128 {
				*input = ""
				finish(value)
			}
		})
	})
}

func (a *App) totpMenu(c *ui.Context, menu *ui.Menu, kind WindowKind, host *config.Host) {
	if host.IsRDP() {
		return
	}
	if a.secretsUnavailable() {
		menu.Item(locales.TotpCopy()).Disabled(true).Subtitle(locales.TotpStoreError())
		return
	}
	status := a.totpStatus[host.ID]
	now := time.Now()
	if c.MenuPresentation() == ui.PopoverMenus && status.available {
		c.After(time.Second)
	}
	if !status.loading && now.Sub(status.checked) > 10*time.Second {
		if a.totpStatus == nil {
			a.totpStatus = map[string]totpStatus{}
		}
		status.loading = true
		a.totpStatus[host.ID] = status
		a.readTOTP(host.ID, nil)
	}
	var subtitle string
	switch {
	case status.available:
		left := status.expires.Sub(now)
		if left <= 0 {
			left = status.period - (-left)%status.period
		}
		subtitle = totpSeconds(left)
	case status.loading:
		subtitle = locales.TotpChecking()
	case status.failed:
		subtitle = locales.TotpStoreError()
	default:
		subtitle = locales.TotpNotSet()
	}
	if menu.Item(locales.TotpCopy()).Subtitle(subtitle).Disabled(!status.available).Chosen() {
		a.copyTOTP(kind, host.ID)
	}
}

func totpSeconds(left time.Duration) string {
	return locales.TotpSeconds(max(1, int((left+time.Second-1)/time.Second)))
}

func (a *App) copyTOTP(kind WindowKind, id string) {
	a.readTOTP(id, func(secret totp.Secret, available bool) {
		if a.Locked() || kind != WindowMain && !a.windowOpen(kind) {
			return
		}
		if !available {
			a.pendingTOTPNotice(kind, id)
			return
		}
		now := time.Now()
		if a.totpCopies == nil {
			a.totpCopies = map[WindowKind]totpClipboard{}
		}
		a.totpCopies[kind] = totpClipboard{id, secret.Code(now), now.Add(secret.Remaining(now))}
	})
}

func (a *App) pendingTOTPNotice(kind WindowKind, id string) {
	if a.totpCopies == nil {
		a.totpCopies = map[WindowKind]totpClipboard{}
	}
	a.totpCopies[kind] = totpClipboard{hostID: id}
}

func (a *App) writeTOTPCopy(c *ui.Context, kind WindowKind) {
	if item, ok := a.totpCopies[kind]; ok {
		delete(a.totpCopies, kind)
		if item.code == "" {
			if a.totpStatus[item.hostID].failed {
				c.Toast(locales.TotpStoreError())
			} else {
				c.Toast(locales.TotpNotSet())
			}
		} else if left := time.Until(item.expiry); left > 0 {
			c.WriteClipboard(item.code)
			c.Toast(locales.TotpCopied(totpSeconds(left)))
		} else {
			a.copyTOTP(kind, item.hostID)
		}
	}
}
