package app

import (
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The Caddy tab of the bottom panel manages the Caddy web server of the
// connection's server: its install and updates, the service, the sites
// and global options, the config files in a code editor, certificates
// and logs. A row at the top switches between the sections, a file
// each. What changes the server goes through the escalation layer, asks
// first, backs up, and is checked by caddy before it takes effect.

// The sections of the Caddy tab, in the order of its row.
const (
	caddySecOverview = iota
	caddySecSites
	caddySecGlobal
	caddySecConfig
	caddySecCerts
	caddySecLogs
	caddySecInstall
)

// caddyPage is a session's Caddy tab: the section shown and the state of
// each, which their files keep.
type caddyPage struct {
	section  int
	overview caddyOverviewPage // caddypage_overview.go
	sites    caddySitesPage    // caddypage_sites.go
	global   caddyGlobalPage   // caddypage_global.go
	config   caddyConfigPage   // caddypage_config.go
	certs    caddyCertsPage    // caddypage_certs.go
	logs     caddyLogsPage     // caddypage_logs.go
	install  caddyInstallPage  // caddypage_install.go
}

// caddySectionNames are the labels of the sections' row, by section.
func caddySectionNames() []string {
	return []string{
		caddySecOverview: locales.CaddyOverview(),
		caddySecSites:    locales.CaddySites(),
		caddySecGlobal:   locales.CaddyGlobal(),
		caddySecConfig:   locales.CaddyConfig(),
		caddySecCerts:    locales.CaddyCerts(),
		caddySecLogs:     locales.CaddyLogs(),
		caddySecInstall:  locales.CaddyInstall(),
	}
}

// openCaddyPage shows the Caddy tab on section.
func (a *App) openCaddyPage(s *Session, section int) {
	s.caddy.section = section
	a.openBottomTab(tabCaddy)
}

// caddyPanel is the Caddy tab of the connection shown: why there is
// nothing to manage, or the sections.
func (a *App) caddyPanel(c *ui.Context) {
	s := a.connSession()
	ui.Column(c.Key("caddy-page")).Grow(1).MinHeight(0).FillWidth().Background(colors.overviewPage).Children(func() {
		switch {
		case s == nil || s.isNewTab():
			overviewMessage(c, "unplug", locales.NotConnected(), nil)
		case s.isLocal():
			overviewMessage(c, "laptop", locales.LocalNotAvailable(), nil)
		case s.status != statusConnected:
			overviewMessage(c, "unplug", s.statusText(), func() {
				if s.status != statusConnecting && ui.Button(c, locales.Reconnect()).Clicked() {
					a.reconnect(s)
				}
			})
		case !execAllowed(s.Host):
			overviewMessage(c, "globe", locales.SysInfoOff(), nil)
		default:
			a.caddyPageOf(c, s)
		}
	})
}

// caddyPageOf is the Caddy tab of s, a connected server: the row of
// sections over the one chosen.
func (a *App) caddyPageOf(c *ui.Context, s *Session) {
	page := &s.caddy
	ui.Row(c).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.Icon(c, icons.Get("globe")).FontSize(14).TextColor(theme.Accent).Shrink(0)
		ui.Text(c, "Caddy").Bold().SingleLine().Shrink(0)
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
			ui.Segmented(c, &page.section, caddySectionNames()...).Label(locales.TabCaddy())
		})
		if widgets.IconButton(c, "refresh-cw", locales.CaddyRefresh()).Shrink(0).Clicked() {
			a.refreshCaddySection(s)
		}
	})
	ui.Column(c.Key(page.section)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch page.section {
		case caddySecSites:
			a.caddySitesSection(c, s)
		case caddySecGlobal:
			a.caddyGlobalSection(c, s)
		case caddySecConfig:
			a.caddyConfigSection(c, s)
		case caddySecCerts:
			a.caddyCertsSection(c, s)
		case caddySecLogs:
			a.caddyLogsSection(c, s)
		case caddySecInstall:
			a.caddyInstallSection(c, s)
		default:
			a.caddyOverviewSection(c, s)
		}
	})
}

// refreshCaddySection reads again what the section shown reads.
func (a *App) refreshCaddySection(s *Session) {
	switch s.caddy.section {
	case caddySecSites:
		a.refreshCaddySites(s)
	case caddySecGlobal:
		a.refreshCaddyGlobal(s)
	case caddySecConfig:
		a.refreshCaddyConfig(s)
	case caddySecCerts:
		a.refreshCaddyCerts(s)
	case caddySecLogs:
		a.refreshCaddyLogs(s)
	case caddySecInstall:
		a.refreshCaddyInstall(s)
	default:
		a.refreshCaddyOverview(s)
	}
}

// caddySectionTodo is a section not built yet.
func caddySectionTodo(c *ui.Context) {
	overviewMessage(c, "globe", locales.DockerPageSoon(), nil)
}
