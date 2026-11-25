package app

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"

	"finallyshell/internal/widgets"
	"finallyshell/locales"
)

// The desktop's terminal, browser and app center, and the session's tools
// (Docker, databases, the System tab's sections…) as desktop apps.

func init() {
	registerDesktopApp(desktopApp{
		id: appTerminal, icon: "square-terminal", tint: ui.Hex("#1f2937"), name: locales.DesktopAppTerminal,
		category: desktopCatSystem, order: 30, onDesktop: true, pinned: true,
		size: desktopSize{W: 760, H: 440}, minSize: desktopSize{W: 390, H: 250},
		view: (*App).desktopTerminalView, opened: (*App).desktopTerminalOpened, closing: (*App).desktopTerminalClosing,
	})
	registerDesktopApp(desktopApp{
		id: appBrowser, icon: "globe", tint: ui.Hex("#2563eb"), name: locales.DesktopAppBrowser,
		category: desktopCatTools, order: 70, onDesktop: true,
		size: desktopSize{W: 650, H: 420}, minSize: desktopSize{W: 420, H: 365}, single: true,
		view: (*App).desktopBrowserView, opened: (*App).desktopBrowserOpened, closing: (*App).desktopBrowserClosing,
	})
	registerDesktopApp(desktopApp{
		id: appAppCenter, icon: "layout-grid", tint: ui.Hex("#7c3aed"), name: locales.DesktopAppAppCenter,
		category: desktopCatSystem, order: 90, onDesktop: true,
		size: desktopSize{W: 900, H: 590}, minSize: desktopSize{W: 390, H: 350}, single: true,
		view: (*App).desktopCenterView, opened: (*App).desktopCenterOpened,
	})
	for index, tool := range desktopServerTools() {
		registerDesktopApp(desktopApp{
			id: tool.id, icon: tool.icon, tint: ui.Hex(tool.tint), name: tool.name,
			category: desktopCatServer, order: 100 + index, onDesktop: true, single: true,
			minSize: desktopSize{W: tool.width, H: 410}, view: tool.view,
			opened: func(a *App, w *desktopWindow, _ any) { a.desktopToolsNoteOpened(w) },
		})
	}
}

// desktopOpenTerminalAt opens a terminal window on s's desktop whose shell
// starts in the remote folder dir. The file manager's "Terminal" button
// calls it.
func (a *App) desktopOpenTerminalAt(s *Session, dir string) {
	a.desktopOpen(s, appTerminal, dir)
}

var desktopToolsTabApps = map[int]desktopAppID{
	tabCaddy: "srv-caddy",
	tabFiles: appFiles, tabDocker: "srv-docker", tabDatabase: "srv-db", tabProcesses: "srv-processes",
	tabNetwork: "srv-network", tabTunnels: "srv-tunnels", tabTransfers: "srv-transfers",
	tabCommands: "srv-commands", tabOverview: "srv-overview",
}

var desktopToolsSectionApps = map[int]desktopAppID{
	serverSecServices: "srv-services", serverSecLogs: "srv-logs", serverSecCron: "srv-cron",
	serverSecDisk: "srv-disk", serverSecUpdates: "srv-updates", serverSecSecurity: "srv-security",
	serverSecFirewall: "srv-firewall", serverSecSites: "srv-sites",
}

func (a *App) desktopToolsOpenTab(s *Session, tab int, arg any) bool {
	if current := a.current(); !current.isDesktopRow() || current.panelOf != s {
		return false
	}
	id := desktopToolsTabApps[tab]
	if tab == tabSystem {
		id = desktopToolsSectionApps[s.server.section]
	}
	return id != "" && a.desktopOpen(s, id, arg) != nil
}

func (a *App) desktopToolsViewing(s *Session, id desktopAppID) bool {
	current := a.current()
	if !current.isDesktopRow() || current.panelOf != s {
		return false
	}
	window := current.desktop.focused
	return window != nil && !window.minimized && window.app.id == id
}

func (a *App) desktopToolsNoteOpened(w *desktopWindow) {
	if w.app.id == appAppCenter {
		return
	}
	recent := w.session.desktopToolsRecent
	recent = slices.DeleteFunc(recent, func(id desktopAppID) bool { return id == w.app.id })
	recent = slices.Insert(recent, 0, w.app.id)
	w.session.desktopToolsRecent = recent[:min(len(recent), 8)]
}

func (a *App) desktopToolsLive(w *desktopWindow) bool {
	desktop := a.desktopOf(w.session)
	return desktop != nil && slices.Contains(desktop.windows, w)
}

type desktopToolsLifetime struct {
	stop context.CancelFunc
}

type desktopToolsSSHWatch struct {
	client *ssh.Client
	ended  chan struct{}
}

func (s *Session) desktopToolsSSHClosed() <-chan struct{} {
	if watch := s.desktopToolsSSH; watch != nil && watch.client == s.client {
		return watch.ended
	}
	watch := &desktopToolsSSHWatch{s.client, make(chan struct{})}
	s.desktopToolsSSH = watch
	go func() {
		_ = watch.client.Wait()
		close(watch.ended)
	}()
	return watch.ended
}

func (a *App) desktopToolsContext(w *desktopWindow) (context.Context, context.CancelFunc) {
	s := w.session
	ctx, cancel := context.WithCancel(systemContext(s))
	lifetime := &desktopToolsLifetime{cancel}
	if s.desktopToolsStops == nil {
		s.desktopToolsStops = make(map[*desktopWindow]*desktopToolsLifetime)
	}
	s.desktopToolsStops[w] = lifetime
	stop := func() {
		if s.desktopToolsStops[w] == lifetime {
			delete(s.desktopToolsStops, w)
		}
		cancel()
	}
	sshEnded := s.desktopToolsSSHClosed()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sshEnded:
				cancel()
				return
			case <-ticker.C:
				a.post(func() {
					if ctx.Err() == nil && !a.desktopToolsLive(w) {
						stop()
					}
				})
			}
		}
	}()
	return ctx, stop
}

func (s *Session) stopDesktopTools() {
	for _, lifetime := range s.desktopToolsStops {
		lifetime.stop()
	}
	clear(s.desktopToolsStops)
}

type desktopServerTool struct {
	id         desktopAppID
	icon, tint string
	name       func() string
	width      float32
	view       func(*App, *ui.Context, *desktopWindow)
}

func desktopServerTools() []desktopServerTool {
	return []desktopServerTool{
		{"srv-docker", "container", "#2563eb", locales.TabDocker, 750, desktopToolsTab(tabDocker)},
		{"srv-db", "database", "#b45309", locales.DbTab, 720, desktopToolsTab(tabDatabase)},
		{"srv-sites", "globe", "#047857", locales.DesktopToolsSites, 690, desktopToolsSection((*App).serverSitesSection, (*App).refreshServerSites)},
		{"srv-caddy", "shield-check", "#0e7490", locales.DesktopToolsCaddy, 750, desktopToolsCaddy},
		{"srv-services", "settings", "#4f46e5", locales.ServerServices, 710, desktopToolsSection((*App).serverServicesSection, (*App).refreshServerServices)},
		{"srv-logs", "file-text", "#7257a4", locales.ServerLogs, 750, desktopToolsSection((*App).serverLogsSection, (*App).refreshServerLogs)},
		{"srv-cron", "calendar-clock", "#a21caf", locales.ServerCron, 680, desktopToolsSection((*App).serverCronSection, (*App).refreshServerCron)},
		{"srv-disk", "hard-drive", "#475569", locales.ServerDisk, 690, desktopToolsSection((*App).serverDiskSection, (*App).refreshServerDisk)},
		{"srv-updates", "package", "#0369a1", locales.ServerUpdates, 650, desktopToolsSection((*App).serverUpdatesSection, (*App).refreshServerUpdates)},
		{"srv-security", "lock", "#9f1239", locales.ServerSecurity, 690, desktopToolsSection((*App).serverSecuritySection, (*App).refreshServerSecurity)},
		{"srv-firewall", "shield", "#b91c1c", locales.ServerFirewall, 700, desktopToolsSection((*App).serverFirewallSection, (*App).refreshServerFirewall)},
		{"srv-processes", "activity", "#0f766e", locales.TabProcesses, 700, desktopToolsTab(tabProcesses)},
		{"srv-network", "network", "#1d4ed8", locales.TabNetwork, 700, desktopToolsTab(tabNetwork)},
		{"srv-tunnels", "link", "#6d28d9", locales.TabTunnels, 710, desktopToolsTab(tabTunnels)},
		{"srv-transfers", "arrow-down-up", "#c2410c", locales.TabTransfers, 680, desktopToolsTab(tabTransfers)},
		{"srv-commands", "list", "#4338ca", locales.TabCommands, 660, desktopToolsTab(tabCommands)},
		{"srv-overview", "gauge", "#15803d", locales.TabOverview, 720, desktopToolsTab(tabOverview)},
	}
}

func desktopToolsTab(index int) func(*App, *ui.Context, *desktopWindow) {
	return func(a *App, c *ui.Context, _ *desktopWindow) { a.bottomTabs()[index].page(c) }
}

func desktopToolsSection(section func(*App, *ui.Context, *Session), refresh func(*App, *Session)) func(*App, *ui.Context, *desktopWindow) {
	return func(a *App, c *ui.Context, w *desktopWindow) {
		s := w.session
		ui.Column(c).Fill().MinHeight(0).Background(colors.overviewPage).Children(func() {
			switch {
			case s.status != statusConnected:
				overviewMessage(c, "unplug", s.statusText(), func() {
					if ui.Button(c, locales.Reconnect()).Disabled(s.status == statusConnecting).Clicked() {
						a.reconnect(s)
					}
				})
			case !execAllowed(s.Host):
				overviewMessage(c, "server", locales.SysInfoOff(), nil)
			default:
				ui.Row(c).FillWidth().Padding(4, 10).AlignItems(ui.Center).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
					ui.Text(c, s.label()).SingleLine().Ellipsis("…").MinWidth(0).Grow(1).TextColor(theme.TextMuted)
					if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Clicked() {
						refresh(a, s)
					}
				})
				section(a, c, s)
			}
		})
	}
}

func desktopToolsCaddy(a *App, c *ui.Context, _ *desktopWindow) {
	if panel, ok := any(a).(interface{ caddyPanel(*ui.Context) }); ok {
		panel.caddyPanel(c)
		return
	}
	ui.Column(c).Fill().Children(func() {
		overviewMessage(c, "shield-check", locales.DesktopToolsCaddyUnavailable(), nil)
	})
}

// openFilesAt shows the remote folder dir of s: in a Files window when the
// desktop is the current row, else in the file panel.
func (a *App) openFilesAt(s *Session, dir string) {
	if cur := a.current(); cur.isDesktopRow() && cur.panelOf == s {
		a.desktopOpen(s, appFiles, dir)
		return
	}
	a.openBottomTab(tabFiles)
	a.openDir(s, dir)
}
