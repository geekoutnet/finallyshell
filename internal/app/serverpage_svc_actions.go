package app

import (
	"context"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type serviceAction struct {
	id                 string
	label, consequence func() string
}

var serviceActions = []serviceAction{
	{"start", locales.SvcStart, locales.SvcStartNote},
	{"stop", locales.SvcStop, locales.SvcStopNote},
	{"restart", locales.SvcRestart, locales.SvcRestartNote},
	{"reload", locales.SvcReload, locales.SvcReloadNote},
	{"enable", locales.SvcEnable, locales.SvcEnableNote},
	{"disable", locales.SvcDisable, locales.SvcDisableNote},
	{"enable-now", locales.SvcEnableNow, locales.SvcEnableNowNote},
	{"mask", locales.SvcMask, locales.SvcMaskNote},
	{"unmask", locales.SvcUnmask, locales.SvcUnmaskNote},
	{"reset-failed", locales.SvcReset, locales.SvcResetNote},
}

func serviceReason(systemd bool, rows []monitor.ServiceUnit, action string) string {
	if !systemd {
		return locales.SvcLegacy()
	}
	if len(rows) == 0 {
		return locales.SvcSelect()
	}
	for _, row := range rows {
		if !monitor.ValidService(row.Name) {
			return locales.SvcWrongState()
		}
		masked := strings.HasPrefix(row.Enabled, "masked") || row.Load == "masked"
		active := row.Active == "active" || row.Active == "activating" || row.Active == "reloading"
		template := strings.HasSuffix(row.Name, "@.service")
		missing := row.Load == "not-found" && row.Enabled == ""
		valid := false
		switch action {
		case "start":
			valid = !active && !masked && !template && !missing
		case "stop":
			valid = active
		case "restart":
			valid = active && !masked
		case "reload":
			valid = active && row.CanReload && !masked
		case "enable", "enable-now":
			valid = (row.Enabled == "disabled" || row.Enabled == "enabled-runtime") && !template && !masked
		case "disable":
			valid = strings.HasPrefix(row.Enabled, "enabled")
		case "mask":
			valid = !masked && !missing && !template
		case "unmask":
			valid = masked
		case "reset-failed":
			valid = row.Active == "failed"
		}
		if !valid {
			return locales.SvcActionUnavailable(row.Name)
		}
	}
	return ""
}

func (a *App) serviceMenu(menu *ui.Menu, s *Session, rows []monitor.ServiceUnit) {
	page := &s.server.services
	reason := ""
	if !page.systemd {
		reason = locales.SvcLegacy()
	} else if len(rows) != 1 {
		reason = locales.SvcSelectOne()
	}
	if menu.Item(locales.SvcDetails()).Disabled(reason != "").Subtitle(reason).Chosen() && reason == "" {
		a.serviceDetails(s, rows[0].Name)
	}
	if menu.Item(locales.SvcJournal()).Disabled(reason != "").Subtitle(reason).Chosen() && reason == "" {
		a.openServiceJournal(s, rows[0].Name)
	}
	editReason := reason
	if editReason == "" && (strings.HasSuffix(rows[0].Name, "@.service") || rows[0].Load == "not-found") {
		editReason = locales.SvcWrongState()
	}
	if menu.Item(locales.SvcEdit()).Disabled(editReason != "").Subtitle(editReason).Chosen() && editReason == "" {
		a.serviceOverride(s, rows[0].Name)
	}
	menu.Separator()
	for _, action := range serviceActions {
		reason := serviceReason(page.systemd, rows, action.id)
		item := menu.Item(action.label()).Disabled(reason != "").Subtitle(reason)
		if action.id == "mask" || action.id == "stop" {
			item.Danger()
		}
		if item.Chosen() && reason == "" {
			a.confirmService(s, rows, action)
		}
	}
}

func (a *App) confirmService(s *Session, rows []monitor.ServiceUnit, action serviceAction) {
	page := &s.server.services
	if serviceReason(page.systemd, rows, action.id) != "" {
		return
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	script, err := monitor.ServiceActionCommand(action.id, names...)
	if err != nil {
		return
	}
	dialog := &systemDialog{client: s.client, title: action.label(), target: strings.Join(names, "\n"), consequence: action.consequence()}
	dialog.confirm = func() { a.systemWork(s, dialog, a.systemRoot(s), script, nil, a.refreshServerServices) }
	page.dialog = dialog
}

func (a *App) serviceOverride(s *Session, name string) {
	file, err := monitor.ServiceOverridePath(name)
	if err == nil {
		a.systemFileEditor(s, &s.server.services.dialog, file, locales.SvcEdit(), locales.SvcOverrideNote(), a.refreshServerServices)
	}
}

func (a *App) serviceDetails(s *Session, name string) {
	if !monitor.ValidService(name) {
		return
	}
	dialog := &systemDialog{client: s.client, title: locales.SvcDetails(), target: name, busy: true}
	s.server.services.dialog = dialog
	ctx, cancel := context.WithTimeout(systemContext(s), 15*time.Second)
	dialog.cancel = cancel
	run := systemReader(s)
	go func() {
		defer cancel()
		var output [3]string
		for index, action := range [...]string{"status", "cat", "show"} {
			script, _ := monitor.ServiceReadCommand(action, name)
			out, stderr, err := run(ctx, script, nil, nil)
			output[index] = systemTail(systemRedact(out), 262144)
			if out == "" || index != 0 || strings.TrimSpace(stderr) != "" {
				detail := systemError(stderr, err)
				if detail != "" {
					output[index] = strings.TrimSpace(output[index] + "\n" + detail)
				}
			}
		}
		a.post(func() {
			dialog.busy = false
			section := 0
			status := monitor.ParseServiceStatus(output[0])
			dialog.body = func(c *ui.Context, height float32) {
				ui.Segmented(c, &section, locales.SvcState(), locales.SvcUnitFile(), locales.SvcProperties())
				if section == 0 && status.Active != "" {
					ui.Text(c, status.Active).SingleLine().Tooltip(status.Active).TextColor(theme.TextMuted)
				}
				ui.TextArea(c.Key(section), &output[section]).Label(locales.SvcDetails()).ReadOnly(true).Font("monospace").FillWidth().Height(min(420, height/2))
				ui.Row(c).Gap(6).Wrap().Children(func() {
					if ui.Button(c, locales.SvcJournal()).Clicked() {
						s.server.services.dialog = nil
						a.openServiceJournal(s, name)
					}
					if ui.Button(c, locales.SvcEdit()).Disabled(strings.HasSuffix(name, "@.service")).Clicked() {
						a.serviceOverride(s, name)
					}
				})
			}
		})
	}()
}
