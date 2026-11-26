package app

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopFilesMarquee struct {
	active               bool
	startX, startY, x, y float32
	initial              map[string]bool
	boxes                map[string]ui.Rect
}

func (a *App) desktopFilesView(c *ui.Context, w *desktopWindow) {
	f, s := w.explorer(), w.session
	if f.client != s.sftp {
		f.home, f.mountsRead, f.drives = "", false, nil
		a.desktopFilesRefresh(w)
	}
	a.desktopFilesCheckEdits(w)
	width, height := w.w, w.h
	if d := a.desktopOf(s); w.maximized && d != nil && d.area.W > 0 {
		width, height = d.area.W, d.area.H
	}
	items := f.visible()
	chosen := f.chosen(items)
	ui.Column(c).Fill().MinWidth(0).MinHeight(0).Background(theme.Background).Children(func() {
		a.desktopFilesToolbar(c, w, chosen, width)
		a.desktopFilesAddress(c, w, width)
		if prompt := f.inline; prompt != nil {
			a.desktopFilesInline(c, w, prompt)
		}
		ui.Row(c).Grow(1).MinHeight(0).MinWidth(0).AlignItems(ui.Stretch).Children(func() {
			if f.sidebar {
				a.desktopFilesSidebar(c, w, width)
			}
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignSelf(ui.Stretch).Clip().Children(func() {
				a.desktopFilesBanners(c, w)
				if f.dir == "" && w.app.id == appThisPC || f.dir == "" && len(f.history) > 0 {
					a.desktopFilesPC(c, w, width)
				} else {
					a.desktopFilesContent(c, w, items, chosen, width)
				}
				a.desktopFilesBusy(c, w)
			})
		})
		a.desktopFilesStatus(c, w, items, chosen, width)
	})
	if d := a.desktopOf(s); d != nil && d.focused == w {
		a.desktopFilesDialog(c, w, height)
	}
}

func desktopFilesCommandButton(c *ui.Context, icon, title string, label, enabled bool) ui.Element {
	button := ui.Row(c.Key(icon + "|" + title)).Height(28).MinWidth(28).PaddingX(6).Gap(5).AlignItems(ui.Center).
		Justify(ui.Center).Shrink(0).Radius(6).Role(ui.RoleButton).Label(title).Tooltip(title).Disabled(!enabled)
	ink := theme.Text
	if !enabled {
		ink = theme.TextMuted.Alpha(.55)
	} else if button.Hovered() {
		button.Background(theme.Accent.Alpha(.11))
	}
	button.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(15).TextColor(ink).Shrink(0)
		if label {
			ui.Text(c, title).FontSize(12).SingleLine().TextColor(ink)
		}
	})
	return button
}

func desktopFilesMenuButton(c *ui.Context, icon, title string, label, enabled bool, menu func(*ui.Menu)) {
	desktopFilesCommandButton(c, icon, title, label, enabled).PopoverMenuButton(menu)
}

func desktopFilesRule(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Shrink(0).Background(colors.line)
}

func desktopFilesMenuAction(menu *ui.Menu, label string, enabled bool, action func()) {
	if menu.Item(label).Disabled(!enabled).Chosen() && enabled {
		action()
	}
}

func (a *App) desktopFilesToolbar(c *ui.Context, w *desktopWindow, chosen []desktopFilesItem, width float32) {
	f, s := w.explorer(), w.session
	ready := s.sftp != nil && !f.loading && f.job == nil && f.modal == nil && f.conflict == nil && f.inline == nil
	exec := desktopFilesRunner(s, f) != nil
	folder := ready && desktopFilesPath(f.dir)
	one, any := ready && len(chosen) == 1, ready && len(chosen) > 0
	wide := width >= 840
	ui.Row(c).FillWidth().Padding(4, 8).Gap(2).AlignItems(ui.Center).Wrap().Shrink(0).Background(colors.sidebar).Children(func() {
		desktopFilesMenuButton(c, "plus", locales.DesktopFilesNew(), wide, folder, func(menu *ui.Menu) { a.desktopFilesNewMenu(w, menu, folder) })
		if desktopFilesCommandButton(c, "cloud-download", locales.DesktopFilesRemoteDownload(), wide, folder && exec).Clicked() {
			a.desktopFilesFetch(w)
		}
		desktopFilesMenuButton(c, "upload", locales.DesktopFilesUpload(), wide, folder, func(menu *ui.Menu) { a.desktopFilesUploadMenu(w, menu, folder) })
		a.archiveModeButton(c)
		if desktopFilesCommandButton(c, "terminal", locales.DesktopFilesTerminal(), wide, folder).Clicked() {
			a.desktopOpenTerminalAt(s, f.dir)
		}
		ui.Box(c).Width(1).Height(18).MarginX(3).Background(colors.line)
		if desktopFilesCommandButton(c, "scissors", locales.DesktopFilesCut(), false, any).Clicked() {
			a.desktopFilesCopy(c, s, chosen, true)
		}
		if desktopFilesCommandButton(c, "copy", locales.Copy(), false, any).Clicked() {
			a.desktopFilesCopy(c, s, chosen, false)
		}
		if desktopFilesCommandButton(c, "clipboard-paste", locales.DesktopFilesPaste(), false, folder && (a.desktopFilesClipboard(c) != nil || desktopFilesHasLocalClipboard(c))).Clicked() {
			a.desktopFilesPaste(c, w)
		}
		if desktopFilesCommandButton(c, "pencil", locales.Rename(), false, one).Clicked() && one {
			a.desktopFilesRename(w, chosen[0])
		}
		if desktopFilesCommandButton(c, "trash-2", locales.Delete(), false, any).Clicked() {
			a.desktopFilesDelete(w, chosen, false)
		}
		ui.Box(c).Width(1).Height(18).MarginX(3).Background(colors.line)
		desktopFilesMenuButton(c, "arrow-down-up", locales.DesktopFilesSort(), wide, f.dir != "", func(menu *ui.Menu) { desktopFilesSortMenu(f, menu) })
		desktopFilesMenuButton(c, "layout-grid", locales.DesktopFilesViewMode(), wide, f.dir != "", func(menu *ui.Menu) { desktopFilesViewMenu(f, menu) })
		desktopFilesMenuButton(c, "ellipsis", locales.DesktopFilesActions(), false, true, func(menu *ui.Menu) {
			if any {
				a.desktopFilesItemMenu(c, w, menu, chosen)
			} else {
				a.desktopFilesBlankMenu(c, w, menu)
			}
		})
		ui.Box(c).Grow(1).MinWidth(0)
		a.downloadsButton(c)
	})
	desktopFilesRule(c)
}

func (a *App) desktopFilesNewMenu(w *desktopWindow, menu *ui.Menu, enabled bool) {
	desktopFilesMenuAction(menu, locales.NewFolder(), enabled, func() { a.desktopFilesNew(w, true) })
	desktopFilesMenuAction(menu, locales.DesktopFilesNewText(), enabled, func() { a.desktopFilesNew(w, false) })
}

func (a *App) desktopFilesUploadMenu(w *desktopWindow, menu *ui.Menu, enabled bool) {
	desktopFilesMenuAction(menu, locales.UploadFiles(), enabled, func() { a.desktopFilesUpload(w, nil, false) })
	desktopFilesMenuAction(menu, locales.UploadFolder(), enabled, func() { a.desktopFilesUpload(w, nil, true) })
}

func desktopFilesSortMenu(f *desktopFilesState, menu *ui.Menu) {
	for index, label := range []string{locales.ColName(), locales.ColSize(), locales.ColModified(), locales.DesktopFilesType()} {
		if menu.Item(label).Checked(f.sort == index).Chosen() {
			f.sort, f.row = index, -1
		}
	}
	menu.Separator()
	if menu.Item(locales.DesktopFilesAsc()).Checked(!f.descending).Chosen() {
		f.descending, f.row = false, -1
	}
	if menu.Item(locales.DesktopFilesDesc()).Checked(f.descending).Chosen() {
		f.descending, f.row = true, -1
	}
}

func desktopFilesViewMenu(f *desktopFilesState, menu *ui.Menu) {
	for index, label := range []string{locales.DesktopFilesDetails(), locales.DesktopFilesList(), locales.DesktopFilesMedium(), locales.DesktopFilesLarge()} {
		if menu.Item(label).Checked(f.view == index).Chosen() {
			f.view = index
		}
	}
}

func (a *App) desktopFilesAddress(c *ui.Context, w *desktopWindow, width float32) {
	f := w.explorer()
	exec := desktopFilesRunner(w.session, f) != nil
	ui.Row(c).FillWidth().Padding(6, 8).Gap(4).AlignItems(ui.Center).Shrink(0).Background(theme.Background).Children(func() {
		if desktopFilesCommandButton(c, "panels-top-left", locales.DesktopFilesToggleSidebar(), false, true).Clicked() {
			f.sidebar = !f.sidebar
		}
		if desktopFilesCommandButton(c, "arrow-left", locales.DesktopFilesBack(), false, f.historyAt > 0 && !f.loading).Clicked() {
			a.desktopFilesHistory(w, f.historyAt-1)
		}
		if desktopFilesCommandButton(c, "arrow-right", locales.DesktopFilesForward(), false, f.historyAt+1 < len(f.history) && !f.loading).Clicked() {
			a.desktopFilesHistory(w, f.historyAt+1)
		}
		desktopFilesMenuButton(c, "history", locales.DesktopFilesHistory(), false, len(f.history) > 0 && !f.loading, func(menu *ui.Menu) {
			menu.MaxWidth(400)
			for index := len(f.history) - 1; index >= max(0, len(f.history)-20); index-- {
				label := f.history[index]
				if label == "" {
					label = locales.DesktopAppThisPC()
				}
				if menu.Item(label).Checked(index == f.historyAt).Chosen() {
					a.desktopFilesHistory(w, index)
				}
			}
		})
		if desktopFilesCommandButton(c, "arrow-up", locales.ParentFolder(), false, f.dir != "" && !f.loading).Clicked() {
			a.desktopFilesUp(w)
		}
		if f.addressEdit {
			input := ui.TextInput(c, &f.address).AutoFocus().Placeholder(locales.DesktopFilesAddress()).Grow(1).MinWidth(0)
			if input.Submitted() {
				desktopFilesAddressSubmit(a, w)
			} else if input.Shortcut(0, ui.KeyEscape) || input.FocusLeft() {
				f.addressEdit = false
			}
		} else {
			a.desktopFilesBreadcrumb(c, w, width)
		}
		if desktopFilesCommandButton(c, "refresh-cw", locales.Refresh(), false, !f.loading && w.session.sftp != nil).Clicked() {
			a.desktopFilesRefresh(w)
		}
		if f.dir != "" {
			hint := locales.DesktopFilesSearch()
			if width < 790 {
				hint = locales.DesktopFilesSearchShort()
			}
			field := ui.TextInput(c, &f.query).Width(min(190, width*.25)).MinWidth(75).Placeholder(hint).Tooltip(locales.DesktopFilesSearchHint())
			if field.Changed() {
				f.row = -1
				if f.deep != "" {
					a.desktopFilesRead(w, f.dir, false, false)
				}
			}
			if field.Submitted() && !f.loading && exec {
				a.desktopFilesSearch(w, f.query)
			}
			if desktopFilesCommandButton(c, "search", locales.DesktopFilesSearchDeep(), false, exec && !f.loading && strings.TrimSpace(f.query) != "").Clicked() {
				a.desktopFilesSearch(w, f.query)
			}
		}
	})
	desktopFilesRule(c)
}

func desktopFilesAddressSubmit(a *App, w *desktopWindow) {
	f := w.explorer()
	address := f.address
	if address == "~" {
		address = f.home
	} else if tail, ok := strings.CutPrefix(address, "~/"); ok && desktopFilesPath(f.home) {
		address = path.Join(f.home, tail)
	}
	if !desktopFilesPath(address) {
		f.err = errors.New(locales.DesktopFilesBadPath())
		return
	}
	f.addressEdit = false
	a.desktopFilesRead(w, address, false, true)
}

func (a *App) desktopFilesUp(w *desktopWindow) {
	if dir := w.explorer().dir; dir != "" {
		parent := path.Dir(dir)
		if parent == dir {
			parent = ""
		}
		a.desktopFilesRead(w, parent, false, true)
	}
}

func (a *App) desktopFilesBreadcrumb(c *ui.Context, w *desktopWindow, width float32) {
	f := w.explorer()
	bar := ui.Row(c).Grow(1).MinWidth(0).Height(28).AlignItems(ui.Center).PaddingX(4).Gap(2).Clip().Radius(6).Border(1, colors.line).Background(colors.note)
	if bar.Clicked() {
		f.address, f.addressEdit = f.dir, true
	}
	bar.Children(func() {
		if desktopFilesCommandButton(c, "monitor", locales.DesktopAppThisPC(), false, !f.loading).Clicked() {
			a.desktopFilesRead(w, "", false, true)
		}
		if f.dir == "" {
			ui.Text(c, locales.DesktopAppThisPC()).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
			return
		}
		parts := strings.Split(strings.TrimPrefix(f.dir, "/"), "/")
		paths := []string{"/"}
		for _, part := range parts {
			if part != "" {
				paths = append(paths, path.Join(paths[len(paths)-1], part))
			}
		}
		show := 3
		if width < 780 {
			show = 1
		}
		if len(paths) > show {
			hidden := paths[:len(paths)-show]
			desktopFilesMenuButton(c, "ellipsis", locales.DesktopFilesAncestors(), false, !f.loading, func(menu *ui.Menu) {
				menu.MaxWidth(400)
				for _, target := range hidden {
					desktopFilesMenuAction(menu, target, !f.loading, func() { a.desktopFilesRead(w, target, false, true) })
				}
			})
			paths = paths[len(paths)-show:]
		}
		for index, target := range paths {
			ui.Icon(c, icons.Get("chevron-right")).FontSize(11).TextColor(theme.TextMuted).Shrink(0)
			crumb := ui.Row(c.Key(target)).PaddingX(3).MaxWidth(100).Shrink(1).MinWidth(0).Role(ui.RoleButton).Tooltip(target)
			if crumb.Clicked() && !f.loading {
				a.desktopFilesRead(w, target, false, true)
			}
			crumb.Children(func() {
				text := ui.Text(c, path.Base(target)).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
				if index == len(paths)-1 {
					text.Bold()
				}
			})
		}
		spacer := ui.Box(c).Grow(1).MinWidth(6).FillHeight().Role(ui.RoleButton).Label(locales.DesktopFilesAddress()).Tooltip(locales.DesktopFilesAddress())
		if spacer.Clicked() {
			f.address, f.addressEdit = f.dir, true
		}
	})
}

func (a *App) desktopFilesBanners(c *ui.Context, w *desktopWindow) {
	f := w.explorer()
	if f.err != nil {
		ui.Row(c).Padding(4, 10).Gap(5).AlignItems(ui.Center).Background(colors.dangerBanner).Shrink(0).Children(func() {
			label := fileErrorText(f.err)
			ui.Text(c, label).Grow(1).MinWidth(0).SingleLine().Tooltip(label).TextColor(theme.Danger)
			if widgets.IconButton(c, "x", locales.Close()).Clicked() {
				f.err = nil
			}
		})
	}
}

// desktopFilesBusyDelay is how long reading or working goes on before the
// window says so: a quick read shows nothing, rather than a flash.
const desktopFilesBusyDelay = 300 * time.Millisecond

// desktopFilesBusy says what the window is reading or doing, with a
// spinner and a way to stop it, on a card over the bottom right of the
// list: the list does not move as it comes and goes, so that a click or
// a double click lands where it was aimed.
func (a *App) desktopFilesBusy(c *ui.Context, w *desktopWindow) {
	f := w.explorer()
	if f.job == nil && !f.loading {
		f.busySince = time.Time{}
		return
	}
	now := c.Now()
	if f.busySince.IsZero() {
		f.busySince = now
		time.AfterFunc(desktopFilesBusyDelay, func() { a.post(func() {}) })
	}
	if now.Sub(f.busySince) < desktopFilesBusyDelay {
		return
	}
	label := locales.DesktopFilesLoading()
	var stop func()
	if f.job != nil {
		label, stop = f.job.label, f.job.cancel
		if f.job.progress != "" {
			label = label + " · " + f.job.progress
		}
	} else if f.readCancel != nil && f.searching {
		stop = f.cancelSearch
	}
	ui.Row(c.Key("busy")).Absolute().Right(14).Bottom(14).MaxWidth(420).MaxWidthPercent(80).Gap(8).Padding(6, 6, 6, 12).
		AlignItems(ui.Center).Radius(10).Background(theme.Background).Border(1, theme.Border).
		Shadow(0, 6, 18, 0, ui.RGBA(8, 11, 28, 0.18)).Role(ui.RoleStatus).Label(label).Children(func() {
		ui.Spinner(c).Size(14, 14).Shrink(0)
		ui.Text(c, label).FontSize(12).SingleLine().MinWidth(0).Shrink(1).Tooltip(label)
		if stop != nil && ui.Button(c, locales.Cancel()).Clicked() {
			stop()
		}
	})
}

func (a *App) desktopFilesStatus(c *ui.Context, w *desktopWindow, items, chosen []desktopFilesItem, width float32) {
	f := w.explorer()
	status := locales.DesktopFilesCount(len(items))
	if f.dir == "" {
		status = locales.DesktopPCDriveCount(len(f.drives))
	}
	if f.limited {
		status = locales.DesktopFilesResultLimit(fmt.Sprint(desktopFilesSearchLimit))
	}
	if len(chosen) > 0 {
		var size int64
		for _, item := range chosen {
			if !item.IsDir() {
				size += item.Size
			}
		}
		status += "  ·  " + locales.DesktopFilesSelected(len(chosen), formatSize(size))
	}
	free := f.free
	desktopFilesRule(c)
	ui.Row(c).FillWidth().Height(28).PaddingX(11).Gap(10).AlignItems(ui.Center).Shrink(0).Background(colors.sidebar).Children(func() {
		ui.Text(c, status).FontSize(11).TextColor(theme.TextMuted).SingleLine().Tooltip(status).Grow(1).MinWidth(0)
		if f.dir != "" && !f.freeKnown {
			longest := 0
			for _, drive := range f.drives {
				if len(drive.mount) > longest && (drive.mount == "/" || within(f.dir, drive.mount)) {
					free, longest = drive.free, len(drive.mount)
				}
			}
		}
		if f.dir != "" && free >= 0 {
			ui.Text(c, locales.DesktopFilesFree(formatSize(free))).FontSize(11).TextColor(theme.TextMuted).SingleLine().Shrink(0)
		}
	})
}

type desktopFilesFavoriteDrag struct {
	window *desktopWindow
	target string
}

func (a *App) desktopFilesSidebar(c *ui.Context, w *desktopWindow, width float32) {
	f, s := w.explorer(), w.session
	data := a.desktopFilesHost(s)
	ui.Scroll(c).Width(min(170, width*.22)).Shrink(0).MinHeight(0).AlignSelf(ui.Stretch).Clip().Padding(9, 7).Background(colors.sidebar).Children(func() {
		ui.Column(c).Gap(10).FillWidth().Children(func() {
			a.desktopFilesSidebarSection(c, w, 0, "history", locales.DesktopFilesRecent(), func() {
				for _, target := range data.Recent {
					a.desktopFilesPlace(c, w, target, "history", false)
				}
			})
			a.desktopFilesSidebarSection(c, w, 1, "star", locales.DesktopFilesFavorites(), func() {
				for _, target := range data.Favorites {
					a.desktopFilesPlace(c, w, target, "star", true)
				}
			})
			desktopFilesRule(c)
			a.desktopFilesSidebarSection(c, w, 2, "monitor", locales.DesktopAppThisPC(), func() {
				a.desktopFilesPlace(c, w, "", "monitor", false)
				for _, disk := range f.drives {
					a.desktopFilesPlace(c, w, disk.mount, "hard-drive", false)
				}
			})
		})
	})
	ui.Box(c).Width(1).Shrink(0).FillHeight().Background(colors.line)
}

func (a *App) desktopFilesSidebarSection(c *ui.Context, w *desktopWindow, index int, icon, label string, content func()) {
	f := w.explorer()
	ui.Column(c.Key(index)).Gap(2).FillWidth().Children(func() {
		row := ui.Row(c).FillWidth().Height(26).PaddingX(3).Gap(5).AlignItems(ui.Center).Radius(5).Role(ui.RoleButton).Label(label)
		if row.Hovered() {
			row.Background(theme.Accent.Alpha(.10))
		}
		if row.Clicked() {
			f.collapsed[index] = !f.collapsed[index]
		}
		row.Children(func() {
			chevron := "chevron-down"
			if f.collapsed[index] {
				chevron = "chevron-right"
			}
			ui.Icon(c, icons.Get(chevron)).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
			ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(theme.Accent).Shrink(0)
			ui.Text(c, label).FontSize(11.5).Bold().SingleLine().MinWidth(0).Shrink(1)
		})
		if !f.collapsed[index] {
			content()
		}
	})
}

func (a *App) desktopFilesPlace(c *ui.Context, w *desktopWindow, target, icon string, favorite bool) {
	f := w.explorer()
	label := path.Base(target)
	switch target {
	case "":
		label = locales.DesktopPCOverview()
	case "/":
		label = locales.DesktopPCSystemDisk()
	}
	row := ui.Row(c.Key(target)).FillWidth().Height(28).Padding(0, 4, 0, 20).Gap(6).AlignItems(ui.Center).Radius(5).Role(ui.RoleButton).Tooltip(target)
	if f.dir == target && icon != "history" || row.Hovered() {
		row.Background(theme.Accent.Alpha(.13))
	}
	if row.Clicked() && !f.loading {
		if target == "" || icon == "hard-drive" {
			a.desktopFilesRead(w, target, false, true)
		} else {
			a.desktopFilesOpenPlace(w, target)
		}
	}
	if favorite {
		row.Drag(desktopFilesFavoriteDrag{w, target})
		if drag, ok := ui.DragOver[desktopFilesFavoriteDrag](row); ok && drag.window == w && drag.target != target {
			row.Background(theme.Accent.Alpha(.20)).Border(1, theme.Accent)
		}
		if drag, ok := ui.Drop[desktopFilesFavoriteDrag](row); ok && drag.window == w && drag.target != target {
			a.desktopFilesChangeHost(w.session, func(host *config.DesktopHost) {
				from, to := slices.Index(host.Favorites, drag.target), slices.Index(host.Favorites, target)
				if from >= 0 && to >= 0 {
					host.Favorites = slices.Delete(host.Favorites, from, from+1)
					host.Favorites = slices.Insert(host.Favorites, to, drag.target)
				}
			})
		}
		row.ContextMenu(func(menu *ui.Menu) {
			desktopFilesMenuAction(menu, locales.DesktopFilesRemoveFavorite(), true, func() { a.desktopFilesFavorite(w.session, target, false) })
		})
	}
	row.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(theme.TextMuted).Shrink(0)
		ui.Text(c, label).FontSize(12).SingleLine().Grow(1).MinWidth(0)
	})
}

func (a *App) desktopFilesPC(c *ui.Context, w *desktopWindow, width float32) {
	f, s := w.explorer(), w.session
	compact := width < 750
	tileW, tileH, iconSize, driveW := float32(93), float32(75), float32(32), float32(244)
	if compact {
		tileW, tileH, iconSize, driveW = 85, 51, 24, 207
	}
	ui.Scroll(c).Grow(1).MinHeight(0).MinWidth(0).Clip().Padding(12, 15).Children(func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, locales.DesktopPCQuick()).FontSize(15).Bold()
			quick := slices.Clone(f.quick)
			for _, target := range a.desktopFilesHost(s).Favorites {
				if !slices.Contains(quick, target) {
					quick = append(quick, target)
				}
			}
			if len(quick) == 0 && !f.loading {
				widgets.Muted(c, locales.DesktopPCNoQuick()).FontSize(12)
			}
			ui.Row(c).FillWidth().Wrap().Gap(7).Children(func() {
				for _, target := range quick {
					label := path.Base(target)
					if target == f.home {
						label = locales.HomeFolder()
					}
					tile := ui.Column(c.Key(target)).Width(tileW).Height(tileH).Gap(4).Padding(5).Center().Radius(8).
						Role(ui.RoleButton).Label(label).Tooltip(target).Background(colors.note)
					if tile.Hovered() {
						tile.Background(theme.Accent.Alpha(.13))
					}
					if tile.DoubleClicked() || tile.Shortcut(0, ui.KeyEnter) {
						if slices.Contains(f.quick, target) {
							a.desktopOpen(s, appFiles, target)
						} else {
							a.desktopFilesOpenPlace(w, target)
						}
					}
					tile.ContextMenu(func(menu *ui.Menu) {
						desktopFilesMenuAction(menu, locales.OpenFile(), true, func() { a.desktopFilesOpenPlace(w, target) })
					})
					tile.Focusable().Children(func() {
						a.desktopFilesIcon(c, path.Base(target), true, iconSize)
						ui.Text(c, label).FontSize(11.5).SingleLine().MaxWidth(tileW - 10)
					})
				}
			})
			ui.Text(c, locales.DesktopPCDrives()).FontSize(15).Bold().Margin(5, 0, 0, 0)
			if f.driveErr != nil {
				widgets.Muted(c, f.driveErr.Error()).SingleLine().Tooltip(f.driveErr.Error())
			} else if len(f.drives) == 0 && !f.loading {
				widgets.Muted(c, locales.DesktopPCNoDrives()).FontSize(12)
			}
			ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
				for _, disk := range f.drives {
					a.desktopFilesDriveTile(c, s, disk, driveW)
				}
			})
		})
	})
}

func (a *App) desktopFilesDriveTile(c *ui.Context, s *Session, disk desktopFilesDrive, width float32) {
	name := disk.device
	if disk.mount == "/" {
		name = locales.DesktopPCSystemDisk()
	}
	details := disk.mount + " · " + disk.kind
	tile := ui.Row(c.Key(disk.mount)).Width(width).Height(86).Padding(10).Gap(9).AlignItems(ui.Center).Radius(9).Background(colors.note).
		Border(1, colors.line).Role(ui.RoleButton).Label(name).Tooltip(details).Focusable()
	if tile.Hovered() {
		tile.Background(theme.Accent.Alpha(.10))
	}
	if tile.DoubleClicked() || tile.Shortcut(0, ui.KeyEnter) {
		a.desktopOpen(s, appFiles, disk.mount)
	}
	tile.ContextMenu(func(menu *ui.Menu) {
		desktopFilesMenuAction(menu, locales.OpenFile(), true, func() { a.desktopOpen(s, appFiles, disk.mount) })
	})
	tile.Children(func() {
		ui.Box(c).Size(36, 36).Shrink(0).Radius(9).Center().Background(ui.Hex("#4f7cff").Alpha(.16)).Children(func() {
			ui.Icon(c, icons.Get("hard-drive")).FontSize(21).TextColor(theme.Accent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, name).Bold().SingleLine().Tooltip(disk.device)
			widgets.Muted(c, details).FontSize(11).SingleLine()
			usageBar(c, float64(disk.used)/100)
			free := locales.DesktopPCDriveSpace(formatSize(disk.free), formatSize(disk.total))
			widgets.Muted(c, free).FontSize(11).SingleLine().Tooltip(free)
		})
	})
}

func (a *App) desktopFilesIcon(c *ui.Context, name string, folder bool, size float32) {
	icon := a.iconOf(name, folder, false, false, theme.Dark)
	ui.Box(c).Size(size, size).Shrink(0).Center().Children(func() {
		switch {
		case icon != nil && icon.Image != nil:
			ui.Image(c, icon.Image).Size(size, size)
		case icon != nil:
			glyph := ui.Text(c, string(icon.Glyph)).Font(icon.Font).FontSize(size * .85 * icon.Scale).LineHeight(1).SingleLine()
			if icon.HasColor {
				glyph.TextColor(icon.Color)
			}
		case folder:
			ui.Icon(c, icons.Get("folder")).FontSize(size).TextColor(folderColor)
		default:
			glyph := "file-text"
			if desktopFilesArchiveKind(name) != "" {
				glyph = "file-archive"
			}
			ui.Icon(c, icons.Get(glyph)).FontSize(size).TextColor(theme.Accent)
		}
	})
}

func (a *App) desktopFilesContent(c *ui.Context, w *desktopWindow, items, chosen []desktopFilesItem, width float32) {
	f, s := w.explorer(), w.session
	zone := ui.Box(c).Grow(1).MinHeight(0).MinWidth(0).Clip().Focusable()
	if paths := zone.DroppedFiles(); len(paths) > 0 {
		a.desktopFilesUpload(w, paths, false)
	}
	if zone.FileDragOver() && desktopFilesPath(f.dir) {
		zone.Border(2, theme.Accent)
	}
	zone.Children(func() {
		f.marquee.boxes = map[string]ui.Rect{}
		if len(items) == 0 && !f.loading && f.err == nil {
			ui.Column(c).Absolute().Top(30).Left(0).Right(0).Center().Gap(8).Children(func() {
				ui.Icon(c, icons.Get("folder-open")).FontSize(33).TextColor(theme.TextMuted.Alpha(.65))
				label := locales.DesktopFilesEmpty()
				if f.query != "" {
					label = locales.DesktopFilesNoMatches()
				}
				widgets.Muted(c, label).SingleLine()
			})
		}
		if f.view <= desktopFilesList || f.deep != "" {
			a.desktopFilesTable(c, w, items, width)
		} else {
			a.desktopFilesGrid(c, w, items)
		}
		canvas := ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).ZIndex(5)
		canvasY := canvas.Bounds().Y
		canvas.HitTest(func(_, y float32, rect ui.Rect) bool {
			bottom := canvasY
			for _, cell := range f.marquee.boxes {
				bottom = max(bottom, cell.Y+cell.H)
			}
			return len(items) == 0 || bottom > canvasY && y >= rect.Y+bottom-canvasY+2
		})
		canvas.ContextMenu(func(menu *ui.Menu) { a.desktopFilesBlankMenu(c, w, menu) })
		if canvas.Clicked() {
			f.selected.Clear()
			f.row = -1
		}
		a.desktopFilesMarquee(c, canvas, f, items)
	})
	if d := a.desktopOf(s); d != nil && d.focused == w && f.modal == nil && f.inline == nil && f.conflict == nil && !c.FocusTakesText() {
		a.desktopFilesKeys(c, w, zone, items, chosen)
	}
}

func (a *App) desktopFilesTable(c *ui.Context, w *desktopWindow, items []desktopFilesItem, width float32) {
	f := w.explorer()
	columns := []ui.TableColumn{{Title: locales.ColName(), ID: "name", MinWidth: 125}}
	if f.deep != "" {
		columns = append(columns, ui.TableColumn{Title: locales.DesktopFilesAddress(), ID: "path", MinWidth: 160})
	} else if f.view == desktopFilesDetails {
		columns = append(columns,
			ui.TableColumn{Title: locales.ColSize(), ID: "size", Width: 73},
			ui.TableColumn{Title: locales.ColModified(), ID: "time", Width: 128},
			ui.TableColumn{Title: locales.ColPermissions(), ID: "perm", Width: 96},
			ui.TableColumn{Title: locales.DesktopFilesOwnerGroup(), ID: "owner", Width: 126},
			ui.TableColumn{Title: locales.DesktopFilesNote(), ID: "note", MinWidth: 82})
		if f.sidebar && width < 810 || width < 660 {
			columns[0].MinWidth = 112
			columns[1].Width = 68
			columns[2].Width = 86
			columns[3].Width, columns[3].Title = 81, locales.DesktopFilesMode()
			columns[4].Width, columns[4].Title = 74, locales.DesktopFilesOwner()
			columns[5].Width = 38
		}
	}
	f.list.Selected, f.list.Selection = &f.row, &f.selected
	f.list.Key = func(index int) any { return items[index].target }
	f.list.Label = func(index int) string { return items[index].Name }
	f.list.CellMenu = func(menu *ui.Menu, row, col int) { a.desktopFilesItemTarget(c, w, menu, items, row) }
	notes := a.desktopFilesHost(w.session).Notes
	table := ui.Table(c, &f.list, columns, len(items), func(row, col int) {
		item := items[row]
		note := notes[item.target]
		cell := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).MinWidth(0)
		if col == 0 {
			f.marquee.boxes[item.target] = cell.Bounds()
			if note != "" {
				cell.Tooltip(note)
			} else {
				cell.Tooltip(item.target)
			}
		}
		cell.Children(func() {
			label := ""
			switch columns[col].ID {
			case "name":
				a.entryIcon(c, item.Entry)
				label = item.Name
			case "path":
				label = item.target
			case "size":
				if !item.IsDir() && !item.Broken {
					label = formatSize(item.Size)
					cell.Tooltip(label)
				}
			case "time":
				label = item.ModTime.Format("2006-01-02 15:04")
				if columns[col].Width < 100 {
					label = item.ModTime.Format("01-02 15:04")
				}
				cell.Tooltip(item.ModTime.Format("2006-01-02 15:04:05"))
			case "perm":
				label = desktopFilesPermissionText(item.Mode)
				cell.Tooltip(label)
			case "owner":
				label = fmt.Sprintf("%d:%d", item.UID, item.GID)
				cell.Tooltip(label)
			case "note":
				label = note
				cell.Tooltip(note)
			}
			text := ui.Text(c, label).FontSize(12).SingleLine().Grow(1).MinWidth(0)
			if columns[col].ID == "perm" {
				text.Font("monospace")
			} else if columns[col].ID == "owner" && columns[col].Width < 80 {
				text.FontSize(11)
			}
		})
	}).Grow(1).FillWidth().MinHeight(0)
	if table.Submitted() {
		a.desktopFilesSubmit(w, items)
	}
}

func (a *App) desktopFilesGrid(c *ui.Context, w *desktopWindow, items []desktopFilesItem) {
	f := w.explorer()
	f.grid.Selected, f.grid.Selection = &f.row, &f.selected
	f.grid.Key = func(index int) any { return items[index].target }
	f.grid.Label = func(index int) string { return items[index].Name }
	width, height, icon := float32(92), float32(75), float32(30)
	if f.view == desktopFilesLarge {
		width, height, icon = 122, 104, 47
	}
	notes := a.desktopFilesHost(w.session).Notes
	grid := ui.GridView(c, &f.grid, len(items), width, height, func(index int) {
		item := items[index]
		note := notes[item.target]
		cell := ui.Column(c).Grow(1).FillWidth().MinHeight(0).Center().Gap(5).Padding(4).Clip().Radius(7)
		f.marquee.boxes[item.target] = cell.Bounds()
		if note != "" {
			cell.Tooltip(item.target + "\n" + note)
		} else {
			cell.Tooltip(item.target)
		}
		if cell.Hovered() && !f.selected.Has(item.target) {
			cell.Background(theme.Accent.Alpha(.09))
		}
		cell.ContextMenu(func(menu *ui.Menu) { a.desktopFilesItemTarget(c, w, menu, items, index) })
		cell.Children(func() {
			a.desktopFilesIcon(c, item.Name, item.IsDir(), icon)
			ui.Text(c, item.Name).FontSize(12).SingleLine().MaxWidth(width - 8)
			if note != "" {
				ui.Box(c).Absolute().Top(5).Right(5).Size(17, 17).Radius(5).Center().Background(colors.note).Children(func() {
					ui.Icon(c, icons.Get("sticky-note")).FontSize(12).TextColor(theme.Accent)
				})
			}
		})
	}).Grow(1).FillWidth().MinHeight(0).Clip().PaddingX(6)
	if grid.Submitted() {
		a.desktopFilesSubmit(w, items)
	}
}

func (a *App) desktopFilesItemTarget(c *ui.Context, w *desktopWindow, menu *ui.Menu, items []desktopFilesItem, index int) {
	f := w.explorer()
	if target := items[index].target; !f.selected.Has(target) {
		f.selected.Clear()
		f.selected.Add(target)
		f.row = index
	}
	a.desktopFilesItemMenu(c, w, menu, f.chosen(items))
}

func (a *App) desktopFilesSubmit(w *desktopWindow, items []desktopFilesItem) {
	f := w.explorer()
	if f.loading || f.job != nil || f.modal != nil || f.conflict != nil {
		return
	}
	if f.row >= 0 && f.row < len(items) && f.selected.Has(items[f.row].target) {
		a.desktopFilesOpenItem(w, items[f.row], false)
		return
	}
	if chosen := f.chosen(items); len(chosen) == 1 {
		a.desktopFilesOpenItem(w, chosen[0], false)
	}
}

func (a *App) desktopFilesMarquee(c *ui.Context, surface ui.Element, f *desktopFilesState, items []desktopFilesItem) {
	m := &f.marquee
	px, py, _ := surface.PointerPosition()
	bounds := surface.Bounds()
	if _, _, pressed := surface.Dragged(); pressed {
		if !m.active {
			m.active, m.startX, m.startY = true, px, py
			m.initial = map[string]bool{}
			if c.Modifiers()&(ui.Ctrl|ui.Cmd|ui.Shift) != 0 {
				for _, item := range f.chosen(items) {
					m.initial[item.target] = true
				}
			}
		}
		m.x, m.y = min(max(px, 0), bounds.W), min(max(py, 0), bounds.H)
		left, top := min(m.startX, m.x), min(m.startY, m.y)
		right, bottom := max(m.startX, m.x), max(m.startY, m.y)
		if right-left >= 3 || bottom-top >= 3 {
			f.selected.Clear()
			f.row = -1
			for target := range m.initial {
				f.selected.Add(target)
			}
			for target, box := range m.boxes {
				if box.W > 0 && box.H > 0 && box.X < bounds.X+right && box.X+box.W > bounds.X+left && box.Y < bounds.Y+bottom && box.Y+box.H > bounds.Y+top {
					f.selected.Add(target)
				}
			}
			ui.Box(c).Absolute().Left(left).Top(top).Size(right-left, bottom-top).ZIndex(100).PassThrough().
				Border(1, theme.Accent).Background(theme.Accent.Alpha(.14)).Radius(3)
		}
	} else if m.active {
		m.active = false
	}
}

func (a *App) desktopFilesKeys(c *ui.Context, w *desktopWindow, zone ui.Element, items, chosen []desktopFilesItem) {
	f := w.explorer()
	key := func(modifiers ui.Modifiers, key ui.Key) bool {
		return zone.Shortcut(modifiers, key) || zone.FocusWithin() && c.Shortcut(modifiers, key)
	}
	if key(0, ui.KeyF5) && !f.loading {
		a.desktopFilesRefresh(w)
	}
	if f.loading || f.job != nil {
		return
	}
	switch {
	case key(0, ui.KeyBackspace):
		a.desktopFilesUp(w)
	case key(ui.Alt, ui.KeyLeft):
		a.desktopFilesHistory(w, f.historyAt-1)
	case key(ui.Alt, ui.KeyRight):
		a.desktopFilesHistory(w, f.historyAt+1)
	case key(0, ui.KeyF2) && len(chosen) == 1:
		a.desktopFilesRename(w, chosen[0])
	case key(ui.Shift, ui.KeyDelete):
		a.desktopFilesDelete(w, chosen, true)
	case key(0, ui.KeyDelete):
		a.desktopFilesDelete(w, chosen, false)
	case key(ui.Cmd, ui.KeyC) || key(ui.Ctrl, ui.KeyC):
		a.desktopFilesCopy(c, w.session, chosen, false)
	case key(ui.Cmd, ui.KeyX) || key(ui.Ctrl, ui.KeyX):
		a.desktopFilesCopy(c, w.session, chosen, true)
	case key(ui.Cmd, ui.KeyV) || key(ui.Ctrl, ui.KeyV):
		a.desktopFilesPaste(c, w)
	case key(ui.Cmd, ui.KeyA) || key(ui.Ctrl, ui.KeyA):
		for _, item := range items {
			f.selected.Add(item.target)
		}
	case zone.Focused() && key(0, ui.KeyEnter):
		a.desktopFilesSubmit(w, items)
	}
}
