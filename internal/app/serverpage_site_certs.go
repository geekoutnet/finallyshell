package app

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type certificateRow struct {
	cert      monitor.Certificate
	name      string
	source    string
	status    string
	days      int
	hasExpiry bool
}

func (page *serverSitesPage) certificateRows(now time.Time) []certificateRow {
	var rows []certificateRow
	byFile := make(map[string]int, len(page.certificates))
	for _, cert := range page.certificates {
		name := cert.Subject
		if len(cert.SANs) > 0 {
			name = cert.SANs[0]
		}
		byFile[cert.File] = len(rows)
		rows = append(rows, certificateRow{cert, name, locales.CertPEM(), "", cert.DaysLeft(now), true})
	}
	for _, cert := range page.certbot {
		if index, found := byFile[cert.Path]; found {
			rows[index].name, rows[index].source, rows[index].status = cert.Name, "Certbot", cert.Status
			continue
		}
		data := monitor.Certificate{File: cert.Path, SANs: cert.Domains, KeyType: cert.KeyType, NotAfter: cert.Expiry}
		rows = append(rows, certificateRow{data, cert.Name, "Certbot", cert.Status, data.DaysLeft(now), !cert.Expiry.IsZero()})
		if cert.Path != "" {
			byFile[cert.Path] = len(rows) - 1
		}
	}
	for _, file := range page.certFailures {
		if index, found := byFile[file]; found {
			rows[index].status = strings.TrimSpace(rows[index].status + "\n" + locales.CertUnreadable())
			continue
		}
		rows = append(rows, certificateRow{cert: monitor.Certificate{File: file}, name: file, source: locales.CertPEM(), status: locales.CertUnreadable()})
	}
	return rows
}

func (a *App) certList(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.sites
	cols := []ui.TableColumn{
		{ID: "name", Title: locales.CertSubject(), MinWidth: 125, Sortable: true},
		resourceColumn("expiry", locales.CertExpiry(), 140),
		resourceColumn("left", locales.CertRemaining(), 105),
		resourceColumn("issuer", locales.CertIssuer(), 180),
		resourceColumn("source", locales.CertSource(), 86),
		resourceColumn("file", locales.SiteFile(), 270),
	}
	if narrow {
		cols = cols[:3]
		cols[1].Width, cols[2].Width = 120, 100
	}
	var tableRows []dockerResourceRow
	rows := map[string]certificateRow{}
	for _, row := range page.certificateRows(time.Now()) {
		key := row.source + "\x00" + row.cert.File
		if row.source == "Certbot" {
			key += "\x00" + row.name
		}
		remaining := row.status
		if row.hasExpiry {
			remaining = certificateRemaining(row.days)
		}
		name := siteClean(row.name)
		tableRows = append(tableRows, dockerResourceRow{key: key, name: name, values: []string{name, certificateDate(row.cert.NotAfter, false), siteClean(remaining), siteClean(row.cert.Issuer), row.source, row.cert.File, siteClean(strings.Join(row.cert.SANs, " "))},
			order: map[string]int64{"left": siteExpiryOrder(row.hasExpiry, row.days)}})
		rows[key] = row
	}
	shown := page.certs.shown(tableRows, cols)
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
		ui.SearchField(c, &page.certs.query).Grow(1).MinWidth(82).Placeholder(locales.CertFilter()).Label(locales.CertFilter())
		if ui.Button(c, locales.CertCheck()).Shrink(0).Clicked() {
			a.siteCheckDomain(s, "")
		}
		ui.MenuButton(c, locales.CertTools(), func(menu *ui.Menu) { a.certToolsMenu(menu, s) }).Shrink(0)
	})
	if page.loaded {
		siteBanner(c, page.certError, true)
	}
	siteTable(c, &page.certs, locales.CertTitle(), cols, shown, func(menu *ui.Menu, key string) {
		row := rows[key]
		if resourceMenuItem(menu, locales.SiteDetails(), "", false) {
			a.certDetails(s, row)
		}
		host := ""
		for _, name := range append([]string{row.name}, row.cert.SANs...) {
			if monitor.ValidSiteHost(name) {
				host = name
				break
			}
		}
		if resourceMenuItem(menu, locales.CertCheck(), "", false) {
			a.siteCheckDomain(s, host)
		}
	}, func(key string) { a.certDetails(s, rows[key]) }, func(key string, col int) int {
		if row := rows[key]; col == 2 && row.hasExpiry {
			return siteExpiryLevel(row.days)
		}
		return 0
	})
}

func (a *App) certToolsMenu(menu *ui.Menu, s *Session) {
	page := &s.server.sites
	busy := ""
	if page.reading || page.dialog != nil && page.dialog.busy {
		busy = locales.SiteBusy()
	}
	reason := busy
	if !page.status.Certbot {
		reason = cmp.Or(reason, locales.CertNoCertbot())
	}
	if page.status.Installed {
		reason = cmp.Or(reason, siteReadyReason(page))
		if !page.inventory.CertbotBackupAvailable(time.Now()) {
			reason = cmp.Or(reason, locales.CertUnsafeBackup())
		}
	}
	if resourceMenuItem(menu, locales.CertDryRun(), reason, false) {
		a.certRenew(s, true)
	}
	if resourceMenuItem(menu, locales.CertRenew(), reason, true) {
		a.certRenew(s, false)
	}
	if !page.certbotNginx {
		reason = cmp.Or(reason, locales.CertNoPlugin())
	}
	if resourceMenuItem(menu, locales.CertIssue(), cmp.Or(reason, siteReadyReason(page)), false) {
		a.certIssue(s)
	}
	if resourceMenuItem(menu, locales.CertSchedule(), busy, false) {
		status := []string{
			"Certbot: " + siteInstalled(page.status.Certbot),
			"acme.sh: " + siteInstalled(page.status.ACME || strings.Contains(page.schedule, "acme.sh")),
			"OpenSSL: " + siteInstalled(page.status.OpenSSL),
			"Certbot nginx: " + siteInstalled(page.certbotNginx),
			"",
			cmp.Or(page.schedule, locales.CertNoSchedule()),
		}
		page.dialog = &systemDialog{client: s.client, title: locales.CertSchedule(), output: strings.Join(status, "\n")}
	}
}

func siteInstalled(found bool) string {
	if found {
		return locales.CertInstalled()
	}
	return locales.CertNotDetected()
}

func (a *App) certRenew(s *Session, dry bool) {
	page := &s.server.sites
	command := monitor.CertbotRenewCommand(dry)
	target, title, warning := "Certbot", locales.CertRenew(), locales.CertRenewWarning()
	if dry {
		title, warning = locales.CertDryRun(), locales.CertDryWarning()
	}
	if page.status.Installed {
		var backups []string
		var err error
		command, backups, err = page.inventory.CertbotRenewWithBackups(dry, time.Now())
		if err != nil {
			return
		}
		target += "\n\n" + locales.CertBackups() + "\n" + strings.Join(backups, "\n")
	}
	a.siteCommand(s, title, target, warning, command+" 2>&1", nil, true)
}

func (a *App) certIssue(s *Session) {
	page := &s.server.sites
	dialog := &systemDialog{client: s.client, title: locales.CertIssue()}
	page.dialog = dialog
	var domains, email string
	var form func(*ui.Context, float32)
	form = func(c *ui.Context, _ float32) {
		siteField(c, locales.SiteDomains(), &domains)
		siteField(c, locales.CertEmail(), &email)
		ui.Text(c, locales.CertIssueHint()).FontSize(12).TextColor(theme.TextMuted)
		names := strings.Fields(domains)
		command, backups, err := page.inventory.CertbotIssueWithBackups(names, strings.TrimSpace(email), time.Now())
		if ui.PrimaryButton(c, locales.CertIssueNext()).Disabled(err != nil).AlignSelf(ui.End).Clicked() {
			dialog.target = strings.Join(names, ", ") + "\n\n" + locales.CertBackups() + "\n" + strings.Join(backups, "\n")
			dialog.body, dialog.consequence = nil, locales.CertIssueWarning()
			dialog.back = func() { dialog.body, dialog.target, dialog.consequence, dialog.confirm = form, "", "", nil }
			dialog.confirm = func() { a.siteWork(s, dialog, command+" 2>&1", nil, true) }
		}
	}
	dialog.body = form
}

func (a *App) siteCheckDomain(s *Session, domain string) {
	page := &s.server.sites
	dialog := &systemDialog{client: s.client, title: locales.CertCheck()}
	page.dialog = dialog
	dialog.body = func(c *ui.Context, _ float32) {
		siteField(c, locales.CertDomain(), &domain)
		ui.Text(c, locales.CertCheckHint()).FontSize(12).TextColor(theme.TextMuted)
		host := strings.TrimSpace(domain)
		if ui.PrimaryButton(c, locales.CertCheckRun()).Disabled(!monitor.ValidSiteHost(host)).AlignSelf(ui.End).Clicked() {
			a.siteTLSProbe(s, dialog, host)
		}
	}
}

func (a *App) siteTLSProbe(s *Session, dialog *systemDialog, host string) {
	page := &s.server.sites
	script := monitor.CertificateRemoteCommand(host)
	read, local := sitesReader(s), page.localTLS
	if local == nil {
		local = monitor.CertificateLocal
	}
	ctx, cancel := context.WithTimeout(systemContext(s), 12*time.Second)
	dialog.body, dialog.target, dialog.busy, dialog.cancel = nil, host+":443", true, cancel
	go func() {
		defer cancel()
		localResult := make(chan string, 1)
		go func() {
			cert, err := local(ctx, host)
			localResult <- certificateProbeResult(cert, err, true)
		}()
		out, stderr, err := read(ctx, script, nil, nil)
		var cert monitor.Certificate
		if err == nil {
			cert, err = monitor.ParseRemoteCertificate(out)
		} else if stderr != "" {
			err = fmt.Errorf("%w: %s", err, stderr)
		}
		remote := certificateProbeResult(cert, err, false)
		output := locales.CertFromServer() + "\n" + remote + "\n\n" + locales.CertFromComputer() + "\n" + <-localResult
		a.post(func() {
			if page.dialog == dialog && systemReady(s, dialog.client) {
				dialog.output, dialog.busy, dialog.done = siteClean(output), false, true
			}
		})
	}()
}

func certificateProbeResult(cert monitor.Certificate, err error, local bool) string {
	if cert.NotAfter.IsZero() {
		return locales.CertCheckFailed() + ": " + siteError("", err)
	}
	text := strings.Join(certDetailsLines(cert), "\n")
	if err != nil {
		return text + "\n" + locales.CertVerifyFailed() + ": " + siteError("", err)
	}
	if local {
		return text + "\n" + locales.CertVerified()
	}
	return text + "\n" + locales.CertNotVerified()
}

func (a *App) certDetails(s *Session, row certificateRow) {
	details := certDetailsLines(row.cert)
	if row.status != "" {
		details = append(details, row.status)
	}
	if row.cert.File != "" {
		details = append(details, locales.SiteFile()+": "+row.cert.File)
	}
	s.server.sites.dialog = &systemDialog{client: s.client, title: locales.CertTitle(), target: siteClean(row.name), output: siteClean(strings.Join(details, "\n"))}
}

func certDetailsLines(cert monitor.Certificate) []string {
	var details []string
	for _, field := range []struct{ label, value string }{
		{locales.CertSubject(), cert.Subject}, {"SAN", strings.Join(cert.SANs, ", ")}, {locales.CertIssuer(), cert.Issuer},
		{locales.CertNotBefore(), certificateDate(cert.NotBefore, true)}, {locales.CertExpiry(), certificateDate(cert.NotAfter, true)}, {locales.CertKeyType(), cert.KeyType},
	} {
		if field.value != "" {
			details = append(details, field.label+": "+field.value)
		}
	}
	return details
}

func certificateDate(date time.Time, withTime bool) string {
	if date.IsZero() {
		return ""
	}
	if withTime {
		return date.UTC().Format("2006-01-02 15:04 UTC")
	}
	return date.UTC().Format("2006-01-02")
}
