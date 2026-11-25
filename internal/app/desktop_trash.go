package app

import (
	"github.com/egoist/mygo/ui"

	"finallyshell/locales"
)

// The desktop's trash: deleting from the desktop's file manager moves
// files there, and the Trash app restores them or deletes them for good.

func init() {
	registerDesktopApp(desktopApp{
		id: appTrash, icon: "trash-2", tint: ui.Hex("#8a94a6"), name: locales.DesktopAppTrash,
		category: desktopCatSystem, order: 40, onDesktop: true, pinned: true,
		size: desktopSize{W: 900, H: 540}, single: true, view: desktopPlaceholder,
	})
}

// desktopTrash moves the remote paths of s's connection to the trash,
// off the UI thread, and calls done on the UI thread with the error, nil
// when every one moved. The file manager calls it to delete.
func (a *App) desktopTrash(s *Session, paths []string, done func(error)) {
	done(errDesktopNotImplemented)
}
