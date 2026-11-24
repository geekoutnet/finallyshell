package app

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type dbKeyEdit struct {
	conn                    *dbConnection
	key, kind, field, value string
}

func (a *App) dbKeyTree(c *ui.Context, page *dbPage, conn *dbConnection) {
	ui.Row(c).FillWidth().Gap(3).Children(func() {
		input := ui.TextInput(c, &conn.pattern).Label(locales.DbPattern()).Placeholder(locales.DbPattern()).Grow(1).MinWidth(35)
		apply := dbButton(c, "search", locales.DbApplyFilter(), dbBusy(page))
		if apply || input.Submitted() && !page.busy {
			a.dbLoadKeys(page, conn, 0, 0)
		}
	})
	index := max(0, len(conn.scanCursors)-1)
	ui.Row(c).FillWidth().Gap(2).AlignItems(ui.Center).Children(func() {
		prev, next := dbBusy(page), dbBusy(page)
		if index == 0 {
			prev = locales.DbFirstPage()
		}
		if conn.keys.Next == 0 {
			next = locales.DbLastPage()
		}
		if dbButton(c, "chevron-left", locales.DbPrevious(), prev) {
			a.dbLoadKeys(page, conn, conn.scanCursors[index-1], index-1)
		}
		if dbButton(c, "chevron-right", locales.DbNext(), next) {
			a.dbLoadKeys(page, conn, conn.keys.Next, index+1)
		}
		ui.Text(c, strconv.Itoa(index+1)).FontSize(11).TextColor(theme.TextMuted).Grow(1).MinWidth(0)
		if dbButton(c, "trash-2", locales.DbDeleteKeys(), dbDeleteReason(page, conn, conn.chosenKeys.Len())) {
			a.dbDeleteKeys(page, conn, slices.Sorted(conn.chosenKeys.All()))
		}
	})
	if len(conn.keys.Keys) == 0 {
		ui.Text(c, locales.DbNoKeys()).SingleLine().FontSize(11).TextColor(theme.TextMuted)
	}
	for _, key := range conn.keys.Keys {
		ui.Row(c.Key(key)).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(2).Children(func() {
			checked := conn.chosenKeys.Has(key)
			label := dbShort(key, 95)
			if label == "" {
				label = `""`
			}
			if ui.Checkbox(c, &checked, "").Label(locales.ManagerSelectRow(label)).Width(19).Shrink(0).Disabled(page.busy).Changed() {
				if checked {
					conn.chosenKeys.Add(key)
				} else {
					conn.chosenKeys.Remove(key)
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				dbTreeRow(c, 0, "key-round", label, conn.keyPicked && conn.key == key, dbBusy(page), func(menu *ui.Menu) {
					if dbMenu(menu, locales.DbKey(), dbBusy(page)) {
						a.dbLoadKey(page, conn, key, dbclient.KeyCursor{}, 0)
					}
					targets := []string{key}
					if conn.chosenKeys.Has(key) {
						targets = slices.Sorted(conn.chosenKeys.All())
					}
					reason := dbDeleteReason(page, conn, len(targets))
					if menu.Item(locales.DbDeleteKeys()).Danger().Subtitle(reason).Disabled(reason != "").Chosen() {
						a.dbDeleteKeys(page, conn, targets)
					}
				}, func() { a.dbLoadKey(page, conn, key, dbclient.KeyCursor{}, 0) })
			})
		})
	}
}

func (a *App) dbLoadKeys(page *dbPage, conn *dbConnection, cursor uint64, index int) {
	client, pattern := conn.client, conn.appliedPattern
	if index == 0 {
		pattern = conn.pattern
	}
	if a.dbContainsSecret(page, conn, pattern) {
		page.err = dbclient.ErrSecret
		return
	}
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		keys, err := client.Scan(ctx, pattern, cursor)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			page.err = err
			if err != nil {
				return
			}
			conn.keys, conn.appliedPattern = keys, pattern
			conn.scanCursors = append(conn.scanCursors[:min(index, len(conn.scanCursors))], cursor)
			conn.chosenKeys.Clear()
		}
	})
}

func (a *App) dbLoadKey(page *dbPage, conn *dbConnection, key string, cursor dbclient.KeyCursor, index int) {
	client := conn.client
	conn.section = 0
	if !conn.keyPicked || conn.key != key {
		conn.key, conn.keyPicked, conn.keyView, conn.keyCursors = key, true, dbclient.KeyView{}, nil
	}
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		view, err := client.Key(ctx, key, cursor)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client || conn.key != key {
				return
			}
			page.err = err
			if err != nil {
				return
			}
			conn.keyView, conn.grid = view, ui.ListState{}
			conn.keyCursors = append(conn.keyCursors[:min(index, len(conn.keyCursors))], cursor)
		}
	})
}

func dbDeleteReason(page *dbPage, conn *dbConnection, count int) string {
	if conn.profile.ReadOnly {
		return locales.DbReadOnlyBlocked()
	}
	if count == 0 {
		return locales.DbChooseKeys()
	}
	return dbBusy(page)
}

func (a *App) dbKeyPanel(c *ui.Context, page *dbPage, conn *dbConnection) {
	if !conn.keyPicked {
		overviewMessage(c, "key-round", locales.DbChooseKeys(), nil)
		return
	}
	view := conn.keyView
	ui.Row(c).FillWidth().Padding(4, 7).AlignItems(ui.Center).Gap(5).Children(func() {
		ttl := locales.DbTTLNoExpiry()
		switch {
		case view.Type == "" || view.Type == "none" || view.TTL == -2:
			ttl = locales.DbKeyMissing()
		case view.TTL >= 0:
			ttl = (time.Duration(view.TTL) * time.Millisecond).String()
		}
		text := fmt.Sprintf("%q · %s · TTL: %s", conn.key, view.Type, ttl)
		ui.Text(c, text).SingleLine().Grow(1).MinWidth(0).FontSize(12).Tooltip(text)
		if dbButton(c, "refresh-cw", locales.Refresh(), dbBusy(page)) {
			a.dbLoadKey(page, conn, conn.key, dbclient.KeyCursor{}, 0)
		}
		editReason := dbDeleteReason(page, conn, 1)
		if editReason == "" && !view.Editable {
			editReason = locales.DbKeyTypeReadOnly()
		}
		if dbButton(c, "pencil", locales.DbEditValue(), editReason) {
			a.dbOpenKeyEdit(page, conn, "", "")
		}
		count := 1
		if view.Type == "none" || view.Type == "" {
			count = 0
		}
		if dbButton(c, "trash-2", locales.DbDeleteKeys(), dbDeleteReason(page, conn, count)) {
			a.dbDeleteKeys(page, conn, []string{conn.key})
		}
	})
	a.dbGrid(c, page, &conn.grid, view.Data, false, func(menu *ui.Menu, row, col int) {
		reason := dbDeleteReason(page, conn, 1)
		if reason == "" && (!view.Editable || view.Data.Rows[row][0].Binary || strings.Contains(view.Data.Rows[row][0].Text, "•••") || view.Type == "hash" && (view.Data.Rows[row][1].Binary || strings.Contains(view.Data.Rows[row][1].Text, "•••"))) {
			reason = locales.DbKeyTypeReadOnly()
		}
		if dbMenu(menu, locales.DbEditValue(), reason) {
			if view.Type == "hash" {
				a.dbOpenKeyEdit(page, conn, view.Data.Rows[row][0].Text, view.Data.Rows[row][1].Text)
			} else {
				a.dbOpenKeyEdit(page, conn, "", view.Data.Rows[row][0].Text)
			}
		}
	})
	if view.Next != 0 || view.StreamNext != "" || len(conn.keyCursors) > 1 || view.Data.More {
		ui.Row(c).FillWidth().Padding(3, 7).Gap(4).AlignItems(ui.Center).Children(func() {
			index := max(0, len(conn.keyCursors)-1)
			prev, next := dbBusy(page), dbBusy(page)
			if index == 0 {
				prev = locales.DbFirstPage()
			}
			if view.Next == 0 && view.StreamNext == "" {
				next = locales.DbLastPage()
			}
			if dbButton(c, "chevron-left", locales.DbPrevious(), prev) {
				a.dbLoadKey(page, conn, conn.key, conn.keyCursors[index-1], index-1)
			}
			if dbButton(c, "chevron-right", locales.DbNext(), next) {
				a.dbLoadKey(page, conn, conn.key, dbclient.KeyCursor{Position: view.Next, Stream: view.StreamNext}, index+1)
			}
			msg := locales.DbPage(index+1, len(view.Data.Rows))
			if view.Data.More {
				msg += " · " + locales.DbTruncated()
			}
			ui.Text(c, msg).SingleLine().Grow(1).MinWidth(0).FontSize(11).TextColor(theme.TextMuted)
		})
	}
}

func (a *App) dbOpenKeyEdit(page *dbPage, conn *dbConnection, field, value string) {
	if conn.keyView.Type == "string" && len(conn.keyView.Data.Rows) != 0 {
		value = conn.keyView.Data.Rows[0][0].Text
	}
	page.keyEdit = &dbKeyEdit{conn: conn, key: conn.key, kind: conn.keyView.Type, field: field, value: value}
}

func (a *App) dbDeleteKeys(page *dbPage, conn *dbConnection, keys []string) {
	if dbDeleteReason(page, conn, len(keys)) != "" {
		return
	}
	names := make([]string, len(keys))
	for index, key := range keys {
		names[index] = strconv.Quote(key)
	}
	page.question = &dbQuestion{message: locales.DbConfirmDelete(len(keys), conn.profile.Name), detail: locales.DbDeleteConsequence() + "\n\n" + strings.Join(names, "\n"), accept: func() { a.dbChangeKey(page, conn, dbclient.KeyChange{Keys: keys, Delete: true, Confirmed: true}) }}
}

func (a *App) dbChangeKey(page *dbPage, conn *dbConnection, change dbclient.KeyChange) {
	client := conn.client
	if client == nil || page.active != conn {
		return
	}
	change.ReadOnly = conn.profile.ReadOnly
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		err := client.ChangeKey(ctx, change)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			page.err = err
			if err == nil && change.Delete && slices.Contains(change.Keys, conn.key) {
				conn.keyPicked = false
				conn.keyView = dbclient.KeyView{}
			}
			a.dbAfterWrite(page, conn)
		}
	})
}

func (a *App) dbDialogs(c *ui.Context, page *dbPage) {
	if page.question != nil {
		a.dbQuestionView(c, page)
		return
	}
	a.dbFormView(c, page)
	if page.discovery {
		a.dbDiscoveryView(c, page)
	}
	if page.viewer != nil {
		open := true
		ui.Modal(c, &open, func() {
			width, height := c.Size()
			ui.Column(c).Width(min(760, width-45)).Gap(9).Children(func() {
				ui.Text(c, locales.DbViewValue()).FontSize(17).Bold()
				editor.View(c, page.viewer).Height(min(360, height*0.52)).FillWidth().Label(locales.DbValue())
				if ui.Button(c, locales.Close()).AlignSelf(ui.End).Clicked() {
					open = false
				}
			})
		})
		if !open {
			page.viewer = nil
		}
	}
	if page.keyEdit != nil {
		a.dbKeyEditView(c, page)
	}
}

func (a *App) dbQuestionView(c *ui.Context, page *dbPage) {
	question := page.question
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(620, width-44)).Gap(10).Children(func() {
			ui.Text(c, question.message).FontSize(17).Bold().FillWidth()
			ui.Scroll(c).MaxHeight(min(260, height*0.48)).FillWidth().Children(func() { ui.Text(c, question.detail).FillWidth().Selectable() })
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.DbConfirm()).Clicked() {
					page.question = nil
					open = false
					question.accept()
				}
			})
		})
	})
	if !open && page.question == question {
		page.question = nil
	}
}

func (a *App) dbKeyEditView(c *ui.Context, page *dbPage) {
	edit := page.keyEdit
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(540, width-45)).Gap(8).Children(func() {
			ui.Text(c, locales.DbEditValue()).Bold().FontSize(17)
			ui.Text(c, strconv.Quote(edit.key)).SingleLine().FillWidth()
			if edit.kind == "hash" {
				ui.TextInput(c, &edit.field).Placeholder(locales.DbField()).Label(locales.DbField()).FillWidth()
			}
			ui.TextArea(c, &edit.value).Height(min(170, height*0.40)).FillWidth().Label(locales.DbValue())
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.DbReviewChange()).Clicked() {
					target := strconv.Quote(edit.key)
					if edit.kind == "hash" {
						target += " / " + strconv.Quote(edit.field)
					}
					change := dbclient.KeyChange{Keys: []string{edit.key}, Type: edit.kind, Field: edit.field, Value: edit.value, Confirmed: true}
					if a.dbContainsSecret(page, edit.conn, edit.field) || a.dbContainsSecret(page, edit.conn, edit.value) {
						page.keyEdit = nil
						page.err = dbclient.ErrSecret
						open = false
						return
					}
					page.question = &dbQuestion{message: locales.DbConfirmQuery(edit.conn.profile.Name), detail: locales.DbKeyChange(target), accept: func() { page.keyEdit = nil; a.dbChangeKey(page, edit.conn, change) }}
				}
			})
		})
	})
	if !open {
		page.keyEdit = nil
	}
}

func (a *App) dbDiscoveryView(c *ui.Context, page *dbPage) {
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(490, width-45)).Gap(8).Children(func() {
			ui.Text(c, locales.DbDiscover()).Bold().FontSize(17)
			ui.Text(c, locales.DbDiscoverHint()).FillWidth()
			if !page.busy && len(page.endpoints) == 0 {
				ui.Text(c, locales.DbNoEndpoints()).TextColor(theme.TextMuted)
			}
			ui.Scroll(c).MaxHeight(min(200, height*0.34)).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					for _, endpoint := range page.endpoints {
						text := dbEngineName(endpoint.Engine) + " · " + net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
						if ui.Button(c.Key(endpoint), text).Disabled(page.busy).FillWidth().Clicked() {
							open = false
							a.dbOpenForm(page, nil, endpoint)
						}
					}
				})
			})
			if page.busy {
				ui.Spinner(c).Label(locales.DbWorking())
			} else if page.err != nil {
				dbNotice(c, dbErr(page.err), true)
			} else if page.status != "" {
				ui.Text(c, page.status).FillWidth()
			}
			ui.Row(c).Wrap().Justify(ui.End).Gap(7).Children(func() {
				if ui.Button(c, locales.DbDiscoverRoot()).Disabled(page.busy).Clicked() {
					a.dbDiscover(page, true)
				}
				if ui.Button(c, locales.Close()).Clicked() {
					open = false
					if page.cancel != nil {
						page.cancel()
					}
				}
			})
		})
	})
	page.discovery = open
}
