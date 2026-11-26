package app

import (
	"fmt"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The server's details on the desktop: on the right of the wallpaper, as a
// Windows server's BGInfo, what the server is (its name, system, processor,
// memory, disks and addresses) and how busy it is now, in light text on a
// pane of dark glass that any wallpaper shows through. It is under the
// windows, as part of the wallpaper. A click on its button folds it to its
// name and back; the choice is kept (DesktopSettings.HideServerInfo).

// desktopInfoW is the details' width; a desktop narrower than
// desktopInfoMinArea shows them folded.
const (
	desktopInfoW       = 300
	desktopInfoMinArea = 720
)

// desktopInfoText is the details' text, light on the glass.
var (
	desktopInfoText  = ui.RGBA(255, 255, 255, 0.94)
	desktopInfoMuted = ui.RGBA(255, 255, 255, 0.66)
	desktopInfoGlass = ui.RGBA(12, 14, 28, 0.34)
	desktopInfoLine  = ui.RGBA(255, 255, 255, 0.16)
)

// desktopServerInfo shows s's details on the desktop d.
func (a *App) desktopServerInfo(c *ui.Context, d *desktopState, s *Session) {
	if s == nil || s.isLocal() {
		return
	}
	// The details are the Overview tab's, read once per connection.
	ov := &s.overview
	if s.client != nil && !ov.loading && ov.client != s.client && execAllowed(s.Host) {
		a.readOverview(s)
	}
	hidden := a.desktopPreferences().HideServerInfo || d.area.W > 0 && d.area.W < desktopInfoMinArea
	name := s.label()
	if ov.read && ov.info.Hostname != "" {
		name = ov.info.Hostname
	}
	box := ui.Column(c.Key("server-info")).Absolute().Top(18).Right(18).Radius(14).Background(desktopInfoGlass).
		Border(1, desktopInfoLine).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.18)).Role(ui.RoleGroup).Label(locales.DesktopInfoTitle())
	if !hidden {
		box.Width(desktopInfoW).Padding(14, 16, 16, 16).Gap(8)
	} else {
		box.Padding(6, 6, 6, 12)
	}
	box.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			iconSize := float32(16)
			if hidden {
				iconSize = 13
			}
			ui.Icon(c, icons.Get("server")).FontSize(iconSize).TextColor(desktopInfoText).Shrink(0)
			title := ui.Text(c, name).Bold().SingleLine().MinWidth(0).Shrink(1).TextColor(desktopInfoText).Tooltip(name)
			if !hidden {
				title.FontSize(18).Grow(1)
			} else {
				title.FontSize(13).MaxWidth(220)
			}
			icon, label := "chevron-up", locales.DesktopInfoHide()
			if hidden {
				icon, label = "chevron-down", locales.DesktopInfoShow()
			}
			toggle := ui.Box(c).Size(22, 22).Center().Radius(6).Shrink(0).Cursor(ui.CursorPointer).
				Role(ui.RoleButton).Label(label).Tooltip(label)
			if toggle.Hovered() {
				toggle.Background(ui.RGBA(255, 255, 255, 0.14))
			}
			toggle.Children(func() { ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(desktopInfoMuted) })
			if toggle.Clicked() && (d.area.W == 0 || d.area.W >= desktopInfoMinArea) {
				a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.HideServerInfo = !hidden })
			}
		})
		if hidden {
			return
		}
		a.desktopInfoBody(c, s)
	})
}

// desktopInfoBody is the details under the server's name.
func (a *App) desktopInfoBody(c *ui.Context, s *Session) {
	ov, st := &s.overview, &s.stats
	info := ov.info
	if !ov.read {
		text := locales.DesktopInfoReading()
		if ov.err != nil || !execAllowed(s.Host) || s.client == nil {
			text = locales.DesktopInfoUnavailable()
		}
		ui.Text(c, text).FontSize(12).TextColor(desktopInfoMuted)
	} else {
		system := strings.Join(nonEmpty(info.OS, info.Arch), " · ")
		if system != "" {
			ui.Text(c, system).FontSize(12).TextColor(desktopInfoMuted).MaxWidthPercent(100)
		}
	}
	desktopInfoDivider(c)
	row := func(label, value string) {
		if value == "" {
			return
		}
		ui.Row(c.Key("row:" + label)).Gap(10).FillWidth().AlignItems(ui.Start).Children(func() {
			ui.Text(c, label).FontSize(12).Width(64).Shrink(0).TextColor(desktopInfoMuted)
			ui.Text(c, value).FontSize(12).Grow(1).MinWidth(0).TextColor(desktopInfoText).Selectable()
		})
	}
	if ov.read {
		row(locales.DesktopInfoKernel(), info.Kernel)
		cpu := info.CPUModel
		if info.CPUs > 0 {
			cores := locales.DesktopInfoThreads(fmt.Sprint(info.CPUs))
			if info.Cores > 0 {
				cores = locales.DesktopInfoCores(fmt.Sprint(info.Cores), fmt.Sprint(info.CPUs))
			}
			cpu = strings.Join(nonEmpty(cpu, cores), "\n")
		}
		row(locales.DesktopInfoCPU(), cpu)
		if info.MemTotal > 0 {
			row(locales.DesktopInfoMemory(), formatBytes(float64(info.MemTotal)))
		}
		if info.SwapTotal > 0 {
			row(locales.DesktopInfoSwap(), formatBytes(float64(info.SwapTotal)))
		}
		var disks []string
		for _, disk := range info.Disks {
			disks = append(disks, strings.Join(nonEmpty(disk.Name, formatBytes(float64(disk.Size)), disk.Model), "  "))
		}
		row(locales.DesktopInfoDisks(), strings.Join(disks, "\n"))
		var addrs []string
		for _, addr := range info.Addrs {
			if addr.Iface != "lo" && !strings.HasPrefix(addr.Addr, "127.") && !strings.HasPrefix(addr.Addr, "::1") && len(addrs) < 4 {
				addrs = append(addrs, addr.Addr)
			}
		}
		row(locales.DesktopInfoAddresses(), strings.Join(addrs, "\n"))
	}
	if !st.OK {
		return
	}
	row(locales.DesktopInfoUptime(), uptimeText(st.Uptime))
	row(locales.DesktopInfoLoad(), fmt.Sprintf("%.2f  %.2f  %.2f", st.Load[0], st.Load[1], st.Load[2]))
	desktopInfoDivider(c)
	desktopInfoBar(c, locales.DesktopInfoCPU(), st.CPU/100, fmt.Sprintf("%.0f%%", st.CPU))
	if st.MemTotal > 0 {
		desktopInfoBar(c, locales.DesktopInfoMemory(), ratio(st.MemUsed, st.MemTotal), usage(st.MemUsed, st.MemTotal))
	}
	for _, disk := range st.Disks {
		if disk.Mount == "/" && disk.Size > 0 {
			used := disk.Size - min(disk.Avail, disk.Size)
			desktopInfoBar(c, locales.DesktopInfoRoot(), ratio(used, disk.Size), usage(used, disk.Size))
		}
	}
}

// nonEmpty is the values that are not empty.
func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func desktopInfoDivider(c *ui.Context) {
	ui.Box(c).FillWidth().Height(1).Background(desktopInfoLine)
}

// desktopInfoBar is a usage, as a thin bar under its label and value.
func desktopInfoBar(c *ui.Context, label string, frac float64, value string) {
	frac = min(max(frac, 0), 1)
	color := ui.RGBA(110, 231, 183, 0.95)
	switch {
	case frac >= 0.9:
		color = ui.RGBA(252, 129, 129, 0.95)
	case frac >= 0.7:
		color = ui.RGBA(251, 191, 36, 0.95)
	}
	ui.Column(c.Key("bar:" + label)).Gap(4).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, label).FontSize(12).Grow(1).TextColor(desktopInfoMuted)
			ui.Text(c, value).FontSize(12).TextColor(desktopInfoText).FontFeatures("tnum")
		})
		ui.Box(c).FillWidth().Height(4).Radius(2).Background(ui.RGBA(255, 255, 255, 0.16)).Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * float32(frac), H: r.H}, color, 2)
		})
	})
}
