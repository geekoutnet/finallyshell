package app

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type caddySitesPage struct {
	caddySectionFiles
	table       dockerResourceTable
	rows        []caddySiteRow
	mainDoc     *caddyfile.Document
	parseFailed bool
	dialog      *systemDialog
	form        *caddySiteForm
	phpSockets  []string
	phpReading  bool
	work        bool
}

type caddySiteRow struct {
	id   string
	file monitor.CaddyFile
	doc  *caddyfile.Document
	node *caddyfile.Node
	spec caddyfile.SiteSpec
	raw  bool
}

func (row caddySiteRow) name() string { return strings.Join(row.node.Args, ", ") }

func (row caddySiteRow) active() bool { return row.node.Kind == caddyfile.Site }

func (page *caddySitesPage) inventory() {
	page.rows, page.mainDoc, page.parseFailed = nil, nil, false
	page.table.selected.Clear()
	if page.inst.Adapter != "" && page.inst.Adapter != "caddyfile" {
		return
	}
	docs := make(map[string]*caddyfile.Document, len(page.files))
	paths := make(map[string]monitor.CaddyFile, len(page.files))
	snippets := make(map[string]bool)
	for _, file := range page.files {
		if _, seen := paths[file.Path]; seen {
			continue
		}
		paths[file.Path] = file
		doc, err := caddyfile.Parse(file.Content)
		if err == nil {
			docs[file.Path] = doc
			for _, snippet := range doc.Snippets() {
				snippets[snippet.Name] = true
			}
		}
	}
	page.mainDoc = docs[page.inst.Config]
	if page.mainDoc == nil {
		page.parseFailed = len(page.files) > 0
		return
	}
	visited := map[string]bool{}
	var visit func(string)
	visit = func(filename string) {
		if visited[filename] {
			return
		}
		visited[filename] = true
		doc := docs[filename]
		if doc == nil {
			page.parseFailed = true
			return
		}
		for _, node := range doc.Nodes {
			if node.Kind == caddyfile.Import && len(node.Args) > 0 {
				if node.Block {
					page.parseFailed = true
					continue
				}
				pattern := node.Args[0]
				if snippets[pattern] {
					continue
				}
				if !path.IsAbs(pattern) {
					pattern = path.Join(path.Dir(filename), pattern)
				}
				matched := false
				for imported := range paths {
					if match, _ := path.Match(pattern, imported); match {
						matched = true
						visit(imported)
					}
				}
				if !matched && !strings.ContainsAny(pattern, "*?[") {
					page.parseFailed = true
				}
			}
		}
		for _, node := range doc.Sites() {
			active := node
			if node.Kind == caddyfile.Disabled {
				copy := *node
				copy.Kind = caddyfile.Site
				active = &copy
			}
			spec, err := caddyfile.ParseSite(active)
			page.rows = append(page.rows, caddySiteRow{
				id: fmt.Sprintf("%s:%d", filename, node.Start), file: paths[filename], doc: doc,
				node: node, spec: spec, raw: err != nil || caddySiteProtected(spec) || doc.HasComments(node),
			})
		}
	}
	visit(page.inst.Config)
}

func caddySiteProtected(spec caddyfile.SiteSpec) bool {
	for _, value := range append(slices.Clone(spec.Upstreams), spec.RedirectURL, spec.PHPUpstream) {
		if systemRedact(value) != value {
			return true
		}
	}
	for _, values := range [][]caddyfile.Header{spec.Headers, spec.Proxy.HeaderUp, spec.Proxy.HeaderDown} {
		for _, header := range values {
			if header.Value != "" && systemRedact(header.Name+": "+header.Value) != header.Name+": "+header.Value {
				return true
			}
		}
	}
	return false
}

func (a *App) refreshCaddySites(s *Session) {
	page := a.caddySitesState(s)
	if !page.work {
		a.loadCaddySection(s, &page.caddySectionFiles, page.inventory)
	}
}

func (a *App) caddySitesState(s *Session) *caddySitesPage {
	page := &s.caddy.sites
	if page.client != s.client {
		if page.request != nil {
			page.request.cancel()
		}
		if page.dialog != nil && page.dialog.cancel != nil {
			page.dialog.cancel()
		}
		if page.form != nil {
			page.form.clearPasswords()
		}
		*page = caddySitesPage{caddySectionFiles: caddySectionFiles{client: s.client, backend: page.backend}}
	}
	return page
}

func (a *App) caddySitesSection(c *ui.Context, s *Session) {
	page := a.caddySitesState(s)
	if !page.loaded && !page.reading {
		a.refreshCaddySites(s)
	}
	box := ui.Column(c.Key("caddy-sites-layout")).FillWidth().Grow(1).MinHeight(0)
	width := box.LayoutBounds().W
	box.Children(func() {
		if page.reading {
			ui.Spinner(c).Label(locales.CsiteReading()).Margin(8)
		}
		if page.failure != "" {
			ui.Text(c, page.failure).TextColor(theme.Danger).SingleLine().Margin(4, 8).Tooltip(page.failure)
		}
		if page.loaded && page.mainDoc == nil {
			message := locales.CsiteNoConfig()
			if file, ok := page.main(); ok {
				message = locales.CsiteRawConfig()
				if reason := caddyConfigWriteReason(page.inst, file.Path); ui.Button(c, locales.CsiteCode()).Disabled(reason != "").Tooltip(reason).AlignSelf(ui.Start).Margin(6, 8).Clicked() {
					a.caddySectionEditor(s, &page.caddySectionFiles, file, func() { a.refreshCaddySites(s) })
				}
			}
			ui.Text(c, message).Margin(4, 8).FillWidth()
		}
		if page.mainDoc != nil {
			a.caddySitesTable(c, s, width > 0 && width < 840)
		}
		a.systemDialogView(c.Key("caddy-site-dialog"), s, &page.dialog)
		if page.dialog == nil && page.form != nil {
			page.form.clearPasswords()
			page.form = nil
		}
	})
}

type caddySiteAction struct {
	title, reason string
	danger        bool
	run           func()
}

func (page *caddySitesPage) find(id string) *caddySiteRow {
	for index := range page.rows {
		if page.rows[index].id == id {
			return &page.rows[index]
		}
	}
	return nil
}

func (page *caddySitesPage) blocked() string {
	switch {
	case page.reading || page.work:
		return locales.CsiteBusy()
	case page.failure != "" || !page.loaded:
		return locales.CsiteRefreshFirst()
	default:
		return ""
	}
}

func (a *App) caddySitesTable(c *ui.Context, s *Session, narrow bool) {
	page := &s.caddy.sites
	cols := []ui.TableColumn{
		resourceColumn("addresses", locales.CsiteAddresses(), 0),
		resourceColumn("kind", locales.CsiteKind(), 125),
		resourceColumn("target", locales.CsiteTarget(), 0),
		resourceColumn("tls", "TLS", 95),
		resourceColumn("features", locales.CsiteFeatures(), 112),
		resourceColumn("state", locales.SiteState(), 88),
		resourceColumn("location", locales.CsiteLocation(), 220),
	}
	columns := []int{0, 1, 2, 3, 4, 5, 6}
	if narrow {
		cols = []ui.TableColumn{cols[0], cols[1], cols[5]}
		cols[1].Width, cols[2].Width = 95, 82
		columns = []int{0, 1, 5}
	}
	var rows []dockerResourceRow
	for _, row := range page.rows {
		values := row.columns()
		shown := make([]string, len(columns))
		for index, col := range columns {
			shown[index] = values[col]
		}
		if query := strings.ToLower(strings.TrimSpace(page.table.query)); query != "" && !strings.Contains(strings.ToLower(strings.Join(values, " ")), query) {
			continue
		}
		rows = append(rows, dockerResourceRow{key: row.id, name: row.name(), values: shown})
	}
	query := page.table.query
	page.table.query = ""
	rows = page.table.shown(rows, cols)
	page.table.query = query
	var selected *caddySiteRow
	for _, row := range rows {
		if page.table.selected.Has(row.key) {
			if selected != nil {
				selected = nil
				break
			}
			selected = page.find(row.key)
		}
	}
	add := func() { a.caddySiteOpenForm(s, nil, false, false) }
	templates := func() { a.caddySiteOpenForm(s, nil, false, true) }
	actions := a.caddySiteActions(s, selected)
	ui.Row(c).FillWidth().Gap(6).Padding(4, 8).Wrap().AlignItems(ui.Center).Children(func() {
		ui.SearchField(c, &page.table.query).Grow(1).MinWidth(85).Placeholder(locales.CsiteFilter()).Label(locales.CsiteFilter())
		if reason := page.newSiteReason(); ui.PrimaryButton(c, locales.CsiteNew()).Disabled(reason != "").Tooltip(reason).Clicked() {
			add()
		}
		if reason := page.newSiteReason(); ui.Button(c, locales.CsiteTemplates()).Disabled(reason != "").Tooltip(reason).Clicked() {
			templates()
		}
		edit := actions[0]
		if ui.Button(c, edit.title).Disabled(edit.reason != "").Tooltip(edit.reason).Clicked() {
			edit.run()
		}
		ui.MenuButton(c, locales.CsiteActions(), func(menu *ui.Menu) { caddySiteMenu(menu, actions) })
	})
	if page.parseFailed {
		ui.Text(c, locales.CsiteSomeRaw()).Margin(2, 8).SingleLine().Tooltip(locales.CsiteSomeRaw()).FontSize(11).TextColor(theme.Warning)
	}
	page.table.list.Sort, page.table.list.KeepOffset = &page.table.sort, true
	page.table.list.Key = func(index int) any { return rows[index].key }
	page.table.list.Label = func(index int) string { return rows[index].name }
	page.table.list.Selection = &page.table.selected
	page.table.list.CellMenu = func(menu *ui.Menu, index, _ int) {
		if index >= 0 && index < len(rows) {
			caddySiteMenu(menu, a.caddySiteActions(s, page.find(rows[index].key)))
		}
	}
	table := ui.Table(c, &page.table.list, cols, len(rows), func(index, col int) {
		row := rows[index]
		value := resourceValue(row.values[col])
		if columns[col] == 4 {
			page.find(row.key).features(c, value)
			return
		}
		hint := value
		if narrow && columns[col] == 0 {
			hint = strings.Join(page.find(row.key).columns(), " · ")
		}
		ui.Text(c, value).SingleLine().MinWidth(0).Tooltip(hint)
	}).FillWidth().Grow(1).MinHeight(0).Label(locales.CaddySites())
	if table.Submitted() && selected != nil {
		if selected.raw {
			actions[4].run()
		} else if actions[0].reason == "" {
			actions[0].run()
		}
	}
	text := locales.CsiteCount(len(rows))
	if selected != nil && selected.raw {
		text = locales.CsiteRawSite()
	}
	ui.Text(c, text).Margin(3, 8).SingleLine().Tooltip(text).FontSize(11).TextColor(theme.TextMuted)
}

func caddySiteMenu(menu *ui.Menu, actions []caddySiteAction) {
	for _, action := range actions {
		if resourceMenuItem(menu, action.title, action.reason, action.danger) {
			action.run()
		}
	}
}

func (row caddySiteRow) columns() []string {
	spec := row.spec
	state := locales.SiteEnabled()
	if !row.active() {
		state = locales.SiteDisabled()
	}
	tls := map[string]string{"": locales.CsiteAuto(), "auto": locales.CsiteAuto(), "email": "ACME", "internal": locales.CsiteInternalTLS(), "custom": locales.CsiteCertTLS(), "dns": "DNS"}[spec.TLS.Mode]
	if spec.TLS.Mode == "" || spec.TLS.Mode == "auto" {
		if row.raw {
			tls = ""
		}
		if !slices.ContainsFunc(row.node.Args, func(address string) bool { return !strings.HasPrefix(address, "http://") }) {
			tls = "HTTP"
		}
	}
	var features []string
	for _, feature := range row.featureLabels() {
		features = append(features, feature.label)
	}
	return []string{row.name(), caddySiteTypeLabel(spec.Type), systemRedact(cmp.Or(strings.Join(spec.Upstreams, " "), spec.Root, spec.RedirectURL)), tls,
		strings.Join(features, " · "), state, fmt.Sprintf("%s:%d", row.file.Path, row.node.Line)}
}

type caddySiteFeature struct{ icon, label string }

func (row caddySiteRow) featureLabels() []caddySiteFeature {
	spec := row.spec
	var result []caddySiteFeature
	if len(spec.Auth) > 0 {
		result = append(result, caddySiteFeature{"lock", locales.CsiteHasAuth()})
	}
	if len(spec.IP.Allow)+len(spec.IP.Deny) > 0 {
		result = append(result, caddySiteFeature{"shield", locales.CsiteHasIP()})
	}
	if spec.SecurityHeaders || spec.RemoveServer || len(spec.CORS.Origins)+len(spec.Headers) > 0 || spec.HSTS != "" || spec.ReferrerPolicy != "" || spec.PermissionsPolicy != "" {
		result = append(result, caddySiteFeature{"list", locales.CsiteHasHeaders()})
	}
	if spec.Log.Enabled || spec.Log.Path != "" {
		result = append(result, caddySiteFeature{"file-text", locales.CsiteHasLogs()})
	}
	return result
}

func (row caddySiteRow) features(c *ui.Context, label string) {
	features := row.featureLabels()
	if len(features) == 0 {
		ui.Text(c, label)
		return
	}
	ui.Row(c).Gap(5).Tooltip(label).Children(func() {
		for _, feature := range features {
			ui.Icon(c.Key(feature.icon), icons.Get(feature.icon)).FontSize(13).TextColor(theme.TextMuted).Tooltip(feature.label).Label(feature.label)
		}
	})
}

func caddySiteTypeLabel(kind caddyfile.SiteType) string {
	switch kind {
	case caddyfile.Static:
		return locales.SiteStatic()
	case caddyfile.Proxy:
		return locales.SiteProxy()
	case caddyfile.PHP:
		return "PHP"
	case caddyfile.SPA:
		return "SPA"
	case caddyfile.Redirect:
		return locales.CsiteRedirect()
	case caddyfile.Respond:
		return locales.CsiteRespond()
	case caddyfile.Browser:
		return locales.CsiteBrowser()
	default:
		return locales.CsiteCustom()
	}
}
