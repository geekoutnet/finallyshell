package app

import "github.com/egoist/mygo/ui"

// serverFirewallPage is the state of the System tab's firewall section.
type serverFirewallPage struct{}

// refreshServerFirewall reads again what the section reads.
func (a *App) refreshServerFirewall(s *Session) {}

// serverFirewallSection is the System tab's firewall.
func (a *App) serverFirewallSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
