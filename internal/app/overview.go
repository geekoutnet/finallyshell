package app

import (
	"context"
	"fmt"
	"strings"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// The Overview tab of the bottom panel is a server's dashboard: a header
// with what the server is, four ring gauges (load, CPU, memory, the disk
// of /), then rows of cards as high as each other, as many to a row as
// the width takes: network, disks and system; the Docker summary; then
// IP addresses, top processes and the containers (docker.go). The live
// numbers are the monitor's (Session.stats); what the server is comes
// from one read of its system info, as the System info popover does
// (sysinfo.go), made when the tab first shows a connection and again on
// Refresh.

// overviewState is a session's Overview tab.
type overviewState struct {
	info    monitor.SysInfo
	read    bool        // info holds what a read found
	client  *ssh.Client // the connection info, or err, is from
	loading bool
	err     error
	gen     int // counts the reads; a result of an older one is dropped
	cancel  context.CancelFunc
	// allAddrs shows the addresses of virtual interfaces too.
	allAddrs     bool
	memory       memoryState
	memoryRunner monitor.MemoryRunner
	// docker is the Docker cards (docker.go).
	docker dockerState
}

// readOverview reads s's system info for the tab, dropping a read under
// way; what was read before stays until the new one is done.
func (a *App) readOverview(s *Session) {
	ov := &s.overview
	if ov.cancel != nil {
		ov.cancel()
	}
	ov.gen++
	gen, client := ov.gen, s.client
	ov.loading, ov.err, ov.client = true, nil, client
	ov.cancel = a.startSysInfoRead(s, func(info monitor.SysInfo, err error) {
		if ov.gen != gen {
			return
		}
		ov.loading, ov.cancel = false, nil
		if err != nil {
			ov.err = err
			return
		}
		ov.info, ov.read = info, true
	})
}

// virtualIface tells whether the interface name is no network of the
// server's own: the loopback, and what containers, bridges, tunnels and
// overlay networks make. Their addresses show only when asked.
func virtualIface(name string) bool {
	if name == "lo" || strings.HasPrefix(name, "lo:") {
		return true
	}
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "vxlan", "tun", "tap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// splitAddrs splits addrs into those of real interfaces and those of
// virtual ones (virtualIface), in their order. A server with no real
// one has them all in real, so that the list is never empty.
func splitAddrs(addrs []monitor.Addr) (real, virtual []monitor.Addr) {
	for _, ad := range addrs {
		if virtualIface(ad.Iface) {
			virtual = append(virtual, ad)
		} else {
			real = append(real, ad)
		}
	}
	if len(real) == 0 {
		return virtual, nil
	}
	return real, virtual
}

// addrIP is an address without its prefix length: 10.0.0.5 of
// 10.0.0.5/24.
func addrIP(addr string) string {
	ip, _, _ := strings.Cut(addr, "/")
	return ip
}

// overviewPanel is the Overview tab: the dashboard of the session the
// selected row shows, or why there is none.
func (a *App) overviewPanel(c *ui.Context) {
	s := a.connSession()
	ui.Column(c.Key("overview")).Grow(1).MinHeight(0).FillWidth().Background(colors.overviewPage).Children(func() {
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
			overviewMessage(c, "gauge", locales.SysInfoOff(), nil)
		default:
			a.overviewPage(c, s)
		}
	})
}

// overviewMessage is a state of the tab with no dashboard: an icon and a
// line in the middle, and what more builds under them.
func overviewMessage(c *ui.Context, icon, text string, more func()) {
	ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Padding(16).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(28).TextColor(theme.TextMuted)
		widgets.Muted(c, text).TextAlign(ui.Center).MaxWidth(520)
		if more != nil {
			more()
		}
	})
}

// overviewPage is the dashboard of s, a connected server whose host
// allows the monitor, once there is something to show.
func (a *App) overviewPage(c *ui.Context, s *Session) {
	ov := &s.overview
	if !ov.loading && (!ov.read && ov.err == nil || ov.client != s.client) {
		a.readOverview(s) // first shown, or connected again since
	}
	a.keepDockerRead(s)
	st := &s.stats
	switch {
	case ov.read && ov.info.KernelOS != "" && ov.info.KernelOS != "Linux":
		overviewMessage(c, "server", locales.ProcOnlyLinux(), nil)
		return
	case !ov.read && !st.OK && ov.err != nil:
		overviewMessage(c, "triangle-alert", locales.SysInfoFailed(describeSysInfoError(ov.err)), func() {
			if ui.Button(c, locales.Retry()).Clicked() {
				a.readOverview(s)
			}
		})
		return
	case !ov.read && !st.OK:
		ui.Column(c).Grow(1).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
			ui.Spinner(c).Label(locales.ReadingServerInfo())
			widgets.Muted(c, locales.ReadingServerInfo())
		})
		return
	}
	scroll := ui.Scroll(c).Grow(1).MinHeight(0).FillWidth()
	width, height := scroll.Bounds().W, scroll.Bounds().H
	scroll.Children(func() {
		const pad = overviewGap
		ui.Column(c).FillWidth().Padding(pad).Gap(pad).Children(func() {
			a.overviewHeader(c, s)
			overviewGauges(c, s, width-2*pad, height > 0 && height < 380)
			if s.client != nil || !st.Memory.SampledAt.IsZero() {
				a.memoryCard(c, s, width-2*pad)
			}
			a.overviewCards(c, s, width-2*pad)
			if ov.docker.shown() && ov.docker.d.State == monitor.DockerOK && (ov.docker.read || len(ov.docker.d.Containers) > 0) {
				a.dockerContainers(c, s, width-2*pad)
			}
		})
	})
	a.memoryDialog(c, s)
}

// overviewHeader is what the server is: its name, system, kernel and
// architecture, how long it has been up and its latency, and Refresh.
func (a *App) overviewHeader(c *ui.Context, s *Session) {
	ov, st := &s.overview, &s.stats
	info := ov.info
	name := info.Hostname
	if name == "" {
		name = s.Host.Title()
	}
	var about []string
	for _, v := range []string{info.OS, info.Kernel, info.Arch} {
		if v != "" {
			about = append(about, v)
		}
	}
	if len(about) == 0 {
		about = append(about, s.target())
	}
	uptime := info.Uptime
	if st.OK {
		uptime = st.Uptime
	}
	ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Grow(1).MinWidth(220).Basis(220).Children(func() {
			ui.Box(c).Size(36, 36).Radius(8).Center().Shrink(0).Background(theme.Accent.Alpha(0.14)).Children(func() {
				ui.Icon(c, icons.Get("server")).FontSize(18).TextColor(theme.Accent)
			})
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, name).FontSize(16).Bold().SingleLine().Selectable()
				widgets.Muted(c, strings.Join(about, "  ·  ")).FontSize(12).SingleLine().Tooltip(strings.Join(about, "  ·  "))
			})
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			overviewChip(c, "rotate-cw", locales.SysUptime(), uptimeText(uptime))
			latency := "-"
			if st.OK {
				latency = formatLatency(st.Latency)
			}
			overviewChip(c, "activity", locales.Latency(), latency)
			if ov.loading {
				ui.Box(c).Size(24, 24).Center().Children(func() { ui.Spinner(c).Label(locales.ReadingServerInfo()) })
			} else if widgets.IconButton(c, "refresh-cw", locales.Refresh()).Clicked() {
				a.readOverview(s)
				if !ov.docker.loading {
					a.readDockerOf(s)
				}
			}
		})
	})
	if ov.err != nil {
		msg := locales.SysInfoFailed(describeSysInfoError(ov.err))
		ui.Text(c, msg).FontSize(12).TextColor(theme.Danger)
	}
}

// overviewChip is a small label and value in a pill, as the header's
// uptime and latency.
func overviewChip(c *ui.Context, icon, label, value string) {
	ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(3, 9).Radius(12).Background(colors.card).Border(1, colors.line).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(12).TextColor(theme.TextMuted)
		widgets.Muted(c, label).FontSize(12).SingleLine()
		ui.Text(c, value).FontSize(12).SingleLine()
	})
}

// usageColor is the color of a gauge or a bar filled to frac: the
// normal one, busy from 70%, high from 90%.
func usageColor(frac float64) ui.Color {
	switch {
	case frac >= 0.9:
		return colors.gaugeHigh
	case frac >= 0.7:
		return colors.gaugeBusy
	}
	return colors.gaugeOK
}

// rootDisk is the disk mounted on /, or the first one, nil for none.
func rootDisk(disks []monitor.Disk) *monitor.Disk {
	for i := range disks {
		if disks[i].Mount == "/" {
			return &disks[i]
		}
	}
	if len(disks) > 0 {
		return &disks[0]
	}
	return nil
}

// gaugeValue is what a gauge shows: how full it is, from 0 to 1, the
// text in the ring, and the line under its label.
type gaugeValue struct {
	label  string
	frac   float64
	center string
	detail string
}

// overviewGaugeValues are the four gauges of s: load, CPU, memory and
// the disk of /; dashes before the monitor's first stats.
func overviewGaugeValues(s *Session) []gaugeValue {
	st, info := &s.stats, s.overview.info
	g := []gaugeValue{
		{label: locales.OverviewLoad(), center: "-", detail: "-"},
		{label: locales.Cpu(), center: "-", detail: coresText(info)},
		{label: locales.SysMemory(), center: "-", detail: "-"},
		{label: locales.OverviewDisk(), center: "-", detail: "-"},
	}
	if g[1].detail == "" {
		g[1].detail = "-"
	}
	if !st.OK {
		return g
	}
	pct := func(f float64) string { return fmt.Sprintf("%.0f%%", 100*min(max(f, 0), 1)) }
	g[0].detail = fmt.Sprintf("%.2f / %.2f / %.2f", st.Load[0], st.Load[1], st.Load[2])
	if info.CPUs > 0 {
		g[0].frac = st.Load[0] / float64(info.CPUs)
		g[0].center = pct(g[0].frac)
	} else {
		g[0].center = fmt.Sprintf("%.2f", st.Load[0])
	}
	g[1].frac = st.CPU / 100
	g[1].center = pct(g[1].frac)
	if st.MemTotal > 0 {
		g[2].frac = ratio(st.MemUsed, st.MemTotal)
		g[2].center = pct(g[2].frac)
		g[2].detail = formatBytes(float64(st.MemUsed)) + " / " + formatBytes(float64(st.MemTotal))
	}
	if d := rootDisk(st.Disks); d != nil && d.Size > 0 {
		used := d.Size - min(d.Avail, d.Size)
		g[3].frac = ratio(used, d.Size)
		g[3].center = pct(g[3].frac)
		g[3].detail = formatBytes(float64(used)) + " / " + formatBytes(float64(d.Size))
		if d.Mount != "/" {
			g[3].label += " " + d.Mount
		}
	}
	return g
}

// overviewGauges is the card of the four gauges, in a row, or two rows
// of two where four do not fit; compact makes the rings smaller, for a
// short panel.
func overviewGauges(c *ui.Context, s *Session, width float32, compact bool) {
	cols := 4
	if width > 0 && width < 4*120 {
		cols = 2
	}
	size := float32(92)
	if compact || cols == 2 {
		size = 72
	}
	ui.Grid(c).Columns(cols).Gap(12).FillWidth().Padding(14, 12).Background(colors.card).Border(1, colors.line).Radius(8).Children(func() {
		for i, g := range overviewGaugeValues(s) {
			ui.Column(c.Key(i)).AlignItems(ui.Center).Gap(3).MinWidth(0).Children(func() {
				ringGauge(c, g, size)
				ui.Text(c, g.label).Bold().SingleLine().Margin(4, 0, 0, 0)
				widgets.Muted(c, g.detail).FontSize(12).SingleLine().MaxWidth(200).Tooltip(g.detail)
			})
		}
	})
}

// ringGauge is a ring size DIPs wide filled clockwise from the top to
// g.frac, in the color of its level, with g.center inside.
func ringGauge(c *ui.Context, g gaugeValue, size float32) {
	frac := float32(min(max(g.frac, 0), 1))
	track, fill := colors.gaugeTrack, usageColor(g.frac)
	stroke := size * 0.1
	ui.Box(c).Size(size, size).Center().Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := min(r.W, r.H)/2 - stroke/2
		var ring ui.Path
		ringPath(&ring, cx, cy, rad)
		p.StrokePath(&ring, stroke, track)
		if frac > 0 {
			var arc ui.Path
			ringPath(&arc, cx, cy, rad)
			p.StrokePath(arc.Trim(0, frac), stroke, fill)
		}
	}).Children(func() {
		ui.Text(c, g.center).FontSize(size * 0.2).Bold().SingleLine().FontFeatures("tnum")
	})
}

// ringPath adds a circle starting at its top, going clockwise, so that
// Trim(0, f) is the arc of a gauge filled to f.
func ringPath(p *ui.Path, cx, cy, r float32) {
	const k = 0.5522847498
	p.MoveTo(cx, cy-r)
	p.CubeTo(cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy)
	p.CubeTo(cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r)
	p.CubeTo(cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy)
	p.CubeTo(cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r)
}

// overviewGap is the space between the cards, and around them.
const overviewGap = 12

// overviewColumns is how many cards a row takes in width DIPs.
func overviewColumns(width float32) int {
	switch {
	case width >= 1200:
		return 3
	case width >= 600:
		return 2
	}
	return 1
}

// overviewRow is a row of cards of the same width, each built by one of
// cards. The row is as high as its highest card, and every card in it
// stretches to that height.
func overviewRow(c *ui.Context, key string, cards ...func()) {
	ui.Row(c.Key(key)).Gap(overviewGap).FillWidth().AlignItems(ui.Stretch).Children(func() {
		for i, card := range cards {
			ui.Column(c.Key(i)).Grow(1).Basis(0).MinWidth(0).Children(card)
		}
	})
}

// overviewCards are the cards under the gauges, in rows of cards as high
// as each other, as many to a row as width takes: the network, the disks
// and the system; Docker's engine across the width; the addresses and
// the top processes. Two to a row, the network is across the width over
// the disks and the system; one to a row, each is under the other.
func (a *App) overviewCards(c *ui.Context, s *Session, width float32) {
	st, ov := &s.stats, &s.overview
	network := func() { overviewNetwork(c, st) }
	disks := func() { overviewDisks(c, st) }
	system := func() { overviewSystem(c, s) }
	addrs := func() { overviewAddrs(c, ov) }
	procs := func() { overviewProcs(c, st, a.openProcesses) }
	docker := func() {
		if ov.docker.shown() {
			ui.Column(c.Key("docker")).FillWidth().Children(func() { a.dockerCard(c, s, width) })
		}
	}
	columns := overviewColumns(width)
	switch columns {
	case 3:
		overviewRow(c, "machine", network, disks, system)
	case 2:
		overviewRow(c, "network", network)
		overviewRow(c, "machine", disks, system)
	default:
		overviewRow(c, "network", network)
		overviewRow(c, "disks", disks)
		overviewRow(c, "system", system)
	}
	docker()
	if columns > 1 {
		overviewRow(c, "lists", addrs, procs)
	} else {
		overviewRow(c, "addrs", addrs)
		overviewRow(c, "procs", procs)
	}
}

// overviewCard is a card: an icon and a title, with actions at the
// right, over what body builds. It fills the height of its row.
func overviewCard(c *ui.Context, icon, title string, actions, body func()) {
	ui.Column(c).FillWidth().Grow(1).Background(colors.card).Border(1, colors.line).Radius(8).Padding(8, 12, 12, 12).Gap(8).
		Role(ui.RoleGroup).Label(title).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Height(26).Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(theme.Accent)
			ui.Text(c, title).Bold().SingleLine().Grow(1).MinWidth(0)
			if actions != nil {
				actions()
			}
		})
		body()
	})
}

// overviewNetwork is the busiest interface's speeds and their graph,
// which takes the height the card's row leaves.
func overviewNetwork(c *ui.Context, st *monitor.Stats) {
	overviewCard(c, "arrow-down-up", locales.TabNetwork(), func() {
		if st.Iface != "" {
			widgets.Muted(c, st.Iface).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
		}
	}, func() {
		ui.Row(c).Gap(16).Children(func() {
			speed := func(label, arrow string, rate float64, color ui.Color) {
				ui.Column(c).Gap(1).Grow(1).Basis(0).MinWidth(0).Children(func() {
					widgets.Muted(c, label).FontSize(12).SingleLine()
					ui.Text(c, arrow+" "+formatBytes(rate)+"/s").FontSize(16).Bold().TextColor(color).SingleLine().FontFeatures("tnum")
				})
			}
			speed(locales.UploadDirection(), "↑", st.TxRate, colors.tx)
			speed(locales.DownloadDirection(), "↓", st.RxRate, colors.rx)
		})
		const minChartHeight = 64
		chart := ui.Box(c).Grow(1).MinHeight(minChartHeight).Clip().Role(ui.RoleImage).Label(locales.TabNetwork())
		height := max(chart.Bounds().H, minChartHeight)
		chart.Children(func() {
			ui.Column(c).Absolute().Top(0).Left(0).Right(0).Children(func() {
				widgets.BarChart(c, height, monitor.HistoryLen, [][]float64{st.TxHist, st.RxHist}, []ui.Color{txChart, rxChart}, formatBytes)
			})
		})
	})
}

// overviewDisks is every mount: a bar of its use, its used, free and
// total space.
func overviewDisks(c *ui.Context, st *monitor.Stats) {
	overviewCard(c, "hard-drive", locales.OverviewDisks(), nil, func() {
		if len(st.Disks) == 0 {
			widgets.Muted(c, "-")
		}
		for i, d := range st.Disks {
			used := d.Size - min(d.Avail, d.Size)
			frac := ratio(used, d.Size)
			ui.Column(c.Key(i)).Gap(4).FillWidth().Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, d.Mount).SingleLine().Grow(1).MinWidth(0).Tooltip(d.Mount)
					ui.Text(c, fmt.Sprintf("%.0f%%", 100*frac)).FontSize(12).Bold().TextColor(usageColor(frac)).FontFeatures("tnum")
				})
				usageBar(c, frac)
				usage := locales.OverviewDiskUsage(formatBytes(float64(used)), formatBytes(float64(d.Avail)), formatBytes(float64(d.Size)))
				widgets.Muted(c, usage).FontSize(12)
			})
		}
	})
}

// usageBar is a thin bar filled to frac in the color of its level.
func usageBar(c *ui.Context, frac float64) {
	frac = min(max(frac, 0), 1)
	ui.Box(c).FillWidth().Height(6).Radius(3).Background(colors.gaugeTrack).Clip().Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Bottom(0).WidthPercent(float32(frac * 100)).Radius(3).Background(usageColor(frac))
	})
}

// overviewSystem is what the server is: its processor, memory, system,
// kernel, architecture and uptime.
func overviewSystem(c *ui.Context, s *Session) {
	st, info := &s.stats, s.overview.info
	or := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	cpu := []string{or(info.CPUModel)}
	if cores := coresText(info); cores != "" {
		cpu = append(cpu, cores)
	}
	mem, swap := "-", "-"
	if info.MemTotal > 0 {
		mem = formatSize(int64(info.MemTotal))
	}
	if info.SwapTotal > 0 {
		swap = formatSize(int64(info.SwapTotal))
	}
	if st.OK {
		if st.MemTotal > 0 {
			mem = formatBytes(float64(st.MemUsed)) + " / " + formatBytes(float64(st.MemTotal))
		}
		if st.SwapTotal > 0 {
			swap = formatBytes(float64(st.SwapUsed)) + " / " + formatBytes(float64(st.SwapTotal))
		}
	}
	uptime := info.Uptime
	if st.OK {
		uptime = st.Uptime
	}
	rows := []sysInfoRow{
		{label: locales.SysCPU(), values: cpu},
		{label: locales.SysMemory(), values: []string{mem}},
		{label: locales.Swap(), values: []string{swap}},
		{label: locales.SysOS(), values: []string{or(info.OS)}},
		{label: locales.SysKernel(), values: []string{or(info.Kernel)}},
		{label: locales.SysArch(), values: []string{or(info.Arch)}},
		{label: locales.SysUptime(), values: []string{uptimeText(uptime)}},
	}
	overviewCard(c, "server", locales.SystemInfo(), nil, func() {
		ui.Column(c).Gap(5).FillWidth().Children(func() {
			for i, r := range rows {
				ui.Row(c.Key(i)).Gap(10).AlignItems(ui.Start).Children(func() {
					widgets.Muted(c, r.label).Width(86).Shrink(0).SingleLine()
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						for j, v := range r.values {
							ui.Text(c.Key(j), v).MaxLines(2).Tooltip(v).Selectable()
						}
					})
				})
			}
		})
	})
}

// overviewAddrs is the server's addresses, those of real interfaces
// first, the virtual ones behind "Show all"; a click on one copies it,
// and Copy all copies every one.
func overviewAddrs(c *ui.Context, ov *overviewState) {
	real, virtual := splitAddrs(ov.info.Addrs)
	overviewCard(c, "globe", locales.SysAddresses(), func() {
		if len(ov.info.Addrs) > 0 && widgets.IconButton(c, "copy", locales.CopyAll()).Clicked() {
			var b strings.Builder
			for _, ad := range ov.info.Addrs {
				fmt.Fprintf(&b, "%s  %s\n", ad.Iface, ad.Addr)
			}
			c.WriteClipboard(b.String())
			c.Toast(locales.CopiedText(locales.SysAddresses()))
		}
	}, func() {
		if len(real) == 0 {
			widgets.Muted(c, "-")
			return
		}
		ui.Column(c).FillWidth().Children(func() {
			shown := real
			if ov.allAddrs {
				shown = append(shown[:len(shown):len(shown)], virtual...)
			}
			for i, ad := range shown {
				addrRow(c.Key(i), ad)
			}
		})
		if len(virtual) > 0 && showAllLink(c, ov.allAddrs, len(real)+len(virtual)).Clicked() {
			ov.allAddrs = !ov.allAddrs
		}
	})
}

// addrRow is an address, with its interface; a click copies the address
// without its prefix length.
func addrRow(c *ui.Context, ad monitor.Addr) {
	ip := addrIP(ad.Addr)
	row := ui.Row(c).Gap(10).Padding(2, 4).Radius(4).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(ad.Iface + " " + ad.Addr).Tooltip(locales.Copy())
	if row.Hovered() {
		row.Background(theme.Text.Alpha(0.06))
	}
	if row.Clicked() {
		c.WriteClipboard(ip)
		c.Toast(locales.CopiedText(ip))
	}
	row.Children(func() {
		widgets.Muted(c, ad.Iface).FontSize(12).Width(112).Shrink(0).SingleLine()
		ui.Text(c, ad.Addr).SingleLine().Grow(1).MinWidth(0)
	})
}

// showAllLink is the link under a list of addresses that shows those of
// virtual interfaces too, n addresses in all, or hides them again.
func showAllLink(c *ui.Context, all bool, n int) ui.Element {
	label := locales.OverviewShowAll(n)
	if all {
		label = locales.OverviewShowLess()
	}
	return ui.Text(c, label).FontSize(12).TextColor(theme.Accent).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).AlignSelf(ui.Start)
}

// overviewProcs is the processes using the most memory; the button at
// the top opens the Processes tab.
func overviewProcs(c *ui.Context, st *monitor.Stats, open func()) {
	overviewCard(c, "cpu", locales.OverviewTopProcs(), func() {
		if widgets.IconButton(c, "arrow-up-right", locales.OpenProcesses()).Clicked() {
			open()
		}
	}, func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				widgets.Muted(c, locales.Command()).FontSize(12).Grow(1).MinWidth(0)
				widgets.Muted(c, locales.Memory()).FontSize(12).Width(64).TextAlign(ui.End)
				widgets.Muted(c, locales.Cpu()).FontSize(12).Width(44).TextAlign(ui.End)
			})
			if len(st.Procs) == 0 {
				widgets.Muted(c, "-")
			}
			for i, p := range st.Procs {
				ui.Row(c.Key(i)).Gap(8).Children(func() {
					ui.Text(c, p.Name).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, formatBytes(float64(p.RSS))).Width(64).TextAlign(ui.End).FontFeatures("tnum")
					ui.Text(c, fmt.Sprintf("%.1f%%", p.CPU)).Width(44).TextAlign(ui.End).FontFeatures("tnum")
				})
			}
		})
	})
}

// iconTextButton is a small button with an icon before its text.
func iconTextButton(c *ui.Context, icon, text string) ui.Element {
	return ui.Button(c, "").FontSize(12).Padding(2, 8).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get(icon)).FontSize(12)
			ui.Text(c, text).FontSize(12)
		})
	}).Label(text)
}
