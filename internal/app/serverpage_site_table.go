package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type siteRow struct {
	monitor.NginxSite
	file, link, state string
	tls, hasExpiry    bool
	expiry            int
}

func (page *serverSitesPage) siteRows(now time.Time) []siteRow {
	files := map[string]monitor.Certificate{}
	for _, cert := range page.certificates {
		files[cert.File] = cert
	}
	var rows []siteRow
	for _, site := range append(slices.Clone(page.inventory.Sites), page.inventory.Inactive...) {
		file, link, state := page.inventory.Target(site)
		row := siteRow{NginxSite: site, file: file, link: link, state: state}
		for _, listener := range site.Listen {
			row.tls = row.tls || listener.TLS
		}
		row.tls = row.tls || len(site.Certificates) != 0
		for _, file := range site.Certificates {
			if cert, ok := files[page.inventory.CertificatePath(file)]; ok {
				days := cert.DaysLeft(now)
				if !row.hasExpiry || days < row.expiry {
					row.hasExpiry, row.expiry = true, days
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func (row siteRow) displayName() string {
	return cmp.Or(strings.Join(row.Names, ", "), row.link, locales.SiteUnnamed())
}

func (row siteRow) listeners() string {
	var ports []string
	for _, listener := range row.Listen {
		port := strconv.Itoa(listener.Port)
		if listener.Port == 0 {
			port = "UNIX"
		}
		if listener.TLS {
			port += " TLS"
		}
		if listener.HTTP2 {
			port += " h2"
		}
		if listener.Default {
			port += " ★"
		}
		if !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
	}
	return strings.Join(ports, ", ")
}

func (row siteRow) destination() string {
	values := slices.DeleteFunc(append(slices.Clone(row.Proxy), row.Root), func(value string) bool { return value == "" })
	return strings.Join(slices.Compact(values), " ")
}

func (row siteRow) location() string { return row.NginxSite.File + ":" + strconv.Itoa(row.Line) }

func (row siteRow) stateText() string {
	switch row.state {
	case "enabled":
		return locales.SiteEnabled()
	case "disabled":
		return locales.SiteDisabled()
	default:
		return locales.SiteIncluded()
	}
}

func (a *App) sitesHeader(c *ui.Context, s *Session) {
	page := &s.server.sites
	ui.Row(c).FillWidth().Padding(4, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Segmented(c, &page.tabs, locales.ServerSites(), locales.CertTitle()).Shrink(0).Label(locales.ServerSites())
		if page.reading {
			ui.Spinner(c).Label(locales.SiteReading())
		}
		status := page.status
		if status.Installed && page.tabs == 0 {
			state := locales.SiteStateUnknown()
			switch status.State {
			case "active":
				state = locales.SiteRunning()
			case "inactive":
				state = locales.SiteStopped()
			case "failed":
				state = locales.SiteFailed()
			case "activating", "deactivating", "reloading":
				state = locales.SiteChanging()
			}
			text := cmp.Or(siteClean(status.Version), "Nginx") + " · " + state
			if status.PID > 0 {
				text += " · PID " + strconv.Itoa(status.PID)
			}
			if status.Custom {
				text += " · " + locales.SiteCustom()
			}
			widgets.Muted(c, text).FontSize(12).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		}
	})
}

func siteBanner(c *ui.Context, text string, warning bool) {
	if text == "" {
		return
	}
	item := widgets.Muted(c, text).SingleLine().Tooltip(text).FontSize(12).FillWidth().Padding(2, 8)
	if warning {
		item.TextColor(theme.Warning)
	}
}

func (a *App) siteList(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.sites
	if file := page.status.Apache; file != "" {
		siteBanner(c.Key("Apache"), locales.SiteOtherServer("Apache", siteClean(file)), false)
	}
	if file := page.status.Caddy; file != "" {
		ui.Row(c.Key("Caddy")).FillWidth().Padding(2, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			text := locales.SiteCaddyDetected(siteClean(file))
			widgets.Muted(c, text).SingleLine().Tooltip(text).FontSize(12).MinWidth(0).Grow(1)
			if ui.Button(c, locales.TabCaddy()).Shrink(0).Clicked() {
				a.openCaddyPage(s, caddySecSites)
			}
		})
	}
	if page.loaded && !page.status.Installed {
		siteBanner(c, locales.SiteNotInstalled(), false)
	}
	siteBanner(c, page.readErr, true)
	if !page.status.Installed {
		return
	}
	cols := []ui.TableColumn{
		{ID: "name", Title: locales.SiteDomains(), MinWidth: 112, Sortable: true},
		resourceColumn("port", locales.SitePorts(), 140),
		resourceColumn("tls", "TLS", 125),
		resourceColumn("state", locales.SiteState(), 105),
		resourceColumn("upstream", locales.SiteTarget(), 150),
		resourceColumn("file", locales.SiteFile(), 300),
	}
	if narrow {
		cols = cols[:4]
		cols[1].Width, cols[2].Width, cols[3].Width = 98, 94, 80
	}
	var tableRows []dockerResourceRow
	rows := map[string]siteRow{}
	now := time.Now()
	for _, row := range page.siteRows(now) {
		key := row.location()
		name := siteClean(row.displayName())
		days := "—"
		if row.tls {
			days = locales.SiteYes()
			if row.hasExpiry {
				days = certificateRemaining(row.expiry)
			}
		}
		var port int
		if len(row.Listen) > 0 {
			port = row.Listen[0].Port
		}
		tableRows = append(tableRows, dockerResourceRow{key: key, name: name, values: []string{name, row.listeners(), days, row.stateText(), siteClean(row.destination()), siteClean(key)},
			order: map[string]int64{"port": int64(port), "tls": siteExpiryOrder(row.hasExpiry, row.expiry)}})
		rows[key] = row
	}
	shown := page.sites.shown(tableRows, cols)
	picked := func() []siteRow {
		var selection []siteRow
		for _, row := range shown {
			if page.sites.selected.Has(row.key) {
				selection = append(selection, rows[row.key])
			}
		}
		return selection
	}
	a.siteToolbar(c, s, narrow, picked)
	siteTable(c, &page.sites, locales.ServerSites(), cols, shown, func(menu *ui.Menu, key string) {
		if !page.sites.selected.Has(key) {
			page.sites.selected.Clear()
			page.sites.selected.Add(key)
		}
		a.siteRowMenu(menu, s, picked())
	}, func(row string) { a.siteDetails(s, rows[row]) }, func(key string, column int) int {
		if row := rows[key]; column == 2 && row.hasExpiry {
			return siteExpiryLevel(row.expiry)
		}
		return 0
	})
}

func (a *App) siteToolbar(c *ui.Context, s *Session, narrow bool, picked func() []siteRow) {
	page := &s.server.sites
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &page.sites.query).Grow(1).MinWidth(82).Placeholder(locales.SiteFilter()).Label(locales.SiteFilter())
		ui.MenuButton(c, "Nginx", func(menu *ui.Menu) { a.siteServiceMenu(menu, s, narrow) }).Shrink(0)
		if !narrow {
			if ui.Button(c, locales.SiteNew()).Disabled(siteNewReason(page) != "").Tooltip(siteNewReason(page)).Shrink(0).Clicked() {
				a.siteNew(s)
			}
		}
		ui.MenuButton(c, locales.SiteActions(), func(menu *ui.Menu) { a.siteRowMenu(menu, s, picked()) }).Shrink(0)
	})
}

func siteTable(c *ui.Context, table *dockerResourceTable, label string, cols []ui.TableColumn, rows []dockerResourceRow,
	menu func(*ui.Menu, string), details func(string), level func(string, int) int) {
	table.list.Sort, table.list.Selection, table.list.KeepOffset = &table.sort, &table.selected, true
	table.list.Key = func(index int) any { return rows[index].key }
	table.list.Label = func(index int) string { return rows[index].name }
	table.list.CellMenu = func(item *ui.Menu, index, _ int) { menu(item, rows[index].key) }
	element := ui.Table(c, &table.list, cols, len(rows), func(index, col int) {
		row := rows[index]
		value := orDash(row.values[col])
		cell := ui.Text(c, value).SingleLine().MinWidth(0).Tooltip(value)
		switch level(row.key, col) {
		case 1:
			cell.TextColor(theme.Warning)
		case 2:
			cell.TextColor(theme.Danger)
		}
	}).Grow(1).MinHeight(0).FillWidth().Label(label)
	if element.Submitted() {
		for _, row := range rows {
			if table.selected.Has(row.key) {
				details(row.key)
				break
			}
		}
	}
	if len(rows) == 0 {
		siteBanner(c, locales.SiteNoResults(), false)
	}
}

func siteExpiryLevel(days int) int {
	if days < 7 {
		return 2
	}
	if days < 30 {
		return 1
	}
	return 0
}

func siteExpiryOrder(valid bool, days int) int64 {
	if !valid {
		return 1 << 30
	}
	return int64(days)
}

func certificateRemaining(days int) string {
	if days < 0 {
		return locales.CertExpired()
	}
	return locales.CertDays(days)
}
