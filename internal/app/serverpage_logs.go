package app

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const logLimit = 20000
const logByteLimit = 24 << 20

type systemLogLine struct {
	id   int
	text string
}

type serverLogsPage struct {
	poll                                                    sessionPoll
	source                                                  monitor.JournalSource
	common, recent                                          []string
	path, unit, filter, keywords, failure                   string
	sourceMode, priority, since                             int
	paused, regex, exact, wrap, noWrap, running, rootDenied bool
	narrow                                                  bool
	lines, frozen, view                                     []systemLogLine
	serial, version, viewed, bytes                          int
	viewFilter                                              string
	viewRegex, viewExact, viewPaused                        bool
	buffer                                                  *systemChunks
	list                                                    ui.ListState
	picked                                                  ui.Selection[int]
	client                                                  *ssh.Client
	dialog                                                  *systemDialog
}

func (a *App) openServiceJournal(s *Session, unit string) {
	page := &s.server.logs
	page.unit, page.sourceMode, page.priority, page.since = unit, 1, 0, 0
	a.setSystemLog(s, monitor.JournalSource{Unit: unit, Lines: 300})
	a.openServerPage(s, serverSecLogs)
}

func (a *App) setSystemLog(s *Session, source monitor.JournalSource) {
	page := &s.server.logs
	a.stopPoll(&page.poll)
	page.source, page.failure, page.rootDenied = source, "", false
	page.running, page.paused, page.buffer = false, false, nil
	page.clear()
	page.list.ScrollToEnd()
}

func (a *App) refreshServerLogs(s *Session) {
	page := &s.server.logs
	if page.source.Lines != 0 {
		a.setSystemLog(s, page.source)
	}
}

func (page *serverLogsPage) clear() {
	if page.buffer != nil {
		page.buffer.take(true)
	}
	page.lines, page.frozen, page.view, page.bytes = nil, nil, nil, 0
	page.picked.Clear()
	page.version++
}

func (page *serverLogsPage) append(lines []string) {
	if len(lines) == 0 {
		return
	}
	for _, text := range lines {
		page.serial++
		page.bytes += len(text)
		page.lines = append(page.lines, systemLogLine{page.serial, text})
	}
	drop := 0
	for len(page.lines)-drop > logLimit || page.bytes > logByteLimit {
		page.bytes -= len(page.lines[drop].text)
		page.picked.Remove(page.lines[drop].id)
		drop++
	}
	if drop > 0 {
		if drop >= len(page.lines)/2 {
			page.lines = slices.Clone(page.lines[drop:])
		} else {
			clear(page.lines[:drop])
			page.lines = page.lines[drop:]
		}
	}
	page.version++
}

func (page *serverLogsPage) shown() []systemLogLine {
	changed := page.viewFilter != page.filter || page.viewRegex != page.regex || page.viewExact != page.exact || page.viewPaused != page.paused
	if !changed && (page.paused || page.viewed == page.version) {
		return page.view
	}
	page.viewFilter, page.viewRegex, page.viewExact, page.viewPaused, page.viewed = page.filter, page.regex, page.exact, page.paused, page.version
	match, err := logMatcher(page.filter, page.regex, page.exact)
	page.view = nil
	if err != nil {
		return nil
	}
	lines := page.lines
	if page.paused {
		lines = page.frozen
	}
	if page.filter == "" {
		page.view = slices.Clone(lines)
		return page.view
	}
	for _, line := range lines {
		if match(line.text) {
			page.view = append(page.view, line)
		}
	}
	return page.view
}

func (a *App) pollSystemLog(s *Session) {
	page := &s.server.logs
	if s.client == nil && s.server.services.run == nil {
		return
	}
	if page.poll.run == nil || page.poll.run.client != s.client {
		if page.buffer != nil {
			page.paused = false
			page.clear()
			page.list.ScrollToEnd()
		}
		page.buffer = newSystemChunks(logLimit)
		page.failure, page.rootDenied, page.running = "", false, true
	}
	source := page.source
	run, buffer := systemReader(s), page.buffer
	var root systemRunner
	if source.File != "" {
		root = a.systemRoot(s)
	}
	first := true
	a.keepPolling(&page.poll, s, 150*time.Millisecond, func() pollRead {
		return func(ctx context.Context, _ *ssh.Client) func() {
			if first {
				first = false
				go a.followSystemLog(ctx, page, source, run, root, buffer)
			}
			lines := buffer.take(false)
			if len(lines) == 0 {
				return nil
			}
			return func() { page.append(lines) }
		}
	})
}

func (a *App) followSystemLog(ctx context.Context, page *serverLogsPage, source monitor.JournalSource, run, root systemRunner, buffer *systemChunks) {
	if source.File != "" && root != nil {
		_, _, err := run(ctx, monitor.SystemCommand("test", "-r", source.File), nil, nil)
		var status interface{ ExitStatus() int }
		if errors.As(err, &status) && status.ExitStatus() == 1 {
			run = root
		}
	}
	command, err := monitor.JournalCommand(source)
	var stderr string
	if err == nil {
		script := "exec " + command
		if source.File != "" {
			script = monitor.SystemCommand("test", "-f", source.File) + ` || { 'echo' 'Log source is not a regular file' >&2; exit 1; }` + "\n" + script
		}
		_, stderr, err = run(ctx, script, nil, buffer.add)
	}
	if ctx.Err() != nil {
		return
	}
	a.post(func() {
		if page.buffer != buffer || page.poll.run == nil {
			return
		}
		page.append(buffer.take(true))
		page.running = false
		page.failure = systemError(stderr, err)
		if page.failure == "" {
			page.failure = strings.TrimSpace(systemRedact(stderr))
		}
		if page.failure == "" {
			page.failure = locales.LogvEnded()
		}
		page.rootDenied = page.failure == locales.PrivNeedsRoot()
	})
}

func (a *App) serverLogsSection(c *ui.Context, s *Session) {
	box := ui.Column(c.Key("logs-layout")).Grow(1).MinHeight(0).FillWidth()
	width := box.LayoutBounds().W
	box.Children(func() { a.systemLogView(c, s, width > 0 && width < 620) })
}

func (a *App) systemLogView(c *ui.Context, s *Session, narrow bool) {
	page := &s.server.logs
	page.narrow = narrow
	if page.client != s.client {
		page.client = s.client
		page.common = nil
		a.setSystemLog(s, page.source)
	}
	if page.source.Lines == 0 {
		page.source.Lines = 300
	}
	a.pollSystemLog(s)
	a.logSources(c, s)
	a.logTools(c, s)
	if _, err := logMatcher(page.filter, page.regex, page.exact); err != nil {
		ui.Text(c, locales.LogvInvalidRegex()).Padding(1, 8).TextColor(theme.Danger)
	}
	if page.failure != "" {
		message := systemRedact(page.failure)
		ui.Text(c, message).SingleLine().Tooltip(message).Padding(1, 8).TextColor(theme.Warning)
		if page.rootDenied && ui.Button(c, locales.PrivCopyCommands()).AlignSelf(ui.Start).Clicked() {
			page.dialog = &systemDialog{client: s.client, title: locales.PrivNeedsRoot(), rootDenied: true}
		}
	}
	rows := page.shown()
	if len(rows) == 0 && page.failure == "" {
		message := locales.LogvWaiting()
		if len(page.lines) != 0 && !page.paused {
			message = locales.LogvNoMatches()
		} else if page.paused {
			message = locales.LogvPaused()
		}
		if page.running || page.paused {
			ui.Text(c, message).TextColor(theme.TextMuted).Padding(4, 8)
		}
	}
	page.list.Key = func(index int) any { return rows[index].id }
	page.list.Selection, page.list.FollowEnd, page.list.DragSelect = &page.picked, !page.paused, true
	page.list.Label = func(index int) string { return rows[index].text }
	keywords := strings.FieldsFunc(strings.ToLower(page.keywords), func(char rune) bool { return char == ',' || char == '，' })
	for index := range keywords {
		keywords[index] = strings.TrimSpace(keywords[index])
	}
	ui.List(c, &page.list, len(rows), func(index int) {
		text := rows[index].text
		row := ui.Row(c).FillWidth().Padding(1, 8).MinHeight(21)
		row.ContextMenu(func(menu *ui.Menu) { a.logMenu(c, menu, s) })
		row.Children(func() {
			line := ui.Text(c, text).Font("monospace").FontSize(12).Grow(1).MinWidth(0)
			if !(page.wrap || narrow && !page.noWrap) {
				line.SingleLine().Tooltip(text)
			}
			switch systemLogLevel(text, keywords) {
			case 3:
				line.TextColor(theme.Danger)
			case 2:
				line.TextColor(theme.Warning)
			case 1:
				line.TextColor(theme.Accent).Bold()
			}
		})
	}).Grow(1).MinHeight(0).FillWidth().Label(locales.ServerLogs())
	a.systemDialogView(c.Key("log-dialog"), s, &page.dialog)
}

func (a *App) logSources(c *ui.Context, s *Session) {
	page := &s.server.logs
	ui.Row(c.Key("log-sources")).Padding(4, 8, 0, 8).FillWidth().Gap(5).Wrap().AlignItems(ui.Center).Children(func() {
		systemSelect(c, &page.sourceMode, locales.LogvSource(), []string{locales.LogvJournal(), locales.LogvUnit(), locales.LogvFile()})
		if page.sourceMode == 1 {
			ui.TextInput(c, &page.unit).Label(locales.LogvUnit()).Placeholder("example.service").Width(155)
		}
		if page.sourceMode == 2 {
			ui.TextInput(c, &page.path).Label(locales.LogvPath()).Placeholder("/var/log/…").Width(165)
		} else {
			systemSelect(c, &page.priority, locales.LogvPriority(), []string{locales.SvcAll(), locales.LogvErr(), locales.LogvWarn(), locales.LogvInfo()})
			systemSelect(c, &page.since, locales.LogvSince(), []string{locales.LogvRecent(), locales.LogvTenMin(), locales.LogvHour(), locales.LogvToday(), locales.LogvBoot()})
		}
		source := monitor.JournalSource{Lines: 300}
		switch page.sourceMode {
		case 1:
			source.Unit = strings.TrimSpace(page.unit)
		case 2:
			source.File = strings.TrimSpace(page.path)
		}
		if page.sourceMode != 2 {
			source.Priority = [...]string{"", "err", "warning", "info"}[page.priority]
			source.Since = [...]string{"", "-10min", "-1h", "today", "boot"}[page.since]
		}
		_, invalid := monitor.JournalCommand(source)
		valid := invalid == nil && (page.sourceMode != 1 || source.Unit != "") && (page.sourceMode != 2 || source.File != "")
		if ui.PrimaryButton(c, locales.LogvFollow()).Disabled(!valid).Clicked() {
			if source.File != "" {
				a.rememberSystemLog(s, source.File)
			}
			a.setSystemLog(s, source)
		}
		if ui.Button(c, locales.LogvCommon()).Clicked() {
			a.logFilePicker(s)
		}
	})
}

func (a *App) logTools(c *ui.Context, s *Session) {
	page := &s.server.logs
	ui.Row(c.Key("log-tools")).Padding(4, 8).FillWidth().Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
		ui.SearchField(c, &page.filter).Label(locales.LogvFilter()).Placeholder(locales.LogvFilter()).Width(150).MinWidth(90).Shrink(1)
		label := locales.LogvPause()
		if page.paused {
			label = locales.LogvResume()
		}
		if ui.Button(c, label).Clicked() {
			page.paused = !page.paused
			if page.paused {
				page.frozen = slices.Clone(page.lines)
			} else {
				page.frozen = nil
				page.list.ScrollToEnd()
			}
		}
		if ui.Button(c, locales.LogvBottom()).Clicked() {
			page.list.ScrollToEnd()
		}
		ui.MenuButton(c, locales.LogvOptions(), func(menu *ui.Menu) { a.logMenu(c, menu, s) })
	})
}

func (a *App) logMenu(c *ui.Context, menu *ui.Menu, s *Session) {
	page := &s.server.logs
	for _, option := range []struct {
		label string
		value *bool
	}{{locales.LogvRegex(), &page.regex}, {locales.LogvCase(), &page.exact}} {
		if menu.Item(option.label).Checked(*option.value).Chosen() {
			*option.value = !*option.value
		}
	}
	wrapping := page.wrap || page.narrow && !page.noWrap
	if menu.Item(locales.LogvWrap()).Checked(wrapping).Chosen() {
		page.wrap, page.noWrap = !wrapping, wrapping
	}
	if menu.Item(locales.LogvKeywords()).Chosen() {
		page.dialog = &systemDialog{client: s.client, title: locales.LogvKeywords(), body: func(c *ui.Context, _ float32) {
			ui.TextInput(c, &page.keywords).FillWidth().Label(locales.LogvKeywords()).Placeholder(locales.LogvKeywordHint())
		}}
	}
	menu.Separator()
	if menu.Item(locales.LogvCopyPicked()).Disabled(page.picked.Len() == 0).Chosen() {
		c.WriteClipboard(page.logText(true))
	}
	if menu.Item(locales.LogvCopyAll()).Disabled(len(page.shown()) == 0).Chosen() {
		c.WriteClipboard(page.logText(false))
	}
	if menu.Item(locales.LogvSave()).Disabled(len(page.shown()) == 0).Chosen() {
		a.saveSystemLog(s, page.logText(false))
	}
	if menu.Item(locales.LogvClear()).Chosen() {
		page.clear()
		page.viewed = 0
		page.viewPaused = false
	}
}

func (page *serverLogsPage) logText(selected bool) string {
	var output strings.Builder
	for _, line := range page.shown() {
		if !selected || page.picked.Has(line.id) {
			output.WriteString(line.text)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func (a *App) saveSystemLog(s *Session, text string) {
	client := s.client
	go func() {
		file, err := a.plat.SaveFile("", locales.LogvSave(), "server.log", "Log", []string{"log", "txt"})
		if err == nil && file != "" {
			var handle *os.File
			handle, err = os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				_, err = io.WriteString(handle, text)
				closeErr := handle.Close()
				if err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			a.post(func() {
				if systemReady(s, client) {
					s.server.logs.dialog = &systemDialog{client: client, title: locales.LogvSave(), failure: err.Error()}
				}
			})
		}
	}()
}
