package app

import "github.com/egoist/mygo/ui"

// dockerResourcesPage is the state of the Docker tab's networks, volumes
// and system sections.
type dockerResourcesPage struct{}

// refreshDockerResources reads again what the sections read of their own.
func (a *App) refreshDockerResources(s *Session) {}

// dockerNetworksSection is the Docker tab's networks.
func (a *App) dockerNetworksSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }

// dockerVolumesSection is the Docker tab's volumes.
func (a *App) dockerVolumesSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }

// dockerSystemSection is the Docker tab's engine: its information, disk
// use, cleanup, events and registries.
func (a *App) dockerSystemSection(c *ui.Context, s *Session) { dockerSectionTodo(c) }
