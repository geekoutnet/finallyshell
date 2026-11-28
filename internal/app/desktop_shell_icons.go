package app

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

const desktopFolderPrefix = "folder:"
const desktopPathPrefix = "path:"

type desktopBand struct {
	start ui.Point
	prior map[string]bool
}
type desktopIconDrag struct {
	id     string
	start  ui.Point
	active bool
}
type desktopIconCell struct {
	icon config.DesktopIcon
	rect ui.Rect
}
type desktopGrid struct {
	spaceW, spaceH, tile float32
	rows, cols           int
}

func init() { desktopAddShortcutHook = (*App).desktopAddShortcut }

func desktopRemotePath(p string) bool {
	return path.IsAbs(p) && !strings.ContainsAny(p, "\x00\r\n")
}

func (a *App) desktopAddShortcut(s *Session, p string) {
	if s.isPanelTab() {
		s = s.panelOf
	}
	if !canOpenDesktop(s) || !desktopRemotePath(p) {
		return
	}
	id := desktopPathPrefix + path.Clean(p)
	if !a.desktopOwnLayout(s) {
		// A shortcut to a path means nothing on the other hosts.
		if slices.ContainsFunc(a.desktopHostIcons(s, a.desktopHost(s)), func(icon config.DesktopIcon) bool { return icon.App == id }) {
			return
		}
		a.desktopChangeOwnLayout(s, func(h *config.DesktopHost) {
			h.Icons = append(h.Icons, config.DesktopIcon{App: id, Col: -1, Row: -1})
		})
		a.notifyDesktopOwnLayout()
		return
	}
	a.desktopAddIcon(s, id)
}

func (a *App) desktopHostIcons(s *Session, h config.DesktopHost) []config.DesktopIcon {
	if h.IconsConfigured || h.Icons != nil {
		return h.Icons
	}
	return desktopDefaultIcons(s)
}

func (a *App) changeDesktopIcons(s *Session, change func(*config.DesktopHost)) {
	a.changeDesktopHost(s, func(h *config.DesktopHost) {
		h.Folders = desktopHostFolders(*h) // before Icons, which it reads
		h.Icons = a.desktopHostIcons(s, *h)
		h.IconsConfigured = true
		change(h)
	})
}

func (a *App) desktopAddIcon(s *Session, id string) {
	h := a.desktopHost(s)
	if slices.ContainsFunc(a.desktopHostIcons(s, h), func(icon config.DesktopIcon) bool { return icon.App == id && icon.Folder == "" }) {
		return
	}
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		if idx := slices.IndexFunc(h.Icons, func(i config.DesktopIcon) bool { return i.App == id }); idx >= 0 {
			h.Icons[idx].Folder = ""
			h.Icons[idx].Col, h.Icons[idx].Row = -1, -1
		} else {
			h.Icons = append(h.Icons, config.DesktopIcon{App: id, Col: -1, Row: -1})
		}
	})
}

func desktopIconFolder(h config.DesktopHost, id string) (config.DesktopFolder, bool) {
	for _, folder := range desktopHostFolders(h) {
		if folder.ID == id {
			return folder, true
		}
	}
	return config.DesktopFolder{}, false
}

func desktopIconInfo(s *Session, h config.DesktopHost, id string) (label, icon string, tint ui.Color) {
	if raw, ok := strings.CutPrefix(id, desktopFolderPrefix); ok {
		if f, ok := desktopIconFolder(h, raw); ok {
			return f.Name, "folder", ui.Hex("#ba722a")
		}
	} else if name, ok := desktopSurfaceFile(id); ok {
		icon, tint := desktopSurfaceIcon(id)
		return name, icon, tint
	} else if raw, ok := strings.CutPrefix(id, desktopPathPrefix); ok && desktopRemotePath(raw) {
		return path.Base(raw), "folder", ui.Hex("#2568c4")
	} else if app := desktopAppByID(desktopAppID(id)); app != nil && (app.available == nil || app.available(s)) {
		return app.name(), app.icon, app.tint
	}
	return "", "", ui.Color{}
}

func desktopMakeGrid(area desktopSize, size string) desktopGrid {
	g := desktopGrid{spaceW: 100, spaceH: 88, tile: 42}
	switch size {
	case "small":
		g = desktopGrid{spaceW: 76, spaceH: 71, tile: 32}
	case "large":
		g = desktopGrid{spaceW: 108, spaceH: 106, tile: 54}
	}
	g.rows = max(1, int(max(0, area.H-desktopDockHeight-18)/g.spaceH))
	g.cols = max(1, int(max(0, area.W-20)/g.spaceW))
	return g
}

func (g desktopGrid) rect(cell [2]int) ui.Rect {
	return ui.Rect{X: 10 + float32(cell[0])*g.spaceW, Y: 10 + float32(cell[1])*g.spaceH, W: g.spaceW - 4, H: g.spaceH - 4}
}

func (g desktopGrid) layout(s *Session, h config.DesktopHost, list []config.DesktopIcon) []desktopIconCell {
	cells := make([][2]int, len(list))
	used := make(map[[2]int]bool)
	for idx, icon := range list {
		cell := [2]int{icon.Col, icon.Row}
		cells[idx] = [2]int{-1, -1}
		if name, _, _ := desktopIconInfo(s, h, icon.App); name == "" || icon.Folder != "" {
			continue
		}
		if !h.AutoArrange && cell[0] >= 0 && cell[1] >= 0 && cell[0] < g.cols && cell[1] < g.rows && !used[cell] {
			cells[idx] = cell
			used[cell] = true
		}
	}
	var out []desktopIconCell
	next := 0
	for idx, icon := range list {
		if name, _, _ := desktopIconInfo(s, h, icon.App); name == "" || icon.Folder != "" {
			continue
		}
		cell := cells[idx]
		if cell[0] < 0 {
			for {
				cell = [2]int{next / g.rows, next % g.rows}
				next++
				if !used[cell] {
					break
				}
			}
			used[cell] = true
		}
		icon.Col, icon.Row = cell[0], cell[1]
		out = append(out, desktopIconCell{icon, g.rect(cell)})
	}
	return out
}

func desktopTile(c *ui.Context, icon string, tint ui.Color, size float32, raised bool) ui.Element {
	tile := ui.Box(c).Size(size, size).Shrink(0).Radius(size*.255).Center().
		Gradient(tint.Mix(ui.Hex("#a5bcff"), .28), tint, 150).
		Border(1, ui.RGBA(255, 255, 255, .23)).Shadow(0, 3, 8, 0, ui.RGBA(3, 8, 26, .29))
	if raised {
		tile.Scale(1.09, 1.09).Translate(0, -2)
	}
	if icon != "" {
		tile.Children(func() { ui.Icon(c, icons.Get(icon)).FontSize(size * .55).TextColor(ui.Hex("#ffffff")) })
	}
	return tile
}

func (a *App) desktopIcons(c *ui.Context, d *desktopState, s *Session) {
	shell := &d.shell
	prefs, h := a.desktopPreferences(), a.desktopHost(s)
	grid := desktopMakeGrid(d.area, prefs.IconSize)
	a.desktopSurface(d, s)
	items := grid.layout(s, h, append(slices.Clone(a.desktopHostIcons(s, h)), desktopSurfaceIcons(d)...))
	if shell.iconKeys && !shell.launcher && shell.deleteFolder == "" && shell.layoutConfirm == 0 {
		a.desktopSurfaceKeys(c, d, s)
	}
	bg := ui.Box(c.Key("desktop-background")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Label(locales.DesktopTab())
	bg.ContextMenu(func(m *ui.Menu) { a.desktopBackgroundMenu(c, m, d, s, h) })
	a.desktopMarquee(c, d, bg, items)
	if shell.iconKeys && !shell.launcher && shell.rename == "" && shell.deleteFolder == "" && shell.layoutConfirm == 0 && c.Shortcut(0, ui.KeyEnter) {
		var selected []string
		for _, icon := range append(slices.Clone(a.desktopHostIcons(s, h)), desktopSurfaceIcons(d)...) {
			if shell.selection[icon.App] && icon.Folder == shell.folder {
				selected = append(selected, icon.App)
			}
		}
		for _, id := range selected {
			a.desktopOpenIcon(d, s, id)
		}
	}
	for _, item := range items {
		tile := ui.Column(c.Key(item.icon.App)).Absolute().Left(item.rect.X).Top(item.rect.Y).Size(item.rect.W, item.rect.H)
		a.desktopIconView(c, d, s, h, tile, item.icon, grid, items)
	}
	if shell.band != nil && shell.band.start != shell.pointer {
		r := desktopBandRect(shell.band.start, shell.pointer)
		ui.Box(c).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).HitTest(desktopIgnoreHit).
			Background(ui.RGBA(130, 172, 255, .28)).Border(1, ui.RGBA(220, 230, 255, .75))
	}
}

func desktopIgnoreHit(float32, float32, ui.Rect) bool { return false }

func desktopBandRect(start, end ui.Point) ui.Rect {
	return ui.Rect{X: min(start.X, end.X), Y: min(start.Y, end.Y), W: max(start.X, end.X) - min(start.X, end.X), H: max(start.Y, end.Y) - min(start.Y, end.Y)}
}

func desktopContains(r ui.Rect, p ui.Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

func (a *App) desktopMarquee(c *ui.Context, d *desktopState, bg ui.Element, items []desktopIconCell) {
	sh := &d.shell
	_, _, pressed := bg.Dragged()
	if pressed {
		if sh.band == nil {
			prior := sh.selection
			if c.Modifiers()&(ui.Ctrl|ui.Super|ui.Shift) == 0 || sh.folder != "" {
				prior = nil
			}
			sh.band = &desktopBand{start: sh.pointer, prior: prior}
			sh.folder = ""
			sh.iconKeys = true
			d.focused = nil
			sh.cycle = nil
		}
		r := desktopBandRect(sh.band.start, sh.pointer)
		selected := make(map[string]bool)
		for id, on := range sh.band.prior {
			if on {
				selected[id] = true
			}
		}
		if r.W > 3 || r.H > 3 {
			for _, item := range items {
				v := item.rect
				if r.X < v.X+v.W && v.X < r.X+r.W && r.Y < v.Y+v.H && v.Y < r.Y+r.H {
					selected[item.icon.App] = true
				}
			}
		}
		sh.selection = selected
	} else {
		sh.band = nil
	}
}

func (a *App) desktopIconView(c *ui.Context, d *desktopState, s *Session, h config.DesktopHost, tile ui.Element, item config.DesktopIcon, g desktopGrid, items []desktopIconCell) {
	sh, id := &d.shell, item.App
	label, icon, tint := desktopIconInfo(s, h, id)
	if label == "" {
		return
	}
	insideFolder := item.Folder != ""
	folderID, isFolder := strings.CutPrefix(id, desktopFolderPrefix)
	tip := label
	if p, ok := strings.CutPrefix(id, desktopPathPrefix); ok {
		tip = p
	}
	tile.Radius(10).Gap(4).AlignItems(ui.Center).Justify(ui.Center).Clip().Role(ui.RoleButton).Label(label).Tooltip(tip)
	if sh.selection[id] && (insideFolder == (sh.folder != "")) {
		tile.Background(ui.RGBA(116, 163, 255, .32)).Border(1, ui.RGBA(218, 232, 255, .56))
	} else if tile.Hovered() {
		tile.Background(ui.RGBA(180, 200, 255, .17))
	}
	if tile.DoubleClicked() {
		sh.doubleIcon = id
	}
	if tile.Clicked() {
		mods := tile.ClickModifiers()
		double := sh.doubleIcon == id
		sh.doubleIcon = ""
		a.desktopSelectIcon(d, id, mods, items)
		if !insideFolder {
			sh.folder = ""
		}
		if (double || a.desktopPreferences().SingleClick) && mods&(ui.Ctrl|ui.Super|ui.Shift) == 0 {
			a.desktopOpenIcon(d, s, id)
		}
		c.Invalidate()
	}
	if tile.PressCanceled() && sh.doubleIcon == id {
		sh.doubleIcon = ""
	}
	tile.ContextMenu(func(m *ui.Menu) {
		if _, file := desktopSurfaceFile(id); file {
			a.desktopSurfaceMenu(c, m, d, s, id)
			return
		}
		a.desktopIconMenu(m, d, s, id)
	})
	a.desktopDragIcon(c, d, s, tile, item, g, items)
	tile.Children(func() {
		if isFolder {
			desktopTile(c, "", ui.Hex("#7165bb"), g.tile, false).Padding(4).Children(func() {
				var children []config.DesktopIcon
				for _, child := range a.desktopHostIcons(s, h) {
					if child.Folder == folderID {
						children = append(children, child)
					}
				}
				mini := (g.tile - 12) / 3
				ui.Column(c).Gap(2).Children(func() {
					for row := range 3 {
						ui.Row(c).Gap(2).Children(func() {
							for col := range 3 {
								idx := row*3 + col
								box := ui.Box(c).Size(mini, mini).Radius(2).Center().Background(ui.RGBA(255, 255, 255, .14))
								if idx < len(children) {
									_, glyph, color := desktopIconInfo(s, h, children[idx].App)
									box.Background(color).Children(func() { ui.Icon(c, icons.Get(glyph)).FontSize(mini * .78).TextColor(ui.Hex("#ffffff")) })
								}
							}
						})
					}
				})
			})
		} else {
			desktopTile(c, icon, tint, g.tile, false)
		}
		if sh.rename == folderID && isFolder {
			edit := ui.TextInput(c, &sh.renameText).Width(g.spaceW - 12).Height(22).FontSize(11).Label(locales.Rename())
			if sh.renameFocus {
				edit.Focus()
				sh.renameFocus = false
			}
			if edit.Submitted() {
				a.desktopRenameFolder(d, s)
			}
			if edit.Shortcut(0, ui.KeyEscape) {
				sh.rename = ""
			}
		} else {
			text := ui.Text(c, label).SingleLine().MaxWidth(g.spaceW - 12).FontSize(11)
			if insideFolder {
				text.TextColor(theme.Text)
			} else {
				text.TextColor(ui.Hex("#ffffff")).TextShadow(0, 1, 3, ui.RGBA(0, 0, 0, .92)).
					Padding(1, 4).Radius(4).Background(ui.RGBA(10, 14, 31, .31))
			}
		}
	})
}

func (a *App) desktopSelectIcon(d *desktopState, id string, mods ui.Modifiers, items []desktopIconCell) {
	sh := &d.shell
	sh.iconKeys = true
	d.focused = nil
	sh.cycle = nil
	if sh.selection == nil {
		sh.selection = make(map[string]bool)
	}
	if mods&ui.Shift != 0 && sh.anchor != "" {
		start := slices.IndexFunc(items, func(i desktopIconCell) bool { return i.icon.App == sh.anchor })
		end := slices.IndexFunc(items, func(i desktopIconCell) bool { return i.icon.App == id })
		if start >= 0 && end >= 0 {
			if mods&(ui.Ctrl|ui.Super) == 0 {
				clear(sh.selection)
			}
			for _, item := range items[min(start, end) : max(start, end)+1] {
				sh.selection[item.icon.App] = true
			}
			return
		}
	}
	if mods&(ui.Ctrl|ui.Super|ui.Shift) != 0 {
		sh.selection[id] = !sh.selection[id]
	} else {
		clear(sh.selection)
		sh.selection[id] = true
	}
	sh.anchor = id
}

func (a *App) desktopOpenIcon(d *desktopState, s *Session, id string) {
	if folder, ok := strings.CutPrefix(id, desktopFolderPrefix); ok {
		d.shell.folder = folder
		clear(d.shell.selection)
		d.shell.anchor = ""
		d.shell.iconKeys = true
		return
	}
	d.shell.folder = ""
	if _, file := desktopSurfaceFile(id); file {
		a.desktopSurfaceOpen(d, s, id)
		return
	}
	if p, ok := strings.CutPrefix(id, desktopPathPrefix); ok {
		if desktopRemotePath(p) {
			a.desktopOpen(s, appFiles, p)
		}
	} else {
		a.desktopOpen(s, desktopAppID(id), nil)
	}
}

func (a *App) desktopDragIcon(c *ui.Context, d *desktopState, s *Session, tile ui.Element, item config.DesktopIcon, g desktopGrid, items []desktopIconCell) {
	sh := &d.shell
	if _, file := desktopSurfaceFile(item.App); file {
		return // a file stays where its folder's order puts it
	}
	_, _, pressed := tile.Dragged()
	if pressed {
		if sh.iconDrag == nil {
			sh.iconDrag = &desktopIconDrag{id: item.App, start: sh.pointer}
		}
		drag := sh.iconDrag
		if drag.id != item.App {
			return
		}
		if desktopBandRect(drag.start, sh.pointer).W >= 5 || desktopBandRect(drag.start, sh.pointer).H >= 5 {
			drag.active = true
		}
		if drag.active {
			tile.Opacity(.58)
			if !sh.selection[item.App] {
				a.desktopSelectIcon(d, item.App, 0, items)
			}
		}
	} else if drag := sh.iconDrag; drag != nil && drag.id == item.App {
		sh.iconDrag = nil
		if _, _, ended := tile.DragEnded(); ended && drag.active {
			sh.doubleIcon = ""
			a.desktopDropIcon(d, s, item, g, items)
			c.Invalidate()
		}
	}
}

func (a *App) desktopDropIcon(d *desktopState, s *Session, item config.DesktopIcon, g desktopGrid, items []desktopIconCell) {
	sh := &d.shell
	p := sh.pointer
	if !desktopContains(ui.Rect{W: d.area.W, H: d.area.H - desktopDockHeight}, p) {
		return
	}
	if item.Folder != "" && desktopContains(sh.folderRect, p) {
		return
	}
	dest := ""
	for _, cell := range items {
		if folder, ok := strings.CutPrefix(cell.icon.App, desktopFolderPrefix); ok && desktopContains(cell.rect, p) {
			dest = folder
			break
		}
	}
	if _, folder := strings.CutPrefix(item.App, desktopFolderPrefix); folder && dest != "" {
		return
	}
	if dest == item.Folder && a.desktopHost(s).AutoArrange {
		return
	}
	col := min(g.cols-1, max(0, int((p.X-10)/g.spaceW)))
	row := min(g.rows-1, max(0, int((p.Y-10)/g.spaceH)))
	if idx := slices.IndexFunc(items, func(i desktopIconCell) bool { return i.icon.App == item.App }); idx >= 0 {
		item = items[idx].icon
	}
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		positions := make(map[string]config.DesktopIcon, len(items))
		for _, cell := range items {
			positions[cell.icon.App] = cell.icon
		}
		for idx := range h.Icons {
			i := &h.Icons[idx]
			if placed, ok := positions[i.App]; ok {
				i.Col, i.Row = placed.Col, placed.Row
			}
			if i.App == item.App {
				i.Col, i.Row, i.Folder = col, row, dest
			} else if dest == "" && i.Folder == "" && i.Col == col && i.Row == row {
				i.Col, i.Row = item.Col, item.Row
				if item.Folder != "" {
					i.Col, i.Row = -1, -1
				}
			}
		}
	})
}

func (a *App) desktopBackgroundMenu(c *ui.Context, m *ui.Menu, d *desktopState, s *Session, h config.DesktopHost) {
	m.Submenu(locales.DesktopShellView(), func(sub *ui.Menu) {
		for _, size := range desktopIconSizes {
			if sub.Item(size.name()).Radio(a.desktopPreferences().IconSize == size.id).Chosen() {
				a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.IconSize = size.id })
			}
		}
		sub.Separator()
		if sub.Item(locales.DesktopShellAutoArrange()).Checked(h.AutoArrange).Chosen() {
			a.changeDesktopHost(s, func(host *config.DesktopHost) { host.AutoArrange = !host.AutoArrange })
		}
	})
	m.Submenu(locales.DesktopShellSort(), func(sub *ui.Menu) {
		if sub.Item(locales.DesktopShellSortName()).Chosen() {
			a.desktopSortIcons(s, false)
		}
		if sub.Item(locales.DesktopShellSortType()).Chosen() {
			a.desktopSortIcons(s, true)
		}
	})
	a.desktopLayoutMenu(m, d, s)
	if m.Item(locales.Refresh()).Chosen() {
		a.desktopSurfaceRead(d)
		clear(d.shell.selection)
		a.desktopWall.requested = false
		c.Invalidate()
	}
	m.Submenu(locales.DesktopShellNew(), func(sub *ui.Menu) {
		a.desktopSurfaceNewMenu(sub, d, s)
		sub.Separator()
		if sub.Item(locales.DesktopShellNewGroup()).Chosen() {
			a.desktopNewFolder(d, s)
		}
	})
	if d.files != nil {
		ready := s.sftp != nil && d.filesDir != "" && d.files.explorer().job == nil
		m.Submenu(locales.DesktopFilesUpload(), func(sub *ui.Menu) {
			if ready && d.files.explorer().dir != d.filesDir {
				return // the folder is made by a file made or pasted first
			}
			a.desktopFilesUploadMenu(d.files, sub, ready)
		}).Disabled(!ready || d.files.explorer().dir != d.filesDir)
		if m.Item(locales.DesktopFilesPaste()).Disabled(!a.desktopSurfaceCanPaste(c, s, d)).Chosen() && a.desktopSurfaceCanPaste(c, s, d) {
			a.desktopSurfacePaste(c, d, s)
		}
	}
	m.Separator()
	a.desktopModeItem(m, s)
	m.Separator()
	if m.Item(locales.DesktopShellOpenTerminal()).Chosen() {
		a.desktopOpen(s, appTerminal, nil)
	}
	if m.Item(locales.DesktopShellOpenFiles()).Chosen() {
		a.desktopOpen(s, appFiles, nil)
	}
	if m.Item(locales.DesktopShellPersonalize()).Chosen() {
		a.desktopOpen(s, appSettings, "personalize")
	}
	if m.Item(locales.DesktopDockShowDesktop()).Chosen() {
		a.desktopShow(d)
	}
}

func (a *App) desktopSortIcons(s *Session, byType bool) {
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		typeOf := func(id string) string {
			if app := desktopAppByID(desktopAppID(id)); app != nil {
				return app.category
			}
			prefix, _, _ := strings.Cut(id, ":")
			return prefix
		}
		slices.SortStableFunc(h.Icons, func(first, second config.DesktopIcon) int {
			if byType {
				if order := strings.Compare(typeOf(first.App), typeOf(second.App)); order != 0 {
					return order
				}
			}
			firstName, _, _ := desktopIconInfo(s, *h, first.App)
			secondName, _, _ := desktopIconInfo(s, *h, second.App)
			return strings.Compare(strings.ToLower(firstName), strings.ToLower(secondName))
		})
		for idx := range h.Icons {
			if h.Icons[idx].Folder == "" {
				h.Icons[idx].Col, h.Icons[idx].Row = -1, -1
			}
		}
	})
}

func (a *App) desktopIconMenu(m *ui.Menu, d *desktopState, s *Session, id string) {
	if m.Item(locales.DesktopShellOpen()).Chosen() {
		a.desktopOpenIcon(d, s, id)
	}
	if folder, ok := strings.CutPrefix(id, desktopFolderPrefix); ok {
		if m.Item(locales.Rename()).Chosen() {
			a.desktopStartRename(d, s, folder)
		}
		if m.Item(locales.DesktopShellDeleteFolder()).Chosen() {
			d.shell.deleteFolder = folder
		}
		return
	}
	if app := desktopAppByID(desktopAppID(id)); app != nil && app.id != appAI {
		a.desktopPinMenu(m, app.id)
	}
	a.desktopMoveMenu(m, d, s, id)
	if m.Item(locales.DesktopShellRemoveIcon()).Chosen() {
		a.changeDesktopIcons(s, func(h *config.DesktopHost) {
			h.Icons = slices.DeleteFunc(h.Icons, func(icon config.DesktopIcon) bool { return icon.App == id })
		})
		delete(d.shell.selection, id)
	}
}

func (a *App) desktopNewFolder(d *desktopState, s *Session) string {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		h.Folders = append(h.Folders, config.DesktopFolder{ID: id, Name: locales.NewFolder()})
		h.Icons = append(h.Icons, config.DesktopIcon{App: desktopFolderPrefix + id, Col: -1, Row: -1})
	})
	d.shell.folder = ""
	a.desktopStartRename(d, s, id)
	return id
}

func (a *App) desktopStartRename(d *desktopState, s *Session, id string) {
	if f, ok := desktopIconFolder(a.desktopHost(s), id); ok {
		d.shell.rename, d.shell.renameText, d.shell.renameFocus = id, f.Name, true
	}
}

func (a *App) desktopRenameFolder(d *desktopState, s *Session) {
	id, name := d.shell.rename, strings.TrimSpace(d.shell.renameText)
	d.shell.rename = ""
	if name == "" {
		return
	}
	a.changeDesktopHost(s, func(h *config.DesktopHost) {
		for idx := range h.Folders {
			if h.Folders[idx].ID == id {
				h.Folders[idx].Name = name
				return
			}
		}
	})
}

func (a *App) desktopFolderPanel(c *ui.Context, d *desktopState, s *Session) {
	sh, h := &d.shell, a.desktopHost(s)
	a.desktopFolderConfirm(c, d, s, h)
	a.desktopLayoutConfirm(c, d, s)
	f, ok := desktopIconFolder(h, sh.folder)
	if !ok || sh.folder == "" {
		sh.folderRect = ui.Rect{}
		return
	}
	var items []desktopIconCell
	for _, icon := range a.desktopHostIcons(s, h) {
		if icon.Folder == f.ID {
			items = append(items, desktopIconCell{icon: icon})
		}
	}
	g := desktopMakeGrid(d.area, a.desktopPreferences().IconSize)
	r, ok := desktopFolderRect(d, sh, f.ID, g, len(items))
	if !ok {
		return
	}
	sh.folderRect = r
	panel := ui.Column(c.Key("folder-panel")).Absolute().Left(r.X).Top(r.Y).Size(r.W, r.H).ZIndex(3).
		Radius(13).Border(1, theme.Border).Shadow(0, 8, 30, 0, ui.RGBA(0, 0, 0, .4)).Background(theme.Background).Clip().Label(f.Name)
	if panel.PressedOutside() && sh.iconDrag == nil && sh.deleteFolder == "" {
		sh.folder = ""
		if sh.rename == f.ID {
			sh.rename = ""
		}
		c.Invalidate()
	}
	panel.Children(func() {
		header := ui.Row(c).Height(desktopFolderHeader).Shrink(0).PaddingX(12).Gap(5).AlignItems(ui.Center).Background(colors.sidebar).Cursor(ui.CursorMove)
		if dx, dy, dragging := header.Dragged(); dragging && (dx != 0 || dy != 0) && sh.rename != f.ID {
			sh.folderAt = desktopClampFolder(d, ui.Point{X: r.X + dx, Y: r.Y + dy}, r.W, r.H)
			if sh.folderMoved != f.ID {
				sh.folderSize = desktopSize{}
			}
			sh.folderMoved = f.ID
			c.Invalidate()
		}
		header.Children(func() {
			if sh.rename == f.ID {
				edit := ui.TextInput(c, &sh.renameText).Grow(1).MinWidth(0).Label(locales.Rename())
				if sh.renameFocus {
					edit.Focus()
					sh.renameFocus = false
				}
				if edit.Submitted() {
					a.desktopRenameFolder(d, s)
				}
				if edit.Shortcut(0, ui.KeyEscape) {
					sh.rename = ""
				}
			} else {
				ui.Text(c, f.Name).Bold().SingleLine().Grow(1).MinWidth(0)
			}
			if desktopFrameButton(c, "pencil", locales.Rename()).Clicked() {
				a.desktopStartRename(d, s, f.ID)
				c.Invalidate()
			}
			if desktopFrameButton(c, "trash-2", locales.DesktopShellDeleteFolder()).Clicked() {
				sh.deleteFolder = f.ID
				c.Invalidate()
			}
			if desktopFrameButton(c, "x", locales.Close()).Clicked() {
				sh.folder = ""
				c.Invalidate()
			}
		})
		if len(items) == 0 {
			ui.Box(c).Grow(1).Center().Padding(12).Children(func() { ui.Text(c, locales.DesktopShellEmptyFolder()).TextColor(theme.TextMuted) })
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
			ui.Row(c).Wrap().Padding(desktopFolderPad).Gap(desktopFolderGap).Children(func() {
				for _, item := range items {
					tile := ui.Column(c.Key(item.icon.App)).Size(g.spaceW, g.spaceH).Shrink(0).Background(theme.Surface)
					a.desktopIconView(c, d, s, h, tile, item.icon, g, items)
				}
			})
		})
		a.desktopFolderResize(c, d, f.ID, r) // after the icons, over them
	})
}

func (a *App) desktopFolderConfirm(c *ui.Context, d *desktopState, s *Session, h config.DesktopHost) {
	sh := &d.shell
	f, open := desktopIconFolder(h, sh.deleteFolder)
	if !open {
		return
	}
	ui.Modal(c.Key("delete-desktop-folder"), &open, func() {
		ui.Column(c).Width(340).Padding(16).Gap(12).Children(func() {
			ui.Text(c, locales.DesktopShellDeleteFolder()).Bold().FontSize(16)
			ui.Text(c, locales.DesktopShellDeleteFolderPrompt(f.Name))
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.Button(c, locales.Delete()).Clicked() {
					a.changeDesktopIcons(s, func(h *config.DesktopHost) {
						h.Folders = slices.DeleteFunc(h.Folders, func(folder config.DesktopFolder) bool { return folder.ID == f.ID })
						h.Icons = slices.DeleteFunc(h.Icons, func(icon config.DesktopIcon) bool { return icon.App == desktopFolderPrefix+f.ID })
						for idx := range h.Icons {
							if h.Icons[idx].Folder == f.ID {
								h.Icons[idx].Folder = ""
								h.Icons[idx].Col, h.Icons[idx].Row = -1, -1
							}
						}
					})
					if sh.folder == f.ID {
						sh.folder = ""
					}
					open = false
				}
			})
		})
	})
	if !open {
		sh.deleteFolder = ""
	}
}
