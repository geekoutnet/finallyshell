package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The desktop's settings: the server's information, personalization
// (desktopPersonalizePage, the shell's), the operation log, DNS and the
// time zone.

func init() {
	registerDesktopApp(desktopApp{
		id: appSettings, icon: "settings", tint: ui.Hex("#5a6578"), name: locales.DesktopAppSettings,
		category: desktopCatSystem, order: 50, onDesktop: true, pinned: true,
		size: desktopSize{W: 980, H: 620}, single: true, view: desktopPlaceholder,
	})
}
