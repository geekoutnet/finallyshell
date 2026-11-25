package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The AI assistant (an OpenAI-compatible API the user configures):
// questions about the server, commands it writes, logs it explains.

func init() {
	registerDesktopApp(desktopApp{
		id: appAI, icon: "sparkles", tint: ui.Hex("#c026d3"), name: locales.DesktopAppAI,
		category: desktopCatTools, order: 80, onDesktop: true, pinned: true,
		size: desktopSize{W: 760, H: 600}, single: true, view: desktopPlaceholder,
	})
}
