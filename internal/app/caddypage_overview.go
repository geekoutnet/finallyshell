package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type caddyOverviewPage struct {
	requested bool
	data      caddyOverviewData
}

type caddyOverviewData struct {
	service                                      caddyServiceInfo
	latest, admin                                string
	sites, certs, expiring, upstreams, unhealthy int
}

func (a *App) refreshCaddyOverview(s *Session) { a.caddyRefresh(s) }

func (a *App) caddyOverviewSection(c *ui.Context, s *Session) {
	if !a.caddyDetected(c, s, false) {
		return
	}
	page, install := &s.caddy.overview, s.caddy.state.install
	if !page.requested {
		page.requested = true
		page.data = caddyOverviewData{sites: -1, certs: -1, upstreams: -1}
		caddyRead(a, s, readCaddyOverview, func(data caddyOverviewData, _ error) { page.data = data })
	}
	actions := a.caddyServiceActions(s, install)
	caddyActions(c, actions...)
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		column := ui.Column(c).FillWidth().Padding(8, 12).Gap(8)
		column.ContextMenu(func(menu *ui.Menu) { caddyMenu(menu, actions...) })
		column.Children(func() {
			ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Caddy").FontSize(19).Bold()
				state := ui.Text(c, caddyActiveName(install.Active)).Bold()
				switch install.Active {
				case "active", "running":
					state.TextColor(theme.Accent)
				case "failed", "dead", "exited":
					state.TextColor(theme.Danger)
				default:
					state.TextColor(theme.TextMuted)
				}
				ui.Text(c, caddyInstallName(install.Method)).TextColor(theme.TextMuted)
				if version := strings.Fields(install.Version); len(version) > 0 {
					ui.Text(c, version[0]).SingleLine().Tooltip(install.Version)
				}
				if latest := caddyPanelLatest(s); latest != "" {
					reason := caddyUpdateReason(install, false)
					if ui.Button(c, locales.CpanelUpdateAvailable(latest)).Disabled(reason != "").Tooltip(reason).Clicked() {
						a.openCaddyPage(s, caddySecInstall)
					}
				}
			})
			if len(install.Conflicts) != 0 {
				ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.CpanelConflicts(strings.Join(install.Conflicts, ", "))).TextColor(theme.Warning)
					if ui.Button(c, locales.ServerSites()).Clicked() {
						a.openServerPage(s, serverSecSites)
					}
				})
			}
			caddyOverviewDetails(c, install, page.data)
		})
	})
}

func caddyActiveName(active string) string {
	switch active {
	case "active", "running":
		return locales.CpanelActive()
	case "failed", "dead", "exited":
		return locales.CpanelFailed()
	case "inactive", "stopped", "created":
		return locales.CpanelInactive()
	case "":
		return locales.CpanelUnknown()
	default:
		return active
	}
}

func caddyOverviewDetails(c *ui.Context, install monitor.CaddyInstall, data caddyOverviewData) {
	adapter := install.Adapter
	if adapter == "" {
		adapter = "caddyfile"
	}
	admin := install.AdminAddr
	if data.admin != "" && admin != "off" {
		admin += " · " + data.admin
	}
	rows := []labelValue{
		{locales.CpanelService(), install.Unit + install.Container},
		{locales.CpanelAutostart(), caddyBool(install.Enabled)},
		{locales.CpanelBinary(), install.Binary},
		{locales.CpanelConfigPath(), install.Config},
		{locales.CpanelAdapter(), adapter},
		{locales.CpanelManaged(), caddyBool(install.Managed)},
		{locales.CpanelAdmin(), admin},
		{locales.CpanelDataDir(), install.DataDir},
		{locales.CpanelUser(), install.User},
		{locales.CpanelListening(), strings.Join(install.Listening, ", ")},
		{locales.CpanelModules(), strings.Join(install.Modules, ", ")},
		{locales.CpanelSince(), data.service.Since},
	}
	if data.service.PID > 0 {
		rows = append(rows, labelValue{"PID", strconv.Itoa(data.service.PID)})
	}
	if data.service.Memory > 0 {
		rows = append(rows, labelValue{locales.CpanelMemory(), formatBytes(float64(data.service.Memory))})
	}
	if data.sites >= 0 {
		rows = append(rows, labelValue{locales.CaddySites(), strconv.Itoa(data.sites)})
	}
	if data.certs >= 0 {
		rows = append(rows, labelValue{locales.CaddyCerts(), locales.CpanelCertificateCount(data.certs, data.expiring)})
	}
	if data.upstreams >= 0 {
		rows = append(rows, labelValue{locales.CpanelUpstreams(), locales.CpanelUpstreamCount(data.upstreams-data.unhealthy, data.unhealthy)})
	}
	grid := ui.Row(c).Gap(8).Wrap().FillWidth()
	width := float32(238)
	if available := grid.LayoutBounds().W; available > 0 {
		columns := max(1, int((available+8)/205))
		width = (available - float32(columns-1)*8) / float32(columns)
	}
	grid.Children(func() {
		for _, row := range rows {
			if strings.TrimSpace(row.value) == "" {
				continue
			}
			ui.Column(c).Width(width).MinHeight(64).Padding(6, 8).Gap(3).BorderWidth(1).BorderColor(colors.line).Radius(5).Children(func() {
				ui.Text(c, row.label).FontSize(12).TextColor(theme.TextMuted).FillWidth()
				ui.Text(c, row.value).FillWidth().Tooltip(row.value).Selectable()
			})
		}
	})
}

func caddyBool(enabled bool) string {
	if enabled {
		return locales.CpanelYes()
	}
	return locales.CpanelNo()
}

func readCaddyOverview(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyOverviewData, error) {
	data := caddyOverviewData{sites: -1, certs: -1, upstreams: -1}
	data.service, _ = backend.serviceInfo(ctx, run, install)
	if install.PackageLatest == "" && (install.Method == monitor.CaddyBinary || install.Method == monitor.CaddyDocker) {
		data.latest, _ = backend.latest(ctx)
	}
	if caddyAdminReason(install) == "" {
		data.admin = locales.CpanelUnreachable()
		if config, err := backend.admin(ctx, run, install, "GET", "/config/", ""); err == nil {
			data.admin = locales.CpanelReachable()
			data.sites = caddySiteCount(config)
		}
		if text, err := backend.admin(ctx, run, install, "GET", "/reverse_proxy/upstreams", ""); err == nil {
			var upstreams []struct {
				Fails int `json:"fails"`
			}
			if json.Unmarshal([]byte(text), &upstreams) == nil {
				data.upstreams = len(upstreams)
				for _, upstream := range upstreams {
					if upstream.Fails > 0 {
						data.unhealthy++
					}
				}
			}
		}
	}
	if data.sites < 0 && caddyIsCaddyfile(install) {
		if config, err := backend.adapt(ctx, run, install); err == nil {
			data.sites = caddySiteCount(config)
		}
	}
	if certs, err := backend.certs(ctx, run, install); err == nil {
		data.certs = len(certs.Rows)
		for _, cert := range certs.Rows {
			if caddyDaysLeft(cert.Expires, time.Now()) < 30 {
				data.expiring++
			}
		}
	}
	return data, nil
}

func caddySiteCount(config string) int {
	var document struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct{ Match []struct{ Host []string } }
				}
			}
		}
	}
	if json.Unmarshal([]byte(config), &document) != nil {
		return -1
	}
	hosts := map[string]bool{}
	for _, server := range document.Apps.HTTP.Servers {
		for _, route := range server.Routes {
			for _, match := range route.Match {
				for _, host := range match.Host {
					hosts[host] = true
				}
			}
		}
	}
	return len(hosts)
}

var caddyVersionNumber = regexp.MustCompile(`\d+\.\d+\.\d+`)
var caddyContainerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func caddyPanelLatest(s *Session) string {
	install := s.caddy.state.install
	latest := install.PackageLatest
	if latest == "" {
		latest = s.caddy.overview.data.latest
	}
	current := caddyVersionNumber.FindString(install.Version)
	candidate := caddyVersionNumber.FindString(latest)
	if current == "" || candidate == "" {
		return ""
	}
	left, right := strings.Split(current, "."), strings.Split(candidate, ".")
	for index := range left {
		old, _ := strconv.Atoi(left[index])
		newer, _ := strconv.Atoi(right[index])
		if newer > old {
			return latest
		}
		if newer < old {
			break
		}
	}
	return ""
}

func caddyServiceCommand(install monitor.CaddyInstall, action string) (string, error) {
	if action == "validate" {
		if !caddyValidPath(install.Config) {
			return "", errCaddyPanelTarget
		}
		command := []string{install.Binary, "validate", "--config", install.Config}
		if install.Method == monitor.CaddyDocker && caddyContainerName.MatchString(install.Container) && caddyValidPath(install.ContainerConfig) {
			command = []string{"docker", "exec", install.Container, "caddy", "validate", "--config", install.ContainerConfig}
		} else if !caddyValidPath(install.Binary) || install.Method == monitor.CaddyDocker {
			return "", errCaddyPanelTarget
		}
		if install.Adapter != "" && install.Adapter != "json" {
			if strings.ContainsAny(install.Adapter, "\x00\r\n") {
				return "", errCaddyPanelTarget
			}
			command = append(command, "--adapter", install.Adapter)
		}
		return monitor.SystemCommand(command...), nil
	}
	switch action {
	case "start", "stop", "restart", "reload", "enable", "disable":
	default:
		return "", errCaddyPanelTarget
	}
	return monitor.CaddyServiceCommand(install, action)
}

func (a *App) caddyServiceActions(s *Session, install monitor.CaddyInstall) []caddyAction {
	active := caddyServiceRunning(install)
	busy := install.Active == "activating" || install.Active == "deactivating" || install.Active == "reloading"
	actions := make([]caddyAction, 0, 9)
	for _, item := range []struct {
		id, name, note string
		enabled        bool
	}{
		{"start", locales.SvcStart(), locales.SvcStartNote(), !active && len(install.Conflicts) == 0},
		{"stop", locales.SvcStop(), locales.CpanelStopWarning(), active},
		{"restart", locales.SvcRestart(), locales.SvcRestartNote(), active},
		{"reload", locales.CpanelReload(), locales.SvcReloadNote(), active},
		{"validate", locales.CpanelValidate(), locales.CpanelValidateNote(), true},
		{"enable", locales.SvcEnable(), locales.SvcEnableNote(), !install.Enabled},
		{"disable", locales.SvcDisable(), locales.SvcDisableNote(), install.Enabled},
	} {
		command, err := caddyServiceCommand(install, item.id)
		reason := ""
		if item.id == "reload" && install.AdminAddr == "off" {
			reason = locales.CpanelAdminOff()
		} else if item.id == "validate" && caddyValidateReason(install) != "" {
			reason = caddyValidateReason(install)
		} else if install.Method == monitor.CaddyDocker && install.ContainerAutoRemove && item.id != "validate" && item.id != "reload" {
			reason = locales.CwireDockerLifecycle()
		} else if install.ServiceManager == "systemd" && (item.id == "enable" || item.id == "disable") && install.UnitFileState != "" && install.UnitFileState != "enabled" && install.UnitFileState != "enabled-runtime" && install.UnitFileState != "disabled" {
			reason = locales.CwireNoAutostart(install.UnitFileState)
		} else if err != nil {
			reason = locales.CpanelNoTarget()
		} else if item.id == "start" && len(install.Conflicts) > 0 {
			reason = locales.CpanelConflicts(strings.Join(install.Conflicts, ", "))
		} else if !item.enabled || busy && item.id != "validate" {
			reason = locales.SvcWrongState()
		}
		actions = append(actions, caddyAction{item.name, reason, func() {
			a.caddyConfirm(s, item.name, command, item.note, func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
				if item.id == "validate" {
					return backend.validate(ctx, run, install, nil)
				}
				return backend.service(ctx, run, install, item.id, progress)
			})
		}})
	}
	dataReason := ""
	if !caddyValidPath(install.DataDir) || install.Method == monitor.CaddyDocker {
		dataReason = locales.CpanelNoTarget()
	}
	return append(actions, caddyAction{locales.CpanelOpenConfig(), "", func() { a.openCaddyPage(s, caddySecConfig) }},
		caddyAction{locales.CpanelOpenData(), dataReason, func() {
			a.openBottomTab(tabFiles)
			a.openDir(s, install.DataDir)
		}})
}

func caddyApplyOutput(result monitor.CaddyApplyResult) string {
	text := result.Validate
	if result.Backup != "" {
		text += "\n" + locales.CpanelBackupLocation(result.Backup)
	}
	if result.RolledBack {
		text += "\n" + locales.CpanelRolledBack()
	}
	return strings.TrimSpace(text)
}
