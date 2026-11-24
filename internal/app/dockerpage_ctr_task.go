package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type ctrModal struct {
	target dockerTarget
	task   *ctrTask
	form   *ctrForm
	stats  *ctrStats
	files  *ctrFiles
}

func (a *App) setCtrModal(modal *ctrModal) {
	a.ctrDlg = &containerDialog{s: modal.target.session, ctr: modal}
}

func (a *App) ctrModalView(c *ui.Context, dialog *containerDialog) {
	modal := dialog.ctr
	if !modal.target.available() || !slices.Contains(a.sessions, modal.target.session) {
		a.closeCtrModal(dialog)
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		maxWidth := float32(520)
		if modal.form != nil || modal.stats != nil || modal.files != nil {
			maxWidth = 800
		}
		ui.Column(c).Width(min(max(width-64, 275), maxWidth)).Gap(10).Children(func() {
			switch {
			case modal.task != nil:
				a.ctrTaskView(c, modal, height, &open)
			case modal.form != nil:
				a.ctrFormView(c, modal, height, &open)
			case modal.stats != nil:
				a.ctrStatsView(c, modal, height, &open)
			case modal.files != nil:
				a.ctrFilesView(c, modal, height, &open)
			}
		})
	})
	if !open {
		if modal.task != nil && modal.task.work != nil && !modal.task.work.done {
			return
		}
		a.closeCtrModal(dialog)
	}
}

func (a *App) closeCtrModal(dialog *containerDialog) {
	if stats := dialog.ctr.stats; stats != nil && stats.cancel != nil {
		stats.cancel()
	}
	if files := dialog.ctr.files; files != nil && files.cancel != nil {
		files.cancel()
	}
	if a.ctrDlg == dialog {
		a.ctrDlg = nil
	}
}

func ctrActionReason(action dockerAction, ct *monitor.Container) string {
	if ct == nil {
		return locales.DockerUnavailable()
	}
	switch action {
	case dockerStart, dockerRemove:
		return locales.DockerCtrMustStop()
	case dockerStop, dockerRestart, dockerKill, dockerPause:
		return locales.DockerCtrMustRun()
	case dockerUnpause:
		return locales.DockerCtrMustPause()
	case dockerRecreate:
		if ct.Project == "" && ct.AutoRemove {
			return locales.DockerCtrAutoRemoveReason()
		}
	}
	return locales.DockerCtrStateReason()
}

func ctrEditReason(target dockerTarget, ct *monitor.Container) string {
	if ct != nil && ct.Project != "" {
		return locales.DockerCtrComposeReason(ct.Project)
	}
	if target.session.overview.docker.d.Tool == "podman" {
		return locales.DockerCtrDockerOnly()
	}
	if ct != nil && ct.AutoRemove {
		return locales.DockerCtrAutoRemoveReason()
	}
	if !target.allow(dockerRecreate) {
		return locales.DockerCtrStateReason()
	}
	return ""
}

func (a *App) ctrMoreMenu(menu *ui.Menu, target dockerTarget, ct *monitor.Container) {
	menu.Separator()
	readable := target.available() && monitor.ValidName(target.id)
	busy := target.session.overview.docker.work != nil
	when := func(allowed bool, why string) string {
		if !readable || busy {
			return locales.DockerUnavailable()
		}
		if !allowed {
			return why
		}
		return ""
	}
	reason := ctrEditReason(target, ct)
	if !readable || busy {
		reason = locales.DockerUnavailable()
	}
	if menu.Item(locales.DockerCtrEdit()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.openCtrForm(target, ct, false)
	}
	reason = when(len(ct.Inspect) > 0, locales.DockerCtrInspectMissing())
	if menu.Item(locales.DockerCtrDuplicate()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.openCtrForm(target, ct, true)
	}
	reason = ctrStatsReason(target, []string{ct.ID})
	if menu.Item(locales.DockerCtrStats()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.openCtrStats(target, []string{ct.ID})
	}
	reason = when(ctrExecuting(ct), locales.DockerCtrMustRun())
	if menu.Item(locales.DockerCtrFiles()).Disabled(reason != "").Subtitle(reason).Chosen() {
		a.setCtrModal(&ctrModal{target: target, files: &ctrFiles{dir: "/", input: "/"}})
	}
	for _, action := range []struct{ kind, title string }{{"commit", locales.DockerCtrCommit()}, {"export", locales.DockerCtrExport()}} {
		reason = when(ct.Status != "removing" && ct.Status != "dead", locales.DockerCtrStateReason())
		if menu.Item(action.title).Disabled(reason != "").Subtitle(reason).Chosen() {
			a.openCtrTask(&ctrTask{target: target, ids: []string{ct.ID}, names: []string{ct.Name}, kind: action.kind, title: action.title, pause: true})
		}
	}
}

func ctrExecuting(ct *monitor.Container) bool {
	return ct != nil && ct.Status == "running" && !ct.Paused
}

type ctrTask struct {
	target                 dockerTarget
	action                 dockerAction
	ids, names             []string
	kind, title            string
	value, message, author string
	volumes, pause         bool
	form                   *ctrForm
	file                   *ctrFileTask
	work                   *dockerWork
}

func ctrTargetNames(s *Session, ids []string) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		if ct := dockerContainer(s, id); ct != nil {
			names = append(names, ct.Name)
		}
	}
	return names
}

func (a *App) openCtrTask(task *ctrTask) {
	a.setCtrModal(&ctrModal{target: task.target, task: task})
}

func (task *ctrTask) unavailable(a *App) string {
	target := task.target
	s := target.session
	if !target.available() || !slices.Contains(a.sessions, s) || s.overview.docker.work != nil {
		return locales.DockerUnavailable()
	}
	for index, id := range task.ids {
		ct := dockerContainer(s, id)
		if ct == nil || !monitor.ValidName(id) || index < len(task.names) && task.names[index] != ct.Name {
			return locales.DockerUnavailable()
		}
		switch task.kind {
		case "":
			if !task.action.allowed(ct, s.overview.docker.d.Tool) {
				return ctrActionReason(task.action, ct)
			}
		case "edit":
			if reason := ctrEditReason(target, ct); reason != "" {
				return reason
			}
			if !slices.Equal(task.form.inspect, ct.Inspect) {
				return locales.DockerCtrChanged()
			}
		case "commit", "export":
			if ct.Status == "removing" || ct.Status == "dead" {
				return locales.DockerCtrStateReason()
			}
		case "files":
			if !ctrExecuting(ct) {
				return locales.DockerCtrMustRun()
			}
		}
	}
	if task.form != nil {
		for _, ct := range s.overview.docker.d.Containers {
			if ct.Name == task.form.config.Name && ct.ID != target.id {
				return locales.DockerCtrNameExists()
			}
		}
	}
	return ""
}

func (task *ctrTask) script() (string, error) {
	s := task.target.session
	cmd := monitor.CmdOf(&s.overview.docker.d)
	switch task.kind {
	case "create":
		return cmd.CtrRunScript(task.form.config)
	case "edit":
		return cmd.CtrEditScript(dockerContainer(s, task.target.id), task.form.config, time.Now())
	case "commit":
		return cmd.CtrCommitScript(task.target.id, strings.TrimSpace(task.value), task.message, task.author, task.pause)
	case "export":
		return cmd.CtrExportScript(task.target.id, task.value)
	case "files":
		return task.file.script(cmd, task.target.id)
	default:
		if task.action == dockerRemove {
			return cmd.RemoveScript(false, task.volumes, task.ids...)
		}
		return cmd.OpScript(dockerOps[task.action], task.ids...)
	}
}

func (a *App) ctrTaskView(c *ui.Context, modal *ctrModal, height float32, open *bool) {
	task := modal.task
	ui.Text(c, task.title).FontSize(16).Bold()
	if task.work != nil {
		finished := true
		dockerWorkView(c, task.work, height, &finished)
		if !finished && modal.files != nil {
			modal.task = nil
			modal.files.loaded = false
			if modal.files.cancel != nil {
				modal.files.cancel()
			}
		} else if !finished {
			*open = false
		}
		return
	}
	if task.kind == "edit" && task.target.name != task.form.config.Name {
		ui.Text(c, task.target.name+" → "+task.form.config.Name).Selectable()
	} else if len(task.names) > 0 {
		ui.Scroll(c).MaxHeight(110).FillWidth().Children(func() {
			ui.Text(c, strings.Join(task.names, "\n")).Selectable()
		})
	}
	var warning string
	switch task.kind {
	case "create":
		warning = locales.DockerCtrCreateWarning(task.form.config.Name)
	case "edit":
		warning = locales.DockerCtrEditWarning()
	case "commit":
		warning = locales.DockerCtrCommitWarning()
		ctrField(c, locales.DockerCtrImageTag(), &task.value, "example/app:snapshot", nil)
		ctrField(c, locales.DockerCtrCommitMessage(), &task.message, "", nil)
		ctrField(c, locales.DockerCtrAuthor(), &task.author, "", nil)
		ui.Checkbox(c, &task.pause, locales.DockerCtrCommitPause())
	case "export":
		warning = locales.DockerCtrExportWarning()
		ctrField(c, locales.DockerCtrServerPath(), &task.value, "/tmp/container.tar", nil)
	case "files":
		warning = task.file.fields(c)
	case "":
		warning = locales.DockerCtrBatchWarning()
		switch task.action {
		case dockerKill:
			warning = locales.DockerKillWarning()
		case dockerRemove:
			warning = locales.DockerRemoveWarning()
			ui.Checkbox(c, &task.volumes, locales.DockerRemoveVolumes())
		}
	}
	widgets.Muted(c, warning)
	if task.form != nil && (task.form.config.Privileged || len(task.form.config.Devices) > 0 || len(task.form.config.CapAdd) > 0) {
		ui.Text(c, locales.DockerCtrPrivilegeWarning()).TextColor(theme.Danger).FontSize(12)
	}
	reason := task.unavailable(a)
	script := ""
	if reason == "" {
		var err error
		script, err = task.script()
		if err != nil {
			reason = ctrErrorText(err)
		}
	}
	if reason != "" && (task.kind != "commit" && task.kind != "export" || task.value != "") {
		ui.Text(c, reason).TextColor(theme.Danger).FontSize(12)
	}
	ui.Row(c).Gap(8).Wrap().Justify(ui.End).Children(func() {
		if ui.Button(c, locales.Cancel()).Clicked() {
			if modal.form != nil || modal.files != nil {
				modal.task = nil
			} else {
				*open = false
			}
		}
		if ui.PrimaryButton(c, task.title).Disabled(script == "").Clicked() {
			a.runCtrTask(task, script)
		}
	})
}

func (a *App) ctrRunner(target dockerTarget, timeout time.Duration) (dockerRunner, context.Context, context.CancelFunc) {
	s := target.session
	run := a.dockerExec(s, monitor.RunDockerProgress, nil, false).command
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return run, ctx, cancel
}

func (a *App) runCtrTask(task *ctrTask, script string) {
	if script == "" || task.unavailable(a) != "" {
		return
	}
	target := task.target
	s := target.session
	state := &s.overview.docker
	work := &dockerWork{target: target, label: task.title, ids: task.ids}
	task.work, state.work = work, work
	timeout := dockerTimeout
	if task.kind != "" {
		timeout = 30 * time.Minute
	}
	run, ctx, cancel := a.ctrRunner(target, timeout)
	if task.kind == "create" || task.kind == "export" || task.kind == "files" && task.file.copying() {
		work.cancel = cancel
	}
	report := a.dockerProgress(work)
	var redactor *strings.Replacer
	if task.form != nil {
		redactor = ctrOutputRedactor(task.form.config)
		if redactor != nil {
			report = ctrRedactedProgress(report, redactor)
		}
	}
	go func() {
		defer cancel()
		output, err := run(ctx, target.client, script, report)
		var exit *monitor.DockerExit
		if task.file != nil && task.file.copying() && errors.As(err, &exit) && exit.Status == 73 {
			err = errors.New(locales.DockerPrivCopyDestination())
		}
		if redactor != nil {
			output = redactor.Replace(output)
		}
		a.post(func() {
			work.done, work.err, work.output, work.cancel = true, err, output, nil
			if state.work != work {
				return
			}
			if !target.available() || !slices.Contains(a.sessions, s) {
				state.work = nil
				return
			}
			a.readDockerOf(s)
			a.refreshDockerContainers(s)
		})
	}()
}

func ctrOutputRedactor(cfg monitor.CtrConfig) *strings.Replacer {
	var secrets []string
	for _, values := range [][]string{cfg.Env, cfg.LogOptions} {
		for _, entry := range values {
			_, value, found := strings.Cut(entry, "=")
			if found && value != "" {
				for line := range strings.FieldsFuncSeq(value, func(r rune) bool { return r == '\n' || r == '\r' }) {
					secrets = append(secrets, line)
				}
			}
		}
	}
	if len(secrets) == 0 {
		return nil
	}
	slices.SortFunc(secrets, func(left, right string) int { return len(right) - len(left) })
	pairs := make([]string, 0, len(secrets)*2)
	for _, value := range secrets {
		pairs = append(pairs, value, secretMask)
	}
	return strings.NewReplacer(pairs...)
}

func ctrRedactedProgress(report func(string), redactor *strings.Replacer) func(string) {
	pending := ""
	discard := false
	return func(chunk string) {
		if discard {
			end := strings.IndexAny(chunk, "\r\n")
			if end < 0 {
				return
			}
			chunk, discard = chunk[end+1:], false
		}
		pending += chunk
		end := strings.LastIndexAny(pending, "\r\n") + 1
		if end > 0 {
			report(redactor.Replace(pending[:end]))
			pending = pending[end:]
		}
		if len(pending) > 64<<10 {
			pending, discard = "", true
		}
	}
}

func ctrErrorText(err error) string {
	var field monitor.CtrConfigError
	if !errors.As(err, &field) {
		return locales.DockerCtrInvalid()
	}
	labels := map[monitor.CtrConfigError]string{
		"image": locales.DockerCtrImageTag(), "name": locales.FieldName(), "workdir": locales.DockerWorkDir(),
		"identity": locales.DockerUser() + " / " + locales.DockerHostname(), "command": locales.DockerCommand(),
		"ports": locales.DockerColPorts(), "mounts": locales.DockerSecMounts(), "env": locales.DockerSecEnv(),
		"labels": locales.DockerSecLabels(), "logs": locales.DockerSecLogging(), "network": locales.DockerSecNetwork(),
		"restart": locales.DockerRestartPolicy(), "resources": locales.DockerResources(), "capabilities": locales.DockerCtrCapabilities(),
		"devices": locales.DockerCtrDevices(), "path": locales.DockerCtrPath(), "autoremove": locales.DockerCtrAutoRemove(),
	}
	return locales.DockerCtrInvalidField(labels[field])
}
