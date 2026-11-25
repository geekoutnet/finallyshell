package app

import (
	"math"
	"path"
	"strings"
	"time"

	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type caddyCertsPage struct {
	requested, loading bool
	failure            string
	data               caddyCertificateInfo
	list               ui.ListState
	selected           ui.Selection[int]
}

func (a *App) refreshCaddyCerts(s *Session) { a.caddyRefresh(s) }

func caddyDaysLeft(expiry, now time.Time) int {
	return int(math.Floor(expiry.Sub(now).Hours() / 24))
}

func caddyExpiryColor(days int) ui.Color {
	switch {
	case days < 7:
		return theme.Danger
	case days < 30:
		return theme.Warning
	default:
		return theme.Text
	}
}

func (a *App) caddyCertsSection(c *ui.Context, s *Session) {
	if !a.caddyDetected(c, s, false) {
		return
	}
	page := &s.caddy.certs
	if !page.requested {
		page.requested, page.loading = true, true
		caddyRead(a, s, caddyBackend.certs, func(result caddyCertificateInfo, err error) {
			page.data, page.failure, page.loading = result, caddyError(err), false
		})
	}
	root := page.data.Root
	rootReason, downloadReason := "", ""
	if root.PEM == "" {
		rootReason = locales.CpanelNoRootCA()
	}
	if s.sftp == nil || !caddyValidPath(root.Path) || !strings.HasSuffix(root.Path, "/root.crt") {
		downloadReason = locales.CpanelNoRootDownload()
	}
	rows := page.data.Rows
	certActions := func(index int) caddyAction {
		reason := ""
		if index < 0 || rows[index].PEM == "" {
			reason = locales.CpanelSelectCert()
		}
		return caddyAction{locales.CpanelOpenCert(), reason, func() {
			cert := rows[index]
			s.caddy.state.dialog = &systemDialog{client: s.client, title: locales.CpanelOpenCert(), target: cert.Path, body: caddyReadOnlyBody(cert.Path, cert.PEM)}
		}}
	}
	selected := -1
	for index := range rows {
		if page.selected.Has(index) {
			selected = index
			break
		}
	}
	actions := []caddyAction{
		certActions(selected),
		{locales.CpanelCopyRoot(), rootReason, func() { c.WriteClipboard(root.PEM) }},
		{locales.CpanelDownloadRoot(), downloadReason, func() { a.downloadFrom(s, path.Dir(root.Path), []string{path.Base(root.Path)}, "", false) }},
	}
	caddyActions(c, actions...)
	if page.loading {
		ui.Spinner(c).Label(locales.CpanelReading()).Padding(6)
	}
	caddyNotice(c, page.failure)
	if root.Subject != "" {
		ui.Text(c, locales.CpanelRootSummary(root.Subject, root.Expires.Format("2006-01-02"))).Padding(3, 8).FillWidth().TextColor(theme.TextMuted)
	}
	if page.data.OnDemand {
		caddyNotice(c, locales.CpanelOnDemand())
	}
	ui.Text(c, locales.CpanelAutoRenew()).Padding(2, 8).FillWidth().TextColor(theme.TextMuted)
	if len(rows) == 0 && !page.loading && page.failure == "" {
		caddyNotice(c, locales.CpanelNoCerts())
	}
	columns := []ui.TableColumn{
		{ID: "domain", Title: locales.CpanelDomains(), Width: 220, MinWidth: 120},
		{ID: "issuer", Title: locales.CpanelIssuer(), Width: 130, MinWidth: 90},
		{ID: "expiry", Title: locales.CpanelExpiry(), Width: 225},
		{ID: "key", Title: locales.CpanelKeyType(), Width: 100},
		{ID: "path", Title: locales.CpanelCertPath(), Width: 360},
	}
	if a.opsNarrow() {
		columns[0].Width, columns[1].Width, columns[2].Width = 155, 105, 160
	}
	page.list.Selection = &page.selected
	page.list.Key = func(row int) any { return row }
	page.list.Label = func(row int) string { return rows[row].Domains }
	page.list.CellMenu = func(menu *ui.Menu, row, _ int) { caddyMenu(menu, certActions(row), actions[1], actions[2]) }
	now := time.Now()
	table := ui.Table(c, &page.list, columns, len(rows), func(row, column int) {
		cert := rows[row]
		days := caddyDaysLeft(cert.Expires, now)
		expiry := locales.CpanelUnknown()
		if !cert.Expires.IsZero() {
			expiry = cert.Expires.Format("2006-01-02") + " · " + locales.CpanelDaysLeft(max(0, days))
			if days < 0 {
				expiry = cert.Expires.Format("2006-01-02") + " · " + locales.CpanelExpired()
			}
		}
		value := [...]string{cert.Domains, cert.Issuer, expiry, cert.Key, cert.Path}[column]
		cell := ui.Text(c, value).SingleLine().Tooltip(value).MinWidth(0)
		if column == 2 && !cert.Expires.IsZero() {
			cell.TextColor(caddyExpiryColor(days))
		}
	}).Grow(1).MinHeight(0).FillWidth().Label(locales.CaddyCerts())
	if table.Submitted() && selected >= 0 && actions[0].reason == "" {
		actions[0].invoke()
	}
}
