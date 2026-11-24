package app

import (
	"errors"
	"slices"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *App) bulkResults(c *ui.Context, view *bulkView) {
	if len(view.rows) == 0 {
		ui.Column(c).Fill().Center().Children(func() { ui.Text(c, locales.BulkNoResults()).TextColor(theme.TextMuted) })
		return
	}
	ui.Column(c).Fill().Gap(7).Children(func() {
		ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, bulkSummary(bulkCounts(view.rows))).Bold()
			ui.Spacer(c)
			ui.Button(c, locales.BulkStatus()+": "+bulkStatusText(view.filter)).PopoverMenuButton(func(menu *ui.Menu) {
				for _, status := range append([]bulkStatus{""}, bulkStatuses...) {
					if menu.Item(bulkStatusText(status)).Radio(view.filter == status).Chosen() {
						view.filter = status
					}
				}
			})
		})
		a.bulkResultsTools(c, view)
		if view.cancel != nil {
			ui.Text(c, locales.BulkRunningNote()).FontSize(11).TextColor(theme.TextMuted).FillWidth()
		}
		rows := bulkVisible(view)
		if len(rows) == 0 {
			ui.Text(c, locales.BulkNoResultMatches()).TextColor(theme.TextMuted).Grow(1)
		} else {
			a.bulkResultTable(c, view, rows)
		}
		if index := slices.IndexFunc(view.rows, func(result bulkResult) bool { return result.target.id == view.current }); index >= 0 {
			result := view.rows[index]
			ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(colors.line).Padding(5, 0, 0, 0).Children(func() {
				ui.Text(c, bulkPreview(result.target.name+" · "+orDash(bulkFirstLine(result)), 1, 320)).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				if ui.Button(c, locales.BulkViewOutput()).Disabled(!result.status.final()).Tooltip(bulkReason(!result.status.final(), locales.BulkNotFinished())).Clicked() {
					view.outputRow = result.target.id
				}
			})
		}
	})
}

func (a *App) bulkResultsTools(c *ui.Context, view *bulkView) {
	busy := view.cancel != nil
	failed, selected := bulkRetry(view, true), bulkRetry(view, false)
	ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(6).Children(func() {
		if busy {
			ui.Spinner(c).Size(16, 16).Label(locales.BulkWorking())
			if ui.Button(c, locales.BulkCancelAll()).Clicked() {
				view.cancel()
			}
		}
		if ui.Button(c, locales.BulkRetryFailed()).Disabled(busy || len(failed.targets) == 0).Tooltip(bulkReason(busy, locales.BulkNotFinished()) + bulkReason(!busy && len(failed.targets) == 0, locales.BulkNoFailed())).Clicked() {
			view.confirm, view.confirmRetry = &failed, true
		}
		if ui.Button(c, locales.BulkRetrySelected()).Disabled(busy || len(selected.targets) == 0).Tooltip(bulkReason(busy, locales.BulkNotFinished()) + bulkReason(!busy && len(selected.targets) == 0, locales.BulkNoTargets())).Clicked() {
			view.confirm, view.confirmRetry = &selected, true
		}
		if ui.Button(c, locales.BulkCopyAll()).Disabled(busy).Tooltip(bulkReason(busy, locales.BulkNotFinished())).Clicked() {
			c.WriteClipboard(bulkReport(view.rows))
		}
		ui.Button(c, locales.BulkExport()).Disabled(busy || view.exporting).Tooltip(bulkReason(busy, locales.BulkNotFinished())).PopoverMenuButton(func(menu *ui.Menu) {
			if menu.Item(locales.BulkExportCSV()).Chosen() {
				a.bulkPickExport(view, true)
			}
			if menu.Item(locales.BulkExportText()).Chosen() {
				a.bulkPickExport(view, false)
			}
		})
	})
}

func (a *App) bulkResultTable(c *ui.Context, view *bulkView, rows []bulkResult) {
	width, _ := c.Size()
	exitTitle, exitWidth := locales.BulkExit(), float32(85)
	if width < 600 {
		exitTitle, exitWidth = locales.BulkExitShort(), 50
	}
	columns := []ui.TableColumn{
		{ID: "selected", Width: 32, Fixed: true},
		{ID: "host", Title: locales.BulkHost(), Width: min(215, width*0.22), Sortable: true},
		{ID: "status", Title: locales.BulkStatus(), Width: 90, Sortable: true},
		{ID: "exit", Title: exitTitle, Width: exitWidth, Sortable: true},
		{ID: "duration", Title: locales.BulkDuration(), Width: 84, Sortable: true},
		{ID: "output", Title: locales.BulkOutput(), MinWidth: 62, Sortable: true},
	}
	row := slices.IndexFunc(rows, func(result bulkResult) bool { return result.target.id == view.current })
	previous := row
	view.table.Selected, view.table.Sort, view.table.KeepOffset = &row, &view.sort, true
	view.table.Key = func(index int) any { return rows[index].target.id }
	view.table.Label = func(index int) string { return rows[index].target.name + " · " + bulkStatusText(rows[index].status) }
	view.table.CellMenu = func(menu *ui.Menu, index, _ int) {
		if index >= 0 && index < len(rows) {
			view.current = rows[index].target.id
			a.bulkRowMenu(c, view, menu, rows[index])
		}
	}
	table := ui.Table(c, &view.table, columns, len(rows), func(index, column int) {
		result := rows[index]
		switch column {
		case 0:
			checked := view.checked[result.target.id]
			if ui.Checkbox(c, &checked, "").Label(locales.ManagerSelectRow(result.target.name)).Changed() {
				view.checked[result.target.id] = checked
				view.current = result.target.id
			}
		case 1:
			bulkCell(c, result.target.name).Tooltip(result.target.endpoint)
		case 2:
			color := theme.TextMuted
			switch result.status {
			case bulkOK:
				color = theme.Success
			case bulkFailed:
				color = theme.Danger
			case bulkTimeout:
				color = theme.Warning
			case bulkRunning, bulkConnecting:
				color = theme.Accent
			}
			bulkCell(c, bulkStatusText(result.status)).TextColor(color).Bold()
		case 3:
			bulkCell(c, orDash(bulkExit(result)))
		case 4:
			bulkCell(c, orDash(bulkDuration(result)))
		case 5:
			bulkCell(c, bulkFirstLine(result)).Font("monospace").FontSize(12)
		}
	}).Grow(1).MinHeight(0).FillWidth().Background(theme.Background).Border(1, colors.line).Radius(6).Label(locales.BulkResults())
	if row >= 0 && row < len(rows) {
		if row != previous {
			view.current = rows[row].target.id
		}
		if table.Submitted() && rows[row].status.final() {
			view.outputRow = rows[row].target.id
		}
	}
}

func bulkCell(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).SingleLine().Ellipsis("…").MinWidth(0).FillWidth()
}

func (a *App) bulkRowMenu(c *ui.Context, view *bulkView, menu *ui.Menu, result bulkResult) {
	final := result.status.final()
	busy := view.cancel != nil
	if menu.Item(locales.BulkViewOutput()).Disabled(!final).Subtitle(bulkReason(!final, locales.BulkNotFinished())).Chosen() && final {
		view.outputRow = result.target.id
	}
	output := bulkOutput(result)
	if menu.Item(locales.BulkCopyOutput()).Disabled(output == "").Subtitle(bulkReason(output == "", locales.BulkNoOutput())).Chosen() && output != "" {
		c.WriteClipboard(output)
	}
	if menu.Item(locales.Retry()).Disabled(busy || !final).Subtitle(bulkReason(busy || !final, locales.BulkNotFinished())).Chosen() && !busy && final {
		request := view.last
		request.targets = []bulkTarget{result.target}
		view.confirm, view.confirmRetry = &request, true
	}
}

func (a *App) bulkHistory(c *ui.Context, view *bulkView) {
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			if view.historyErr != nil {
				ui.Text(c, locales.BulkHistoryError(view.historyErr.Error())).TextColor(theme.Danger).FillWidth()
			}
			if len(view.history) == 0 {
				ui.Text(c, locales.BulkNoHistory()).TextColor(theme.TextMuted).Padding(8)
			}
			for index, entry := range view.history {
				ui.Row(c.Key(index)).FillWidth().Padding(9).Gap(8).Wrap().AlignItems(ui.Center).Background(theme.Background).Border(1, colors.line).Radius(6).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, entry.Time.Local().Format("2006-01-02 15:04:05")+" · "+locales.BulkHistoryTargets(len(entry.IDs))).Bold()
						line := locales.BulkScriptNotSaved()
						if entry.Script != "" {
							line = bulkPreview(entry.Script, 1, 100)
						}
						ui.Text(c, line).FontSize(12).SingleLine().Ellipsis("…").FillWidth()
						ui.Text(c, bulkSummary(entry.Summary)).FontSize(12).TextColor(theme.TextMuted).FillWidth()
					})
					if ui.Button(c, locales.BulkRestore()).Disabled(view.cancel != nil).Tooltip(bulkReason(view.cancel != nil, locales.BulkNotFinished())).Clicked() {
						a.bulkRestore(view, entry)
					}
				})
			}
		})
	})
}

func (a *App) bulkDialogs(c *ui.Context, view *bulkView) {
	width, height := c.Size()
	if view.optionsOpen {
		ui.Modal(c.Key("bulk-options"), &view.optionsOpen, func() {
			ui.Column(c).Width(min(460, width-44)).Gap(8).Children(func() {
				ui.Text(c, locales.Options()).FontSize(16).Bold()
				bulkExecutionOptions(c, view, false)
				if ui.PrimaryButton(c, locales.Close()).AlignSelf(ui.End).AutoFocus().Clicked() {
					view.optionsOpen = false
				}
			})
		})
		return
	}
	if request := view.confirm; request != nil {
		open := true
		ui.Modal(c.Key("bulk-confirm"), &open, func() {
			ui.Column(c).Width(min(600, width-44)).Gap(8).Children(func() {
				ui.Text(c, locales.BulkConfirm(len(request.targets))).Bold().FontSize(17)
				ui.Text(c, locales.BulkConfirmNote()).FillWidth()
				if request.root {
					ui.Text(c, locales.BulkConfirmRoot()).TextColor(theme.Danger).Bold().FillWidth()
				}
				ui.Scroll(c).FillWidth().MaxHeight(max(42, min(120, height*0.16))).Children(func() {
					ui.Column(c).FillWidth().Gap(2).Children(func() {
						for _, target := range request.targets {
							ui.Text(c.Key(target.id), target.name+"  ·  "+target.endpoint).FontSize(12).FillWidth()
						}
					})
				})
				ui.Text(c, bulkPreview(request.script, 4, 360)).Font("monospace").FontSize(12).FillWidth().Padding(7).Background(colors.overviewPage).Radius(4).Selectable()
				if bulkConfirmButtons(c, &open, locales.BulkRunNow()) {
					a.bulkStart(view, *request, view.confirmRetry)
				}
			})
		})
		if !open {
			view.confirm = nil
		}
		return
	}
	if view.closeAsk {
		ui.Modal(c.Key("bulk-close"), &view.closeAsk, func() {
			ui.Column(c).Width(min(480, width-44)).Gap(9).Children(func() {
				ui.Text(c, locales.BulkCloseRunning()).FontSize(16).Bold().FillWidth()
				ui.Text(c, locales.BulkRunningNote()).FillWidth()
				if bulkConfirmButtons(c, &view.closeAsk, locales.BulkCloseNow()) {
					a.closeWindow(windowBulkExec)
				}
			})
		})
		return
	}
	if request := view.replace; request != nil {
		open := true
		ui.Modal(c.Key("bulk-replace"), &open, func() {
			ui.Column(c).Width(min(520, width-44)).Gap(8).Children(func() {
				ui.Text(c, locales.BulkExportReplace()).Bold().FontSize(16)
				ui.Text(c, request.path).FillWidth().Selectable()
				ui.Text(c, locales.BulkExportBackup()).FillWidth()
				if bulkConfirmButtons(c, &open, locales.Save()) {
					a.bulkExport(view, request)
				}
			})
		})
		if !open {
			view.replace = nil
		}
		return
	}
	for _, result := range view.rows {
		if view.outputRow != "" && result.target.id == view.outputRow {
			a.bulkOutputDialog(c, view, result, width, height)
			break
		}
	}
}

func bulkConfirmButtons(c *ui.Context, open *bool, label string) bool {
	yes := false
	ui.Row(c).FillWidth().Wrap().Justify(ui.End).Gap(8).Children(func() {
		if ui.Button(c, locales.Cancel()).AutoFocus().Clicked() {
			*open = false
		}
		if ui.PrimaryButton(c, label).Clicked() {
			*open, yes = false, true
		}
	})
	return yes
}

func (a *App) bulkOutputDialog(c *ui.Context, view *bulkView, result bulkResult, width, height float32) {
	open := true
	if view.output.Label != result.target.id {
		text := bulkOutput(result)
		if text == "" {
			text = locales.BulkNoOutput()
		}
		view.output.Buffer.Reset(text)
		view.output.Label = result.target.id
	}
	view.output.Theme = codeTheme()
	ui.Modal(c.Key("bulk-output"), &open, func() {
		ui.Column(c).Width(min(900, width-44)).Height(min(620, height-62)).Gap(8).Children(func() {
			ui.Text(c, result.target.name+" · "+bulkStatusText(result.status)).Bold().FontSize(16).FillWidth()
			ui.Text(c, result.target.endpoint).TextColor(theme.TextMuted).FillWidth()
			editor.View(c.Key(result.target.id), &view.output).Grow(1).MinHeight(0).FillWidth().Label(locales.BulkOutput())
			if result.truncated {
				ui.Text(c, locales.BulkTruncated()).TextColor(theme.Warning).FillWidth()
			}
			if errors.Is(result.err, monitor.ErrPrivilegeDenied) || errors.Is(result.err, monitor.ErrPrivilegeWrongPassword) {
				ui.Scroll(c).FillWidth().MaxHeight(132).Children(func() { privilegeHelp(c, result.target.user) })
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Wrap().Children(func() {
				if ui.Button(c, locales.BulkCopyOutput()).Disabled(bulkOutput(result) == "").Clicked() {
					c.WriteClipboard(bulkOutput(result))
				}
				if ui.PrimaryButton(c, locales.Close()).AutoFocus().Clicked() {
					open = false
				}
			})
		})
	})
	if !open {
		view.outputRow, view.output.Label = "", ""
		view.output.Buffer.Reset("")
	}
}
