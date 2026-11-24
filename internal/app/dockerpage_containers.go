package app

import "github.com/egoist/mygo/ui"

// dockerContainersPage is the state of the Docker tab's containers.
type dockerContainersPage struct{}

// refreshDockerContainers reads again what the section reads of its own.
func (a *App) refreshDockerContainers(s *Session) {}

// dockerContainersSection is the Docker tab's containers.
func (a *App) dockerContainersSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }
