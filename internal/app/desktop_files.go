package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The desktop's "This PC" and "Files" apps (the file manager). Opening
// appFiles with a string arg opens that folder.

func init() {
	registerDesktopApp(desktopApp{
		id: appThisPC, icon: "monitor", tint: ui.Hex("#4f7cff"), name: locales.DesktopAppThisPC,
		category: desktopCatSystem, order: 10, onDesktop: true, pinned: true,
		size: desktopSize{W: 960, H: 600}, view: desktopPlaceholder,
	})
	registerDesktopApp(desktopApp{
		id: appFiles, icon: "folder", tint: ui.Hex("#f0a630"), name: locales.DesktopAppFiles,
		category: desktopCatSystem, order: 20, onDesktop: true, pinned: true,
		size: desktopSize{W: 960, H: 600}, view: desktopPlaceholder,
	})
}
