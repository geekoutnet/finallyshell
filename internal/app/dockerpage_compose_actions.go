package app

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"golang.org/x/crypto/ssh"
)

type composeDialog struct {
	client          *ssh.Client
	project         monitor.ComposeProject
	services        []monitor.Container
	action          monitor.ComposeAction
	options         monitor.ComposeOptions
	create, delete  bool
	removeDirectory bool
	name, dir, env  string
	typed           string
	code            *editor.State
	deploy          bool
	checking        bool
	directory       *composeDirectory
	validation      string
	volumeNames     []string
	volumeState     string
	volumeRead      bool
	work            *composeWork
}

type composeWork struct {
	view       dockerWork
	cancel     context.CancelFunc
	stopping   bool
	privileged bool
	written    bool
}

type composeEditOffer struct {
	tab     *editTab
	project string
	before  time.Time
}

func composeUnavailable(s *Session) string {
	page := &s.docker.compose
	switch {
	case !composeSessionReady(s) || page.client != s.client:
		return locales.DockerUnavailable()
	case page.work != nil || s.overview.docker.work != nil:
		return locales.DockerCmpBusy()
	case page.reading:
		return locales.DockerReading()
	case page.cli.Flavor == "":
		return composeMissing(s)
	}
	return ""
}

func composeNames(rows []monitor.Container) []string {
	var names []string
	for _, row := range rows {
		if !slices.Contains(names, row.Service) {
			names = append(names, row.Service)
		}
	}
	slices.Sort(names)
	return names
}

func composeActionReason(s *Session, project monitor.ComposeProject, action monitor.ComposeAction, rows []monitor.Container) string {
	if reason := composeUnavailable(s); reason != "" {
		return reason
	}
	if monitor.CheckComposeProject(project.Compose) != nil || slices.ContainsFunc(composeNames(rows), func(name string) bool { return !monitor.ValidName(name) }) {
		return locales.DockerCmpMetadata()
	}
	switch action {
	case monitor.ComposeStart, monitor.ComposeStop, monitor.ComposeRestart:
		if len(rows) == 0 {
			rows = project.Services
		}
		eligible := func(ct monitor.Container) bool {
			tool := s.overview.docker.d.Tool
			switch action {
			case monitor.ComposeStart:
				return dockerStart.allowed(&ct, tool)
			case monitor.ComposeStop:
				return dockerStop.allowed(&ct, tool)
			default:
				return dockerRestart.allowed(&ct, tool)
			}
		}
		if !slices.ContainsFunc(rows, eligible) {
			return locales.DockerCmpServiceState()
		}
	case monitor.ComposeLogs:
		if len(rows) == 0 && project.Total == 0 && len(project.Services) == 0 {
			return locales.DockerCmpServiceState()
		}
	}
	return ""
}

func composeActionLabel(action monitor.ComposeAction) string {
	switch action {
	case monitor.ComposeUp:
		return locales.DockerCmpDeploy()
	case monitor.ComposeStart:
		return locales.DockerStart()
	case monitor.ComposeStop:
		return locales.DockerStop()
	case monitor.ComposeRestart:
		return locales.DockerRestart()
	case monitor.ComposePull:
		return locales.DockerCmpPull()
	case monitor.ComposeRecreate:
		return locales.DockerCmpRecreate()
	case monitor.ComposeDown:
		return locales.DockerCmpDown()
	case monitor.ComposeValidate:
		return locales.DockerCmpValidate()
	case monitor.ComposeLogs:
		return locales.DockerLogs()
	}
	return ""
}

func (a *App) openComposeAction(s *Session, project monitor.ComposeProject, action monitor.ComposeAction, rows []monitor.Container, deleting bool) {
	if composeActionReason(s, project, action, rows) != "" {
		return
	}
	dialog := &composeDialog{client: s.client, project: project, services: slices.Clone(rows), action: action, delete: deleting}
	dialog.options.Services = composeNames(rows)
	s.docker.compose.dialog = dialog
	if action == monitor.ComposeValidate || action == monitor.ComposeLogs {
		dialog.options.Tail = 200
		a.runComposeAction(s, dialog)
	}
}

func (a *App) composeVisibleActions(c *ui.Context, s *Session, project monitor.ComposeProject) {
	page := &s.docker.compose
	reason := composeActionReason(s, project, monitor.ComposeUp, nil)
	if ui.PrimaryButton(c, locales.DockerCmpDeploy()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
		a.openComposeAction(s, project, monitor.ComposeUp, nil, false)
	}
	if page.edit != nil && page.edit.project == project.Project && page.edit.tab.savedAt.After(page.edit.before) {
		if ui.Button(c, locales.DockerCmpValidate()).Disabled(reason != "").Tooltip(reason).Shrink(0).Clicked() {
			a.openComposeAction(s, project, monitor.ComposeValidate, nil, false)
		}
	}
	ui.MenuButton(c, locales.DockerCmpActions(), func(menu *ui.Menu) { a.composeMenu(menu, s, project, nil) }).Shrink(0)
	if selected := composeSelected(page, composeVisibleServices(project.Services, page.filter, page.sort, c.Now())); len(selected) > 0 {
		ui.MenuButton(c, locales.DockerCmpPicked(len(selected)), func(menu *ui.Menu) { a.composeMenu(menu, s, project, selected) }).Shrink(0)
	}
}

func (a *App) composeMenu(menu *ui.Menu, s *Session, project monitor.ComposeProject, rows []monitor.Container) {
	actions := []monitor.ComposeAction{monitor.ComposeStart, monitor.ComposeStop, monitor.ComposeRestart, monitor.ComposePull, monitor.ComposeRecreate, monitor.ComposeLogs}
	if len(rows) == 0 {
		actions = append([]monitor.ComposeAction{monitor.ComposeUp}, actions...)
		actions = append(actions, monitor.ComposeValidate, monitor.ComposeDown)
	}
	for _, action := range actions {
		reason := composeActionReason(s, project, action, rows)
		if menu.Item(composeActionLabel(action)).Disabled(reason != "").Subtitle(reason).Chosen() {
			a.openComposeAction(s, project, action, rows, false)
		}
	}
	if len(rows) != 0 {
		var target *monitor.Container
		if len(rows) == 1 {
			target = dockerContainer(s, rows[0].ID)
		}
		reason := composeUnavailable(s)
		if reason == "" && (target == nil || target.Project != project.Project || target.Status != "running" || target.Paused || s.client == nil || !canSplit(s)) {
			reason = locales.DockerCmpTerminalReason()
		}
		if menu.Item(locales.DockerTerminal()).Disabled(reason != "").Subtitle(reason).Chosen() && target != nil {
			a.openDockerTerminal(dockerRowTarget(s, target), monitor.CmdOf(&s.overview.docker.d))
		}
		return
	}
	menu.Separator()
	editReason := ""
	if monitor.CheckComposeProject(project.Compose) != nil {
		editReason = locales.DockerCmpMetadata()
	} else if s.sftp == nil {
		editReason = locales.DockerCmpSftpRequired()
	}
	for _, file := range append(slices.Clone(project.Files), ".env") {
		if menu.Item(locales.DockerCmpEdit(file)).Disabled(editReason != "").Subtitle(editReason).Chosen() {
			a.openRemoteFile(s, composeFilePath(project, file))
			if tab := a.fe.current(); tab != nil {
				s.docker.compose.edit = &composeEditOffer{tab: tab, before: tab.savedAt, project: project.Project}
			}
		}
	}
	reason := composeActionReason(s, project, monitor.ComposeDown, nil)
	if menu.Item(locales.DockerCmpDelete()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.openComposeAction(s, project, monitor.ComposeDown, nil, true)
	}
}

func composeOwnedDirectory(s *Session, project monitor.ComposeProject) string {
	for _, entry := range s.docker.compose.managed {
		if entry.Marker != "" && entry.Project == project.Project && entry.Dir == project.Dir && len(project.Files) == 1 &&
			composeFilePath(project, project.Files[0]) == composeFilePath(project, "compose.yaml") {
			return entry.Marker
		}
	}
	return ""
}

func (a *App) runComposeAction(s *Session, dialog *composeDialog) {
	page := &s.docker.compose
	if dialog.client != s.client || !slices.Contains(a.sessions, s) || composeActionReason(s, dialog.project, dialog.action, dialog.services) != "" {
		dialog.validation = locales.DockerUnavailable()
		return
	}
	if !slices.ContainsFunc(page.projects(&s.overview.docker.d), func(project monitor.ComposeProject) bool {
		return project.Project == dialog.project.Project && project.Dir == dialog.project.Dir && slices.Equal(project.Files, dialog.project.Files)
	}) {
		dialog.validation = locales.DockerUnavailable()
		return
	}
	marker := composeOwnedDirectory(s, dialog.project)
	if dialog.removeDirectory && (!dialog.delete || dialog.typed != dialog.project.Project || marker == "") {
		dialog.validation = locales.DockerCmpDeleteType(dialog.project.Project)
		return
	}
	script, err := page.cli.Script(dialog.project.Compose, dialog.action, dialog.options)
	if err != nil {
		dialog.validation = err.Error()
		return
	}
	client, run := s.client, a.composeRunner(s)
	remote := a.composeFilesOf(s)
	deleting, removeDirectory, registry := dialog.delete, dialog.removeDirectory, composeRegistryPath(s)
	project := dialog.project
	mutates := dialog.action != monitor.ComposeLogs && dialog.action != monitor.ComposeValidate
	a.startComposeWork(s, dialog, mutates, func(ctx context.Context, report func(string)) (string, error, bool, bool) {
		output, err := run(ctx, client, script, report)
		if err != nil || !deleting {
			return output, err, false, false
		}
		if removeDirectory {
			if err := remote.remove(ctx, project.Dir, marker); err != nil {
				return output, err, false, false
			}
		}
		err = updateComposeRegistry(registry, composeRegistration{Project: project.Project}, true)
		return output, err, false, false
	})
}

func (a *App) startComposeWork(s *Session, dialog *composeDialog, mutates bool, task func(context.Context, func(string)) (string, error, bool, bool)) {
	page, client := &s.docker.compose, s.client
	duration := dockerTimeout
	if mutates || dialog.options.Follow {
		duration = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(composeContext(s), duration)
	work := &composeWork{cancel: cancel, view: dockerWork{target: dockerTarget{session: s, client: client, project: dialog.project.Project}, action: dockerRecreate}}
	page.work, dialog.work = work, work
	if mutates {
		s.overview.docker.work = &work.view
	}
	report := a.composeProgress(&work.view)
	go func() {
		defer cancel()
		output, err, privileged, written := task(ctx, report)
		a.post(func() {
			if page.work != work {
				return
			}
			page.work = nil
			if s.client != client || !slices.Contains(a.sessions, s) || s.status != statusConnected {
				if s.overview.docker.work == &work.view {
					s.overview.docker.work = nil
				}
				return
			}
			work.view.done, work.view.err, work.privileged, work.written = true, err, privileged, written
			if output != "" {
				work.view.output = composeTail(output)
			}
			if written {
				page.pick(dialog.project.Project)
			}
			if mutates {
				a.refreshDockerCompose(s)
				a.readDockerOf(s)
			}
		})
	}()
}

func composeTail(text string) string {
	const limit = 64 << 10
	start := max(0, len(text)-limit)
	for start > 0 && start < len(text) && text[start]&0xc0 == 0x80 {
		start++
	}
	return text[start:]
}

func (a *App) composeProgress(work *dockerWork) func(string) {
	var lock sync.Mutex
	var pending string
	queued := false
	return func(chunk string) {
		lock.Lock()
		pending = composeTail(pending + chunk)
		if queued {
			lock.Unlock()
			return
		}
		queued = true
		lock.Unlock()
		a.post(func() {
			lock.Lock()
			text := pending
			pending, queued = "", false
			lock.Unlock()
			if !work.done {
				if work.scroll.Y >= work.scroll.MaxY {
					work.scroll.Y = math.MaxFloat32
				}
				work.output = composeTail(work.output + text)
			}
		})
	}
}

func composeDirectoryError(err error) string {
	if errors.Is(err, errComposeDirectoryChanged) {
		return locales.DockerCmpDirectoryChanged()
	}
	if errors.Is(err, errComposeFileExists) || errors.Is(err, fs.ErrExist) {
		return locales.DockerCmpFilesExist()
	}
	return composeOutputError(err)
}

func (a *App) composeDialogView(c *ui.Context, s *Session, dialog *composeDialog) {
	page := &s.docker.compose
	if dialog.client != s.client || !composeSessionReady(s) {
		if dialog.work != nil {
			dialog.work.cancel()
		}
		page.dialog = nil
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-56, 260), 620)).Gap(9).Children(func() {
			title := composeDialogTitle(dialog)
			ui.Text(c, title).FontSize(16).Bold()
			if dialog.project.Project != "" {
				ui.Text(c, dialog.project.Project).Bold().Selectable()
			}
			if dialog.work != nil {
				a.composeWorkView(c, s, dialog, height, &open)
				return
			}
			if dialog.create {
				a.composeNewFields(c, s, dialog, height, &open)
				return
			}
			valid := a.composeActionFields(c, s, dialog)
			if dialog.validation != "" {
				ui.Text(c, dialog.validation).TextColor(theme.Danger)
			}
			ui.Row(c).Justify(ui.End).Gap(8).Wrap().Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, title).Disabled(!valid).Clicked() {
					a.runComposeAction(s, dialog)
				}
			})
		})
	})
	if !open && page.dialog == dialog {
		if dialog.work != nil && !dialog.work.view.done {
			dialog.work.cancel()
		}
		page.dialog = nil
	}
}

func composeDialogTitle(dialog *composeDialog) string {
	if dialog.create {
		return locales.DockerCmpNew()
	}
	if dialog.delete {
		return locales.DockerCmpDelete()
	}
	return composeActionLabel(dialog.action)
}

func (a *App) composeActionFields(c *ui.Context, s *Session, dialog *composeDialog) bool {
	if len(dialog.services) > 0 {
		ui.Text(c, strings.Join(composeNames(dialog.services), ", ")).Selectable()
	} else if dialog.project.Dir != "" {
		widgets.Muted(c, dialog.project.Dir).Selectable()
	}
	flavor := s.docker.compose.cli.Flavor
	switch dialog.action {
	case monitor.ComposeUp:
		widgets.Muted(c, locales.DockerCmpDeployWarning())
		ui.Checkbox(c, &dialog.options.Build, locales.DockerCmpBuild())
		composeCheckbox(c, &dialog.options.PullAlways, locales.DockerCmpPullAlways(), flavor != monitor.ComposeV2, locales.DockerCmpV2Only())
		ui.Checkbox(c, &dialog.options.Force, locales.DockerCmpForce())
		composeCheckbox(c, &dialog.options.Orphans, locales.DockerCmpOrphans(), flavor == monitor.ComposePodman, locales.DockerCmpUnsupported())
	case monitor.ComposeRecreate:
		widgets.Muted(c, locales.DockerComposeRecreateWarning())
	case monitor.ComposeDown:
		widgets.Muted(c, locales.DockerCmpDownWarning())
		ui.Checkbox(c, &dialog.options.Volumes, locales.DockerCmpVolumes())
		if dialog.options.Volumes {
			ui.Text(c, locales.DockerCmpVolumeLoss(dialog.project.Project)).TextColor(theme.Danger).Bold()
			if !dialog.volumeRead {
				a.readComposeRemovalVolumes(s, dialog)
			}
			switch {
			case dialog.volumeState != "":
				widgets.Muted(c, dialog.volumeState).FontSize(12)
			case len(dialog.volumeNames) != 0:
				labels := strings.Join(dialog.volumeNames, ", ")
				ui.Text(c, locales.DockerCmpNamedVolumes(labels)).FontSize(12).SingleLine().Tooltip(labels).Selectable()
			}
		}
		composeCheckbox(c, &dialog.options.Orphans, locales.DockerCmpOrphans(), flavor == monitor.ComposePodman, locales.DockerCmpUnsupported())
		images := ui.MenuButton(c, locales.DockerCmpImages(composeImageOption(dialog.options.Images)), func(menu *ui.Menu) {
			for _, value := range []string{"", "local", "all"} {
				if menu.Item(composeImageOption(value)).Chosen() {
					dialog.options.Images = value
				}
			}
		})
		if flavor == monitor.ComposePodman {
			images.Disabled(true).Tooltip(locales.DockerCmpUnsupported())
		}
		if dialog.delete {
			return composeDeleteFields(c, s, dialog)
		}
	default:
		widgets.Muted(c, locales.DockerCmpConfirm(composeActionLabel(dialog.action)))
	}
	return true
}

func composeCheckbox(c *ui.Context, value *bool, label string, disabled bool, reason string) {
	control := ui.Checkbox(c, value, label)
	if disabled {
		control.Disabled(true).Tooltip(reason)
	}
}

func (a *App) readComposeRemovalVolumes(s *Session, dialog *composeDialog) {
	page, client, run := &s.docker.compose, s.client, a.composeRunner(s)
	dialog.volumeRead = true
	cmd, err := page.cli.VolumeListScript(dialog.project.Project)
	if err != nil {
		dialog.volumeState = locales.DockerCmpVolumeNamesUnknown()
		return
	}
	dialog.volumeState = locales.DockerReading()
	ctx, cancel := context.WithTimeout(composeContext(s), dockerTimeout)
	go func() {
		defer cancel()
		output, err := run(ctx, client, cmd, nil)
		var names []string
		if err == nil {
			for line := range strings.SplitSeq(output, "\n") {
				if name := strings.TrimSpace(line); monitor.ValidName(name) {
					names = append(names, name)
				}
			}
			slices.Sort(names)
		}
		a.post(func() {
			if page.dialog != dialog || client != s.client {
				return
			}
			dialog.volumeNames, dialog.volumeState = names, ""
			if err != nil || len(names) == 0 {
				dialog.volumeState = locales.DockerCmpVolumeNamesUnknown()
			}
		})
	}()
}

func composeImageOption(value string) string {
	switch value {
	case "local":
		return locales.DockerCmpLocalImages()
	case "all":
		return locales.DockerCmpAllImages()
	}
	return locales.DockerCmpKeepImages()
}

func composeDeleteFields(c *ui.Context, s *Session, dialog *composeDialog) bool {
	owned := composeOwnedDirectory(s, dialog.project) != ""
	reason := ""
	if !owned {
		reason = locales.DockerCmpOwnedOnly()
	}
	ui.Checkbox(c, &dialog.removeDirectory, locales.DockerCmpRemoveDir()).Disabled(!owned).Tooltip(reason)
	if !dialog.removeDirectory {
		return true
	}
	ui.Text(c, locales.DockerCmpDirectoryLoss(dialog.project.Dir)).TextColor(theme.Danger).Bold()
	label := locales.DockerCmpDeleteType(dialog.project.Project)
	ui.TextInput(c, &dialog.typed).Label(label).Placeholder(label).FillWidth()
	return owned && dialog.typed == dialog.project.Project
}

func (a *App) composeWorkView(c *ui.Context, s *Session, dialog *composeDialog, height float32, open *bool) {
	work := dialog.work
	switch {
	case !work.view.done:
		widgets.Muted(c, locales.DockerActionRunning(composeDialogTitle(dialog)))
	case work.view.err != nil:
		switch {
		case dockerPrivilegeFailed(work.view.err):
			dockerPrivilegeHelp(c, s)
		case errors.Is(work.view.err, monitor.ErrPrivilegeCancelled):
			widgets.Muted(c, locales.DockerCmpCanceled())
		default:
			ui.Text(c, composeDirectoryError(work.view.err)).TextColor(theme.Danger).Selectable()
		}
	default:
		widgets.Muted(c, locales.DockerActionDone())
	}
	if work.privileged {
		widgets.Muted(c, locales.DockerPrivComposeFiles())
	}
	if work.view.output != "" || dialog.action == monitor.ComposeLogs {
		text := work.view.output
		if text == "" {
			text = locales.DockerLogEmpty()
		}
		dockerOutput(c, text, false, min(290, max(height-240, 100)), &work.view.scroll)
	}
	if work.stopping && !work.view.done {
		widgets.Muted(c, locales.DockerCmpCanceled())
	}
	ui.Row(c).Justify(ui.End).Gap(8).Wrap().Children(func() {
		if work.view.output != "" && ui.Button(c, locales.CopyAll()).Clicked() {
			c.WriteClipboard(work.view.output)
		}
		if !work.view.done {
			if ui.Button(c, locales.Cancel()).Disabled(work.stopping).Clicked() {
				work.stopping = true
				work.cancel()
			}
			return
		}
		if dialog.action == monitor.ComposeLogs && ui.Button(c, locales.DockerLogFollow()).Clicked() {
			dialog.options.Follow = true
			a.runComposeAction(s, dialog)
		}
		if work.written && work.view.err == nil && !dialog.deploy && ui.Button(c, locales.DockerCmpDeploy()).Disabled(composeUnavailable(s) != "").Clicked() {
			project := dialog.project
			for _, current := range s.docker.compose.projects(&s.overview.docker.d) {
				if current.Project == project.Project && current.Dir == project.Dir {
					project = current
					break
				}
			}
			a.openComposeAction(s, project, monitor.ComposeUp, nil, false)
		}
		if ui.PrimaryButton(c, locales.Close()).Clicked() {
			*open = false
		}
	})
}
