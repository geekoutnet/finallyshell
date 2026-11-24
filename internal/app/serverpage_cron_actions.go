package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) cronBrowse(s *Session) {
	page := &s.server.cron
	dialog := &systemDialog{client: s.client, title: locales.CronBrowse(), busy: true}
	page.dialog = dialog
	run := systemReader(s)
	ctx, cancel := context.WithTimeout(systemContext(s), 15*time.Second)
	dialog.cancel = cancel
	user := systemUser(s)
	go func() {
		defer cancel()
		users, userErr, err := run(ctx, monitor.CronUsers, nil, nil)
		files, fileErr, filesErr := run(ctx, monitor.CronSystemFiles, nil, nil)
		sources := cronDefaultSources()
		for name := range strings.FieldsSeq(users) {
			if name != "root" && name != user && monitor.ValidCronUser(name) {
				sources = append(sources, cronSource{label: locales.CronUserName(name), user: name})
			}
		}
		for file := range strings.FieldsSeq(files) {
			if !strings.HasPrefix(file, "/etc/cron.d/") || strings.Contains(file, ".bak-") {
				continue
			}
			if _, err := monitor.SystemFileReadCommand(file); err == nil {
				sources = append(sources, cronSource{label: file, file: file})
			}
		}
		slices.SortFunc(sources[4:], func(left, right cronSource) int { return strings.Compare(left.label, right.label) })
		a.post(func() {
			if page.dialog != dialog {
				return
			}
			dialog.busy = false
			dialog.failure = strings.TrimSpace(systemError(userErr, err) + "\n" + systemError(fileErr, filesErr))
			query := ""
			dialog.body = func(c *ui.Context, height float32) {
				ui.SearchField(c, &query).FillWidth().Label(locales.CronFilter()).Placeholder(locales.CronFilter())
				ui.Scroll(c).FillWidth().MaxHeight(height / 2).Children(func() {
					ui.Column(c).Gap(3).FillWidth().Children(func() {
						for index, source := range sources {
							if !strings.Contains(strings.ToLower(source.label), strings.ToLower(query)) {
								continue
							}
							if ui.Button(c.Key(index), source.label).AlignSelf(ui.Start).Clicked() {
								page.choices, page.selected, page.dialog = sources, index, nil
								page.rows, page.text = nil, ""
								page.picked.Clear()
								a.refreshServerCron(s)
							}
						}
					})
				})
			}
		})
	}()
}

func (a *App) cronMenu(menu *ui.Menu, s *Session, jobs []monitor.CronJob) {
	page := &s.server.cron
	source := page.choices[page.selected]
	reason := ""
	if !page.usable {
		reason = locales.CronReading()
	} else if source.file != "" || source.timer {
		reason = locales.CronReadOnly()
	} else if len(jobs) == 0 {
		reason = locales.CronPick()
	}
	one := reason
	if one == "" && len(jobs) != 1 {
		one = locales.SvcSelectOne()
	}
	detailsReason := ""
	if len(jobs) != 1 {
		detailsReason = locales.SvcSelectOne()
	}
	if menu.Item(locales.CronDetails()).Disabled(detailsReason != "").Subtitle(detailsReason).Chosen() && detailsReason == "" {
		job := jobs[0]
		zone := page.zone
		dialog := &systemDialog{client: s.client, title: locales.CronDetails(), target: cronTarget(s, source, jobs)}
		dialog.body = func(c *ui.Context, _ float32) {
			ui.Text(c, cronMeaning(job.Schedule))
			ui.Text(c, locales.CronPreview(cronNextText(job.Schedule, job.Disabled, c.Now(), zone))).Selectable()
		}
		page.dialog = dialog
	}
	if menu.Item(locales.CronEdit()).Disabled(one != "").Subtitle(one).Chosen() && one == "" {
		a.cronEditor(s, &jobs[0])
	}
	runReason := one
	if runReason == "" {
		if _, err := monitor.CronManualInput(jobs[0].Command); err != nil || systemRedact(jobs[0].Command) != jobs[0].Command {
			runReason = locales.CronRunUnsupported()
		}
	}
	if menu.Item(locales.CronRun()).Disabled(runReason != "").Subtitle(runReason).Chosen() && runReason == "" {
		a.cronRun(s, jobs[0])
	}
	for _, change := range []struct{ id, label string }{{"enable", locales.SvcEnable()}, {"disable", locales.SvcDisable()}, {"remove", locales.CronRemove()}} {
		disabled := reason
		if disabled == "" && change.id != "remove" && slices.ContainsFunc(jobs, func(job monitor.CronJob) bool { return job.Disabled != (change.id == "enable") }) {
			disabled = locales.SvcWrongState()
		}
		item := menu.Item(change.label).Disabled(disabled != "").Subtitle(disabled)
		if change.id == "remove" {
			item.Danger()
		}
		if item.Chosen() && disabled == "" {
			a.cronChange(s, jobs, change.id, change.label)
		}
	}
}

func cronTarget(s *Session, source cronSource, jobs []monitor.CronJob) string {
	user := source.user
	if user == "" {
		user = systemUser(s)
	}
	if len(jobs) == 1 && jobs[0].User != "" {
		user = jobs[0].User
	}
	var lines []string
	for _, job := range jobs {
		lines = append(lines, fmt.Sprintf("%d: %s  %s", job.Line+1, job.Schedule, systemRedact(job.Command)))
	}
	return strings.TrimSpace(locales.CronUserName(user) + "\n" + strings.Join(lines, "\n"))
}

func (a *App) cronChange(s *Session, jobs []monitor.CronJob, action, label string) {
	page := &s.server.cron
	source := page.choices[page.selected]
	if !page.usable || source.file != "" || source.timer || len(jobs) == 0 {
		return
	}
	before, after := page.text, page.text
	jobs = slices.Clone(jobs)
	slices.SortFunc(jobs, func(left, right monitor.CronJob) int { return right.Line - left.Line })
	note := locales.CronToggleNote()
	for _, job := range jobs {
		if action == "remove" {
			after = monitor.ChangeCron(after, job.Line, "")
			note = locales.CronRemoveNote()
		} else {
			after = monitor.ToggleCron(after, job)
		}
	}
	dialog := &systemDialog{client: s.client, title: label, target: cronTarget(s, source, jobs), consequence: note}
	page.dialog = dialog
	a.cronPrepareWrite(s, dialog, source, before, after, page.exists)
}

func (a *App) cronPrepareWrite(s *Session, dialog *systemDialog, source cronSource, before, after string, exists bool) {
	user := source.user
	if systemRedact(before) != before || systemRedact(after) != after {
		dialog.failure = locales.SvcCannotEdit()
		return
	}
	script, err := monitor.CronWriteCommand(user, before, exists)
	if err != nil {
		dialog.failure = err.Error()
		return
	}
	dialog.confirm = func() {
		run := systemReader(s)
		if user != "" {
			run = a.systemRoot(s)
		}
		a.systemWork(s, dialog, run, script, strings.NewReader(after), a.refreshServerCron)
	}
}

func (a *App) cronRun(s *Session, job monitor.CronJob) {
	page := &s.server.cron
	source := page.choices[page.selected]
	if !page.usable || source.file != "" || source.timer || systemRedact(job.Command) != job.Command {
		return
	}
	user := source.user
	script, err := monitor.CronRunCommand(user)
	input, invalid := monitor.CronManualInput(job.Command)
	if err != nil || invalid != nil {
		return
	}
	dialog := &systemDialog{client: s.client, title: locales.CronRun(), target: cronTarget(s, source, []monitor.CronJob{job}), consequence: locales.CronRunNote()}
	dialog.confirm = func() {
		run := systemReader(s)
		if user != "" {
			run = a.systemRoot(s)
		}
		a.systemWork(s, dialog, run, script, strings.NewReader(input), nil)
	}
	page.dialog = dialog
}

func (a *App) cronEditor(s *Session, job *monitor.CronJob) {
	page := &s.server.cron
	source := page.choices[page.selected]
	if !page.usable || source.file != "" || source.timer {
		return
	}
	before, exists, zone := page.text, page.exists, page.zone
	title := locales.CronAdd()
	schedule, command, comment, logPath := "*/5 * * * *", "", "", ""
	index, disabled, protected := -1, false, false
	if job != nil {
		title, schedule, command, index, disabled = locales.CronEdit(), job.Schedule, job.Command, job.Line, job.Disabled
		protected = systemRedact(command) != command
		if protected {
			command = ""
		}
	}
	dialog := &systemDialog{client: s.client, title: title, target: cronTarget(s, source, nil)}
	page.dialog = dialog
	preset := 0
	fields := [5]string{"*", "*", "*", "*", "*"}
	macro := ""
	setSchedule := func(text string) {
		macro = ""
		if strings.HasPrefix(text, "@") {
			macro = text
			return
		}
		for field, value := range strings.Fields(text) {
			if field < len(fields) {
				fields[field] = value
			}
		}
	}
	setSchedule(schedule)
	var editor func(*ui.Context, float32)
	editor = func(c *ui.Context, height float32) {
		ui.Scroll(c).FillWidth().MaxHeight(height * .56).Children(func() {
			ui.Column(c).FillWidth().Gap(7).Children(func() {
				if systemSelect(c, &preset, locales.CronPreset(), []string{locales.CronCustom(), locales.CronEveryMinutes(5), locales.CronHourly(), locales.CronDaily("03:00"), locales.CronWeekly(), locales.CronMonthly(), locales.CronYearly(), locales.CronReboot()}) && preset > 0 {
					setSchedule([...]string{"", "*/5 * * * *", "0 * * * *", "0 3 * * *", "@weekly", "@monthly", "@yearly", "@reboot"}[preset])
				}
				if macro != "" {
					ui.Text(c, macro).Font("monospace")
					if ui.Button(c, locales.CronFields()).AlignSelf(ui.Start).Clicked() {
						macro = ""
						preset = 0
					}
				} else {
					ui.Row(c).Gap(6).Wrap().Children(func() {
						labels := [...]string{locales.CronMinute(), locales.CronHour(), locales.CronDay(), locales.CronMonth(), locales.CronWeekday()}
						for field, label := range labels {
							ui.Column(c.Key(field)).Width(105).Gap(3).Children(func() {
								ui.Text(c, label).FontSize(11).SingleLine().Tooltip(label)
								if ui.TextInput(c, &fields[field]).Label(label).FillWidth().Changed() {
									preset = 0
								}
							})
						}
					})
				}
				schedule = macro
				if schedule == "" {
					schedule = strings.Join(fields[:], " ")
				}
				_, scheduleErr := monitor.ParseCronSchedule(schedule)
				preview := cronNextText(schedule, false, c.Now(), zone)
				ui.Text(c, locales.CronPreview(preview)).FontSize(12).TextColor(theme.TextMuted)
				if protected {
					ui.Text(c, locales.CronRedacted()).TextColor(theme.Warning)
				}
				ui.TextInput(c, &command).Label(locales.CronCommand()).Placeholder(locales.CronCommand()).FillWidth()
				ui.TextInput(c, &comment).Label(locales.CronComment()).Placeholder(locales.CronComment()).FillWidth()
				ui.TextInput(c, &logPath).Label(locales.CronLogPath()).Placeholder(locales.CronLogPath()).FillWidth()
				redirect := ""
				var pathErr error
				if logPath != "" {
					redirect, pathErr = monitor.CronLogRedirect(logPath)
				}
				valid := scheduleErr == nil && pathErr == nil && strings.TrimSpace(command) != "" && !strings.ContainsAny(command+comment, "\x00\r\n") && utf8.ValidString(command+comment) && systemRedact(command+comment) == command+comment
				if macro == "" && slices.ContainsFunc(fields[:], func(field string) bool { return field == "" || strings.ContainsAny(field, " \t\r\n") }) {
					valid = false
				}
				if logPath != "" && strings.Contains(command, "%") {
					valid = false
				}
				if !valid && (command != "" || pathErr != nil || scheduleErr != nil) {
					ui.Text(c, locales.CronInvalidEdit()).TextColor(theme.Warning).FontSize(12)
				}
				if ui.PrimaryButton(c, locales.Save()).Disabled(!valid).AlignSelf(ui.End).Clicked() {
					line := schedule + " " + command + redirect
					if disabled {
						line = monitor.CronDisabled + line
					}
					if strings.TrimSpace(comment) != "" {
						line = "# " + strings.TrimSpace(comment) + "\n" + line
					}
					after := monitor.ChangeCron(before, index, line)
					if len(after) > monitor.SystemEditLimit {
						dialog.failure = locales.SvcCannotEdit()
						return
					}
					dialog.target = cronTarget(s, source, []monitor.CronJob{{Line: max(0, index), Schedule: schedule, Command: command + redirect}})
					if index < 0 {
						dialog.target = cronTarget(s, source, nil) + "\n" + schedule + "  " + command + redirect
					}
					dialog.body, dialog.consequence = nil, locales.CronWriteNote()
					a.cronPrepareWrite(s, dialog, source, before, after, exists)
					if dialog.confirm != nil {
						dialog.back = func() {
							dialog.body, dialog.consequence, dialog.confirm = editor, "", nil
							dialog.target = cronTarget(s, source, nil)
						}
					} else {
						dialog.body, dialog.consequence = editor, ""
					}
				}
			})
		})
	}
	dialog.body = editor
}
