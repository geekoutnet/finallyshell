package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// sysInfoTimeout is how long the popover waits for the server.
const sysInfoTimeout = 20 * time.Second

// sysInfoPopover is the System info popover below the IP. There is one,
// for the session shown; each open reads the server again.
type sysInfoPopover struct {
	open    bool
	s       *Session // the session it shows
	gen     int      // counts the reads; a result of an older one is dropped
	loading bool
	info    monitor.SysInfo
	err     error
	off     bool // the host allows no exec channel
	cancel  context.CancelFunc
	// allAddrs shows the addresses of virtual interfaces too, which the
	// list leaves out at first (virtualIface).
	allAddrs bool
}

// execAllowed tells whether the app may open exec channels to h's server
// besides the shell and SFTP: the server monitor and the system info.
// It is the one place that decides, by the host's Monitor switch; a
// session without a host (a local terminal) allows none.
func execAllowed(h *config.Host) bool { return h != nil && h.MonitorEnabled() }

var errNoClient = errors.New("not connected")

// sysInfoReader reads a server's system info over client.
type sysInfoReader func(ctx context.Context, client *ssh.Client) (monitor.SysInfo, error)

// readSysInfo reads what the popover shows, unless App.readSysInfo
// stands in for it, as in tests.
func readSysInfo(ctx context.Context, client *ssh.Client) (monitor.SysInfo, error) {
	if client == nil {
		return monitor.SysInfo{}, errNoClient
	}
	return monitor.ReadSysInfo(ctx, client)
}

// openSysInfo opens the popover for s and starts reading the server on a
// goroutine, when its host allows it.
func (a *App) openSysInfo(s *Session) {
	a.closeSysInfo()
	p := &a.sysInfo
	p.open, p.s = true, s
	p.info, p.err, p.loading, p.off, p.allAddrs = monitor.SysInfo{}, nil, false, false, false
	if !execAllowed(s.Host) {
		p.off = true
		return
	}
	if s.status != statusConnected {
		return
	}
	gen := p.gen
	p.loading = true
	p.cancel = a.startSysInfoRead(s, func(info monitor.SysInfo, err error) {
		// Closed, opened again, or showing another session since.
		if p.gen != gen || p.s != s {
			return
		}
		p.loading, p.info, p.err, p.cancel = false, info, err, nil
	})
}

// startSysInfoRead reads s's system info on a goroutine, giving up after
// sysInfoTimeout, and calls done with the result on the UI thread. The
// popover and the Overview tab (overview.go) read with it. It returns
// what stops the read.
func (a *App) startSysInfoRead(s *Session, done func(monitor.SysInfo, error)) context.CancelFunc {
	read := a.readSysInfo
	if read == nil {
		read = readSysInfo
	}
	ctx, cancel := context.WithTimeout(context.Background(), sysInfoTimeout)
	client := s.client
	go func() {
		info, err := read(ctx, client)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = context.DeadlineExceeded
		}
		cancel()
		a.post(func() { done(info, err) })
	}()
	return cancel
}

// closeSysInfo closes the popover, dropping the read under way.
func (a *App) closeSysInfo() {
	p := &a.sysInfo
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.open, p.loading, p.s = false, false, nil
	p.gen++
}

// sysInfoButton is the button under the IP, with its popover; it is
// disabled while s is nil or not connected.
func (a *App) sysInfoButton(c *ui.Context, s *Session) {
	p := &a.sysInfo
	// Another session shown, or this one closed: the popover was its.
	if p.open && (s == nil || p.s != s) {
		a.closeSysInfo()
	}
	enabled := s != nil && s.status == statusConnected
	b := ui.Button(c, locales.SystemInfo()).FontSize(12).Padding(2, 10).AlignSelf(ui.Start).Disabled(!enabled)
	if b.Clicked() && enabled {
		if p.open {
			a.closeSysInfo()
		} else {
			a.openSysInfo(s)
		}
	}
	ui.Popover(c, b, &p.open, func() { a.sysInfoBody(c) })
	if !p.open && p.s != nil { // dismissed by a press outside or Escape
		a.closeSysInfo()
	}
}

func (a *App) sysInfoBody(c *ui.Context) {
	p := &a.sysInfo
	ui.Column(c).Width(400).Padding(8, 10, 10, 10).Gap(4).Children(func() {
		ui.Row(c).Gap(6).Margin(0, 0, 4, 0).Children(func() {
			ui.Text(c, locales.SystemInfo()).Bold().Grow(1)
			if p.s != nil && iconTextButton(c, "gauge", locales.TabOverview()).Tooltip(locales.OverviewOpenTip()).Clicked() {
				a.closeSysInfo()
				a.openBottomTab(tabOverview)
			}
			ready := !p.off && !p.loading && p.err == nil && p.s != nil && p.s.status == statusConnected
			if iconTextButton(c, "copy", locales.Copy()).Disabled(!ready).Clicked() && ready {
				c.WriteClipboard(sysInfoSummary(p.info))
			}
		})
		switch {
		case p.off:
			widgets.Muted(c, locales.SysInfoOff())
		case p.loading:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 0).Children(func() {
				ui.Spinner(c).Label(locales.ReadingServerInfo())
				widgets.Muted(c, locales.ReadingServerInfo())
			})
		case p.err != nil:
			ui.Text(c, locales.SysInfoFailed(describeSysInfoError(p.err))).TextColor(theme.Danger)
			if p.s != nil && p.s.status == statusConnected && ui.Button(c, locales.Retry()).AlignSelf(ui.Start).Clicked() {
				a.openSysInfo(p.s)
			}
		case p.s == nil || p.s.status != statusConnected:
			widgets.Muted(c, locales.NotConnected())
		default:
			for i, r := range sysInfoRows(p.info) {
				ui.Row(c.Key(i)).Gap(10).AlignItems(ui.Start).Children(func() {
					widgets.Muted(c, r.label).Width(86).Shrink(0)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						for j, v := range r.values {
							ui.Text(c.Key(j), v).Selectable()
						}
						if len(r.more) == 0 {
							return
						}
						if p.allAddrs {
							for j, v := range r.more {
								ui.Text(c.Key(len(r.values)+j), v).Selectable()
							}
						}
						if showAllLink(c, p.allAddrs, len(r.values)+len(r.more)).Clicked() {
							p.allAddrs = !p.allAddrs
						}
					})
				})
			}
		}
	})
}

func describeSysInfoError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return locales.SysInfoTimeout()
	case errors.Is(err, monitor.ErrSysInfoOutput):
		return locales.SysInfoNoShell()
	case errors.Is(err, errNoClient):
		return locales.NotConnected()
	}
	return describeError(err)
}

// sysInfoRow is a line of the popover: a label and one or more values,
// and more values that show only when asked: the addresses of virtual
// interfaces (splitAddrs).
type sysInfoRow struct {
	label  string
	values []string
	more   []string
}

func sysInfoRows(info monitor.SysInfo) []sysInfoRow {
	or := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	cpu := []string{or(info.CPUModel)}
	if cores := coresText(info); cores != "" {
		cpu = append(cpu, cores)
	}
	mem := "-"
	if info.MemTotal > 0 {
		mem = formatSize(int64(info.MemTotal))
	}
	swap := "-"
	if info.SwapTotal > 0 {
		swap = formatSize(int64(info.SwapTotal))
	}
	uptime := uptimeText(info.Uptime)
	real, virtual := splitAddrs(info.Addrs)
	var addrs, more []string
	for _, ad := range real {
		addrs = append(addrs, ad.Iface+"  "+ad.Addr)
	}
	for _, ad := range virtual {
		more = append(more, ad.Iface+"  "+ad.Addr)
	}
	var disks []string
	for _, d := range info.Disks {
		line := d.Name + "  " + formatSize(int64(d.Size))
		if d.Model != "" {
			line += "  " + d.Model
		}
		disks = append(disks, line)
	}
	list := func(l []string) []string {
		if len(l) == 0 {
			return []string{"-"}
		}
		return l
	}
	return []sysInfoRow{
		{label: locales.SysHostname(), values: []string{or(info.Hostname)}},
		{label: locales.SysOS(), values: []string{or(info.OS)}},
		{label: locales.SysKernel(), values: []string{or(info.Kernel)}},
		{label: locales.SysArch(), values: []string{or(info.Arch)}},
		{label: locales.SysCPU(), values: cpu},
		{label: locales.SysMemory(), values: []string{mem}},
		{label: locales.Swap(), values: []string{swap}},
		{label: locales.SysUptime(), values: []string{uptime}},
		{label: locales.SysAddresses(), values: list(addrs), more: more},
		{label: locales.SysDisks(), values: list(disks)},
	}
}

// coresText is the server's physical cores and logical processors, as
// "2 cores, 4 threads", or its processors alone when they are the same
// or the cores are unknown: "" when neither is known.
func coresText(info monitor.SysInfo) string {
	switch {
	case info.Cores > 0 && info.Cores != info.CPUs && info.CPUs > 0:
		return locales.SysCoresThreads(info.Cores, info.CPUs)
	case info.CPUs > 0:
		return locales.SysCores(info.CPUs)
	}
	return ""
}

// uptimeText is how long a server has been up, as "410 days, 0:17":
// "-" for 0, unknown.
func uptimeText(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	days := int(d / (24 * time.Hour))
	rest := d % (24 * time.Hour)
	return locales.SysUptimeValue(days, fmt.Sprintf("%d:%02d", int(rest/time.Hour), int(rest%time.Hour/time.Minute)))
}

// sysInfoSummary is what Copy puts on the clipboard: the popover's rows
// as plain text, a list's items on lines of their own.
func sysInfoSummary(info monitor.SysInfo) string {
	var b strings.Builder
	for _, r := range sysInfoRows(info) {
		if len(r.values)+len(r.more) == 1 {
			fmt.Fprintf(&b, "%s: %s\n", r.label, r.values[0])
			continue
		}
		fmt.Fprintf(&b, "%s:\n", r.label)
		for _, v := range append(r.values, r.more...) {
			fmt.Fprintf(&b, "  %s\n", v)
		}
	}
	return b.String()
}
