package app

import (
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) tunnelRecipeImport(c *ui.Context, view *tunnelRecipeView) {
	problem := locales.TrecPreviewFirst()
	ui.Scroll(c.Key("import")).TrackScroll(&view.scroll).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(8).Padding(0, 5, 2, 0).FillWidth().Children(func() {
			widgets.Muted(c, locales.TrecImportIntro()).FontSize(12).FillWidth()
			if ui.TextArea(c.Key("input"), &view.input).Label(locales.TrecSSHText()).Placeholder("ssh -N -L 13306:localhost:3306 user@server").MaxLength(32768).Height(90).FillWidth().Changed() {
				view.imported, view.problem, view.accepted = config.SSHForwards{}, "", false
			}
			if ui.Button(c, locales.TrecPreview()).Clicked() {
				view.imported, view.problem = a.parseTunnelRecipeImport(view)
				view.accepted = false
				if view.problem == "" {
					view.input = importedForwardPreview(view.imported)
				}
			}
			if view.problem != "" {
				problem = view.problem
				return
			}
			if len(view.imported.Tunnels) == 0 {
				return
			}
			problem = ""
			if view.imported.ExitOnForwardFailure != nil {
				widgets.Muted(c, locales.TrecExitIgnored()).FillWidth()
			}
			public := false
			for index, tunnel := range view.imported.Tunnels {
				ui.Column(c.Key(strconv.Itoa(index))).FillWidth().Gap(3).Padding(7).Border(1, theme.Border).Radius(5).Children(func() {
					ui.Text(c, recipeKindLabel(tunnel)+" · "+tunnel.ListenAddr()).Bold().FillWidth()
					if target := tunnel.TargetAddr(); target != "" {
						widgets.Muted(c, target).FillWidth()
					} else if tunnel.TargetSocket != "" {
						widgets.Muted(c, tunnel.TargetSocket).FillWidth()
					}
					if !config.IsLoopback(tunnel.ListenHost) {
						public = true
						recipeWarning(c, recipeExposure(tunnel))
					}
				})
			}
			if public {
				ui.Checkbox(c, &view.accepted, locales.TrecAcceptAccess())
				if !view.accepted {
					problem = locales.TrecAcceptAccess()
				}
			}
		})
	})
	a.recipeFooter(c, view, problem, view.imported.Tunnels)
}

func importedForwardPreview(result config.SSHForwards) string {
	text := strings.TrimSuffix(config.FormatSSHConfig(result.Tunnels), "\nExitOnForwardFailure yes")
	if result.ExitOnForwardFailure != nil {
		value := "no"
		if *result.ExitOnForwardFailure {
			value = "yes"
		}
		text += "\nExitOnForwardFailure " + value
	}
	return text
}

func (a *App) parseTunnelRecipeImport(view *tunnelRecipeView) (config.SSHForwards, string) {
	result, err := config.ParseSSHForwards(view.input)
	if err != nil {
		return config.SSHForwards{}, locales.TrecInvalidSSH()
	}
	if len(result.Tunnels) == 0 {
		return config.SSHForwards{}, locales.TrecNoForwards()
	}
	existing := a.recipeExisting(view.editor)
	for index, tunnel := range result.Tunnels {
		if recipePortInUse(tunnel, existing) || recipePortInUse(tunnel, result.Tunnels[:index]) {
			return config.SSHForwards{}, locales.ErrTunnelDuplicate()
		}
	}
	result.Tunnels = slices.Clip(result.Tunnels)
	return result, ""
}
