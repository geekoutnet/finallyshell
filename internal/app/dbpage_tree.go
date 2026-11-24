package app

import (
	"context"
	"strings"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) dbTree(c *ui.Context, page *dbPage) {
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Padding(4).Gap(2).Children(func() {
			ui.Text(c, locales.DbConnections()).FontSize(12).Bold().Padding(3, 4)
			for _, profile := range a.db.profiles[page.session.Host.ID] {
				a.dbProfileRow(c.Key(profile.ID), page, profile)
			}
		})
	})
}

func (a *App) dbProfileRow(c *ui.Context, page *dbPage, profile dbclient.Profile) {
	conn := page.connections[profile.ID]
	connected := conn != nil && conn.client != nil
	label := profile.Name
	icon := "hard-drive"
	if connected && conn.expanded {
		icon = "chevron-down"
	} else if connected {
		icon = "chevron-right"
	}
	reason := dbBusy(page)
	dbTreeRow(c, 0, icon, label, conn != nil && conn == page.active, reason, func(menu *ui.Menu) {
		a.dbProfileMenu(menu, page, profile, conn)
	}, func() {
		if !connected {
			a.dbConnectSaved(page, profile)
			return
		}
		if page.active == conn {
			conn.expanded = !conn.expanded
		} else {
			page.active, conn.expanded = conn, true
		}
	})
	if !connected || !conn.expanded || page.active != conn {
		return
	}
	if profile.Engine == dbclient.Redis {
		a.dbKeyTree(c, page, conn)
		return
	}
	for _, namespace := range conn.namespaces {
		a.dbNamespaceRow(c.Key(namespace), page, conn, namespace)
	}
}

func (a *App) dbProfileMenu(menu *ui.Menu, page *dbPage, profile dbclient.Profile, conn *dbConnection) {
	connected := conn != nil && conn.client != nil
	reason := dbBusy(page)
	if !connected && dbMenu(menu, locales.Connect(), reason) {
		a.dbConnectSaved(page, profile)
	}
	if connected {
		if dbMenu(menu, locales.DbDisconnect(), reason) {
			conn.disconnect()
		}
		if dbMenu(menu, locales.Refresh(), reason) {
			a.dbRefreshTree(page, conn)
		}
	}
	if dbMenu(menu, locales.DbEditProfile(), reason) {
		a.dbOpenForm(page, &profile, dbclient.Endpoint{})
	}
	if menu.Item(locales.DbRemove()).Danger().Disabled(reason != "").Subtitle(reason).Chosen() {
		a.dbRemoveProfile(page, profile)
	}
}

func (a *App) dbNamespaceRow(c *ui.Context, page *dbPage, conn *dbConnection, namespace dbclient.Namespace) {
	icon := "chevron-right"
	if conn.open[namespace] {
		icon = "chevron-down"
	}
	dbTreeRow(c, 1, icon, namespace.Database, conn.database == namespace.Database && conn.object.Name == "", dbBusy(page), func(menu *ui.Menu) {
		if dbMenu(menu, locales.DbEditor(), dbBusy(page)) {
			conn.database, conn.section = namespace.Database, 2
		}
		if dbMenu(menu, locales.Refresh(), dbBusy(page)) {
			a.dbLoadObjects(page, conn, namespace)
		}
	}, func() {
		conn.database = namespace.Database
		if conn.object.Database != namespace.Database {
			conn.object = dbclient.Object{}
		}
		conn.open[namespace] = !conn.open[namespace]
		if _, loaded := conn.objects[namespace]; !loaded && conn.open[namespace] {
			a.dbLoadObjects(page, conn, namespace)
		}
	})
	if !conn.open[namespace] {
		return
	}
	objects := conn.objects[namespace]
	if len(objects) == 0 && !page.busy {
		ui.Text(c, locales.DbNoObjects()).FontSize(11).TextColor(theme.TextMuted).SingleLine().Padding(2, 20)
	}
	previous := ""
	for _, object := range objects {
		depth := 2
		if namespace.Schema == "" {
			depth++
			schema := dbclient.Namespace{Database: object.Database, Schema: object.Namespace}
			if previous != object.Namespace {
				previous = object.Namespace
				icon := "chevron-right"
				if conn.schemas[schema] {
					icon = "chevron-down"
				}
				dbTreeRow(c.Key(schema), 2, icon, schema.Schema, false, dbBusy(page), nil, func() { conn.schemas[schema] = !conn.schemas[schema] })
			}
			if !conn.schemas[schema] {
				continue
			}
		}
		icon := "layout-grid"
		if object.View {
			icon = "eye"
		}
		dbTreeRow(c.Key(object), depth, icon, object.Name, conn.object == object, dbBusy(page), func(menu *ui.Menu) {
			if dbMenu(menu, locales.DbData(), dbBusy(page)) {
				a.dbOpenObject(page, conn, object, 0)
			}
			if dbMenu(menu, locales.DbStructure(), dbBusy(page)) {
				a.dbOpenObject(page, conn, object, 1)
			}
		}, func() { a.dbOpenObject(page, conn, object, 0) })
	}
}

func dbTreeRow(c *ui.Context, depth int, icon, label string, selected bool, reason string, menu func(*ui.Menu), press func()) {
	row := ui.Row(c).FillWidth().MinWidth(0).Padding(4, 4, 4, float32(4+depth*9)).Radius(4).Gap(4).AlignItems(ui.Center).Label(label).Role(ui.RoleButton).Tooltip(label)
	if selected {
		row.Background(theme.Accent.Alpha(0.16))
	} else if row.Hovered() {
		row.Background(theme.Accent.Alpha(0.08))
	}
	if menu != nil {
		row.ContextMenu(menu)
	}
	row.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(12).Shrink(0).TextColor(theme.Accent)
		ui.Text(c, dbShort(label, 96)).SingleLine().Grow(1).MinWidth(0).FontSize(12)
	})
	if row.Clicked() && reason == "" {
		press()
	}
}

func dbBusy(page *dbPage) string {
	if page.busy {
		return locales.DbWorking()
	}
	return ""
}

func dbMenu(menu *ui.Menu, label, reason string) bool {
	return menu.Item(label).Disabled(reason != "").Subtitle(reason).Chosen()
}

func (a *App) dbRefreshTree(page *dbPage, conn *dbConnection) {
	if conn.profile.Engine == dbclient.Redis {
		a.dbLoadKeys(page, conn, 0, 0)
		return
	}
	client := conn.client
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		namespaces, err := client.Namespaces(ctx)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			page.err = err
			if err == nil {
				conn.namespaces = namespaces
				clear(conn.open)
				clear(conn.objects)
			}
		}
	})
}

func (a *App) dbLoadObjects(page *dbPage, conn *dbConnection, namespace dbclient.Namespace) {
	client := conn.client
	a.dbWork(page, 15*time.Second, func(ctx context.Context) func() {
		objects, err := client.Objects(ctx, namespace)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client {
				return
			}
			page.err = err
			if err == nil {
				conn.objects[namespace] = objects
				for _, object := range objects {
					if object.Namespace == "public" {
						conn.schemas[dbclient.Namespace{Database: object.Database, Schema: object.Namespace}] = true
					}
				}
			}
		}
	})
}

func (a *App) dbOpenObject(page *dbPage, conn *dbConnection, object dbclient.Object, section int) {
	if conn.object != object {
		conn.page, conn.dataSort, conn.grid = dbclient.Page{}, ui.SortOrder{}, ui.ListState{}
		conn.where, conn.table, conn.structure = "", dbclient.Result{}, dbclient.Structure{}
	}
	conn.object, conn.database, conn.section = object, object.Database, section
	if section == 1 {
		a.dbReadStructure(page, conn)
	} else {
		a.dbBrowse(page, conn, conn.page)
	}
}

func (a *App) dbBrowse(page *dbPage, conn *dbConnection, request dbclient.Page) {
	client, object := conn.client, conn.object
	if object.Name == "" || client == nil {
		return
	}
	if a.dbContainsSecret(page, conn, request.Where) {
		page.err = dbclient.ErrSecret
		return
	}
	a.dbWork(page, 30*time.Second, func(ctx context.Context) func() {
		result, err := client.Browse(ctx, object, request)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client || conn.object != object {
				return
			}
			page.err = err
			if err == nil || len(result.Columns) != 0 {
				conn.table, conn.page = result, request
				conn.grid = ui.ListState{}
			}
			conn.dataSort = ui.SortOrder{Column: conn.page.Sort, Descending: conn.page.Desc}
		}
	})
}

func (a *App) dbReadStructure(page *dbPage, conn *dbConnection) {
	client, object := conn.client, conn.object
	if object.Name == "" || client == nil {
		return
	}
	a.dbWork(page, 20*time.Second, func(ctx context.Context) func() {
		structure, err := client.Structure(ctx, object)
		err = dbContextError(ctx, err)
		return func() {
			if !page.current() || conn.client != client || conn.object != object {
				return
			}
			page.err = err
			if err == nil || len(structure.Columns.Columns) > 0 {
				conn.structure = structure
			}
		}
	})
}

func dbObjectTitle(object dbclient.Object) string {
	return strings.Join([]string{object.Namespace, object.Name}, ".")
}
