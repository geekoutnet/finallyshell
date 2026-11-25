package app

import (
	"errors"
	"slices"
	"sort"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The desktop mode: a session's server as a desktop, as GMSSH's, with
// icons on a wallpaper, windows that move, resize, minimize and
// maximize, and a dock. It is a panel row of the session (paneltab.go)
// whose panelTab is tabDesktop: a second view of the session's
// connection, opened from the session's menu or the bottom panel's
// corner, closed with the session.
//
// The desktop's apps register themselves (registerDesktopApp) from their
// own files, desktop_<app>.go; a window shows its app's view, which reads
// the session through w.session and keeps its own state in w.state.
//
// Contract for the desktop's files (do not rename what is here; adding is
// fine):
//   - desktop.go: the types, the registry, opening and closing windows,
//     the desktop row. The window manager, the icons, the dock, the
//     launcher and the wallpaper are desktop_shell*.go.
//   - each app: desktop_<app>*.go, registering in an init func.

// tabDesktop is a panel row's panelTab when the row is its session's
// desktop. It is no tab of the bottom panel.
const tabDesktop = 1000

// desktopAppID names a desktop app: in settings (pinned apps, icons) and
// in desktopOpen.
type desktopAppID string

// The built-in apps.
const (
	appThisPC    desktopAppID = "thispc"
	appFiles     desktopAppID = "files"
	appTerminal  desktopAppID = "terminal"
	appTrash     desktopAppID = "trash"
	appSettings  desktopAppID = "settings"
	appTaskMgr   desktopAppID = "taskmgr"
	appBrowser   desktopAppID = "browser"
	appAppCenter desktopAppID = "appcenter"
	appAI        desktopAppID = "ai"
)

// Categories of desktop apps, as the app center groups them.
const (
	desktopCatSystem = "system" // this PC, files, terminal, trash, settings, task manager
	desktopCatTools  = "tools"  // browser, AI
	desktopCatServer = "server" // the session's tools: Docker, databases, sites…
)

// desktopApp is an app of the desktop.
type desktopApp struct {
	id   desktopAppID
	icon string        // a Lucide icon's name (internal/icons)
	tint ui.Color      // the icon tile's color
	name func() string // its name, in the UI's language
	// category groups it in the app center; order sorts it there, on the
	// desktop and in the launcher.
	category string
	order    int
	// onDesktop shows it on the desktop by default, and pinned in the
	// dock by default.
	onDesktop, pinned bool
	// single keeps one window of it at most: opening it again brings the
	// window to the front, with the new arg.
	single bool
	// size is a new window's content size, minSize its smallest.
	size, minSize desktopSize
	// view draws the window's content, which fills it.
	view func(a *App, c *ui.Context, w *desktopWindow)
	// opened, if set, is called when desktopOpen opens a window or
	// brings a single one to the front, with the arg given.
	opened func(a *App, w *desktopWindow, arg any)
	// closing, if set, is asked before the user closes a window of it:
	// false keeps it open (unsaved changes, a running task).
	closing func(a *App, w *desktopWindow) bool
	// available, if set, hides the app for sessions it cannot serve.
	available func(s *Session) bool
}

// desktopSize is a width and a height in DIPs.
type desktopSize struct{ W, H float32 }

// desktopApps are the registered apps.
var desktopApps []*desktopApp

// registerDesktopApp adds app to the desktop's apps. Call it from an
// init func.
func registerDesktopApp(app desktopApp) {
	if app.id == "" || app.view == nil || desktopAppByID(app.id) != nil {
		panic("desktop: bad or duplicate app " + string(app.id))
	}
	if app.size == (desktopSize{}) {
		app.size = desktopSize{W: 860, H: 560}
	}
	if app.minSize == (desktopSize{}) {
		app.minSize = desktopSize{W: 360, H: 240}
	}
	desktopApps = append(desktopApps, &app)
	sort.SliceStable(desktopApps, func(i, j int) bool { return desktopApps[i].order < desktopApps[j].order })
}

// desktopAppByID is the app id, nil for none.
func desktopAppByID(id desktopAppID) *desktopApp {
	for _, app := range desktopApps {
		if app.id == id {
			return app
		}
	}
	return nil
}

// desktopAppsFor are the apps s can use, in order.
func desktopAppsFor(s *Session) []*desktopApp {
	var out []*desktopApp
	for _, app := range desktopApps {
		if app.available == nil || app.available(s) {
			out = append(out, app)
		}
	}
	return out
}

// desktopWindow is a window of the desktop.
type desktopWindow struct {
	id  int64
	app *desktopApp
	// session is the connection's session, not the desktop row.
	session *Session
	// title is shown instead of the app's name when set (a folder's name).
	title string
	// x, y, w, h are its frame in the desktop's area, title bar included.
	x, y, w, h float32
	minimized  bool
	maximized  bool
	// snapped is "" or "left" or "right": filling that half.
	snapped string
	// restore is the frame to go back to from maximized or snapped.
	restore [4]float32
	// z orders the windows: the highest is in front.
	z         int
	keepOnTop bool
	// state is the app's own state for this window.
	state any
	// opened is when it opened.
	opened time.Time
}

// displayTitle is the title its bar and the dock show.
func (w *desktopWindow) displayTitle() string {
	if w.title != "" {
		return w.title
	}
	return w.app.name()
}

// desktopState is a desktop row's state: its windows and what the shell
// shows. The shell's files add fields of their own below.
type desktopState struct {
	windows []*desktopWindow
	focused *desktopWindow // nil when the desktop itself has the focus
	nextZ   int
	// area is the desktop's size in the last frame, where new windows
	// are placed.
	area desktopSize
	// log is what the desktop's apps did on the server, newest last
	// (the settings app shows it).
	log []desktopLogEntry

	shell desktopShellState // desktop_shell*.go
}

// desktopLogEntry is an operation an app ran on the server.
type desktopLogEntry struct {
	at   time.Time
	app  desktopAppID
	text string // what it did, as the user reads it
	err  string // "" when it worked
}

// maxDesktopLog is how many entries the operation log keeps.
const maxDesktopLog = 500

var nextDesktopWindowID int64

// desktopRowOf is s's desktop row, nil for none.
func (a *App) desktopRowOf(s *Session) *Session {
	for _, p := range a.sessions {
		if p.panelOf == s && p.panelTab == tabDesktop {
			return p
		}
	}
	return nil
}

// isDesktopRow tells whether s is a desktop row; false for nil.
func (s *Session) isDesktopRow() bool { return s.isPanelTab() && s.panelTab == tabDesktop }

// canOpenDesktop tells whether s is a session that has a desktop: an SSH
// session, not a new tab, a local terminal, Telnet or a serial port.
func canOpenDesktop(s *Session) bool {
	return s != nil && !s.isPanelTab() && !s.isNewTab() && !s.isLocal() && !s.isDeviceSession()
}

// openDesktop selects s's desktop row, adding it right below s (and its
// panel row) first when it has none.
func (a *App) openDesktop(s *Session) {
	if s.isPanelTab() {
		s = s.panelOf
	}
	if !canOpenDesktop(s) {
		return
	}
	d := a.desktopRowOf(s)
	if d == nil {
		i := slices.Index(a.sessions, s)
		if i < 0 {
			return
		}
		if p := a.panelRowOf(s); p != nil {
			i = slices.Index(a.sessions, p)
		}
		d = &Session{ID: nextSessionID.Add(1), status: statusClosed, start: startState{row: -1}, panelOf: s, panelTab: tabDesktop, desktop: &desktopState{}}
		a.sessions = slices.Insert(a.sessions, i+1, d)
	}
	a.active = slices.Index(a.sessions, d)
}

// desktopOf is the desktop of s's connection: s's desktop row's state, nil
// when it has none. s may be the session or one of its rows.
func (a *App) desktopOf(s *Session) *desktopState {
	if s.isDesktopRow() {
		return s.desktop
	}
	if s.isPanelTab() {
		s = s.panelOf
	}
	if d := a.desktopRowOf(s); d != nil {
		return d.desktop
	}
	return nil
}

// desktopOpen opens a window of the app id on the desktop of s's
// connection, opening the desktop first if needed, and returns it; nil
// when there is no such app or s has no desktop. A single app's window
// that is open comes to the front instead. arg goes to the app's opened.
func (a *App) desktopOpen(s *Session, id desktopAppID, arg any) *desktopWindow {
	app := desktopAppByID(id)
	if s.isPanelTab() {
		s = s.panelOf
	}
	if app == nil || !canOpenDesktop(s) {
		return nil
	}
	if a.desktopRowOf(s) == nil {
		cur := a.current()
		a.openDesktop(s)
		if i := slices.Index(a.sessions, cur); i >= 0 {
			a.active = i
		}
	}
	d := a.desktopOf(s)
	if app.single {
		for _, w := range d.windows {
			if w.app == app {
				a.desktopFocus(w)
				if app.opened != nil {
					app.opened(a, w, arg)
				}
				return w
			}
		}
	}
	w := &desktopWindow{id: nextDesktopWindowIDNext(), app: app, session: s, opened: time.Now()}
	w.w, w.h = app.size.W, app.size.H+desktopTitleHeight+desktopTitleDivider
	a.placeDesktopWindow(d, w)
	d.windows = append(d.windows, w)
	a.desktopFocus(w)
	if app.opened != nil {
		app.opened(a, w, arg)
	}
	return w
}

func nextDesktopWindowIDNext() int64 { nextDesktopWindowID++; return nextDesktopWindowID }

// desktopFocus brings w to the front, restoring it if minimized.
func (a *App) desktopFocus(w *desktopWindow) {
	d := a.desktopOf(w.session)
	if d == nil {
		return
	}
	w.minimized = false
	d.shell.iconKeys = false
	d.shell.cycle = nil
	d.nextZ++
	w.z = d.nextZ
	d.focused = w
}

// desktopClose closes w at once, without asking its app.
func (a *App) desktopClose(w *desktopWindow) {
	d := a.desktopOf(w.session)
	if d == nil {
		return
	}
	d.windows = slices.DeleteFunc(d.windows, func(x *desktopWindow) bool { return x == w })
	delete(d.shell.hidden, w)
	if len(d.shell.hidden) == 0 {
		d.shell.hidden, d.shell.hiddenFocus = nil, nil
	}
	if d.shell.drag != nil && d.shell.drag.window == w {
		d.shell.drag, d.shell.preview = nil, ""
	}
	if d.focused == w {
		d.focused = d.topWindow()
	}
}

// desktopRequestClose closes w as its close button does: after its
// app's closing agrees.
func (a *App) desktopRequestClose(w *desktopWindow) {
	if w.app.closing != nil && !w.app.closing(a, w) {
		return
	}
	a.desktopClose(w)
}

// topWindow is the frontmost window not minimized, nil for none.
func (d *desktopState) topWindow() *desktopWindow {
	var top *desktopWindow
	for _, w := range d.windows {
		if !w.minimized && (top == nil || desktopWindowOrder(top, w) < 0) {
			top = w
		}
	}
	return top
}

// windowsOf are d's windows of app, in the order they opened.
func (d *desktopState) windowsOf(app desktopAppID) []*desktopWindow {
	var out []*desktopWindow
	for _, w := range d.windows {
		if w.app.id == app {
			out = append(out, w)
		}
	}
	return out
}

// desktopLog adds an operation to the log of s's desktop.
func (a *App) desktopLog(s *Session, app desktopAppID, text string, err error) {
	d := a.desktopOf(s)
	if d == nil {
		return
	}
	e := desktopLogEntry{at: time.Now(), app: app, text: text}
	if err != nil {
		e.err = err.Error()
	}
	d.log = append(d.log, e)
	if n := len(d.log) - maxDesktopLog; n > 0 {
		d.log = slices.Delete(d.log, 0, n)
	}
}

// errDesktopNotImplemented is what a contract's stub returns until its
// file implements it.
var errDesktopNotImplemented = errors.New("not implemented yet")

// desktopTitleHeight is a window's title bar's height.
const desktopTitleHeight = 36
const desktopTitleDivider = 1

// desktopRowIcon and desktopRowName are the desktop row's icon and name in
// the session list.
const desktopRowIcon = "monitor"

func desktopRowName() string { return locales.DesktopTab() }

// desktopMenuItem is the "Open desktop" item of a session's menu.
func (a *App) desktopMenuItem(m *ui.Menu, s *Session) {
	if canOpenDesktop(s) && m.Item(locales.OpenDesktop()).Chosen() {
		a.openDesktop(s)
	}
}

// desktopCornerButton is the button beside the panel's corner button that
// opens the session's desktop, under the terminal or in a panel row.
func (a *App) desktopCornerButton(c *ui.Context) {
	s := a.current()
	if a.panelOnRight() && !s.isPanelTab() {
		return // the narrow column keeps its room for the tabs; the session's menu opens the desktop
	}
	if s.isPanelTab() {
		s = s.panelOf
	}
	if !canOpenDesktop(s) {
		return
	}
	if widgets.IconButton(c, desktopRowIcon, locales.OpenDesktop()).Clicked() {
		a.openDesktop(s)
	}
}

// desktopPlaceholder is the view of an app whose file is not written yet.
func desktopPlaceholder(a *App, c *ui.Context, w *desktopWindow) {
	ui.Column(c).Fill().Center().Gap(8).Children(func() {
		ui.Icon(c, icons.Get(w.app.icon)).FontSize(32).TextColor(theme.TextMuted)
		ui.Text(c, locales.DesktopComingSoon()).TextColor(theme.TextMuted)
	})
}

// desktopAddShortcutHook, set by the shell, puts a shortcut to the remote
// path on the desktop of s's connection: the file manager's "Send to
// desktop". Nil hides that item.
var desktopAddShortcutHook func(a *App, s *Session, path string)
