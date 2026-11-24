package app

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const composeStarter = "services:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"127.0.0.1:8080:80\"\n"

func (a *App) openComposeNew(s *Session) {
	buffer := editor.NewBuffer(composeStarter)
	buffer.SetLanguage(editor.LanguageFor("compose.yaml"))
	s.docker.compose.dialog = &composeDialog{client: s.client, create: true,
		code: &editor.State{Buffer: buffer, Font: codeFont, Label: "compose.yaml"}}
}

func (a *App) composeNewFields(c *ui.Context, s *Session, dialog *composeDialog, height float32, open *bool) {
	ui.Row(c).Gap(8).FillWidth().Children(func() {
		ui.TextInput(c, &dialog.name).Label(locales.FieldName()).Placeholder(locales.FieldName()).Disabled(dialog.checking).Grow(1).MinWidth(0)
		ui.TextInput(c, &dialog.dir).Label(locales.DockerCmpDirectory()).Placeholder(composeDefaultDir(dialog.name)).Disabled(dialog.checking).Grow(2).MinWidth(0)
	})
	if dialog.directory != nil && dialog.directory.nonEmpty {
		ui.Text(c, locales.DockerCmpDirConfirm(dialog.project.Dir)).TextColor(theme.Danger)
	}
	ui.Text(c, "compose.yaml").Bold()
	dialog.code.Theme = codeTheme()
	editor.View(c.Key("compose-yaml"), dialog.code).Height(min(210, max(92, height-420))).FillWidth()
	ui.Text(c, locales.DockerCmpEnv()).Bold()
	ui.TextArea(c, &dialog.env).Label(locales.DockerCmpEnv()).Height(54).FillWidth()
	ui.Checkbox(c, &dialog.deploy, locales.DockerCmpDeployNow())
	if dialog.validation != "" {
		ui.Text(c, dialog.validation).TextColor(theme.Danger)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if dialog.checking {
			ui.Spinner(c).Label(locales.DockerReading())
		}
		if ui.Button(c, locales.Cancel()).Clicked() {
			*open = false
		}
		label := locales.DockerCmpCreate()
		if dialog.directory != nil && dialog.directory.nonEmpty {
			label = locales.DockerCmpCreateAnyway()
		}
		if ui.PrimaryButton(c, label).Disabled(dialog.checking || composeUnavailable(s) != "").Clicked() {
			a.createComposeProject(s, dialog)
		}
	})
}

func composeDefaultDir(name string) string {
	if !monitor.ValidComposeProject(name) {
		return "/opt/compose/<name>"
	}
	return path.Join("/opt/compose", name)
}

func composeNewProject(name, dir string) (monitor.ComposeProject, string) {
	if !monitor.ValidComposeProject(name) {
		return monitor.ComposeProject{}, locales.DockerCmpInvalidName()
	}
	if dir == "" {
		dir = composeDefaultDir(name)
	}
	if !monitor.ValidComposeDirectory(dir) {
		return monitor.ComposeProject{}, locales.DockerCmpInvalidDir()
	}
	return monitor.ComposeProject{Compose: monitor.Compose{Project: name, Dir: dir, Files: []string{"compose.yaml"}}}, ""
}

func (a *App) createComposeProject(s *Session, dialog *composeDialog) {
	page := &s.docker.compose
	if dialog.client != s.client || !slices.Contains(a.sessions, s) || composeUnavailable(s) != "" || page.managedErr != nil {
		dialog.validation = locales.DockerUnavailable()
		return
	}
	project, message := composeNewProject(dialog.name, dialog.dir)
	if message != "" {
		dialog.validation = message
		return
	}
	if slices.ContainsFunc(page.projects(&s.overview.docker.d), func(existing monitor.ComposeProject) bool {
		return existing.Project == project.Project || existing.Dir == project.Dir
	}) {
		dialog.validation = locales.DockerCmpDuplicate()
		return
	}
	yaml, env := dialog.code.Buffer.String(), dialog.env
	if strings.TrimSpace(yaml) == "" || len(yaml) > 1<<20 || len(env) > 1<<20 || strings.ContainsRune(yaml, 0) || strings.ContainsRune(env, 0) {
		dialog.validation = locales.DockerCmpInvalidFiles()
		return
	}
	dialog.validation = ""
	remote := a.composeFilesOf(s)
	if dialog.directory != nil && dialog.project.Project == project.Project && dialog.project.Dir == project.Dir {
		a.writeComposeProject(s, dialog, remote, *dialog.directory, yaml, env)
		return
	}
	dialog.project = project
	dialog.directory, dialog.checking = nil, true
	ctx, cancel := context.WithTimeout(composeContext(s), dockerTimeout)
	client := s.client
	go func() {
		defer cancel()
		state, err := remote.inspect(ctx, project.Dir)
		a.post(func() {
			if page.dialog != dialog || s.client != client {
				return
			}
			dialog.checking = false
			if err != nil {
				if !errors.Is(err, monitor.ErrPrivilegeCancelled) {
					dialog.validation = composeDirectoryError(err)
				}
				return
			}
			if state.occupied {
				dialog.validation = locales.DockerCmpFilesExist()
				return
			}
			dialog.directory = &state
			if !state.nonEmpty {
				a.writeComposeProject(s, dialog, remote, state, dialog.code.Buffer.String(), dialog.env)
			}
		})
	}()
}

func (a *App) writeComposeProject(s *Session, dialog *composeDialog, remote composeFileStore, expected composeDirectory, yaml, env string) {
	page, project, client, run := &s.docker.compose, dialog.project, s.client, a.composeRunner(s)
	if composeUnavailable(s) != "" || strings.TrimSpace(yaml) == "" || len(yaml) > 1<<20 || len(env) > 1<<20 || strings.ContainsRune(yaml, 0) || strings.ContainsRune(env, 0) {
		dialog.validation = locales.DockerCmpInvalidFiles()
		return
	}
	validate, err := page.cli.Script(project.Compose, monitor.ComposeValidate, monitor.ComposeOptions{})
	if err != nil {
		dialog.validation = err.Error()
		return
	}
	deploy, err := page.cli.Script(project.Compose, monitor.ComposeUp, monitor.ComposeOptions{})
	if err != nil {
		dialog.validation = err.Error()
		return
	}
	now, registry := dialog.deploy, composeRegistryPath(s)
	registration := composeRegistration{Project: project.Project, Dir: project.Dir}
	if !expected.exists {
		registration.Marker = config.NewHostID()
	}
	registryFailed := locales.DockerCmpRegistryFailed()
	a.startComposeWork(s, dialog, true, func(ctx context.Context, report func(string)) (string, error, bool, bool) {
		privileged, written, err := createComposeFiles(ctx, remote, project.Dir, registration.Marker, expected, []byte(yaml), []byte(env))
		if !written {
			return "", err, privileged, false
		}
		if err != nil {
			registration.Marker = ""
		}
		if registryErr := updateComposeRegistry(registry, registration, false); registryErr != nil {
			return "", errors.Join(err, errors.New(registryFailed), registryErr), privileged, true
		}
		if err != nil {
			return "", err, privileged, true
		}
		output, err := run(ctx, client, validate, report)
		if err != nil || !now {
			return output, err, privileged, true
		}
		up, err := run(ctx, client, deploy, report)
		return output + up, err, privileged, true
	})
}
