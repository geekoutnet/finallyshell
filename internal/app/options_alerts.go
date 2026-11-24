package app

import (
	"maps"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const pageAlerts = "alerts"

func (a *App) alertsPage(c *ui.Context) {
	state := &a.alerts
	ui.Column(c).Gap(10).FillWidth().Children(func() {
		widgets.Muted(c, locales.AlertWhileRunning()).FontSize(12)
		label := locales.AlertAllHosts()
		if host := a.hosts.Host(state.scope); host != nil && !host.Deleted() && host.Kind == config.KindSSH {
			label = host.Title()
		} else {
			state.scope = ""
		}
		ui.MenuButton(c, label, func(menu *ui.Menu) {
			if menu.Item(locales.AlertAllHosts()).Chosen() {
				state.scope = ""
			}
			for _, host := range a.folderHosts(&a.connTree, "") {
				if host.Kind == config.KindSSH && menu.Item(host.Title()).Subtitle(host.Target()).Chosen() {
					state.scope = host.ID
				}
			}
		}).FillWidth().Label(locales.AlertScope())
		rules := a.settings.AlertsFor(state.scope)
		if state.scope != "" {
			custom := false
			if a.settings.AlertRules != nil {
				_, custom = (*a.settings.AlertRules)[state.scope]
			}
			if ui.Checkbox(c, &custom, locales.AlertOverride()).Changed() {
				a.setAlertRules(state.scope, rules, custom)
			}
			if !custom {
				widgets.Muted(c, locales.AlertInherits()).FontSize(12)
				return
			}
		}
		before := rules
		ui.Checkbox(c, &rules.Unreachable, locales.AlertUnreachable())
		ui.Checkbox(c, &rules.Services, locales.AlertService())
		widgets.Muted(c, locales.AlertThresholdNote()).FontSize(12)
		alertThreshold(c, locales.AlertCPUPercent(), &rules.CPU, 100, 1)
		if rules.CPU > 0 {
			minutes := float64(max(1, rules.CPUMinutes))
			if alertThreshold(c, locales.AlertMinutes(), &minutes, 60, 1) {
				rules.CPUMinutes = max(1, int(minutes))
			}
		}
		alertThreshold(c, locales.AlertMemoryPercent(), &rules.Memory, 100, 1)
		alertThreshold(c, locales.AlertDiskPercent(), &rules.Disk, 100, 1)
		alertThreshold(c, locales.AlertLoadFactor(), &rules.Load, 64, 0.1)
		if rules != before {
			a.setAlertRules(state.scope, rules, true)
		}
	})
}

func alertThreshold(c *ui.Context, label string, value *float64, maxValue, step float64) (changed bool) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Grow(1).MinWidth(0)
		changed = ui.NumberInput(c, value, 0, maxValue, step).Width(100).Shrink(0).Label(label).Changed()
	})
	return changed
}

func (a *App) setAlertRules(scope string, rules config.AlertRules, custom bool) {
	a.changeSettings(func() {
		settings := make(map[string]config.AlertRules)
		if a.settings.AlertRules != nil {
			maps.Copy(settings, *a.settings.AlertRules)
		}
		if !custom || scope == "" && rules == (config.AlertRules{}) {
			delete(settings, scope)
		} else {
			settings[scope] = rules
		}
		a.settings.AlertRules = nil
		if len(settings) > 0 {
			a.settings.AlertRules = &settings
		}
		a.syncAlertSessions()
	})
}
