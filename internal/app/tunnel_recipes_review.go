package app

import (
	"net"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) tunnelRecipesButton(c *ui.Context, ed *connEditor) {
	if ui.Button(c, locales.TrecTitle()).Clicked() {
		a.openTunnelRecipes(ed)
	}
}

func (a *App) tunnelRecipeReview(c *ui.Context, view *tunnelRecipeView) {
	form, recipe := &view.form, view.recipe
	tunnel, problem := form.result(a.recipeExisting(view.editor), recipe.lan)
	ui.Scroll(c.Key("review")).TrackScroll(&view.scroll).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(8).Padding(0, 5, 2, 0).FillWidth().Children(func() {
			widgets.Muted(c, recipe.summary()).FillWidth()
			ui.Form(c, func() {
				recipeTextField(c, locales.TrecName(), &form.tunnel.Name, "")
				recipeTextField(c, locales.FieldListenAddr(), &form.tunnel.ListenHost, config.LoopbackHost)
				recipeTextField(c, locales.FieldListenPort(), &form.listen, "")
				if !tunnel.Kind.IsProxy() {
					if recipe.socket != "" {
						recipeTextField(c, locales.TrecSocket(), &form.tunnel.TargetSocket, recipe.socket)
					} else {
						recipeTextField(c, locales.FieldTargetHost(), &form.tunnel.TargetHost, "")
						recipeTextField(c, locales.FieldTargetPort(), &form.target, "")
					}
				}
				if tunnel.Kind.IsRemote() && tunnel.Kind.IsProxy() {
					recipeTextField(c, locales.TrecUpstream(), &form.upstream, locales.TrecDirect())
				}
				if recipe.lan || !config.IsLoopback(tunnel.ListenHost) {
					recipeTextField(c, locales.TrecACL(), &form.acl, "192.168.1.0/24")
				}
			})
			if len(view.upstream) > 0 {
				widgets.Muted(c, locales.TrecDetected()).FontSize(12).FillWidth()
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for _, proxy := range view.upstream {
						if ui.Button(c.Key(proxy), locales.TrecUseProxy(proxy)).Clicked() {
							form.upstream = proxy
						}
					}
				})
			}
			a.systemProxyControls(c, &form.tunnel, &form.pac)
			tunnel, problem = form.result(a.recipeExisting(view.editor), recipe.lan)
			if recipe.id == "docker" {
				recipeWarning(c, locales.TrecDockerWarn())
			}
			if recipe.group == 4 {
				recipeWarning(c, locales.TrecGateway())
			}
			if config.IsLoopback(tunnel.ListenHost) {
				view.accepted = false
			} else {
				spec := tunnel.ListenAddr() + "|" + tunnel.TargetAddr() + "|" + tunnel.TargetSocket + "|" + form.acl
				if view.acceptedSpec != spec {
					view.accepted, view.acceptedSpec = false, spec
				}
				recipeWarning(c, recipeExposure(tunnel))
				ui.Checkbox(c, &view.accepted, locales.TrecAcceptAccess())
			}
			if problem == "" {
				a.tunnelRecipeUsage(c, view, tunnel)
			}
		})
	})
	if problem == "" && !config.IsLoopback(tunnel.ListenHost) && !view.accepted {
		problem = locales.TrecAcceptAccess()
	}
	a.recipeFooter(c, view, problem, []config.Tunnel{tunnel})
	ui.Modal(c, &view.send, func() { a.recipeSendDialog(c, view, tunnel) })
}

func recipeTextField(c *ui.Context, label string, value *string, placeholder string) {
	ui.Field(c, label, func() { ui.TextInput(c, value).Placeholder(placeholder).FillWidth() })
}

func recipeWarning(c *ui.Context, warning string) {
	ui.Text(c, warning).FillWidth().Padding(8).Radius(5).Background(theme.Warning.Alpha(0.15))
}

func recipeExposure(tunnel config.Tunnel) string {
	address := tunnel.ListenAddr()
	var message string
	if len(tunnel.AllowFrom) > 0 {
		message = locales.TrecAllowWarn(address, strings.Join(tunnel.AllowFrom, ", "))
	} else if tunnel.Kind.IsRemote() {
		message = locales.TrecRemoteWarn(address)
	} else {
		message = locales.TrecLocalWarn(address)
	}
	if tunnel.Kind.IsProxy() && tunnel.ProxyUser == "" {
		message += " " + locales.TrecNoAuthWarn()
	}
	return message
}

func (a *App) recipeFooter(c *ui.Context, view *tunnelRecipeView, problem string, tunnels []config.Tunnel) {
	if problem != "" && problem != locales.TrecPreviewFirst() {
		ui.Text(c, problem).TextColor(theme.Danger).FontSize(12).FillWidth().Shrink(0)
	}
	ui.Row(c).Gap(8).Shrink(0).FillWidth().Children(func() {
		if !view.importWindow && ui.Button(c, locales.TrecBack()).Clicked() {
			view.goTo("")
		}
		ui.Spacer(c)
		if ui.Button(c, locales.Cancel()).Clicked() {
			if view.importWindow {
				a.closeWindow(windowTunnelImport)
			} else {
				a.closeWindow(windowTunnelRecipes)
			}
		}
		if ui.PrimaryButton(c, locales.TrecAdd()).Disabled(problem != "").Tooltip(problem).Clicked() && problem == "" {
			a.addRecipeTunnels(view, tunnels)
		}
	})
}

func (a *App) recipeSession(ed *connEditor) *Session {
	session := a.current()
	if session != nil && session.panelOf != nil {
		session = session.panelOf
	}
	if session == nil || session.Host == nil || session.Host.ID != ed.id || session.client == nil || session.status != statusConnected || session.term() == nil {
		return nil
	}
	return session
}

func (a *App) tunnelRecipeUsage(c *ui.Context, view *tunnelRecipeView, tunnel config.Tunnel) {
	if tunnel.ListenPort == 0 {
		widgets.Muted(c, locales.TrecAutoPort()).FillWidth()
		return
	}
	if tunnel.Kind.IsRemote() && tunnel.Kind.IsProxy() {
		exports, apt, docker := recipeProxyCommands(tunnel)
		widgets.Muted(c, locales.TrecUseRemoteProxy()).FillWidth()
		recipeCopyLine(c, locales.TrecShell(), exports)
		disabled := a.recipeSession(view.editor) == nil
		reason := ""
		if disabled {
			reason = locales.TrecConnectFirst()
		}
		if ui.Button(c, locales.TrecSend()).Disabled(disabled).Tooltip(reason).Clicked() && !disabled {
			view.send = true
		}
		if apt != "" {
			recipeCopyLine(c, locales.TrecAPT(), apt)
			recipeCopyLine(c, locales.TrecDockerEnv(), docker)
		}
		return
	}
	address := recipeClientAddress(tunnel)
	switch view.recipe.id {
	case "docker":
		widgets.Muted(c, locales.TrecUseDocker()).FillWidth()
		recipeCopyLine(c, locales.TrecShell(), "docker -H 'tcp://"+address+"' ps\nexport DOCKER_HOST='tcp://"+address+"'")
	case "publish-web", "publish-rdp", "publish-port":
		widgets.Muted(c, locales.TrecUsePublished(strconv.Itoa(tunnel.ListenPort))).FillWidth()
	case "lan-web", "lan-proxy":
		widgets.Muted(c, locales.TrecUseLAN(strconv.Itoa(tunnel.ListenPort))).FillWidth()
	case "rdp":
		widgets.Muted(c, locales.TrecUseRDP(address)).FillWidth()
	case "ssh":
		host, port, _ := net.SplitHostPort(address)
		recipeCopyLine(c, locales.TrecShell(), "ssh -p '"+port+"' '"+host+"'")
	default:
		widgets.Muted(c, locales.TrecUseClient(address)).FillWidth()
	}
}

func recipeClientAddress(tunnel config.Tunnel) string {
	ip := net.ParseIP(tunnel.ListenHost)
	if ip != nil && ip.IsUnspecified() {
		if ip.To4() == nil {
			return net.JoinHostPort("::1", strconv.Itoa(tunnel.ListenPort))
		}
		return net.JoinHostPort(config.LoopbackHost, strconv.Itoa(tunnel.ListenPort))
	}
	return tunnel.ListenAddr()
}

func recipeCopyLine(c *ui.Context, label, content string) {
	ui.Column(c.Key(label)).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).Bold().Grow(1).MinWidth(0)
			if ui.Button(c, locales.Copy()).Clicked() {
				c.WriteClipboard(content)
			}
		})
		ui.TextArea(c, &content).ReadOnly(true).FillWidth().Height(44 + 14*float32(strings.Count(content, "\n")))
	})
}

func recipeProxyCommands(tunnel config.Tunnel) (exports, apt, docker string) {
	addr := recipeClientAddress(tunnel)
	if tunnel.Kind == config.TunnelRemoteDynamic {
		return "export all_proxy='socks5h://" + addr + "'", "", ""
	}
	proxy := "http://" + addr
	return "export http_proxy='" + proxy + "' https_proxy='" + proxy + "' all_proxy='" + proxy + "'",
		"Acquire::http::Proxy \"" + proxy + "\";\nAcquire::https::Proxy \"" + proxy + "\";",
		"[Service]\nEnvironment=\"HTTP_PROXY=" + proxy + "\"\nEnvironment=\"HTTPS_PROXY=" + proxy + "\""
}

func (a *App) recipeSendDialog(c *ui.Context, view *tunnelRecipeView, tunnel config.Tunnel) {
	ui.Column(c).Width(350).Gap(10).Children(func() {
		ui.Text(c, locales.TrecSendConfirm()).FillWidth()
		command, _, _ := recipeProxyCommands(tunnel)
		ui.Text(c, command).FillWidth()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				view.send = false
			}
			session := a.recipeSession(view.editor)
			if ui.PrimaryButton(c, locales.TrecSend()).Disabled(session == nil).Clicked() && session != nil {
				a.sendLine(session, command, false)
				view.send = false
			}
		})
	})
}
