package app

import "github.com/egoist/mygo/ui"

// caddyLogsPage is the state of the Caddy tab's logs section.
type caddyLogsPage struct{}

// refreshCaddyLogs reads again what the section reads.
func (a *App) refreshCaddyLogs(s *Session) {}

// caddyLogsSection is the Caddy tab's logs section.
func (a *App) caddyLogsSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
