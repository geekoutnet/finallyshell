package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) caddyGlobalEdit(s *Session, row caddyGlobalRow, snippet bool) {
	page := &s.caddy.global
	if page.actionReason(row, true, false) != "" {
		return
	}
	if snippet {
		a.caddyGlobalEditSnippet(s, &row)
		return
	}
	form := &caddyGlobalForm{row: row, text: row.doc.Text(row.node), mode: row.option.form}
	form.setup()
	label := locales.Edit()
	if row.node == nil && len(row.doc.GlobalOptions(row.name)) != 0 {
		label = locales.CglobAddInstance()
	}
	dialog := &systemDialog{client: s.client, title: label + " · " + row.name, target: row.file.Path}
	page.dialog = dialog
	dialog.body = func(c *ui.Context, height float32) {
		ui.Scroll(c).MaxHeight(max(height-245, 120)).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(0, 8, 0, 0).Gap(7).Children(func() {
				form.draw(c, height, a, s, dialog)
			})
		})
		preview := strings.TrimSpace(form.render())
		if form.mode == caddyGlobSyntax {
			preview = caddyGlobalRestoreIndent(row.doc, row.node, preview)
		}
		updated, err := caddyGlobalOptionEdit(row.doc, row.node, row.option, preview)
		invalid := err != nil && strings.TrimSpace(preview) != ""
		if invalid {
			ui.Text(c, locales.CglobInvalidOption()).TextColor(theme.Warning).FontSize(12)
		}
		disabled := invalid || updated == row.file.Content || form.fetching || page.blocked() != ""
		if ui.PrimaryButton(c, locales.Save()).Disabled(disabled).AlignSelf(ui.End).Clicked() && !disabled {
			warning := locales.CglobSaveWarning()
			remove := row.node != nil && strings.TrimSpace(preview) == ""
			if remove {
				warning = locales.CglobRemoveWarning()
			}
			a.caddyGlobalConfirm(s, dialog, row.file, row.name, updated, warning)
		}
	}
}

type caddyGlobalForm struct {
	row          caddyGlobalRow
	mode         caddyGlobalFormKind
	text         string
	value        string
	initialValue string
	enabled      bool
	fetching     bool
	fetch        *caddyGlobalProxyRequest
	feedback     string
	nested       caddyGlobalNestedForm
}

type caddyGlobalProxyRequest struct {
	cancel context.CancelFunc
}

func (form *caddyGlobalForm) setup() {
	row := form.row
	form.text = caddyGlobalDedent(row.doc, row.node)
	if row.node == nil {
		return
	}
	if row.node.Block || form.mode == caddyGlobFlag && len(row.node.Args) != 0 {
		form.mode = caddyGlobSyntax
		return
	}
	form.enabled = true
	if after := strings.TrimSpace(strings.TrimPrefix(form.text, row.name)); after != form.text {
		form.value = after
	} else {
		form.mode = caddyGlobSyntax
	}
	if form.mode == caddyGlobChoice && !slices.Contains(row.option.choices, form.value) {
		form.mode = caddyGlobSyntax
	}
	form.initialValue = form.value
}

func (form *caddyGlobalForm) render() string {
	if form.mode == caddyGlobSyntax {
		return form.text
	}
	if form.mode == caddyGlobFlag {
		if form.enabled {
			return form.row.name
		}
		return ""
	}
	if value := strings.TrimSpace(form.value); value != "" {
		if form.row.node != nil && form.value == form.initialValue {
			return form.row.doc.Text(form.row.node)
		}
		return form.row.name + " " + value
	}
	return ""
}

func (form *caddyGlobalForm) draw(c *ui.Context, height float32, a *App, s *Session, dialog *systemDialog) {
	row := form.row
	switch row.name {
	case "admin":
		widgets.Muted(c, locales.CglobAdminWarning()).FontSize(12).FillWidth()
	case "on_demand_tls":
		widgets.Muted(c, locales.CglobOnDemandWarning()).FontSize(12).FillWidth()
	}
	switch form.mode {
	case caddyGlobFlag:
		ui.Checkbox(c, &form.enabled, locales.CglobEnabled())
	case caddyGlobValue:
		placeholder := strings.TrimPrefix(row.option.example, row.name+" ")
		ui.TextInput(c, &form.value).FillWidth().Label(locales.CglobValue()).Placeholder(placeholder).Font("monospace")
	case caddyGlobChoice:
		choices := append([]string{locales.CglobUnset()}, row.option.choices...)
		selected := form.value
		if selected == "" {
			selected = choices[0]
		}
		if ui.Select(c, &selected, choices).Label(locales.CglobValue()).MaxWidth(270).Changed() {
			form.value = selected
			if selected == choices[0] {
				form.value = ""
			}
		}
	default:
		if row.name == "servers" {
			form.proxyTools(c, a, s, dialog)
		}
		form.drawNested(c, height)
		if form.text == "" && ui.Button(c, locales.CglobExample()).Disabled(form.fetching).AlignSelf(ui.Start).Clicked() {
			form.text = row.option.example
		}
		ui.Text(c, locales.CglobSyntax()).FontSize(12)
		ui.TextArea(c, &form.text).Font("monospace").Label(locales.CglobSyntax()).FillWidth().Height(min(220, max(height/3, 85))).Disabled(form.fetching)
	}
}

func (form *caddyGlobalForm) proxyTools(c *ui.Context, a *App, s *Session, dialog *systemDialog) {
	widgets.Muted(c, locales.CglobProxyWarning()).FontSize(12).FillWidth()
	ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
		if ui.Button(c, locales.CglobPrivate()).Disabled(form.fetching).Clicked() {
			form.setProxyPreset([]string{"private_ranges"})
		}
		if ui.Button(c, locales.CglobCloudflare()).Disabled(form.fetching).Clicked() {
			form.fetchProxies(a, s, dialog)
		}
	})
	if form.fetching {
		ui.Row(c).Gap(6).Wrap().Children(func() {
			widgets.Muted(c, locales.CglobRangeReading()).FontSize(12)
			if ui.Button(c, locales.CglobProxyCancel()).Clicked() && dialog.cancel != nil {
				form.cancelFetch(dialog)
			}
		})
	} else if form.feedback != "" {
		ui.Text(c, form.feedback).TextColor(theme.Warning).FontSize(12)
	}
}

func (form *caddyGlobalForm) cancelFetch(dialog *systemDialog) {
	if form.fetch != nil {
		form.fetch.cancel()
	}
	dialog.cancel, form.fetching, form.fetch = nil, false, nil
}

func (form *caddyGlobalForm) setProxyPreset(ranges []string) {
	text := form.text
	if text == "" {
		text = "servers {\n}"
	}
	updated, err := caddyGlobalProxyPreset(text, ranges)
	if err != nil {
		form.feedback = locales.CglobEditRangeError()
		return
	}
	form.text, form.feedback = updated, ""
}

func (form *caddyGlobalForm) fetchProxies(a *App, s *Session, dialog *systemDialog) {
	form.fetching, form.feedback = true, ""
	ctx, cancel := context.WithTimeout(systemContext(s), 8*time.Second)
	request := &caddyGlobalProxyRequest{cancel: cancel}
	form.fetch = request
	dialog.cancel = cancel
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	go func() {
		defer cancel()
		ranges, err := caddyGlobalCloudflareRanges(ctx, client)
		a.post(func() {
			if s.caddy.global.dialog != dialog || form.fetch != request || !systemReady(s, dialog.client) {
				return
			}
			form.fetching, dialog.cancel, form.fetch = false, nil, nil
			if err != nil {
				form.feedback = locales.CglobRangeError()
				return
			}
			form.setProxyPreset(ranges)
		})
	}()
}

func (a *App) caddyGlobalEditSnippet(s *Session, old *caddyGlobalRow) {
	page := &s.caddy.global
	if page.blocked() != "" || page.doc == nil {
		return
	}
	file, doc := page.main, page.doc
	base := "shared"
	for index := 2; caddyGlobalSnippetConflict(page.rows[caddyGlobSnippets], nil, base); index++ {
		base = fmt.Sprintf("shared-%d", index)
	}
	text, name, title := "("+base+") {\n\tencode zstd gzip\n}", "", locales.CglobSnippetNew()
	if old != nil {
		file, doc, name = old.file, old.doc, old.name
		text, title = doc.Text(old.node), locales.Edit()+" · ("+name+")"
	}
	dialog := &systemDialog{client: s.client, title: title, target: file.Path}
	page.dialog = dialog
	dialog.body = func(c *ui.Context, height float32) {
		if old != nil {
			widgets.Muted(c, locales.CglobKeepName()).FontSize(12)
		}
		ui.Text(c, locales.CglobSyntax()).FontSize(12)
		ui.TextArea(c, &text).Font("monospace").Label(locales.CglobSyntax()).Height(min(220, max(height/3, 85))).FillWidth()
		trimmed := strings.TrimSpace(text)
		nextName := caddyGlobalSnippetName(trimmed)
		valid := nextName != "" && (name == "" || nextName == name) && !caddyGlobalSecret(nextName, text) && !caddyGlobalSnippetConflict(page.rows[caddyGlobSnippets], old, nextName)
		if !valid {
			widgets.Muted(c, locales.CglobInvalidSnippet()).TextColor(theme.Warning).FontSize(12)
		}
		unchanged := old != nil && trimmed == old.doc.Text(old.node)
		disabled := !valid || unchanged || page.blocked() != ""
		if !ui.PrimaryButton(c, locales.Save()).Disabled(disabled).AlignSelf(ui.End).Clicked() || disabled {
			return
		}
		updated, err := caddyGlobalWriteSnippet(doc, old, trimmed)
		if err != nil {
			dialog.failure = locales.CglobInvalidSnippet()
			return
		}
		a.caddyGlobalConfirm(s, dialog, file, "("+nextName+")", updated, locales.CglobSaveWarning())
	}
}

func caddyGlobalWriteSnippet(doc *caddyfile.Document, old *caddyGlobalRow, text string) (string, error) {
	if old != nil {
		return doc.Replace(old.node, caddyGlobalNewlines(doc.Source, text))
	}
	newline := caddyGlobalNewlines(doc.Source, "\n")
	for _, node := range doc.Nodes {
		if node.Kind == caddyfile.Global {
			continue
		}
		position := strings.LastIndexByte(doc.Source[:node.Start], '\n') + 1
		if position == 0 && strings.HasPrefix(doc.Source, "\ufeff") {
			position = len("\ufeff")
		}
		if strings.TrimSpace(doc.Source[position:node.Start]) != "" {
			position = node.Start
			text = newline + text
		}
		return doc.Source[:position] + caddyGlobalNewlines(doc.Source, text) + newline + newline + doc.Source[position:], nil
	}
	return doc.Append(text)
}

func (a *App) caddyGlobalRemove(s *Session, row caddyGlobalRow) {
	page := &s.caddy.global
	if page.actionReason(row, false, true) != "" {
		return
	}
	updated, err := row.doc.Replace(row.node, "")
	if err != nil {
		return
	}
	name := row.name
	if row.node.Kind == caddyfile.Snippet {
		name = "(" + name + ")"
	}
	dialog := &systemDialog{client: s.client, title: locales.Delete()}
	page.dialog = dialog
	a.caddyGlobalConfirm(s, dialog, row.file, name, updated, locales.CglobRemoveWarning())
}

func (a *App) caddyGlobalConfirm(s *Session, dialog *systemDialog, file monitor.CaddyFile, name, updated, warning string) {
	dialog.target = fmt.Sprintf("%s\n%s", name, file.Path)
	dialog.consequence, dialog.failure, dialog.output = warning, "", ""
	body := dialog.body
	dialog.body = nil
	if body != nil {
		dialog.back = func() {
			dialog.body, dialog.target, dialog.consequence, dialog.confirm = body, file.Path, "", nil
		}
	}
	dialog.confirm = func() { a.caddyGlobalApply(s, dialog, file, updated) }
}

func (a *App) caddyGlobalApply(s *Session, dialog *systemDialog, file monitor.CaddyFile, update string) {
	page := &s.caddy.global
	if !systemReady(s, dialog.client) || page.blocked() != "" || !caddySectionPath(file.Path) || !slices.Contains(page.source.files, file) {
		dialog.failure = locales.CglobSourceUnavailable()
		return
	}
	ctx, cancel := context.WithTimeout(systemContext(s), 2*time.Minute)
	page.work = dialog
	dialog.busy, dialog.cancel, dialog.body, dialog.back = true, cancel, nil, nil
	change := monitor.CaddyChange{
		Writes: []monitor.CaddyFile{{Path: file.Path, Content: update}},
		Expect: map[string]string{file.Path: file.Content}, Reload: true,
	}
	progress, _ := a.caddySectionProgress(dialog, func() bool { return page.work == dialog && page.dialog == dialog })
	a.runCaddySectionApply(s, &page.source, change, func(result monitor.CaddyApplyResult, err error) {
		cancel()
		if page.work != dialog {
			return
		}
		page.work = nil
		if !systemReady(s, dialog.client) {
			return
		}
		dialog.busy, dialog.done, dialog.cancel = false, true, nil
		dialog.rootDenied = errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword)
		dialog.failure = systemError("", err)
		dialog.output = systemTail(strings.TrimSpace(dialog.output+"\n"+systemRedact(result.Validate)), 65536)
		if result.Backup != "" {
			backup := locales.SiteBackup(result.Backup)
			if !strings.Contains(dialog.output, backup) {
				dialog.output += "\n" + backup
			}
		}
		if result.RolledBack {
			dialog.output += "\n" + locales.CglobRollback()
		}
		if err == nil {
			dialog.output += "\n" + locales.SvcDone()
		}
		a.refreshCaddyGlobal(s)
	}, ctx, progress)
}
