package app

import (
	"slices"
	"time"

	"finallyshell/internal/config"

	"github.com/egoist/mygo/ui"
)

const desktopDockHeight = 64

type desktopShellState struct {
	root            ui.Rect
	pointer         ui.Point
	drag            *desktopFrameDrag
	preview         string
	covered, reveal bool
	hidden          map[*desktopWindow]bool
	hiddenFocus     *desktopWindow
	cycle           []*desktopWindow

	selection  map[string]bool
	anchor     string
	doubleIcon string
	iconKeys   bool
	band       *desktopBand
	iconDrag   *desktopIconDrag
	folderRect ui.Rect
	// folderAt is where the user dragged the panel of the folder
	// folderMoved, which opens centered until then.
	folderAt    ui.Point
	folderMoved string
	// folderSize is the size the user gave that panel by its edges, zero
	// while it has its own.
	folderSize                               desktopSize
	folder, rename, renameText, deleteFolder string
	renameFocus                              bool
	// layoutConfirm is the layout change asked about: desktopConfirm*.
	layoutConfirm int

	launcher, launcherFocus bool
	launcherButton          ui.Rect
	hostButton              ui.Point
	query                   string
	dockPopup               desktopAppID
	hostPopup, dockOverflow bool
	dockDrag                *desktopDockDrag
	dockRelease             bool
	dockPositions           []desktopDockPosition

	// The tray's popups, the month its calendar shows, and the power
	// menu's restart or shutdown (desktop_shell_tray.go).
	trayPopup, clockPopup, powerPopup bool
	clockMonth                        time.Time
	power                             opsState
}

func (a *App) desktopPreferences() config.DesktopSettings {
	if a.settings.Desktop != nil {
		return *a.settings.Desktop
	}
	return config.DesktopSettings{}
}

func (a *App) changeDesktopPreferences(change func(*config.DesktopSettings)) {
	next := a.desktopPreferences()
	change(&next)
	a.changeSettings(func() { a.settings.Desktop = &next })
}

func (a *App) desktopArea(c *ui.Context, row *Session) {
	d, s := row.desktop, row.panelOf
	prefs := a.desktopPreferences()
	root := ui.Box(c).Fill().Clip()
	bounds := root.Bounds()
	if bounds.W > 0 && bounds.H > 0 {
		area := desktopSize{W: bounds.W, H: bounds.H}
		if d.area != area {
			d.area = area
			c.Invalidate()
		}
	}
	d.shell.root = bounds
	x, y, inside := root.PointerPosition()
	d.shell.pointer = ui.Point{X: x, Y: y}
	d.shell.covered = prefs.CoverDock && slices.ContainsFunc(d.windows, func(w *desktopWindow) bool { return w.maximized && !w.minimized })
	if d.shell.covered {
		d.shell.reveal = inside && (y >= d.area.H-5 || d.shell.reveal && y >= d.area.H-desktopDockHeight)
	} else {
		d.shell.reveal = false
	}
	a.desktopWallpaper(root, prefs.Wallpaper, 0)
	root.Children(func() {
		a.desktopIcons(c, d, s)
		a.desktopServerInfo(c, d, s)
		for _, w := range a.desktopWindowsByZ(d) {
			a.desktopWindowFrame(c, d, w)
		}
		a.desktopSnapPreview(c, d)
		a.desktopFolderPanel(c, d, s)
		a.desktopDock(c, d, s)
		a.desktopLauncher(c, d, s)
		a.opsWorkDialog(c, s, &d.shell.power)
		a.desktopSurfaceDialogs(c, d, s)
	})
}
