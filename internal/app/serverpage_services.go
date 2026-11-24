package app

import "github.com/egoist/mygo/ui"

// serverServicesPage is the state of the System tab's services section.
type serverServicesPage struct{}

// refreshServerServices reads again what the section reads.
func (a *App) refreshServerServices(s *Session) {}

// serverServicesSection is the System tab's services.
func (a *App) serverServicesSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
