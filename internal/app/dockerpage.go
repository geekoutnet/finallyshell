package app

import (
	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The Docker tab of the bottom panel manages the engine of the
// connection's server, as a Docker panel does: its containers, images,
// Compose projects, networks, volumes and the engine itself, a section
// each, chosen in a row at the top. It reads the server as the Overview's
// Docker cards do (s.overview.docker, docker.go), and each section reads
// more of what it lists when it shows. The Overview keeps its Docker
// summary, whose button opens this tab.

// The sections of the Docker tab, in the order of its row.
const (
	dockerSecContainers = iota
	dockerSecImages
	dockerSecCompose
	dockerSecNetworks
	dockerSecVolumes
	dockerSecSystem
)

// dockerPage is a session's Docker tab: the section shown and the state
// of each, which their files keep.
type dockerPage struct {
	section    int
	containers dockerContainersPage // dockerpage_containers.go
	images     dockerImagesPage     // dockerpage_images.go
	compose    dockerComposePage    // dockerpage_compose.go
	resources  dockerResourcesPage  // dockerpage_resources.go: networks, volumes, system
}

// dockerSectionNames are the labels of the sections' row, by section.
func dockerSectionNames() []string {
	return []string{
		dockerSecContainers: locales.DockerContainers(),
		dockerSecImages:     locales.DockerImages(),
		dockerSecCompose:    locales.DockerPageCompose(),
		dockerSecNetworks:   locales.DockerNetworks(),
		dockerSecVolumes:    locales.DockerVolumes(),
		dockerSecSystem:     locales.DockerPageSystem(),
	}
}

// openDockerPage shows the Docker tab on section.
func (a *App) openDockerPage(s *Session, section int) {
	s.docker.section = section
	a.openBottomTab(tabDocker)
}

// dockerPanel is the Docker tab of the connection shown: why there is
// nothing to manage, or the sections.
func (a *App) dockerPanel(c *ui.Context) {
	s := a.connSession()
	ui.Column(c.Key("docker-page")).Grow(1).MinHeight(0).FillWidth().Background(colors.overviewPage).Children(func() {
		switch {
		case s == nil || s.isNewTab():
			overviewMessage(c, "unplug", locales.NotConnected(), nil)
		case s.isLocal():
			overviewMessage(c, "laptop", locales.LocalNotAvailable(), nil)
		case s.status != statusConnected:
			overviewMessage(c, "unplug", s.statusText(), func() {
				if s.status != statusConnecting && ui.Button(c, locales.Reconnect()).Clicked() {
					a.reconnect(s)
				}
			})
		case !execAllowed(s.Host):
			overviewMessage(c, "container", locales.SysInfoOff(), nil)
		default:
			a.dockerPageOf(c, s)
		}
	})
}

// dockerPageOf is the Docker tab of s, a connected server: the engine's
// state until it is read and answers, then the row of sections over the
// one chosen.
func (a *App) dockerPageOf(c *ui.Context, s *Session) {
	a.keepDockerRead(s)
	ds := &s.overview.docker
	d := &ds.d
	switch {
	case !ds.read && ds.err == nil:
		overviewMessage(c, "container", locales.DockerReading(), func() { ui.Spinner(c).Label(locales.DockerReading()) })
		return
	case !ds.read && ds.err != nil:
		overviewMessage(c, "container", locales.DockerFailed(describeSysInfoError(ds.err)), func() { a.dockerRetry(c, s) })
		return
	case d.Tool == "":
		overviewMessage(c, "container", locales.DockerPageNone(), func() { a.dockerRetry(c, s) })
		return
	case d.State == monitor.DockerNeedsRoot:
		overviewMessage(c, "container", locales.DockerPrivNeedsRoot(dockerName(d)), func() {
			if ds.err != nil {
				dockerProblem(c, locales.DockerFailed(describeSysInfoError(ds.err)), "")
			} else {
				widgets.Muted(c, locales.DockerPrivHint()).TextAlign(ui.Center).MaxWidth(520)
			}
			if ds.loading {
				ui.Spinner(c).Label(locales.DockerReading())
			} else {
				ui.Row(c).Justify(ui.Center).Children(func() { a.dockerPrivilegeButton(c, s) })
			}
		})
		return
	case d.State == monitor.DockerDenied:
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(12, 16).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(520).Gap(8).Children(func() {
					ui.Text(c, dockerDeniedText(d)).Bold()
					dockerPrivilegeHelp(c, s)
					a.dockerPrivilegeButton(c, s)
				})
			})
		})
		return
	case d.State == monitor.DockerDown:
		overviewMessage(c, "container", locales.DockerDown(), func() { a.dockerRetry(c, s) })
		return
	case d.State == monitor.DockerFailed:
		overviewMessage(c, "container", locales.DockerFailed(d.Error), func() { a.dockerRetry(c, s) })
		return
	}
	page := &s.docker
	ui.Row(c).FillWidth().Padding(6, 10).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.Icon(c, icons.Get("container")).FontSize(14).TextColor(theme.Accent).Shrink(0)
		ui.Text(c, dockerName(d)).Bold().SingleLine().Shrink(0)
		if d.Version != "" {
			widgets.Muted(c, d.Version).FontSize(12).SingleLine().Shrink(0)
		}
		ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Scrollbars(false).Children(func() {
			ui.Segmented(c, &page.section, dockerSectionNames()...).Label(locales.TabDocker())
		})
		if ds.err != nil {
			widgets.Muted(c, "!").TextColor(theme.Danger).Tooltip(locales.DockerFailed(describeSysInfoError(ds.err))).Shrink(0)
		}
		if ds.loading {
			ui.Box(c).Size(24, 24).Center().Shrink(0).Children(func() { ui.Spinner(c).Label(locales.DockerReading()) })
		} else if widgets.IconButton(c, "refresh-cw", locales.DockerRefresh()).Shrink(0).Clicked() {
			a.refreshDockerPage(s)
		}
	})
	ui.Column(c.Key(page.section)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch page.section {
		case dockerSecImages:
			a.dockerImagesSection(c, s)
		case dockerSecCompose:
			a.dockerComposeSection(c, s)
		case dockerSecNetworks:
			a.dockerNetworksSection(c, s)
		case dockerSecVolumes:
			a.dockerVolumesSection(c, s)
		case dockerSecSystem:
			a.dockerSystemSection(c, s)
		default:
			a.dockerContainersSection(c, s)
		}
	})
}

// dockerRetry is the button that reads the server's Docker again, under
// a message of why there is nothing to manage.
func (a *App) dockerRetry(c *ui.Context, s *Session) {
	if s.overview.docker.loading {
		ui.Spinner(c).Label(locales.DockerReading())
	} else if ui.Button(c, locales.DockerRefresh()).Clicked() {
		a.readDockerOf(s)
	}
}

// refreshDockerPage reads the server's Docker again, and what the
// section shown reads of its own.
func (a *App) refreshDockerPage(s *Session) {
	a.readDockerOf(s)
	switch s.docker.section {
	case dockerSecContainers:
		a.refreshDockerContainers(s)
	case dockerSecImages:
		a.refreshDockerImages(s)
	case dockerSecCompose:
		a.refreshDockerCompose(s)
	default:
		a.refreshDockerResources(s)
	}
}

// dockerSectionTodo is a section not built yet.
func dockerSectionTodo(c *ui.Context) {
	overviewMessage(c, "container", locales.DockerPageSoon(), nil)
}
