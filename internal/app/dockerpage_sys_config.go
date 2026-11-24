package app

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) loadDockerRegistries(s *Session, page *dockerResourcesPage, force bool) {
	if page.tool.Tool != "docker" {
		return
	}
	readDockerResource(a, s, &page.system.registries, force, resourceOnce(page.tool.RegistryList(), monitor.ParseDockerRegistries))
}

func (a *App) dockerRegistryView(c *ui.Context, s *Session, page *dockerResourcesPage) {
	if page.tool.Tool != "docker" {
		overviewMessage(c, "container", locales.DockerResPodman(), nil)
		return
	}
	a.loadDockerRegistries(s, page, false)
	state := &page.system
	data := state.registries.rows
	cols := []ui.TableColumn{resourceColumn("name", locales.DockerSysRegistry(), 185), resourceColumn("helper", locales.DockerSysHelper(), 170)}
	var rows []dockerResourceRow
	for _, registry := range data.Registries {
		rows = append(rows, dockerResourceRow{registry, registry, []string{registry, data.Helpers[registry]}, nil})
	}
	for registry, helper := range data.Helpers {
		if !slices.Contains(data.Registries, registry) {
			rows = append(rows, dockerResourceRow{registry, registry, []string{registry, helper + " · " + locales.DockerSysHelperConfigured()}, nil})
		}
	}
	rows = state.registryTable.shown(rows, cols)
	menu := func(m *ui.Menu, registry string) {
		resourceCopy(c, m, labelValue{locales.DockerSysRegistry(), registry})
		reason := page.blocked(s)
		if !slices.Contains(data.Registries, registry) {
			reason = cmp.Or(reason, locales.DockerSysRegistryNotListed())
		}
		if !monitor.ValidDockerRegistry(registry) {
			reason = cmp.Or(reason, locales.DockerSysInvalidLogin())
		}
		if resourceMenuItem(m, locales.DockerSysLogout(), reason, true) {
			a.openDockerLogout(s, page, registry)
		}
	}
	resourceToolbar(c, &state.registryTable, len(rows), func() {
		if ui.PrimaryButton(c, locales.DockerSysLogin()).Disabled(page.blocked(s) != "").Shrink(0).Clicked() {
			a.openDockerLogin(s, page)
		}
		ui.Button(c, locales.DockerResManage()).Disabled(state.registryTable.selected.Len() == 0).Shrink(0).Menu(func(m *ui.Menu) {
			for _, row := range rows {
				if state.registryTable.selected.Has(row.key) {
					menu(m, row.key)
					break
				}
			}
		})
	})
	hint := locales.DockerSysRegistryHint()
	if page.tool.Root {
		hint = locales.DockerPrivRegistryHint()
	}
	widgets.Muted(c, hint).FontSize(11).SingleLine().FillWidth().Padding(0, 8, 2).Tooltip(hint)
	if data.Store != "" {
		widgets.Muted(c, locales.DockerSysStore()+": "+data.Store).FontSize(11).SingleLine().Padding(0, 8, 2)
	}
	resourceStatus(c, &state.registries, func() { a.loadDockerRegistries(s, page, true) })
	state.registryTable.draw(c, locales.DockerSysRegistries(), cols, rows, menu, nil)
}

func (a *App) openDockerLogin(s *Session, page *dockerResourcesPage) {
	registry, username, password := "docker.io", "", ""
	page.show(locales.DockerSysLogin(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerSysLoginHint()).TextColor(theme.TextMuted)
		resourceText(c, locales.DockerSysRegistry(), &registry)
		resourceText(c, locales.FieldUser(), &username)
		ui.Text(c, locales.FieldPassword()).FontSize(12)
		ui.TextInput(c, &password).Password().Label(locales.FieldPassword()).FillWidth()
		input, err := page.tool.RegistryLogin(cmp.Or(strings.TrimSpace(registry), "docker.io"), strings.TrimSpace(username), password)
		reason := page.blocked(s)
		key := cmp.Or(strings.TrimSpace(registry), "docker.io")
		data := page.system.registries.rows
		helper, specific := data.Helpers[key]
		if !specific {
			helper = data.Store
		}
		if helper == "" {
			reason = cmp.Or(reason, locales.DockerSysLoginHelper())
			ui.Text(c, reason).TextColor(theme.Danger)
		}
		if err != nil {
			reason = cmp.Or(reason, locales.DockerSysInvalidLogin())
		}
		if resourceButtons(c, open, locales.DockerSysLogin(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{input: &input, private: true})
		}
	})
	page.dialog.clear = func() { password = "" }
}

func (a *App) openDockerLogout(s *Session, page *dockerResourcesPage, registry string) {
	page.show(locales.DockerSysLogout(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, registry).Bold().Selectable()
		warning := locales.DockerSysLogoutWarning()
		if page.tool.Root {
			warning = locales.DockerPrivRegistryLogout()
		}
		ui.Text(c, warning)
		script, err := page.tool.RegistryLogout(registry)
		reason := page.blocked(s)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerSysInvalidLogin())
		}
		if resourceButtons(c, open, locales.DockerSysLogout(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}

func daemonUnavailable(s *Session, page *dockerResourcesPage) string {
	if page.tool.Tool != "docker" {
		return locales.DockerResPodman()
	}
	if page.system.info.rows.Rootless || s.overview.docker.d.CgroupDriver == "none" && strings.Contains(s.overview.docker.d.RootDir, "/.local/") {
		return locales.DockerSysConfigUnavailable()
	}
	return page.blocked(s)
}

type daemonEditor struct {
	load                dockerResourceLoad[string]
	before, raw         string
	mirrors             []string
	ready, dirtyMirrors bool
	showRaw             bool
}

func (a *App) openDaemonConfig(s *Session, page *dockerResourcesPage) {
	edit := &daemonEditor{}
	load := func(force bool) {
		readDockerResourceAccess(a, s, &edit.load, force, true, resourceOnce(page.tool.DaemonRead(), func(out string) (string, error) { return out, nil }))
	}
	page.show(locales.DockerSysConfig(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, "/etc/docker/daemon.json").Bold().Selectable()
		ui.Text(c, locales.DockerSysConfigWarning()).TextColor(theme.TextMuted)
		resourceStatus(c, &edit.load, func() { load(true) })
		if !edit.load.read || edit.load.loading || edit.load.err != nil {
			if dockerPrivilegeFailed(edit.load.err) {
				dockerPrivilegeHelp(c, s)
			} else if edit.load.err != nil && !errors.Is(edit.load.err, monitor.ErrPrivilegeCancelled) {
				widgets.Muted(c, locales.DockerSysConfigUnavailable())
			}
			if ui.Button(c, locales.Cancel()).Clicked() {
				*open = false
			}
			return
		}
		if !edit.ready {
			edit.before, edit.raw, edit.ready = edit.load.rows, edit.load.rows, true
			if strings.TrimSpace(edit.raw) == "" {
				edit.raw = "{}\n"
			}
			edit.mirrors, _ = monitor.DaemonMirrors(edit.raw)
		}
		ui.Text(c, locales.DockerSysMirrors()).Bold()
		remove := -1
		for index := range edit.mirrors {
			ui.Row(c.Key(index)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if ui.TextInput(c, &edit.mirrors[index]).Label(locales.DockerSysMirrors()).Grow(1).MinWidth(0).Changed() {
					edit.dirtyMirrors = true
				}
				if widgets.IconButton(c, "trash-2", locales.Delete()).Shrink(0).Clicked() {
					remove = index
				}
			})
		}
		if remove >= 0 {
			edit.mirrors, edit.dirtyMirrors = slices.Delete(edit.mirrors, remove, remove+1), true
		}
		update, mirrorErr := monitor.WithDaemonMirrors(edit.raw, edit.mirrors)
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c, locales.DockerSysAddMirror()).Disabled(len(edit.mirrors) >= 100).Clicked() {
				edit.mirrors = append(edit.mirrors, "")
				edit.dirtyMirrors = true
			}
			if ui.Button(c, locales.DockerSysUseMirrors()).Disabled(!edit.dirtyMirrors || mirrorErr != nil).Clicked() {
				edit.raw, edit.dirtyMirrors = update, false
			}
		})
		ui.Checkbox(c, &edit.showRaw, locales.DockerSysRaw())
		if edit.showRaw && ui.TextArea(c, &edit.raw).Label(locales.DockerSysRaw()).Height(160).FillWidth().Changed() && !edit.dirtyMirrors {
			if mirrors, err := monitor.DaemonMirrors(edit.raw); err == nil {
				edit.mirrors = mirrors
			}
		}
		toSave := edit.raw
		if edit.dirtyMirrors && mirrorErr == nil {
			toSave = update
		}
		input, err := page.tool.DaemonSave(edit.before, toSave, time.Now())
		reason := daemonUnavailable(s, page)
		if err != nil || edit.dirtyMirrors && mirrorErr != nil {
			reason = cmp.Or(reason, locales.DockerSysBadConfig())
			ui.Text(c, reason).TextColor(theme.Danger)
		}
		if strings.TrimSpace(toSave) == strings.TrimSpace(edit.before) {
			reason = cmp.Or(reason, locales.DockerSysConfigWarning())
		}
		if resourceButtons(c, open, locales.Save(), reason) {
			dialog.after = func() {
				a.refreshResourceAfterAction(s, page, dockerSecSystem)
				a.offerDockerRestart(s, page, toSave, dialog.work.output)
			}
			a.runDockerResource(s, page, dialog, dockerResourceStep{input: &input, root: true})
		}
	})
	page.dialog.clear = func() {
		if edit.load.request != nil {
			edit.load.request.cancel()
		}
	}
	load(false)
}

func (a *App) offerDockerRestart(s *Session, page *dockerResourcesPage, config, output string) {
	page.show(locales.DockerSysConfig(), func(c *ui.Context, _ *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerSysRestartLater())
		if output != "" {
			ui.Text(c, strings.TrimSpace(output)).Selectable()
		}
		if resourceButtons(c, open, locales.DockerSysRestart(), daemonUnavailable(s, page)) {
			a.confirmDockerRestart(s, page, config)
		}
	})
}

func (a *App) confirmDockerRestart(s *Session, page *dockerResourcesPage, config string) {
	ack := false
	page.show(locales.DockerSysRestart(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerSysRestartWarning()).Bold().TextColor(theme.Danger)
		ui.Checkbox(c, &ack, locales.DockerSysRestartAck())
		script, err := page.tool.DaemonRestart(config)
		reason := daemonUnavailable(s, page)
		if err != nil || !ack {
			reason = cmp.Or(reason, locales.DockerSysRestartAck())
		}
		if resourceButtons(c, open, locales.DockerSysRestart(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script, root: true})
		}
	})
}
