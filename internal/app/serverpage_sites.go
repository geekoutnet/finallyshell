package app

import "github.com/egoist/mygo/ui"

// serverSitesPage is the state of the System tab's websites and certificates section.
type serverSitesPage struct{}

// refreshServerSites reads again what the section reads.
func (a *App) refreshServerSites(s *Session) {}

// serverSitesSection is the System tab's websites and certificates.
func (a *App) serverSitesSection(c *ui.Context, s *Session) { serverSectionTodo(c) }
