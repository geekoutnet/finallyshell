package app

import (
	"maps"
	"slices"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The desktop's layouts: the shared one, which every host without a layout
// of its own uses (config.DesktopSharedLayout), and each host's own. The
// default layout keeps a few apps on the desktop and groups the others
// into preset folders, which the user can flatten, tidy up again, rename
// or delete like folders of their own.

// desktopPresetFolders are the default layout's folders, in order.
var desktopPresetFolders = []struct {
	id   string
	name func() string
}{
	{"preset-system", locales.DesktopFolderSystem},
	{"preset-network", locales.DesktopFolderNetwork},
	{"preset-apps", locales.DesktopFolderApps},
	{"preset-transfer", locales.DesktopFolderTransfer},
}

// desktopPresetApps puts each app in a preset folder; an app not listed is
// in none and stays on the desktop, unless its category is the server's
// tools, which go with the system tools.
var desktopPresetApps = map[desktopAppID]string{
	appSettings: "preset-system", appTaskMgr: "preset-system",
	"srv-overview": "preset-system", "srv-processes": "preset-system", "srv-services": "preset-system",
	"srv-logs": "preset-system", "srv-cron": "preset-system", "srv-disk": "preset-system", "srv-updates": "preset-system",

	appBrowser: "preset-network", appCerts: "preset-network",
	"srv-network": "preset-network", "srv-firewall": "preset-network", "srv-security": "preset-network", "srv-tunnels": "preset-network",

	"srv-docker": "preset-apps", "srv-db": "preset-apps", "srv-sites": "preset-apps", "srv-caddy": "preset-apps", appOpenWrt: "preset-apps",

	"srv-transfers": "preset-transfer", "srv-commands": "preset-transfer",
}

// desktopTopApps stay on the desktop whatever their category.
var desktopTopApps = []desktopAppID{appThisPC, appFiles, appTerminal, appTrash, appAppCenter, appAI}

func desktopPresetOf(app *desktopApp) string {
	if folder, ok := desktopPresetApps[app.id]; ok {
		return folder
	}
	if app.category == desktopCatServer && !slices.Contains(desktopTopApps, app.id) {
		return "preset-system"
	}
	return ""
}

func desktopIsPreset(id string) bool {
	return slices.ContainsFunc(desktopPresetFolders, func(f struct {
		id   string
		name func() string
	}) bool {
		return f.id == id
	})
}

func desktopPresetFolderList() []config.DesktopFolder {
	out := make([]config.DesktopFolder, 0, len(desktopPresetFolders))
	for _, f := range desktopPresetFolders {
		out = append(out, config.DesktopFolder{ID: f.id, Name: f.name()})
	}
	return out
}

// desktopDefaultIcons are the default layout's icons for s: the apps
// shown by default, the preset folders' after the desktop's own.
func desktopDefaultIcons(s *Session) []config.DesktopIcon {
	var out, inside []config.DesktopIcon
	for _, app := range desktopAppsFor(s) {
		if !app.onDesktop {
			continue
		}
		if folder := desktopPresetOf(app); folder != "" {
			inside = append(inside, config.DesktopIcon{App: string(app.id), Col: -1, Row: -1, Folder: folder})
		} else {
			out = append(out, config.DesktopIcon{App: string(app.id), Col: -1, Row: -1})
		}
	}
	for _, f := range desktopPresetFolders {
		out = append(out, config.DesktopIcon{App: desktopFolderPrefix + f.id, Col: -1, Row: -1})
	}
	return append(out, inside...)
}

// desktopHostFolders are h's folders: the preset ones while h has the
// default layout.
func desktopHostFolders(h config.DesktopHost) []config.DesktopFolder {
	if h.IconsConfigured || h.Icons != nil {
		return h.Folders
	}
	return desktopPresetFolderList()
}

// desktopLayoutFields copies the layout of from into to: its icons,
// folders and arrangement, nothing else.
func desktopLayoutFields(to *config.DesktopHost, from config.DesktopHost) {
	to.Icons, to.IconsConfigured, to.AutoArrange, to.Folders = from.Icons, from.IconsConfigured, from.AutoArrange, from.Folders
}

// desktopHostOf is the desktop of the host id in prefs, with the shared
// layout when it has none of its own.
func desktopHostOf(prefs config.DesktopSettings, id string) config.DesktopHost {
	h := prefs.Hosts[id]
	if !h.HasOwnLayout() {
		desktopLayoutFields(&h, prefs.Hosts[config.DesktopSharedLayout])
	}
	return h
}

func (a *App) desktopHost(s *Session) config.DesktopHost {
	return desktopHostOf(a.desktopPreferences(), s.Host.ID)
}

// desktopOwnLayout reports whether s's host has a layout of its own.
func (a *App) desktopOwnLayout(s *Session) bool {
	return a.desktopPreferences().Hosts[s.Host.ID].HasOwnLayout()
}

// changeDesktopHost changes the desktop of s's host: a change to its
// layout goes to the shared layout while the host uses it.
func (a *App) changeDesktopHost(s *Session, change func(*config.DesktopHost)) {
	a.changeDesktopPreferences(func(prefs *config.DesktopSettings) {
		shared := !prefs.Hosts[s.Host.ID].HasOwnLayout()
		host := desktopHostOf(*prefs, s.Host.ID)
		host.Icons = slices.Clone(host.Icons)
		host.Folders = slices.Clone(host.Folders)
		change(&host)
		prefs.Hosts = maps.Clone(prefs.Hosts)
		if prefs.Hosts == nil {
			prefs.Hosts = make(map[string]config.DesktopHost)
		}
		if shared {
			layout := prefs.Hosts[config.DesktopSharedLayout]
			desktopLayoutFields(&layout, host)
			desktopSetHost(prefs, config.DesktopSharedLayout, layout)
			desktopLayoutFields(&host, config.DesktopHost{})
		}
		desktopSetHost(prefs, s.Host.ID, host)
	})
}

// desktopSetHost stores h as the desktop of id in prefs, whose Hosts is
// its own, or deletes it when it holds nothing.
func desktopSetHost(prefs *config.DesktopSettings, id string, h config.DesktopHost) {
	if h.HasOwnLayout() || h.AutoArrange || h.Browser != "" || h.LastMode != "" || len(h.Favorites) > 0 || len(h.Recent) > 0 || len(h.Notes) > 0 {
		prefs.Hosts[id] = h
	} else {
		delete(prefs.Hosts, id)
	}
}

// desktopChangeOwnLayout changes the layout of s's host itself, giving it
// a layout of its own, copied from the shared one, first.
func (a *App) desktopChangeOwnLayout(s *Session, change func(*config.DesktopHost)) {
	a.changeDesktopPreferences(func(prefs *config.DesktopSettings) {
		host := prefs.Hosts[s.Host.ID]
		if !host.HasOwnLayout() {
			current := desktopHostOf(*prefs, s.Host.ID)
			host.Icons = slices.Clone(a.desktopHostIcons(s, current))
			host.Folders = slices.Clone(desktopHostFolders(current))
			host.AutoArrange = current.AutoArrange
			host.IconsConfigured = true
		}
		host.Icons = slices.Clone(host.Icons)
		host.Folders = slices.Clone(host.Folders)
		change(&host)
		prefs.Hosts = maps.Clone(prefs.Hosts)
		if prefs.Hosts == nil {
			prefs.Hosts = make(map[string]config.DesktopHost)
		}
		desktopSetHost(prefs, s.Host.ID, host)
	})
}

// desktopUseOwnLayout gives s's host a layout of its own, a copy of the
// shared one.
func (a *App) desktopUseOwnLayout(s *Session) {
	a.desktopChangeOwnLayout(s, func(*config.DesktopHost) {})
}

// desktopUseSharedLayout drops the layout of s's host, which then uses the
// shared one.
func (a *App) desktopUseSharedLayout(s *Session) {
	a.changeDesktopPreferences(func(prefs *config.DesktopSettings) {
		host, ok := prefs.Hosts[s.Host.ID]
		if !ok {
			return
		}
		prefs.Hosts = maps.Clone(prefs.Hosts)
		desktopLayoutFields(&host, config.DesktopHost{})
		desktopSetHost(prefs, s.Host.ID, host)
	})
}

// desktopMakeShared makes the layout of s's host the shared one, but for
// its shortcuts to remote paths, which mean nothing on other hosts.
func (a *App) desktopMakeShared(s *Session) {
	h := a.desktopHost(s)
	layout := config.DesktopHost{
		Icons:           slices.DeleteFunc(slices.Clone(a.desktopHostIcons(s, h)), func(i config.DesktopIcon) bool { return strings.HasPrefix(i.App, desktopPathPrefix) }),
		Folders:         slices.Clone(desktopHostFolders(h)),
		AutoArrange:     h.AutoArrange,
		IconsConfigured: true,
	}
	a.changeDesktopPreferences(func(prefs *config.DesktopSettings) {
		prefs.Hosts = maps.Clone(prefs.Hosts)
		if prefs.Hosts == nil {
			prefs.Hosts = make(map[string]config.DesktopHost)
		}
		prefs.Hosts[config.DesktopSharedLayout] = layout
	})
}

// desktopResetLayout puts the layout s's host uses back to the default
// one: the shared layout while the host uses it, else the host's own,
// which stays its own.
func (a *App) desktopResetLayout(s *Session) {
	if !a.desktopOwnLayout(s) {
		a.changeDesktopHost(s, func(h *config.DesktopHost) { desktopLayoutFields(h, config.DesktopHost{}) })
		return
	}
	a.changeDesktopHost(s, func(h *config.DesktopHost) {
		h.Icons, h.Folders = desktopDefaultIcons(s), desktopPresetFolderList()
		h.IconsConfigured, h.AutoArrange = true, false
	})
}

// desktopFlatten puts every app of s on the desktop itself, with the
// shortcuts, and deletes the folders.
func (a *App) desktopFlatten(s *Session) {
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		var icons []config.DesktopIcon
		for _, app := range desktopAppsFor(s) {
			icons = append(icons, config.DesktopIcon{App: string(app.id), Col: -1, Row: -1})
		}
		for _, icon := range h.Icons {
			if strings.HasPrefix(icon.App, desktopPathPrefix) {
				icons = append(icons, config.DesktopIcon{App: icon.App, Col: -1, Row: -1})
			}
		}
		h.Icons, h.Folders = icons, nil
	})
}

// desktopOrganize puts every app of s in its preset folder, making the
// preset folders that are missing: an app in a folder of the user's own
// stays there, and so do the shortcuts and the user's folders.
func (a *App) desktopOrganize(s *Session) {
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		has := func(id string) bool {
			return slices.ContainsFunc(h.Folders, func(f config.DesktopFolder) bool { return f.ID == id })
		}
		placed := map[string]config.DesktopIcon{}
		for _, icon := range h.Icons {
			placed[icon.App] = icon
		}
		var icons []config.DesktopIcon
		used := map[string]bool{}
		for _, app := range desktopAppsFor(s) {
			icon, ok := placed[string(app.id)]
			if !ok {
				icon = config.DesktopIcon{App: string(app.id), Col: -1, Row: -1}
			}
			if icon.Folder == "" || desktopIsPreset(icon.Folder) || !has(icon.Folder) {
				if folder := desktopPresetOf(app); folder != icon.Folder {
					icon.Folder, icon.Col, icon.Row = folder, -1, -1
				}
			}
			used[icon.Folder] = true
			icons = append(icons, icon)
		}
		var folders []config.DesktopFolder
		for _, f := range desktopPresetFolders {
			if !used[f.id] {
				continue
			}
			if idx := slices.IndexFunc(h.Folders, func(o config.DesktopFolder) bool { return o.ID == f.id }); idx >= 0 {
				folders = append(folders, h.Folders[idx])
			} else {
				folders = append(folders, config.DesktopFolder{ID: f.id, Name: f.name()})
			}
		}
		for _, f := range h.Folders {
			if !desktopIsPreset(f.ID) {
				folders = append(folders, f)
			}
		}
		var top []config.DesktopIcon
		for _, f := range folders {
			icon, ok := placed[desktopFolderPrefix+f.ID]
			if !ok || icon.Folder != "" {
				icon = config.DesktopIcon{App: desktopFolderPrefix + f.ID, Col: -1, Row: -1}
			}
			top = append(top, icon)
		}
		for _, icon := range h.Icons {
			if strings.HasPrefix(icon.App, desktopPathPrefix) {
				top = append(top, icon)
			}
		}
		h.Icons, h.Folders = append(top, icons...), folders
	})
}

// desktopIconsOf are the icons the icon id stands for: the selected ones
// in the same place when it is selected, else id alone; never folders.
func (a *App) desktopIconsOf(d *desktopState, s *Session, id string) []string {
	h := a.desktopHost(s)
	icons := a.desktopHostIcons(s, h)
	where := ""
	if idx := slices.IndexFunc(icons, func(i config.DesktopIcon) bool { return i.App == id }); idx >= 0 {
		where = icons[idx].Folder
	}
	var out []string
	for _, icon := range icons {
		if strings.HasPrefix(icon.App, desktopFolderPrefix) || icon.Folder != where {
			continue
		}
		if icon.App == id || d.shell.selection[id] && d.shell.selection[icon.App] {
			out = append(out, icon.App)
		}
	}
	return out
}

// desktopMoveIcons moves the icons ids into the folder, "" for the
// desktop.
func (a *App) desktopMoveIcons(s *Session, ids []string, folder string) {
	a.changeDesktopIcons(s, func(h *config.DesktopHost) {
		for idx := range h.Icons {
			if slices.Contains(ids, h.Icons[idx].App) && h.Icons[idx].Folder != folder {
				h.Icons[idx].Folder, h.Icons[idx].Col, h.Icons[idx].Row = folder, -1, -1
			}
		}
	})
}

// desktopMoveMenu is the icon's items that move it, and the icons
// selected with it, into a folder or out of one.
func (a *App) desktopMoveMenu(m *ui.Menu, d *desktopState, s *Session, id string) {
	h := a.desktopHost(s)
	icons := a.desktopHostIcons(s, h)
	idx := slices.IndexFunc(icons, func(i config.DesktopIcon) bool { return i.App == id })
	if idx < 0 || strings.HasPrefix(id, desktopFolderPrefix) {
		return
	}
	current := icons[idx].Folder
	if current != "" && m.Item(locales.DesktopShellMoveOut()).Chosen() {
		a.desktopMoveIcons(s, a.desktopIconsOf(d, s, id), "")
		d.shell.folder = current
		clear(d.shell.selection)
	}
	m.Submenu(locales.DesktopShellMoveToFolder(), func(sub *ui.Menu) {
		for _, f := range desktopHostFolders(h) {
			if f.ID == current {
				continue
			}
			if sub.Item(f.Name).Chosen() {
				a.desktopMoveIcons(s, a.desktopIconsOf(d, s, id), f.ID)
				clear(d.shell.selection)
			}
		}
		sub.Separator()
		if sub.Item(locales.NewFolder()).Chosen() {
			ids := a.desktopIconsOf(d, s, id)
			folder := a.desktopNewFolder(d, s)
			a.desktopMoveIcons(s, ids, folder)
			clear(d.shell.selection)
		}
	})
}

// desktopLayoutMenu is the desktop's menu of layouts: tidy up or flatten
// the icons, use the shared layout or one of the host's own, reset.
func (a *App) desktopLayoutMenu(m *ui.Menu, d *desktopState, s *Session) {
	m.Submenu(locales.DesktopShellLayout(), func(sub *ui.Menu) {
		if sub.Item(locales.DesktopShellOrganize()).Chosen() {
			a.desktopOrganize(s)
			d.shell.folder = ""
		}
		if sub.Item(locales.DesktopShellFlatten()).Chosen() {
			a.desktopFlatten(s)
			d.shell.folder = ""
		}
		sub.Separator()
		own := a.desktopOwnLayout(s)
		if sub.Item(locales.DesktopShellLayoutShared()).Radio(!own).Chosen() && own {
			d.shell.layoutConfirm = desktopConfirmUseShared
		}
		if sub.Item(locales.DesktopShellLayoutOwn()).Radio(own).Chosen() && !own {
			a.desktopUseOwnLayout(s)
		}
		if own && sub.Item(locales.DesktopShellLayoutMakeShared()).Chosen() {
			d.shell.layoutConfirm = desktopConfirmMakeShared
		}
		sub.Separator()
		if sub.Item(locales.DesktopShellLayoutReset()).Chosen() {
			d.shell.layoutConfirm = desktopConfirmReset
		}
	})
}

// What the layout's confirmation asks.
const (
	desktopConfirmUseShared = iota + 1
	desktopConfirmMakeShared
	desktopConfirmReset
)

// desktopLayoutConfirm asks before the layout changes that lose one: the
// host's own, the shared one, or the one used.
func (a *App) desktopLayoutConfirm(c *ui.Context, d *desktopState, s *Session) {
	sh := &d.shell
	if sh.layoutConfirm == 0 {
		return
	}
	open := true
	title, prompt := locales.DesktopShellLayoutShared(), locales.DesktopShellUseSharedPrompt()
	switch sh.layoutConfirm {
	case desktopConfirmMakeShared:
		title, prompt = locales.DesktopShellLayoutMakeShared(), locales.DesktopShellMakeSharedPrompt()
	case desktopConfirmReset:
		title, prompt = locales.DesktopShellLayoutReset(), locales.DesktopShellResetPrompt()
	}
	ui.Modal(c.Key("desktop-layout-confirm"), &open, func() {
		ui.Column(c).Width(360).Padding(16).Gap(12).Children(func() {
			ui.Text(c, title).Bold().FontSize(16)
			ui.Text(c, prompt)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.Button(c, locales.Ok()).Clicked() {
					switch sh.layoutConfirm {
					case desktopConfirmUseShared:
						a.desktopUseSharedLayout(s)
					case desktopConfirmMakeShared:
						a.desktopMakeShared(s)
					case desktopConfirmReset:
						a.desktopResetLayout(s)
					}
					sh.folder = ""
					clear(sh.selection)
					open = false
				}
			})
		})
	})
	if !open {
		sh.layoutConfirm = 0
	}
}

// notifyDesktopOwnLayout tells that a host got a layout of its own, as a
// shortcut to one of its paths was added to its desktop.
func (a *App) notifyDesktopOwnLayout() { a.notify(locales.DesktopShellOwnLayoutNotice()) }

// The folder panel's header, padding and gap between icons.
const (
	desktopFolderHeader = 42
	desktopFolderPad    = 8
	desktopFolderGap    = 4
	// desktopFolderScrollbar is the room the panel's scroll area keeps for
	// its scroll bar, which the icons' row cannot use.
	desktopFolderScrollbar = 14
)

// desktopFolderRect is where the panel of the folder id is, with n icons
// as large as the desktop's (g): 3 to 5 columns, as many rows as they
// need while the desktop has the room, where the user dragged it, else
// centered above the dock.
func desktopFolderRect(d *desktopState, sh *desktopShellState, id string, g desktopGrid, n int) (ui.Rect, bool) {
	cols := min(5, max(3, n))
	rows := max(1, (n+cols-1)/cols)
	w := float32(cols)*(g.spaceW+desktopFolderGap) - desktopFolderGap + 2*desktopFolderPad + desktopFolderScrollbar
	h := desktopFolderHeader + float32(rows)*(g.spaceH+desktopFolderGap) - desktopFolderGap + 2*desktopFolderPad
	if n == 0 {
		h = max(h, desktopFolderHeader+110) // the hint to drag icons in
	}
	areaW, areaH := d.area.W, d.area.H-desktopDockHeight
	w, h = min(w, areaW-24), min(h, areaH-24)
	if w < 80 || h < 80 {
		return ui.Rect{}, false
	}
	p := ui.Point{X: (areaW - w) / 2, Y: (areaH - h) / 2}
	if sh.folderMoved == id {
		p = desktopClampFolder(d, sh.folderAt, w, h)
	}
	return ui.Rect{X: p.X, Y: p.Y, W: w, H: h}, true
}

// desktopClampFolder keeps a folder panel of w by h at p on the desktop,
// above the dock.
func desktopClampFolder(d *desktopState, p ui.Point, w, h float32) ui.Point {
	maxX, maxY := max(0, d.area.W-w), max(0, d.area.H-desktopDockHeight-h)
	return ui.Point{X: min(max(p.X, 0), maxX), Y: min(max(p.Y, 0), maxY)}
}
