package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type bulkPaletteAction struct {
	label string
	run   func()
}

func (a *App) showBulkPalette(commands []bulkPaletteAction) {
	const kind WindowKind = "command-palette"
	var query string
	a.openWindow(WindowSpec{Kind: kind, Title: locales.BulkCommandPalette, Width: 480, Height: max(215, 68+35*len(commands))}, func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
			enter := ui.SearchField(c, &query).Label(locales.BulkCommandPalette()).AutoFocus().FillWidth().Submitted()
			search := strings.ToLower(strings.TrimSpace(query))
			for _, command := range commands {
				if !strings.Contains(strings.ToLower(command.label), search) {
					continue
				}
				if ui.Button(c.Key(command.label), command.label).FillWidth().Clicked() || enter {
					a.closeWindow(kind)
					command.run()
					return
				}
			}
		})
	})
}

func (a *App) bulkWindow(c *ui.Context, view *bulkView) {
	ui.Column(c).Fill().Background(colors.overviewPage).Children(func() {
		ui.Row(c).FillWidth().Padding(12, 14).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, locales.BulkTitle()).Bold().FontSize(17).Shrink(0)
			ui.Spacer(c)
			ui.Segmented(c, &view.page, locales.BulkSetup(), locales.BulkResults(), locales.BulkHistory()).Label(locales.BulkTitle())
		})
		if view.err != nil || view.notice != "" {
			text, color := view.notice, theme.TextMuted
			if view.err != nil {
				text, color = view.err.Error(), theme.Danger
			}
			ui.Text(c, text).TextColor(color).Padding(0, 14, 6, 14).FillWidth()
		}
		ui.Column(c.Key(view.page)).FillWidth().Grow(1).MinHeight(0).Padding(0, 14, 12, 14).Children(func() {
			switch view.page {
			case bulkResultsPage:
				a.bulkResults(c, view)
			case bulkHistoryPage:
				a.bulkHistory(c, view)
			default:
				a.bulkSetup(c, view)
			}
		})
	})
	a.bulkDialogs(c, view)
}

func (a *App) bulkSetup(c *ui.Context, view *bulkView) {
	width, height := c.Size()
	compact := height < 530 || width < 720
	ui.Row(c).Grow(1).MinHeight(0).FillWidth().Gap(10).Children(func() {
		ui.Column(c.Key("targets")).Width(min(330, max(160, width*0.30))).FillHeight().MinHeight(0).Children(func() { a.bulkTargetPane(c, view) })
		ui.Column(c.Key("script")).Grow(1).MinWidth(0).FillHeight().MinHeight(0).Children(func() { a.bulkScriptPane(c, view, compact) })
	})
	ui.Row(c).FillWidth().Gap(8).Padding(9, 0, 0, 0).Wrap().AlignItems(ui.Center).Children(func() {
		request, problem := a.bulkDraft(view)
		if view.cancel != nil {
			problem = locales.BulkNotFinished()
		}
		if compact && ui.Button(c, locales.Options()).Clicked() {
			view.optionsOpen = true
		}
		if problem != "" && view.code.Buffer.Len() > 0 {
			ui.Text(c, problem).TextColor(theme.TextMuted).FontSize(12)
		}
		ui.Spacer(c)
		if ui.PrimaryButton(c, locales.BulkRun()).Disabled(problem != "").Tooltip(problem).Clicked() && problem == "" {
			view.confirm, view.confirmRetry = &request, false
		}
	})
}

func (a *App) bulkTargetPane(c *ui.Context, view *bulkView) {
	all := a.newManagerTree(&managerState{sort: ui.SortOrder{Column: "name"}})
	shown := all
	search := strings.TrimSpace(view.search)
	if search != "" {
		shown = a.bulkSearchTree(search)
	}
	byFolder := make(map[string][]string)
	seen := make(map[string]bool)
	var collect func(managerItem) []string
	collect = func(item managerItem) []string {
		if item.isHost() {
			if bulkEligible(a.hosts.Host(item.host)) {
				return []string{item.host}
			}
			return nil
		}
		if seen[item.folder] {
			return nil
		}
		seen[item.folder] = true
		var ids []string
		for _, child := range all.children(item) {
			ids = append(ids, collect(child)...)
		}
		byFolder[item.folder] = ids
		return ids
	}
	root := managerItem{}
	collect(root)
	allLabel, allIDs := locales.SelectAll(), byFolder[""]
	if search != "" {
		allLabel, allIDs = locales.BulkSelectMatches(), nil
		for _, hosts := range shown.hosts {
			for _, host := range hosts {
				if bulkEligible(host) {
					allIDs = append(allIDs, host.ID)
				}
			}
		}
	}
	ui.Column(c).Fill().MinHeight(0).Gap(6).Padding(9).Background(theme.Background).Border(1, colors.line).Radius(6).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.BulkTargets()).Bold()
			ui.Spacer(c)
			ui.Text(c, locales.BulkSelected(len(a.bulkTargets(view)))).TextColor(theme.TextMuted).FontSize(12)
		})
		ui.SearchField(c, &view.search).FillWidth().Placeholder(locales.BulkSearchShort()).Label(locales.BulkSearch())
		ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				if len(shown.roots()) == 0 {
					ui.Text(c, locales.BulkNoMatches()).TextColor(theme.TextMuted)
				}
				bulkTargetChecks(c.Key("all"), view, allIDs, allLabel, "", 0, nil)
				var show func(managerItem, int)
				show = func(item managerItem, depth int) {
					key := c.Key(item)
					if item.isHost() {
						host := a.hosts.Host(item.host)
						if host == nil || host.Deleted() {
							return
						}
						reason := ""
						if host.Kind != "" {
							reason = locales.BulkSSHOnly()
						} else if !execAllowed(host) {
							reason = locales.SysInfoOff()
						}
						bulkTargetChecks(key, view, []string{host.ID}, host.Title(), reason, depth, nil)
						return
					}
					folder := a.hosts.Folder(item.folder)
					if folder == nil {
						return
					}
					open := search != "" || !view.folded[folder.ID]
					bulkTargetChecks(key, view, byFolder[folder.ID], folder.Name, "", depth, func() {
						icon := "▸"
						if open {
							icon = "▾"
						}
						if ui.Button(key, icon).Size(22, 22).Padding(0).Label(locales.BulkToggleFolder(folder.Name)).Disabled(search != "").Clicked() {
							view.folded[folder.ID] = open
						}
					})
					if open {
						for _, child := range shown.children(item) {
							show(child, depth+1)
						}
					}
				}
				for _, item := range shown.roots() {
					show(item, 0)
				}
			})
		})
	})
}

func (a *App) bulkSearchTree(search string) *managerTree {
	tree := a.newManagerTree(&managerState{query: search, sort: ui.SortOrder{Column: "name"}})
	words := strings.Fields(strings.ToLower(search))
	for _, folder := range a.hosts.Folders {
		name := strings.ToLower(folder.Name)
		if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(name, word) }) {
			continue
		}
		for _, ancestor := range a.hosts.FolderPath(folder.ID) {
			tree.matches[ancestor.ID] = true
		}
		var add func(string)
		visited := make(map[string]bool)
		add = func(id string) {
			if visited[id] {
				return
			}
			visited[id] = true
			tree.matches[id] = true
			tree.hosts[id] = sortHosts(a.hosts.HostsIn(id), tree.m.sort)
			for _, child := range a.hosts.Subfolders(id) {
				add(child.ID)
			}
		}
		add(folder.ID)
	}
	return tree
}

func bulkTargetChecks(c *ui.Context, view *bulkView, ids []string, label, reason string, depth int, disclosure func()) {
	check := ui.CheckOff
	selected := 0
	for _, id := range ids {
		if view.selected[id] {
			selected++
		}
	}
	if selected != 0 {
		check = ui.CheckMixed
		if selected == len(ids) {
			check = ui.CheckOn
		}
	}
	ui.Row(c).FillWidth().Gap(4).Padding(0, 0, 0, float32(min(depth, 10))*12).AlignItems(ui.Center).Children(func() {
		if disclosure != nil {
			disclosure()
		} else {
			ui.Box(c).Width(22).Shrink(0)
		}
		if ui.CheckboxState(c, &check, "").Label(label).Disabled(len(ids) == 0 || reason != "").Tooltip(reason).Changed() {
			for _, id := range ids {
				if check == ui.CheckOn {
					view.selected[id] = true
				} else {
					delete(view.selected, id)
				}
			}
		}
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, label).SingleLine().Ellipsis("…").FillWidth().Tooltip(label)
			if reason != "" {
				ui.Text(c, reason).TextColor(theme.TextMuted).FontSize(11).FillWidth()
			}
		})
	})
}

func (a *App) bulkScriptPane(c *ui.Context, view *bulkView, compact bool) {
	commands := a.quick().Commands
	ui.Column(c).Fill().Gap(7).Padding(9).Background(theme.Background).Border(1, colors.line).Radius(6).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.BulkScript()).Bold()
			ui.Spacer(c)
			ui.Button(c, locales.BulkLoadScript()).Disabled(len(commands) == 0).Tooltip(bulkReason(len(commands) == 0, locales.BulkNoScripts())).PopoverMenuButton(func(menu *ui.Menu) {
				for _, command := range commands {
					if menu.Item(command.Title()).ID(command.ID).Chosen() {
						view.code.Buffer.Set(command.Text)
					}
				}
			})
		})
		view.code.Theme, view.code.Label, view.code.Placeholder = codeTheme(), locales.BulkScript(), locales.BulkScriptHint()
		view.code.SoftWrap = compact
		editor.View(c.Key("script-editor"), &view.code).FillWidth().Grow(1).MinHeight(70)
		if compact {
			ui.Checkbox(c, &view.root, locales.BulkRoot())
		} else {
			bulkExecutionOptions(c, view, true)
		}
		ui.Text(c, locales.BulkNoSecrets()).TextColor(theme.TextMuted).FillWidth().FontSize(11)
	})
}

func bulkExecutionOptions(c *ui.Context, view *bulkView, showRoot bool) {
	ui.Column(c).FillWidth().Gap(7).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
			if showRoot {
				ui.Checkbox(c, &view.root, locales.BulkRoot())
			}
			ui.Checkbox(c, &view.stop, locales.BulkStop())
		})
		ui.Row(c).Gap(8).Wrap().FillWidth().Children(func() {
			bulkNumber(c.Key("timeout"), &view.timeout, locales.BulkTimeoutInput())
			bulkNumber(c.Key("concurrency"), &view.concurrency, locales.BulkConcurrency())
		})
		ui.Checkbox(c, &view.keepScript, locales.BulkKeepScript())
	})
}

func bulkNumber(c *ui.Context, value *string, label string) {
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).SingleLine()
		ui.TextInput(c, value).Label(label).Width(63)
	})
}

func bulkReason(disabled bool, reason string) string {
	if disabled {
		return reason
	}
	return ""
}

func bulkStatusText(status bulkStatus) string {
	switch status {
	case bulkWaiting:
		return locales.BulkWaiting()
	case bulkConnecting:
		return locales.BulkConnecting()
	case bulkRunning:
		return locales.BulkRunning()
	case bulkOK:
		return locales.BulkOK()
	case bulkFailed:
		return locales.BulkFailed()
	case bulkTimeout:
		return locales.BulkTimeout()
	case bulkCancelled:
		return locales.BulkCancelled()
	case bulkSkipped:
		return locales.BulkSkipped()
	default:
		return locales.FilterAll()
	}
}

func bulkCounts(rows []bulkResult) map[bulkStatus]int {
	counts := make(map[bulkStatus]int)
	for _, result := range rows {
		counts[result.status]++
	}
	return counts
}

func bulkSummary(counts map[bulkStatus]int) string {
	var parts []string
	for _, status := range []bulkStatus{bulkOK, bulkFailed, bulkTimeout, bulkCancelled, bulkSkipped, bulkRunning, bulkConnecting, bulkWaiting} {
		if count := counts[status]; count > 0 {
			parts = append(parts, strconv.Itoa(count)+" "+strings.ToLower(bulkStatusText(status)))
		}
	}
	return strings.Join(parts, " · ")
}

func bulkPreview(text string, maxLines, maxBytes int) string {
	if len(text) > maxBytes {
		text = text[:maxBytes]
		for !utf8.ValidString(text) && len(text) > 0 && !utf8.RuneStart(text[len(text)-1]) {
			text = text[:len(text)-1]
		}
		text = strings.ToValidUTF8(text, "�") + "…"
	}
	lines := strings.SplitN(text, "\n", maxLines+1)
	if len(lines) > maxLines {
		return strings.Join(lines[:maxLines], "\n") + "…"
	}
	return text
}

func bulkFirstLine(result bulkResult) string {
	for _, text := range []string{result.stdout, result.stderr, bulkErrorText(result.err)} {
		if text != "" {
			line, _, _ := strings.Cut(text, "\n")
			return bulkPreview(strings.TrimRight(line, "\r"), 1, 320)
		}
	}
	return ""
}

func bulkVisible(view *bulkView) []bulkResult {
	var rows []bulkResult
	for _, result := range view.rows {
		if view.filter == "" || view.filter == result.status {
			rows = append(rows, result)
		}
	}
	slices.SortStableFunc(rows, func(first, second bulkResult) int {
		var order int
		switch view.sort.Column {
		case "status":
			order = cmp.Compare(slices.Index(bulkStatuses, first.status), slices.Index(bulkStatuses, second.status))
		case "exit":
			order = cmp.Compare(first.exit, second.exit)
		case "duration":
			order = cmp.Compare(first.duration, second.duration)
		case "output":
			order = strings.Compare(bulkFirstLine(first), bulkFirstLine(second))
		default:
			order = strings.Compare(strings.ToLower(first.target.name), strings.ToLower(second.target.name))
		}
		return sortDir(order, view.sort)
	})
	return rows
}
