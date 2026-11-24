package app

import (
	"cmp"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func siteClean(text string) string {
	text = systemRedact(strings.ToValidUTF8(text, "�"))
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if !monitor.NginxOutputSafe(line) {
			lines[index] = "[redacted]"
		}
	}
	return strings.Join(lines, "\n")
}

func siteError(stderr string, err error) string { return siteClean(systemError(stderr, err)) }

func siteRootDenied(err error) bool {
	return errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword)
}

func siteGeneralReason(page *serverSitesPage) string {
	if page.reading || page.dialog != nil && page.dialog.busy {
		return locales.SiteBusy()
	}
	if !page.loaded || !page.status.Installed {
		return locales.SiteNotInstalled()
	}
	if page.status.Custom || page.inventory.Main != "" && page.inventory.Main != monitor.NginxMain {
		return locales.SiteCustom()
	}
	return ""
}

func siteReadyReason(page *serverSitesPage) string {
	if reason := siteMutationReason(page); reason != "" {
		return reason
	}
	if page.readErr != "" || page.inventory.Main == "" {
		return locales.SiteNeedConfig()
	}
	if page.recovery != nil {
		return locales.SiteRestoreFirst()
	}
	return ""
}

func siteMutationReason(page *serverSitesPage) string {
	if reason := siteGeneralReason(page); reason != "" {
		return reason
	}
	if (page.status.State == "active" || page.status.PID > 0) && !page.status.MasterVerified {
		return locales.SiteMasterUnknown()
	}
	return ""
}

func siteNewReason(page *serverSitesPage) string {
	if reason := siteReadyReason(page); reason != "" {
		return reason
	}
	if !page.inventory.Debian && !page.inventory.ConfD {
		return locales.SiteLayout()
	}
	return ""
}

func (a *App) siteServiceMenu(menu *ui.Menu, s *Session, narrow bool) {
	page := &s.server.sites
	reason := siteGeneralReason(page)
	if resourceMenuItem(menu, locales.SiteTest(), reason, false) {
		a.siteServiceAction(s, "test", locales.SiteTest(), "")
	}
	reload, restart := siteMutationReason(page), siteMutationReason(page)
	if page.status.State != "active" {
		reload = cmp.Or(reload, locales.SiteNotRunning())
	}
	if !page.status.Unit {
		restart = cmp.Or(restart, locales.SiteNeedSystemd())
	}
	if resourceMenuItem(menu, locales.SiteReload(), reload, false) {
		a.siteServiceAction(s, "reload", locales.SiteReload(), locales.SiteReloadWarning())
	}
	if resourceMenuItem(menu, locales.SiteRestart(), restart, true) {
		a.siteServiceAction(s, "restart", locales.SiteRestart(), locales.SiteRestartWarning())
	}
	if narrow && resourceMenuItem(menu, locales.SiteNew(), siteNewReason(page), false) {
		a.siteNew(s)
	}
	if recovery := page.recovery; recovery != nil && resourceMenuItem(menu, locales.SiteRestore(), reason, false) {
		dialog := &systemDialog{client: s.client, target: recovery.File, output: locales.SiteBackup(recovery.Backup)}
		page.dialog = dialog
		a.siteOfferRestore(s, dialog, *recovery)
	}
}

func (a *App) siteServiceAction(s *Session, action, title, consequence string) {
	page := &s.server.sites
	script, err := monitor.NginxActionCommand(action, page.status.Unit, cmp.Or(page.inventory.Main, monitor.NginxMain))
	if err == nil {
		a.siteCommand(s, title, "nginx", consequence, script, nil, action != "test")
	}
}

func siteToggleReason(page *serverSitesPage, rows []siteRow, before string) ([]string, string) {
	if reason := siteReadyReason(page); reason != "" {
		return nil, reason
	}
	if len(rows) == 0 {
		return nil, locales.SiteSelect()
	}
	var links []string
	for _, row := range rows {
		if row.link == "" {
			return nil, locales.SiteLayout()
		}
		if row.state != before {
			return nil, locales.SiteSelectionState()
		}
		if !slices.Contains(links, row.link) {
			links = append(links, row.link)
		}
	}
	if _, err := page.inventory.ToggleCommand(links, before == "disabled", false, page.status.Unit); err != nil {
		return nil, locales.SiteLayout()
	}
	return links, ""
}

func (a *App) siteRowMenu(menu *ui.Menu, s *Session, rows []siteRow) {
	page := &s.server.sites
	var row siteRow
	single := ""
	if len(rows) == 1 {
		row = rows[0]
	} else {
		single = locales.SiteSelectOne()
	}
	if resourceMenuItem(menu, locales.SiteDetails(), single, false) {
		a.siteDetails(s, row)
	}
	edit := cmp.Or(single, siteReadyReason(page))
	if !monitor.NginxEditable(row.file) {
		edit = cmp.Or(edit, locales.SiteUnsupportedFile())
	} else if !page.inventory.AllowsBackup(row.file + ".bak-" + time.Now().Format("20060102150405")) {
		edit = cmp.Or(edit, locales.SiteBackupIncluded())
	}
	if resourceMenuItem(menu, locales.SiteEdit(), edit, false) {
		a.siteEditor(s, row.file, row.state != "disabled")
	}
	for _, toggle := range []struct{ state, label string }{{"disabled", locales.SiteEnable()}, {"enabled", locales.SiteDisable()}} {
		names, reason := siteToggleReason(page, rows, toggle.state)
		if resourceMenuItem(menu, toggle.label, reason, toggle.state == "enabled") {
			a.siteToggle(s, names, toggle.state == "disabled")
		}
	}
	for _, log := range []struct{ label, file string }{{locales.SiteAccessLog(), row.AccessLog}, {locales.SiteErrorLog(), row.ErrorLog}} {
		reason := single
		if monitor.NginxLogsCommand(log.file) == "" {
			reason = cmp.Or(reason, locales.SiteNoLogFile())
		}
		if resourceMenuItem(menu, log.label, reason, false) {
			if monitor.ValidLogPath(log.file) {
				logs := &s.server.logs
				logs.path, logs.sourceMode, logs.priority, logs.since = log.file, 2, 0, 0
				a.setSystemLog(s, monitor.JournalSource{File: log.file, Lines: 300})
				a.openServerPage(s, serverSecLogs)
			} else {
				a.siteCommand(s, log.label, log.file, "", monitor.NginxLogsCommand(log.file), nil, false)
			}
		}
	}
}

func (a *App) siteToggle(s *Session, names []string, enable bool) {
	page := &s.server.sites
	reload := page.status.State == "active"
	script, err := page.inventory.ToggleCommand(names, enable, reload, page.status.Unit)
	if err != nil {
		return
	}
	title, consequence := locales.SiteDisable(), locales.SiteDisableWarning()
	if enable {
		title, consequence = locales.SiteEnable(), locales.SiteEnableWarning()
	}
	var links []string
	for _, name := range names {
		links = append(links, monitor.NginxEnabled+name+" → "+monitor.NginxAvailable+name)
	}
	a.siteCommand(s, title, strings.Join(links, "\n"), consequence+" "+siteApplyHint(reload), script, nil, true)
}

func siteApplyHint(reload bool) string {
	if reload {
		return locales.SiteValidateReload()
	}
	return locales.SiteValidateOnly()
}

func (a *App) siteDetails(s *Session, row siteRow) {
	page := &s.server.sites
	details := []string{
		locales.SitePorts() + ": " + orDash(row.listeners()),
		locales.SiteState() + ": " + row.stateText(),
		locales.SiteTarget() + ": " + orDash(row.destination()),
		locales.SiteFile() + ": " + row.location(),
		locales.SiteAccessLog() + ": " + orDash(row.AccessLog),
		locales.SiteErrorLog() + ": " + orDash(row.ErrorLog),
		locales.CertTitle() + ": " + orDash(strings.Join(row.Certificates, ", ")),
	}
	page.dialog = &systemDialog{client: s.client, title: locales.SiteDetails(), target: siteClean(row.displayName()), output: siteClean(strings.Join(details, "\n"))}
}

func (a *App) siteCommand(s *Session, title, target, warning, script string, input io.Reader, refresh bool) {
	page := &s.server.sites
	dialog := &systemDialog{client: s.client, title: title, target: siteClean(target), consequence: warning}
	page.dialog = dialog
	run := func() { a.siteWork(s, dialog, script, input, refresh) }
	if warning != "" {
		dialog.confirm = run
	} else {
		run()
	}
}

func (a *App) siteWork(s *Session, dialog *systemDialog, script string, input io.Reader, refresh bool) {
	root := a.sitesRoot(s)
	run := func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		pending := newSystemChunks(256)
		forward := func(chunk string) {
			pending.add(chunk)
			if lines := pending.take(false); len(lines) > 0 {
				progress(siteClean(strings.Join(lines, "\n")) + "\n")
			}
		}
		out, stderr, err := root(ctx, script, input, forward)
		if last := pending.take(true); len(last) > 0 {
			progress(siteClean(strings.Join(last, "\n")) + "\n")
		}
		return siteClean(out), siteClean(stderr), err
	}
	var reread func(*Session)
	if refresh {
		reread = a.refreshServerSites
	}
	a.systemWork(s, dialog, run, script, input, reread)
}
