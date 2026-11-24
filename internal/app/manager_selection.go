package app

import (
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (m *managerState) selectOnly(it managerItem) {
	m.selection.Clear()
	m.selection.Add(it)
}

func (m *managerState) keepVisibleSelection(visible []managerItem) {
	if m.selection.Len() == 0 {
		return
	}
	shown := make(map[managerItem]bool, len(visible))
	for _, it := range visible {
		shown[it] = true
	}
	if it, ok := m.chosen(); ok && !shown[it] {
		m.row = -1
	}
	for it := range m.selection.All() {
		if !shown[it] {
			m.selection.Remove(it)
		}
	}
}

func (m *managerState) selectedRows() []managerItem {
	if m.selection.Len() == 0 {
		return nil
	}
	items := make([]managerItem, 0, m.selection.Len())
	for row := range m.tree.Rows() {
		if it := m.tree.Item(row); m.selection.Has(it) {
			items = append(items, it)
		}
	}
	return items
}

func (m *managerState) managerSelectionHeader(c *ui.Context, visible []managerItem) {
	state := ui.CheckOff
	switch count := m.selection.Len(); {
	case count == 0:
	case count == len(visible):
		state = ui.CheckOn
	default:
		state = ui.CheckMixed
	}
	label := locales.SelectAll()
	if ui.CheckboxState(c, &state, "").Label(label).Tooltip(label).Disabled(len(visible) == 0).Changed() {
		m.selection.Clear()
		if state == ui.CheckOn {
			for _, it := range visible {
				m.selection.Add(it)
			}
		} else {
			m.row = -1
		}
		c.Invalidate()
	}
}

func (a *App) managerRowCheckbox(c *ui.Context, m *managerState, it managerItem) {
	checked := m.selection.Has(it)
	box := ui.Checkbox(c, &checked, "").Label(locales.ManagerSelectRow(a.managerTitle(it)))
	if checked {
		color := theme.AccentText.Alpha(0.75)
		box.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Stroke(r, color, 3, 1)
		})
	}
	if box.Changed() {
		if checked {
			m.selection.Add(it)
		} else {
			m.selection.Remove(it)
		}
		m.row = m.rowOfItem(it)
		c.Invalidate()
	}
}

func managerDisclosure(c *ui.Context, row ui.OutlineRow, chosen bool) {
	if row.Depth > 0 {
		ui.Box(c).Width(float32(row.Depth) * theme.Space(4)).Shrink(0)
	}
	arrow := row.Arrow().Size(16, 16).Center()
	if !row.Branch {
		return
	}
	icon := "chevron-right"
	if row.Open {
		icon = "chevron-down"
	}
	color := theme.TextMuted
	if chosen {
		color = theme.AccentText
	}
	arrow.Children(func() { ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(color) })
}

func (a *App) managerSelectionKeys(c *ui.Context, m *managerState, later *[]func()) bool {
	escape := m.tree.List.Shortcut(c, 0, ui.KeyEscape)
	remove := m.tree.List.Shortcut(c, 0, ui.KeyDelete)
	backspace := m.tree.List.Shortcut(c, ui.Cmd, ui.KeyBackspace)
	switch {
	case escape && m.selection.Len() > 1:
		if it, ok := m.chosen(); ok && m.selection.Has(it) {
			m.selectOnly(it)
		} else if items := m.selectedRows(); len(items) > 0 {
			m.row = m.rowOfItem(items[0])
			m.selectOnly(items[0])
		}
		c.Invalidate()
		return true
	case remove || backspace:
		items := m.selectedRows()
		*later = append(*later, func() { a.deleteManagerSelection(m, items) })
		return true
	}
	return false
}

func (a *App) managerSelectionTools(c *ui.Context, m *managerState, later *[]func()) {
	items := m.selectedRows()
	count := len(items)
	if count == 0 {
		return
	}
	label := locales.ManagerDeleteCount(count)
	if a.allManagerItemsDeleted(items) {
		label = locales.ManagerDeleteForeverCount(count)
	}
	ui.Toolbar(c, func() {
		ui.Text(c, locales.ManagerSelectedCount(count)).TextColor(theme.TextMuted).SingleLine().Shrink(0)
		hosts := a.bulkManagerHosts(m, items)
		if ui.Button(c, locales.BulkTitle()).Disabled(len(hosts) == 0).Tooltip(bulkReason(len(hosts) == 0, locales.BulkNoTargets())).Clicked() {
			*later = append(*later, func() { a.openBulkExec(hosts, "") })
		}
		button := ui.Button(c, "").Label(label).KeepFocus().Border(1, theme.Border).Children(func() {
			ui.Icon(c, icons.Get("trash-2")).FontSize(14).TextColor(theme.TextMuted)
			ui.Text(c, label).SingleLine()
		})
		if button.Clicked() {
			*later = append(*later, func() { a.deleteManagerSelection(m, items) })
		}
	}).Label(locales.ManagerSelectedCount(count)).Shrink(0)
}

func (a *App) allManagerItemsDeleted(items []managerItem) bool {
	return len(items) > 0 && !slices.ContainsFunc(items, func(it managerItem) bool {
		host := a.hosts.Host(it.host)
		return !it.isHost() || host == nil || !host.Deleted()
	})
}

func (a *App) managerSelectionMenu(menu *ui.Menu, m *managerState, items []managerItem, later *[]func()) {
	a.bulkManagerItem(menu, m, items, later)
	label := locales.ManagerDeleteSelected(len(items))
	if a.allManagerItemsDeleted(items) {
		label = locales.ManagerDeleteForeverCount(len(items))
		if menu.Item(locales.ManagerRestoreSelected(len(items))).Chosen() {
			*later = append(*later, func() {
				for _, it := range items {
					a.hosts.Restore(it.host)
				}
				a.saveFromTree(m)
			})
		}
	}
	if menu.Item(label).Shortcut(0, ui.KeyDelete).Chosen() {
		*later = append(*later, func() { a.deleteManagerSelection(m, items) })
	}
}

func (a *App) deleteManagerSelection(m *managerState, items []managerItem) {
	if len(items) == 1 {
		a.deleteItem(m, items[0])
		return
	}
	var folders, live, deleted, names []string
	selectedHosts := make(map[string]bool, len(items))
	for _, it := range items {
		switch {
		case it.isHost():
			host := a.hosts.Host(it.host)
			if host == nil {
				continue
			}
			if host.Deleted() {
				deleted = append(deleted, it.host)
			} else {
				live = append(live, it.host)
			}
			selectedHosts[it.host] = true
		case a.hosts.Folder(it.folder) != nil:
			folders = append(folders, it.folder)
		default:
			continue
		}
		names = append(names, a.managerTitle(it))
	}
	count := len(names)
	if count == 0 {
		return
	}
	const maxNames = 10
	q := &managerQuestion{
		title: func() string { return locales.ManagerDeleteSelectedTitle(count) },
		ok:    locales.Delete,
		names: names[:min(maxNames, count)],
		more:  max(0, count-maxNames),
	}
	switch count {
	case len(live):
		q.title = func() string { return locales.ManagerTrashTitle(count) }
	case len(deleted):
		q.title = func() string { return locales.ManagerDeleteForeverSelectionTitle(count) }
		q.ok = locales.DeleteForever
	}
	affected := 0
	for _, host := range a.hosts.Live() {
		proxy := config.ProxyOf(host)
		if !selectedHosts[host.ID] && proxy != nil && proxy.Kind == config.ProxyJump && selectedHosts[proxy.JumpHost] {
			affected++
		}
	}
	q.body = func() string {
		var parts []string
		if len(live) > 0 {
			parts = append(parts, locales.ManagerTrashBody(len(live)))
		}
		if len(folders) > 0 {
			parts = append(parts, locales.ManagerDeleteFoldersBody())
		}
		if len(deleted) > 0 {
			parts = append(parts, locales.ManagerDeleteForeverSelectionBody(len(deleted)))
		}
		if affected > 0 {
			parts = append(parts, locales.ManagerDeleteJumpWarning(affected))
		}
		return strings.Join(parts, "\n")
	}
	q.yes = func() {
		if !a.backupBeforeDelete(m) || !a.rememberManagerDeletes(m, deleted) {
			return
		}
		changed := false
		for _, id := range folders {
			changed = a.hosts.RemoveFolder(id) || changed
			m.tree.Open.Remove(managerItem{folder: id})
		}
		now := time.Now()
		for _, id := range live {
			changed = a.hosts.SoftDelete(id, now) || changed
		}
		for _, id := range deleted {
			if host := a.hosts.Host(id); host != nil && host.Deleted() {
				a.removeHostForever(host)
				changed = true
			}
		}
		m.selection.Clear()
		m.row, m.focus = -1, true
		if changed {
			a.saveFromTree(m)
		}
	}
	m.question = q
}

func (a *App) managerDragRoots(items managerDrag) managerDrag {
	selected := make(map[string]bool)
	for _, it := range items {
		if !it.isHost() {
			selected[it.folder] = true
		}
	}
	var roots managerDrag
	for _, it := range items {
		parent := ""
		if it.isHost() {
			if host := a.hosts.Host(it.host); host != nil {
				parent = host.Folder
			}
		} else if folder := a.hosts.Folder(it.folder); folder != nil {
			parent = folder.Parent
		}
		if !slices.ContainsFunc(a.hosts.FolderPath(parent), func(folder *config.Folder) bool { return selected[folder.ID] }) {
			roots = append(roots, it)
		}
	}
	return roots
}
