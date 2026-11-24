package app

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) dockerImgDialogView(c *ui.Context, s *Session) {
	dialog := s.docker.images.dialog
	if dialog == nil {
		return
	}
	if !dialog.current() {
		dialog.close()
		if dialog.stopWork != nil {
			dialog.stopWork()
		}
		s.docker.images.dialog = nil
		return
	}
	open := true
	ui.Modal(c.Key(dialog.op), &open, func() {
		width, height := c.Size()
		dialogWidth := float32(560)
		if dialog.op == imgDetails || dialog.op == imgHistory {
			dialogWidth = 720
		}
		ui.Column(c).Width(min(max(width-58, 220), dialogWidth)).Gap(10).Children(func() {
			ui.Text(c, dialog.title()).FontSize(17).Bold()
			if len(dialog.rows) == 1 && (dialog.op == imgDetails || dialog.op == imgHistory) {
				ui.Text(c, dialog.rows[0].Ref).SingleLine().Selectable().Tooltip(dialog.rows[0].Ref)
			}
			switch {
			case dialog.work != nil:
				a.dockerImgWorkView(c, dialog, height, &open)
			case dialog.op == imgDetails || dialog.op == imgHistory:
				dockerImgReadView(c, dialog, height, &open)
			default:
				ui.Scroll(c.Key("img-form")).FillWidth().MaxHeight(max(112, height-190)).Children(func() {
					ui.Column(c).FillWidth().Padding(1, 8, 1, 1).Gap(8).Children(func() { a.dockerImgFields(c, dialog) })
				})
				reason := dialog.blocked()
				_, err := dialog.script()
				if reason == "" && err != nil && (dialog.value != "" || dialog.local != "") {
					reason = dockerImgError(err)
				}
				if dialog.validation != "" {
					reason = dialog.validation
				}
				if reason != "" {
					widgets.Muted(c, reason).FontSize(12)
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Wrap().Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						open = false
					}
					if ui.PrimaryButton(c, dialog.title()).Disabled(err != nil || dialog.blocked() != "" || dialog.picking).Clicked() {
						a.runDockerImgDialog(dialog)
					}
				})
			}
		})
	})
	if !open {
		if dialog.work != nil && !dialog.work.done {
			return
		}
		dialog.close()
		s.docker.images.dialog = nil
	}
}

func dockerImgError(err error) string {
	switch {
	case errors.Is(err, monitor.ErrImageReference):
		return locales.DockerImgInvalidRef()
	case errors.Is(err, monitor.ErrImagePlatform):
		return locales.DockerImgInvalidPlatform()
	case errors.Is(err, monitor.ErrImagePath):
		return locales.DockerImgInvalidPath()
	case errors.Is(err, monitor.ErrImageBuildArg):
		return locales.DockerImgInvalidArg()
	case errors.Is(err, monitor.ErrImageTarget):
		return locales.DockerImgInvalidTarget()
	case errors.Is(err, monitor.ErrImageForce):
		return locales.DockerImgPodmanForce()
	default:
		return err.Error()
	}
}

func dockerImgField(c *ui.Context, label, hint string, value *string) {
	ui.Column(c.Key(label)).FillWidth().Gap(3).Children(func() {
		ui.Text(c, label).FontSize(13).Bold()
		ui.TextInput(c, value).Label(label).Placeholder(hint).FillWidth()
	})
}

func dockerImgRefList(c *ui.Context, rows []monitor.ImageRow) {
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, row.Ref)
	}
	slices.Sort(refs)
	ui.Column(c.Key("references")).FillWidth().Gap(3).Padding(6, 8).Radius(5).Border(1, colors.line).Children(func() {
		for index, ref := range refs {
			ui.Text(c.Key(index), ref).Selectable().FillWidth()
		}
	})
}

func (a *App) dockerImgFields(c *ui.Context, dialog *dockerImgDialog) {
	s := dialog.target.session
	switch dialog.op {
	case imgPull:
		dockerImgField(c, locales.DockerImgReference(), "registry.example/team/image:tag", &dialog.value)
		dockerImgField(c, locales.DockerImgPlatform(), "linux/arm64", &dialog.platform)
		ui.Checkbox(c, &dialog.all, locales.DockerImgAllTags())
		if dialog.tool.Tool == "podman" {
			widgets.Muted(c, locales.DockerImgPodmanPull()+" "+locales.DockerImgPodmanSearch()).FontSize(12)
		} else {
			ui.Checkbox(c, &dialog.search, locales.DockerImgSearch())
			a.requestDockerImgSearch(dialog)
			if len(dialog.suggestions) != 0 {
				ui.Row(c).FillWidth().Gap(5).Wrap().Children(func() {
					for _, name := range dialog.suggestions {
						if ui.Button(c.Key(name), name).MaxWidth(270).Tooltip(name).Clicked() {
							dialog.value, dialog.search, dialog.suggestions = name, false, nil
						}
					}
				})
			}
		}
	case imgBuild:
		dockerImgBuildFields(c, dialog)
	case imgLoad:
		ui.Checkbox(c, &dialog.importing, locales.DockerImgImportMode())
		widgets.Muted(c, locales.DockerImgArchiveHint()).FontSize(12)
		if dialog.local == "" {
			dockerImgField(c, locales.DockerImgArchivePath(), "/srv/images/image.tar.gz", &dialog.value)
			button := ui.Button(c, locales.DockerImgChooseArchive()).Disabled(s.sftp == nil || dialog.picking)
			if s.sftp == nil {
				button.Tooltip(locales.DockerImgNoSFTP())
			}
			if button.Clicked() {
				a.pickDockerImgArchive(dialog)
			}
		} else {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, filepath.Base(dialog.local)).Grow(1).MinWidth(0).SingleLine().Tooltip(dialog.local)
				if ui.Button(c, locales.DockerImgUseServer()).Clicked() {
					dialog.local = ""
				}
			})
		}
		if dialog.importing {
			dockerImgField(c, locales.DockerImgNewTag(), "team/image:tag", &dialog.platform)
		}
	case imgPrune:
		if ui.Checkbox(c, &dialog.all, locales.DockerImgPruneAll()).Changed() {
			dialog.snapshotPrune(s)
		}
		warning := locales.DockerImgPruneDangling()
		if dialog.all {
			warning = locales.DockerImgPruneWarning()
		}
		ui.Text(c, warning).TextColor(theme.Danger)
		rows := dialog.pruneRows
		if len(rows) > 0 {
			ui.Text(c, locales.DockerImgEstimate(dockerImgSize(rows))).Bold()
			widgets.Muted(c, locales.DockerImgEstimateNote()).FontSize(12)
			dockerImgRefList(c, rows)
		}
	case imgTag:
		dockerImgRefList(c, dialog.rows)
		dockerImgField(c, locales.DockerImgNewTag(), "registry.example/team/image:new-tag", &dialog.value)
		widgets.Muted(c, locales.DockerImgTagWarning()).FontSize(12)
	case imgPush:
		dockerImgRefList(c, dialog.rows)
		widgets.Muted(c, locales.DockerImgPushWarning())
	case imgRemove:
		ui.Text(c, locales.DockerImgRemoveWarning())
		dockerImgRefList(c, dialog.rows)
		button := ui.Checkbox(c, &dialog.force, locales.DockerForceRemove()).Disabled(dialog.tool.Tool == "podman")
		if dialog.tool.Tool == "podman" {
			button.Tooltip(locales.DockerImgPodmanForce())
		}
		if dialog.force || dialog.forceOnly {
			ui.Text(c, locales.DockerImgForceWarning()).TextColor(theme.Danger)
		}
	case imgSave:
		dockerImgRefList(c, dialog.rows)
		dockerImgField(c, locales.DockerImgArchivePath(), "/srv/images/image.tar", &dialog.value)
		ui.Checkbox(c, &dialog.gzip, locales.DockerImgGzip())
		widgets.Muted(c, locales.DockerImgSaveHint()).FontSize(12)
	}
}

func dockerImgBuildFields(c *ui.Context, dialog *dockerImgDialog) {
	dockerImgField(c, locales.DockerImgContext(), "/srv/project", &dialog.value)
	dockerImgField(c, locales.DockerImgDockerfile(), locales.DockerImgDockerfileDefault(), &dialog.build.Dockerfile)
	dockerImgInputRows(c, locales.DockerImgBuildTags(), locales.DockerImgAddTag(), "team/image:tag", &dialog.build.Tags)
	dockerImgInputRows(c, locales.DockerImgBuildArgs(), locales.DockerImgAddArg(), "NAME=value", &dialog.build.Args)
	widgets.Muted(c, locales.DockerImgBuildSecrets()).FontSize(12)
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		ui.Column(c.Key("stage")).Grow(1).MinWidth(0).Children(func() {
			dockerImgField(c, locales.DockerImgTarget(), "runtime", &dialog.build.Target)
		})
		ui.Column(c.Key("platform")).Grow(1).MinWidth(0).Children(func() {
			dockerImgField(c, locales.DockerImgPlatform(), "linux/amd64", &dialog.platform)
		})
	})
	ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
		ui.Checkbox(c, &dialog.build.NoCache, locales.DockerImgNoCache())
		ui.Checkbox(c, &dialog.build.Pull, locales.DockerImgBuildPull())
	})
}

func dockerImgInputRows(c *ui.Context, label, add, hint string, values *[]string) {
	ui.Column(c.Key(label)).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, label).Bold().Grow(1)
			if ui.Button(c, add).Clicked() {
				*values = append(*values, "")
			}
		})
		for index := range *values {
			removed := false
			ui.Row(c.Key(index)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &(*values)[index]).Label(label).Placeholder(hint).Grow(1).MinWidth(0)
				removed = widgets.IconButton(c, "x", locales.DockerImgRemoveField()).Clicked()
			})
			if removed {
				*values = slices.Delete(*values, index, index+1)
				break
			}
		}
	})
}

func (a *App) dockerImgWorkView(c *ui.Context, dialog *dockerImgDialog, height float32, open *bool) {
	work := dialog.work
	status := locales.DockerActionRunning(dialog.title())
	pushAuth := false
	switch {
	case !work.done && dialog.stopping:
		status = locales.DockerImgStopping()
	case !work.done:
	case errors.Is(work.err, context.Canceled):
		status = locales.DockerImgStopped()
	case work.err != nil:
		status = locales.DockerActionFailed()
		output := strings.ToLower(work.output)
		pushAuth = dialog.op == imgPush && (strings.Contains(output, "unauthorized") || strings.Contains(output, "denied") || strings.Contains(output, "authentication required") || strings.Contains(output, "no basic auth"))
	default:
		status = locales.DockerActionDone()
	}
	ui.Text(c, status).Bold()
	if pushAuth {
		ui.Text(c, locales.DockerImgPushAuth()).TextColor(theme.Danger)
	} else if work.err != nil && work.err != context.Canceled {
		ui.Text(c, work.err.Error()).TextColor(theme.Danger).Selectable()
	}
	if work.output != "" {
		dockerOutput(c, work.output, true, min(330, max(height-240, 100)), &work.scroll)
	}
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Wrap().Children(func() {
		if pushAuth && ui.Button(c, locales.DockerPageSystem()).Clicked() {
			*open = false
			a.openDockerPage(dialog.target.session, dockerSecSystem)
		}
		if work.output != "" && ui.Button(c, locales.CopyAll()).Clicked() {
			c.WriteClipboard(work.output)
		}
		if work.done {
			if ui.PrimaryButton(c, locales.Close()).Clicked() {
				*open = false
			}
		} else if (dialog.op == imgPull || dialog.op == imgBuild || dialog.op == imgLoad || dialog.op == imgPush || dialog.op == imgSave) && ui.Button(c, locales.DockerImgStop()).Disabled(dialog.stopping).Clicked() {
			dialog.stopping = true
			dialog.stopWork()
		}
	})
}

func dockerImgReadView(c *ui.Context, dialog *dockerImgDialog, height float32, open *bool) {
	maxHeight := min(max(height-202, 112), 590)
	switch {
	case dialog.loading:
		ui.Spinner(c).Label(locales.DockerImgReading())
	case dialog.readErr != nil:
		ui.Text(c, locales.DockerImgListError(dialog.readErr.Error())).TextColor(theme.Danger).Selectable()
		if dialog.readOutput != "" {
			dockerOutput(c, dialog.readOutput, true, min(maxHeight, 245), &dialog.readScroll)
		}
	case dialog.showJSON:
		dockerOutput(c, inspectJSON(dialog.inspect.Raw), true, maxHeight, &dialog.readScroll)
	default:
		ui.Scroll(c.Key("image-inspect")).FillWidth().MaxHeight(maxHeight).Children(func() {
			ui.Column(c).FillWidth().Gap(10).Padding(0, 8, 0, 1).Children(func() {
				if dialog.op == imgHistory {
					dockerImgHistoryRows(c, dialog.layers)
				} else {
					dockerImgInspectRows(c, dialog)
				}
			})
		})
	}
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Wrap().Children(func() {
		if dialog.op == imgDetails && len(dialog.inspect.Raw) > 0 {
			label := locales.DockerImgRaw()
			if dialog.showJSON {
				label = locales.DockerDetails()
			}
			if ui.Button(c, label).Clicked() {
				dialog.showJSON = !dialog.showJSON
				dialog.readScroll = ui.ScrollState{}
			}
			if ui.Button(c, locales.DockerCopyJSON()).Clicked() {
				c.WriteClipboard(inspectJSON(dialog.inspect.Raw))
				c.Toast(locales.CopiedText(dialog.rows[0].Ref))
			}
		}
		if ui.PrimaryButton(c, locales.Close()).Clicked() {
			*open = false
		}
	})
}

func dockerImgKeys(values map[string]json.RawMessage) string {
	return strings.Join(slices.Sorted(maps.Keys(values)), "\n")
}

func dockerImgInspectRows(c *ui.Context, dialog *dockerImgDialog) {
	image := dialog.inspect
	const labelWidth = 126
	ctrSection(c, locales.DockerSecGeneral(), nil, func() {
		ctrRows(c, []labelValue{{locales.DockerID(), image.ID}, {locales.DockerImgBuildTags(), strings.Join(image.RepoTags, "\n")},
			{locales.DockerImgDigests(), strings.Join(image.RepoDigests, "\n")}, {locales.DockerCreatedAt(), localTime(image.Created)},
			{locales.DockerImgAuthor(), image.Author}, {locales.DockerImgPlatform(), image.Platform()},
			{locales.DockerSize(), formatSize(int64(image.Size))}, {locales.DockerImgLayers(), strconv.Itoa(len(image.RootFS.Layers))},
		}, labelWidth)
	})
	ctrSection(c, locales.DockerImgConfig(), nil, func() {
		ctrRows(c, []labelValue{{locales.DockerCommand(), shellWords(image.Config.Cmd)}, {locales.DockerEntrypoint(), shellWords(image.Config.Entrypoint)},
			{locales.DockerSecPorts(), dockerImgKeys(image.Config.ExposedPorts)}, {locales.DockerVolumes(), dockerImgKeys(image.Config.Volumes)},
			{locales.DockerWorkDir(), image.Config.WorkingDir}, {locales.DockerUser(), image.Config.User},
		}, labelWidth)
	})
	ctrSection(c, locales.DockerSecEnv(), func() {
		label := locales.DockerShowValues()
		if dialog.showEnv {
			label = locales.DockerHideValues()
		}
		if ui.Button(c, label).Clicked() {
			dialog.showEnv = !dialog.showEnv
		}
	}, func() { ctrEnv(c, &monitor.Container{Env: image.Config.Env}, dialog.showEnv) })
	ctrSection(c, locales.DockerSecLabels(), nil, func() { ctrRows(c, ctrLabels(&monitor.Container{Labels: image.Config.Labels}), labelWidth) })
}

func dockerImgHistoryRows(c *ui.Context, rows []monitor.ImageLayer) {
	if len(rows) == 0 {
		widgets.Muted(c, locales.DockerNone())
	}
	for index, layer := range rows {
		ui.Column(c.Key(index)).FillWidth().Gap(4).Padding(8).Border(1, colors.line).Radius(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, locales.DockerImgLayer(index+1)).Bold()
				widgets.Muted(c, localTime(layer.Created)).Grow(1).MinWidth(0).SingleLine()
				widgets.Muted(c, formatSize(int64(layer.Size))).SingleLine()
			})
			ctrRows(c, []labelValue{{locales.DockerImgCreatedBy(), layer.CreatedBy}, {locales.DockerImgComment(), layer.Comment}}, 120)
		})
	}
}
