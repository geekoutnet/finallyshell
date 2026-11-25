package app

import (
	"maps"
	"slices"

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

	selection                                map[string]bool
	anchor                                   string
	doubleIcon                               string
	iconKeys                                 bool
	band                                     *desktopBand
	iconDrag                                 *desktopIconDrag
	folderRect                               ui.Rect
	folder, rename, renameText, deleteFolder string
	renameFocus                              bool

	launcher, launcherFocus bool
	launcherButton          ui.Rect
	hostButton              ui.Point
	query                   string
	dockPopup               desktopAppID
	hostPopup, dockOverflow bool
	dockDrag                *desktopDockDrag
	dockRelease             bool
	dockPositions           []desktopDockPosition
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

func (a *App) desktopHost(s *Session) config.DesktopHost {
	return a.desktopPreferences().Hosts[s.Host.ID]
}

func (a *App) changeDesktopHost(s *Session, change func(*config.DesktopHost)) {
	a.changeDesktopPreferences(func(prefs *config.DesktopSettings) {
		host := prefs.Hosts[s.Host.ID]
		host.Icons = slices.Clone(host.Icons)
		host.Folders = slices.Clone(host.Folders)
		change(&host)
		prefs.Hosts = maps.Clone(prefs.Hosts)
		if prefs.Hosts == nil {
			prefs.Hosts = make(map[string]config.DesktopHost)
		}
		prefs.Hosts[s.Host.ID] = host
	})
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
		for _, w := range a.desktopWindowsByZ(d) {
			a.desktopWindowFrame(c, d, w)
		}
		a.desktopSnapPreview(c, d)
		a.desktopFolderPanel(c, d, s)
		a.desktopDock(c, d, s)
		a.desktopLauncher(c, d, s)
	})
}
