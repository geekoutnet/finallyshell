package app

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type dockerActionDialog struct {
	action         dockerAction
	target         dockerTarget
	volumes        bool
	value, retries string
	validation     string
	work           *dockerWork
}

func (d *dockerActionDialog) script(cmd monitor.DockerCmd) (string, error) {
	target := d.target
	if target.id == "" {
		project, members := target.projectInfo()
		switch d.action {
		case dockerRecreate:
			return cmd.ComposeUpdateScript(project, "")
		case dockerDown:
			return cmd.ComposeDownScript(project)
		default:
			var ids []string
			for _, ct := range members {
				if d.action.allowedProjectMember(ct, cmd.Tool) {
					ids = append(ids, ct.ID)
				}
			}
			return cmd.OpScript(dockerOps[d.action], ids...)
		}
	}
	switch d.action {
	case dockerRemove, dockerForceRemove:
		return cmd.RemoveScript(d.action == dockerForceRemove, d.volumes, target.id)
	case dockerRename:
		return cmd.RenameScript(target.id, strings.TrimSpace(d.value))
	case dockerPolicy:
		retries, _ := strconv.Atoi(d.retries)
		return cmd.RestartPolicyScript(target.id, d.value, retries)
	case dockerRecreate:
		ct := dockerContainer(target.session, target.id)
		if ct.Project != "" {
			return cmd.ComposeUpdateScript(monitor.ComposeOf(ct), ct.Service)
		}
		return cmd.RecreateScript(ct, time.Now())
	default:
		return cmd.OpScript(dockerOps[d.action], target.id)
	}
}

func (a *App) dockerActionView(c *ui.Context, dialog *containerDialog) {
	d := dialog.action
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-64, 280), 520)).Gap(12).Children(func() {
			ui.Text(c, d.action.title(d.target.id == "")).FontSize(16).Bold()
			ui.Text(c, d.target.name).Selectable()
			if d.work != nil {
				dockerWorkView(c, d.work, height, &open)
				return
			}
			valid := dockerActionFields(c, d)
			if d.validation != "" {
				ui.Text(c, d.validation).TextColor(theme.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Wrap().Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				label := d.action.title(d.target.id == "")
				if d.action == dockerRename || d.action == dockerPolicy {
					label = locales.Save()
				}
				if ui.PrimaryButton(c, label).Disabled(!valid).Clicked() {
					a.runDockerAction(d)
				}
			})
		})
	})
	if !open && a.ctrDlg == dialog {
		a.ctrDlg = nil
	}
}

func dockerActionFields(c *ui.Context, d *dockerActionDialog) bool {
	warning := ""
	switch d.action {
	case dockerStart, dockerStop, dockerRestart, dockerPause, dockerUnpause:
		warning = locales.DockerPrivActionWarning(d.action.title(d.target.id == ""), d.target.name)
	case dockerKill:
		warning = locales.DockerKillWarning()
	case dockerRemove:
		warning = locales.DockerRemoveWarning()
	case dockerForceRemove:
		warning = locales.DockerForceRemoveWarning()
	case dockerRecreate:
		warning = locales.DockerRecreateWarning()
		if d.target.project != "" {
			warning = locales.DockerComposeRecreateWarning()
		}
	case dockerDown:
		warning = locales.DockerDownWarning()
	case dockerRename:
		label := locales.FieldName()
		ui.Text(c, label)
		ui.TextInput(c, &d.value).Label(label).AutoFocus().FillWidth()
		if !monitor.ValidName(strings.TrimSpace(d.value)) {
			widgets.Muted(c, locales.DockerInvalidName()).FontSize(12)
			return false
		}
		return strings.TrimSpace(d.value) != d.target.name
	case dockerPolicy:
		ui.Select(c, &d.value, monitor.RestartPolicies).Label(locales.DockerRestartPolicy()).FillWidth()
		if d.value == "on-failure" {
			label := locales.DockerPolicyMax()
			ui.Text(c, label)
			ui.TextInput(c, &d.retries).Label(label).Width(100)
			_, err := strconv.ParseUint(d.retries, 10, 31)
			return d.retries == "" || err == nil
		}
		return true
	}
	ui.Text(c, warning).TextColor(theme.TextMuted)
	if d.action == dockerRemove || d.action == dockerForceRemove {
		ui.Checkbox(c, &d.volumes, locales.DockerRemoveVolumes())
	}
	return true
}

func dockerWorkView(c *ui.Context, work *dockerWork, height float32, open *bool) {
	if !work.done {
		widgets.Muted(c, work.busyText())
	} else if work.err != nil {
		ui.Text(c, locales.DockerActionFailed()).Bold().TextColor(theme.Danger)
		ui.Text(c, work.err.Error()).Selectable()
	} else {
		widgets.Muted(c, locales.DockerActionDone())
	}
	if work.output != "" {
		dockerOutput(c, work.output, true, min(280, max(height-220, 100)), &work.scroll)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if !work.done && work.cancel != nil && ui.Button(c, locales.Cancel()).Clicked() {
			work.cancel()
			work.cancel = nil
		}
		if work.output != "" && ui.Button(c, locales.CopyAll()).Clicked() {
			c.WriteClipboard(work.output)
		}
		if ui.PrimaryButton(c, locales.Close()).Disabled(work.label != "" && !work.done).Clicked() {
			*open = false
		}
	})
}

const dockerLogInterval = 2 * time.Second

type dockerLogsDialog struct {
	target              dockerTarget
	tail, query, output string
	follow, wrap, busy  bool
	err                 error
	next                time.Time
	cancel              context.CancelFunc
	scroll              ui.ScrollState
}

func (a *App) openDockerLogs(target dockerTarget) {
	d := &dockerLogsDialog{target: target, tail: "200"}
	a.ctrDlg = &containerDialog{s: target.session, logs: d}
	a.loadDockerLogs(d, 200)
}

func (a *App) loadDockerLogs(d *dockerLogsDialog, tail int) {
	if d.busy || !d.target.available() {
		return
	}
	state := &d.target.session.overview.docker
	cmd := monitor.CmdOf(&state.d)
	var script string
	var err error
	if d.target.id != "" {
		script, err = cmd.LogsScript(d.target.id, tail)
	} else {
		project, _ := d.target.projectInfo()
		script, err = cmd.ComposeLogsScript(project, tail)
	}
	if err != nil {
		d.err = err
		return
	}
	run := a.dockerExec(d.target.session, monitor.RunDockerProgress, nil, false).command
	parent := d.target.session.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, dockerTimeout)
	d.busy, d.cancel = true, cancel
	go func() {
		defer cancel()
		output, err := run(ctx, d.target.client, script, nil)
		a.post(func() {
			if a.ctrDlg == nil || a.ctrDlg.logs != d {
				return
			}
			if d.follow && d.scroll.Y >= d.scroll.MaxY {
				d.scroll.Y = math.MaxFloat32
			}
			d.output, d.err, d.busy, d.cancel = output, err, false, nil
		})
	}()
}

func (a *App) dockerLogsView(c *ui.Context, dialog *containerDialog) {
	d := dialog.logs
	if !slices.Contains(a.sessions, d.target.session) || !d.target.available() {
		a.closeDockerLogs(dialog)
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		width, height := c.Size()
		ui.Column(c).Width(min(max(width-64, 280), 840)).Gap(10).Children(func() {
			ui.Text(c, locales.DockerLogs()).FontSize(16).Bold()
			ui.Text(c, d.target.name).Selectable()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				label := locales.DockerLogTail()
				ui.Text(c, label)
				input := ui.TextInput(c, &d.tail).Label(label).Width(70)
				tail, err := strconv.Atoi(d.tail)
				valid := err == nil && tail >= 1 && tail <= 10000
				refresh := ui.Button(c, locales.Refresh()).Disabled(d.busy || !valid).Clicked()
				if refresh || input.Submitted() && valid {
					a.loadDockerLogs(d, tail)
				}
				if ui.Checkbox(c, &d.follow, locales.DockerLogFollow()).Changed() && d.follow {
					d.scroll.Y = math.MaxFloat32
				}
				ui.Checkbox(c, &d.wrap, locales.DockerLogWrap())
				if d.follow && valid {
					now := c.Now()
					if d.next.IsZero() || refresh {
						d.next = now.Add(dockerLogInterval)
					} else if !now.Before(d.next) {
						a.loadDockerLogs(d, tail)
						d.next = now.Add(dockerLogInterval)
					}
					c.After(d.next.Sub(now))
				} else {
					d.next = time.Time{}
				}
			})
			ui.SearchField(c, &d.query).Placeholder(locales.DockerLogFilter()).Label(locales.DockerLogFilter()).FillWidth()
			output := filterDockerLogs(d.output, d.query)
			if d.err != nil {
				ui.Text(c, d.err.Error()).TextColor(theme.Danger).Selectable()
			}
			if output == "" {
				output = locales.DockerLogEmpty()
				if d.busy {
					output = locales.DockerLogLoading()
				} else if d.output != "" {
					output = locales.DockerLogNoMatches()
				}
			}
			dockerOutput(c, output, d.wrap, min(380, max(height-280, 100)), &d.scroll)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.CopyAll()).Disabled(d.output == "").Clicked() {
					c.WriteClipboard(d.output)
				}
				if ui.PrimaryButton(c, locales.Close()).Clicked() {
					open = false
				}
			})
		})
	})
	if !open {
		a.closeDockerLogs(dialog)
	}
}

func (a *App) closeDockerLogs(dialog *containerDialog) {
	if dialog.logs.cancel != nil {
		dialog.logs.cancel()
	}
	if a.ctrDlg == dialog {
		a.ctrDlg = nil
	}
}

func filterDockerLogs(output, query string) string {
	if query = strings.ToLower(strings.TrimSpace(query)); query == "" {
		return output
	}
	var match strings.Builder
	for line := range strings.SplitAfterSeq(output, "\n") {
		if strings.Contains(strings.ToLower(line), query) {
			match.WriteString(line)
		}
	}
	return match.String()
}

func dockerOutput(c *ui.Context, output string, wrap bool, height float32, position *ui.ScrollState) {
	var scroll ui.Element
	if !wrap {
		scroll = ui.ScrollBoth(c.Key("unwrapped"))
	} else {
		scroll = ui.Scroll(c.Key("wrapped"))
	}
	scroll.Height(height).FillWidth().TrackScroll(position).Border(1, colors.line).Radius(6).Background(theme.Background).Children(func() {
		text := ui.Text(c, output).Font("monospace").FontSize(12).PreserveSpaces().Selectable().Padding(8, 10)
		if !wrap {
			text.NoWrap().Shrink(0)
		}
	})
}
