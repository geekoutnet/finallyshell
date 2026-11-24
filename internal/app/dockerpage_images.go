package app

import "github.com/egoist/mygo/ui"

// dockerImagesPage is the state of the Docker tab's images.
type dockerImagesPage struct{}

// refreshDockerImages reads again what the section reads of its own.
func (a *App) refreshDockerImages(s *Session) {}

// dockerImagesSection is the Docker tab's images.
func (a *App) dockerImagesSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }
