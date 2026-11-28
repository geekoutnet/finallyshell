package app

import (
	"context"
	"math"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dockerRunner func(context.Context, *ssh.Client, string, func(string)) (string, error)

type dockerTarget struct {
	session           *Session
	client            *ssh.Client
	id, project, name string
}

func dockerRowTarget(s *Session, ct *monitor.Container) dockerTarget {
	return dockerTarget{session: s, client: s.client, id: ct.ID, project: ct.Project, name: ct.Name}
}

func (target dockerTarget) available() bool {
	s := target.session
	if s.client != target.client || s.status != statusConnected || !execAllowed(s.Host) {
		return false
	}
	state := &s.overview.docker
	return state.connection == s.ctx && state.dataClient == target.client && state.d.Tool != "" && state.d.State == monitor.DockerOK && (target.client != nil || state.run != nil || s.overview.memory.privilegeRunner != nil)
}

func (target dockerTarget) projectInfo() (monitor.Compose, []*monitor.Container) {
	var project monitor.Compose
	var members []*monitor.Container
	list := target.session.overview.docker.d.Containers
	for index := range list {
		ct := &list[index]
		if ct.Project != target.project {
			continue
		}
		members = append(members, ct)
		if project.Project == "" || !composeMetadata(project) {
			project = monitor.ComposeOf(ct)
		}
	}
	return project, members
}

func composeMetadata(project monitor.Compose) bool {
	return strings.HasPrefix(project.Dir, "/") && len(project.Files) > 0
}

type dockerAction int

const (
	dockerStart dockerAction = iota
	dockerStop
	dockerRestart
	dockerKill
	dockerPause
	dockerUnpause
	dockerRemove
	dockerForceRemove
	dockerRename
	dockerPolicy
	dockerRecreate
	dockerDown
)

var dockerOps = [...]monitor.CtrOp{monitor.OpStart, monitor.OpStop, monitor.OpRestart, monitor.OpKill, monitor.OpPause, monitor.OpUnpause}

func (action dockerAction) title(project bool) string {
	if project {
		switch action {
		case dockerStart:
			return locales.DockerProjectStart()
		case dockerStop:
			return locales.DockerProjectStop()
		case dockerRestart:
			return locales.DockerProjectRestart()
		case dockerRecreate:
			return locales.DockerProjectRecreate()
		}
	}
	switch action {
	case dockerStart:
		return locales.DockerStart()
	case dockerStop:
		return locales.DockerStop()
	case dockerRestart:
		return locales.DockerRestart()
	case dockerKill:
		return locales.DockerKill()
	case dockerPause:
		return locales.DockerPause()
	case dockerUnpause:
		return locales.DockerUnpause()
	case dockerRemove:
		return locales.DockerRemove()
	case dockerForceRemove:
		return locales.DockerForceRemove()
	case dockerRename:
		return locales.Rename()
	case dockerPolicy:
		return locales.DockerRestartPolicy()
	case dockerRecreate:
		return locales.DockerRecreate()
	default:
		return locales.DockerProjectDown()
	}
}

func (action dockerAction) allowed(ct *monitor.Container, tool string) bool {
	if ct == nil || !monitor.ValidName(ct.ID) || ct.Status == "removing" {
		return false
	}
	paused := ct.Paused || ct.Status == "paused"
	running := ct.Status == "running" && !paused
	stopped := (ct.Status == "created" || ct.Status == "exited" || ct.Status == "stopped" || ct.Status == "configured") && !paused
	switch action {
	case dockerStart:
		return stopped
	case dockerStop, dockerRestart:
		return running || ct.Status == "restarting"
	case dockerKill, dockerPause:
		return running
	case dockerUnpause:
		return paused
	case dockerRemove:
		return stopped || ct.Status == "dead"
	case dockerPolicy:
		return !ct.AutoRemove && ct.Status != "dead"
	case dockerRecreate:
		if ct.Project != "" {
			return !paused && monitor.ValidName(ct.Service) && composeMetadata(monitor.ComposeOf(ct))
		}
		return tool == "docker" && !ct.AutoRemove && !paused && (running || stopped) && len(ct.Inspect) > 0
	default:
		return true
	}
}

func (target dockerTarget) allow(action dockerAction) bool {
	state := &target.session.overview.docker
	if !target.available() || state.work != nil {
		return false
	}
	if target.id != "" {
		ct := dockerContainer(target.session, target.id)
		return ct != nil && ct.Name == target.name && ct.Project == target.project && action.allowed(ct, state.d.Tool)
	}
	project, members := target.projectInfo()
	switch action {
	case dockerRecreate, dockerDown:
		return composeMetadata(project)
	default:
		return slices.ContainsFunc(members, func(ct *monitor.Container) bool { return action.allowedProjectMember(ct, state.d.Tool) })
	}
}

func (action dockerAction) allowedProjectMember(ct *monitor.Container, tool string) bool {
	return action.allowed(ct, tool) || action == dockerRestart && dockerStart.allowed(ct, tool)
}

const ctrMenuW = 24

func dockerMoreButton(c *ui.Context, name string, menu func(*ui.Menu)) {
	label := locales.DockerMore(name)
	button := ui.Box(c.Key("docker-more")).Size(ctrMenuW, ctrMenuW).Shrink(0).Center().Radius(4).
		Role(ui.RoleButton).Label(label).Tooltip(label).Cursor(ui.CursorPointer)
	if button.Hovered() {
		button.Background(theme.Text.Alpha(0.09))
	}
	button.Children(func() { widgets.Muted(c, "⋯").FontSize(17).SingleLine() })
	button.Menu(menu)
}

func (a *App) dockerContainerMenu(c *ui.Context, m *ui.Menu, s *Session, ct *monitor.Container) {
	target := dockerRowTarget(s, ct)
	tool := monitor.CmdOf(&s.overview.docker.d)
	for _, action := range []dockerAction{dockerStart, dockerStop, dockerRestart, dockerKill, dockerPause, dockerUnpause} {
		if action == dockerPause && (ct.Paused || ct.Status == "paused") || action == dockerUnpause && !ct.Paused && ct.Status != "paused" {
			continue
		}
		a.dockerActionItem(m, target, action)
	}
	m.Separator()
	readable := target.available() && monitor.ValidName(ct.ID)
	if m.Item(locales.DockerLogs()).Disabled(!readable).Chosen() {
		a.openDockerLogs(target)
	}
	terminalReady := canSplit(s) && s.client != nil
	terminalReason := ""
	if !readable || !terminalReady {
		terminalReason = locales.DockerUnavailable()
	} else if !ctrExecuting(ct) {
		terminalReason = locales.DockerCtrMustRun()
	}
	if m.Item(locales.DockerTerminal()).Disabled(terminalReason != "").Subtitle(terminalReason).Chosen() {
		a.openDockerTerminal(target, tool)
	}
	if m.Item(locales.DockerDetails()).Chosen() {
		a.openContainer(s, ct.ID)
	}
	m.Submenu(locales.Copy(), func(sub *ui.Menu) {
		for _, item := range []labelValue{
			{locales.DockerColName(), ct.Name}, {locales.DockerID(), ct.ID},
			{locales.DockerColIP(), strings.Join(ctrIPs(ct), "\n")}, {locales.DockerColImage(), ct.Image},
			{locales.DockerColPorts(), strings.Join(portTexts(ct.Ports), "\n")},
		} {
			if sub.Item(item.label).Disabled(item.value == "").Chosen() {
				c.WriteClipboard(item.value)
				c.Toast(locales.CopiedText(item.label))
			}
		}
	})
	ports := dockerPortURLs(s.Host.Addr, s.ip, ct.Ports)
	m.Submenu(locales.DockerOpenPort(), func(sub *ui.Menu) {
		for _, address := range ports {
			if sub.Item(address).Chosen() {
				c.OpenURL(address)
			}
		}
	}).Disabled(len(ports) == 0)
	internal := dockerInternalURLs(ct)
	m.Submenu(locales.DockerOpenInternal(), func(sub *ui.Menu) {
		for _, address := range internal {
			if sub.Item(address).Chosen() {
				a.desktopOpen(s, appBrowser, desktopBrowserVisit{url: address})
			}
		}
	}).Disabled(len(internal) == 0 || !ct.Running)
	m.Separator()
	for _, action := range []dockerAction{dockerRename, dockerPolicy, dockerRecreate, dockerRemove, dockerForceRemove} {
		a.dockerActionItem(m, target, action)
	}
	a.ctrMoreMenu(m, target, ct)
}

func (a *App) dockerProjectMenu(m *ui.Menu, s *Session, name string) {
	target := dockerTarget{session: s, client: s.client, name: name, project: name}
	for _, action := range []dockerAction{dockerStart, dockerStop, dockerRestart} {
		a.dockerActionItem(m, target, action)
	}
	m.Separator()
	project, _ := target.projectInfo()
	if m.Item(locales.DockerLogs()).Disabled(!target.available() || !composeMetadata(project)).Chosen() {
		a.openDockerLogs(target)
	}
	a.dockerActionItem(m, target, dockerRecreate)
	a.dockerActionItem(m, target, dockerDown)
}

func (a *App) dockerActionItem(m *ui.Menu, target dockerTarget, action dockerAction) {
	allowed := target.allow(action)
	reason := ""
	if !allowed {
		reason = locales.DockerUnavailable()
		if target.available() && target.session.overview.docker.work == nil {
			if target.id != "" {
				reason = ctrActionReason(action, dockerContainer(target.session, target.id))
			} else {
				reason = locales.DockerCtrStateReason()
			}
		}
	}
	if !m.Item(action.title(target.id == "")).Disabled(!allowed).Subtitle(reason).Chosen() {
		return
	}
	dialog := &dockerActionDialog{action: action, target: target}
	switch action {
	case dockerRename:
		dialog.value = target.name
	case dockerPolicy:
		ct := dockerContainer(target.session, target.id)
		dialog.value = ct.RestartPolicy
		if !slices.Contains(monitor.RestartPolicies, dialog.value) {
			dialog.value = "no"
		}
		dialog.retries = strconv.Itoa(ct.MaxRetries)
	}
	a.ctrDlg = &containerDialog{s: target.session, action: dialog}
}

func dockerPortURLs(host, resolved string, ports []monitor.PortBinding) []string {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if host == "" || strings.ContainsAny(host, " \t\r\n/?#@\\[]") || strings.Contains(host, ":") && net.ParseIP(strings.SplitN(host, "%", 2)[0]) == nil {
		return nil
	}
	var urls []string
	for _, port := range ports {
		if port.Proto != "tcp" || port.HostPort < 1 || port.HostPort > 65535 {
			continue
		}
		bound := net.ParseIP(port.HostIP)
		any := bound != nil && bound.IsUnspecified()
		same := strings.EqualFold(port.HostIP, host) || bound != nil && (bound.Equal(net.ParseIP(host)) || bound.Equal(net.ParseIP(resolved)))
		if !any && !same && !port.AlsoIPv6 {
			continue
		}
		address := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port.HostPort))}).String()
		if !slices.Contains(urls, address) {
			urls = append(urls, address)
		}
	}
	return urls
}

// dockerInternalURLs are the addresses of ct's TCP ports as seen from the
// server, for the desktop browser whose connections leave from there:
// each published port on the server, then each port of the container at
// its address on each of its networks.
func dockerInternalURLs(ct *monitor.Container) []string {
	var urls []string
	add := func(host string, port int) {
		if port < 1 || port > 65535 {
			return
		}
		address := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/"}).String()
		if !slices.Contains(urls, address) {
			urls = append(urls, address)
		}
	}
	var ports []int
	for _, port := range ct.Ports {
		if port.Proto != "tcp" {
			continue
		}
		ports = append(ports, port.Port)
		if port.HostPort > 0 {
			host := "127.0.0.1"
			if ip := net.ParseIP(port.HostIP); ip != nil && !ip.IsUnspecified() {
				host = ip.String()
			}
			add(host, port.HostPort)
		}
	}
	for _, exposed := range ct.Exposed {
		number, proto, _ := strings.Cut(exposed, "/")
		if port, err := strconv.Atoi(number); err == nil && (proto == "" || proto == "tcp") {
			ports = append(ports, port)
		}
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	if ct.NetworkMode == "host" {
		for _, port := range ports {
			add("127.0.0.1", port)
		}
		return urls
	}
	for _, network := range ct.Networks {
		if ip := net.ParseIP(network.IP); ip != nil {
			for _, port := range ports {
				add(ip.String(), port)
			}
		}
	}
	return urls
}

func (a *App) openDockerTerminal(target dockerTarget, tool monitor.DockerCmd) {
	s := target.session
	ct := dockerContainer(s, target.id)
	if !target.available() || ct == nil || ct.Status != "running" || ct.Paused || !canSplit(s) || s.client == nil {
		a.notify(locales.DockerUnavailable())
		return
	}
	line, err := tool.ExecShell(target.id)
	if err != nil {
		a.notify(err.Error())
		return
	}
	if tool.Root {
		line = a.sessionPrivilege(s).runner.TerminalCommand(line)
	}
	at, pane := s.pane(), newPane()
	spec := a.shellSpec(s.Host)
	go func() {
		term, err := a.openPaneShell(s, pane, target.client, spec)
		a.post(func() {
			a.paneOpened(s, at, pane, splitRight, target.client, spec, term, err)
			if pane.term == nil {
				return
			}
			a.active = slices.Index(a.sessions, s)
			pane.term.Send([]byte(line + "\r"))
		})
	}()
}

type dockerWork struct {
	target dockerTarget
	action dockerAction
	done   bool
	output string
	err    error
	scroll ui.ScrollState
	ids    []string
	label  string
	cancel context.CancelFunc
}

func (work *dockerWork) busyText() string {
	if work.done {
		return locales.DockerUpdatingList()
	}
	if work.label != "" {
		return locales.DockerActionRunning(work.label)
	}
	if work.action == dockerRestart {
		return locales.DockerActionRestarting()
	}
	return locales.DockerActionRunning(work.action.title(work.target.id == ""))
}

func (a *App) runDockerAction(dialog *dockerActionDialog) {
	target := dialog.target
	s := target.session
	if !slices.Contains(a.sessions, s) || !target.allow(dialog.action) {
		dialog.validation = locales.DockerUnavailable()
		return
	}
	state := &s.overview.docker
	cmd := monitor.CmdOf(&state.d)
	script, err := dialog.script(cmd)
	if err != nil {
		dialog.validation = err.Error()
		return
	}
	work := &dockerWork{target: target, action: dialog.action}
	state.work, dialog.work = work, work
	if dialog.action != dockerRecreate && a.ctrDlg != nil && a.ctrDlg.action == dialog {
		a.ctrDlg = nil
	}
	run := a.dockerExec(s, monitor.RunDockerProgress, nil, false).command
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	timeout := dockerTimeout
	if dialog.action == dockerRecreate {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	if dialog.action == dockerRecreate && target.project != "" {
		work.cancel = cancel
	}
	go func() {
		defer cancel()
		var report func(string)
		if dialog.action == dockerRecreate {
			report = a.dockerProgress(work)
		}
		output, err := run(ctx, target.client, script, report)
		a.post(func() {
			if state.work != work || s.client != target.client || !slices.Contains(a.sessions, s) || s.status != statusConnected {
				if state.work == work {
					state.work = nil
				}
				return
			}
			work.done, work.output, work.err, work.cancel = true, output, err, nil
			if err != nil && (a.ctrDlg == nil || a.ctrDlg.action != dialog) {
				state.actionError = dialog
			}
			a.readDockerOf(s)
		})
	}()
}

func (a *App) dockerProgress(work *dockerWork) func(string) {
	var lock sync.Mutex
	var pending strings.Builder
	queued := false
	return func(chunk string) {
		lock.Lock()
		pending.WriteString(chunk)
		if queued {
			lock.Unlock()
			return
		}
		queued = true
		lock.Unlock()
		a.post(func() {
			lock.Lock()
			chunk := pending.String()
			pending.Reset()
			queued = false
			lock.Unlock()
			if !work.done {
				if work.scroll.Y >= work.scroll.MaxY {
					work.scroll.Y = math.MaxFloat32
				}
				work.output += chunk
				const limit = 64 << 10
				if len(work.output) > limit {
					work.output = work.output[len(work.output)-limit:]
				}
			}
		})
	}
}
