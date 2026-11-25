package app

import "github.com/egoist/mygo/ui"

// caddyCertsPage is the state of the Caddy tab's certs section.
type caddyCertsPage struct{}

// refreshCaddyCerts reads again what the section reads.
func (a *App) refreshCaddyCerts(s *Session) {}

// caddyCertsSection is the Caddy tab's certs section.
func (a *App) caddyCertsSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
