package app

import (
	"path/filepath"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/totp"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type totpEditAction int

const (
	totpKeep totpEditAction = iota
	totpRemove
)

func totpIDFromKey(key string) (string, bool) {
	path, ok := strings.CutPrefix(key, "host/")
	id, name, valid := strings.Cut(path, "/")
	return id, ok && valid && id != "" && name == secretTOTP
}

func (a *App) totpEditorField(c *ui.Context, ed *connEditor, input func(ui.Element) ui.Element) {
	status := a.totpStatus[ed.id]
	description := locales.TotpFieldHint()
	switch {
	case ed.totpChange == totpRemove:
		description = locales.TotpRemovePending()
	case status.available:
		description = locales.TotpStored()
	case status.failed:
		description = locales.TotpStoreError()
	case a.secretsUnavailable():
		description = locales.TotpStoreError()
	}
	ui.Field(c, locales.TotpField(), func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			input(ui.TextInput(c, &ed.totpText).Label(locales.TotpField()).Password().Placeholder(locales.TotpPlaceholder()).Grow(1).MinWidth(0).Disabled(ed.totpBusy || ed.totpChange == totpRemove || a.secretsUnavailable()))
			if status.present {
				label := locales.TotpRemove()
				if ed.totpChange == totpRemove {
					label = locales.TotpKeep()
				}
				if ui.Button(c, label).Disabled(ed.totpBusy).Shrink(0).Clicked() {
					ed.totpText = ""
					if ed.totpChange == totpRemove {
						ed.totpChange = totpKeep
					} else {
						ed.totpChange = totpRemove
					}
				}
			}
		})
	}).Description(description)
}

func (a *App) confirmTOTPEdit(ed *connEditor) {
	remove := ed.totpChange == totpRemove
	secret, err := totp.Parse(ed.totpText)
	if !remove && err != nil {
		ed.nav.Page, ed.saveErr = editorSSH, locales.TotpInvalid
		return
	}
	host := config.Host{Name: strings.TrimSpace(ed.name), Addr: strings.TrimSpace(ed.addr), User: strings.TrimSpace(ed.user)}
	if ed.id != "" {
		if current := a.hosts.Host(ed.id); current == nil || current.Deleted() {
			ed.saveErr = locales.ErrHostDeleted
			return
		}
	}
	title, detail := locales.TotpSave(), locales.TotpSaveDetail(host.Title())
	if remove {
		title, detail = locales.TotpRemove(), locales.TotpRemoveDetail(host.Title())
	}
	q := Confirmation{Title: locales.TotpField(), Message: title, Detail: detail, OK: title, Cancel: locales.Cancel(), RequireExplicit: true, Parent: WindowEditor}
	ed.totpBusy, ed.saveErr = true, nil
	go func() {
		confirmed := a.plat.Confirm(q)
		a.post(func() {
			if a.editor != ed || !a.windowOpen(WindowEditor) || !confirmed || a.Locked() {
				ed.totpBusy = false
				return
			}
			if err := backupKeyHostConfig(filepath.Join(config.Dir(), "hosts.json"), a.lock.clock()); err != nil {
				ed.totpBusy, ed.saveErr = false, locales.TotpBackupError
				return
			}
			id := ed.id
			if id == "" {
				id = config.NewHostID()
			}
			a.saveTOTPEdit(ed, id, secret, remove)
		})
	}()
}

func (a *App) saveTOTPEdit(ed *connEditor, id string, secret totp.Secret, remove bool) {
	key := hostSecretKey(id, secretTOTP)
	a.secrets.queue.do(func() {
		old, existed, err := a.plat.SecretGet(key)
		if err == nil {
			if remove {
				err = a.plat.SecretDelete(key)
			} else {
				err = a.plat.SecretSet(key, secret.Encode())
			}
		}
		a.post(func() {
			ed.totpBusy = false
			if err != nil {
				ed.saveErr = locales.TotpStoreError
				return
			}
			if a.lock.resetting {
				if a.hosts.Host(id) == nil {
					a.deleteSecret(key)
				}
				return
			}
			ed.totpText, ed.totpChange = "", totpKeep
			if a.editor == ed && a.windowOpen(WindowEditor) && (a.hosts.Host(id) != nil || ed.id == "") {
				a.setTOTPStatus(id, secret, !remove, !remove, false)
				a.sync.again, a.account.sync.again = true, true
				if a.saveEditorFields(ed, id) {
					return
				}
			}
			delete(a.totpStatus, id)
			restore := existed && (ed.id == "" || a.hosts.Host(id) != nil)
			a.secrets.queue.do(func() {
				var rollback error
				if restore {
					rollback = a.plat.SecretSet(key, old)
				} else {
					rollback = a.plat.SecretDelete(key)
				}
				if rollback != nil {
					a.post(func() { ed.saveErr = locales.TotpStoreError })
				}
			})
		})
	})
}
