package app

import "github.com/egoist/mygo/ui"

// caddyConfigPage is the state of the Caddy tab's config section.
type caddyConfigPage struct{}

// refreshCaddyConfig reads again what the section reads.
func (a *App) refreshCaddyConfig(s *Session) {}

// caddyConfigSection is the Caddy tab's config section.
func (a *App) caddyConfigSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
