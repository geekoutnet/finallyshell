package app

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

const (
	dockerSystemOverview = iota
	dockerSystemEvents
	dockerSystemRegistries
)

type dockerSystemPage struct {
	mode                     int
	info                     dockerResourceLoad[monitor.DockerEngineInfo]
	disk                     dockerResourceLoad[[]monitor.DockerResourceDisk]
	registries               dockerResourceLoad[monitor.DockerRegistries]
	diskTable, registryTable dockerResourceTable
	events                   dockerSystemEventsPage
}

func (a *App) loadDockerSystem(s *Session, page *dockerResourcesPage, force bool) {
	cmd := page.tool
	readDockerResource(a, s, &page.system.disk, force, resourceOnce(cmd.ResourceDisk(false), monitor.ParseDockerResourceDisk))
	if cmd.Tool == "docker" {
		readDockerResource(a, s, &page.system.info, force, func(ctx context.Context, client *ssh.Client, run dockerRunner) (monitor.DockerEngineInfo, error) {
			var empty monitor.DockerEngineInfo
			info, err := run(ctx, client, cmd.EngineInfo(), nil)
			if err != nil {
				return empty, err
			}
			version, err := run(ctx, client, cmd.EngineVersion(), nil)
			if err != nil {
				return empty, err
			}
			return monitor.ParseDockerEngineInfo(info, version)
		})
	}
	if force && page.system.mode == dockerSystemRegistries {
		a.loadDockerRegistries(s, page, true)
	}
}

func (a *App) dockerSystemSection(c *ui.Context, s *Session) {
	page := a.dockerResources(s)
	system := &page.system
	a.loadDockerSystem(s, page, false)
	ui.Row(c).FillWidth().Gap(6).Padding(4, 8).AlignItems(ui.Center).Children(func() {
		if a.panelOnRight() && !a.current().isPanelTab() {
			ui.Text(c, locales.DockerPageSystem()).Bold().SingleLine().Shrink(0)
		}
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
			ui.Segmented(c, &system.mode, locales.DockerSysDisk(), locales.DockerSysEvents(), locales.DockerSysRegistries()).Label(locales.DockerPageSystem())
		})
		ui.Button(c, locales.DockerResManage()).Shrink(0).Menu(func(m *ui.Menu) {
			page.outputMenu(m)
			if m.Item(locales.DockerSysInfo()).Chosen() {
				a.openEngineInfo(s, page)
			}
			if resourceMenuItem(m, locales.DockerSysCleanup(), cleanupReason(s, page), true) {
				a.openDockerCleanup(s, page)
			}
			if resourceMenuItem(m, locales.DockerSysConfig(), daemonUnavailable(s, page), false) {
				a.openDaemonConfig(s, page)
			}
		})
	})
	ui.Column(c.Key(system.mode)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch system.mode {
		case dockerSystemEvents:
			a.dockerEventsView(c, s, page)
		case dockerSystemRegistries:
			a.dockerRegistryView(c, s, page)
		default:
			a.dockerDiskView(c, s, page)
		}
	})
	if system.mode == dockerSystemEvents {
		a.keepDockerEvents(s, page)
	} else {
		a.stopPoll(&system.events.poll)
	}
	page.drawDialog(c)
}

func (a *App) dockerDiskView(c *ui.Context, s *Session, page *dockerResourcesPage) {
	section := &page.system
	diskCols := []ui.TableColumn{
		resourceColumn("type", locales.DockerSysType(), 115), resourceColumn("total", locales.DockerSysTotal(), 65),
		resourceColumn("active", locales.DockerSysActive(), 70), resourceColumn("size", locales.DockerSize(), 82),
		resourceColumn("reclaimable", locales.DockerSysReclaimable(), 124),
	}
	var rows []dockerResourceRow
	for _, item := range section.disk.rows {
		kind := dockerDiskLabel(item.Type)
		parseInt := func(text string) int64 { number, _ := strconv.ParseInt(text, 10, 64); return number }
		rows = append(rows, dockerResourceRow{item.Type, kind, []string{kind, item.Total, item.Active, item.Size, item.Reclaimable}, map[string]int64{
			"total": parseInt(item.Total), "active": parseInt(item.Active), "size": int64(monitor.ParseSize(item.Size)), "reclaimable": int64(monitor.ParseSize(reclaimableSize(item.Reclaimable))),
		}})
	}
	if section.diskTable.sort.Column != "" || section.diskTable.query != "" {
		rows = section.diskTable.shown(rows, diskCols)
	}
	resourceToolbar(c, &section.diskTable, len(rows), func() {
		if ui.Button(c, locales.DockerSysInfo()).Shrink(0).Clicked() {
			a.openEngineInfo(s, page)
		}
		reason := cleanupReason(s, page)
		if ui.PrimaryButton(c, locales.DockerSysCleanup()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
			a.openDockerCleanup(s, page)
		}
	})
	resourceStatus(c, &section.disk, func() { a.loadDockerSystem(s, page, true) })
	section.diskTable.draw(c, locales.DockerSysDisk(), diskCols, rows, nil, nil)
}

func dockerDiskLabel(kind string) string {
	switch strings.ToLower(kind) {
	case "images":
		return locales.DockerImages()
	case "containers":
		return locales.DockerContainers()
	case "local volumes", "volumes":
		return locales.DockerVolumes()
	case "build cache":
		return locales.DockerSysCache()
	default:
		return kind
	}
}

func (a *App) openEngineInfo(s *Session, page *dockerResourcesPage) {
	page.show(locales.DockerSysInfo(), func(c *ui.Context, _ *dockerResourceDialog, open *bool) {
		info := &page.system.info
		engine := &s.overview.docker.d
		resourceStatus(c, info, func() { a.loadDockerSystem(s, page, true) })
		values := info.rows
		if !info.read {
			values = monitor.DockerEngineInfo{
				Server: monitor.DockerEngineVersion{Version: engine.Version, APIVersion: engine.APIVersion}, OS: engine.OS,
				Driver: engine.StorageDriver, RootDir: engine.RootDir, CgroupDriver: engine.CgroupDriver, CgroupVersion: engine.CgroupVersion,
				LoggingDriver: engine.LoggingDriver, Memory: engine.MemTotal, CPUs: engine.CPUs, Warnings: engine.Warnings,
				Containers: engine.NumContainers, Running: engine.Running, Paused: engine.Paused, Stopped: engine.Stopped, Images: engine.NumImages,
			}
		}
		joined := func(values ...string) string {
			return strings.Join(slices.DeleteFunc(values, func(value string) bool { return value == "" }), " · ")
		}
		ctrSection(c, dockerName(engine), nil, func() {
			ctrRows(c, []labelValue{
				{locales.DockerSysClient(), joined(values.Client.Version, values.Client.APIVersion)}, {locales.DockerSysServer(), joined(values.Server.Version, values.Server.APIVersion)},
				{locales.DockerSysPlatform(), joined(cmp.Or(values.OS, values.Server.OS), values.Kernel, cmp.Or(values.Architecture, values.Server.Arch))},
				{locales.DockerSysCPU(), joined(strconv.Itoa(values.CPUs), formatSize(int64(values.Memory)))},
				{locales.DockerContainers(), locales.DockerContainerCounts(values.Containers, values.Running, values.Paused, values.Stopped)},
				{locales.DockerImages(), strconv.Itoa(values.Images)},
			}, 145)
		})
		ctrSection(c, locales.DockerStorage(), nil, func() {
			live := ""
			if info.read {
				live = yesNo(values.LiveRestore)
			}
			ctrRows(c, []labelValue{
				{locales.DockerStorage(), values.Driver}, {locales.DockerSysRoot(), values.RootDir},
				{locales.DockerSysCgroup(), joined(values.CgroupDriver, values.CgroupVersion)}, {locales.DockerSysRuntime(), strings.Join(values.Runtimes, ", ")},
				{locales.DockerSysDefaultRuntime(), values.DefaultRuntime}, {locales.DockerSysLogging(), values.LoggingDriver}, {locales.DockerSysLive(), live},
			}, 145)
		})
		ctrSection(c, locales.DockerSysRegistries(), nil, func() {
			ctrRows(c, []labelValue{
				{locales.DockerSysMirrors(), strings.Join(values.Mirrors, "\n")}, {locales.DockerSysInsecure(), strings.Join(values.Insecure, "\n")},
				{locales.DockerSysProxies(), joined(values.HTTPProxy, values.HTTPSProxy)},
			}, 145)
		})
		if len(values.Warnings) > 0 {
			ctrSection(c, locales.DockerSysWarnings(), nil, func() { ui.Text(c, strings.Join(values.Warnings, "\n")).Selectable() })
		}
		if ui.Button(c, locales.Close()).Clicked() {
			*open = false
		}
	})
}

func reclaimableSize(value string) string {
	size, _, _ := strings.Cut(value, " (")
	return strings.TrimSpace(size)
}

func cleanupReason(s *Session, page *dockerResourcesPage) string {
	if reason := page.blocked(s); reason != "" {
		return reason
	}
	if page.tool.Tool == "podman" {
		return locales.DockerResPodman()
	}
	return ""
}

type dockerCleanupChoice struct {
	label, kind, estimate string
	all                   bool
	identities            []string
	unsupported           string
	networks              []monitor.DockerNetworkDetails
	volumes               []monitor.DockerVolumeDetails
}

func cleanupChoices(s *Session, page *dockerResourcesPage) []dockerCleanupChoice {
	estimate := func(kind string) string {
		for _, row := range page.system.disk.rows {
			if strings.EqualFold(row.Type, kind) {
				return row.Reclaimable
			}
		}
		return ""
	}
	docker := &s.overview.docker.d
	var stopped, dangling, networkNames, volumeNames []string
	for _, item := range docker.Containers {
		if slices.Contains([]string{"created", "exited", "stopped", "dead", "configured"}, item.Status) {
			stopped = append(stopped, item.Name)
		}
	}
	for _, image := range docker.Images {
		if image.Repository == "<none>" && image.Tag == "<none>" {
			dangling = append(dangling, image.ID)
		}
	}
	var unusedImages []string
	for _, image := range docker.Images {
		ref := strings.TrimPrefix(image.ID, "sha256:")
		if ref == "" || slices.ContainsFunc(docker.Containers, func(container monitor.Container) bool {
			used := strings.TrimPrefix(container.ImageID, "sha256:")
			return used != "" && strings.HasPrefix(used, ref)
		}) {
			continue
		}
		label := image.ID
		if image.Repository != "" && image.Repository != "<none>" {
			label = image.Repository + ":" + image.Tag
		}
		if !slices.Contains(unusedImages, label) {
			unusedImages = append(unusedImages, label)
		}
	}
	networks := networkCandidates(s, page.networks.rows)
	for _, network := range networks {
		networkNames = append(networkNames, network.Name)
	}
	var anonymousBytes uint64
	measured := 0
	users := volumeUsers(s)
	var volumes []monitor.DockerVolumeDetails
	for _, volume := range page.volumes.rows {
		if volume.Anonymous && page.volumes.unusedVolume(volume.Name, users) && !volume.Created.IsZero() && volume.CreatedRaw != "" {
			volumes = append(volumes, volume)
			volumeNames = append(volumeNames, volume.Name)
			if size := page.volumes.usage.rows[volume.Name].Size; size != "" && size != "N/A" {
				anonymousBytes += monitor.ParseSize(size)
				measured++
			}
		}
	}
	volumeEstimate := ""
	if measured > 0 {
		volumeEstimate = formatSize(int64(anonymousBytes)) + " · " + strconv.Itoa(measured) + " / " + strconv.Itoa(len(volumes))
	}
	volumeReason := ""
	if !page.tool.VolumePruneSupported(docker.Version, docker.APIVersion) {
		volumeReason = locales.DockerResVolumeNoPrune()
	}
	networkReason := resourceListReason(&page.networks.dockerResourceLoad)
	volumeReason = cmp.Or(volumeReason, resourceListReason(&page.volumes.dockerResourceLoad), resourceListReason(&page.volumes.unused))
	if len(networks) == 0 {
		networkReason = cmp.Or(networkReason, locales.DockerResNoCandidates())
	}
	if len(volumes) == 0 {
		volumeReason = cmp.Or(volumeReason, locales.DockerResNoCandidates())
	}
	return []dockerCleanupChoice{
		{label: locales.DockerSysStopped(), kind: "container", estimate: estimate("Containers"), identities: stopped},
		{label: locales.DockerSysDangling(), kind: "image", estimate: estimate("Images"), identities: dangling},
		{label: locales.DockerSysAllImages(), kind: "image", estimate: estimate("Images"), all: true, identities: unusedImages},
		{label: locales.DockerSysUnusedNets(), kind: "network", identities: networkNames, unsupported: networkReason, networks: networks},
		{label: locales.DockerSysUnusedVols(), kind: "volume", estimate: volumeEstimate, identities: volumeNames, unsupported: volumeReason, volumes: volumes},
		{label: locales.DockerSysCache(), kind: "builder", estimate: estimate("Build Cache")},
	}
}

func (a *App) openDockerCleanup(s *Session, page *dockerResourcesPage) {
	a.loadDockerNetworks(s, page, false)
	a.loadDockerVolumes(s, page, false)
	a.loadDockerVolumeUsage(s, page, false)
	var selected [6]bool
	ack := false
	var acknowledged []string
	page.show(locales.DockerSysCleanup(), func(c *ui.Context, dialog *dockerResourceDialog, open *bool) {
		ui.Text(c, locales.DockerSysCleanupWarning()).Bold().TextColor(theme.Danger)
		widgets.Muted(c, locales.DockerSysEstimate()).FontSize(12)
		choices := cleanupChoices(s, page)
		var steps []dockerResourceStep
		for index, choice := range choices {
			if selected[index] && choice.unsupported != "" {
				selected[index], ack = false, false
			}
			ui.Column(c.Key(index)).Gap(1).FillWidth().Children(func() {
				label := choice.label + " · " + resourceValue(choice.estimate)
				if choice.kind == "network" && page.networks.read {
					label = choice.label + " · " + locales.DockerResCount(len(choice.identities))
				}
				if ui.Checkbox(c, &selected[index], label).Disabled(choice.unsupported != "").Tooltip(choice.unsupported).Changed() {
					ack = false
					if selected[index] && index == 1 {
						selected[2] = false
					}
					if selected[index] && index == 2 {
						selected[1] = false
					}
				}
				if choice.unsupported != "" && choice.unsupported != locales.DockerResStale() {
					widgets.Muted(c, choice.unsupported).FontSize(11)
				}
				if selected[index] && len(choice.identities) > 0 {
					slices.Sort(choice.identities)
					label := strings.Join(choice.identities, ", ")
					if choice.kind == "container" || choice.kind == "image" {
						label = locales.DockerSysPreview() + ": " + label
					}
					widgets.Muted(c, label).FontSize(11).Selectable()
				}
			})
		}
		var summary []string
		var unavailable bool
		for index, choice := range choices {
			if !selected[index] {
				continue
			}
			if choice.unsupported != "" {
				unavailable = true
				continue
			}
			var script string
			var err error
			switch choice.kind {
			case "network":
				script, err = page.tool.NetworkRemove(choice.networks...)
			case "volume":
				script, err = page.tool.VolumeRemove(false, choice.volumes...)
			default:
				script, err = page.tool.ResourcePrune(choice.kind, choice.all, s.overview.docker.d.Version)
			}
			if err == nil {
				steps = append(steps, dockerResourceStep{label: choice.label, script: script})
				estimate := resourceValue(choice.estimate)
				if choice.kind == "network" {
					estimate = locales.DockerResCount(len(choice.networks))
				}
				summary = append(summary, choice.label+" · "+estimate)
			} else {
				unavailable = true
			}
		}
		if len(summary) > 0 {
			ui.Text(c, strings.Join(summary, "\n")).Bold()
		}
		scripts := make([]string, 0, len(steps))
		for _, step := range steps {
			scripts = append(scripts, step.script)
		}
		if ack && !slices.Equal(acknowledged, scripts) {
			ack = false
		}
		if ui.Checkbox(c, &ack, locales.DockerSysCleanupAck()).Changed() && ack {
			acknowledged = scripts
		}
		reason := cleanupReason(s, page)
		if unavailable {
			reason = cmp.Or(reason, locales.DockerResStale())
		}
		if !ack || len(steps) == 0 {
			reason = cmp.Or(reason, locales.DockerSysCleanupAck())
		}
		if resourceButtons(c, open, locales.DockerSysCleanup(), reason) {
			a.runDockerResource(s, page, dialog, steps...)
		}
	})
}
