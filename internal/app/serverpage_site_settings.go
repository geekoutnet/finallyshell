package app

import (
	"cmp"
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The Nginx settings tab of the Sites page, as a hosting panel has them:
// the service (start, stop, restart, reload, test), the main configuration
// file and the logs, and the performance settings of nginx.conf
// (monitor.NginxSettings), saved as an edit of the file is: with a backup,
// tested by nginx, reloaded, and undone at once when nginx refuses it.

// nginxTuning is the performance settings' form.
type nginxTuning struct {
	reading, loaded bool
	err             string
	// original is nginx.conf as read, values the settings it sets, and
	// edits the form's, by name.
	original string
	values   map[string]string
	edits    map[string]string
}

// nginxSettingNote is what each setting does, in the user's words.
var nginxSettingNote = map[string]func() string{
	"worker_processes":              locales.SiteTuneWorkerProcesses,
	"worker_connections":            locales.SiteTuneWorkerConnections,
	"keepalive_timeout":             locales.SiteTuneKeepalive,
	"client_max_body_size":          locales.SiteTuneBodySize,
	"client_header_buffer_size":     locales.SiteTuneHeaderBuffer,
	"client_body_buffer_size":       locales.SiteTuneBodyBuffer,
	"server_names_hash_bucket_size": locales.SiteTuneHashBucket,
	"gzip":                          locales.SiteTuneGzip,
	"gzip_min_length":               locales.SiteTuneGzipMin,
	"gzip_comp_level":               locales.SiteTuneGzipLevel,
	"server_tokens":                 locales.SiteTuneServerTokens,
}

// The default log files of nginx's packages.
const (
	nginxErrorLog  = "/var/log/nginx/error.log"
	nginxAccessLog = "/var/log/nginx/access.log"
)

func (a *App) nginxSettingsView(c *ui.Context, s *Session) {
	page := &s.server.sites
	t := &page.tuning
	if !t.loaded && !t.reading && siteGeneralReason(page) == "" && page.inventory.Main != "" && (page.dialog == nil || !page.dialog.busy) {
		a.readNginxTuning(s)
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(8, 12, 12, 12).Gap(12).Children(func() {
			if reason := siteGeneralReason(page); reason != "" && !page.reading {
				siteBanner(c, reason, true)
			}
			a.nginxServiceCard(c, s)
			a.nginxFilesCard(c, s)
			a.nginxTuningCard(c, s)
		})
	})
}

// nginxCard is a titled section of the tab.
func nginxCard(c *ui.Context, title string, body func()) {
	ui.Column(c).FillWidth().Gap(8).Padding(12, 14).Radius(8).Border(1, theme.Border).Background(colors.card).Children(func() {
		ui.Text(c, title).Bold().FontSize(14)
		body()
	})
}

// nginxRunning reports whether nginx runs, as systemd or its master's
// process says.
func nginxRunning(status monitor.NginxStatus) bool {
	return status.State == "active" || !status.Unit && status.PID > 0
}

func (a *App) nginxServiceCard(c *ui.Context, s *Session) {
	page := &s.server.sites
	status := page.status
	nginxCard(c, locales.SiteServiceTitle(), func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			color, state := theme.TextMuted, locales.SiteStateUnknown()
			switch {
			case !status.Installed:
				state = locales.SiteNotInstalled()
			case nginxRunning(status):
				color, state = theme.Success, locales.SiteRunning()
			case status.State == "failed":
				color, state = theme.Danger, locales.SiteFailed()
			case status.State == "inactive":
				color, state = theme.Warning, locales.SiteStopped()
			case status.State != "":
				state = locales.SiteChanging()
			}
			ui.Box(c).Size(9, 9).Radius(5).Background(color).Shrink(0)
			ui.Text(c, state).Bold()
			info := []string{cmp.Or(siteClean(status.Version), "Nginx")}
			if status.PID > 0 {
				info = append(info, "PID "+strconv.Itoa(status.PID))
			}
			if status.Unit {
				info = append(info, "systemd")
			}
			widgets.Muted(c, strings.Join(info, " · ")).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
		})
		running := nginxRunning(status)
		mutate := siteMutationReason(page)
		when := func(cond bool, reason string) string {
			if cond {
				return reason
			}
			return ""
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			start := cmp.Or(mutate, when(running, locales.SiteRunning()))
			if ui.PrimaryButton(c, locales.SiteStart()).Disabled(start != "").Tooltip(start).Clicked() {
				a.siteServiceAction(s, "start", locales.SiteStart(), "")
			}
			stop := cmp.Or(mutate, when(!running, locales.SiteNotRunning()))
			if ui.Button(c, locales.SiteStop()).Disabled(stop != "").Tooltip(stop).Clicked() {
				a.siteServiceAction(s, "stop", locales.SiteStop(), locales.SiteStopWarning())
			}
			restart := cmp.Or(mutate, when(!status.Unit, locales.SiteNeedSystemd()))
			if ui.Button(c, locales.SiteRestart()).Disabled(restart != "").Tooltip(restart).Clicked() {
				a.siteServiceAction(s, "restart", locales.SiteRestart(), locales.SiteRestartWarning())
			}
			reload := cmp.Or(mutate, when(!running, locales.SiteNotRunning()))
			if ui.Button(c, locales.SiteReload()).Disabled(reload != "").Tooltip(reload).Clicked() {
				a.siteServiceAction(s, "reload", locales.SiteReload(), locales.SiteReloadWarning())
			}
			test := siteGeneralReason(page)
			if ui.Button(c, locales.SiteTest()).Disabled(test != "").Tooltip(test).Clicked() {
				a.siteServiceAction(s, "test", locales.SiteTest(), "")
			}
		})
	})
}

func (a *App) nginxFilesCard(c *ui.Context, s *Session) {
	page := &s.server.sites
	main := cmp.Or(page.inventory.Main, monitor.NginxMain)
	nginxCard(c, locales.SiteFilesTitle(), func() {
		row := func(label, file, button, reason string, open func()) {
			ui.Row(c.Key(label)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).Width(120).Shrink(0)
				ui.Text(c, file).Font("monospace").FontSize(12).SingleLine().Grow(1).MinWidth(0).Shrink(1).Selectable()
				if ui.Button(c, button).Label(label + " · " + button).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
					open()
				}
			})
		}
		row(locales.SiteMainConfig(), main, locales.Edit(), siteReadyReason(page), func() {
			a.siteEditor(s, main, true)
		})
		for _, log := range []struct{ label, file string }{{locales.SiteErrorLog(), nginxErrorLog}, {locales.SiteAccessLog(), nginxAccessLog}} {
			row(log.label, log.file, locales.SiteViewLog(), siteGeneralReason(page), func() { a.siteOpenLog(s, log.label, log.file) })
		}
	})
}

// siteOpenLog shows the log file: on the Logs page, or, for a file only
// root reads, in a dialog.
func (a *App) siteOpenLog(s *Session, label, file string) {
	if monitor.ValidLogPath(file) {
		logs := &s.server.logs
		logs.path, logs.sourceMode, logs.priority, logs.since = file, 2, 0, 0
		a.setSystemLog(s, monitor.JournalSource{File: file, Lines: 300})
		a.openServerPage(s, serverSecLogs)
		return
	}
	if script := monitor.NginxLogsCommand(file); script != "" {
		a.siteCommand(s, label, file, "", script, nil, false)
	}
}

// readNginxTuning reads nginx.conf for the performance settings.
func (a *App) readNginxTuning(s *Session) {
	page := &s.server.sites
	t := &page.tuning
	main := page.inventory.Main
	script, err := monitor.NginxReadCommand(main)
	if err != nil {
		t.loaded, t.err = true, locales.SiteCustom()
		return
	}
	t.reading, t.err = true, ""
	client := s.client
	run := a.sitesRoot(s)
	go func() {
		ctx, cancel := context.WithTimeout(systemContext(s), 30*time.Second)
		defer cancel()
		text, stderr, err := run(ctx, script, nil, nil)
		a.post(func() {
			if page.client != client {
				return
			}
			t.reading, t.loaded = false, true
			var values map[string]string
			switch {
			case err != nil:
				t.err = siteError(stderr, err)
				page.denied = page.denied || siteRootDenied(err)
				return
			case !utf8.ValidString(text) || !monitor.NginxConfigSafe(text) || systemRedact(text) != text:
				t.err = locales.SiteUnsafeText()
				return
			}
			if values, err = monitor.ReadNginxSettings(text); err != nil {
				t.err = locales.SiteTuneUnreadable()
				return
			}
			t.original, t.values, t.edits = text, values, map[string]string{}
			for name, value := range values {
				t.edits[name] = value
			}
		})
	}()
}

func (a *App) nginxTuningCard(c *ui.Context, s *Session) {
	page := &s.server.sites
	t := &page.tuning
	nginxCard(c, locales.SiteTuningTitle(), func() {
		widgets.Muted(c, locales.SiteTuningNote()).FontSize(12).MaxWidthPercent(100)
		switch {
		case t.reading:
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(12, 12)
				widgets.Muted(c, locales.SiteTuneReading()).FontSize(12)
			})
			return
		case t.err != "":
			ui.Text(c, t.err).TextColor(theme.Danger).FontSize(12).MaxWidthPercent(100)
			if ui.Button(c, locales.Refresh()).AlignSelf(ui.Start).Clicked() {
				t.loaded, t.err = false, ""
			}
			return
		case t.edits == nil:
			return
		}
		invalid := false
		for _, setting := range monitor.NginxSettings {
			value := t.edits[setting.Name]
			ui.Row(c.Key(setting.Name)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, setting.Name).Font("monospace").FontSize(12).Width(230).Shrink(0)
				if setting.Default == "on" || setting.Default == "off" {
					on := cmp.Or(value, setting.Default) == "on"
					ui.Row(c).Width(150).Shrink(0).Children(func() {
						if ui.Switch(c, &on).Label(setting.Name).Changed() {
							t.edits[setting.Name] = "off"
							if on {
								t.edits[setting.Name] = "on"
							}
						}
					})
				} else {
					field := ui.TextInput(c, &value).Label(setting.Name).Placeholder(setting.Default).Width(150).Shrink(0)
					if field.Changed() {
						t.edits[setting.Name] = strings.TrimSpace(value)
					}
					if v := t.edits[setting.Name]; v != "" && !setting.Valid(v) {
						invalid = true
						field.Border(1, theme.Danger)
					}
				}
				note := nginxSettingNote[setting.Name]() + " · " + locales.SiteTuneDefault(setting.Default)
				widgets.Muted(c, note).FontSize(12).Grow(1).MinWidth(0).Shrink(1).MaxWidthPercent(100)
			})
		}
		changes := nginxTuningChanges(t)
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if invalid {
				ui.Text(c, locales.SiteTuneInvalid()).TextColor(theme.Danger).FontSize(12)
			}
			if ui.Button(c, locales.SiteTuneReset()).Disabled(len(changes) == 0).Clicked() {
				t.edits = map[string]string{}
				for name, value := range t.values {
					t.edits[name] = value
				}
			}
			reason := siteReadyReason(page)
			if ui.PrimaryButton(c, locales.Save()).Disabled(invalid || len(changes) == 0 || reason != "").Tooltip(reason).Clicked() {
				a.saveNginxTuning(s, changes)
			}
		})
	})
}

// nginxTuningChanges are the settings the form changes, by name.
func nginxTuningChanges(t *nginxTuning) map[string]string {
	changes := map[string]string{}
	for _, setting := range monitor.NginxSettings {
		if v := t.edits[setting.Name]; v != "" && v != t.values[setting.Name] {
			changes[setting.Name] = v
		}
	}
	return changes
}

// saveNginxTuning asks, then writes the changed settings to nginx.conf,
// tests it and reloads nginx, as the editor saves a file.
func (a *App) saveNginxTuning(s *Session, changes map[string]string) {
	page := &s.server.sites
	t := &page.tuning
	text, err := monitor.ApplyNginxSettings(t.original, changes)
	if err != nil {
		t.err = locales.SiteTuneUnreadable()
		return
	}
	main := page.inventory.Main
	change, err := monitor.NewNginxChange(main, t.original, text, time.Now(), page.inventory.NginxTree)
	if err != nil {
		t.err = locales.SiteUnsafeText()
		return
	}
	script, err := change.SaveCommand(t.original, page.inventory.NginxTree)
	if err != nil {
		t.err = locales.SiteUnsafeText()
		return
	}
	var lines []string
	for _, setting := range monitor.NginxSettings {
		if v, ok := changes[setting.Name]; ok {
			lines = append(lines, setting.Name+": "+cmp.Or(t.values[setting.Name], setting.Default)+" → "+v)
		}
	}
	reload := nginxRunning(page.status)
	dialog := &systemDialog{client: s.client, title: locales.SiteTuneSaveTitle(), target: main + "\n" + strings.Join(lines, "\n"),
		consequence: locales.SiteSaveWarning(change.Backup) + " " + siteApplyHint(reload)}
	page.dialog = dialog
	dialog.confirm = func() {
		// Read again once written, or once undone.
		t.loaded = false
		a.siteWriteConfig(s, dialog, change, script, text, reload, false, nil)
	}
}
