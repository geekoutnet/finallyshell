package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *App) dbDetails(c *ui.Context, page *dbPage, conn *dbConnection) {
	redis := conn.profile.Engine == dbclient.Redis
	labels := []string{locales.DbData(), locales.DbStructure(), locales.DbEditor()}
	section := conn.section
	if redis {
		labels = []string{locales.DbKey(), locales.DbConsole()}
		if section == 2 {
			section = 1
		}
	}
	ui.Row(c).FillWidth().Gap(6).Padding(4, 7).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		sections := ui.Segmented(c, &section, labels...).Label(locales.DbTab())
		if sections.Changed() && !page.busy {
			conn.section = section
			if redis && section == 1 {
				conn.section = 2
			}
			if !redis && section == 1 && len(conn.structure.Columns.Columns) == 0 {
				a.dbReadStructure(page, conn)
			}
			if !redis && section == 0 && len(conn.table.Columns) == 0 {
				a.dbBrowse(page, conn, conn.page)
			}
		}
		title := conn.database
		if title == "" {
			title = conn.profile.Name
		}
		if conn.object.Name != "" && conn.section != 2 {
			title = dbObjectTitle(conn.object)
		}
		ui.Text(c, title).FontSize(12).TextColor(theme.TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(title)
	})
	ui.Column(c.Key(conn.section)).FillWidth().Grow(1).MinHeight(0).Children(func() {
		switch {
		case conn.section == 2:
			a.dbQueryPanel(c, page, conn)
		case redis:
			a.dbKeyPanel(c, page, conn)
		case conn.object.Name == "":
			overviewMessage(c, "layout-grid", locales.DbChooseObject(), nil)
		case conn.section == 1:
			a.dbStructurePanel(c, page, conn)
		default:
			a.dbDataPanel(c, page, conn)
		}
	})
}

func (a *App) dbDataPanel(c *ui.Context, page *dbPage, conn *dbConnection) {
	ui.Row(c).FillWidth().Padding(4, 7).Gap(4).AlignItems(ui.Center).Children(func() {
		apply := func() {
			request := conn.page
			request.Index, request.Where = 0, conn.where
			a.dbBrowse(page, conn, request)
		}
		field := ui.TextInput(c, &conn.where).Placeholder(locales.DbWhere()).Label(locales.DbWhere()).Grow(1).MinWidth(60)
		if field.Submitted() && !page.busy {
			apply()
		}
		if dbButton(c, "search", locales.DbApplyFilter(), dbBusy(page)) {
			apply()
		}
		if dbButton(c, "refresh-cw", locales.Refresh(), dbBusy(page)) {
			a.dbBrowse(page, conn, conn.page)
		}
		a.dbExportMenu(c, page, conn.table)
	})
	conn.grid.Sort = &conn.dataSort
	before := conn.dataSort
	a.dbGrid(c, page, &conn.grid, conn.table, true, nil)
	if before != conn.dataSort {
		if page.busy {
			conn.dataSort = before
		} else {
			request := conn.page
			request.Sort, request.Desc, request.Index = conn.dataSort.Column, conn.dataSort.Descending, 0
			a.dbBrowse(page, conn, request)
		}
	}
	ui.Row(c).FillWidth().Gap(6).Padding(3, 7).AlignItems(ui.Center).Children(func() {
		reason := dbBusy(page)
		previous, next := reason, reason
		if conn.page.Index == 0 {
			previous = locales.DbFirstPage()
		}
		if !conn.table.More {
			next = locales.DbLastPage()
		}
		if dbButton(c, "chevron-left", locales.DbPrevious(), previous) {
			request := conn.page
			request.Index--
			a.dbBrowse(page, conn, request)
		}
		if dbButton(c, "chevron-right", locales.DbNext(), next) {
			request := conn.page
			request.Index++
			a.dbBrowse(page, conn, request)
		}
		ui.Text(c, locales.DbPage(conn.page.Index+1, len(conn.table.Rows))).SingleLine().MinWidth(0).Grow(1).FontSize(12).TextColor(theme.TextMuted)
	})
}

func (a *App) dbStructurePanel(c *ui.Context, page *dbPage, conn *dbConnection) {
	structure := conn.structure
	ui.Row(c).FillWidth().Padding(4, 7).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
			if ui.Segmented(c, &conn.structureTab, locales.DbColumns(), locales.DbKeys(), locales.DbIndexes(), "CREATE").Changed() {
				conn.grid = ui.ListState{}
			}
		})
		if dbButton(c, "refresh-cw", locales.Refresh(), dbBusy(page)) {
			a.dbReadStructure(page, conn)
		}
	})
	if conn.structureTab == 3 {
		if structure.Reconstructed {
			dbNotice(c, locales.DbReconstructed(), false)
		}
		text := structure.Create
		ui.TextArea(c, &text).ReadOnly(true).Label(locales.DbStructure()).Font("monospace").FillWidth().Grow(1).MinHeight(0)
		return
	}
	result := []dbclient.Result{structure.Columns, structure.Keys, structure.Indexes}[conn.structureTab]
	a.dbGrid(c, page, &conn.grid, result, false, nil)
}

func (a *App) dbGrid(c *ui.Context, page *dbPage, state *ui.ListState, result dbclient.Result, sortable bool, extra func(*ui.Menu, int, int)) {
	if len(result.Columns) == 0 {
		ui.Column(c).FillWidth().Grow(1).MinHeight(0).Center().Children(func() { ui.Text(c, locales.DbNoResults()).TextColor(theme.TextMuted).FontSize(12) })
		return
	}
	cols := make([]ui.TableColumn, len(result.Columns))
	root := ui.Column(c).FillWidth().Grow(1).MinHeight(0)
	width := root.Bounds().W
	columnWidth := float32(140)
	if width > 0 && width < 480 {
		columnWidth = max(82, min(135, width/float32(len(cols))))
	}
	for index, name := range result.Columns {
		id := strconv.Itoa(index)
		if sortable {
			id = name
		}
		cols[index] = ui.TableColumn{ID: id, Title: dbColumnName(name, sortable), Width: columnWidth, Sortable: sortable}
	}
	cols[len(cols)-1].Width, cols[len(cols)-1].MinWidth = 0, columnWidth
	if !sortable {
		state.Sort = nil
	}
	state.CellMenu = func(menu *ui.Menu, row, col int) {
		if row < 0 || row >= len(result.Rows) || col < 0 || col >= len(result.Rows[row]) {
			return
		}
		value := result.Rows[row][col]
		if dbMenu(menu, locales.DbViewValue(), "") {
			if value.Null {
				value.Text = "NULL"
			}
			page.viewer = dbEditor(value.Text, dbclient.Redis, true)
		}
		if dbMenu(menu, locales.DbCopyValue(), "") {
			if value.Null {
				value.Text = "NULL"
			}
			c.WriteClipboard(value.Text)
		}
		if extra != nil {
			extra(menu, row, col)
		}
	}
	root.Children(func() {
		ui.Table(c, state, cols, len(result.Rows), func(row, col int) {
			value := result.Rows[row][col]
			text := dbShort(value.Text, 95)
			if value.Null {
				text = "NULL"
			}
			cell := ui.Text(c, text).SingleLine().FontSize(12)
			if value.Null || value.Binary {
				cell.TextColor(theme.TextMuted).Font("monospace")
			}
		}).Label(locales.DbResults()).FillWidth().Grow(1).MinHeight(0)
	})
}

func dbColumnName(name string, raw bool) string {
	if raw {
		return name
	}
	switch name {
	case "Value":
		return locales.DbValue()
	case "Name":
		return locales.DbName()
	case "Type":
		return locales.DbType()
	case "Nullable":
		return locales.DbNullable()
	case "Default", "DefaultValue":
		return locales.DbDefault()
	case "ColumnName":
		return locales.DbColumns()
	case "ColumnKey":
		return locales.DbKeys()
	case "Field", "Fields":
		return locales.DbField()
	case "Reply":
		return locales.DbReply()
	}
	return name
}

func (a *App) dbExportMenu(c *ui.Context, page *dbPage, result dbclient.Result) {
	reason := ""
	if len(result.Columns) == 0 {
		reason = locales.DbNoResults()
	}
	ui.MenuButton(c, locales.DbExport(), func(menu *ui.Menu) {
		for _, extension := range []string{"csv", "json"} {
			if dbMenu(menu, strings.ToUpper(extension), reason) {
				a.dbExport(page, result, extension)
			}
		}
	}).Disabled(reason != "").Tooltip(reason).Shrink(0)
}

func (a *App) dbExport(page *dbPage, result dbclient.Result, extension string) {
	title := locales.DbExport()
	go func() {
		path, err := a.plat.SaveFile("", title, "database-result."+extension, strings.ToUpper(extension), []string{extension})
		if err != nil || path == "" {
			if err != nil {
				a.post(func() { page.err = err })
			}
			return
		}
		info, existing := os.Lstat(path)
		write := func() {
			go func() {
				var data bytes.Buffer
				err := dbclient.Export(&data, result, extension)
				if err == nil {
					if existing == nil {
						err = dbReplaceFile(path, data.Bytes())
					} else {
						err = dbWriteNew(path, data.Bytes())
					}
				}
				a.post(func() {
					page.err = err
					if err == nil {
						page.status = locales.DbExported()
					}
				})
			}()
		}
		a.post(func() {
			if !page.current() {
				return
			}
			if existing == nil && info.Mode().IsRegular() {
				page.question = &dbQuestion{message: locales.DbOverwrite(path), detail: locales.DbBackupDetail(), accept: write}
			} else if existing == nil {
				page.err = dbclient.ErrInput
			} else if os.IsNotExist(existing) {
				write()
			} else {
				page.err = existing
			}
		})
	}()
}

func (a *App) dbQueryPanel(c *ui.Context, page *dbPage, conn *dbConnection) {
	root := ui.Column(c).FillWidth().Grow(1).MinHeight(0).Clip()
	height := root.Bounds().H
	root.Children(func() { a.dbQueryContents(c, page, conn, height) })
}

func (a *App) dbQueryContents(c *ui.Context, page *dbPage, conn *dbConnection, height float32) {
	var selected string
	ui.Row(c).FillWidth().Padding(4, 7).Gap(4).AlignItems(ui.Center).Wrap().Children(func() {
		selected = conn.code.SelectedText()
		if ui.PrimaryButton(c, locales.DbRun()).Disabled(page.busy).Clicked() {
			a.dbRun(page, conn, conn.code.Buffer.String())
		}
		reason := dbBusy(page)
		if selected == "" {
			reason = locales.DbSelectText()
		}
		if dbButton(c, "arrow-up-right", locales.DbRunSelection(), reason) {
			a.dbRun(page, conn, selected)
		}
		if dbButton(c, "rotate-cw", locales.DbHistory(), dbBusy(page)) {
			conn.showHistory = !conn.showHistory
		}
	})
	if conn.showHistory {
		ui.Scroll(c).MaxHeight(105).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(3, 7).Gap(3).Children(func() {
				if len(conn.history) == 0 {
					ui.Text(c, locales.DbHistoryEmpty()).FontSize(12)
				}
				for index, entry := range conn.history {
					if ui.Button(c.Key(index), dbShort(entry.text, 80)).FillWidth().MinWidth(0).Tooltip(entry.database).Clicked() {
						conn.code.Buffer.Reset(entry.text)
						conn.database = entry.database
						conn.showHistory = false
					}
				}
			})
		})
	}
	var box ui.Element
	if len(conn.results) == 0 {
		box = editor.View(c.Key("query-editor"), conn.code).Grow(1).MinHeight(0).FillWidth()
	} else {
		box = editor.View(c.Key("query-editor"), conn.code).Height(min(230, max(46, height*0.27))).Shrink(1).MinHeight(35).FillWidth()
	}
	box.Label(locales.DbQueryText()).ContextMenu(func(menu *ui.Menu) {
		if dbMenu(menu, locales.DbRunSelection(), bulkReason(selected == "", locales.DbSelectText())) && !page.busy {
			a.dbRun(page, conn, selected)
		}
		if dbMenu(menu, locales.DbRun(), dbBusy(page)) {
			a.dbRun(page, conn, conn.code.Buffer.String())
		}
	})
	if len(conn.results) == 0 {
		return
	}
	result := conn.results[conn.resultIndex]
	ui.Row(c).FillWidth().Padding(3, 7).Gap(4).AlignItems(ui.Center).Children(func() {
		labels := make([]string, len(conn.results))
		for index, item := range conn.results {
			labels[index] = fmt.Sprintf("%d · %s", index+1, item.Verb)
		}
		if dbSelect(c, &conn.resultIndex, labels).Label(locales.DbResults()).MaxWidth(140).Shrink(0).Changed() {
			conn.resultGrid = ui.ListState{}
		}
		count := ""
		if result.AffectedKnown {
			count = locales.DbAffected(int(result.Affected))
		}
		if len(result.Columns) != 0 {
			if count != "" {
				count = locales.DbRows(len(result.Rows)) + " · " + count
			} else {
				count = locales.DbRows(len(result.Rows))
			}
		}
		if result.More {
			count += " · " + locales.DbTruncated()
		}
		if count != "" {
			count += " · "
		}
		ui.Text(c, count+result.Duration.Round(time.Millisecond).String()).SingleLine().Grow(1).MinWidth(0).FontSize(12).TextColor(theme.TextMuted)
		a.dbExportMenu(c, page, result)
	})
	if len(result.Columns) != 0 {
		a.dbGrid(c, page, &conn.resultGrid, result, false, nil)
	}
}

func (a *App) dbRun(page *dbPage, conn *dbConnection, text string) {
	if page.busy || conn.client == nil {
		return
	}
	if a.dbContainsSecret(page, conn, text) {
		page.err = dbclient.ErrSecret
		return
	}
	batch := dbclient.Batch{Text: text, Database: conn.database, ReadOnly: conn.profile.ReadOnly}
	var verbs []string
	var err error
	if conn.profile.Engine == dbclient.Redis {
		var statements []dbclient.RedisStatement
		statements, err = dbclient.RedisStatements(text)
		for _, statement := range statements {
			if !statement.ReadOnly {
				verbs = append(verbs, statement.Args[0])
			}
		}
	} else {
		var statements []dbclient.SQLStatement
		statements, err = dbclient.SQLStatements(conn.profile.Engine, text)
		for _, statement := range statements {
			if !statement.ReadOnly {
				verbs = append(verbs, statement.Verb)
			}
		}
	}
	if err != nil {
		page.err = err
		return
	}
	if len(verbs) != 0 {
		if batch.ReadOnly {
			page.err = dbclient.ErrReadOnly
			return
		}
		target := conn.profile.Name
		if batch.Database != "" {
			target += " / " + batch.Database
		}
		batch.Confirmed = true
		page.question = &dbQuestion{message: locales.DbConfirmQuery(target), detail: locales.DbQueryConsequence(strings.Join(verbs, ", ")) + "\n\n" + text, accept: func() { a.dbExecute(page, conn, batch) }}
		return
	}
	a.dbExecute(page, conn, batch)
}

func (a *App) dbExecute(page *dbPage, conn *dbConnection, batch dbclient.Batch) {
	client := conn.client
	if client == nil || page.active != conn {
		return
	}
	batch.ReadOnly = conn.profile.ReadOnly
	a.dbWork(page, 2*time.Minute, func(ctx context.Context) func() {
		results, err := client.Run(ctx, batch)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			conn.results, conn.resultIndex, conn.resultGrid = results, 0, ui.ListState{}
			if !errors.Is(err, dbclient.ErrSecret) {
				conn.history = append([]dbHistoryEntry{{batch.Text, batch.Database}}, conn.history[:min(len(conn.history), 99)]...)
			}
			page.err = err
			if batch.Confirmed {
				a.dbAfterWrite(page, conn)
			}
		}
	})
}

func (a *App) dbAfterWrite(page *dbPage, conn *dbConnection) {
	previous := page.err
	client := conn.client
	object, request := conn.object, conn.page
	pattern, key, picked := conn.appliedPattern, conn.key, conn.keyPicked
	isRedis := conn.profile.Engine == dbclient.Redis
	section := conn.section
	namespace := dbclient.Namespace{Database: conn.database}
	if conn.profile.Engine == dbclient.MySQL {
		namespace.Schema = conn.database
	}
	if object.Database != "" {
		namespace.Database = object.Database
		if conn.profile.Engine == dbclient.MySQL {
			namespace.Schema = object.Namespace
		}
	}
	refreshObjects := conn.open[namespace]
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		var err error
		var namespaces []dbclient.Namespace
		var result dbclient.Result
		var list dbclient.KeyPage
		var view dbclient.KeyView
		var objects []dbclient.Object
		var structure dbclient.Structure
		if isRedis {
			list, err = client.Scan(ctx, pattern, 0)
			if err == nil && picked {
				view, err = client.Key(ctx, key, dbclient.KeyCursor{})
			}
		} else {
			namespaces, err = client.Namespaces(ctx)
			if err == nil && refreshObjects {
				objects, err = client.Objects(ctx, namespace)
			}
			if err == nil && object.Name != "" {
				result, err = client.Browse(ctx, object, request)
				if err == nil && section == 1 {
					structure, err = client.Structure(ctx, object)
				}
			}
		}
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			if previous != nil {
				page.err = previous
			} else {
				page.err = err
			}
			if isRedis {
				conn.keys, conn.keyView, conn.scanCursors, conn.keyCursors = list, view, []uint64{0}, []dbclient.KeyCursor{{}}
				conn.chosenKeys.Clear()
			} else {
				if namespaces != nil {
					conn.namespaces = namespaces
				}
				conn.table = result
				conn.structure = structure
				clear(conn.objects)
				clear(conn.open)
				if refreshObjects {
					conn.objects[namespace], conn.open[namespace] = objects, true
				}
			}
		}
	})
	page.err = previous
}
