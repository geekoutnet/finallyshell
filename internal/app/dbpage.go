package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"golang.org/x/crypto/ssh"
)

type dbOpener func(context.Context, dbclient.Profile, string, dbclient.Dialer) (dbclient.Client, error)
type dbScriptRunner func(context.Context, *ssh.Client, string) (string, string, error)

type dbWorkspace struct {
	pages    map[*Session]*dbPage
	profiles dbProfiles
	loaded   bool
	loadErr  error
	open     dbOpener
	script   dbScriptRunner
	path     string
}

type dbPage struct {
	session     *Session
	ssh         *ssh.Client
	life        context.Context
	owner       context.Context
	end         context.CancelFunc
	connections map[string]*dbConnection
	active      *dbConnection
	form        *dbForm
	question    *dbQuestion
	viewer      *editor.State
	keyEdit     *dbKeyEdit
	discovery   bool
	endpoints   []dbclient.Endpoint
	status      string
	err         error
	busy        bool
	sequence    int
	cancel      context.CancelFunc
}

type dbConnection struct {
	profile                            dbclient.Profile
	client                             dbclient.Client
	stop                               func() bool
	expanded                           bool
	namespaces                         []dbclient.Namespace
	objects                            map[dbclient.Namespace][]dbclient.Object
	open                               map[dbclient.Namespace]bool
	schemas                            map[dbclient.Namespace]bool
	database                           string
	object                             dbclient.Object
	where                              string
	page                               dbclient.Page
	table                              dbclient.Result
	structure                          dbclient.Structure
	grid, resultGrid                   ui.ListState
	dataSort                           ui.SortOrder
	section, structureTab, resultIndex int
	code                               *editor.State
	secret                             func(string) bool
	history                            []dbHistoryEntry
	showHistory                        bool
	results                            []dbclient.Result
	keys                               dbclient.KeyPage
	pattern, appliedPattern            string
	key                                string
	keyPicked                          bool
	keyView                            dbclient.KeyView
	keyCursors                         []dbclient.KeyCursor
	scanCursors                        []uint64
	chosenKeys                         ui.Selection[string]
}

type dbQuestion struct {
	message, detail string
	accept          func()
}
type dbHistoryEntry struct{ text, database string }

func (a *App) dbConfigPath() string {
	if a.db.path != "" {
		return a.db.path
	}
	return dbProfilesPath()
}

func (a *App) dbPageOf(s *Session) *dbPage {
	if !a.db.loaded {
		a.db.profiles, a.db.loadErr = loadDBProfiles(a.dbConfigPath())
		a.db.loaded = true
	}
	if a.db.pages == nil {
		a.db.pages = make(map[*Session]*dbPage)
	}
	if previous := a.db.pages[s]; previous != nil && (previous.ssh != s.client || previous.owner != s.ctx) {
		previous.dispose()
		delete(a.db.pages, s)
	}
	if page := a.db.pages[s]; page != nil {
		return page
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	life, end := context.WithCancel(parent)
	page := &dbPage{session: s, ssh: s.client, life: life, owner: s.ctx, end: end, connections: make(map[string]*dbConnection)}
	a.db.pages[s] = page
	if page.ssh != nil {
		go func() { page.ssh.Wait(); end() }()
	}
	if s.ctx != nil || s.client != nil {
		context.AfterFunc(life, func() {
			a.post(func() {
				if a.db.pages[s] == page {
					page.dispose()
					delete(a.db.pages, s)
				}
			})
		})
	}
	return page
}

func (p *dbPage) dispose() {
	p.end()
	if p.cancel != nil {
		p.cancel()
	}
	for _, conn := range p.connections {
		conn.disconnect()
	}
}

func (conn *dbConnection) disconnect() {
	if conn.stop != nil {
		conn.stop()
		conn.stop = nil
	}
	if conn.client != nil {
		client := conn.client
		conn.client = nil
		go client.Close()
	}
	conn.secret = nil
}

func (p *dbPage) current() bool {
	return p.session.status == statusConnected && p.session.client == p.ssh && (p.life == nil || p.life.Err() == nil)
}

func (p *dbPage) parent() context.Context {
	if p.life != nil {
		return p.life
	}
	return context.Background()
}

func (a *App) dbWork(page *dbPage, timeout time.Duration, work func(context.Context) func()) {
	if page.busy || !page.current() {
		return
	}
	ctx, cancel := context.WithTimeout(page.parent(), timeout)
	page.busy, page.cancel, page.err, page.status = true, cancel, nil, ""
	page.sequence++
	sequence := page.sequence
	go func() {
		apply := work(ctx)
		cancel()
		a.post(func() {
			if page.sequence != sequence {
				return
			}
			page.busy, page.cancel = false, nil
			apply()
		})
	}()
}

func dbErr(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return locales.DbCancelled()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.DbTimeout()
	case errors.Is(err, dbclient.ErrProfile):
		return locales.DbInvalidProfile()
	case errors.Is(err, dbclient.ErrInput):
		return locales.DbInvalidInput()
	case errors.Is(err, dbclient.ErrSecret):
		return locales.DbCredentialInput()
	case errors.Is(err, dbclient.ErrReadOnly):
		return locales.DbReadOnlyBlocked()
	case errors.Is(err, dbclient.ErrResultSize):
		return locales.DbResultLimit()
	case err != nil:
		return err.Error()
	}
	return ""
}

func (a *App) databasePanel(c *ui.Context) {
	s := a.connSession()
	ui.Column(c.Key("database-page")).FillWidth().Grow(1).MinHeight(0).Background(colors.overviewPage).Children(func() {
		switch {
		case s == nil || s.isNewTab():
			overviewMessage(c, "unplug", locales.NotConnected(), nil)
		case s.isLocal():
			localUnavailable(c)
		case s.status != statusConnected:
			a.statusBanner(c, s)
		default:
			a.dbView(c.Key(s.ID), a.dbPageOf(s))
		}
	})
}

func (a *App) dbView(c *ui.Context, page *dbPage) {
	conn := page.active
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).Wrap().AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		if ui.Button(c, locales.DbNew()).Disabled(a.db.loadErr != nil || page.busy).Clicked() {
			a.dbOpenForm(page, nil, dbclient.Endpoint{})
		}
		reason := ""
		if !execAllowed(page.session.Host) {
			reason = locales.SysInfoOff()
		}
		if ui.Button(c, locales.DbDiscover()).Disabled(page.busy || a.db.loadErr != nil || reason != "").Tooltip(reason).Clicked() {
			a.dbDiscover(page, false)
		}
		if conn != nil && conn.client != nil {
			readonly := conn.profile.ReadOnly
			if ui.Checkbox(c, &readonly, locales.DbReadOnly()).Disabled(page.busy).Changed() {
				a.dbSetReadOnly(page, conn, readonly)
			}
		}
		if conn != nil {
			ui.MenuButton(c, locales.DbConnectionActions(), func(menu *ui.Menu) { a.dbProfileMenu(menu, page, conn.profile, conn) }).Disabled(page.busy)
		}
		if page.busy {
			ui.Spinner(c).Label(locales.DbWorking())
			if ui.Button(c, locales.Cancel()).Clicked() && page.cancel != nil {
				page.cancel()
			}
		}
	})
	if a.db.loadErr != nil {
		dbNotice(c, locales.DbLoadFailed(), true)
		return
	}
	if page.err != nil {
		message := dbErr(page.err)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, message).SingleLine().Grow(1).MinWidth(0).Padding(3, 8).TextColor(theme.Danger).Tooltip(dbShort(message, 300))
			if dbButton(c, "file-text", locales.DbErrorDetails(), "") {
				page.viewer = dbEditor(message, dbclient.Redis, true)
			}
		})
	} else if page.status != "" {
		dbNotice(c, page.status, false)
	}
	body := ui.Row(c).FillWidth().Grow(1).MinHeight(0).AlignItems(ui.Stretch).Clip()
	width := body.Bounds().W
	if width == 0 {
		width = 460
	}
	body.Children(func() {
		ui.Column(c).Width(min(226, max(128, width*0.22))).Shrink(0).MinHeight(0).BorderWidth(0, 1, 0, 0).BorderColor(colors.line).Children(func() { a.dbTree(c, page) })
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Clip().Children(func() {
			if conn == nil || conn.client == nil {
				overviewMessage(c, "hard-drive", locales.DbChooseConnection(), nil)
				return
			}
			a.dbDetails(c, page, conn)
		})
	})
	a.dbDialogs(c, page)
}

func dbNotice(c *ui.Context, text string, danger bool) {
	line := ui.Text(c, text).SingleLine().FillWidth().Padding(3, 8).Tooltip(text)
	if danger {
		line.TextColor(theme.Danger)
	} else {
		line.TextColor(theme.TextMuted)
	}
}

func (a *App) dbDiscover(page *dbPage, root bool) {
	run := a.db.script
	if run == nil {
		run = monitor.RunScript
	}
	var privileged *monitor.Privilege
	if root {
		privileged = a.sessionPrivilege(page.session).runner
	}
	a.dbWork(page, 25*time.Second, func(ctx context.Context) func() {
		listeners, _, listenErr := run(ctx, page.ssh, dbclient.ListenScript)
		var ports, stderr string
		var err error
		if privileged != nil {
			ports, stderr, err = privileged.Run(ctx, dbclient.DockerScript, monitor.PrivilegeOptions{})
		} else {
			ports, stderr, err = run(ctx, page.ssh, dbclient.DockerScript)
		}
		endpoints := dbclient.Discover(listeners, ports)
		workErr := ctx.Err()
		return func() {
			if !page.current() {
				return
			}
			page.discovery, page.endpoints = true, endpoints
			if workErr != nil {
				page.err = workErr
			} else if root && err != nil {
				page.err = err
			} else if listenErr != nil && len(endpoints) == 0 {
				page.err = listenErr
			}
			if !root && strings.Contains(strings.ToLower(stderr), "permission denied") {
				page.status = locales.DbDockerPermission()
			}
		}
	})
}

func dbEditor(text string, engine dbclient.Engine, readonly bool) *editor.State {
	buffer := editor.NewBuffer(text)
	if engine != dbclient.Redis {
		buffer.SetLanguage(editor.LanguageFor("query.sql"))
	}
	return &editor.State{Buffer: buffer, Font: codeFont, NoAutoClose: true, NoFolding: true, SoftWrap: true, ReadOnly: readonly}
}

func dbEngineName(engine dbclient.Engine) string {
	switch engine {
	case dbclient.MySQL:
		return "MySQL / MariaDB"
	case dbclient.Postgres:
		return "PostgreSQL"
	default:
		return "Redis"
	}
}

func dbShort(text string, limit int) string {
	text = strings.NewReplacer("\n", " ↵ ", "\r", "", "\t", " ⇥ ").Replace(text)
	count := 0
	for index := range text {
		if count == limit {
			return text[:index] + "…"
		}
		count++
	}
	return text
}

func dbOpenError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%s: %s", locales.DbConnectionFailed(), dbErr(err))
}

func dbButton(c *ui.Context, icon, label, reason string) bool {
	button := widgets.IconButton(c, icon, label).Shrink(0).Disabled(reason != "")
	if reason != "" {
		button.Tooltip(reason)
	}
	return button.Clicked()
}

func dbSelect(c *ui.Context, index *int, labels []string) ui.Element {
	current := labels[*index]
	selectBox := ui.Select(c, &current, labels)
	if selectBox.Changed() {
		*index = max(0, slices.Index(labels, current))
	}
	return selectBox
}

func (a *App) dbContainsSecret(page *dbPage, conn *dbConnection, text string) bool {
	if strings.Contains(text, "PRIVATE KEY-----") {
		return true
	}
	if sshPassword := a.passwords[page.session.Host.ID]; sshPassword != "" && strings.Contains(text, sshPassword) {
		return true
	}
	return conn != nil && conn.secret != nil && conn.secret(text)
}
