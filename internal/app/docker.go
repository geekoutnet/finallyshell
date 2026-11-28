package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// The Overview tab's Docker cards: when the server has Docker (or
// Podman), under the other cards a strip of the engine and its disk use,
// and below it, across the width, the containers, grouped by Compose
// project, which a filter and the headers sort. A click on a container
// opens its details (docker_dialog.go). It is one read, as the system
// info is: when the tab first shows the connection, on Refresh, and when
// the tab shows again after dockerStale; docker stats is too heavy to
// poll.

// dockerTimeout is how long the Docker read waits for the server.
const dockerTimeout = 45 * time.Second

// dockerStale is how old the Docker data may be when the tab shows
// again before it is read again.
const dockerStale = 30 * time.Second

// dockerReader reads a server's Docker over client.
type dockerReader func(ctx context.Context, client *ssh.Client) (monitor.Docker, error)

// readDocker reads the Docker cards, unless App.readDocker stands in for
// it, as in tests.
func readDocker(ctx context.Context, client *ssh.Client) (monitor.Docker, error) {
	if client == nil {
		return monitor.Docker{}, errNoClient
	}
	return monitor.ReadDocker(ctx, client)
}

// The columns the containers sort by.
const (
	ctrSortName = iota
	ctrSortStatus
	ctrSortCPU
	ctrSortMemory
)

// dockerState is a session's Docker cards.
type dockerState struct {
	d          monitor.Docker
	read       bool      // d holds what a read found
	at         time.Time // when
	client     *ssh.Client
	dataClient *ssh.Client
	loading    bool
	authing    bool
	autoRoot   bool
	connection context.Context
	err        error
	gen        int // counts the reads; a result of an older one is dropped
	cancel     context.CancelFunc
	frame      uint64 // the last frame of the main window that showed the cards

	ctrList
	run         dockerRunner
	work        *dockerWork
	actionError *dockerActionDialog
}

type ctrList struct {
	filter      string
	sortBy      int
	desc, flat  bool
	hideStopped bool
	closed      map[string]bool
	// widths are the widths the user dragged the columns to.
	widths widgets.ColumnWidths
}

// columns are the columns that fit in width, at the widths the user gave
// them.
func (l *ctrList) columns(width float32) ctrColumns {
	if l.widths == nil {
		l.widths = widgets.ColumnWidths{}
	}
	cols := ctrColumnsFor(width)
	cols.w = l.widths
	return cols
}

// shown tells whether the server has Docker, so that the cards show.
func (ds *dockerState) shown() bool { return ds.d.Tool != "" }

// readDockerOf reads s's Docker on a goroutine, dropping a read under
// way; what was read before stays until the new one is done, and when it
// fails.
func (a *App) readDockerOf(s *Session) {
	a.readDockerAccess(s, false)
}

func (a *App) readDockerAccess(s *Session, authorize bool) {
	ds := &s.overview.docker
	same := ds.client == s.client && ds.connection == s.ctx
	if same && ds.authing {
		return
	}
	if ds.cancel != nil {
		ds.cancel()
	}
	if !same {
		ds.read, ds.autoRoot, ds.d, ds.at, ds.err = false, true, monitor.Docker{}, time.Time{}, nil
		ds.work, ds.actionError = nil, nil
	}
	ds.gen++
	gen, client, connection := ds.gen, s.client, s.ctx
	ds.loading, ds.authing, ds.client, ds.connection = true, authorize, client, connection
	read := a.readDocker
	if read == nil {
		read = readDocker
	}
	last := ds.d
	auto := ds.autoRoot || !ds.read && ds.err == nil
	privilege := s.overview.memory.privilege
	if authorize || last.Root {
		privilege = a.sessionPrivilege(s)
	} else if privilege != nil && (privilege.client != client || privilege.life.Err() != nil) {
		privilege = nil
	}
	budget := dockerTimeout
	if authorize {
		budget = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(resourceContext(s), budget)
	ds.cancel = cancel
	go func() {
		d := last
		var err error
		if !authorize && !last.Root {
			d, err = read(ctx, client)
		}
		if err == nil && privilege != nil && (authorize || last.Root || d.State == monitor.DockerNeedsRoot && auto && privilege.runner.CachedMethod() != 0) {
			run := privilege.runner.RunCached
			if authorize {
				run = privilege.runner.Run
			}
			root, rootErr := monitor.ReadDockerRoot(ctx, d.Tool, func(ctx context.Context, script string) (string, string, error) {
				return run(ctx, script, monitor.PrivilegeOptions{ScriptStdin: true})
			})
			switch {
			case rootErr == nil:
				d = root
			case dockerPrivilegeFailed(rootErr):
				d.Root, d.State = false, monitor.DockerDenied
				auto = false
			case errors.Is(rootErr, monitor.ErrPrivilegeCancelled), errors.Is(rootErr, monitor.ErrPrivilegeNotCached):
				d.Root, d.State = false, monitor.DockerNeedsRoot
				auto = errors.Is(rootErr, monitor.ErrPrivilegeCancelled) && privilege.runner.CachedMethod() == 0
			default:
				err = rootErr
			}
		} else if err == nil && d.State == monitor.DockerNeedsRoot && last.State == monitor.DockerDenied {
			d.State = monitor.DockerDenied
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = context.DeadlineExceeded
		}
		cancel()
		a.post(func() {
			if ds.gen != gen || s.client != client || s.ctx != connection {
				return
			}
			ds.loading, ds.authing, ds.cancel, ds.err, ds.autoRoot = false, false, nil, err, auto
			if ds.work != nil && ds.work.done {
				ds.work = nil
			}
			switch {
			case err == nil:
				ds.d, ds.read, ds.at, ds.dataClient = d, true, time.Now(), client
			case !ds.read:
				ds.d = d // what the output had: whether Docker is there
			}
		})
	}()
}

// keepDockerRead reads s's Docker when the tab first shows the
// connection, and again when it shows after a while hidden and the data
// is older than dockerStale.
func (a *App) keepDockerRead(s *Session) {
	ds := &s.overview.docker
	again := ds.frame+1 < a.frame
	ds.frame = a.frame
	if ds.loading && ds.client == s.client && ds.connection == s.ctx {
		return
	}
	priv := s.overview.memory.privilege
	reuse := ds.d.State == monitor.DockerNeedsRoot && ds.autoRoot && ds.err == nil && priv != nil && priv.client == s.client && priv.life.Err() == nil && priv.runner.CachedMethod() != 0
	if ds.client != s.client || ds.connection != s.ctx || !ds.read && ds.err == nil || again && time.Since(ds.at) > dockerStale || reuse {
		a.readDockerOf(s)
	}
}

// dockerName is the engine's name, as the cards title it.
func dockerName(d *monitor.Docker) string {
	if d.Tool == "podman" {
		return "Podman"
	}
	return locales.DockerTitle()
}

// dockerCard is the engine, across the width of the page, width DIPs:
// tiles of its version, its containers, images, volumes, networks and
// disk use, then how it is set up in a line, its warnings in a badge; or
// why it could not be read.
func (a *App) dockerCard(c *ui.Context, s *Session, width float32) {
	ds := &s.overview.docker
	d := &ds.d
	ok := ds.read && d.State == monitor.DockerOK
	overviewCard(c, "container", dockerName(d), func() {
		if ok && len(d.Warnings) > 0 {
			dockerWarningsBadge(c, d.Warnings)
		}
		if ok && widgets.IconButton(c, "arrow-up-right", locales.DockerPageManage()).Clicked() {
			a.openDockerPage(s, dockerSecContainers)
		}
		if ds.loading {
			ui.Box(c).Size(24, 24).Center().Children(func() { ui.Spinner(c).Label(locales.DockerReading()) })
		} else if widgets.IconButton(c, "refresh-cw", locales.DockerRefresh()).Clicked() {
			a.readDockerOf(s)
		}
	}, func() {
		if ds.err != nil && d.State != monitor.DockerDenied {
			ui.Text(c, locales.DockerFailed(describeSysInfoError(ds.err))).FontSize(12).TextColor(theme.Danger)
		}
		if !ds.read && ds.err != nil {
			return
		}
		switch d.State {
		case monitor.DockerNeedsRoot:
			ui.Text(c, locales.DockerPrivNeedsRoot(dockerName(d))).Bold()
			widgets.Muted(c, locales.DockerPrivHint()).FontSize(12)
			a.dockerPrivilegeButton(c, s)
			return
		case monitor.DockerDenied:
			ui.Text(c, dockerDeniedText(d))
			dockerPrivilegeHelp(c, s)
			a.dockerPrivilegeButton(c, s)
			return
		case monitor.DockerDown:
			dockerProblem(c, locales.DockerDown(), d.Error)
			return
		case monitor.DockerFailed:
			dockerProblem(c, locales.DockerFailed(d.Error), "")
			return
		}
		cols := 6
		switch inner := width - 24; {
		case inner < 365:
			cols = 2
		case inner < 860:
			cols = 3
		}
		ui.Grid(c).Columns(cols).Gap(8).FillWidth().Children(func() {
			dockerTiles(c, d)
		})
		ui.Row(c).FillWidth().Wrap().GapX(14).GapY(2).Children(func() {
			for i, r := range dockerSummary(d) {
				dockerSetting(c.Key(i), r.label, r.value)
			}
		})
	})
}

// dockerTiles are the summary card's tiles: the version, the containers
// by state, the images, volumes and networks with what their disk holds,
// and the disk use in all, which a click copies line by line.
func dockerTiles(c *ui.Context, d *monitor.Docker) {
	disk := map[string]string{}
	var size, reclaim uint64
	var lines []string
	for _, u := range d.Disk {
		disk[u.Type] = u.Size
		size += monitor.ParseSize(u.Size)
		reclaimable, _, _ := strings.Cut(u.Reclaimable, "(")
		reclaim += monitor.ParseSize(reclaimable)
		lines = append(lines, diskTypeLabel(u.Type)+": "+locales.DockerDiskLine(u.Size, orDash(u.Reclaimable)))
	}
	text := func(s string) func() {
		if s == "" {
			return nil
		}
		return func() { widgets.Muted(c, s).FontSize(12).MaxLines(2).Tooltip(s) }
	}

	var about []string
	if d.APIVersion != "" && d.APIVersion != d.Version {
		about = append(about, locales.DockerAPI(d.APIVersion))
	}
	if d.Root {
		about = append(about, locales.DockerPrivViaRoot())
	} else if d.Sudo {
		about = append(about, locales.DockerViaSudo())
	}
	statTile(c.Key("version"), locales.DockerVersion(), orDash(d.Version), text(strings.Join(about, ", "))).
		Tooltip(dockerVersionText(d))

	counts := locales.DockerContainerCounts(d.NumContainers, d.Running, d.Paused, d.Stopped)
	statTile(c.Key("containers"), locales.DockerContainers(), strconv.Itoa(d.NumContainers), func() {
		ui.Row(c).Wrap().GapX(8).GapY(2).Children(func() {
			state := func(color ui.Color, text string) {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					widgets.StatusDot(c, color)
					widgets.Muted(c, text).FontSize(12).SingleLine()
				})
			}
			state(colors.gaugeOK, locales.OverviewRunning(d.Running))
			state(colors.gaugeBusy, locales.OverviewPaused(d.Paused))
			state(theme.TextMuted, locales.OverviewStopped(d.Stopped))
		})
	}).Tooltip(counts).Label(locales.DockerContainers() + ": " + counts)

	statTile(c.Key("images"), locales.DockerImages(), strconv.Itoa(max(d.NumImages, len(d.Images))), text(disk["Images"]))
	statTile(c.Key("volumes"), locales.DockerVolumes(), strconv.Itoa(len(d.Volumes)), text(disk["Local Volumes"]))
	statTile(c.Key("networks"), locales.DockerNetworks(), strconv.Itoa(len(d.Networks)), nil)

	if len(d.Disk) == 0 {
		statTile(c.Key("disk"), locales.DockerDiskUsage(), "-", nil)
		return
	}
	all := strings.Join(lines, "\n")
	tile := statTile(c.Key("disk"), locales.DockerDiskUsage(), dockerSize(size), text(locales.OverviewReclaimable(dockerSize(reclaim)))).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(all).Tooltip(all)
	if tile.Hovered() {
		tile.Background(theme.Text.Alpha(0.08))
	}
	if tile.Clicked() {
		c.WriteClipboard(all)
		c.Toast(locales.CopiedText(locales.DockerDiskUsage()))
	}
}

// statTile is a tile of a number: what it counts, its value large, and
// what sub builds under it.
func statTile(c *ui.Context, label, value string, sub func()) ui.Element {
	return ui.Column(c).MinWidth(0).Gap(2).Padding(8, 10).Radius(6).Background(colors.overviewPage).Children(func() {
		widgets.Muted(c, label).FontSize(12).SingleLine()
		ui.Text(c, value).FontSize(18).Bold().SingleLine().FontFeatures("tnum")
		if sub != nil {
			sub()
		}
	})
}

// dockerSize is n bytes as docker writes a size: at most 4 digits, in
// units of 1000, as 5.202GB.
func dockerSize(n uint64) string {
	f := float64(n)
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	i := 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	return strconv.FormatFloat(f, 'g', 4, 64) + units[i]
}

// dockerWarningsBadge is the engine's warnings as a badge: their number,
// all of them in its tooltip; a click copies them.
func dockerWarningsBadge(c *ui.Context, warnings []string) {
	all := strings.Join(warnings, "\n")
	text := locales.DockerWarnings(len(warnings))
	b := ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(2, 8).Radius(10).Shrink(0).Background(colors.gaugeBusy.Alpha(0.14)).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(text).Tooltip(all)
	if b.Clicked() {
		c.WriteClipboard(all)
		c.Toast(locales.CopiedText(text))
	}
	b.Children(func() {
		ui.Icon(c, icons.Get("triangle-alert")).FontSize(12).TextColor(colors.gaugeBusy)
		ui.Text(c, text).FontSize(12).SingleLine()
	})
}

// dockerSetting is a setting of the engine in the line under the tiles:
// what, and its value, which a click copies.
func dockerSetting(c *ui.Context, label, value string) {
	row := ui.Row(c).Gap(6).Padding(2, 4).Radius(4).AlignItems(ui.Center).Shrink(1).MinWidth(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label + ": " + value).Tooltip(value)
	if row.Hovered() {
		row.Background(theme.Text.Alpha(0.06))
	}
	if row.Clicked() {
		c.WriteClipboard(value)
		c.Toast(locales.CopiedText(label))
	}
	row.Children(func() {
		widgets.Muted(c, label).FontSize(12).SingleLine().Shrink(0)
		ui.Text(c, value).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
	})
}

// dockerProblem says why the engine could not be read, with what it
// said in small letters.
func dockerProblem(c *ui.Context, text, detail string) {
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.Get("triangle-alert")).FontSize(14).TextColor(colors.gaugeBusy).Margin(2, 0, 0, 0)
		ui.Text(c, text).Grow(1).MinWidth(0)
	})
	if detail != "" {
		widgets.Muted(c, detail).FontSize(12).MaxLines(3).Selectable()
	}
}

// labelValue is a line of a card or of the details: what, and its value.
type labelValue struct{ label, value string }

// dockerVersionText is the engine's version, with its API's and how it
// was read: 26.1.4 (API 1.45), read with sudo.
func dockerVersionText(d *monitor.Docker) string {
	version := orDash(d.Version)
	if d.APIVersion != "" && d.APIVersion != d.Version {
		version = locales.DockerVersionAPI(version, d.APIVersion)
	}
	if d.Root {
		version += ", " + locales.DockerPrivViaRoot()
	} else if d.Sudo {
		version += ", " + locales.DockerViaSudo()
	}
	return version
}

// dockerSummary is how the engine is set up, the line under the summary
// card's tiles.
func dockerSummary(d *monitor.Docker) []labelValue {
	join := func(parts ...string) string {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return orDash(strings.Join(kept, ", "))
	}
	cgroup := d.CgroupDriver
	if v := strings.TrimPrefix(d.CgroupVersion, "v"); v != "" {
		cgroup = join(cgroup, "v"+v)
	}
	res := "-"
	if d.CPUs > 0 {
		res = locales.DockerCPUsMem(d.CPUs, formatSize(int64(d.MemTotal)))
	}
	return []labelValue{
		{locales.DockerStorage(), orDash(d.StorageDriver)},
		{locales.DockerCgroup(), orDash(cgroup)},
		{locales.DockerLogging(), orDash(d.LoggingDriver)},
		{locales.DockerRootDir(), orDash(d.RootDir)},
		{locales.DockerResources(), res},
	}
}

// diskTypeLabel is the name of a line of docker system df.
func diskTypeLabel(t string) string {
	switch t {
	case "Images":
		return locales.DockerImages()
	case "Containers":
		return locales.DockerContainers()
	case "Local Volumes":
		return locales.DockerVolumes()
	case "Build Cache":
		return locales.DockerBuildCache()
	}
	return t
}

// copyRow is a line of a label and its value; a click copies the value.
// wrap lets a long value take several lines; else it is cut short, and
// its tooltip has it whole. The value is not selectable text, which
// would take the click.
func copyRow(c *ui.Context, label, value string, labelWidth float32, wrap bool) {
	copyRowAs(c, label, value, value, labelWidth, wrap)
}

// copyRowAs is copyRow showing shown, and copying value.
func copyRowAs(c *ui.Context, label, shown, value string, labelWidth float32, wrap bool) {
	row := ui.Row(c).Gap(10).Padding(2, 4).Radius(4).AlignItems(ui.Start).FillWidth().
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label + ": " + shown)
	if row.Hovered() {
		row.Background(theme.Text.Alpha(0.06))
	}
	if row.Clicked() {
		c.WriteClipboard(value)
		c.Toast(locales.CopiedText(label))
	}
	row.Children(func() {
		widgets.Muted(c, label).Width(labelWidth).Shrink(0).SingleLine().Tooltip(label)
		t := ui.Text(c, shown).Grow(1).MinWidth(0)
		if !wrap {
			t.SingleLine().Tooltip(shown)
		}
	})
}

// ctrColor is the color of a container's dot: green when it runs and is
// not unhealthy, amber for a passing state, red for a failure, gray when
// it stopped as asked.
func ctrColor(ct *monitor.Container) ui.Color {
	switch ct.Status {
	case "running":
		switch ct.Health {
		case "unhealthy":
			return colors.gaugeHigh
		case "starting":
			return colors.gaugeBusy
		}
		return colors.gaugeOK
	case "paused", "restarting", "removing":
		return colors.gaugeBusy
	case "created":
		return theme.TextMuted
	case "exited":
		if ct.ExitCode == 0 && !ct.OOMKilled {
			return theme.TextMuted
		}
	}
	return colors.gaugeHigh
}

// ctrRank orders the states for sorting by status: running first.
func ctrRank(ct *monitor.Container) int {
	switch ct.Status {
	case "running":
		if ct.Health == "unhealthy" {
			return 1
		}
		return 0
	case "restarting":
		return 2
	case "paused":
		return 3
	case "created":
		return 4
	case "exited":
		return 5
	}
	return 6
}

// healthText is a health check's status in words.
func healthText(h string) string {
	switch h {
	case "healthy":
		return locales.DockerHealthy()
	case "unhealthy":
		return locales.DockerUnhealthy()
	case "starting":
		return locales.DockerStarting()
	}
	return h
}

// ctrStatusText is a container's state as docker ps says it: Up 3 days
// (healthy), Exited (137) 5 hours ago.
func ctrStatusText(ct *monitor.Container, now time.Time) string {
	switch ct.Status {
	case "running":
		up := "-"
		if !ct.StartedAt.IsZero() {
			up = humanDuration(now.Sub(ct.StartedAt))
		}
		if ct.Health != "" {
			return locales.DockerUpHealth(up, healthText(ct.Health))
		}
		return locales.DockerUp(up)
	case "exited":
		text := locales.DockerExitedCode(ct.ExitCode)
		if !ct.FinishedAt.IsZero() {
			text = locales.DockerExited(ct.ExitCode, humanDuration(now.Sub(ct.FinishedAt)))
		}
		if ct.OOMKilled {
			text += ", " + locales.DockerOOMKilled()
		}
		return text
	case "paused":
		return locales.DockerPaused()
	case "restarting":
		return locales.DockerRestarting()
	case "created":
		return locales.DockerCreated()
	case "dead":
		return locales.DockerDead()
	case "removing":
		return locales.DockerRemoving()
	}
	return ct.Status
}

// humanDuration is d in its largest unit, as docker writes it: 3 days,
// 5 hours.
func humanDuration(d time.Duration) string {
	d = max(d, 0)
	switch h := d.Hours(); {
	case d < time.Minute:
		return locales.DurSeconds(int(d.Seconds()))
	case d < time.Hour:
		return locales.DurMinutes(int(d.Minutes()))
	case h < 48:
		return locales.DurHours(int(h))
	case h < 24*14:
		return locales.DurDays(int(h / 24))
	case h < 24*60:
		return locales.DurWeeks(int(h / 24 / 7))
	case h < 24*365*2:
		return locales.DurMonths(int(h / 24 / 30))
	default:
		return locales.DurYears(int(h / 24 / 365))
	}
}

// hostAddr is a host address and port: [::1]:5353 for IPv6.
func hostAddr(ip string, port string) string {
	switch {
	case ip == "":
		return port
	case strings.Contains(ip, ":"):
		return "[" + ip + "]:" + port
	}
	return ip + ":" + port
}

// portTexts are a container's published ports as docker ps writes them,
// 0.0.0.0:8080→80/tcp, consecutive ports as a range:
// 0.0.0.0:8000-8002→8000-8002/tcp.
func portTexts(ports []monitor.PortBinding) []string {
	var out []string
	for i := 0; i < len(ports); {
		p := ports[i]
		j := i + 1
		for j < len(ports) {
			q, prev := ports[j], ports[j-1]
			if q.Port != prev.Port+1 || q.HostPort != prev.HostPort+1 || q.HostIP != p.HostIP || q.Proto != p.Proto || q.AlsoIPv6 != p.AlsoIPv6 {
				break
			}
			j++
		}
		last := ports[j-1]
		host, ctr := strconv.Itoa(p.HostPort), strconv.Itoa(p.Port)
		if j-i > 1 {
			host += "-" + strconv.Itoa(last.HostPort)
			ctr += "-" + strconv.Itoa(last.Port)
		}
		out = append(out, hostAddr(p.HostIP, host)+"→"+ctr+"/"+p.Proto)
		i = j
	}
	return out
}

// ctrIPs are a container's addresses, one for each network that gave it
// one; "host" on the host's network.
func ctrIPs(ct *monitor.Container) []string {
	var ips []string
	for _, n := range ct.Networks {
		if n.IP != "" {
			ips = append(ips, n.IP)
		}
		if n.IPv6 != "" {
			ips = append(ips, n.IPv6)
		}
	}
	if len(ips) == 0 && ct.NetworkMode == "host" {
		return []string{"host"}
	}
	return ips
}

// firstAndMore is the first of list, and "+2" for two more: "-" for none.
func firstAndMore(list []string) string {
	switch len(list) {
	case 0:
		return "-"
	case 1:
		return list[0]
	}
	return list[0] + "  +" + strconv.Itoa(len(list)-1)
}

func ctrCPUText(ct *monitor.Container) string {
	if ct.Stats == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", ct.Stats.CPU)
}

// ctrMemText is a container's memory, with its limit when it has one.
func ctrMemText(ct *monitor.Container) string {
	if ct.Stats == nil {
		return "-"
	}
	used := formatBytes(float64(ct.Stats.MemUsed))
	if ct.Memory > 0 {
		return used + " / " + formatBytes(float64(ct.Memory))
	}
	return used
}

// ctrMatches tells whether a container shows for the filter q, in lower
// case: by its name, image, project, service, status, ID, addresses or
// ports.
func ctrMatches(ct *monitor.Container, q string, now time.Time) bool {
	if q == "" {
		return true
	}
	hay := []string{ct.Name, ct.Image, ct.Project, ct.Service, ct.Status, ctrStatusText(ct, now), ct.NetworkMode}
	if len(ct.ID) >= 12 {
		hay = append(hay, ct.ID[:12])
	}
	hay = append(hay, ctrIPs(ct)...)
	hay = append(hay, portTexts(ct.Ports)...)
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// ctrCompare orders two containers by the column sortBy, the busiest
// first for CPU and memory, then by name.
func ctrCompare(sortBy int, desc bool) func(a, b *monitor.Container) int {
	cpu := func(ct *monitor.Container) float64 {
		if ct.Stats == nil {
			return -1
		}
		return ct.Stats.CPU
	}
	mem := func(ct *monitor.Container) int64 {
		if ct.Stats == nil {
			return -1
		}
		return int64(ct.Stats.MemUsed)
	}
	return func(a, b *monitor.Container) int {
		r := 0
		switch sortBy {
		case ctrSortStatus:
			r = cmp.Compare(ctrRank(a), ctrRank(b))
		case ctrSortCPU:
			r = cmp.Compare(cpu(b), cpu(a))
		case ctrSortMemory:
			r = cmp.Compare(mem(b), mem(a))
		}
		if r == 0 {
			r = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		if desc {
			r = -r
		}
		return r
	}
}

// ctrGroup is a Compose project, as the card
// shows it, or with project "" the other containers.
type ctrGroup struct {
	project string
	list    []*monitor.Container
}

// ctrGroups are the containers that show for the filter, sorted: the
// projects, then the others; all in one group
// when flat.
func (ds *dockerState) ctrGroups(now time.Time) (groups []ctrGroup, shown int) {
	return ds.ctrList.groups(ds.d.Containers, now)
}

func (list *ctrList) groups(containers []monitor.Container, now time.Time) (groups []ctrGroup, shown int) {
	q := strings.ToLower(strings.TrimSpace(list.filter))
	byProject := map[string]*ctrGroup{}
	rest := &ctrGroup{}
	for i := range containers {
		ct := &containers[i]
		if list.hideStopped && ct.Status != "running" && ct.Status != "restarting" && ct.Status != "paused" || !ctrMatches(ct, q, now) {
			continue
		}
		shown++
		g := rest
		if !list.flat && ct.Project != "" {
			if g = byProject[ct.Project]; g == nil {
				g = &ctrGroup{project: ct.Project}
				byProject[ct.Project] = g
			}
		}
		g.list = append(g.list, ct)
	}
	order := ctrCompare(list.sortBy, list.desc)
	for _, g := range byProject {
		slices.SortFunc(g.list, order)
		groups = append(groups, *g)
	}
	slices.SortFunc(groups, func(a, b ctrGroup) int {
		if list.sortBy == ctrSortName {
			r := strings.Compare(strings.ToLower(a.project), strings.ToLower(b.project))
			if list.desc {
				r = -r
			}
			return r
		}
		return order(a.list[0], b.list[0])
	})
	slices.SortFunc(rest.list, order)
	if len(rest.list) > 0 {
		groups = append(groups, *rest)
	}
	return groups, shown
}

// ctrColumns are the columns of the containers that fit in width, and the
// widths the user gave them.
type ctrColumns struct {
	image, status, ports, ip bool
	w                        widgets.ColumnWidths
}

func ctrColumnsFor(width float32) ctrColumns {
	return ctrColumns{status: width >= 520, ip: width >= 640, ports: width >= 780, image: width >= 920}
}

// The default widths of the containers' columns besides the name and the
// image, which share what is left; the user drags them wider or narrower.
const (
	ctrDotW    = 22
	ctrStatusW = 168
	ctrPortsW  = 172
	ctrIPW     = 118
	ctrCPUW    = 56
	ctrMemW    = 108
)

// dockerContainers is the card of the containers, across the width.
func (a *App) dockerContainers(c *ui.Context, s *Session, width float32) {
	ds := &s.overview.docker
	d := &ds.d
	now := time.Now()
	groups, shown := ds.ctrGroups(now)
	cols := ds.ctrList.columns(width - 24)
	ui.Column(c).FillWidth().Background(colors.card).Border(1, colors.line).Radius(8).Padding(8, 12, 12, 12).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Wrap().Children(func() {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Grow(1).MinWidth(180).Height(26).Children(func() {
				ui.Icon(c, icons.Get("container")).FontSize(14).TextColor(theme.Accent)
				ui.Text(c, locales.DockerContainers()).Bold().SingleLine().Shrink(0)
				count := locales.DockerCount(len(d.Containers), runningCount(d.Containers))
				if shown != len(d.Containers) {
					count = locales.DockerCountFiltered(shown, len(d.Containers))
				}
				widgets.Muted(c, count).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
			})
			ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
				ui.SearchField(c, &ds.filter).Placeholder(locales.DockerFilter()).Label(locales.DockerFilter()).Width(240).Shrink(1).MinWidth(120)
				if widgets.ToggleIconButton(c, "layout-grid", locales.DockerGroupByProject(), !ds.flat).Clicked() {
					ds.flat = !ds.flat
				}
			})
		})
		if len(d.Containers) == 0 {
			widgets.Muted(c, locales.DockerNoContainers())
			return
		}
		ui.Column(c).FillWidth().Children(func() {
			a.ctrHeader(c, &ds.ctrList, cols, nil)
			if shown == 0 {
				widgets.Muted(c, locales.DockerNoMatch()).Padding(8, 4)
			}
			a.ctrGroupRows(c, s, &ds.ctrList, groups, cols, now, nil)
		})
	})
}

func runningCount(list []monitor.Container) int {
	n := 0
	for i := range list {
		if list[i].Status == "running" {
			n++
		}
	}
	return n
}

// ctrHeader is the containers' column headers; a click on Name, Status,
// CPU or Memory sorts by it, a second the other way. Dragging the end edge
// of a header resizes its column; the name's, which takes the room the
// others leave, moves its border with the column after it, which the drag
// resizes the other way.
func (a *App) ctrHeader(c *ui.Context, ds *ctrList, cols ctrColumns, leading func()) {
	title := func(text string, by int, end bool) ui.Element {
		label := text
		if by >= 0 && ds.sortBy == by {
			// Names and states sort up, CPU and memory the busiest first.
			if up := by == ctrSortName || by == ctrSortStatus; up != ds.desc {
				label += " ↑"
			} else {
				label += " ↓"
			}
		}
		t := widgets.Muted(c, label).FontSize(12).SingleLine().Grow(1).MinWidth(0)
		if by >= 0 {
			t.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(locales.DockerSortBy(text))
			if ds.sortBy == by {
				t.TextColor(theme.Text)
			}
			if t.Clicked() {
				if ds.sortBy == by {
					ds.desc = !ds.desc
				} else {
					ds.sortBy, ds.desc = by, false
				}
			}
		}
		if end {
			t.TextAlign(ui.End)
		}
		return t
	}
	// The drag of the name's grip, which the column after the name, the
	// first head, takes the other way: what the name gains it loses, the
	// name keeping at least ctrLeastW.
	var dx float32
	var reset, first = false, true
	// head is the header of a column the user resizes; by is -1 for one
	// that does not sort.
	head := func(id, text string, by int, def float32, end bool) {
		h := widgets.ColumnHead(c, cols.w, id, def, ctrLeastW, func() { title(text, by, end) })
		if first {
			first = false
			if reset {
				delete(cols.w, id)
			} else {
				cols.w.Resize(id, h, def, ctrLeastW, -dx)
			}
		}
	}
	ui.Row(c).Gap(10).Padding(2, 6, 4, 6).AlignItems(ui.Center).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
		if leading != nil {
			leading()
		}
		ui.Box(c).Width(ctrDotW).Shrink(0)
		var name ui.Element
		name, dx, reset = widgets.FlexHead(c, "name", 2, func() { title(locales.DockerColName(), ctrSortName, false) })
		if dx != 0 {
			dx = max(dx, ctrLeastW-name.Bounds().W)
		}
		if cols.image {
			head("image", locales.DockerColImage(), -1, 0, false)
		}
		if cols.status {
			head("status", locales.DockerColStatus(), ctrSortStatus, ctrStatusW, false)
		}
		if cols.ports {
			head("ports", locales.DockerColPorts(), -1, ctrPortsW, false)
		}
		if cols.ip {
			head("ip", locales.DockerColIP(), -1, ctrIPW, false)
		}
		head("cpu", locales.Cpu(), ctrSortCPU, ctrCPUW, true)
		head("mem", locales.DockerColMemory(), ctrSortMemory, ctrMemW, true)
		ui.Box(c).Width(ctrMenuW).Shrink(0)
	})
}

// ctrLeastW is the narrowest a column of the containers is dragged to.
const ctrLeastW = 40

func (a *App) ctrGroupRows(c *ui.Context, s *Session, list *ctrList, groups []ctrGroup, cols ctrColumns, now time.Time, page *dockerContainersPage) {
	for _, group := range groups {
		ui.Column(c.Key("project:" + group.project)).FillWidth().Children(func() {
			a.ctrGroupView(c, s, list, group, len(groups) > 1, cols, now, page)
		})
	}
}

// ctrGroupView is a group of containers: a project's, under its header,
// which a click opens and closes, or the others, under a header of their
// own when there are projects above them.
func (a *App) ctrGroupView(c *ui.Context, s *Session, list *ctrList, g ctrGroup, headed bool, cols ctrColumns, now time.Time, page *dockerContainersPage) {
	ds := &s.overview.docker
	open := g.project == "" || !list.closed[g.project]
	if g.project != "" {
		title := g.project
		row := ui.Row(c).Gap(10).Padding(5, 6).Margin(4, 0, 0, 0).Radius(4).AlignItems(ui.Center).FillWidth().
			Cursor(ui.CursorPointer).Role(ui.RoleGroup).Label(title)
		menu := func(m *ui.Menu) { a.dockerProjectMenu(m, s, g.project) }
		row.ContextMenu(menu)
		if row.Hovered() {
			row.Background(theme.Text.Alpha(0.06))
		}
		row.Children(func() {
			content := ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).MinWidth(0).Cursor(ui.CursorPointer).
				Role(ui.RoleButton).Label(title).Expanded(open)
			if content.Clicked() {
				if list.closed == nil {
					list.closed = map[string]bool{}
				}
				list.closed[g.project] = open
				open = !open
			}
			content.Children(func() {
				icon := "chevron-right"
				if open {
					icon = "chevron-down"
				}
				ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(theme.TextMuted)
				ui.Icon(c, icons.Get("layout-grid")).FontSize(12).TextColor(theme.Accent)
				ui.Text(c, title).Bold().SingleLine().Shrink(1).MinWidth(0).Tooltip(locales.DockerProject() + ": " + title)
				running := 0
				var cpu float64
				var mem uint64
				for _, ct := range g.list {
					if ct.Status == "running" {
						running++
					}
					if ct.Stats != nil {
						cpu += ct.Stats.CPU
						mem += ct.Stats.MemUsed
					}
				}
				count := locales.DockerCount(len(g.list), running)
				if work := ds.work; work != nil && work.target.project == g.project && work.target.id == "" {
					count = work.busyText()
				}
				widgets.Muted(c, count).FontSize(12).SingleLine().Grow(1).MinWidth(0).Tooltip(count)
				widgets.Muted(c, fmt.Sprintf("%.1f%%", cpu)).FontSize(12).Width(cols.w.Width("cpu", ctrCPUW)).Shrink(0).TextAlign(ui.End).FontFeatures("tnum")
				widgets.Muted(c, formatBytes(float64(mem))).FontSize(12).Width(cols.w.Width("mem", ctrMemW)).Shrink(0).TextAlign(ui.End).FontFeatures("tnum")
			})
			dockerMoreButton(c, title, menu)
		})
	} else if headed {
		widgets.Muted(c, locales.DockerOther()).FontSize(12).Bold().Padding(8, 6, 2, 6)
	}
	if !open {
		return
	}
	for _, ct := range g.list {
		a.ctrRow(c.Key(ct.ID), s, ct, g.project != "", cols, now, page)
	}
}

// ctrRow is a container's line; a click opens its details.
func (a *App) ctrRow(c *ui.Context, s *Session, ct *monitor.Container, indent bool, cols ctrColumns, now time.Time, page *dockerContainersPage) {
	row := ui.Row(c).Gap(10).Padding(4, 6).Radius(4).AlignItems(ui.Center).FillWidth().MinHeight(26).
		Cursor(ui.CursorPointer).Role(ui.RoleGroup).Label(ct.Name)
	menu := func(m *ui.Menu) { a.dockerContainerMenu(c, m, s, ct) }
	row.ContextMenu(menu)
	if row.Hovered() {
		row.Background(theme.Text.Alpha(0.06))
	}
	if page != nil && page.selected[ct.ID] {
		row.Background(theme.Accent.Alpha(0.16))
	}
	status := ctrStatusText(ct, now)
	work := s.overview.docker.work
	busy := work != nil && (work.target.id == ct.ID || work.target.id == "" && work.target.project != "" && work.target.project == ct.Project || slices.Contains(work.ids, ct.ID))
	if busy {
		status = work.busyText()
	}
	row.Children(func() {
		if page != nil {
			page.checkbox(c, ct)
		}
		content := ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).MinWidth(0).Cursor(ui.CursorPointer).
			Role(ui.RoleButton).Label(ct.Name)
		if page == nil && content.Clicked() || page != nil && content.DoubleClicked() {
			a.openContainer(s, ct.ID)
		} else if page != nil && content.Clicked() {
			page.click(ct.ID, content.ClickModifiers())
			c.Invalidate()
		}
		content.Children(func() { ctrRowCells(c, ct, indent, cols, status, busy) })
		dockerMoreButton(c, ct.Name, menu)
	})
}

func ctrRowCells(c *ui.Context, ct *monitor.Container, indent bool, cols ctrColumns, status string, busy bool) {
	// The names line up, a project's containers' dots indented.
	ui.Row(c).Width(ctrDotW).Shrink(0).Children(func() {
		dot := widgets.StatusDot(c, ctrColor(ct)).Tooltip(status)
		if indent {
			dot.Margin(0, 0, 0, 14)
		}
	})
	name := ct.Name
	tip := ct.Name
	if ct.Service != "" && ct.Project != "" {
		tip += "\n" + ct.Project + " / " + ct.Service
	}
	if !cols.status {
		tip += "\n" + status
		if busy {
			name += " · " + status
		}
	}
	ui.Text(c, name).SingleLine().Grow(2).Basis(0).MinWidth(0).Tooltip(tip)
	if cols.image {
		cols.w.Size(widgets.Muted(c, ct.Image).SingleLine(), "image", 0).Tooltip(ct.Image)
	}
	if cols.status {
		t := ui.Text(c, status).FontSize(12).SingleLine().Width(cols.w.Width("status", ctrStatusW)).Shrink(0).Tooltip(status)
		if ct.Status != "running" || ct.Health == "unhealthy" {
			t.TextColor(ctrColor(ct))
		}
	}
	if cols.ports {
		ports := portTexts(ct.Ports)
		widgets.Muted(c, firstAndMore(ports)).FontSize(12).SingleLine().Width(cols.w.Width("ports", ctrPortsW)).Shrink(0).Tooltip(strings.Join(ports, "\n"))
	}
	if cols.ip {
		ips := ctrIPs(ct)
		widgets.Muted(c, firstAndMore(ips)).FontSize(12).SingleLine().Width(cols.w.Width("ip", ctrIPW)).Shrink(0).Tooltip(strings.Join(ips, "\n"))
	}
	ui.Text(c, ctrCPUText(ct)).FontSize(12).Width(cols.w.Width("cpu", ctrCPUW)).Shrink(0).TextAlign(ui.End).SingleLine().FontFeatures("tnum")
	ui.Text(c, ctrMemText(ct)).FontSize(12).Width(cols.w.Width("mem", ctrMemW)).Shrink(0).TextAlign(ui.End).SingleLine().FontFeatures("tnum")
}
