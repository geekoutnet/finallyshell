package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The desktop's task manager: performance and processes.

func init() {
	registerDesktopApp(desktopApp{
		id: appTaskMgr, icon: "activity", tint: ui.Hex("#22a06b"), name: locales.DesktopAppTaskManager,
		category: desktopCatSystem, order: 60, onDesktop: true, pinned: true,
		size: desktopSize{W: 980, H: 620}, single: true, view: desktopPlaceholder,
	})
}
