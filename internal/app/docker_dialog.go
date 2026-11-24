package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// A container's details are a dialog over the main window, in sections:
// what it is and runs, its networks, ports, mounts, limits and usage,
// environment, labels and logging. A click on a line copies its value;
// the environment's values, which often hold passwords, show masked
// until asked. Copy as JSON copies the container's docker inspect.

// containerDialog is the dialog of a container of s's Docker cards, by
// its ID, so that a new read shows the container as it is now.
type containerDialog struct {
	s       *Session
	id      string
	showEnv bool
	action  *dockerActionDialog
	logs    *dockerLogsDialog
}

func (a *App) openContainer(s *Session, id string) {
	a.ctrDlg = &containerDialog{s: s, id: id}
}

// dialogContainer is the container d shows, nil once it is gone.
func (d *containerDialog) container() *monitor.Container {
	return dockerContainer(d.s, d.id)
}

func dockerContainer(s *Session, id string) *monitor.Container {
	list := s.overview.docker.d.Containers
	if i := slices.IndexFunc(list, func(ct monitor.Container) bool { return ct.ID == id }); i >= 0 {
		return &list[i]
	}
	return nil
}

// containerDialogView is the dialog, while one is open.
func (a *App) containerDialogView(c *ui.Context) {
	d := a.ctrDlg
	if d == nil {
		for _, s := range a.sessions {
			state := &s.overview.docker
			if state.actionError != nil {
				d = &containerDialog{s: s, action: state.actionError}
				state.actionError = nil
				a.ctrDlg = d
				break
			}
		}
		if d == nil {
			return
		}
	}
	if d.action != nil {
		a.dockerActionView(c, d)
		return
	}
	if d.logs != nil {
		a.dockerLogsView(c, d)
		return
	}
	ct := d.container()
	if ct == nil {
		a.ctrDlg = nil
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		w, h := c.Size()
		width := min(max(w-64, 320), 760)
		ui.Column(c).Width(width).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				widgets.StatusDot(c, ctrColor(ct))
				ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, ct.Name).FontSize(16).Bold().SingleLine().Selectable()
					widgets.Muted(c, ct.Image+"  ·  "+ctrStatusText(ct, time.Now())).SingleLine()
				})
			})
			ui.Scroll(c).MaxHeight(max(h-190, 200)).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(10).Padding(0, 8, 0, 0).Children(func() {
					a.containerSections(c, d, ct)
				})
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if iconTextButton(c, "copy", locales.DockerCopyJSON()).Clicked() {
					c.WriteClipboard(inspectJSON(ct.Inspect))
					c.Toast(locales.CopiedText(ct.Name))
				}
				ui.Spacer(c)
				if ui.PrimaryButton(c, locales.Close()).Clicked() {
					open = false
				}
			})
		})
	})
	if !open && a.ctrDlg == d {
		a.ctrDlg = nil
	}
}

// inspectJSON is a container's docker inspect, indented.
func inspectJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// ctrSection is a part of the dialog: a title over a box of lines.
func ctrSection(c *ui.Context, title string, actions, body func()) {
	ui.Column(c.Key(title)).FillWidth().Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Height(24).Children(func() {
			ui.Text(c, title).Bold().Grow(1).MinWidth(0)
			if actions != nil {
				actions()
			}
		})
		ui.Column(c).FillWidth().Border(1, colors.line).Radius(6).Padding(4).Children(body)
	})
}

// ctrRows are lines of a section, empty values left out, or None for
// none at all.
func ctrRows(c *ui.Context, rows []labelValue, labelWidth float32) {
	n := 0
	for i, r := range rows {
		if r.value == "" {
			continue
		}
		copyRow(c.Key(i), r.label, r.value, labelWidth, true)
		n++
	}
	if n == 0 {
		widgets.Muted(c, locales.DockerNone()).Padding(2, 4)
	}
}

// localTime is t as the dialog shows it, "" when it is not known.
func localTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func yesNo(b bool) string {
	if b {
		return locales.DockerYes()
	}
	return locales.DockerNo()
}

// shellWords joins a command's words as a shell would read them.
func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = shellQuote(w)
	}
	return strings.Join(quoted, " ")
}

const ctrLabelW = 150

func (a *App) containerSections(c *ui.Context, d *containerDialog, ct *monitor.Container) {
	ctrSection(c, locales.DockerSecGeneral(), nil, func() { ctrRows(c, ctrGeneral(ct), ctrLabelW) })
	ctrSection(c, locales.DockerSecNetwork(), nil, func() { ctrNetwork(c, ct) })
	ctrSection(c, locales.DockerSecPorts(), nil, func() { ctrRows(c, ctrPorts(ct), ctrLabelW) })
	ctrSection(c, locales.DockerSecMounts(), nil, func() { ctrMounts(c, ct) })
	ctrSection(c, locales.DockerSecResources(), nil, func() { ctrRows(c, ctrResources(ct), ctrLabelW) })
	ctrSection(c, locales.DockerSecEnv(), func() {
		if len(ct.Env) == 0 {
			return
		}
		icon, label := "eye", locales.DockerShowValues()
		if d.showEnv {
			icon, label = "eye-off", locales.DockerHideValues()
		}
		if iconTextButton(c, icon, label).Clicked() {
			d.showEnv = !d.showEnv
		}
	}, func() { ctrEnv(c, ct, d.showEnv) })
	ctrSection(c, locales.DockerSecLabels(), nil, func() { ctrRows(c, ctrLabels(ct), 260) })
	ctrSection(c, locales.DockerSecLogging(), nil, func() {
		rows := []labelValue{{locales.DockerLogDriver(), ct.LogDriver}}
		for _, k := range sortedKeys(ct.LogOptions) {
			rows = append(rows, labelValue{k, ct.LogOptions[k]})
		}
		ctrRows(c, rows, ctrLabelW)
	})
}

func ctrGeneral(ct *monitor.Container) []labelValue {
	status := ctrStatusText(ct, time.Now())
	if ct.Error != "" {
		status += ": " + ct.Error
	}
	health := ""
	if ct.Health != "" {
		health = healthText(ct.Health)
		if ct.FailingStreak > 0 {
			health = locales.DockerHealthValue(health, ct.FailingStreak)
		}
	}
	short := ct.ID
	if len(short) > 12 {
		short = short[:12]
	}
	policy := ct.RestartPolicy
	if policy == "on-failure" && ct.MaxRetries > 0 {
		policy = locales.DockerRestartRetries(policy, ct.MaxRetries)
	}
	pid := ""
	if ct.Running && ct.PID > 0 {
		pid = strconv.Itoa(ct.PID)
	}
	finished := ""
	if !ct.Running {
		finished = localTime(ct.FinishedAt)
	}
	opt := func(on bool) string {
		if on {
			return locales.DockerYes()
		}
		return ""
	}
	return []labelValue{
		{locales.DockerStatus(), status},
		{locales.DockerHealth(), health},
		{locales.DockerLastCheck(), ct.HealthOutput},
		{locales.DockerID(), short},
		{locales.DockerFullID(), ct.ID},
		{locales.DockerImage(), ct.Image},
		{locales.DockerImageID(), ct.ImageID},
		{locales.DockerCreatedAt(), localTime(ct.Created)},
		{locales.DockerStartedAt(), localTime(ct.StartedAt)},
		{locales.DockerFinishedAt(), finished},
		{locales.DockerPID(), pid},
		{locales.DockerRestartPolicy(), policy},
		{locales.DockerRestarts(), strconv.Itoa(ct.RestartCount)},
		{locales.DockerAutoRemove(), opt(ct.AutoRemove)},
		{locales.DockerEntrypoint(), shellWords(ct.Entrypoint)},
		{locales.DockerCommand(), shellWords(ct.Command)},
		{locales.DockerWorkDir(), ct.WorkingDir},
		{locales.DockerUser(), ct.User},
		{locales.DockerHostname(), ct.Hostname},
		{locales.DockerPrivileged(), yesNo(ct.Privileged)},
		{locales.DockerReadOnly(), opt(ct.ReadOnly)},
		{locales.DockerCapAdd(), strings.Join(ct.CapAdd, ", ")},
		{locales.DockerProject(), ct.Project},
		{locales.DockerService(), ct.Service},
		{locales.DockerComposeDir(), ct.ComposeDir},
		{locales.DockerComposeFiles(), ct.ComposeFiles},
	}
}

// withPrefix is an address with its prefix length, as 172.18.0.2/16.
func withPrefix(ip string, n int) string {
	if ip == "" || n <= 0 {
		return ip
	}
	return ip + "/" + strconv.Itoa(n)
}

func ctrNetwork(c *ui.Context, ct *monitor.Container) {
	copyRow(c.Key("mode"), locales.DockerNetworkMode(), orDash(ct.NetworkMode), ctrLabelW, true)
	for i, n := range ct.Networks {
		ui.Column(c.Key(i)).FillWidth().Children(func() {
			ui.Text(c, n.Name).FontSize(12).Bold().TextColor(theme.Accent).Padding(6, 4, 2, 4).SingleLine().Tooltip(n.Name)
			ctrRows(c, []labelValue{
				{locales.DockerIPv4(), withPrefix(n.IP, n.PrefixLen)},
				{locales.DockerGateway(), n.Gateway},
				{locales.DockerIPv6(), withPrefix(n.IPv6, n.IPv6PrefixLen)},
				{locales.DockerIPv6Gateway(), n.IPv6Gateway},
				{locales.DockerMAC(), n.MAC},
				{locales.DockerAliases(), strings.Join(n.Aliases, ", ")},
			}, ctrLabelW)
		})
	}
}

// ctrPorts are the ports published, the host's addresses after each
// container port, then those exposed only.
func ctrPorts(ct *monitor.Container) []labelValue {
	var rows []labelValue
	for _, p := range ct.Ports {
		ip := p.HostIP
		if ip == "" {
			ip = "*"
		}
		port := strconv.Itoa(p.HostPort)
		host := hostAddr(ip, port)
		if p.AlsoIPv6 {
			host += ", " + hostAddr("::", port)
		}
		rows = append(rows, labelValue{strconv.Itoa(p.Port) + "/" + p.Proto, host})
	}
	for _, e := range ct.Exposed {
		rows = append(rows, labelValue{e, locales.DockerNotPublished()})
	}
	return rows
}

func ctrMounts(c *ui.Context, ct *monitor.Container) {
	if len(ct.Mounts) == 0 {
		widgets.Muted(c, locales.DockerNone()).Padding(2, 4)
	}
	for i, m := range ct.Mounts {
		mode := locales.DockerReadWrite()
		copied := m.Source + ":" + m.Destination
		if !m.RW {
			mode = locales.DockerReadOnlyMount()
			copied += ":ro"
		}
		src := m.Source
		if m.Name != "" && m.Type == "volume" {
			src = m.Name
		}
		copyRowAs(c.Key(i), m.Type, src+" → "+m.Destination+"  ("+mode+")", copied, 70, true)
	}
}

// cpuCount is a number of CPUs without trailing zeros: 1.5, 2.
func cpuCount(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func ctrResources(ct *monitor.Container) []labelValue {
	cpu := locales.DockerUnlimited()
	switch {
	case ct.NanoCPUs > 0:
		cpu = locales.DockerCPUs(cpuCount(float64(ct.NanoCPUs) / 1e9))
	case ct.CPUQuota > 0 && ct.CPUPeriod > 0:
		cpu = locales.DockerCPUs(cpuCount(float64(ct.CPUQuota) / float64(ct.CPUPeriod)))
	}
	mem := locales.DockerUnlimited()
	if ct.Memory > 0 {
		mem = formatSize(ct.Memory)
	}
	size := func(n int64) string {
		if n <= 0 {
			return ""
		}
		return formatSize(n)
	}
	shares, pids := "", ""
	if ct.CPUShares > 0 {
		shares = strconv.FormatInt(ct.CPUShares, 10)
	}
	if ct.PidsLimit > 0 {
		pids = strconv.FormatInt(ct.PidsLimit, 10)
	}
	rows := []labelValue{
		{locales.DockerCPULimit(), cpu},
		{locales.DockerCPUShares(), shares},
		{locales.DockerCPUSet(), ct.CPUSet},
		{locales.DockerMemLimit(), mem},
		{locales.DockerMemSwap(), size(ct.MemSwap)},
		{locales.DockerMemReservation(), size(ct.MemResv)},
		{locales.DockerPidsLimit(), pids},
	}
	if st := ct.Stats; st != nil {
		memUse := formatSize(int64(st.MemUsed))
		if st.MemLimit > 0 {
			memUse += " / " + formatSize(int64(st.MemLimit)) + fmt.Sprintf(" (%.2f%%)", st.MemPercent)
		}
		rows = append(rows,
			labelValue{locales.DockerCPUUsage(), fmt.Sprintf("%.2f%%", st.CPU)},
			labelValue{locales.DockerMemUsage(), memUse},
			labelValue{locales.DockerNetIO(), locales.DockerNetIOValue(formatSize(int64(st.NetRx)), formatSize(int64(st.NetTx)))},
			labelValue{locales.DockerBlockIO(), locales.DockerBlockIOValue(formatSize(int64(st.BlockRead)), formatSize(int64(st.BlockWrite)))},
			labelValue{locales.DockerProcesses(), strconv.Itoa(st.PIDs)},
		)
	}
	return rows
}

// secretMask stands for a value that does not show.
const secretMask = "••••••••"

// ctrEnv is the environment, NAME and value; a click copies NAME=value,
// also while the values are masked.
func ctrEnv(c *ui.Context, ct *monitor.Container, show bool) {
	if len(ct.Env) == 0 {
		widgets.Muted(c, locales.DockerNone()).Padding(2, 4)
	}
	for i, e := range ct.Env {
		name, value, _ := strings.Cut(e, "=")
		shown := value
		if !show && value != "" {
			shown = secretMask
		}
		copyRowAs(c.Key(i), name, shown, e, 260, true)
	}
}

func ctrLabels(ct *monitor.Container) []labelValue {
	var rows []labelValue
	for _, k := range sortedKeys(ct.Labels) {
		v := ct.Labels[k]
		if v == "" {
			v = `""`
		}
		rows = append(rows, labelValue{k, v})
	}
	return rows
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
