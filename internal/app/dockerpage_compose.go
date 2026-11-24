package app

import "github.com/egoist/mygo/ui"

// dockerComposePage is the state of the Docker tab's Compose projects.
type dockerComposePage struct{}

// refreshDockerCompose reads again what the section reads of its own.
func (a *App) refreshDockerCompose(s *Session) {}

// dockerComposeSection is the Docker tab's Compose projects.
func (a *App) dockerComposeSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }
