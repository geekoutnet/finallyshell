package app

import "github.com/egoist/mygo/ui"

// caddyGlobalPage is the state of the Caddy tab's global section.
type caddyGlobalPage struct{}

// refreshCaddyGlobal reads again what the section reads.
func (a *App) refreshCaddyGlobal(s *Session) {}

// caddyGlobalSection is the Caddy tab's global section.
func (a *App) caddyGlobalSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
