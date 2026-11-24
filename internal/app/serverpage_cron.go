package app

import "github.com/egoist/mygo/ui"

// serverCronPage is the state of the System tab's scheduled jobs section.
type serverCronPage struct{}

// refreshServerCron reads again what the section reads.
func (a *App) refreshServerCron(s *Session) {}

// serverCronSection is the System tab's scheduled jobs.
func (a *App) serverCronSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
