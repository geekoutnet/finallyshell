package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type caddyLogSource struct {
	label, script string
}

type caddyLogsPage struct {
	poll                     sessionPoll
	viewer                   serverLogsPage
	requested, paused        bool
	structured               bool
	sources                  []caddyLogSource
	selected                 int
	streamError, sourceError string
}

func (a *App) refreshCaddyLogs(s *Session) { a.caddyRefresh(s) }

func caddyLogSources(install monitor.CaddyInstall, files []string) []caddyLogSource {
	var sources []caddyLogSource
	if install.Method == monitor.CaddyDocker && caddyContainerName.MatchString(install.Container) {
		sources = append(sources, caddyLogSource{locales.CpanelDockerLogs(), "exec " + monitor.SystemCommand("docker", "logs", "--follow", "--tail", "300", install.Container) + " 2>&1"})
	} else if install.ServiceManager == "systemd" && monitor.ValidService(install.Unit) {
		command, err := monitor.JournalCommand(monitor.JournalSource{Unit: install.Unit, Lines: 300})
		if err == nil {
			sources = append(sources, caddyLogSource{locales.CpanelJournal(), "exec " + command})
		}
	}
	seen := map[string]bool{}
	for _, file := range files {
		if seen[file] || !monitor.ValidLogPath(file) {
			continue
		}
		seen[file] = true
		if command, err := monitor.JournalCommand(monitor.JournalSource{File: file, Lines: 300}); err == nil {
			sources = append(sources, caddyLogSource{file, monitor.SystemCommand("test", "-f", file) + " || exit 1\nexec " + command})
		}
	}
	return sources
}

func (a *App) caddyLogsSection(c *ui.Context, s *Session) {
	if !a.caddyDetected(c, s, false) {
		return
	}
	page := &s.caddy.logs
	if !page.requested {
		page.requested, page.structured = true, true
		page.sources = caddyLogSources(s.caddy.state.install, nil)
		caddyRead(a, s, func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) ([]caddyLogSource, error) {
			files, err := backend.files(ctx, run, install)
			if err != nil {
				return nil, err
			}
			configs := make([]monitor.CaddyFile, len(files))
			for index, file := range files {
				configs[index] = file.CaddyFile
			}
			return caddyLogSources(install, backend.logFiles(configs)), nil
		}, func(sources []caddyLogSource, err error) {
			page.sourceError = caddyError(err)
			if err == nil {
				page.sources = sources
			}
		})
	}
	if len(page.sources) == 0 {
		caddyNotice(c, locales.CpanelNoLogSource())
		caddyNotice(c, page.sourceError)
		return
	}
	viewer := &page.viewer
	ui.Row(c).Gap(6).Padding(4, 8).FillWidth().Wrap().AlignItems(ui.Center).Children(func() {
		names := make([]string, len(page.sources))
		for index, source := range page.sources {
			names[index] = source.label
		}
		if systemSelect(c, &page.selected, locales.CpanelLogSource(), names) {
			a.stopPoll(&page.poll)
			viewer.clear()
			page.paused, page.streamError = false, ""
		}
		follow := locales.CpanelPause()
		if page.paused || page.streamError != "" {
			follow = locales.CpanelFollow()
		}
		if ui.Button(c, follow).Clicked() {
			if page.paused || page.streamError != "" {
				page.paused, page.streamError = false, ""
				viewer.clear()
			} else {
				page.paused = true
				a.stopPoll(&page.poll)
			}
		}
		if ui.Button(c, locales.CpanelClear()).Clicked() {
			viewer.clear()
		}
		ui.Checkbox(c, &page.structured, locales.CpanelColumns())
	})
	ui.Row(c).Gap(6).Padding(0, 8).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.SearchField(c, &viewer.filter).Grow(1).MinWidth(0).Placeholder(locales.CpanelFilter()).Label(locales.CpanelFilter())
		ui.Checkbox(c, &viewer.regex, locales.CpanelRegex()).Shrink(0)
	})
	if _, err := logMatcher(viewer.filter, viewer.regex, false); err != nil {
		caddyNotice(c, locales.LogvInvalidRegex())
	}
	caddyNotice(c, page.sourceError)
	caddyNotice(c, page.streamError)
	if !page.paused && page.streamError == "" {
		a.caddyFollowLogs(s, page.sources[page.selected])
	}
	rows := viewer.shown()
	viewer.list.Key = func(index int) any { return rows[index].id }
	viewer.list.Label = func(index int) string { return rows[index].text }
	viewer.list.FollowEnd = !page.paused
	logMenu := func(menu *ui.Menu, index int) {
		if index < 0 || index >= len(rows) {
			return
		}
		caddyMenu(menu,
			caddyAction{locales.CpanelViewLog(), "", func() {
				s.caddy.state.dialog = &systemDialog{client: s.client, title: locales.CpanelViewLog(), body: caddyReadOnlyBody("caddy.log", rows[index].text)}
			}},
			caddyAction{locales.Copy(), "", func() { c.WriteClipboard(rows[index].text) }},
		)
	}
	viewer.list.CellMenu = func(menu *ui.Menu, row, _ int) { logMenu(menu, row) }
	if page.structured {
		caddyLogTable(c, &viewer.list, rows, a.opsNarrow())
	} else {
		ui.List(c, &viewer.list, len(rows), func(index int) {
			text := rows[index].text
			ui.Text(c, text).Font("monospace").TextColor(caddyLogColor(text, 0)).Padding(1, 8).SingleLine().Tooltip(text).FillWidth().ContextMenu(func(menu *ui.Menu) { logMenu(menu, index) })
		}).Grow(1).MinHeight(0).FillWidth().Label(locales.CaddyLogs())
	}
}

func (a *App) caddyFollowLogs(s *Session, source caddyLogSource) {
	page := &s.caddy.logs
	if page.poll.run == nil {
		page.viewer.clear()
		page.viewer.buffer = newSystemChunks(logLimit)
	}
	a.keepPolling(&page.poll, s, 150*time.Millisecond, func() pollRead {
		_, run := a.caddyAccess(s)
		buffer := page.viewer.buffer
		finished := make(chan string, 1)
		first := true
		return func(ctx context.Context, _ *ssh.Client) func() {
			if first {
				first = false
				go func() {
					_, stderr, err := run(ctx, source.script, nil, buffer.add)
					message := systemError(stderr, err)
					if message == "" {
						message = locales.LogvEnded()
					}
					finished <- message
				}()
			}
			select {
			case message := <-finished:
				lines := buffer.take(true)
				return func() {
					page.viewer.append(lines)
					page.streamError = message
					a.stopPoll(&page.poll)
				}
			default:
				lines := buffer.take(false)
				if len(lines) != 0 {
					return func() { page.viewer.append(lines) }
				}
				return nil
			}
		}
	})
}

type caddyAccessLine struct {
	Timestamp, Duration float64
	Status              int
	Size                int64
	Level               string
	Request             struct {
		IP     string `json:"remote_ip"`
		Host   string
		Method string
		URI    string
	}
}

func caddyParseAccess(text string) (caddyAccessLine, bool) {
	var line caddyAccessLine
	_, body, found := strings.Cut(text, "{")
	if !found {
		return line, false
	}
	var raw struct {
		caddyAccessLine
		TS float64 `json:"ts"`
	}
	if json.Unmarshal([]byte("{"+body), &raw) != nil || raw.Request.Method == "" || raw.Status <= 0 {
		return line, false
	}
	line = raw.caddyAccessLine
	line.Timestamp = raw.TS
	return line, true
}

func caddyLogColor(text string, status int) ui.Color {
	if status >= 500 || systemLogLevel(text, nil) == 3 {
		return theme.Danger
	}
	if status >= 400 || systemLogLevel(text, nil) == 2 {
		return theme.Warning
	}
	if status >= 200 && status < 300 {
		return theme.Accent
	}
	return theme.Text
}

func caddyLogTable(c *ui.Context, list *ui.ListState, rows []systemLogLine, narrow bool) {
	columns := []ui.TableColumn{
		{ID: "ts", Title: locales.CpanelLogTime(), Width: 130},
		{ID: "ip", Title: "IP", Width: 130},
		{ID: "host", Title: locales.CpanelLogHost(), Width: 160},
		{ID: "method", Title: locales.CpanelLogMethod(), Width: 86},
		{ID: "uri", Title: "URI", Width: 235},
		{ID: "status", Title: locales.CpanelLogStatus(), Width: 74},
		{ID: "duration", Title: locales.CpanelLogDuration(), Width: 95},
		{ID: "size", Title: locales.CpanelSize(), Width: 90},
	}
	order := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if narrow {
		order = []int{5, 3, 4, 0, 1, 2, 6, 7}
		columns[4].Width, columns[0].Width = 160, 115
		compact := make([]ui.TableColumn, len(order))
		for index, column := range order {
			compact[index] = columns[column]
		}
		columns = compact
	}
	cache := map[int]caddyAccessLine{}
	ui.Table(c, list, columns, len(rows), func(row, col int) {
		text := rows[row].text
		line, ok := cache[row]
		if !ok {
			line, _ = caddyParseAccess(text)
			cache[row] = line
		}
		value, color := "", theme.Text
		if line.Status == 0 {
			if col == 0 {
				value, color = text, caddyLogColor(text, 0)
			}
		} else {
			col = order[col]
			when := ""
			if line.Timestamp > 0 {
				when = time.UnixMilli(int64(line.Timestamp * 1000)).Format("15:04:05.000")
			}
			value = [...]string{when, line.Request.IP, line.Request.Host, line.Request.Method, line.Request.URI, strconv.Itoa(line.Status), fmt.Sprintf("%.1f ms", line.Duration*1000), formatBytes(float64(line.Size))}[col]
			if col == 5 {
				color = caddyLogColor(line.Level, line.Status)
			}
		}
		ui.Text(c, value).SingleLine().Tooltip(value).MinWidth(0).TextColor(color)
	}).Grow(1).MinHeight(0).FillWidth().Label(locales.CaddyLogs())
}
