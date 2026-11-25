package app

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopSetInfoState struct {
	load, detect     desktopSetLoad
	summary          monitor.SysInfo
	details          monitor.SysConfInfo
	public, alias    string
	aliasErr         string
	showIPs, editing bool
}

func (a *App) desktopSetRefreshInfo(window *desktopWindow, state *desktopSettingsState) {
	session := window.session
	reader, client := a.readSysInfo, session.client
	if reader == nil {
		reader = readSysInfo
	}
	run := systemReader(session)
	type result struct {
		summary monitor.SysInfo
		details monitor.SysConfInfo
	}
	desktopSetRead(a, window, state, &state.info.load, func(ctx context.Context) (result, string, error) {
		summary, err := reader(ctx, client)
		if err != nil {
			return result{}, "", err
		}
		text, stderr, err := run(ctx, monitor.SysConfInfoCommand, nil, nil)
		if err == nil && !monitor.ValidSysConfOutput(text) {
			err = monitor.ErrSysInfoOutput
		}
		return result{summary, monitor.ParseSysConfInfo(text)}, stderr, err
	}, func(value result) {
		state.info.summary, state.info.details = value.summary, value.details
	})
}

func (a *App) desktopSetInfo(c *ui.Context, window *desktopWindow, state *desktopSettingsState, compact bool) {
	page, session := &state.info, window.session
	if state.online && !page.load.loaded {
		a.desktopSetRefreshInfo(window, state)
	}
	info := page.summary
	if info.MemTotal == 0 {
		info.MemTotal = session.stats.MemTotal
	}
	if info.Uptime == 0 {
		info.Uptime = session.stats.Uptime
	}
	if !state.online {
		desktopSetOffline(c, session)
	}
	desktopSetReadNote(c, &page.load)
	desktopSetCard(c).ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.DesktopSetCopyAll()).Chosen() {
			c.WriteClipboard(desktopSetSummary(session, page, info))
		}
		if menu.Item(locales.ServerRefresh()).Disabled(!state.online || page.load.cancel != nil).Chosen() {
			a.desktopSetRefreshInfo(window, state)
		}
	}).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			desktopSetHeading(c, "server", locales.DesktopSetOverview())
			ui.Box(c).Grow(1)
			if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Disabled(!state.online || page.load.cancel != nil).Clicked() {
				a.desktopSetRefreshInfo(window, state)
			}
			if iconTextButton(c, "copy", locales.DesktopSetCopyAll()).Clicked() {
				c.WriteClipboard(desktopSetSummary(session, page, info))
			}
		})
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			label, tint := desktopSetDistro(page.details.Distro)
			ui.Box(c).Size(42, 42).Shrink(0).Radius(10).Center().Background(tint).Children(func() {
				if label == "" {
					ui.Icon(c, icons.Get("terminal")).FontSize(23).TextColor(ui.Hex("#ffffff"))
				} else {
					ui.Text(c, label).Bold().FontSize(20).TextColor(ui.Hex("#ffffff"))
				}
			})
			ui.Text(c, desktopSetValue(info.Hostname, session.label())).FontSize(19).Bold().Grow(1).MinWidth(0).SingleLine().Tooltip(info.Hostname)
			if info.Uptime > 0 {
				ui.Text(c, desktopSetUptime(info.Uptime)).FontSize(11).Bold().Padding(4, 7).Radius(12).
					Background(colors.gaugeOK.Alpha(0.14)).TextColor(colors.gaugeOK).SingleLine().Shrink(0)
			}
		})
		a.desktopSetAlias(c, session, page)
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Wrap().Children(func() {
			for _, field := range desktopSetInfoFields(session, page, info) {
				ui.Column(c.Key(field[0])).MinWidth(115).Grow(1).Children(func() { desktopSetField(c, field[0], field[1]) })
			}
		})
	})
	a.desktopSetIPs(c, window, state, info)
	resourceCards := func() {
		desktopSetCard(c.Key("cpu")).Grow(1).Children(func() {
			desktopSetHeading(c, "cpu", locales.SysCPU())
			ui.Text(c, desktopSetValue(info.CPUModel, locales.DesktopSetUnknown())).Bold().FillWidth()
			if cores := coresText(info); cores != "" {
				widgets.Muted(c, cores)
			}
		})
		desktopSetCard(c.Key("memory")).Grow(1).Children(func() {
			desktopSetHeading(c, "memory-stick", locales.SysMemory())
			if info.MemTotal > 0 {
				ui.Text(c, formatSize(int64(info.MemTotal))).Bold()
			} else {
				widgets.Muted(c, locales.DesktopSetUnknown())
			}
		})
	}
	if compact {
		ui.Column(c).FillWidth().Gap(9).Children(resourceCards)
	} else {
		ui.Row(c).FillWidth().Gap(9).AlignItems(ui.Stretch).Children(resourceCards)
	}
	desktopSetCard(c).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			desktopSetHeading(c, "hard-drive", locales.DesktopSetStorage())
			ui.Box(c).Grow(1)
			if ui.Button(c, locales.DesktopSetTaskMgr()).Clicked() {
				a.desktopOpen(session, appTaskMgr, nil)
			}
		})
		mounts := desktopSetMounts(session, page)
		if len(mounts) == 0 {
			widgets.Muted(c, locales.DesktopSetUnknown())
		}
		for index, mount := range mounts {
			ui.Column(c.Key(index)).FillWidth().Gap(4).Children(func() {
				ui.Row(c).Gap(6).Wrap().Children(func() {
					ui.Text(c, desktopSetMountName(mount)).Bold().MinWidth(0)
					ui.Box(c).Grow(1)
					widgets.Muted(c, desktopSetMountSpace(mount)).FontSize(12)
				})
				fraction := 100 * float32(mount.Size-min(mount.Size, mount.Avail)) / float32(max(mount.Size, 1))
				ui.Box(c).FillWidth().Height(5).Radius(3).Clip().Background(colors.gaugeTrack).Children(func() {
					ui.Box(c).WidthPercent(fraction).Height(5).Radius(3).Background(theme.Accent)
				})
			})
		}
	})
}

func (a *App) desktopSetAlias(c *ui.Context, session *Session, page *desktopSetInfoState) {
	host := a.hosts.Host(session.Host.ID)
	editable := host != nil && !host.Deleted()
	ui.Row(c).Gap(5).FillWidth().AlignItems(ui.Center).Wrap().Children(func() {
		widgets.Muted(c, locales.DesktopSetAlias()).FontSize(11)
		if !page.editing || !editable {
			ui.Text(c, session.Host.Title()).SingleLine().MinWidth(0).MaxWidth(240).Selectable().Tooltip(session.Host.Title())
			if widgets.IconButton(c, "pencil", locales.RenameConnection()).Disabled(!editable).Clicked() {
				page.alias, page.editing, page.aliasErr = host.Name, true, ""
			}
			if widgets.IconButton(c, "settings", locales.DesktopSetEditConnection()).Disabled(!editable).Clicked() {
				a.editConnection(host)
			}
			return
		}
		input := ui.TextInput(c, &page.alias).Label(locales.DesktopSetAlias()).AutoFocus().Width(155).MinWidth(0)
		if ui.Button(c, locales.Save()).Clicked() || input.Submitted() {
			old := host.Name
			host.Name = strings.TrimSpace(page.alias)
			if err := a.saveHosts(a.hosts); err != nil {
				host.Name = old
				page.aliasErr = locales.ErrSaveFailed(err.Error())
			} else {
				page.editing, page.aliasErr = false, ""
				a.redraw()
			}
		}
		if ui.Button(c, locales.Cancel()).Clicked() {
			page.editing, page.aliasErr = false, ""
		}
	})
	if page.aliasErr != "" {
		ui.Text(c, page.aliasErr).TextColor(theme.Danger)
	}
}

func (a *App) desktopSetIPs(c *ui.Context, window *desktopWindow, state *desktopSettingsState, info monitor.SysInfo) {
	page := &state.info
	desktopSetCard(c).Children(func() {
		ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Wrap().Children(func() {
			desktopSetHeading(c, "globe", locales.DesktopSetAddresses())
			ui.Box(c).Grow(1)
			label, icon := locales.DesktopSetShowIP(), "eye"
			if page.showIPs {
				label, icon = locales.DesktopSetHideIP(), "eye-off"
			}
			if widgets.IconButton(c, icon, label).Clicked() {
				page.showIPs = !page.showIPs
			}
			if ui.Button(c, locales.DesktopSetDetectIP()).Tooltip(locales.DesktopSetIPNotice()).Disabled(!state.online || page.detect.cancel != nil).Clicked() {
				run := systemReader(window.session)
				desktopSetRead(a, window, state, &page.detect, func(ctx context.Context) (string, string, error) {
					out, stderr, err := run(ctx, monitor.SysConfPublicIPCommand, nil, nil)
					if err != nil {
						return "", stderr, err
					}
					ip, err := netip.ParseAddr(strings.TrimSpace(out))
					return ip.String(), "", err
				}, func(ip string) { page.public, page.showIPs = ip, true })
			}
		})
		desktopSetReadNote(c, &page.detect)
		if !page.showIPs {
			widgets.Muted(c, locales.DesktopSetIPHidden())
			return
		}
		ui.Row(c).FillWidth().Gap(10).Wrap().Children(func() {
			ui.Column(c).Grow(1).MinWidth(145).Children(func() {
				desktopSetField(c, locales.DesktopSetPublicIP(), desktopSetPublic(window.session, page.public))
			})
			ui.Column(c).Grow(1).MinWidth(165).Children(func() {
				desktopSetField(c, locales.DesktopSetPrivateIP(), desktopSetAddrs(page, info))
			})
		})
		widgets.Muted(c, locales.DesktopSetIPNotice()).FontSize(11)
	})
}

func desktopSetValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func desktopSetInfoFields(session *Session, page *desktopSetInfoState, info monitor.SysInfo) [][2]string {
	return [][2]string{
		{locales.DesktopSetUser(), systemUser(session)}, {locales.SysOS(), strings.TrimSpace(info.OS + " " + info.Arch)},
		{locales.SysKernel(), info.Kernel}, {locales.DesktopSetVirtualization(), page.details.Virtualization}, {locales.DesktopSetZone(), page.details.Zone},
	}
}

func desktopSetMounts(session *Session, page *desktopSetInfoState) []monitor.SysConfMount {
	if len(page.details.Mounts) > 0 {
		return page.details.Mounts
	}
	var mounts []monitor.SysConfMount
	for _, disk := range session.stats.Disks {
		mounts = append(mounts, monitor.SysConfMount{Disk: disk})
	}
	return mounts
}

func desktopSetMountName(mount monitor.SysConfMount) string {
	if mount.Device != "" {
		return mount.Device + "  ·  " + mount.Mount
	}
	return mount.Mount
}

func desktopSetMountSpace(mount monitor.SysConfMount) string {
	return locales.DesktopSetFree(formatSize(int64(mount.Avail)), formatSize(int64(mount.Size)))
}

func desktopSetUptime(uptime time.Duration) string {
	return locales.DesktopSetUptime(int(uptime/(24*time.Hour)), int(uptime%(24*time.Hour)/time.Hour))
}

func desktopSetPublic(session *Session, detected string) string {
	if detected != "" {
		return detected
	}
	return desktopSetValue(session.ip, session.Host.Addr)
}

func desktopSetAddrs(page *desktopSetInfoState, info monitor.SysInfo) string {
	addrs := page.details.Addrs
	if len(addrs) == 0 {
		addrs = info.Addrs
	}
	var lines []string
	for _, addr := range addrs {
		if prefix, err := netip.ParsePrefix(addr.Addr); err == nil && prefix.Addr().IsLoopback() {
			continue
		}
		lines = append(lines, addr.Iface+"  "+addr.Addr)
	}
	return strings.Join(lines, "\n")
}

func desktopSetSummary(session *Session, page *desktopSetInfoState, info monitor.SysInfo) string {
	var text strings.Builder
	add := func(label, value string) {
		fmt.Fprintf(&text, "%s: %s\n", label, desktopSetValue(value, locales.DesktopSetUnknown()))
	}
	add(locales.SysHostname(), info.Hostname)
	add(locales.DesktopSetAlias(), session.Host.Title())
	for _, field := range desktopSetInfoFields(session, page, info) {
		add(field[0], field[1])
	}
	add(locales.SysUptime(), desktopSetUptime(info.Uptime))
	add(locales.SysCPU(), strings.TrimSpace(info.CPUModel+" "+coresText(info)))
	if info.MemTotal > 0 {
		add(locales.SysMemory(), formatSize(int64(info.MemTotal)))
	}
	if page.showIPs {
		add(locales.DesktopSetPublicIP(), desktopSetPublic(session, page.public))
		add(locales.DesktopSetPrivateIP(), desktopSetAddrs(page, info))
	} else {
		add(locales.DesktopSetAddresses(), locales.DesktopSetIPHidden())
	}
	for _, mount := range desktopSetMounts(session, page) {
		add(desktopSetMountName(mount), desktopSetMountSpace(mount))
	}
	return text.String()
}

func desktopSetDistro(id string) (string, ui.Color) {
	switch id {
	case "ubuntu":
		return "U", ui.Hex("#c8431a")
	case "debian":
		return "D", ui.Hex("#b51148")
	case "centos":
		return "C", ui.Hex("#8055af")
	case "rhel":
		return "R", ui.Hex("#c1242d")
	case "rocky":
		return "R", ui.Hex("#168457")
	case "almalinux":
		return "A", ui.Hex("#356791")
	case "fedora":
		return "F", ui.Hex("#2b5ca5")
	case "arch":
		return "A", ui.Hex("#1277a4")
	case "alpine":
		return "A", ui.Hex("#14536c")
	case "opensuse", "opensuse-leap", "opensuse-tumbleweed":
		return "S", ui.Hex("#53771d")
	case "openeuler":
		return "E", ui.Hex("#5e42ba")
	case "deepin", "uos":
		return "D", ui.Hex("#1876bd")
	default:
		return "", ui.Hex("#52637a")
	}
}
