package app

import (
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The System tab of the bottom panel manages the connection's server
// itself, as a server panel does: its services, logs, scheduled jobs,
// disk, updates, security, firewall and websites, a section each,
// chosen in a row at the top. Each section reads what it lists when it
// shows, over exec channels; what needs root goes through the escalation
// layer (privilege*.go).

// The sections of the System tab, in the order of its row.
const (
	serverSecServices = iota
	serverSecLogs
	serverSecCron
	serverSecDisk
	serverSecUpdates
	serverSecSecurity
	serverSecFirewall
	serverSecSites
)

// serverPage is a session's System tab: the section shown and the state
// of each, which their files keep.
type serverPage struct {
	section  int
	services serverServicesPage // serverpage_services.go
	logs     serverLogsPage     // serverpage_logs.go
	cron     serverCronPage     // serverpage_cron.go
	disk     serverDiskPage     // serverpage_disk.go
	updates  serverUpdatesPage  // serverpage_updates.go
	security serverSecurityPage // serverpage_security.go
	firewall serverFirewallPage // serverpage_firewall.go
	sites    serverSitesPage    // serverpage_sites.go
	openwrt  openWrtPage        // openwrt.go: the sections on OpenWrt
}

// serverSectionNames are the labels of the sections' row, by section.
func serverSectionNames() []string {
	return []string{
		serverSecServices: locales.ServerServices(),
		serverSecLogs:     locales.ServerLogs(),
		serverSecCron:     locales.ServerCron(),
		serverSecDisk:     locales.ServerDisk(),
		serverSecUpdates:  locales.ServerUpdates(),
		serverSecSecurity: locales.ServerSecurity(),
		serverSecFirewall: locales.ServerFirewall(),
		serverSecSites:    locales.ServerSites(),
	}
}

// openServerPage shows the System tab on section.
func (a *App) openServerPage(s *Session, section int) {
	s.server.section = section
	a.openBottomTab(tabSystem)
}

// serverPanel is the System tab of the connection shown: why there is
// nothing to manage, or the sections.
func (a *App) serverPanel(c *ui.Context) {
	s := a.connSession()
	ui.Column(c.Key("server-page")).Grow(1).MinHeight(0).FillWidth().Background(colors.overviewPage).Children(func() {
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
			overviewMessage(c, "server", locales.SysInfoOff(), nil)
		default:
			a.serverPageOf(c, s)
		}
	})
}

// serverPageOf is the System tab of s, a connected server: the row of
// sections over the one chosen, OpenWrt's on OpenWrt.
func (a *App) serverPageOf(c *ui.Context, s *Session) {
	a.openWrtDetect(s)
	switch ow := &s.server.openwrt; {
	case ow.probing && !ow.checked:
		ui.Spinner(c).Label(locales.SvcReading()).Padding(12)
		return
	case s.isOpenWrt():
		a.openWrtPageOf(c, s)
		return
	}
	page := &s.server
	ui.Row(c).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.Icon(c, icons.Get("server")).FontSize(14).TextColor(theme.Accent).Shrink(0)
		ui.Text(c, s.label()).Bold().SingleLine().Shrink(1).MinWidth(40).MaxWidth(200)
		if a.opsNarrow() {
			serverCompactSections(c, &page.section)
		} else {
			ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
				ui.Segmented(c, &page.section, serverSectionNames()...).Label(locales.TabSystem())
			})
		}
		if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Shrink(0).Clicked() {
			a.refreshServerSection(s)
		}
	})
	ui.Column(c.Key(page.section)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch page.section {
		case serverSecLogs:
			a.serverLogsSection(c, s)
		case serverSecCron:
			a.serverCronSection(c, s)
		case serverSecDisk:
			a.serverDiskSection(c, s)
		case serverSecUpdates:
			a.serverUpdatesSection(c, s)
		case serverSecSecurity:
			a.serverSecuritySection(c, s)
		case serverSecFirewall:
			a.serverFirewallSection(c, s)
		case serverSecSites:
			a.serverSitesSection(c, s)
		default:
			a.serverServicesSection(c, s)
		}
	})
}

// refreshServerSection reads again what the section shown reads.
func (a *App) refreshServerSection(s *Session) {
	if s.isOpenWrt() {
		a.refreshOpenWrtSection(s)
		return
	}
	switch s.server.section {
	case serverSecLogs:
		a.refreshServerLogs(s)
	case serverSecCron:
		a.refreshServerCron(s)
	case serverSecDisk:
		a.refreshServerDisk(s)
	case serverSecUpdates:
		a.refreshServerUpdates(s)
	case serverSecSecurity:
		a.refreshServerSecurity(s)
	case serverSecFirewall:
		a.refreshServerFirewall(s)
	case serverSecSites:
		a.refreshServerSites(s)
	default:
		a.refreshServerServices(s)
	}
}

// serverSectionTodo is a section not built yet.
func serverSectionTodo(c *ui.Context) {
	overviewMessage(c, "server", locales.DockerPageSoon(), nil)
}
