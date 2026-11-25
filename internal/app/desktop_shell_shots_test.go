package app

import (
	"os"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestDesktopShellScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("set SHOT_DIR to render desktop screenshots")
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			t.Run(lang+"-"+mode, func(t *testing.T) {
				a, p, _, s := desktopShellTestApp(t)
				tt := ui.NewTester(func(c *ui.Context) { c.SetMenuPresentation(ui.PopoverMenus); a.View(c) }, 1280, 820)
				p.tt = tt
				p.SetLocale(lang)
				tt.SetDark(dark)
				tt.Frame()
				d := a.desktopOf(s)
				shot := func(name string) {
					t.Helper()
					tt.Frame()
					saveShot(t, tt, "desktop-shell-"+name+"-"+mode+"-"+lang+".png")
				}
				files := a.desktopOpen(s, appFiles, nil)
				files.title = locales.DesktopAppFiles() + "  ·  /var/log"
				desktopSetWindowRect(files, ui.Rect{X: 112, Y: 76, W: 615, H: 418})
				filesApp := *files.app
				filesApp.view = desktopShellShotFiles
				files.app = &filesApp
				monitor := a.desktopOpen(s, appTaskMgr, nil)
				desktopSetWindowRect(monitor, ui.Rect{X: 412, Y: 275, W: 493, H: 355})
				monitorApp := *monitor.app
				monitorApp.view = desktopShellShotMonitor
				monitor.app = &monitorApp
				terminal := a.desktopOpen(s, appTerminal, nil)
				desktopSetWindowRect(terminal, ui.Rect{X: 558, Y: 54, W: 425, H: 205})
				terminalApp := *terminal.app
				terminalApp.minSize = desktopSize{W: 360, H: 150}
				terminalApp.view = func(_ *App, c *ui.Context, w *desktopWindow) {
					ui.Column(c).Fill().Background(ui.Hex("#151b29")).Padding(15).Gap(6).Children(func() {
						ui.Text(c, w.session.Host.User+"@"+w.session.label()+":~$ uptime").FontSize(12).TextColor(ui.Hex("#b9ddcf"))
						ui.Text(c, c.Now().Format("15:04")+"   ·   "+locales.StatusConnected()).FontSize(12).TextColor(ui.Hex("#c5d2ee"))
					})
				}
				terminal.app = &terminalApp
				a.desktopFocus(monitor)
				shot("windows")
				a.desktopToggleLauncher(d)
				shot("launcher")
				tt.Key(0, ui.KeyEscape)
				logWindow := a.desktopOpen(s, appFiles, nil)
				logWindow.title = locales.DesktopAppFiles() + "  ·  /home"
				desktopSetWindowRect(logWindow, ui.Rect{X: 170, Y: 120, W: 555, H: 365})
				logWindow.app = &filesApp
				d.shell.dockPopup = appFiles
				shot("dock")
				for _, w := range d.windows {
					a.desktopMinimize(d, w)
				}
				d.shell.dockPopup = ""
				tt.Move(1000, 150)
				tt.RightClickAt(1000, 150)
				shot("menu")
				tt.CloseMenu()
				a.desktopNewFolder(d, s)
				id := d.shell.rename
				d.shell.renameText = locales.DesktopShellTools()
				a.desktopRenameFolder(d, s)
				a.changeDesktopIcons(s, func(h *config.DesktopHost) {
					for idx := range h.Icons {
						if h.Icons[idx].App == string(appBrowser) || h.Icons[idx].App == string(appAppCenter) || h.Icons[idx].App == string(appTerminal) {
							h.Icons[idx].Folder = id
						}
					}
				})
				d.shell.folder = id
				shot("folder")
				d.shell.folder = ""
				settings := a.desktopOpen(s, appSettings, "personalize")
				desktopSetWindowRect(settings, ui.Rect{X: 165, Y: 32, W: 680, H: 681})
				settingsApp := *settings.app
				settingsApp.view = func(a *App, c *ui.Context, _ *desktopWindow) { a.desktopPersonalizePage(c) }
				settings.app = &settingsApp
				shot("personalization")
				tt.SetSize(900, 600)
				shot("small")
			})
		}
	}
}

func desktopShellShotFiles(_ *App, c *ui.Context, _ *desktopWindow) {
	ui.Column(c).Fill().Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("folder")).TextColor(theme.Accent)
			ui.Text(c, "/   ›   var   ›   log").Grow(1).TextColor(theme.TextMuted)
			ui.Button(c, locales.Download())
			ui.Button(c, locales.Refresh())
		})
		ui.Box(c).FillWidth().Height(1).Background(theme.Border)
		for _, file := range []struct{ name, size, icon string }{
			{"journal", "248 MB", "folder"}, {"nginx", "14 MB", "folder"}, {"apt", "92 KB", "folder"},
			{"auth.log", "214 KB", "file"}, {"syslog", "384 KB", "file"}, {"kern.log", "48 KB", "file"},
		} {
			ui.Row(c.Key(file.name)).FillWidth().Height(33).PaddingX(9).Gap(9).Radius(5).AlignItems(ui.Center).Background(theme.Surface).Children(func() {
				ui.Icon(c, icons.Get(file.icon)).TextColor(theme.Accent)
				ui.Text(c, file.name).Grow(1)
				ui.Text(c, file.size).TextColor(theme.TextMuted)
			})
		}
	})
}

func desktopShellShotMonitor(_ *App, c *ui.Context, w *desktopWindow) {
	ui.Column(c).Fill().Padding(16).Gap(14).Children(func() {
		ui.Text(c, w.session.label()).Bold().FontSize(18)
		ui.Text(c, w.session.Host.User+"@"+w.session.Host.Addr).TextColor(theme.TextMuted)
		for _, meter := range []struct {
			name, figure string
			fill         float32
			tint         string
		}{
			{locales.Cpu(), "7%", .07, "#6a72e9"}, {locales.Memory(), "2.7 / 3.8 GB", .71, "#419bd0"}, {"/", "19 / 25 GB", .76, "#45bd99"},
		} {
			ui.Column(c.Key(meter.name)).FillWidth().Padding(10).Gap(8).Radius(8).Border(1, theme.Border).Background(theme.Surface).Children(func() {
				ui.Row(c).Gap(8).Children(func() { ui.Text(c, meter.name).Bold().Grow(1); ui.Text(c, meter.figure).TextColor(theme.TextMuted) })
				track := ui.Box(c).FillWidth().Height(6).Radius(3).Background(theme.Border)
				track.Draw(func(p *ui.Painter, r ui.Rect) { r.W *= meter.fill; p.Fill(r, ui.Hex(meter.tint), 3) })
			})
		}
	})
}
