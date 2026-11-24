package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type serverCronPage struct {
	client                                       *ssh.Client
	selected, sortRow                            int
	started, loading, usable, exists, rootDenied bool
	choices                                      []cronSource
	rows                                         []monitor.CronJob
	text, filter, failure                        string
	zone                                         *time.Location
	table                                        ui.ListState
	picked                                       ui.Selection[int]
	sort                                         ui.SortOrder
	readID                                       int
	cancel                                       context.CancelFunc
	dialog                                       *systemDialog
}

type cronSource struct {
	label, user, file string
	timer             bool
}

func cronDefaultSources() []cronSource {
	return []cronSource{{label: locales.CronMine()}, {label: locales.CronRoot(), user: "root"}, {label: "/etc/crontab", file: "/etc/crontab"}, {label: locales.CronTimers(), timer: true}}
}

func (a *App) refreshServerCron(s *Session) {
	page := &s.server.cron
	if len(page.choices) == 0 || (s.client == nil && s.server.services.run == nil) {
		return
	}
	page.started, page.loading, page.usable, page.rootDenied, page.failure = true, true, false, false, ""
	page.readID++
	request := page.readID
	if page.cancel != nil {
		page.cancel()
	}
	ctx, cancel := context.WithTimeout(systemContext(s), time.Minute)
	page.cancel = cancel
	source, client := page.choices[page.selected], s.client
	run := systemReader(s)
	read := run
	if source.user != "" {
		read = a.systemRoot(s)
	}
	var root systemRunner
	if source.file != "" {
		root = a.systemRoot(s)
	}
	go func() {
		defer cancel()
		zoneText, _, _ := run(ctx, monitor.SystemCommand("date", "+%z"), nil, nil)
		zone, _ := monitor.CronZone(zoneText)
		var command string
		switch {
		case source.timer:
			command = monitor.CronTimersCommand()
		case source.file != "":
			command, _ = monitor.SystemFileReadCommand(source.file)
		default:
			command, _ = monitor.CronReadTransport(source.user)
		}
		text, stderr, err := read(ctx, command, nil, nil)
		if source.file != "" && err != nil {
			text, stderr, err = root(ctx, command, nil, nil)
		}
		exists := err == nil
		if source.file != "" {
			state, raw, _ := strings.Cut(text, "\n")
			text, exists = raw, strings.TrimSpace(state) == "present"
		} else if !source.timer && err != nil && (monitor.CronMissing(stderr) || monitor.CronMissing(text)) {
			text, err = "", nil
		} else if err != nil && stderr == "" {
			stderr, text = text, ""
		}
		valid := len(text) <= monitor.SystemEditLimit && !strings.ContainsRune(text, 0)
		var rows []monitor.CronJob
		if !source.timer && valid {
			rows = monitor.CronJobs(text, source.file != "")
		}
		a.post(func() {
			if page.readID != request || !systemReady(s, client) {
				return
			}
			page.loading, page.exists, page.zone = false, exists, zone
			if page.text != text {
				page.picked.Clear()
				page.sortRow = -1
			}
			page.usable, page.rows, page.text = err == nil && valid, rows, text
			page.failure = systemError(stderr, err)
			page.rootDenied = page.failure == locales.PrivNeedsRoot()
			if !valid {
				page.failure = locales.SvcCannotEdit()
			}
		})
	}()
}

func (a *App) serverCronSection(c *ui.Context, s *Session) {
	box := ui.Column(c.Key("cron-layout")).Grow(1).MinHeight(0).FillWidth()
	width := box.LayoutBounds().W
	box.Children(func() { a.cronView(c, s, width > 0 && width < 1020) })
}

func (a *App) cronView(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.cron
	if page.client != s.client || len(page.choices) == 0 {
		if page.cancel != nil {
			page.cancel()
		}
		*page = serverCronPage{client: s.client, choices: cronDefaultSources(), sortRow: -1}
	}
	if !page.started {
		a.refreshServerCron(s)
	}
	ui.Row(c).Padding(4, 8).Gap(6).Wrap().AlignItems(ui.Center).FillWidth().Children(func() {
		choices := make([]string, len(page.choices))
		copy(page.choices, cronDefaultSources())
		for index, source := range page.choices {
			if index >= 4 && source.user != "" {
				source.label = locales.CronUserName(source.user)
			}
			choices[index] = source.label
		}
		if systemSelect(c, &page.selected, locales.CronSource(), choices) {
			page.picked.Clear()
			page.rows, page.text = nil, ""
			a.refreshServerCron(s)
		}
		if ui.Button(c, locales.CronBrowse()).Clicked() {
			a.cronBrowse(s)
		}
		ui.SearchField(c, &page.filter).Label(locales.CronFilter()).Placeholder(locales.CronFilter()).Width(150).MinWidth(85).Shrink(1)
		source := page.choices[page.selected]
		if source.file != "" {
			if ui.Button(c, locales.CronEditFile()).Disabled(page.loading).Clicked() {
				a.systemFileEditor(s, &page.dialog, source.file, locales.CronEditFile(), locales.CronEditFileNote(), a.refreshServerCron)
			}
		} else if !source.timer {
			if ui.PrimaryButton(c, locales.CronAdd()).Disabled(!page.usable).Clicked() {
				a.cronEditor(s, nil)
			}
		}
		if !source.timer {
			ui.MenuButton(c, locales.SvcActions(), func(menu *ui.Menu) { a.cronMenu(menu, s, page.cronPicked(page.visible())) })
		}
	})
	if page.loading {
		ui.Text(c, locales.CronReading()).TextColor(theme.TextMuted).Padding(4, 8)
	}
	if page.failure != "" {
		message := systemRedact(page.failure)
		ui.Text(c, message).TextColor(theme.Warning).Padding(4, 8).SingleLine().Tooltip(message)
		if page.rootDenied && ui.Button(c, locales.PrivCopyCommands()).AlignSelf(ui.Start).Clicked() {
			page.dialog = &systemDialog{client: s.client, title: locales.PrivNeedsRoot(), rootDenied: true}
		}
	}
	if page.choices[page.selected].timer {
		var filtered []string
		for line := range strings.SplitSeq(systemRedact(page.text), "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(page.filter)) {
				filtered = append(filtered, line)
			}
		}
		text := strings.TrimSpace(strings.Join(filtered, "\n"))
		ui.TextArea(c, &text).ReadOnly(true).Label(locales.CronTimers()).Font("monospace").Grow(1).MinHeight(0).FillWidth()
	} else {
		a.cronTable(c, s, narrow)
	}
	a.systemDialogView(c.Key("cron-dialog"), s, &page.dialog)
}

func (page *serverCronPage) visible() []monitor.CronJob {
	query := strings.ToLower(strings.TrimSpace(page.filter))
	var rows []monitor.CronJob
	for _, job := range page.rows {
		if strings.Contains(strings.ToLower(job.Schedule+" "+job.User+" "+systemRedact(job.Command)), query) {
			rows = append(rows, job)
		}
	}
	slices.SortStableFunc(rows, func(left, right monitor.CronJob) int {
		order := 0
		switch page.sort.Column {
		case "user":
			order = cmp.Compare(left.User, right.User)
		case "schedule":
			order = cmp.Compare(left.Schedule, right.Schedule)
		case "command":
			order = cmp.Compare(left.Command, right.Command)
		case "enabled":
			order = cmp.Compare(cronEnabled(left), cronEnabled(right))
		}
		if order == 0 {
			order = cmp.Compare(left.Line, right.Line)
		}
		if page.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func cronEnabled(job monitor.CronJob) string {
	if job.Disabled {
		return locales.CronDisabled()
	}
	return locales.SvcEnabled()
}

func (page *serverCronPage) cronPicked(rows []monitor.CronJob) []monitor.CronJob {
	var picked []monitor.CronJob
	for _, row := range rows {
		if page.picked.Has(row.Line) {
			picked = append(picked, row)
		}
	}
	return picked
}

func (a *App) cronTable(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.cron
	rows := page.visible()
	if len(rows) == 0 && page.usable {
		ui.Text(c, locales.CronEmpty()).TextColor(theme.TextMuted).Padding(4, 8)
	}
	page.table.Key = func(index int) any { return rows[index].Line }
	page.table.Label = func(index int) string { return systemRedact(rows[index].Command) }
	page.table.Selection, page.table.Sort, page.table.Selected = &page.picked, &page.sort, &page.sortRow
	page.table.CellMenu = func(menu *ui.Menu, index, _ int) {
		if !page.picked.Has(rows[index].Line) {
			page.picked.Clear()
			page.picked.Add(rows[index].Line)
		}
		a.cronMenu(menu, s, page.cronPicked(rows))
	}
	columns := []ui.TableColumn{
		{ID: "schedule", Title: locales.CronSchedule(), Width: 143, Sortable: true},
		{ID: "command", Title: locales.CronCommand(), MinWidth: 175, Sortable: true},
		{ID: "enabled", Title: locales.SvcState(), Width: 100, Sortable: true},
		{ID: "meaning", Title: locales.CronMeaning(), Width: 140},
		{ID: "next", Title: locales.CronNext(), Width: 345},
		{ID: "user", Title: locales.CronUser(), Width: 105, Sortable: true},
	}
	if narrow {
		columns = columns[:3]
		columns[0].Width, columns[1].MinWidth, columns[2].Width = 104, 90, 80
	}
	source := page.choices[page.selected]
	user := source.user
	if user == "" {
		user = systemUser(s)
	}
	ui.Table(c.Key(narrow), &page.table, columns, len(rows), func(index, column int) {
		job := rows[index]
		values := []string{job.Schedule, systemRedact(job.Command), cronEnabled(job)}
		var text string
		switch column {
		case 3:
			text = cronMeaning(job.Schedule)
		case 4:
			text = strings.ReplaceAll(cronNextText(job.Schedule, job.Disabled, c.Now(), page.zone), " · ", "\n")
		case 5:
			text = user
			if job.User != "" {
				text = job.User
			}
		default:
			text = values[column]
		}
		tip := text
		if narrow && column == 0 {
			owner := user
			if job.User != "" {
				owner = job.User
			}
			tip += "\n" + cronMeaning(job.Schedule) + "\n" + locales.CronPreview(cronNextText(job.Schedule, job.Disabled, c.Now(), page.zone)) + "\n" + locales.CronUserName(owner)
		}
		cell := ui.Text(c, text).Tooltip(tip).MinWidth(0).Shrink(1)
		if column == 4 {
			cell.FontSize(12)
		} else {
			cell.SingleLine()
		}
	}).Grow(1).MinHeight(0).Label(locales.ServerCron())
}

func cronMeaning(schedule string) string {
	switch schedule {
	case "@reboot":
		return locales.CronReboot()
	case "@yearly", "@annually":
		return locales.CronYearly()
	case "@monthly":
		return locales.CronMonthly()
	case "@weekly":
		return locales.CronWeekly()
	case "@hourly":
		return locales.CronHourly()
	case "@daily", "@midnight":
		return locales.CronDaily("00:00")
	}
	var minute, hour int
	if count, _ := fmt.Sscanf(schedule, "*/%d * * * *", &minute); count == 1 && schedule == fmt.Sprintf("*/%d * * * *", minute) {
		return locales.CronEveryMinutes(minute)
	}
	if schedule == "* * * * *" {
		return locales.CronEveryMinutes(1)
	}
	if count, _ := fmt.Sscanf(schedule, "%d %d * * *", &minute, &hour); count == 2 && len(strings.Fields(schedule)) == 5 && strings.HasSuffix(schedule, " * * *") {
		return locales.CronDaily(fmt.Sprintf("%02d:%02d", hour, minute))
	}
	return locales.CronCustom()
}

func cronNextText(schedule string, disabled bool, now time.Time, zone *time.Location) string {
	if disabled {
		return "—"
	}
	rule, err := monitor.ParseCronSchedule(schedule)
	if err != nil {
		return locales.CronInvalid()
	}
	if rule.Reboot {
		return locales.CronReboot()
	}
	if zone == nil {
		return locales.CronNoZone()
	}
	var stamps []string
	for _, next := range rule.Next(now.In(zone), 3) {
		stamps = append(stamps, next.Format("2006-01-02 15:04"))
	}
	if len(stamps) == 0 {
		return locales.CronNoRuns()
	}
	return strings.Join(stamps, " · ") + " (" + zone.String() + ")"
}
