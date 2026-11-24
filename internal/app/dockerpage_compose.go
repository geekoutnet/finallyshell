package app

import (
	"cmp"
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerComposePage struct {
	client                *ssh.Client
	cli                   monitor.ComposeCLI
	reading, read         bool
	err, listErr          error
	readID, serviceReadID uint64
	at                    time.Time
	frame                 uint64
	list                  []monitor.ComposeProject
	managed               []composeRegistration
	managedErr            error
	project, filter       string
	serviceReadFor        string
	serviceReading        bool
	serviceErr            error
	serviceRows           []monitor.Container
	projectRow            int
	projectTable, table   ui.ListState
	selected              ui.Selection[string]
	sort                  ui.SortOrder
	dialog                *composeDialog
	work                  *composeWork
	edit                  *composeEditOffer
	files                 composeFileStore
}

func composeContext(s *Session) context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func composeSessionReady(s *Session) bool {
	state := &s.overview.docker
	return s.status == statusConnected && execAllowed(s.Host) && state.dataClient == s.client &&
		state.d.Tool != "" && state.d.State == monitor.DockerOK && (s.client != nil || state.run != nil)
}

func composeRunner(s *Session) dockerRunner {
	if run := s.overview.docker.run; run != nil {
		return run
	}
	return func(ctx context.Context, client *ssh.Client, script string, report func(string)) (string, error) {
		if report != nil {
			return monitor.RunComposeStream(ctx, client, script, report)
		}
		return monitor.RunDocker(ctx, client, script)
	}
}

func (a *App) refreshDockerCompose(s *Session) {
	if !composeSessionReady(s) {
		return
	}
	page := &s.docker.compose
	client, engine, run, registry := s.client, monitor.CmdOf(&s.overview.docker.d), composeRunner(s), composeRegistryPath(s)
	if page.client != client || page.cli.Engine != engine {
		if page.work != nil {
			page.work.cancel()
			if s.overview.docker.work == &page.work.view {
				s.overview.docker.work = nil
			}
		}
		*page = dockerComposePage{client: client, cli: monitor.ComposeCLI{Engine: engine}, readID: page.readID, serviceReadID: page.serviceReadID}
	}
	page.readID++
	readID := page.readID
	page.reading, page.at = true, time.Now()
	page.serviceReadID++
	page.serviceReadFor, page.serviceErr, page.serviceRows, page.serviceReading = "", nil, nil, false
	ctx, cancel := context.WithTimeout(composeContext(s), dockerTimeout)
	go func() {
		defer cancel()
		read := func(ctx context.Context, script string) (string, error) { return run(ctx, client, script, nil) }
		cli, err := monitor.DetectCompose(ctx, engine, read)
		var list []monitor.ComposeProject
		var listErr error
		if script := cli.ListScript(); err == nil && script != "" {
			output, runErr := read(ctx, script)
			listErr = runErr
			if runErr == nil {
				list, listErr = monitor.ParseComposeProjects(output)
			}
		}
		managed, managedErr := readComposeRegistry(registry)
		a.post(func() {
			if page.readID != readID || s.client != client || !slices.Contains(a.sessions, s) {
				return
			}
			page.reading, page.read, page.err, page.listErr = false, true, err, listErr
			page.cli, page.list, page.managed, page.managedErr = cli, list, managed, managedErr
		})
	}()
}

func (page *dockerComposePage) projects(data *monitor.Docker) []monitor.ComposeProject {
	projects := monitor.MergeComposeProjects(page.list, monitor.ComposeProjectsFromContainers(data.Containers))
	for _, entry := range page.managed {
		index := slices.IndexFunc(projects, func(project monitor.ComposeProject) bool { return project.Project == entry.Project })
		if index < 0 {
			projects = append(projects, monitor.ComposeProject{Compose: entry.compose()})
		} else if projects[index].Dir == entry.Dir && len(projects[index].Files) == 0 {
			projects[index].Compose = entry.compose()
		}
	}
	slices.SortFunc(projects, func(left, right monitor.ComposeProject) int { return cmp.Compare(left.Project, right.Project) })
	return projects
}

func (a *App) readComposeServices(s *Session, project monitor.ComposeProject) {
	page := &s.docker.compose
	key := project.Project + "\n" + project.Dir + "\n" + strings.Join(project.Files, "\n")
	if page.serviceReadFor == key || page.reading || page.cli.Flavor != monitor.ComposeV2 || !composeSessionReady(s) {
		return
	}
	page.serviceReadID++
	page.serviceReadFor, page.serviceErr, page.serviceRows = key, nil, nil
	script, err := page.cli.Script(project.Compose, monitor.ComposePS, monitor.ComposeOptions{})
	if err != nil {
		return
	}
	page.serviceReading = true
	readID, client, run := page.serviceReadID, s.client, composeRunner(s)
	ctx, cancel := context.WithTimeout(composeContext(s), dockerTimeout)
	go func() {
		defer cancel()
		output, err := run(ctx, client, script, nil)
		var rows []monitor.Container
		if err == nil {
			rows, err = monitor.ParseComposeServices(output, project.Project)
		}
		a.post(func() {
			if page.serviceReadID != readID || s.client != client || !slices.Contains(a.sessions, s) {
				return
			}
			page.serviceReading, page.serviceRows, page.serviceErr = false, rows, err
		})
	}()
}

func (page *dockerComposePage) pick(project string) {
	if page.project != project {
		page.project = project
		page.selected.Clear()
		page.serviceReadID++
		page.serviceReadFor, page.serviceErr, page.serviceRows, page.serviceReading = "", nil, nil, false
	}
}

func (a *App) dockerComposeSection(c *ui.Context, s *Session) {
	page, engine := &s.docker.compose, &s.overview.docker.d
	if composeSessionReady(s) && ((!page.read && !page.reading) || page.client != s.client || page.cli.Engine != monitor.CmdOf(engine) ||
		page.frame+1 < a.frame && !page.reading && time.Since(page.at) > dockerStale) {
		a.refreshDockerCompose(s)
	}
	page.frame = a.frame
	projects := page.projects(engine)
	if !slices.ContainsFunc(projects, func(project monitor.ComposeProject) bool { return project.Project == page.project }) {
		page.pick("")
		if len(projects) != 0 {
			page.pick(projects[0].Project)
		}
	}
	const narrowWidth = 670
	width, _ := c.Size()
	box := ui.Column(c.Key("compose-section")).Grow(1).MinHeight(0).FillWidth()
	if measured := box.LayoutBounds().W; measured > 0 {
		width = measured
	}
	box.Children(func() {
		a.composeToolbar(c, s, len(projects), width < narrowWidth)
		if len(projects) == 0 {
			message := locales.DockerCmpEmpty()
			if page.reading {
				message = locales.DockerReading()
			} else if note := composeReadNote(s); note != "" && page.cli.Flavor == "" {
				message = note
			}
			overviewMessage(c, "container", message, nil)
			return
		}
		project := projects[slices.IndexFunc(projects, func(project monitor.ComposeProject) bool { return project.Project == page.project })]
		a.readComposeServices(s, project)
		if width < narrowWidth {
			ui.Row(c).FillWidth().Padding(5, 8).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.MenuButton(c, project.Project+" · "+composeProjectStatus(project), func(menu *ui.Menu) {
					for _, choice := range projects {
						if menu.Item(choice.Project).Subtitle(composeProjectStatus(choice)).Chosen() {
							page.pick(choice.Project)
						}
					}
				}).Label(locales.DockerCmpProjects()).Grow(1).MinWidth(0)
			})
			a.composeProjectView(c, s, project)
		} else {
			ui.Row(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				a.composeProjectList(c, s, projects)
				a.composeProjectView(c, s, project)
			})
		}
	})
	if page.dialog != nil {
		a.composeDialogView(c, s, page.dialog)
	}
}

func (a *App) composeToolbar(c *ui.Context, s *Session, count int, narrow bool) {
	page := &s.docker.compose
	ui.Row(c).FillWidth().Padding(5, 8).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		ui.SearchField(c, &page.filter).Label(locales.DockerCmpFilter()).Placeholder(locales.DockerCmpFilter()).Grow(1).MaxWidth(220).MinWidth(70)
		label := locales.DockerCmpProjectCount(count)
		widgets.Muted(c, label).FontSize(12).SingleLine().Shrink(0)
		ui.Spacer(c)
		if page.reading {
			ui.Spinner(c).Label(locales.DockerReading()).Shrink(0)
		} else {
			message := composeReadNote(s)
			if message != "" {
				widgets.Muted(c, message).FontSize(12).SingleLine().Shrink(1).MinWidth(0).Tooltip(message)
			} else if page.cli.Flavor != "" && !narrow {
				widgets.Muted(c, string(page.cli.Flavor)).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
			}
		}
		reason := composeUnavailable(s)
		if page.managedErr != nil && reason == "" {
			reason = locales.DockerCmpRegistryFailed()
		}
		if ui.PrimaryButton(c, locales.DockerCmpNew()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
			a.openComposeNew(s)
		}
	})
}

func composeReadNote(s *Session) string {
	page := &s.docker.compose
	switch {
	case page.err != nil:
		return locales.DockerFailed(describeSysInfoError(page.err))
	case page.read && page.cli.Flavor == "":
		return composeMissing(s)
	case page.managedErr != nil:
		return locales.DockerCmpRegistryFailed()
	case page.listErr != nil:
		return locales.DockerCmpLabelsFallback()
	}
	return ""
}

func composeMissing(s *Session) string {
	if s.overview.docker.d.Tool == "podman" {
		return locales.DockerCmpNoPodman()
	}
	return locales.DockerCmpNoDocker()
}

func composeProjectStatus(project monitor.ComposeProject) string {
	switch {
	case project.Total == 0:
		return locales.DockerCmpNotDeployed()
	case project.Running == 0:
		return locales.DockerCmpProjectStopped(project.Total)
	default:
		return locales.DockerCmpRunning(project.Running, project.Total)
	}
}

func (a *App) composeProjectList(c *ui.Context, s *Session, projects []monitor.ComposeProject) {
	page := &s.docker.compose
	page.projectRow = slices.IndexFunc(projects, func(project monitor.ComposeProject) bool { return project.Project == page.project })
	table := &page.projectTable
	table.Selected = &page.projectRow
	table.Key = func(index int) any { return projects[index].Project }
	table.Label = func(index int) string { return projects[index].Project }
	table.CellMenu = func(menu *ui.Menu, row, _ int) {
		if row >= 0 && row < len(projects) {
			a.composeMenu(menu, s, projects[row], nil)
		}
	}
	columns := []ui.TableColumn{{ID: "project", Title: locales.DockerCmpProjects(), MinWidth: 166}}
	ui.Table(c, table, columns, len(projects), func(row, _ int) {
		project := projects[row]
		color := theme.TextMuted
		if project.Running > 0 {
			color = ctrColor(&monitor.Container{Status: "running"})
			if project.Running < project.Total {
				color = ctrColor(&monitor.Container{Status: "paused", Paused: true})
			}
		}
		ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(color).Shrink(0)
			ui.Text(c, project.Project).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(project.Project)
			status := ui.Text(c, composeProjectStatus(project)).FontSize(11).SingleLine().Shrink(0)
			if row != page.projectRow {
				status.TextColor(theme.TextMuted)
			}
		})
	}).Width(236).Shrink(0).AlignSelf(ui.Stretch).MinHeight(0).Label(locales.DockerCmpProjects())
	if page.projectRow >= 0 && page.projectRow < len(projects) && page.project != projects[page.projectRow].Project {
		page.pick(projects[page.projectRow].Project)
		c.Invalidate()
	}
}

func composeServiceRows(page *dockerComposePage, project monitor.ComposeProject) []monitor.Container {
	rows := project.Services
	if page.serviceRows != nil && page.serviceReadFor != "" && !page.serviceReading && page.serviceErr == nil {
		rows = slices.Clone(page.serviceRows)
		for index, service := range rows {
			if service.Project != project.Project {
				return project.Services
			}
			for _, label := range project.Services {
				if service.ID != label.ID && !(len(service.ID) >= 12 && strings.HasPrefix(label.ID, service.ID)) {
					continue
				}
				if service.Image == "" {
					service.Image = label.Image
				}
				if service.Created.IsZero() {
					service.Created = label.Created
				}
				if service.Health == "" {
					service.Health = label.Health
				}
				service.ID, service.StartedAt, service.ExitCode, service.FinishedAt = label.ID, label.StartedAt, label.ExitCode, label.FinishedAt
				rows[index] = service
				break
			}
		}
	}
	return rows
}

func composeServiceCells(ct monitor.Container, now time.Time) [6]string {
	created := ""
	if !ct.Created.IsZero() {
		created = ct.Created.Local().Format(time.DateTime)
	}
	status := ctrStatusText(&ct, now)
	if ct.Status == "running" && ct.StartedAt.IsZero() {
		status = locales.DockerCmpServiceRunning()
		if ct.Health != "" {
			status = locales.DockerCmpServiceHealth(healthText(ct.Health))
		}
	} else if ct.Status == "exited" && ct.FinishedAt.IsZero() && ct.ExitCode == 0 {
		status = locales.DockerCmpStopped()
	}
	return [6]string{ct.Service, ct.Name, status, ct.Image, strings.Join(append(portTexts(ct.Ports), ct.Exposed...), ", "), created}
}

func composeVisibleServices(rows []monitor.Container, filter string, order ui.SortOrder, now time.Time) []monitor.Container {
	filter = strings.ToLower(strings.TrimSpace(filter))
	var visible []monitor.Container
	for _, ct := range rows {
		cells := composeServiceCells(ct, now)
		if filter == "" || strings.Contains(strings.ToLower(strings.Join(cells[:], " ")), filter) {
			visible = append(visible, ct)
		}
	}
	slices.SortStableFunc(visible, func(left, right monitor.Container) int {
		var result int
		switch order.Column {
		case "name":
			result = cmp.Compare(left.Name, right.Name)
		case "state":
			result = cmp.Compare(ctrRank(&left), ctrRank(&right))
		case "image":
			result = cmp.Compare(left.Image, right.Image)
		case "ports":
			result = cmp.Compare(composeServiceCells(left, now)[4], composeServiceCells(right, now)[4])
		case "created":
			result = left.Created.Compare(right.Created)
		default:
			result = cmp.Compare(left.Service, right.Service)
		}
		if result == 0 {
			return cmp.Compare(left.Name, right.Name)
		}
		if order.Descending {
			return -result
		}
		return result
	})
	return visible
}

func (a *App) composeProjectView(c *ui.Context, s *Session, project monitor.ComposeProject) {
	page := &s.docker.compose
	rows := composeServiceRows(page, project)
	project.Services = rows
	ui.Column(c.Key(project.Project)).Grow(1).FillHeight().MinHeight(0).MinWidth(0).BorderWidth(0, 0, 0, 1).BorderColor(colors.line).Children(func() {
		ui.Row(c).FillWidth().Padding(5, 8).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Text(c, project.Project).Bold().SingleLine().Shrink(1).MinWidth(0).Tooltip(project.Project)
			widgets.Muted(c, composeProjectStatus(project)).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
			ui.Spacer(c)
			a.composeVisibleActions(c, s, project)
		})
		ui.Row(c).FillWidth().Padding(0, 8, 5).Gap(8).AlignItems(ui.Center).Children(func() {
			files := slices.Clone(project.Files)
			for index, file := range files {
				files[index] = strings.TrimPrefix(file, strings.TrimSuffix(project.Dir, "/")+"/")
			}
			metadata := strings.Join(append([]string{project.Dir}, files...), " · ")
			if project.Dir == "" {
				metadata = locales.DockerCmpMetadata()
			}
			widgets.Muted(c, metadata).Grow(1).MinWidth(0).SingleLine().FontSize(11).Tooltip(metadata)
			if page.serviceReading {
				ui.Spinner(c).Label(locales.DockerReading()).Shrink(0)
			} else if page.serviceErr != nil {
				widgets.Muted(c, locales.DockerCmpLabelsFallback()).FontSize(11).SingleLine().Shrink(1).MinWidth(0)
			}
		})
		a.composeServiceTable(c, s, project)
	})
}

func (a *App) composeServiceTable(c *ui.Context, s *Session, project monitor.ComposeProject) {
	page, now := &s.docker.compose, c.Now()
	rows := composeVisibleServices(project.Services, page.filter, page.sort, now)
	page.table.Selection, page.table.Sort = &page.selected, &page.sort
	page.table.Key = func(index int) any { return rows[index].ID }
	page.table.Label = func(index int) string { return rows[index].Name }
	page.table.CellMenu = func(menu *ui.Menu, row, _ int) {
		if row < 0 || row >= len(rows) {
			return
		}
		selected := composeSelected(page, rows)
		if !page.selected.Has(rows[row].ID) || len(selected) == 0 {
			selected = []monitor.Container{rows[row]}
		}
		a.composeMenu(menu, s, project, selected)
	}
	columns := []ui.TableColumn{
		{ID: "service", Title: locales.DockerCmpService(), Width: 84, Sortable: true},
		{ID: "name", Title: locales.DockerColName(), Width: 129, Sortable: true},
		{ID: "state", Title: locales.DockerColStatus(), Width: 130, Sortable: true},
		{ID: "image", Title: locales.DockerColImage(), Width: 123, Sortable: true},
		{ID: "ports", Title: locales.DockerColPorts(), Width: 167, Sortable: true},
		{ID: "created", Title: locales.DockerCmpCreated(), MinWidth: 144, Sortable: true},
	}
	if page.sort.Column == "" {
		page.sort.Column = "service"
	}
	ui.Table(c, &page.table, columns, len(rows), func(row, col int) {
		ct := rows[row]
		text := orDash(composeServiceCells(ct, now)[col])
		cell := ui.Text(c, text).SingleLine().Tooltip(text)
		if col == 2 {
			cell.TextColor(ctrColor(&ct))
		}
	}).Grow(1).MinHeight(0).MinWidth(0).Label(locales.DockerCmpServices())
}

func composeSelected(page *dockerComposePage, rows []monitor.Container) []monitor.Container {
	var selected []monitor.Container
	for _, row := range rows {
		if page.selected.Has(row.ID) {
			selected = append(selected, row)
		}
	}
	return selected
}

func composeFilePath(project monitor.ComposeProject, file string) string {
	if path.IsAbs(file) {
		return path.Clean(file)
	}
	return path.Join(project.Dir, file)
}

func composeOutputError(err error) string {
	if errors.Is(err, context.Canceled) {
		return locales.DockerCmpCanceled()
	}
	return err.Error()
}
