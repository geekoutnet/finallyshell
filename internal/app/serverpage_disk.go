package app

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type serverDiskPage struct {
	opsState
	mounts                        []monitor.Disk
	cache                         map[diskScanKey]monitor.DiskScan
	scan                          monitor.DiskScan
	key                           diskScanKey
	pathInput, filter             string
	root, rootTouched             bool
	mode, row, mountRow, progress int
	stop                          context.CancelFunc
	sort                          ui.SortOrder
	selected                      ui.Selection[string]
	table                         ui.ListState
	cleanup                       map[string]int64
	rotated                       []string
}

type diskScanKey struct {
	target      string
	files, root bool
}

func (a *App) refreshServerDisk(s *Session) {
	page := &s.server.disk
	if page.changed(s) {
		*page = serverDiskPage{opsState: page.reset(s), row: -1, mountRow: -1}
	}
	conn, ok := a.opsConnection(s, &page.opsState)
	if !ok || opsBlocked(&page.opsState) != "" {
		return
	}
	page.loaded, page.reading, page.err = true, true, nil
	login := a.passwords[s.Host.ID]
	go func() {
		ctx, cancel := context.WithTimeout(conn.ctx, 15*time.Second)
		defer cancel()
		out, diagnostic, err := conn.normal(ctx, monitor.DiskMountScript, nil)
		free, _, _ := conn.normal(ctx, monitor.DiskFreeRootScript, nil)
		estimates, _, _ := conn.normal(ctx, monitor.DiskCleanupEstimatesScript, nil)
		a.post(func() {
			if !conn.current() {
				return
			}
			page.reading, page.err, page.output, page.mounts = false, opsReadError(err, login), opsSafeOutput(diagnostic, login), monitor.ParseDiskMounts(out)
			page.cleanup, page.rotated = monitor.ParseDiskCleanupEstimates(estimates)
			if !page.rootTouched {
				page.root = strings.TrimSpace(free) == "yes"
			}
			if page.mode != 0 && page.key.target != "" {
				a.scanServerDisk(s, page.key.target, true)
			}
		})
	}()
}

func (a *App) scanServerDisk(s *Session, target string, force bool) {
	page := &s.server.disk
	conn, ok := a.opsConnection(s, &page.opsState)
	if !ok || opsBlocked(&page.opsState) != "" {
		return
	}
	key := diskScanKey{target, page.mode == 2, page.root}
	script, err := monitor.DiskScanScript(key.target, key.files)
	if err != nil {
		page.err = err
		return
	}
	if page.mode == 0 {
		page.mode = 1
	}
	page.key, page.pathInput, page.err, page.output = key, target, nil, ""
	page.selected.Clear()
	page.row = -1
	if cached, found := page.cache[key]; found && !force {
		page.scan = cached
		return
	}
	page.scan, page.reading, page.progress = monitor.DiskScan{}, true, 0
	ctx, cancel := context.WithTimeout(conn.ctx, 90*time.Second)
	page.stop = cancel
	run := conn.normal
	if key.root {
		run = conn.root
	}
	login := a.passwords[s.Host.ID]
	go func() {
		defer cancel()
		count := 0
		var last time.Time
		out, diagnostics, err := run(ctx, script, func(chunk string) {
			count += strings.Count(chunk, "\x00") + strings.Count(chunk, "\n")
			if time.Since(last) > 150*time.Millisecond {
				last = time.Now()
				current := count
				a.post(func() {
					if conn.current() && page.key == key && page.stop != nil {
						page.progress = current
					}
				})
			}
		})
		result := monitor.ParseDiskScan(out, target, key.files)
		err = monitor.DiskScanError(err)
		a.post(func() {
			if !conn.current() {
				return
			}
			page.reading, page.stop, page.err, page.output = false, nil, opsReadError(err, login), opsSafeOutput(diagnostics, login)
			page.scan = result
			if err == nil {
				if len(page.cache) >= 48 {
					clear(page.cache)
				}
				if page.cache == nil {
					page.cache = make(map[diskScanKey]monitor.DiskScan)
				}
				page.cache[key] = result
			}
		})
	}()
}

func (page *serverDiskPage) target() string {
	if page.key.target == "" {
		return "/"
	}
	return page.key.target
}

func (a *App) serverDiskSection(c *ui.Context, s *Session) {
	page := &s.server.disk
	if !page.loaded || page.changed(s) {
		a.refreshServerDisk(s)
	}
	rows := page.rows()
	opsToolbar(c, func() {
		before := page.mode
		ui.Segmented(c, &page.mode, locales.DuMounts(), locales.DuFolders(), locales.DuLargest()).Disabled(page.reading)
		if before != page.mode && page.mode != 0 {
			a.scanServerDisk(s, page.target(), false)
		}
		enter := ui.TextInput(c, &page.pathInput).Label(locales.DuPath()).Placeholder("/var/log").Width(160).Shrink(1).MinWidth(100).Disabled(page.reading).Submitted()
		why := opsBlocked(&page.opsState)
		if !monitor.ValidDiskPath(page.pathInput) && why == "" {
			why = locales.DuInvalid()
		}
		if opsButton(c, locales.DuScan(), why) || enter && why == "" {
			a.scanServerDisk(s, page.pathInput, false)
		}
		if ui.Checkbox(c, &page.root, locales.DuRoot()).Disabled(page.reading).Changed() {
			page.rootTouched = true
			if page.mode != 0 {
				a.scanServerDisk(s, page.target(), false)
			}
		}
		page.note(c)
		if page.stop != nil {
			ui.Text(c, locales.DuProgress(page.progress)).FontSize(12)
			if ui.Button(c, locales.Cancel()).Clicked() {
				page.stop()
			}
		}
	})
	opsToolbar(c, func() {
		if page.mode != 0 {
			why := opsBlocked(&page.opsState)
			if page.target() == "/" && why == "" {
				why = locales.DuAtRoot()
			}
			if opsButton(c, locales.DuParent(), why) {
				a.scanServerDisk(s, path.Dir(page.target()), false)
			}
			ui.Text(c, page.target()).SingleLine().MaxWidth(180).MinWidth(55).Shrink(1).Tooltip(page.target())
			ui.SearchField(c, &page.filter).Label(locales.DuFilter()).Placeholder(locales.DuFilter()).Width(125).MinWidth(70).Shrink(1)
			if selected := page.chosen(rows); len(selected) > 0 {
				if len(selected) == 1 && opsButton(c, locales.DuOpenFiles(), opsBlocked(&page.opsState)) {
					a.diskOpen(s, selected[0])
				}
				ui.Button(c, locales.DuActions()).Disabled(page.reading).PopoverMenuButton(func(menu *ui.Menu) { a.diskEntryMenu(c, menu, s, selected[0], selected) })
			}
		}
		a.diskCleanupMenu(c, s)
	})
	page.readonlyNote(c, s)
	if page.mode == 0 {
		a.diskMountTable(c, s)
	} else {
		a.diskEntryTable(c, s, rows)
	}
	a.opsWorkDialog(c, s, &page.opsState)
}

func (a *App) diskMountTable(c *ui.Context, s *Session) {
	page := &s.server.disk
	state := &page.table
	state.Selected, state.Selection, state.Sort = &page.mountRow, nil, nil
	state.Key = func(index int) any { return index }
	state.Label = func(index int) string { return page.mounts[index].Mount }
	state.CellMenu = func(menu *ui.Menu, index, _ int) {
		mount := page.mounts[index].Mount
		if opsMenuItem(menu, locales.DuScan(), opsBlocked(&page.opsState)) {
			a.scanServerDisk(s, mount, false)
		}
		if menu.Item(locales.CopyPath()).Chosen() {
			c.WriteClipboard(mount)
		}
	}
	columns := []ui.TableColumn{{Title: locales.DuMount(), MinWidth: 90}, {Title: locales.DuUsed(), Width: 99}, {Title: locales.DuAvailable(), Width: 88}, {Title: locales.DuSize(), Width: 75}}
	table := ui.Table(c.Key("mounts"), state, columns, len(page.mounts), func(index, column int) {
		mount := page.mounts[index]
		switch column {
		case 0:
			ui.Text(c, mount.Mount).SingleLine().Tooltip(mount.Mount)
		case 1:
			diskPercent(c, float64(mount.Size-min(mount.Avail, mount.Size))/float64(mount.Size))
		case 2:
			ui.Text(c, formatSize(int64(mount.Avail))).SingleLine()
		case 3:
			ui.Text(c, formatSize(int64(mount.Size))).SingleLine()
		}
	}).Grow(1).MinHeight(0).Label(locales.DuMounts())
	if table.Changed() && page.mountRow >= 0 && page.mountRow < len(page.mounts) {
		page.pathInput = page.mounts[page.mountRow].Mount
	}
	if table.Submitted() && page.mountRow >= 0 && page.mountRow < len(page.mounts) {
		a.scanServerDisk(s, page.mounts[page.mountRow].Mount, false)
	}
}

func diskPercent(c *ui.Context, fraction float64) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(5).Children(func() {
		ui.Progress(c, fraction).Grow(1).MinWidth(16).Height(6)
		ui.Text(c, fmt.Sprintf("%.0f%%", fraction*100)).SingleLine().FontSize(11)
	})
}

func (page *serverDiskPage) rows() []monitor.DiskEntry {
	query := strings.ToLower(strings.TrimSpace(page.filter))
	var rows []monitor.DiskEntry
	for _, entry := range page.scan.Entries {
		if strings.Contains(strings.ToLower(entry.Path), query) {
			rows = append(rows, entry)
		}
	}
	slices.SortStableFunc(rows, func(left, right monitor.DiskEntry) int {
		var order int
		switch page.sort.Column {
		case "name":
			order = strings.Compare(left.Path, right.Path)
		case "mtime":
			order = left.Modified.Compare(right.Modified)
		default:
			order = cmp.Compare(left.Bytes, right.Bytes)
		}
		if page.sort.Column == "" || page.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func (page *serverDiskPage) chosen(rows []monitor.DiskEntry) []monitor.DiskEntry {
	var chosen []monitor.DiskEntry
	for _, entry := range rows {
		if page.selected.Has(entry.Path) {
			chosen = append(chosen, entry)
		}
	}
	return chosen
}

func (a *App) diskEntryTable(c *ui.Context, s *Session, rows []monitor.DiskEntry) {
	page := &s.server.disk
	if len(rows) == 0 && !page.reading {
		opsTextView(c, "", locales.DuNoEntries())
		return
	}
	state := &page.table
	state.Selected, state.Selection, state.Sort = &page.row, &page.selected, &page.sort
	state.Key = func(index int) any { return rows[index].Path }
	state.Label = func(index int) string { return rows[index].Path }
	state.CellMenu = func(menu *ui.Menu, index, _ int) { a.diskEntryMenu(c, menu, s, rows[index], page.chosen(rows)) }
	columns := []ui.TableColumn{{ID: "name", Title: locales.DuName(), MinWidth: 88, Sortable: true}, {ID: "size", Title: locales.DuSize(), Width: 70, Sortable: true}}
	if page.mode == 1 {
		columns = append(columns, ui.TableColumn{ID: "share", Title: locales.DuShare(), Width: 82, Sortable: true})
	}
	columns = append(columns, ui.TableColumn{ID: "mtime", Title: locales.DuModified(), Width: 140, Sortable: true})
	table := ui.Table(c.Key("entries"), state, columns, len(rows), func(index, column int) {
		entry := rows[index]
		switch columns[column].ID {
		case "name":
			name := strings.TrimPrefix(entry.Path, strings.TrimSuffix(page.target(), "/")+"/")
			ui.Text(c, opsTail(name)).SingleLine().Tooltip(opsTail(entry.Path))
		case "size":
			ui.Text(c, formatSize(entry.Bytes)).SingleLine()
		case "share":
			if page.scan.Total > 0 {
				diskPercent(c, min(1, float64(entry.Bytes)/float64(page.scan.Total)))
			}
		case "mtime":
			stamp := "—"
			if !entry.Modified.IsZero() {
				stamp = entry.Modified.Local().Format("2006-01-02 15:04")
			}
			ui.Text(c, stamp).SingleLine().Tooltip(stamp)
		}
	}).Grow(1).MinHeight(0).Label(locales.DuFolders())
	if table.Submitted() && page.row >= 0 && page.row < len(rows) && !page.reading {
		entry := rows[page.row]
		if entry.File {
			a.diskOpen(s, entry)
		} else {
			a.scanServerDisk(s, entry.Path, false)
		}
	}
}

func (a *App) diskOpen(s *Session, entry monitor.DiskEntry) {
	if !monitor.ValidDiskPath(entry.Path) {
		return
	}
	target := entry.Path
	if entry.File {
		target = path.Dir(target)
	}
	a.openFilesAt(s, target)
}

func (a *App) diskEntryMenu(c *ui.Context, menu *ui.Menu, s *Session, entry monitor.DiskEntry, entries []monitor.DiskEntry) {
	page := &s.server.disk
	why := opsBlocked(&page.opsState)
	if len(entries) <= 1 && opsMenuItem(menu, locales.DuOpenFiles(), why) {
		a.diskOpen(s, entry)
	}
	if menu.Item(locales.CopyPath()).Chosen() {
		c.WriteClipboard(entry.Path)
	}
	if len(entries) == 0 {
		entries = []monitor.DiskEntry{entry}
	}
	targets, labels := make([]string, 0, len(entries)), make([]string, 0, len(entries))
	for _, item := range entries {
		targets = append(targets, item.Path)
		labels = append(labels, fmt.Sprintf("%s (%s)", item.Path, formatSize(item.Bytes)))
	}
	script, deleteErr := monitor.DiskDeleteScript(targets)
	deleteWhy := why
	if deleteErr != nil && deleteWhy == "" {
		deleteWhy = locales.DuProtected()
	}
	if opsMenuItem(menu, locales.Delete(), deleteWhy) {
		a.diskConfirm(s, locales.Delete(), locales.DuDeleteWarning()+"\n\n"+strings.Join(labels, "\n"), opsStatic(script))
		page.dialog.unprivileged = !page.root
	}
	truncate, truncateErr := monitor.DiskTruncateScript(entry.Path)
	if (!entry.File || truncateErr != nil || len(entries) != 1) && why == "" {
		why = locales.DuOnlyLog()
	}
	if opsMenuItem(menu, locales.DuTruncate(), why) {
		a.diskConfirm(s, locales.DuTruncate(), locales.DuTruncateWarning()+"\n\n"+labels[0], opsStatic(truncate))
		page.dialog.unprivileged = !page.root
	}
}

func (a *App) diskConfirm(s *Session, action, message string, build func(*ui.Context) (string, string)) {
	page := &s.server.disk
	page.dialog = &opsDialog{title: action, action: action, description: message, build: build, refresh: func() {
		clear(page.cache)
		a.refreshServerDisk(s)
	}}
}

func (a *App) diskCleanupMenu(c *ui.Context, s *Session) {
	page := &s.server.disk
	ui.Button(c, locales.DuCleanup()).Disabled(page.reading).PopoverMenuButton(func(menu *ui.Menu) {
		for _, item := range []struct{ kind, title, message string }{
			{"apt-get", locales.DuAptCache(), locales.DuAptWarning()},
			{"dnf", locales.DuDnfCache(), locales.DuDnfWarning()},
			{"journalctl", locales.DuJournal(), locales.DuJournalWarning()},
			{"rotated", locales.DuRotated(), locales.DuRotatedWarning()},
		} {
			value, available := page.cleanup[item.kind]
			entry := menu.Item(item.title).Disabled(!available)
			var rotatedScript string
			if item.kind == "rotated" && available {
				if script, err := monitor.DiskRotatedScript(page.rotated); err == nil {
					rotatedScript = script
				} else {
					available = false
					entry.Disabled(true)
				}
			}
			if !available {
				reason := locales.DuUnavailable()
				if item.kind == "rotated" && len(page.rotated) != 0 {
					reason = locales.DuProtected()
				}
				entry.Subtitle(reason)
			} else if value >= 0 {
				entry.Trailing(formatSize(value))
			}
			if !entry.Chosen() || !available {
				continue
			}
			valueText := "200"
			message := item.message
			if value >= 0 {
				message += "\n" + locales.DuEstimate() + ": " + formatSize(value)
			}
			if rotatedScript != "" {
				a.diskConfirm(s, item.title, message+"\n\n"+strings.Join(page.rotated, "\n"), opsStatic(rotatedScript))
				continue
			}
			a.diskConfirm(s, item.title, message, func(c *ui.Context) (string, string) {
				if item.kind == "journalctl" {
					ui.TextInput(c, &valueText).Label(locales.DuJournalSize()).Placeholder(locales.DuJournalSize()).FillWidth()
				}
				size, _ := strconv.Atoi(strings.TrimSpace(valueText))
				script, err := monitor.DiskCleanupScript(item.kind, size)
				if err != nil {
					return "", locales.DuJournalSize()
				}
				return script, ""
			})
		}
	})
}
