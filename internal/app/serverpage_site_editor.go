package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) siteEditor(s *Session, file string, applied bool) {
	script, err := monitor.NginxReadCommand(file)
	if err != nil {
		return
	}
	page := &s.server.sites
	dialog := &systemDialog{client: s.client, title: locales.SiteEdit(), target: file, busy: true}
	page.dialog = dialog
	ctx, cancel := context.WithTimeout(systemContext(s), 30*time.Second)
	dialog.cancel = cancel
	run := a.sitesRoot(s)
	go func() {
		defer cancel()
		text, stderr, err := run(ctx, script, nil, nil)
		a.post(func() {
			if page.dialog != dialog || !systemReady(s, dialog.client) {
				return
			}
			dialog.busy = false
			if err != nil {
				dialog.failure, dialog.rootDenied = siteError(stderr, err), siteRootDenied(err)
			} else if !utf8.ValidString(text) || !monitor.NginxConfigSafe(text) || systemRedact(text) != text {
				dialog.failure = locales.SiteUnsafeText()
			} else {
				a.siteEditBody(s, dialog, file, text, applied)
			}
		})
	}()
}

func (a *App) siteEditBody(s *Session, dialog *systemDialog, file, original string, applied bool) {
	a.siteEditBodyText(s, dialog, file, original, original, applied)
}

// siteEditBodyText is the editor of file, whose content on the server is
// original, showing text: the user's edits again after a save that nginx
// refused and that was undone.
func (a *App) siteEditBodyText(s *Session, dialog *systemDialog, file, original, text string, applied bool) {
	page := &s.server.sites
	var editor func(*ui.Context, float32)
	editor = func(c *ui.Context, height float32) {
		ui.TextArea(c, &text).Label(locales.SiteConfig()).Font("monospace").FillWidth().Height(min(350, height/2)).AutoFocus()
		secret := siteClean(text) != text || !monitor.NginxOutputSafe(text)
		if secret {
			text = original
			dialog.failure = locales.SiteUnsafeText()
		}
		change, err := monitor.NewNginxChange(file, original, text, time.Now(), page.inventory.NginxTree)
		valid := err == nil && !secret && utf8.ValidString(text)
		if !valid {
			ui.Text(c, locales.SiteUnsafeText()).TextColor(theme.Warning)
		}
		if ui.PrimaryButton(c, locales.Save()).Disabled(!valid || text == original).AlignSelf(ui.End).Clicked() {
			script, err := change.SaveCommand(original, page.inventory.NginxTree)
			if err != nil {
				return
			}
			reload := applied && page.status.State == "active"
			dialog.body, dialog.consequence = nil, locales.SiteSaveWarning(change.Backup)+" "+siteApplyHint(reload)
			dialog.back = func() { dialog.body, dialog.consequence, dialog.confirm = editor, "", nil }
			attempted := text
			dialog.confirm = func() {
				a.siteWriteConfig(s, dialog, change, script, attempted, reload, false, func() {
					a.siteEditBodyText(s, dialog, file, original, attempted, applied)
				})
			}
		}
	}
	dialog.body = editor
}

// siteWriteConfig writes change (or restores its backup, for restore) and
// tests the configuration, reloading nginx when reload. A change nginx
// refuses is undone at once, so that the file on disk never stays broken
// for the next start of nginx; edit then shows the user's text again.
func (a *App) siteWriteConfig(s *Session, dialog *systemDialog, change monitor.NginxChange, script, text string, reload, restore bool, edit func()) {
	page := &s.server.sites
	var undo string
	if !restore {
		undo, _ = change.RestoreCommand(page.inventory.NginxTree, time.Now())
	}
	test, _ := monitor.NginxActionCommand("test", page.status.Unit, page.inventory.Main)
	apply, _ := monitor.NginxActionCommand("reload", page.status.Unit, page.inventory.Main)
	if reload && !restore {
		test = apply
	}
	if test == "" {
		dialog.failure = locales.SiteNeedConfig()
		return
	}
	dialog.busy, dialog.done, dialog.body, dialog.back = true, false, nil, nil
	dialog.consequence, dialog.output, dialog.failure = "", "", ""
	ctx, cancel := context.WithTimeout(systemContext(s), time.Minute)
	dialog.cancel = cancel
	run := a.sitesRoot(s)
	go func() {
		defer cancel()
		var input *strings.Reader
		if !restore {
			input = strings.NewReader(text)
		}
		var out, stderr string
		var err error
		if input == nil {
			out, stderr, err = run(ctx, script, nil, nil)
		} else {
			out, stderr, err = run(ctx, script, input, nil)
		}
		written := strings.TrimSpace(out) == "finallyshell-written"
		if err == nil && written {
			out, stderr, err = run(ctx, test, nil, nil)
		} else {
			out = ""
		}
		undone := false
		if err != nil && written && !restore && undo != "" {
			back, _, undoErr := run(ctx, undo, nil, nil)
			undone = undoErr == nil && strings.TrimSpace(back) == "finallyshell-written"
		}
		a.post(func() {
			if !systemReady(s, dialog.client) {
				return
			}
			dialog.busy = false
			dialog.rootDenied = siteRootDenied(err)
			dialog.failure = siteError(stderr, err)
			if written || restore {
				dialog.output = locales.SiteBackup(change.Backup)
			}
			if result := strings.TrimSpace(siteClean(out)); result != "" {
				dialog.output += "\n" + result
			}
			if err == nil && written {
				page.recovery = nil
				dialog.output += "\n" + strings.TrimSpace(siteClean(stderr))
				dialog.done = true
				a.refreshServerSites(s)
				return
			}
			if undone {
				page.recovery = nil
				dialog.output += "\n" + locales.SiteAutoRestored()
				if edit != nil && page.dialog == dialog {
					dialog.title = locales.SiteApplyFailed()
					edit()
				}
				a.refreshServerSites(s)
				return
			}
			if !restore && written {
				page.recovery = &change
				if page.dialog == dialog {
					a.siteOfferRestore(s, dialog, change)
				}
			}
			if err == nil && !written {
				dialog.failure = locales.SiteUnverifiedWrite()
			}
			if restore && written {
				page.recovery = nil
				a.refreshServerSites(s)
			}
		})
	}()
}

func (a *App) siteOfferRestore(s *Session, dialog *systemDialog, change monitor.NginxChange) {
	page := &s.server.sites
	dialog.title = locales.SiteApplyFailed()
	dialog.body = func(c *ui.Context, _ float32) {
		if ui.Button(c, locales.SiteRestore()).AlignSelf(ui.Start).Clicked() {
			now := time.Now()
			script, err := change.RestoreCommand(page.inventory.NginxTree, now)
			if err != nil {
				dialog.failure = locales.SiteRestoreUnavailable()
				return
			}
			previous := dialog.body
			previousTarget := dialog.target
			dialog.target = change.Backup + " → " + change.File
			dialog.consequence = locales.SiteRestoreWarning(change.RestoreBackup(now))
			dialog.back = func() {
				dialog.target, dialog.body, dialog.confirm, dialog.consequence = previousTarget, previous, nil, ""
			}
			dialog.confirm = func() { a.siteWriteConfig(s, dialog, change, script, "", false, true, nil) }
			dialog.body = nil
		}
	}
}

func (a *App) siteNew(s *Session) {
	page := &s.server.sites
	dialog := &systemDialog{client: s.client, title: locales.SiteNew()}
	page.dialog = dialog
	var name, domains, root, port, certificate, key string
	var template int
	var tls bool
	var form func(*ui.Context, float32)
	form = func(c *ui.Context, height float32) {
		values := monitor.NginxTemplate{Names: strings.Fields(domains), Root: strings.TrimSpace(root)}
		validPort := true
		ui.Scroll(c).MaxHeight(height / 2).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				systemSelect(c, &template, locales.SiteTemplate(), []string{locales.SiteStatic(), locales.SiteProxy()})
				siteField(c, locales.SiteName(), &name)
				siteField(c, locales.SiteDomains(), &domains)
				if template == 0 {
					siteField(c, locales.SiteDocumentRoot(), &root)
				} else {
					siteField(c, locales.SiteProxyPort(), &port)
					values.Port, _ = strconv.Atoi(strings.TrimSpace(port))
					validPort = values.Port > 0
				}
				ui.Checkbox(c, &tls, locales.SiteUseTLS())
				if tls {
					siteField(c, locales.SiteCertPath(), &certificate)
					siteField(c, locales.SiteKeyPath(), &key)
					values.Certificate, values.Key = strings.TrimSpace(certificate), strings.TrimSpace(key)
				}
			})
		})
		values.Root, values.Names = strings.TrimSpace(root), strings.Fields(domains)
		reload := page.status.State == "active"
		file, link, err := page.inventory.NewSite(name)
		text, renderErr := values.Render()
		script, commandErr := page.inventory.CreateCommand(name, text, reload, page.status.Unit)
		valid := err == nil && renderErr == nil && commandErr == nil && validPort && (!tls || values.Certificate != "" && values.Key != "")
		if !valid {
			ui.Text(c, locales.SiteFormHint()).FontSize(12).TextColor(theme.TextMuted)
		}
		if ui.PrimaryButton(c, locales.SiteCreate()).Disabled(!valid).AlignSelf(ui.End).Clicked() {
			dialog.target = fmt.Sprintf("%s\n%s", file, strings.Join(values.Names, ", "))
			if link != "" {
				dialog.target += "\n" + link + " → " + file
			}
			dialog.body, dialog.consequence = nil, locales.SiteCreateWarning()+" "+siteApplyHint(reload)
			dialog.back = func() { dialog.body, dialog.target, dialog.consequence, dialog.confirm = form, "", "", nil }
			dialog.confirm = func() { a.siteWork(s, dialog, script, strings.NewReader(text), true) }
		}
	}
	dialog.body = form
}

func siteField(c *ui.Context, label string, value *string) {
	ui.Text(c.Key(label), label).FontSize(12)
	ui.TextInput(c.Key(label+"-input"), value).Label(label).FillWidth()
}
