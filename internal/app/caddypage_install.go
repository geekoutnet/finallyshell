package app

import "github.com/egoist/mygo/ui"

// caddyInstallPage is the state of the Caddy tab's install section.
type caddyInstallPage struct{}

// refreshCaddyInstall reads again what the section reads.
func (a *App) refreshCaddyInstall(s *Session) {}

// caddyInstallSection is the Caddy tab's install section.
func (a *App) caddyInstallSection(c *ui.Context, s *Session) { caddySectionTodo(c) }
