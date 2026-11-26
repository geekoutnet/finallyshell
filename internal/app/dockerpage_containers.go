package app

import (
	"slices"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerContainersPage struct {
	ctrList
	client   *ssh.Client
	selected map[string]bool
	visible  []string
	anchor   string
}

func (a *App) refreshDockerContainers(s *Session) {
	if dialog := a.ctrDlg; dialog != nil && dialog.s == s && dialog.ctr != nil && dialog.ctr.stats != nil {
		dialog.ctr.stats.next = time.Time{}
	}
}

func (a *App) dockerContainersSection(c *ui.Context, s *Session) {
	state, page := &s.overview.docker, &s.docker.containers
	if page.selected == nil || page.client != s.client {
		page.client, page.selected, page.anchor = s.client, map[string]bool{}, ""
	}
	root := ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(4, 8, 4, 8).Gap(4)
	width := root.Bounds().W
	if width == 0 {
		width = 460
	}
	now := c.Now()
	groups, shown := page.groups(state.d.Containers, now)
	page.setVisible(groups)
	root.Children(func() {
		a.ctrToolbar(c, s, page, shown)
		if len(state.d.Containers) == 0 {
			widgets.Muted(c, locales.DockerNoContainers()).Padding(8, 4)
			return
		}
		cols := page.ctrList.columns(width - 60)
		a.ctrHeader(c, &page.ctrList, cols, func() { page.selectAll(c) })
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if shown == 0 {
				widgets.Muted(c, locales.DockerNoMatch()).Padding(8, 4)
			}
			a.ctrGroupRows(c, s, &page.ctrList, groups, cols, now, page)
		})
	})
}

func (a *App) ctrToolbar(c *ui.Context, s *Session, page *dockerContainersPage, shown int) {
	state := &s.overview.docker
	target := dockerTarget{session: s, client: s.client}
	ui.Row(c).FillWidth().Wrap().GapX(6).GapY(3).AlignItems(ui.Center).Children(func() {
		filter := locales.DockerFilter()
		ui.SearchField(c, &page.filter).Placeholder(filter).Label(filter).Grow(1).MinWidth(90).MaxWidth(260)
		if widgets.ToggleIconButton(c, "eye", locales.DockerCtrShowStopped(), !page.hideStopped).Clicked() {
			page.hideStopped = !page.hideStopped
		}
		if widgets.ToggleIconButton(c, "layout-grid", locales.DockerGroupByProject(), !page.flat).Clicked() {
			page.flat = !page.flat
		}
		if ui.PrimaryButton(c, locales.DockerCtrNew()).Disabled(!target.available() || state.work != nil).Shrink(0).Clicked() {
			a.openCtrForm(target, nil, false)
		}
		if ids := page.selectedIDs(); len(ids) > 0 {
			ui.MenuButton(c, locales.DockerCtrSelection(len(ids)), func(menu *ui.Menu) {
				a.ctrSelectionMenu(menu, s, ids)
			}).Shrink(0)
		} else {
			label := locales.DockerCount(len(state.d.Containers), runningCount(state.d.Containers))
			if shown != len(state.d.Containers) {
				label = locales.DockerCountFiltered(shown, len(state.d.Containers))
			}
			widgets.Muted(c, label).FontSize(12).SingleLine().MinWidth(0).Tooltip(label)
		}
	})
}

func (page *dockerContainersPage) setVisible(groups []ctrGroup) {
	page.visible = page.visible[:0]
	shown := make(map[string]bool)
	for _, group := range groups {
		if page.closed[group.project] {
			continue
		}
		for _, container := range group.list {
			page.visible = append(page.visible, container.ID)
			shown[container.ID] = true
		}
	}
	for id := range page.selected {
		if !shown[id] {
			delete(page.selected, id)
		}
	}
	if !shown[page.anchor] {
		page.anchor = ""
	}
}

func (page *dockerContainersPage) selectedIDs() []string {
	var ids []string
	for _, id := range page.visible {
		if page.selected[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

func (page *dockerContainersPage) click(id string, mods ui.Modifiers) {
	if mods&ui.Shift != 0 {
		start, end := slices.Index(page.visible, page.anchor), slices.Index(page.visible, id)
		if start >= 0 && end >= 0 {
			if mods&(ui.Ctrl|ui.Cmd) == 0 {
				clear(page.selected)
			}
			for _, visible := range page.visible[min(start, end) : max(start, end)+1] {
				page.selected[visible] = true
			}
			return
		}
	}
	if mods&(ui.Ctrl|ui.Cmd) == 0 {
		clear(page.selected)
	}
	if page.selected[id] {
		delete(page.selected, id)
	} else {
		page.selected[id] = true
	}
	page.anchor = id
}

func (page *dockerContainersPage) selectAll(c *ui.Context) {
	state := ui.CheckOff
	if count := len(page.selected); count > 0 {
		state = ui.CheckMixed
		if count == len(page.visible) {
			state = ui.CheckOn
		}
	}
	label := locales.SelectAll()
	if ui.CheckboxState(c, &state, "").Label(label).Tooltip(label).Width(20).Shrink(0).Disabled(len(page.visible) == 0).Changed() {
		clear(page.selected)
		if state == ui.CheckOn {
			for _, id := range page.visible {
				page.selected[id] = true
			}
		}
		c.Invalidate()
	}
}

func (page *dockerContainersPage) checkbox(c *ui.Context, container *monitor.Container) {
	checked := page.selected[container.ID]
	if ui.Checkbox(c, &checked, "").Label(locales.ManagerSelectRow(container.Name)).Width(20).Shrink(0).Changed() {
		page.click(container.ID, ui.Ctrl|c.Modifiers())
		c.Invalidate()
	}
}

func (a *App) ctrSelectionMenu(menu *ui.Menu, s *Session, ids []string) {
	target := dockerTarget{session: s, client: s.client}
	for _, action := range []dockerAction{dockerStart, dockerStop, dockerRestart, dockerPause, dockerUnpause, dockerKill, dockerRemove} {
		modal := &ctrTask{target: target, action: action, ids: slices.Clone(ids), title: action.title(false)}
		reason := modal.unavailable(a)
		if menu.Item(modal.title).Disabled(reason != "").Subtitle(reason).Chosen() {
			modal.names = ctrTargetNames(s, ids)
			a.openCtrTask(modal)
		}
	}
	menu.Separator()
	reason := ctrStatsReason(target, ids)
	if menu.Item(locales.DockerCtrStats()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.openCtrStats(target, ids)
	}
}
