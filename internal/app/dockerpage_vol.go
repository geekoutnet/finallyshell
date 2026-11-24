package app

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type dockerVolumesPage struct {
	dockerResourceLoad[[]monitor.DockerVolumeDetails]
	unused     dockerResourceLoad[[]string]
	usage      dockerResourceLoad[map[string]monitor.DockerVolumeUsage]
	local      dockerResourceLoad[bool]
	table      dockerResourceTable
	onlyUnused bool
	browsing   bool
}

func (a *App) loadDockerVolumes(s *Session, page *dockerResourcesPage, force bool) {
	cmd := page.tool
	readDockerResource(a, s, &page.volumes.dockerResourceLoad, force, resourceInspect(cmd, "volume", cmd.VolumeList(), monitor.ParseDockerVolumeDetails))
	readDockerResource(a, s, &page.volumes.unused, force, resourceOnce(cmd.UnusedVolumes(), func(out string) ([]string, error) { return monitor.DockerResourceRefs(out, false) }))
	readDockerResource(a, s, &page.volumes.local, force, resourceOnce(cmd.LocalEngine(), func(out string) (bool, error) { return strings.TrimSpace(out) == "local", nil }))
}

func (a *App) loadDockerVolumeUsage(s *Session, page *dockerResourcesPage, force bool) {
	if page.tool.Tool != "docker" {
		return
	}
	readDockerResource(a, s, &page.volumes.usage, force, resourceOnce(page.tool.ResourceDisk(true), monitor.ParseDockerVolumeUsage))
}

func volumeCols(narrow bool) []ui.TableColumn {
	nameWidth, driverWidth, usedWidth := float32(155), float32(90), float32(150)
	if narrow {
		nameWidth, driverWidth, usedWidth = 134, 66, 104
	}
	return []ui.TableColumn{
		resourceColumn("name", locales.FieldName(), nameWidth), resourceColumn("driver", locales.DockerResDriver(), driverWidth),
		resourceColumn("size", locales.DockerSize(), 79), resourceColumn("used", locales.DockerResVolumeUsed(), usedWidth),
		resourceColumn("mount", locales.DockerResVolumeMount(), 230), resourceColumn("created", locales.DockerCreated(), 155),
		resourceColumn("labels", locales.DockerSecLabels(), 185),
	}
}

func volumeUsers(s *Session) map[string][]string {
	users := make(map[string][]string)
	for _, container := range s.overview.docker.d.Containers {
		for _, mount := range container.Mounts {
			if mount.Type == "volume" && mount.Name != "" && !slices.Contains(users[mount.Name], container.Name) {
				users[mount.Name] = append(users[mount.Name], container.Name)
			}
		}
	}
	for _, names := range users {
		slices.Sort(names)
	}
	return users
}

func (volumes *dockerVolumesPage) unusedVolume(name string, users map[string][]string) bool {
	return volumes.unused.read && volumes.unused.err == nil && !volumes.unused.loading && len(users[name]) == 0 && slices.Contains(volumes.unused.rows, name)
}

func volumeRows(volumes *dockerVolumesPage, users map[string][]string) []dockerResourceRow {
	var rows []dockerResourceRow
	for _, volume := range volumes.rows {
		unused := volumes.unusedVolume(volume.Name, users)
		if volumes.onlyUnused && !unused {
			continue
		}
		name := volume.Name
		if volume.Anonymous {
			name += " · " + locales.DockerResVolumeAnonymous()
		} else if volume.LooksAnonymous() {
			name += " · " + locales.DockerResVolumeMaybeAnonymous()
		}
		usage := volumes.usage.rows[volume.Name]
		usedBy := strings.Join(users[volume.Name], ", ")
		if usedBy == "" && volumes.unused.read && volumes.unused.err == nil && !volumes.unused.loading && !unused {
			usedBy = locales.DockerSysActive()
		}
		labels := make(map[string]string, len(volume.Labels))
		for key, value := range volume.Labels {
			if key != "com.docker.volume.anonymous" {
				labels[key] = value
			}
		}
		rows = append(rows, dockerResourceRow{volume.Name, volume.Name, []string{name, volume.Driver, usage.Size, usedBy, volume.Mountpoint, localTime(volume.Created), strings.ReplaceAll(resourcePairText(labels), "\n", ", ")},
			map[string]int64{"size": int64(monitor.ParseSize(usage.Size)), "created": volume.Created.Unix()}})
	}
	return rows
}

func (a *App) dockerVolumesSection(c *ui.Context, s *Session) {
	page := a.dockerResources(s)
	section := &page.volumes
	a.loadDockerVolumes(s, page, false)
	width, _ := c.Size()
	cols := volumeCols(width < 920 || a.panelOnRight() && !a.current().isPanelTab())
	rows := section.table.shown(volumeRows(section, volumeUsers(s)), cols)
	menu := func(m *ui.Menu, key string) { a.volumeMenu(c, m, s, page, key) }
	details := func(key string) { a.openVolumeDetails(s, page, key) }
	if a.panelOnRight() && !a.current().isPanelTab() {
		resourceTitle(c, locales.DockerVolumes())
	}
	resourceToolbar(c, &section.table, len(rows), func() {
		if ui.PrimaryButton(c, locales.DockerResCreate()).Disabled(page.blocked(s) != "").Shrink(0).Clicked() {
			a.createDockerVolume(s, page)
		}
		reason := volumePruneReason(s, page)
		if ui.Button(c, locales.DockerResPrune()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
			a.confirmVolumePrune(s, page)
		}
		ui.Button(c, locales.DockerResManage()).Shrink(0).Menu(func(m *ui.Menu) {
			page.outputMenu(m)
			if m.Item(locales.DockerResVolumeUnused()).Checked(section.onlyUnused).Chosen() {
				section.onlyUnused = !section.onlyUnused
			}
			if resourceMenuItem(m, locales.DockerResVolumeSizes(), volumeUsageReason(page), false) {
				a.loadDockerVolumeUsage(s, page, true)
			}
			for _, row := range rows {
				if section.table.selected.Has(row.key) {
					m.Separator()
					menu(m, row.key)
					break
				}
			}
		})
	})
	ui.Row(c).Padding(0, 8, 3).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Checkbox(c, &section.onlyUnused, locales.DockerResVolumeUnused())
		reason := volumeUsageReason(page)
		if ui.Button(c, locales.DockerResVolumeSizes()).Disabled(reason != "").Tooltip(reason).Clicked() {
			a.loadDockerVolumeUsage(s, page, true)
		}
	})
	resourceStatus(c, &section.dockerResourceLoad, func() { a.loadDockerVolumes(s, page, true) })
	if section.err == nil && !section.loading {
		resourceStatus(c, &section.unused, func() { a.loadDockerVolumes(s, page, true) })
	}
	resourceStatus(c, &section.usage, func() { a.loadDockerVolumeUsage(s, page, true) })
	section.table.draw(c, locales.DockerVolumes(), cols, rows, menu, details)
	page.drawDialog(c)
}

func volumeUsageReason(page *dockerResourcesPage) string {
	if page.tool.Tool == "podman" {
		return locales.DockerResPodman()
	}
	if page.volumes.usage.loading {
		return locales.DockerReading()
	}
	return ""
}

func lookupVolume(page *dockerResourcesPage, name string) (monitor.DockerVolumeDetails, bool) {
	for _, volume := range page.volumes.rows {
		if volume.Name == name {
			return volume, true
		}
	}
	return monitor.DockerVolumeDetails{}, false
}

func volumeBaseReason(s *Session, page *dockerResourcesPage) string {
	return cmp.Or(page.blocked(s), resourceListReason(&page.volumes.dockerResourceLoad), resourceListReason(&page.volumes.unused))
}

func volumeRemoveReason(s *Session, page *dockerResourcesPage, name string, users map[string][]string, force bool) string {
	if reason := volumeBaseReason(s, page); reason != "" {
		return reason
	}
	if force && page.tool.Tool == "podman" {
		return locales.DockerResPodman()
	}
	if volume, ok := lookupVolume(page, name); !ok || volume.Created.IsZero() || volume.CreatedRaw == "" {
		return locales.DockerResVolumeIdentity()
	}
	if !force && !page.volumes.unusedVolume(name, users) {
		return locales.DockerResVolumeInUse()
	}
	return ""
}

func volumeBrowseReason(s *Session, page *dockerResourcesPage, mount string) string {
	if s.sftp == nil || !path.IsAbs(mount) || strings.ContainsRune(mount, 0) {
		return locales.DockerResRemoteFiles()
	}
	if page.volumes.browsing {
		return locales.DockerReading()
	}
	if !page.volumes.local.read || !page.volumes.local.rows {
		return locales.DockerResBrowseLocal()
	}
	return ""
}

func (a *App) volumeMenu(c *ui.Context, m *ui.Menu, s *Session, page *dockerResourcesPage, key string) {
	volume, found := lookupVolume(page, key)
	if !found {
		return
	}
	if m.Item(locales.DockerDetails()).Chosen() {
		a.openVolumeDetails(s, page, key)
	}
	if resourceMenuItem(m, locales.DockerResBrowse(), volumeBrowseReason(s, page, volume.Mountpoint), false) {
		a.browseDockerVolume(s, page, volume.Mountpoint)
	}
	resourceCopy(c, m, labelValue{locales.FieldName(), volume.Name}, labelValue{locales.DockerResVolumeMount(), volume.Mountpoint})
	m.Separator()
	chosen := []monitor.DockerVolumeDetails{volume}
	if page.volumes.table.selected.Has(key) {
		for other := range page.volumes.table.selected.All() {
			if other != key {
				if item, ok := lookupVolume(page, other); ok {
					chosen = append(chosen, item)
				}
			}
		}
	}
	users := volumeUsers(s)
	reason := ""
	for _, item := range chosen {
		reason = cmp.Or(reason, volumeRemoveReason(s, page, item.Name, users, false))
	}
	if resourceMenuItem(m, locales.DockerResVolumeRemove(), reason, true) {
		a.confirmVolumeRemove(s, page, chosen, false)
	}
	if resourceMenuItem(m, locales.DockerResVolumeForce(), volumeRemoveReason(s, page, key, users, true), true) {
		a.confirmVolumeRemove(s, page, []monitor.DockerVolumeDetails{volume}, true)
	}
}

func (a *App) openVolumeDetails(s *Session, page *dockerResourcesPage, key string) {
	page.show(locales.DockerDetails(), func(c *ui.Context, _ *dockerResourceDialog, _ *bool) {
		volume, found := lookupVolume(page, key)
		if !found {
			ui.Text(c, locales.DockerResStale())
			return
		}
		ui.Text(c, volume.Name).Bold().Selectable()
		usage, measured := page.volumes.usage.rows[key]
		users := volumeUsers(s)
		usedBy := strings.Join(users[key], "\n")
		if usedBy == "" && resourceListReason(&page.volumes.unused) == "" && !page.volumes.unusedVolume(key, users) {
			usedBy = locales.DockerSysActive()
		}
		if measured && usage.Links >= 0 {
			usedBy = resourceValue(usedBy) + " · " + locales.DockerResCount(usage.Links)
		}
		anonymous := yesNo(volume.Anonymous)
		if !volume.Anonymous && volume.LooksAnonymous() {
			anonymous = locales.DockerResVolumeMaybeAnonymous()
		}
		ctrRows(c, []labelValue{
			{locales.DockerResDriver(), volume.Driver}, {locales.DockerResScope(), volume.Scope}, {locales.DockerResVolumeMount(), volume.Mountpoint},
			{locales.DockerResVolumeAnonymous(), anonymous}, {locales.DockerSize(), resourceValue(usage.Size)},
			{locales.DockerResVolumeUsed(), resourceValue(usedBy)}, {locales.DockerCreated(), localTime(volume.Created)},
			{locales.DockerSecLabels(), resourcePairText(volume.Labels)}, {locales.DockerResOptions(), resourcePairText(volume.Options)},
		}, 130)
		reason := volumeBrowseReason(s, page, volume.Mountpoint)
		if ui.Button(c, locales.DockerResBrowse()).Disabled(reason != "").Tooltip(reason).Clicked() {
			a.browseDockerVolume(s, page, volume.Mountpoint)
		}
	})
}

func (a *App) browseDockerVolume(s *Session, page *dockerResourcesPage, mount string) {
	if volumeBrowseReason(s, page, mount) != "" {
		return
	}
	client, sftp := s.client, s.sftp
	page.volumes.browsing = true
	go func() {
		_, err := remotefs.ReadDir(sftp, mount)
		a.post(func() {
			if s.client != client || s.sftp != sftp || !slices.Contains(a.sessions, s) || s.status != statusConnected {
				return
			}
			page.volumes.browsing = false
			if a.connSession() != s || *a.shownBottomTab() != tabDocker || s.docker.section != dockerSecVolumes {
				return
			}
			if err != nil {
				page.show(locales.DockerResBrowse(), func(c *ui.Context, _ *dockerResourceDialog, open *bool) {
					ui.Text(c, locales.DockerResBrowseRoot()).TextColor(theme.Danger)
					ctrRows(c, []labelValue{{locales.DockerResVolumeMount(), mount}}, 130)
					ui.Text(c, err.Error()).Selectable()
					if ui.Button(c, locales.Close()).Clicked() {
						*open = false
					}
				})
				return
			}
			page.closeDialog()
			a.openBottomTab(tabFiles)
			a.openDir(s, mount)
		})
	}()
}

func (a *App) createDockerVolume(s *Session, page *dockerResourcesPage) {
	name, driver, options, labels := "", "local", "", ""
	page.show(locales.DockerResVolumeNew(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		resourceText(c, locales.FieldName(), &name)
		resourceText(c, locales.DockerResDriver(), &driver)
		resourceArea(c, locales.DockerResOptions(), &options, 76)
		resourceArea(c, locales.DockerResLabels(), &labels, 64)
		script, err := page.tool.VolumeCreate(strings.TrimSpace(name), strings.TrimSpace(driver), options, labels)
		reason := page.blocked(s)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResInvalidVolume())
			if name != "" {
				ui.Text(c, reason).TextColor(theme.Danger)
			}
		}
		if resourceButtons(c, open, locales.DockerResCreate(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}

func (a *App) confirmVolumeRemove(s *Session, page *dockerResourcesPage, volumes []monitor.DockerVolumeDetails, force bool) {
	title := locales.DockerResVolumeRemove()
	if force {
		title = locales.DockerResVolumeForce()
	}
	volumes = slices.Clone(volumes)
	slices.SortFunc(volumes, func(left, right monitor.DockerVolumeDetails) int { return strings.Compare(left.Name, right.Name) })
	page.show(title, func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerResVolumeWarning()).TextColor(theme.Danger)
		if force {
			ui.Text(c, locales.DockerResVolumeForceWarning()).Bold().TextColor(theme.Danger)
		}
		users := volumeUsers(s)
		reason := ""
		var latest []monitor.DockerVolumeDetails
		for _, volume := range volumes {
			ui.Text(c.Key(volume.Name), volume.Name).Selectable()
			if current, ok := lookupVolume(page, volume.Name); !ok || current.CreatedRaw != volume.CreatedRaw || current.Mountpoint != volume.Mountpoint || current.Driver != volume.Driver {
				reason = cmp.Or(reason, locales.DockerResStale())
			}
			reason = cmp.Or(reason, volumeRemoveReason(s, page, volume.Name, users, force))
			latest = append(latest, volume)
		}
		script, err := page.tool.VolumeRemove(force, latest...)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResStale())
		}
		if resourceButtons(c, open, title, reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}

func volumePruneReason(s *Session, page *dockerResourcesPage) string {
	if !page.tool.VolumePruneSupported(s.overview.docker.d.Version, s.overview.docker.d.APIVersion) {
		if page.tool.Tool == "podman" {
			return locales.DockerResPodman()
		}
		return locales.DockerResVolumeNoPrune()
	}
	return volumeBaseReason(s, page)
}

func (a *App) confirmVolumePrune(s *Session, page *dockerResourcesPage) {
	all, acknowledged := false, false
	confirmedVolumes := slices.Clone(page.volumes.rows)
	slices.SortFunc(confirmedVolumes, func(left, right monitor.DockerVolumeDetails) int { return strings.Compare(left.Name, right.Name) })
	page.show(locales.DockerResVolumePrune(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerResVolumeWarning()).Bold().TextColor(theme.Danger)
		if ui.Checkbox(c, &all, locales.DockerResVolumeAll()).Changed() {
			acknowledged = false
		}
		users := volumeUsers(s)
		var selected []monitor.DockerVolumeDetails
		for _, volume := range confirmedVolumes {
			current, found := lookupVolume(page, volume.Name)
			if found && current.CreatedRaw == volume.CreatedRaw && current.Driver == volume.Driver && current.Mountpoint == volume.Mountpoint &&
				page.volumes.unusedVolume(volume.Name, users) && (all || volume.Anonymous) && volumeRemoveReason(s, page, volume.Name, users, false) == "" {
				selected = append(selected, volume)
			}
		}
		ui.Text(c, locales.DockerSysEstimate()).TextColor(theme.TextMuted)
		ui.Text(c, locales.DockerResCount(len(selected))).Bold()
		var bytes uint64
		known := 0
		for _, volume := range selected {
			name := volume.Name
			size := page.volumes.usage.rows[name].Size
			ui.Text(c.Key(name), name+" · "+resourceValue(size)).Selectable()
			if size != "" && size != "N/A" {
				known++
				bytes += monitor.ParseSize(size)
			}
		}
		if known > 0 {
			ui.Text(c, formatSize(int64(bytes))+" · "+strconv.Itoa(known)+" / "+strconv.Itoa(len(selected))).Bold()
		}
		if all {
			ui.Checkbox(c, &acknowledged, locales.DockerSysCleanupAck())
		}
		reason := volumePruneReason(s, page)
		if len(selected) == 0 {
			reason = cmp.Or(reason, locales.DockerResNoCandidates())
		}
		if all && !acknowledged {
			reason = cmp.Or(reason, locales.DockerSysCleanupAck())
		}
		script, err := page.tool.VolumeRemove(false, selected...)
		if err != nil {
			reason = cmp.Or(reason, locales.DockerResStale())
		}
		if resourceButtons(c, open, locales.DockerResVolumePrune(), reason) {
			a.runDockerResource(s, page, dialog, dockerResourceStep{script: script})
		}
	})
}
