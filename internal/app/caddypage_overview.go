package app

import "github.com/egoist/mygo/ui"

// caddyOverviewPage is the state of the Caddy tab's overview section.
type caddyOverviewPage struct{}

// refreshCaddyOverview reads again what the section reads.
func (a *App) refreshCaddyOverview(s *Session) {}

// caddyOverviewSection is the Caddy tab's overview section.
func (a *App) caddyOverviewSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
