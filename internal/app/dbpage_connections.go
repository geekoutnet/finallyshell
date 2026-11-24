package app

import (
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

var dbEngines = []dbclient.Engine{dbclient.MySQL, dbclient.Postgres, dbclient.Redis}
var dbTLSModes = []string{"disable", "require", "verify-full"}

type dbForm struct {
	profile        dbclient.Profile
	engine, tls    int
	port, password string
	previous       *dbclient.Profile
	authOnly       bool
	err            error
}

func (a *App) dbOpenForm(page *dbPage, previous *dbclient.Profile, found dbclient.Endpoint) {
	form := &dbForm{previous: previous}
	if previous != nil {
		form.profile = *previous
	} else {
		form.profile = dbclient.Profile{Host: "127.0.0.1", Engine: dbclient.MySQL, Port: 3306, TLS: "disable"}
		if found.Port != 0 {
			form.profile.Engine, form.profile.Host, form.profile.Port = found.Engine, found.Host, found.Port
			form.profile.Name = dbEngineName(found.Engine) + " :" + strconv.Itoa(found.Port)
		}
	}
	form.port = strconv.Itoa(form.profile.Port)
	form.engine = max(0, slices.Index(dbEngines, form.profile.Engine))
	form.tls = max(0, slices.Index(dbTLSModes, form.profile.TLS))
	page.form = form
}

func (form *dbForm) validated() (dbclient.Profile, error) {
	p := form.profile
	p.Name, p.Host, p.User, p.Database = strings.TrimSpace(p.Name), strings.TrimSpace(p.Host), strings.TrimSpace(p.User), strings.TrimSpace(p.Database)
	p.Port, _ = strconv.Atoi(strings.TrimSpace(form.port))
	p.Engine, p.TLS = dbEngines[form.engine], dbTLSModes[form.tls]
	for _, text := range []string{p.Name, p.Host, p.User, p.Database} {
		if form.password != "" && strings.Contains(text, form.password) {
			return p, dbclient.ErrSecret
		}
	}
	return p, p.Validate()
}

func (a *App) dbSaveProfile(page *dbPage, p dbclient.Profile, remove bool) error {
	id := page.session.Host.ID
	next := maps.Clone(a.db.profiles)
	profiles := slices.Clone(next[id])
	index := slices.IndexFunc(profiles, func(profile dbclient.Profile) bool { return profile.ID == p.ID })
	if index >= 0 && remove {
		profiles = slices.Delete(profiles, index, index+1)
	} else if index >= 0 {
		profiles[index] = p
	} else if !remove {
		profiles = append(profiles, p)
	}
	if len(profiles) == 0 {
		delete(next, id)
	} else {
		next[id] = profiles
	}
	if err := saveDBProfiles(a.dbConfigPath(), next); err != nil {
		return err
	}
	a.db.profiles = next
	return nil
}

func (a *App) dbSaveForm(page *dbPage, form *dbForm) {
	p, err := form.validated()
	if err == nil {
		for _, field := range []string{p.Name, p.Host, p.User, p.Database} {
			if a.dbContainsSecret(page, page.connections[p.ID], field) {
				err = dbclient.ErrSecret
				break
			}
		}
	}
	if err != nil {
		form.err = err
		return
	}
	if p.ID == "" {
		p.ID = rand.Text()
	}
	retarget := form.previous != nil && dbProfileRetargeted(*form.previous, p)
	if retarget && form.password == "" {
		p.Remember = false
	}
	accept := func() {
		if err := a.dbSaveProfile(page, p, false); err != nil {
			form.err = err
			return
		}
		for s, other := range a.db.pages {
			if s.Host.ID == page.session.Host.ID {
				if conn := other.connections[p.ID]; conn != nil {
					conn.disconnect()
				}
			}
		}
		key := dbSecretKey(page.session.Host.ID, p.ID)
		password := form.password
		page.form, form.password = nil, ""
		if !p.Remember || retarget {
			a.deleteSecret(key)
		}
		if password == "" && p.Remember {
			a.dbConnectSaved(page, p)
		} else {
			a.dbConnect(page, p, password, p.Remember && password != "")
		}
	}
	if form.previous == nil {
		accept()
		return
	}
	page.question = &dbQuestion{message: locales.DbConfirmProfile(form.previous.Name), detail: locales.DbProfileConsequence(), accept: accept}
}

func dbProfileRetargeted(old, next dbclient.Profile) bool {
	return old.Engine != next.Engine || old.Host != next.Host || old.Port != next.Port || old.User != next.User || old.Database != next.Database || old.TLS != next.TLS
}

func (a *App) dbConnectSaved(page *dbPage, p dbclient.Profile) {
	if !p.Remember {
		a.dbOpenForm(page, &p, dbclient.Endpoint{})
		page.form.authOnly = true
		return
	}
	a.secret(dbSecretKey(page.session.Host.ID, p.ID), func(password string, ok bool) {
		if !page.current() || page.busy {
			return
		}
		if !ok {
			a.dbOpenForm(page, &p, dbclient.Endpoint{})
			page.form.authOnly = true
			return
		}
		a.dbConnect(page, p, password, false)
	})
}

func (a *App) dbConnect(page *dbPage, p dbclient.Profile, password string, remember bool) {
	if page.busy || !page.current() {
		return
	}
	open := a.db.open
	if open == nil {
		open = dbclient.Open
	}
	var dial dbclient.Dialer
	if page.ssh != nil {
		dial = page.ssh.DialContext
	}
	conn := page.connections[p.ID]
	if conn == nil {
		conn = &dbConnection{profile: p, objects: make(map[dbclient.Namespace][]dbclient.Object), open: make(map[dbclient.Namespace]bool), schemas: make(map[dbclient.Namespace]bool), code: dbEditor("", p.Engine, false)}
		page.connections[p.ID] = conn
	}
	if dbProfileRetargeted(conn.profile, p) {
		conn.code = dbEditor("", p.Engine, false)
		conn.history, conn.results = nil, nil
	}
	conn.profile, conn.expanded, conn.database = p, true, p.Database
	page.active = conn
	a.dbWork(page, 12*time.Second, func(ctx context.Context) func() {
		client, err := open(ctx, p, password, dial)
		var namespaces []dbclient.Namespace
		var keys dbclient.KeyPage
		var browseErr error
		if err == nil {
			if p.Engine == dbclient.Redis {
				keys, browseErr = client.Scan(ctx, "", 0)
			} else {
				namespaces, browseErr = client.Namespaces(ctx)
			}
		}
		workErr := ctx.Err()
		return func() {
			if !page.current() || workErr != nil {
				if client != nil {
					go client.Close()
				}
				page.err = workErr
				return
			}
			if err != nil {
				page.err = dbOpenError(err)
				return
			}
			conn.disconnect()
			conn.client = client
			conn.secret = func(text string) bool { return password != "" && strings.Contains(text, password) }
			conn.stop = context.AfterFunc(page.parent(), func() { client.Close() })
			conn.namespaces, conn.keys, conn.scanCursors = namespaces, keys, []uint64{0}
			clear(conn.objects)
			clear(conn.open)
			clear(conn.schemas)
			conn.object, conn.keyPicked, conn.section = dbclient.Object{}, false, 0
			conn.keyView, conn.table, conn.structure = dbclient.KeyView{}, dbclient.Result{}, dbclient.Structure{}
			conn.chosenKeys.Clear()
			page.err = browseErr
			if remember {
				a.setSecret(dbSecretKey(page.session.Host.ID, p.ID), password)
			}
		}
	})
}

func (a *App) dbSetReadOnly(page *dbPage, conn *dbConnection, readonly bool) {
	change := func() {
		p := conn.profile
		p.ReadOnly = readonly
		if err := a.dbSaveProfile(page, p, false); err != nil {
			page.err = err
			return
		}
		for s, other := range a.db.pages {
			if s.Host.ID == page.session.Host.ID {
				if same := other.connections[p.ID]; same != nil {
					same.profile.ReadOnly = readonly
				}
			}
		}
	}
	if readonly {
		change()
	} else {
		page.question = &dbQuestion{message: locales.DbEnableWrites(conn.profile.Name), detail: locales.DbEnableWritesDetail(), accept: change}
	}
}

func (a *App) dbRemoveProfile(page *dbPage, p dbclient.Profile) {
	page.question = &dbQuestion{message: locales.DbRemoveProfile(p.Name), detail: locales.DbRemoveProfileDetail(), accept: func() {
		if err := a.dbSaveProfile(page, p, true); err != nil {
			page.err = err
			return
		}
		a.deleteSecret(dbSecretKey(page.session.Host.ID, p.ID))
		for s, other := range a.db.pages {
			if s.Host.ID != page.session.Host.ID {
				continue
			}
			if conn := other.connections[p.ID]; conn != nil {
				conn.disconnect()
				delete(other.connections, p.ID)
				if other.active == conn {
					other.active = nil
				}
			}
		}
	}}
}

func (a *App) dbFormView(c *ui.Context, page *dbPage) {
	form := page.form
	if form == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(510, max(300, width-55))).MaxHeight(height - 35).Gap(8).Children(func() {
			title := locales.DbNew()
			if form.previous != nil {
				title = locales.DbEditProfile()
			}
			if form.authOnly {
				title = locales.DbPasswordPrompt(form.profile.Name)
			}
			ui.Text(c, title).FontSize(17).Bold()
			ui.Scroll(c).FillWidth().MaxHeight(max(140, height-160)).Children(func() {
				ui.Column(c).FillWidth().Gap(7).Children(func() {
					if form.authOnly {
						dbPasswordField(c, form)
					} else {
						dbFormFields(c, form)
					}
				})
			})
			if form.err != nil {
				dbNotice(c, dbErr(form.err), true)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				label := locales.DbSaveConnect()
				if form.authOnly {
					label = locales.Connect()
				}
				if ui.PrimaryButton(c, label).Clicked() {
					if form.authOnly {
						password := form.password
						form.password, page.form = "", nil
						a.dbConnect(page, form.profile, password, form.profile.Remember && password != "")
					} else {
						a.dbSaveForm(page, form)
					}
				}
			})
		})
	})
	if !open && page.form == form {
		form.password = ""
		page.form = nil
	}
}

func dbFormFields(c *ui.Context, form *dbForm) {
	field := func(label string, value *string) {
		ui.Field(c, label, func() { ui.TextInput(c, value).Label(label).FillWidth() }).FillWidth()
	}
	field(locales.DbName(), &form.profile.Name)
	ui.Field(c, locales.DbEngine(), func() {
		if dbSelect(c, &form.engine, []string{"MySQL / MariaDB", "PostgreSQL", "Redis"}).Label(locales.DbEngine()).FillWidth().Changed() {
			form.port = strconv.Itoa(dbEngines[form.engine].Port())
			form.profile.Database = ""
		}
	}).FillWidth()
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { field(locales.DbHost(), &form.profile.Host) })
		ui.Column(c).Width(96).Shrink(0).Children(func() { field(locales.DbPort(), &form.port) })
	})
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { field(locales.DbUser(), &form.profile.User) })
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { field(locales.DbDatabase(), &form.profile.Database) })
	})
	dbPasswordField(c, form)
	ui.Field(c, "TLS", func() {
		dbSelect(c, &form.tls, []string{locales.DbTLSOff(), locales.DbTLSRequire(), locales.DbTLSVerify()}).Label("TLS").FillWidth()
	}).FillWidth()
	ui.Row(c).Gap(9).Wrap().Children(func() {
		ui.Checkbox(c, &form.profile.Remember, locales.DbRemember())
		ui.Checkbox(c, &form.profile.ReadOnly, locales.DbReadOnly())
	})
}

func dbPasswordField(c *ui.Context, form *dbForm) {
	ui.Field(c, locales.DbPassword(), func() { ui.TextInput(c, &form.password).Password().Label(locales.DbPassword()).FillWidth() }).FillWidth()
}

func dbContextError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ctx.Err()
	}
	return err
}
