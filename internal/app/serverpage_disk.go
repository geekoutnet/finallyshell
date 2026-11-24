package app

import "github.com/egoist/mygo/ui"

// serverDiskPage is the state of the System tab's disk use section.
type serverDiskPage struct{}

// refreshServerDisk reads again what the section reads.
func (a *App) refreshServerDisk(s *Session) {}

// serverDiskSection is the System tab's disk use.
func (a *App) serverDiskSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
