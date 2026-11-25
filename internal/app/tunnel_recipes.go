package app

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

const (
	windowTunnelRecipes WindowKind = "tunnel-recipes"
	windowTunnelImport  WindowKind = "tunnel-recipes-import"
)

type tunnelRecipeView struct {
	editor       *connEditor
	page         string
	importWindow bool
	group        string
	recipe       *tunnelRecipe
	form         tunnelRecipeForm
	upstream     []string
	imported     config.SSHForwards
	input        string
	problem      string
	accepted     bool
	acceptedSpec string
	send         bool
	scroll       ui.ScrollState
	canListen    func(string) bool
	lan          func() []string
	detect       func() []string
}

func (a *App) openTunnelRecipes(ed *connEditor) {
	if ed == nil {
		return
	}
	view := &tunnelRecipeView{editor: ed, group: "0", canListen: recipeCanListen,
		lan:    func() []string { return recipeLAN(recipeInterfaces()) },
		detect: func() []string { return recipeUpstreams(recipeListening) },
	}
	a.openWindow(WindowSpec{Kind: windowTunnelRecipes, Title: locales.TrecTitle, Parent: WindowEditor, Modal: true,
		Width: 770, Height: 720, MinWidth: 430, MinHeight: 300}, func(c *ui.Context) { a.tunnelRecipeWindow(c, view) })
}

func recipeListening(addr string) bool {
	conn, err := net.DialTimeout("tcp4", addr, 160*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (a *App) recipeExisting(ed *connEditor) []config.Tunnel {
	existing := slices.Clone(ed.tunnels.list)
	for _, host := range a.hosts.Live() {
		if host.ID != ed.id {
			for _, tunnel := range host.Tunnels {
				if !tunnel.Kind.IsRemote() {
					existing = append(existing, tunnel)
				}
			}
		}
	}
	return existing
}

func (view *tunnelRecipeView) goTo(page string) {
	view.page, view.problem, view.accepted, view.acceptedSpec, view.send = page, "", false, "", false
	view.scroll.Y = 0
}

func (a *App) chooseTunnelRecipe(view *tunnelRecipeView, recipe *tunnelRecipe) {
	var lan []string
	if recipe.lan {
		lan = view.lan()
	}
	tunnel := recipe.draft(a.recipeExisting(view.editor), view.canListen, lan)
	tunnel.Name = recipe.title()
	view.recipe, view.form, view.upstream = recipe, newTunnelRecipeForm(tunnel), nil
	view.goTo("review")
	if tunnel.Kind.IsRemote() && tunnel.Kind.IsProxy() && recipe.id != "reverse-lan" {
		go func() {
			found := view.detect()
			a.post(func() {
				if view.recipe == recipe && view.page == "review" {
					view.upstream = found
					a.redraw()
				}
			})
		}()
	}
}

func (a *App) tunnelRecipeWindow(c *ui.Context, view *tunnelRecipeView) {
	ui.Column(c).Fill().Padding(14).Gap(10).Children(func() {
		if view.page == "" {
			a.tunnelRecipeGallery(c, view)
			return
		}
		title := locales.TrecImport()
		if view.page == "review" {
			title = view.recipe.title()
		}
		ui.Text(c, title).FontSize(17).Bold().FillWidth().Shrink(0)
		if view.page == "review" {
			a.tunnelRecipeReview(c, view)
		} else {
			a.tunnelRecipeImport(c, view)
		}
	})
}

func (a *App) tunnelRecipeGallery(c *ui.Context, view *tunnelRecipeView) {
	host := strings.TrimSpace(view.editor.name)
	if host == "" {
		host = strings.TrimSpace(view.editor.addr)
	}
	if host == "" {
		host = locales.TrecNewHost()
	}
	ui.Text(c, locales.TrecForHost(host)).FontSize(17).Bold().SingleLine().MinWidth(0).Tooltip(host)
	widgets.Muted(c, locales.TrecIntro()).FontSize(12).FillWidth()
	ui.Row(c).Gap(6).Wrap().Shrink(0).Children(func() {
		if ui.Button(c, locales.TrecImport()).Clicked() {
			a.openTunnelRecipeImport(view.editor)
		}
		host, reason := recipeExportHost(view.editor)
		if ui.Button(c, locales.TrecCopyCommand()).Disabled(reason != "").Tooltip(reason).Clicked() && reason == "" {
			c.WriteClipboard(config.FormatSSHCommand(&host))
		}
		empty := ""
		if len(host.Tunnels) == 0 {
			empty = locales.TunnelsEmpty()
		}
		if ui.Button(c, locales.TrecCopyConfig()).Disabled(empty != "").Tooltip(empty).Clicked() && empty == "" {
			c.WriteClipboard(config.FormatSSHConfig(host.Tunnels))
		}
	})
	width, _ := c.Size()
	ui.Row(c).Gap(12).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c.Key("groups")).Width(min(198, width*0.36)).FillHeight().MinHeight(0).Children(func() {
			ui.Column(c).Gap(5).FillWidth().Children(func() {
				for index, label := range tunnelRecipeGroups {
					id := strconv.Itoa(index)
					button := ui.Button(c.Key(id), label()).TextAlign(ui.Start).FillWidth().Tooltip(label())
					if view.group == id {
						button.Background(theme.Accent.Alpha(0.18))
					}
					if button.Clicked() {
						view.group, view.scroll.Y = id, 0
					}
				}
			})
		})
		ui.Scroll(c.Key(view.group)).TrackScroll(&view.scroll).Grow(1).MinWidth(0).FillHeight().MinHeight(0).Children(func() {
			ui.Column(c).Gap(8).Padding(0, 4, 0, 0).FillWidth().Children(func() {
				for index := range tunnelRecipes {
					recipe := &tunnelRecipes[index]
					if view.group != strconv.Itoa(recipe.group) {
						continue
					}
					ui.Column(c.Key(recipe.id)).Padding(7).Gap(5).FillWidth().Border(1, theme.Border).Radius(6).Children(func() {
						if ui.Button(c, recipe.title()).TextAlign(ui.Start).FillWidth().Clicked() {
							a.chooseTunnelRecipe(view, recipe)
						}
						widgets.Muted(c, recipe.summary()).FontSize(12).FillWidth()
					})
				}
			})
		})
	})
}

func recipeExportHost(ed *connEditor) (config.Host, string) {
	port, valid := validPort(ed.port)
	host := config.Host{Addr: strings.TrimSpace(ed.addr), User: strings.TrimSpace(ed.user), Port: port, Tunnels: ed.tunnels.list}
	if ed.proxy.kind != "" {
		host.Proxy = &config.ProxyConfig{Kind: ed.proxy.kind}
	}
	switch {
	case len(host.Tunnels) == 0:
		return host, locales.TunnelsEmpty()
	case !valid || !validHost(host.Addr):
		return host, locales.TrecExportHostRequired()
	default:
		return host, ""
	}
}

func recipeKindLabel(tunnel config.Tunnel) string {
	switch tunnel.Kind {
	case config.TunnelDynamic:
		if tunnel.Mixed {
			return locales.TrecMixed()
		}
		return locales.TrecSOCKS()
	case config.TunnelHTTP:
		if tunnel.Mixed {
			return locales.TrecMixed()
		}
		return locales.TrecHTTP()
	case config.TunnelRemoteDynamic:
		return locales.TrecReverseSOCKS()
	case config.TunnelRemoteHTTP:
		return locales.TrecReverseHTTP()
	default:
		return tunnelKindLabel(tunnel.Kind)
	}
}

func (a *App) openPaletteTunnelRecipes(host *config.Host) {
	if host == nil || !host.IsSSH() {
		return
	}
	a.editTunnels(host)
	if ed := a.editor; ed != nil && ed.id == host.ID && ed.kind == config.KindSSH {
		ed.nav.Page = editorTunnels
		a.openTunnelRecipes(ed)
	}
}

func (a *App) addRecipeTunnels(view *tunnelRecipeView, tunnels []config.Tunnel) {
	for index := range tunnels {
		tunnels[index].ID = config.NewTunnelID()
	}
	ed := &view.editor.tunnels
	ed.list = append(ed.list, tunnels...)
	ed.row = len(ed.list) - 1
	if view.importWindow {
		a.closeWindow(windowTunnelImport)
	}
	a.closeWindow(windowTunnelRecipes)
	a.redraw()
}

func (a *App) openTunnelRecipeImport(editor *connEditor) {
	view := &tunnelRecipeView{editor: editor, page: "import", importWindow: true}
	a.openWindow(WindowSpec{Kind: windowTunnelImport, Title: locales.TrecImport, Parent: windowTunnelRecipes, Modal: true,
		Width: 680, Height: 450, MinWidth: 430, MinHeight: 300}, func(c *ui.Context) { a.tunnelRecipeWindow(c, view) })
}
