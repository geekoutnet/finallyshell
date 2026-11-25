package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The desktop's terminal, browser and app center, and the session's tools
// (Docker, databases, the System tab's sections…) as desktop apps.

func init() {
	registerDesktopApp(desktopApp{
		id: appTerminal, icon: "square-terminal", tint: ui.Hex("#1f2937"), name: locales.DesktopAppTerminal,
		category: desktopCatSystem, order: 30, onDesktop: true, pinned: true,
		size: desktopSize{W: 900, H: 540}, view: desktopPlaceholder,
	})
	registerDesktopApp(desktopApp{
		id: appBrowser, icon: "globe", tint: ui.Hex("#2563eb"), name: locales.DesktopAppBrowser,
		category: desktopCatTools, order: 70, onDesktop: true,
		size: desktopSize{W: 720, H: 460}, single: true, view: desktopPlaceholder,
	})
	registerDesktopApp(desktopApp{
		id: appAppCenter, icon: "layout-grid", tint: ui.Hex("#7c3aed"), name: locales.DesktopAppAppCenter,
		category: desktopCatSystem, order: 90, onDesktop: true,
		size: desktopSize{W: 980, H: 620}, single: true, view: desktopPlaceholder,
	})
}

// desktopOpenTerminalAt opens a terminal window on s's desktop whose shell
// starts in the remote folder dir. The file manager's "Terminal" button
// calls it.
func (a *App) desktopOpenTerminalAt(s *Session, dir string) {
	a.desktopOpen(s, appTerminal, dir)
}
