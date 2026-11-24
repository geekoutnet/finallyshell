package app

import "github.com/egoist/mygo/ui"

// serverUpdatesPage is the state of the System tab's package updates section.
type serverUpdatesPage struct{}

// refreshServerUpdates reads again what the section reads.
func (a *App) refreshServerUpdates(s *Session) {}

// serverUpdatesSection is the System tab's package updates.
func (a *App) serverUpdatesSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
