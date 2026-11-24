package app

import "github.com/egoist/mygo/ui"

// serverSecurityPage is the state of the System tab's logins and security section.
type serverSecurityPage struct{}

// refreshServerSecurity reads again what the section reads.
func (a *App) refreshServerSecurity(s *Session) {}

// serverSecuritySection is the System tab's logins and security.
func (a *App) serverSecuritySection(c *ui.Context, s *Session) { serverSectionTodo(c) }
