package app

import "github.com/egoist/mygo/ui"

// caddySitesPage is the state of the Caddy tab's sites section.
type caddySitesPage struct{}

// refreshCaddySites reads again what the section reads.
func (a *App) refreshCaddySites(s *Session) {}

// caddySitesSection is the Caddy tab's sites section.
func (a *App) caddySitesSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
