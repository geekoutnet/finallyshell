package app

import "github.com/egoist/mygo/ui"

// serverLogsPage is the state of the System tab's logs section.
type serverLogsPage struct{}

// refreshServerLogs reads again what the section reads.
func (a *App) refreshServerLogs(s *Session) {}

// serverLogsSection is the System tab's logs.
func (a *App) serverLogsSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
