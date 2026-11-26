package app

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type trashColumn int

const (
	trashName trashColumn = iota
	trashLocation
	trashDate
	trashSize
	trashMode
	trashOwner
	trashNote
)

func trashLabels() []string {
	return []string{locales.DesktopTrashName(), locales.DesktopTrashLocation(), locales.DesktopTrashDate(), locales.DesktopTrashSize(), locales.DesktopTrashMode(), locales.DesktopTrashOwner(), locales.DesktopTrashNote()}
}

func trashNameOf(item monitor.TrashItem) string {
	if item.Original != "" {
		return path.Base(item.Original)
	}
	return item.Name
}

func trashStoreOf(item monitor.TrashItem) string {
	if item.Root {
		if item.Mount != "" {
			return locales.DesktopTrashRoot() + " · " + item.Mount
		}
		return locales.DesktopTrashRoot()
	}
	if item.Mount != "" {
		return item.Mount
	}
	return locales.DesktopTrashHome()
}

func (state *desktopTrashState) visibleItems() []monitor.TrashItem {
	query := strings.ToLower(strings.TrimSpace(state.query))
	var items []monitor.TrashItem
	for _, item := range state.items {
		if query == "" || strings.Contains(strings.ToLower(item.Name+"\n"+item.Original+"\n"+item.Note), query) {
			items = append(items, item)
		}
	}
	value := func(item monitor.TrashItem) string {
		switch state.sortBy {
		case trashLocation:
			return item.Original
		case trashDate:
			return item.Deleted
		case trashMode:
			return item.Mode
		case trashOwner:
			return item.Owner
		case trashNote:
			return item.Note
		default:
			return trashNameOf(item)
		}
	}
	slices.SortStableFunc(items, func(left, right monitor.TrashItem) int {
		order := cmp.Compare(strings.ToLower(value(left)), strings.ToLower(value(right)))
		if state.sortBy == trashSize {
			order = cmp.Compare(left.Size, right.Size)
		}
		if state.ascending {
			return order
		}
		return -order
	})
	return items
}

func (state *desktopTrashState) ask(items []monitor.TrashItem, title string, purge bool) {
	items = slices.Clone(items)
	if !purge {
		items = slices.DeleteFunc(items, func(item monitor.TrashItem) bool { return item.Original == "" })
	}
	if len(items) > 0 {
		state.question = &desktopTrashQuestion{items, title, purge}
	}
}

func (a *App) desktopTrashView(c *ui.Context, w *desktopWindow) {
	state := w.state.(*desktopTrashState)
	if w.session.client != state.client {
		if state.cancel != nil {
			state.cancel()
		}
		w.state = nil
		a.desktopTrashOpened(w, nil)
		state = w.state.(*desktopTrashState)
	}
	if state.runners.user == nil {
		ui.Column(c).Fill().Center().Padding(24).Children(func() {
			ui.Text(c, locales.DesktopTrashUnavailable()).TextColor(theme.TextMuted)
		})
		return
	}
	panel := ui.Column(c).Fill().Padding(12).Gap(8).MinHeight(0).Background(colors.overviewPage)
	width := panel.Bounds().W
	if width <= 0 {
		width = w.w
	}
	compact := width < 775
	panel.Children(func() {
		a.desktopTrashToolbar(c, w, state, compact)
		items := state.visibleItems()
		if len(items) == 0 {
			desktopTrashEmptyView(c, state)
		} else {
			a.desktopTrashTable(c, w, state, items, compact)
		}
		desktopTrashFooter(c, state)
		a.desktopTrashDialogs(c, w, state)
	})
}

func desktopTrashButton(c *ui.Context, icon, label string, compact, disabled bool) bool {
	if compact {
		return widgets.IconButton(c, icon, label).Label(label).Disabled(disabled).Clicked()
	}
	btn := ui.ButtonBase(c).Padding(5, 9).Gap(5).AlignItems(ui.Center).Radius(5).Label(label).Tooltip(label).Disabled(disabled)
	if !disabled && btn.Hovered() {
		btn.Background(theme.Text.Alpha(0.07))
	}
	ink := theme.Text
	if !disabled && (icon == "trash" || icon == "trash-2") {
		ink = colors.problemText
	}
	btn.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(ink)
		ui.Text(c, label).SingleLine().TextColor(ink)
	})
	return btn.Clicked()
}

func (a *App) desktopTrashToolbar(c *ui.Context, w *desktopWindow, state *desktopTrashState, compact bool) {
	var selected []monitor.TrashItem
	restoreSelected, restoreAll := false, false
	for _, item := range state.items {
		restoreAll = restoreAll || item.Original != ""
		if state.selected[trashID(item)] {
			selected = append(selected, item)
			restoreSelected = restoreSelected || item.Original != ""
		}
	}
	busy := state.cancel != nil
	if !compact {
		ui.Text(c, locales.DesktopTrashIntro()).TextColor(theme.TextMuted).SingleLine()
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		if desktopTrashButton(c, "undo-2", locales.DesktopTrashRestore(), compact, busy || !restoreSelected) {
			state.ask(selected, locales.DesktopTrashRestore(), false)
		}
		if desktopTrashButton(c, "rotate-ccw", locales.DesktopTrashRestoreAll(), compact, busy || !restoreAll) {
			state.ask(state.items, locales.DesktopTrashRestoreAll(), false)
		}
		if desktopTrashButton(c, "trash-2", locales.DesktopTrashDelete(), compact, busy || len(selected) == 0) {
			state.ask(selected, locales.DesktopTrashDelete(), true)
		}
		if desktopTrashButton(c, "trash", locales.DesktopTrashEmpty(), compact, busy || len(state.items) == 0) {
			state.ask(state.items, locales.DesktopTrashEmpty(), true)
		}
		ui.Box(c).Grow(1)
		if !state.runners.loginRoot && state.runners.root != nil && !state.rootVisible && desktopTrashButton(c, "shield", locales.DesktopTrashLoadRoot(), true, busy) {
			state.message = ""
			a.desktopTrashRefresh(w, state, true)
		}
		if desktopTrashButton(c, "refresh-cw", locales.Refresh(), true, busy) {
			state.message = ""
			a.desktopTrashRefresh(w, state, false)
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Icon(c, icons.Get("search")).FontSize(15).TextColor(theme.TextMuted)
		field := ui.TextInput(c, &state.query).Placeholder(locales.DesktopTrashSearch()).Label(locales.DesktopTrashSearch()).MinWidth(0).Grow(1)
		if field.Changed() {
			clear(state.selected)
		}
		if compact {
			labels := trashLabels()
			value := labels[state.sortBy]
			ui.Select(c, &value, labels).Label(locales.DesktopTrashSort()).MinWidth(0).Width(132)
			if next := slices.Index(labels, value); next >= 0 && trashColumn(next) != state.sortBy {
				state.sortBy, state.ascending = trashColumn(next), trashColumn(next) != trashDate
			}
			icon := "arrow-down"
			if state.ascending {
				icon = "arrow-up"
			}
			if widgets.IconButton(c, icon, locales.DesktopTrashSort()).Clicked() {
				state.ascending = !state.ascending
			}
		}
	})
}

func desktopTrashEmptyView(c *ui.Context, state *desktopTrashState) {
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Center().Gap(9).Radius(9).Background(colors.card).Border(1, colors.rowLine).Children(func() {
		if !state.loaded {
			if state.cancel != nil {
				ui.Spinner(c).Label(locales.DesktopTrashLoading())
			} else if state.message != "" {
				ui.Text(c, locales.DesktopTrashProblems()).Bold()
			}
			return
		}
		icon, title, hint := "trash-2", locales.DesktopTrashEmptyTitle(), locales.DesktopTrashEmptyHint()
		if len(state.items) > 0 {
			icon, title, hint = "search", locales.DesktopTrashNoResults(), locales.DesktopTrashTrySearch()
		}
		ui.Box(c).Size(44, 44).Radius(12).Center().Background(colors.note).Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(23).TextColor(theme.Accent)
		})
		ui.Text(c, title).FontSize(16).Bold()
		ui.Text(c, hint).FontSize(12).TextColor(theme.TextMuted)
	})
}

func desktopTrashFooter(c *ui.Context, state *desktopTrashState) {
	if state.message != "" {
		ui.Scroll(c).FillWidth().MaxHeight(63).Shrink(0).Radius(6).Background(colors.dangerBanner).Children(func() {
			ui.Text(c, state.message).FillWidth().Padding(7, 9).Selectable().TextColor(colors.problemText)
		})
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
		text := locales.DesktopTrashSummary(len(state.items), formatBytes(float64(monitor.TrashSize(state.items))))
		if count := len(state.selected); count > 0 {
			text += "  ·  " + locales.DesktopTrashSelected(count)
		}
		if state.working != "" {
			text = state.working
		}
		ui.Text(c, text).SingleLine().MinWidth(0).Grow(1).TextColor(theme.TextMuted)
		if state.cancel != nil && state.conflict == nil && ui.Button(c, locales.Cancel()).Clicked() {
			state.cancel()
		}
	})
}

// trashColumnIDs name the columns, for the widths the user drags them to;
// trashColumnWidths are their default widths, 0 sharing the room left.
var (
	trashColumnIDs    = []string{"name", "location", "date", "size", "mode", "owner", "note"}
	trashColumnWidths = []float32{0, 0, 133, 64, 65, 85, 0}
)

func desktopTrashCell(widths widgets.ColumnWidths, cell ui.Element, column trashColumn) ui.Element {
	return widths.Size(cell, trashColumnIDs[column], trashColumnWidths[column])
}

func (a *App) desktopTrashTable(c *ui.Context, w *desktopWindow, state *desktopTrashState, items []monitor.TrashItem, compact bool) {
	if state.widths == nil {
		state.widths = widgets.ColumnWidths{}
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Radius(9).Border(1, colors.rowLine).Clip().Background(colors.card).Children(func() {
		ui.Row(c).Shrink(0).Height(31).PaddingX(9).Gap(8).AlignItems(ui.Center).Background(colors.tableHead).Children(func() {
			count := 0
			for _, item := range items {
				if state.selected[trashID(item)] {
					count++
				}
			}
			all := ui.CheckOff
			if count == len(items) {
				all = ui.CheckOn
			} else if count > 0 {
				all = ui.CheckMixed
			}
			before := all
			ui.Box(c).Width(22).Shrink(0).Children(func() {
				ui.CheckboxState(c, &all, "").Label(locales.DesktopTrashSelectAll()).Tooltip(locales.DesktopTrashSelectAll()).Disabled(state.cancel != nil)
			})
			if all != before {
				clear(state.selected)
				if all == ui.CheckOn {
					for _, item := range items {
						state.selected[trashID(item)] = true
					}
				}
			}
			for index, label := range trashLabels() {
				column := trashColumn(index)
				if compact && column != trashName && column != trashSize {
					continue
				}
				header := widgets.ColumnHead(c, state.widths, trashColumnIDs[column], trashColumnWidths[column], 40, func() {
					ui.Text(c, label).FontSize(11).Bold().SingleLine().MinWidth(0).Ellipsis("…").TextColor(theme.TextMuted)
					if state.sortBy == column {
						icon := "arrow-down"
						if state.ascending {
							icon = "arrow-up"
						}
						ui.Icon(c, icons.Get(icon)).FontSize(11).Shrink(0).TextColor(theme.Accent)
					}
				}).Gap(3).Role(ui.RoleButton).Label(label)
				if header.Clicked() {
					if state.sortBy == column {
						state.ascending = !state.ascending
					} else {
						state.sortBy, state.ascending = column, column != trashDate
					}
				}
			}
		})
		ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).KeepScrollbars(true).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for index, item := range items {
					a.desktopTrashRow(c.Key(trashID(item)), w, state, item, index, compact)
				}
			})
		})
	})
}

func trashText(c *ui.Context, text string) ui.Element {
	if text == "" {
		text = "—"
	}
	return ui.Text(c, text).FontSize(12).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(text)
}

func (a *App) desktopTrashRow(c *ui.Context, w *desktopWindow, state *desktopTrashState, item monitor.TrashItem, index int, compact bool) {
	id := trashID(item)
	checked := state.selected[id]
	name := trashNameOf(item)
	location, date := item.Original, strings.Replace(item.Deleted, "T", " ", 1)
	if location == "" {
		location = locales.DesktopTrashUnknown()
	}
	row := ui.Row(c).Padding(5, 9).Gap(8).FillWidth().MinHeight(41).AlignItems(ui.Center).Role(ui.RoleButton).Label(name)
	if checked {
		row.Background(colors.note)
	} else if index%2 == 1 {
		row.Background(colors.overviewPage)
	}
	if row.Clicked() && state.cancel == nil {
		if checked {
			delete(state.selected, id)
		} else {
			state.selected[id] = true
		}
	}
	row.ContextMenu(func(menu *ui.Menu) {
		busy := state.cancel != nil
		items := []monitor.TrashItem{item}
		if state.selected[id] {
			items = slices.DeleteFunc(slices.Clone(state.items), func(candidate monitor.TrashItem) bool { return !state.selected[trashID(candidate)] })
		}
		canRestore := slices.ContainsFunc(items, func(candidate monitor.TrashItem) bool { return candidate.Original != "" })
		if menu.Item(locales.DesktopTrashRestore()).Disabled(busy || !canRestore).Chosen() {
			state.ask(items, locales.DesktopTrashRestore(), false)
		}
		if menu.Item(locales.DesktopTrashDelete()).Disabled(busy).Danger().Chosen() {
			state.ask(items, locales.DesktopTrashDelete(), true)
		}
		menu.Separator()
		if menu.Item(locales.DesktopTrashCopy()).Disabled(item.Original == "").Chosen() {
			c.WriteClipboard(item.Original)
		}
		if menu.Item(locales.DesktopTrashOpenFolder()).Disabled(item.Original == "").Chosen() {
			a.desktopOpen(w.session, appFiles, path.Dir(item.Original))
		}
	})
	row.Children(func() {
		before := checked
		ui.Box(c).Width(22).Shrink(0).Children(func() {
			ui.Checkbox(c, &checked, "").Label(locales.DesktopTrashSelectItem(name)).Disabled(state.cancel != nil)
		})
		if checked != before {
			if checked {
				state.selected[id] = true
			} else {
				delete(state.selected, id)
			}
		}
		desktopTrashCell(state.widths, ui.Row(c).Gap(7).AlignItems(ui.Center), trashName).Children(func() {
			a.fileIcon(c, name, item.Directory, false, false)
			ui.Column(c).MinWidth(0).Grow(1).Children(func() {
				trashText(c, name).Bold()
				identity := item
				identity.Root = identity.Root || state.runners.loginRoot
				store := trashStoreOf(identity)
				if compact {
					trashText(c, location).TextColor(theme.TextMuted)
					store = strings.Join(slices.DeleteFunc([]string{store, date, item.Mode, item.Owner, strings.ReplaceAll(item.Note, "\n", " ")}, func(value string) bool { return value == "" }), " · ")
				}
				trashText(c, store).FontSize(10).TextColor(theme.TextMuted)
			})
		})
		folder := location
		if item.Original != "" {
			folder = path.Dir(item.Original)
		}
		values := []string{name, folder, date, formatBytes(float64(item.Size)), item.Mode, item.Owner, strings.ReplaceAll(item.Note, "\n", " ")}
		for index := 1; index < len(values); index++ {
			column := trashColumn(index)
			if !compact || column == trashSize {
				cell := desktopTrashCell(state.widths, trashText(c, values[index]), column)
				if column == trashLocation {
					cell.Tooltip(location)
				}
			}
		}
	})
}

func (a *App) desktopTrashDialogs(c *ui.Context, w *desktopWindow, state *desktopTrashState) {
	if question := state.question; question != nil {
		open := true
		ui.Modal(c, &open, func() {
			width, _ := c.Size()
			ui.Column(c).Width(min(428, max(250, width-48))).Gap(11).Children(func() {
				ui.Text(c, question.title).FontSize(17).Bold()
				size := formatBytes(float64(monitor.TrashSize(question.items)))
				message := locales.DesktopTrashRestoreQuestion(len(question.items), size)
				if question.purge {
					message = locales.DesktopTrashPurgeQuestion(len(question.items), size)
				}
				ui.Text(c, message).FillWidth()
				ui.Scroll(c).FillWidth().MaxHeight(130).Children(func() {
					ui.Column(c).FillWidth().Padding(9).Gap(3).Radius(7).Background(colors.note).Children(func() {
						for _, item := range question.items {
							label := item.Original
							if label == "" {
								label = item.Name
							}
							trashText(c, label+"  ·  "+trashStoreOf(item)).FillWidth()
						}
					})
				})
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						open = false
					}
					if ui.PrimaryButton(c, locales.SvcConfirm()).Clicked() {
						open = false
						a.desktopTrashApply(w, state, question)
					}
				})
			})
		})
		if !open {
			state.question = nil
			if state.dirty && state.cancel == nil {
				a.desktopTrashRefresh(w, state, false)
			}
		}
	}
	if conflict := state.conflict; conflict != nil {
		open := true
		choice := monitor.TrashRestoreMode("")
		ui.Modal(c, &open, func() {
			width, _ := c.Size()
			ui.Column(c).Width(min(428, max(250, width-48))).Gap(11).Children(func() {
				ui.Text(c, locales.DesktopTrashConflict()).FontSize(17).Bold()
				trashText(c, conflict.item.Original).FillWidth().Padding(9).Radius(7).Background(colors.note)
				ui.Text(c, locales.DesktopTrashConflictHint()).FillWidth()
				ui.Row(c).Gap(8).Justify(ui.End).Wrap().Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						state.cancel()
						open = false
					}
					if ui.Button(c, locales.DesktopTrashSkip()).Clicked() {
						open = false
					}
					if ui.Button(c, locales.DesktopTrashReplace()).Clicked() {
						choice, open = monitor.TrashReplace, false
					}
					if ui.PrimaryButton(c, locales.DesktopTrashKeepBoth()).Clicked() {
						choice, open = monitor.TrashKeepBoth, false
					}
				})
			})
		})
		if !open {
			state.conflict = nil
			conflict.answer <- choice
		}
	}
}
