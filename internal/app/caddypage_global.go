package app

import (
	"fmt"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type caddyGlobalPage struct {
	source  caddySectionFiles
	group   int
	options [caddyGlobGroups]dockerResourceTable
	rows    [caddyGlobGroups][]caddyGlobalRow
	main    monitor.CaddyFile
	doc     *caddyfile.Document
	dialog  *systemDialog
	work    *systemDialog
}

func (a *App) caddyGlobalState(s *Session) *caddyGlobalPage {
	page := &s.caddy.global
	if page.source.client != s.client {
		if page.source.request != nil {
			page.source.request.cancel()
		}
		if page.dialog != nil && page.dialog.cancel != nil {
			page.dialog.cancel()
		}
		if page.work != nil && page.work.cancel != nil {
			page.work.cancel()
		}
		*page = caddyGlobalPage{group: page.group, source: caddySectionFiles{client: s.client, backend: page.source.backend}}
	}
	return page
}

func (a *App) refreshCaddyGlobal(s *Session) {
	page := a.caddyGlobalState(s)
	if page.work != nil {
		return
	}
	a.loadCaddySection(s, &page.source, func() {
		page.main, _ = page.source.main()
		page.doc, page.rows = nil, [caddyGlobGroups][]caddyGlobalRow{}
		if page.main.Path != "" && (page.source.inst.Adapter == "" || page.source.inst.Adapter == "caddyfile") {
			if doc, err := caddyfile.Parse(page.main.Content); err == nil {
				page.doc = doc
				page.rows = caddyGlobalRows(page.main, doc)
				page.rows[caddyGlobSnippets] = caddyGlobalSnippetRows(page.source.files)
			}
		}
		for index := range page.options {
			page.options[index].selected.Clear()
		}
	})
}

func caddyGlobalSelected(table *dockerResourceTable, rows []caddyGlobalRow) caddyGlobalRow {
	var selected caddyGlobalRow
	for _, row := range rows {
		if table.selected.Has(row.key) {
			if selected.key != "" {
				return caddyGlobalRow{}
			}
			selected = row
		}
	}
	return selected
}

func (page *caddyGlobalPage) blocked() string {
	if page.source.reading || page.work != nil {
		return locales.CglobBusy()
	}
	if !page.source.loaded || page.source.failure != "" {
		return locales.CglobSourceUnavailable()
	}
	if page.main.Path != "" {
		return caddyConfigWriteReason(page.source.inst, page.main.Path)
	}
	return ""
}

func (page *caddyGlobalPage) actionReason(row caddyGlobalRow, edit, removing bool) string {
	if reason := page.blocked(); reason != "" {
		return reason
	}
	if row.key == "" {
		return locales.CglobSelect()
	}
	if row.unknown {
		return locales.CglobReadOnly()
	}
	if removing && row.node == nil {
		return locales.CglobSetFirst()
	}
	if reason := caddyConfigWriteReason(page.source.inst, row.file.Path); row.file.Path != "" && reason != "" {
		return reason
	}
	if edit && row.protected {
		return locales.CglobProtected()
	}
	return ""
}

func (a *App) caddyGlobalSection(c *ui.Context, s *Session) {
	page := a.caddyGlobalState(s)
	if !page.source.loaded && !page.source.reading {
		a.refreshCaddyGlobal(s)
	}
	if page.source.reading {
		ui.Row(c).Padding(4, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(locales.CglobReading())
			widgets.Muted(c, locales.CglobReading()).FontSize(12)
		})
	} else if page.source.failure != "" {
		ui.Text(c, page.source.failure).Padding(4, 10).TextColor(theme.Danger).Selectable()
	}
	switch {
	case !page.source.loaded:
	case page.main.Path == "" && page.source.failure == "":
		widgets.Muted(c, locales.CglobNoConfig()).Padding(6, 10)
	case page.main.Path != "" && page.doc == nil:
		ui.Column(c).Padding(6, 10).Gap(5).Children(func() {
			widgets.Muted(c, locales.CglobRawOnly()).FillWidth()
			if reason := page.blocked(); ui.Button(c, locales.CglobCode()).Disabled(reason != "").Tooltip(reason).AlignSelf(ui.Start).Clicked() {
				a.caddyGlobalCode(s, page.main)
			}
		})
	case page.doc != nil:
		a.caddyGlobalTable(c, s, page)
	}
	a.systemDialogView(c.Key("caddy-global-dialog"), s, &page.dialog)
}

func (a *App) caddyGlobalTable(c *ui.Context, s *Session, page *caddyGlobalPage) {
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, locales.CglobGroup()).FontSize(12).Shrink(0)
		systemSelect(c.Key("caddy-global-group"), &page.group, locales.CglobGroup(), caddyGlobalGroups())
		row := caddyGlobalSelected(&page.options[page.group], page.rows[page.group])
		snippets := page.group == caddyGlobSnippets
		if snippets && caddyGlobalButton(c, locales.CglobSnippetNew(), page.blocked()) {
			a.caddyGlobalEditSnippet(s, nil)
		}
		if caddyGlobalRepeatable(row.name) && row.node != nil && caddyGlobalButton(c, locales.CglobAddInstance(), page.blocked()) {
			a.caddyGlobalAddInstance(s, row)
		}
		if caddyGlobalButton(c, locales.Edit(), page.actionReason(row, true, false)) {
			a.caddyGlobalEdit(s, row, snippets)
		}
		if caddyGlobalButton(c, locales.Delete(), page.actionReason(row, false, true)) {
			a.caddyGlobalRemove(s, row)
		}
		if caddyGlobalButton(c, locales.CglobCode(), page.blocked()) {
			if row.file.Path != "" {
				a.caddyGlobalCode(s, row.file)
			} else {
				a.caddyGlobalCode(s, page.main)
			}
		}
	})
	group := page.group
	rows := page.rows[group]
	if group == caddyGlobOther && len(rows) > 0 {
		widgets.Muted(c, locales.CglobReadOnly()).Padding(1, 8, 5).FontSize(12)
	}
	if group == caddyGlobSnippets && len(rows) == 0 {
		widgets.Muted(c, locales.CglobSnippetEmpty()).Padding(4, 10)
		return
	}
	if group == caddyGlobOther && len(rows) == 0 {
		widgets.Muted(c, locales.CglobNoOther()).Padding(4, 10)
		return
	}
	items := make([]dockerResourceRow, 0, len(rows))
	byKey := make(map[string]caddyGlobalRow, len(rows))
	columns := []ui.TableColumn{{ID: "name", Title: locales.CglobOption(), Width: 166, MinWidth: 120, Sortable: true},
		{ID: "state", Title: locales.CglobState(), Width: 95, MinWidth: 78, Sortable: true}, {ID: "value", Title: locales.CglobValue(), MinWidth: 95}}
	if group == caddyGlobSnippets {
		columns = []ui.TableColumn{{ID: "snippet", Title: locales.CglobSnippet(), Width: 160, MinWidth: 105, Sortable: true},
			{ID: "file", Title: locales.CglobLocation(), MinWidth: 150, Sortable: true}}
	}
	for _, row := range rows {
		byKey[row.key] = row
		var values []string
		if group == caddyGlobSnippets {
			values = []string{row.name, fmt.Sprintf("%s:%d", row.file.Path, row.node.Line)}
		} else {
			state, text := locales.CglobUnset(), row.preview
			if row.node != nil {
				state = locales.CglobSet()
				if row.protected {
					text = locales.CglobProtected()
				}
			}
			values = []string{row.name, state, text}
		}
		items = append(items, dockerResourceRow{key: row.key, name: row.name, values: values})
	}
	table := &page.options[group]
	items = table.shown(items, columns)
	table.draw(c.Key(group), caddyGlobalGroups()[group], columns, items, func(menu *ui.Menu, key string) {
		a.caddyGlobalMenu(menu, s, page, byKey[key], group == caddyGlobSnippets)
	}, func(key string) {
		row := byKey[key]
		if page.actionReason(row, true, false) == "" {
			a.caddyGlobalEdit(s, row, group == caddyGlobSnippets)
		}
	})
}

func caddyGlobalButton(c *ui.Context, label, reason string) bool {
	return ui.Button(c.Key(label), label).Disabled(reason != "").Tooltip(reason).Clicked() && reason == ""
}

func (a *App) caddyGlobalMenu(menu *ui.Menu, s *Session, page *caddyGlobalPage, row caddyGlobalRow, snippet bool) {
	if resourceMenuItem(menu, locales.Edit(), page.actionReason(row, true, false), false) {
		a.caddyGlobalEdit(s, row, snippet)
	}
	if caddyGlobalRepeatable(row.name) && row.node != nil && resourceMenuItem(menu, locales.CglobAddInstance(), page.blocked(), false) {
		a.caddyGlobalAddInstance(s, row)
	}
	if resourceMenuItem(menu, locales.Delete(), page.actionReason(row, false, true), true) {
		a.caddyGlobalRemove(s, row)
	}
	if resourceMenuItem(menu, locales.CglobCode(), page.blocked(), false) {
		a.caddyGlobalCode(s, row.file)
	}
}

func (a *App) caddyGlobalAddInstance(s *Session, row caddyGlobalRow) {
	if row.unknown || !caddyGlobalRepeatable(row.name) || row.node == nil {
		return
	}
	row.node, row.protected = nil, false
	a.caddyGlobalEdit(s, row, false)
}

func (a *App) caddyGlobalCode(s *Session, file monitor.CaddyFile) {
	a.caddySectionEditor(s, &s.caddy.global.source, file, func() { a.refreshCaddyGlobal(s) })
}
