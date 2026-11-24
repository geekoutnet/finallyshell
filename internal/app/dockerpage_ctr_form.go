package app

import (
	"encoding/json"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type ctrForm struct {
	config                                monitor.CtrConfig
	inspect                               json.RawMessage
	command, entrypoint, labels, aliases  string
	capAdd, capDrop, logOptions, envPaste string
	env                                   []ctrEnvRow
	showEnv, paste, showPreview           bool
	previewScroll                         ui.ScrollState
	section                               int
	title, warning                        string
	edit                                  bool
}

type ctrEnvRow struct{ key, value string }

func (a *App) openCtrForm(target dockerTarget, ct *monitor.Container, duplicate bool) {
	form := &ctrForm{title: locales.DockerCtrNew(), config: monitor.CtrConfig{Pull: true, Restart: "no"}}
	if ct != nil {
		config, err := monitor.CtrConfigOf(ct)
		if err != nil {
			a.notify(locales.DockerCtrInspectMissing())
			return
		}
		form.config = config
		form.title = locales.DockerCtrEdit()
		form.edit = !duplicate
		if duplicate {
			form.title, form.warning = locales.DockerCtrDuplicate(), locales.DockerCtrDuplicateNote()
			form.config.Name += "-copy"
			form.config.IPv4, form.config.Aliases, form.config.Ports = "", nil, nil
			form.config.Labels = slices.DeleteFunc(form.config.Labels, func(value string) bool {
				return strings.HasPrefix(value, "com.docker.compose.") || strings.HasPrefix(value, "io.podman.compose.")
			})
			target.id, target.name, target.project = "", "", ""
		} else {
			form.inspect = slices.Clone(ct.Inspect)
		}
		form.command, form.entrypoint = ctrArgsText(config.Command), ctrArgsText(config.Entrypoint)
		form.labels = strings.Join(form.config.Labels, "\n")
		form.aliases = strings.Join(form.config.Aliases, " ")
		form.capAdd, form.capDrop = strings.Join(config.CapAdd, " "), strings.Join(config.CapDrop, " ")
		form.logOptions = strings.Join(config.LogOptions, "\n")
		for _, pair := range config.Env {
			key, value, _ := strings.Cut(pair, "=")
			form.env = append(form.env, ctrEnvRow{key, value})
		}
	}
	a.setCtrModal(&ctrModal{target: target, form: form})
}

func ctrArgsText(args []string) string {
	if len(args) == 0 {
		return ""
	}
	data, _ := json.Marshal(args)
	return string(data)
}

func ctrArgs(text string) ([]string, bool) {
	if strings.TrimSpace(text) == "" {
		return nil, true
	}
	var values []string
	return values, json.Unmarshal([]byte(text), &values) == nil && values != nil
}

func ctrNonemptyLines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (form *ctrForm) sync() string {
	config := &form.config
	var valid bool
	if config.Command, valid = ctrArgs(form.command); !valid {
		return locales.DockerCtrArgsHint()
	}
	if config.Entrypoint, valid = ctrArgs(form.entrypoint); !valid {
		return locales.DockerCtrArgsHint()
	}
	config.Labels, config.LogOptions = ctrNonemptyLines(form.labels), ctrNonemptyLines(form.logOptions)
	config.Aliases, config.CapAdd, config.CapDrop = strings.Fields(form.aliases), strings.Fields(form.capAdd), strings.Fields(form.capDrop)
	config.Env = config.Env[:0]
	for _, row := range form.env {
		config.Env = append(config.Env, row.key+"="+row.value)
	}
	return ""
}

func (a *App) ctrFormView(c *ui.Context, modal *ctrModal, height float32, open *bool) {
	form := modal.form
	config := &form.config
	docker := &modal.target.session.overview.docker.d
	ui.Text(c, form.title).FontSize(16).Bold()
	if form.warning != "" {
		widgets.Muted(c, form.warning).FontSize(12)
	}
	ui.ScrollHorizontal(c).FillWidth().Scrollbars(false).Children(func() {
		ui.Segmented(c, &form.section, locales.DockerCtrGeneral(), locales.DockerColPorts(), locales.DockerSecMounts(), locales.DockerSecEnv(),
			locales.DockerSecNetwork(), locales.DockerResources(), locales.DockerCtrAdvanced()).Label(form.title)
	})
	ui.Scroll(c.Key(form.section)).MaxHeight(min(420, max(height-310, 135))).FillWidth().Children(func() {
		ui.Column(c).Gap(10).Padding(6, 12, 10, 0).FillWidth().Children(func() {
			switch form.section {
			case 0:
				form.general(c, docker)
			case 1:
				form.ports(c)
			case 2:
				form.mounts(c, docker)
			case 3:
				form.environment(c)
			case 4:
				form.network(c, docker)
			case 5:
				form.resources(c)
			case 6:
				form.advanced(c)
			}
		})
	})
	reason := form.sync()
	preview := ""
	if reason == "" {
		var err error
		preview, err = monitor.CmdOf(docker).CtrRunPreview(*config)
		if err != nil {
			reason = ctrErrorText(err)
		}
	}
	task := &ctrTask{target: modal.target, kind: "create", form: form, title: locales.DockerCtrCreate()}
	if form.edit {
		task.kind, task.title = "edit", locales.DockerCtrApply()
		task.ids, task.names = []string{modal.target.id}, []string{modal.target.name}
	}
	if reason == "" {
		reason = task.unavailable(a)
	}
	if reason != "" && (form.section != 0 || config.Image != "" && config.Name != "") {
		ui.Text(c, reason).FontSize(12).TextColor(theme.Danger)
	}
	previewLabel := locales.DockerCtrPreview()
	if form.edit {
		previewLabel = locales.DockerCtrEditPreview()
	}
	ui.Checkbox(c, &form.showPreview, previewLabel)
	if form.showPreview && preview != "" {
		dockerOutput(c, preview, true, 65, &form.previewScroll)
	}
	ui.Row(c).Gap(8).Wrap().Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if form.showPreview && ui.Button(c, locales.Copy()).Disabled(preview == "").Clicked() {
			c.WriteClipboard(preview)
			c.Toast(locales.CopiedText(previewLabel))
		}
		if ui.Button(c, locales.Cancel()).Clicked() {
			*open = false
		}
		if ui.PrimaryButton(c, task.title).Disabled(reason != "").Clicked() {
			modal.task = task
		}
	})
}

func ctrField(c *ui.Context, label string, value *string, hint string, choices []string) {
	ui.Column(c.Key(label)).Gap(3).Grow(1).MinWidth(0).FillWidth().Children(func() {
		widgets.Muted(c, label).FontSize(12)
		ui.Row(c).Gap(4).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.TextInput(c, value).Label(label).Placeholder(hint).Grow(1).MinWidth(0)
			ctrSuggestions(c, label, value, choices)
		})
	})
}

func ctrSuggestions(c *ui.Context, label string, value *string, choices []string) {
	if len(choices) == 0 {
		return
	}
	label = locales.DockerCtrSuggestions(label)
	ui.MenuButton(c, "", func(menu *ui.Menu) {
		menu.MaxWidth(420)
		for _, choice := range choices {
			if menu.Item(choice).Checked(choice == *value).Chosen() {
				*value = choice
				c.Invalidate()
			}
		}
	}).Label(label).Tooltip(label).Shrink(0)
}

type ctrFormField struct {
	label string
	value *string
	hint  string
}

func ctrFieldRow(c *ui.Context, fields ...ctrFormField) {
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		for _, field := range fields {
			ctrField(c, field.label, field.value, field.hint, nil)
		}
	})
}

func ctrArea(c *ui.Context, label, hint string, value *string, masked bool) {
	ui.Column(c.Key(label)).Gap(3).FillWidth().Children(func() {
		widgets.Muted(c, label).FontSize(12)
		field := ui.TextArea(c, value).Label(label).Placeholder(hint).Height(78).FillWidth()
		if masked {
			field.Password()
		}
	})
}

func (form *ctrForm) general(c *ui.Context, docker *monitor.Docker) {
	config := &form.config
	var images []string
	for _, image := range docker.Images {
		if image.Repository != "" && image.Repository != "<none>" && image.Tag != "" && image.Tag != "<none>" {
			images = append(images, image.Repository+":"+image.Tag)
		}
	}
	ctrField(c, locales.DockerCtrImageTag(), &config.Image, "nginx:alpine", ctrChoices(images))
	ui.Checkbox(c, &config.Pull, locales.DockerCtrPull())
	ctrField(c, locales.FieldName(), &config.Name, "web", nil)
	ctrField(c, locales.DockerCommand(), &form.command, `["nginx", "-g", "daemon off;"]`, nil)
	ctrField(c, locales.DockerEntrypoint(), &form.entrypoint, `["/bin/sh"]`, nil)
	widgets.Muted(c, locales.DockerCtrArgsHint()).FontSize(12)
	ctrFieldRow(c, ctrFormField{locales.DockerUser(), &config.User, "1000:1000"}, ctrFormField{locales.DockerHostname(), &config.Hostname, "web"})
	ctrField(c, locales.DockerWorkDir(), &config.WorkDir, "/app", nil)
}

func ctrChoices(values []string) []string {
	values = slices.DeleteFunc(values, func(value string) bool { return value == "" })
	slices.Sort(values)
	return slices.Compact(values)
}

func (form *ctrForm) ports(c *ui.Context) {
	config := &form.config
	ui.Checkbox(c, &config.PublishAll, locales.DockerCtrPublishAll())
	remove := -1
	for index := range config.Ports {
		port := &config.Ports[index]
		ui.Row(c.Key(index)).Gap(8).AlignItems(ui.End).FillWidth().Children(func() {
			ctrField(c, locales.DockerCtrHostIP(), &port.IP, "127.0.0.1", nil)
			ctrField(c, locales.DockerCtrHostPort(), &port.Host, "8080", nil)
			ctrField(c, locales.DockerCtrContainerPort(), &port.Container, "80", nil)
			ui.Select(c, &port.Protocol, []string{"tcp", "udp", "sctp"}).Label(locales.DockerCtrProtocol()).Width(84).Shrink(0)
			if ctrRemoveRow(c) {
				remove = index
			}
		})
	}
	config.Ports = ctrRemoveAt(config.Ports, remove)
	if ui.Button(c, locales.DockerCtrAddPort()).AlignSelf(ui.Start).Clicked() {
		config.Ports = append(config.Ports, monitor.CtrPort{Protocol: "tcp"})
	}
}

func ctrRemoveAt[Value any](rows []Value, index int) []Value {
	if index >= 0 {
		return slices.Delete(rows, index, index+1)
	}
	return rows
}

func ctrRemoveRow(c *ui.Context) bool {
	return widgets.IconButton(c, "trash-2", locales.DockerCtrRemoveRow()).Shrink(0).Clicked()
}

func (form *ctrForm) mounts(c *ui.Context, docker *monitor.Docker) {
	config := &form.config
	var volumes []string
	for _, volume := range docker.Volumes {
		volumes = append(volumes, volume.Name)
	}
	volumes = ctrChoices(volumes)
	remove := -1
	for index := range config.Mounts {
		mount := &config.Mounts[index]
		ui.Row(c.Key(index)).Gap(8).AlignItems(ui.End).FillWidth().Children(func() {
			ctrField(c, locales.DockerCtrMountSource(), &mount.Source, "/srv/data", volumes)
			ctrField(c, locales.DockerCtrMountTarget(), &mount.Target, "/data", nil)
			ui.Checkbox(c, &mount.ReadOnly, "RO").Label(locales.DockerReadOnlyMount()).Tooltip(locales.DockerReadOnlyMount()).Shrink(0)
			if ctrRemoveRow(c) {
				remove = index
			}
		})
	}
	config.Mounts = ctrRemoveAt(config.Mounts, remove)
	if ui.Button(c, locales.DockerCtrAddMount()).AlignSelf(ui.Start).Clicked() {
		config.Mounts = append(config.Mounts, monitor.CtrMount{})
	}
}

func (form *ctrForm) environment(c *ui.Context) {
	ui.Checkbox(c, &form.showEnv, locales.DockerCtrShowValues())
	remove := -1
	for index := range form.env {
		entry := &form.env[index]
		ui.Row(c.Key(index)).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.TextInput(c, &entry.key).Label(locales.DockerCtrKey()).Placeholder("KEY").Grow(1).MinWidth(0)
			value := ui.TextInput(c, &entry.value).Label(locales.DockerCtrValue()).Grow(2).MinWidth(0)
			if !form.showEnv {
				value.Password()
			}
			if ctrRemoveRow(c) {
				remove = index
			}
		})
	}
	form.env = ctrRemoveAt(form.env, remove)
	ui.Row(c).Gap(8).Wrap().Children(func() {
		if ui.Button(c, locales.DockerCtrAddEnv()).Clicked() {
			form.env = append(form.env, ctrEnvRow{})
		}
		if ui.Button(c, locales.DockerCtrPasteEnv()).Clicked() {
			form.paste = !form.paste
		}
	})
	if form.paste {
		ctrArea(c, locales.DockerCtrPasteEnv(), "KEY=VALUE", &form.envPaste, !form.showEnv)
		lines := ctrNonemptyLines(form.envPaste)
		valid := len(lines) > 0 && !slices.ContainsFunc(lines, func(line string) bool {
			return !strings.Contains(line, "=") || strings.HasPrefix(line, "=")
		})
		if ui.Button(c, locales.DockerCtrImportEnv()).Disabled(!valid).AlignSelf(ui.Start).Clicked() {
			for _, line := range lines {
				key, value, _ := strings.Cut(line, "=")
				if index := slices.IndexFunc(form.env, func(row ctrEnvRow) bool { return row.key == key }); index >= 0 {
					form.env[index].value = value
				} else {
					form.env = append(form.env, ctrEnvRow{key, value})
				}
			}
			form.paste, form.envPaste = false, ""
		}
	}
	ctrArea(c, locales.DockerSecLabels(), "KEY=VALUE", &form.labels, false)
}

func (form *ctrForm) network(c *ui.Context, docker *monitor.Docker) {
	config := &form.config
	var networks []string
	for _, network := range docker.Networks {
		networks = append(networks, network.Name)
	}
	ctrField(c, locales.DockerNetworkMode(), &config.Network, "bridge", ctrChoices(networks))
	ctrField(c, locales.DockerCtrStaticIP(), &config.IPv4, "192.0.2.8", nil)
	ctrField(c, locales.DockerCtrAliases(), &form.aliases, "app backend", nil)
}

func (form *ctrForm) resources(c *ui.Context) {
	config := &form.config
	ctrField(c, locales.DockerRestartPolicy(), &config.Restart, "on-failure:3", monitor.RestartPolicies)
	ctrFieldRow(c, ctrFormField{locales.DockerCPULimit(), &config.CPUs, "1.5"}, ctrFormField{locales.DockerCPUShares(), &config.Shares, "1024"})
	ctrFieldRow(c, ctrFormField{locales.DockerMemLimit(), &config.Memory, "512m"}, ctrFormField{locales.DockerMemSwap(), &config.MemorySwap, "1g"})
}

func (form *ctrForm) advanced(c *ui.Context) {
	config := &form.config
	ui.Row(c).Gap(10).Wrap().Children(func() {
		ui.Checkbox(c, &config.Privileged, locales.DockerPrivileged())
		ui.Checkbox(c, &config.AutoRemove, locales.DockerCtrAutoRemove()).Disabled(form.edit).Tooltip(locales.DockerCtrAutoRemoveReason())
		ui.Checkbox(c, &config.Init, locales.DockerCtrInit())
		ui.Checkbox(c, &config.TTY, locales.DockerCtrTTY())
		ui.Checkbox(c, &config.Stdin, locales.DockerCtrStdin())
	})
	ctrFieldRow(c, ctrFormField{locales.DockerCtrCapAdd(), &form.capAdd, "NET_ADMIN"}, ctrFormField{locales.DockerCtrCapDrop(), &form.capDrop, "ALL"})
	remove := -1
	for index := range config.Devices {
		device := &config.Devices[index]
		ui.Row(c.Key(index)).Gap(8).AlignItems(ui.End).FillWidth().Children(func() {
			ctrField(c, locales.DockerCtrDeviceSource(), &device.Source, "/dev/fuse", nil)
			ctrField(c, locales.DockerCtrDeviceTarget(), &device.Target, "/dev/fuse", nil)
			ctrField(c, locales.ColPermissions(), &device.Permissions, "rwm", nil)
			if ctrRemoveRow(c) {
				remove = index
			}
		})
	}
	config.Devices = ctrRemoveAt(config.Devices, remove)
	if ui.Button(c, locales.DockerCtrAddDevice()).AlignSelf(ui.Start).Clicked() {
		config.Devices = append(config.Devices, monitor.CtrDevice{})
	}
	ctrField(c, locales.DockerLogDriver(), &config.LogDriver, "json-file", nil)
	ctrArea(c, locales.DockerCtrLogOptions(), "max-size=10m", &form.logOptions, !form.showEnv)
}
