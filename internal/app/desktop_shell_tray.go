package app

import (
	"fmt"
	"strings"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The dock's tray, after its apps, as the panels of Linux desktops have
// them (KDE Plasma's, Deepin's in its efficient mode): the server's load
// at a glance, the date and time with a calendar, and the power menu,
// which restarts or shuts down the server once asked.

// desktopRebootScript and desktopShutdownScript restart and shut down the
// server, with root's rights: in two seconds, in the background, so that
// the command returns before the connection drops; whichever of systemd,
// shutdown or BusyBox the server has.
const (
	desktopRebootScript   = "sync\n(sleep 2; systemctl reboot 2>/dev/null || shutdown -r now 2>/dev/null || reboot) >/dev/null 2>&1 </dev/null &\necho restarting\n"
	desktopShutdownScript = "sync\n(sleep 2; systemctl poweroff 2>/dev/null || shutdown -h now 2>/dev/null || poweroff) >/dev/null 2>&1 </dev/null &\necho shutting down\n"
)

// desktopTrayWidth is the room the tray takes in the dock beside the
// host's button, by the desktop's width: the load shows on a wide one,
// the date under the time on a middling one.
func desktopTrayWidth(width float32) float32 {
	room := float32(37 + 8 + 46) // power, gap, time
	if width >= 700 {
		room += 30 // the date
	}
	if width >= 900 {
		room += 112 // the load
	}
	return room
}

// desktopDockTray draws the tray: the load, the clock and the power
// button.
func (a *App) desktopDockTray(c *ui.Context, d *desktopState, s *Session) {
	if d.area.W >= 900 {
		a.desktopDockLoad(c, d, s)
	}
	a.desktopDockClock(c, d, s)
}

// desktopClosePopups closes the dock's popups but keep, the one opening.
func desktopClosePopups(sh *desktopShellState, keep *bool) {
	open := *keep
	sh.launcher, sh.dockOverflow, sh.dockPopup, sh.hostPopup = false, false, "", false
	sh.trayPopup, sh.clockPopup, sh.powerPopup = false, false, false
	*keep = open
}

func desktopTrayButton(c *ui.Context, key, label string) ui.Element {
	button := ui.Row(c.Key(key)).Height(36).Shrink(0).PaddingX(6).Gap(6).Radius(7).AlignItems(ui.Center).
		Role(ui.RoleButton).Label(label).Tooltip(label)
	if button.Hovered() {
		button.Background(theme.Text.Alpha(.09))
	}
	return button
}

func (a *App) desktopDockLoad(c *ui.Context, d *desktopState, s *Session) {
	sh, st := &d.shell, &s.stats
	button := desktopTrayButton(c, "dock-load", locales.DesktopTray()).Width(104)
	if button.Clicked() {
		sh.trayPopup = !sh.trayPopup
		desktopClosePopups(sh, &sh.trayPopup)
		c.Invalidate()
	}
	mem := 0.0
	if st.MemTotal > 0 {
		mem = ratio(st.MemUsed, st.MemTotal) * 100
	}
	button.Children(func() {
		for _, item := range []struct {
			icon  string
			value float64
		}{{"cpu", st.CPU}, {"memory-stick", mem}} {
			ui.Row(c.Key(item.icon)).Gap(3).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get(item.icon)).FontSize(13).TextColor(usageColor(item.value / 100))
				text := "-"
				if st.OK {
					text = fmt.Sprintf("%.0f%%", item.value)
				}
				ui.Text(c, text).FontSize(11).Bold().TextColor(theme.Text).SingleLine()
			})
		}
	})
	a.desktopDockPopover(c.Key("desktop-load"), button, &sh.trayPopup, func() {
		ui.Text(c, locales.DesktopTray()).Bold().FontSize(15)
		if !st.OK {
			ui.Text(c, locales.DesktopInfoUnavailable()).FontSize(12).TextColor(theme.TextMuted)
		} else {
			bar := func(label string, frac float64, value string) {
				ui.Column(c.Key("bar:" + label)).FillWidth().Gap(3).Children(func() {
					ui.Row(c).FillWidth().Children(func() {
						ui.Text(c, label).FontSize(12).Grow(1)
						ui.Text(c, value).FontSize(12).TextColor(theme.TextMuted)
					})
					usageBar(c, frac)
				})
			}
			bar(locales.DesktopInfoCPU(), st.CPU/100, fmt.Sprintf("%.0f%%", st.CPU))
			if st.MemTotal > 0 {
				bar(locales.DesktopInfoMemory(), ratio(st.MemUsed, st.MemTotal), usage(st.MemUsed, st.MemTotal))
			}
			if st.SwapTotal > 0 {
				bar(locales.DesktopInfoSwap(), ratio(st.SwapUsed, st.SwapTotal), usage(st.SwapUsed, st.SwapTotal))
			}
			for _, disk := range st.Disks {
				if disk.Mount == "/" && disk.Size > 0 {
					used := disk.Size - min(disk.Avail, disk.Size)
					bar(locales.DesktopInfoRoot(), ratio(used, disk.Size), usage(used, disk.Size))
				}
			}
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				for _, rate := range []struct {
					icon, label string
					value       float64
				}{{"arrow-down", locales.DesktopTrayDown(), st.RxRate}, {"arrow-up", locales.DesktopTrayUp(), st.TxRate}} {
					ui.Row(c.Key(rate.icon)).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons.Get(rate.icon)).FontSize(13).TextColor(theme.TextMuted)
						ui.Text(c, formatBytes(rate.value)+"/s").FontSize(12).Label(rate.label)
					})
				}
			})
			ui.Text(c, locales.DesktopInfoUptime()+"  "+uptimeText(st.Uptime)).FontSize(12).TextColor(theme.TextMuted)
		}
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		if desktopPanelRow(c, "activity", locales.DesktopAppTaskManager(), false).Clicked() {
			sh.trayPopup = false
			a.desktopOpen(s, appTaskMgr, nil)
		}
	})
}

func (a *App) desktopDockClock(c *ui.Context, d *desktopState, s *Session) {
	sh := &d.shell
	now := c.Now()
	button := desktopTrayButton(c, "dock-clock", locales.DesktopClock())
	if button.Clicked() {
		sh.clockPopup = !sh.clockPopup
		desktopClosePopups(sh, &sh.clockPopup)
		sh.clockMonth = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		c.Invalidate()
	}
	button.Children(func() {
		ui.Column(c).AlignItems(ui.End).Children(func() {
			ui.Text(c, now.Format("15:04")).FontSize(12).Bold().TextColor(theme.Text)
			if d.area.W >= 700 {
				ui.Text(c, now.Format("2006/01/02")).FontSize(10).TextColor(theme.Text.Alpha(.72))
			}
		})
	})
	if sh.clockPopup {
		c.After(time.Second - time.Duration(now.Nanosecond()))
	}
	a.desktopDockPopover(c.Key("desktop-clock"), button, &sh.clockPopup, func() {
		ui.Text(c, now.Format("15:04:05")).FontSize(26).Bold()
		weekdays := strings.Split(locales.DesktopClockWeekdaysLong(), ",")
		day := now.Format("2006-01-02")
		if len(weekdays) == 7 {
			day += "  " + weekdays[now.Weekday()]
		}
		ui.Text(c, day).FontSize(12).TextColor(theme.TextMuted)
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		a.desktopCalendar(c, sh, now)
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		if desktopPanelRow(c, "clock", locales.DesktopClockSettings(), false).Clicked() {
			sh.clockPopup = false
			a.desktopOpen(s, appSettings, "time")
		}
	})
}

// desktopCalendar is the clock's month, Monday first, with today marked.
func (a *App) desktopCalendar(c *ui.Context, sh *desktopShellState, now time.Time) {
	month := sh.clockMonth
	if month.IsZero() {
		month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
		ui.Text(c, month.Format("2006-01")).Bold().Grow(1)
		if month.Year() != now.Year() || month.Month() != now.Month() {
			if ui.Button(c, locales.DesktopClockToday()).Height(24).Clicked() {
				sh.clockMonth = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
			}
		}
		if desktopFrameButton(c, "chevron-left", locales.DesktopClockPrev()).Clicked() {
			sh.clockMonth = month.AddDate(0, -1, 0)
		}
		if desktopFrameButton(c, "chevron-right", locales.DesktopClockNext()).Clicked() {
			sh.clockMonth = month.AddDate(0, 1, 0)
		}
	})
	const cell = 29
	ui.Row(c).Children(func() {
		for _, name := range strings.Split(locales.DesktopClockWeekdays(), ",") {
			ui.Box(c.Key("wd:"+name)).Size(cell, 20).Center().Children(func() {
				ui.Text(c, name).FontSize(11).TextColor(theme.TextMuted)
			})
		}
	})
	// The first Monday on or before the month's first day.
	start := month.AddDate(0, 0, -((int(month.Weekday()) + 6) % 7))
	for week := range 6 {
		ui.Row(c.Key(fmt.Sprint("week", week))).Children(func() {
			for i := range 7 {
				day := start.AddDate(0, 0, week*7+i)
				box := ui.Box(c.Key(day.Format("0102"))).Size(cell, 24).Center().Radius(12)
				color := theme.Text
				if day.Month() != month.Month() {
					color = theme.TextMuted.Alpha(.6)
				}
				if day.Year() == now.Year() && day.YearDay() == now.YearDay() {
					box.Background(theme.Accent)
					color = ui.RGB(255, 255, 255)
				}
				box.Children(func() { ui.Text(c, fmt.Sprint(day.Day())).FontSize(12).TextColor(color) })
			}
		})
	}
}

// desktopDockPower is the power button and its menu: reconnect,
// disconnect, restart or shut down the server.
func (a *App) desktopDockPower(c *ui.Context, d *desktopState, s *Session) {
	sh := &d.shell
	button := desktopDockButton(c, locales.DesktopPower())
	if button.Clicked() {
		sh.powerPopup = !sh.powerPopup
		desktopClosePopups(sh, &sh.powerPopup)
		c.Invalidate()
	}
	button.Children(func() { ui.Icon(c, icons.Get("power")).FontSize(17).TextColor(theme.Text) })
	a.desktopDockPopover(c.Key("desktop-power"), button, &sh.powerPopup, func() {
		ui.Text(c, s.label()).Bold().FontSize(15).SingleLine().FillWidth()
		ui.Text(c, s.statusText()).FontSize(12).TextColor(theme.TextMuted)
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		if desktopPanelRow(c, "refresh-cw", locales.Reconnect(), false).Clicked() {
			sh.powerPopup = false
			a.reconnect(s)
		}
		connected := s.status == statusConnected || s.status == statusConnecting
		if desktopPanelRow(c, "unplug", locales.Disconnect(), false).Disabled(!connected).Clicked() && connected {
			sh.powerPopup = false
			a.disconnectByUser(s)
		}
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		_, can := a.opsConnection(s, &sh.power)
		reboot := desktopPanelRow(c, "rotate-ccw", locales.DesktopPowerReboot(), false).Disabled(!can)
		shutdown := desktopPanelRow(c, "power", locales.DesktopPowerShutdown(), false).Disabled(!can)
		if !can {
			ui.Text(c, locales.DesktopPowerUnavailable()).FontSize(11).TextColor(theme.TextMuted)
		}
		if reboot.Clicked() && can {
			sh.powerPopup = false
			a.desktopAskPower(d, s, true)
		}
		if shutdown.Clicked() && can {
			sh.powerPopup = false
			a.desktopAskPower(d, s, false)
		}
	})
}

// desktopAskPower asks to restart, or shut down, the server of s.
func (a *App) desktopAskPower(d *desktopState, s *Session, reboot bool) {
	name := s.label()
	dialog := &opsDialog{
		title: locales.DesktopPowerShutdownTitle(), description: locales.DesktopPowerShutdownWarning(name),
		action: locales.DesktopPowerShutdownAction(), build: opsStatic(desktopShutdownScript),
	}
	if reboot {
		dialog.title, dialog.description = locales.DesktopPowerRebootTitle(), locales.DesktopPowerRebootWarning(name)
		dialog.action, dialog.build = locales.DesktopPowerRebootAction(), opsStatic(desktopRebootScript)
	}
	d.shell.power.dialog = dialog
}
