package app

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func (a *App) exposureView(c *ui.Context, dialog *tunnelExposure) {
	ui.Column(c).Fill().Padding(14, 18).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, dialog.host.Title()).Bold().SingleLine().Grow(1).MinWidth(0)
			if len(dialog.records) != 0 && ui.Button(c, locales.TexpFirewallRemovals()).Disabled(dialog.busy).Clicked() {
				dialog.showRecords = !dialog.showRecords
			}
			reason := ""
			if !dialog.online() {
				reason = locales.TexpOffline()
			}
			if ui.Button(c, locales.TexpRefresh()).Disabled(dialog.busy || reason != "").Tooltip(reason).Clicked() {
				a.checkTunnelExposure(dialog)
			}
		})
		if !dialog.online() {
			widgets.Muted(c, locales.TexpOffline()).FontSize(12)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(10).Padding(0, 8, 2, 0).Children(func() {
				if dialog.showRecords {
					a.exposureRecordsView(c, dialog)
					return
				}
				a.exposureBindView(c, dialog)
				a.exposureServerView(c, dialog)
				a.exposureFirewallView(c, dialog)
				exposureStep(c, locales.TexpCloud(), locales.TexpCloudHint(), theme.TextMuted, nil)
				a.exposureOutsideView(c, dialog)
			})
		})
		exposureFooter(c, dialog.busy, dialog.message, dialog.problem, func() { a.closeWindow(windowTunnelExpose) })
	})
	exposureConfirmation(c, &dialog.confirm)
}

func exposureStep(c *ui.Context, title, detail string, status ui.Color, action func()) {
	ui.Column(c).FillWidth().Gap(5).Padding(9, 11).Border(1, theme.Border).Radius(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			widgets.StatusDot(c, status)
			ui.Text(c, title).Bold().Grow(1).MinWidth(0)
			if action != nil {
				action()
			}
		})
		if detail != "" {
			widgets.Muted(c, detail).FontSize(12)
		}
	})
}

func exposureFooter(c *ui.Context, busy bool, note, problem string, close func()) {
	if problem != "" {
		ui.Text(c, problem).TextColor(theme.Danger).MaxLines(3).Tooltip(problem)
	} else if note != "" {
		widgets.Muted(c, note).MaxLines(3).Tooltip(note)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if busy {
			ui.Spinner(c).FontSize(14)
			widgets.Muted(c, locales.TexpWaiting()).Grow(1)
		} else {
			ui.Spacer(c)
		}
		if ui.Button(c, locales.Close()).Disabled(busy).Clicked() {
			close()
		}
	})
}

func exposureConfirmation(c *ui.Context, pending **exposeAction) {
	if *pending == nil {
		return
	}
	question, open := *pending, true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(405).Gap(12).Children(func() {
			ui.Text(c, question.message)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.SvcConfirm()).Clicked() {
					open = false
					question.apply()
				}
			})
		})
	})
	if !open {
		*pending = nil
	}
}

func (a *App) exposureBindView(c *ui.Context, dialog *tunnelExposure) {
	if config.IsLoopback(dialog.spec.ListenHost) {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &dialog.bind).Label(locales.TexpPublicBind()).Placeholder(locales.TexpPublicBind()).Grow(1).MinWidth(0).Disabled(dialog.busy)
			ui.TextInput(c, &dialog.port).Label(locales.TexpPublicPort()).Width(80).Disabled(dialog.busy)
			ip, port, valid := exposureBind(dialog.bind, dialog.port)
			if ui.Button(c, locales.TexpMakePublic()).Disabled(dialog.busy || !valid).Clicked() {
				updated := dialog.spec
				updated.ListenHost, updated.ListenPort = ip.String(), port
				dialog.confirm = &exposeAction{locales.TexpPublicConfirm(updated.ListenAddr(), dialog.host.Title()), func() { a.changeExposureBind(dialog, updated) }}
			}
		})
	} else {
		ui.Text(c, locales.TexpPublicWarn(dialog.spec.ListenAddr())).TextColor(theme.Warning).FontSize(12)
	}
	if dialog.spec.Kind.IsProxy() && dialog.spec.ProxyUser == "" {
		ui.Text(c, locales.TexpProxyWarn()).Bold().TextColor(theme.Danger).FontSize(12)
	}
}

func (a *App) exposureServerView(c *ui.Context, dialog *tunnelExposure) {
	checks, enabled := dialog.checks, dialog.online() && !dialog.busy
	status, detail := theme.TextMuted, locales.TexpUnchecked()
	switch {
	case checks.gatewayErr != nil:
		status, detail = theme.Danger, locales.TexpError(opsSafeOutput(checks.gatewayErr.Error(), dialog.login))
	case checks.gateway.Mode == "clientspecified":
		status, detail = theme.Success, locales.TexpGatewayClient()
	case checks.gateway.Mode == "yes":
		status, detail = theme.Warning, locales.TexpGatewayYes()
	case checks.gateway.Mode == "no":
		status, detail = theme.Warning, locales.TexpGatewayNo()
	}
	if checks.gateway.Match {
		status, detail = theme.Warning, detail+"\n"+locales.TexpGatewayMatch()
	}
	if dialog.needsReconnect {
		detail += "\n" + locales.TexpReconnect()
	}
	exposureStep(c, locales.TexpGateway(), detail, status, func() {
		if dialog.needsReconnect && dialog.session != nil {
			if ui.Button(c, locales.TexpReconnectAction()).Disabled(!enabled).Clicked() {
				dialog.confirm = &exposeAction{locales.TexpReconnectConfirm(dialog.host.Title()), func() {
					a.closeWindow(windowTunnelExpose)
					a.reconnect(dialog.session)
				}}
			}
			return
		}
		if ui.Button(c, locales.TexpAllowGateway()).Disabled(!enabled || checks.gateway.Mode != "no" || checks.gateway.Match).Clicked() {
			dialog.confirm = &exposeAction{locales.TexpGatewayConfirm(dialog.host.Title()), func() { a.exposureSetGateway(dialog) }}
		}
	})
	status, detail = theme.TextMuted, locales.TexpUnchecked()
	if checks.tool != "" {
		status, detail = theme.Success, locales.TexpRelayTool(checks.tool)
	} else if dialog.checked {
		status, detail = theme.Warning, locales.TexpRelayMissing()
	}
	_, port, bindOK := exposureBind(dialog.spec.ListenHost, strconv.Itoa(dialog.spec.ListenPort))
	relayOK := bindOK && port > 0 && len(dialog.spec.AllowFrom) == 0 && !checks.gateway.Match && checks.gateway.Mode != "yes" && checks.gateway.Mode != ""
	if dialog.spec.ServerRelay {
		if dialog.run != nil && dialog.run.err != nil {
			status, detail = theme.Danger, describeTunnelError(dialog.run.err)
		} else if dialog.run != nil && dialog.run.fwd != nil {
			status, detail = theme.Success, locales.TexpRelayRunning()
		} else {
			status, detail = theme.Warning, locales.TexpRelayConfigured()
		}
	} else if dialog.checked && !relayOK {
		status, detail = theme.Warning, locales.TexpRelayUnsupported()
	}
	exposureStep(c, locales.TexpRelay(), detail, status, func() {
		updated := dialog.spec
		if updated.ServerRelay {
			canStart := relayOK && enabled && checks.tool != "" && dialog.run != nil && sameExposureSpec(dialog.run.spec, updated) && !dialog.run.starting && dialog.run.fwd == nil
			if canStart && ui.Button(c, locales.TunnelStart()).Clicked() {
				dialog.confirm = &exposeAction{locales.TexpRelayConfirm(dialog.host.Title(), updated.ListenAddr()), func() { a.runExposureRelay(dialog.session, dialog.run, updated, checks.tool) }}
			}
			if ui.Button(c, locales.TexpRelaySwitchOff()).Disabled(dialog.busy).Clicked() {
				updated.ServerRelay = false
				dialog.confirm = &exposeAction{locales.TexpDirectConfirm(updated.ListenAddr(), dialog.host.Title()), func() { a.changeExposureBind(dialog, updated) }}
			}
		} else if ui.Button(c, locales.TexpRelayAction()).Disabled(!enabled || !relayOK || checks.tool == "").Clicked() {
			updated.ServerRelay = true
			dialog.confirm = &exposeAction{locales.TexpRelayConfirm(dialog.host.Title(), updated.ListenAddr()), func() { a.changeExposureBind(dialog, updated) }}
		}
	})
}

func (a *App) exposureSetGateway(dialog *tunnelExposure) {
	a.exposureWork(dialog, func(ctx context.Context, conn opsConnection) (string, error) {
		out, stderr, err := conn.root(ctx, monitor.GatewayPortsChangeScript, nil)
		if err != nil {
			return "", exposureCommandError(stderr, err)
		}
		return locales.TexpReconnect() + " " + strings.Join(strings.Split(strings.TrimSpace(out), "\n"), " · "), nil
	}, func(string) {
		dialog.needsReconnect = true
		dialog.checks.gateway.Mode = "clientspecified"
	})
}

func exposureCommandError(stderr string, err error) error {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return errors.New(opsTail(detail))
	}
	return err
}

func (a *App) exposureFirewallView(c *ui.Context, dialog *tunnelExposure) {
	port, fw := dialog.listenerPort(), dialog.checks.firewall
	if dialog.checkedPort != port {
		fw = exposeFirewall{}
	}
	status, detail := theme.TextMuted, locales.TexpUnchecked()
	switch {
	case port == 0:
		detail = locales.TexpLANPortPending()
	case dialog.checks.fireErr != nil:
		status, detail = theme.Danger, locales.TexpError(opsSafeOutput(dialog.checks.fireErr.Error(), dialog.login))
	case fw.inactive:
		status, detail = theme.Warning, locales.TexpFirewallInactive()
	case fw.allowed:
		status, detail = theme.Success, locales.TexpFirewallOpen(strconv.Itoa(port))
	case fw.backend != "":
		status, detail = theme.Warning, locales.TexpFirewallClosed(strconv.Itoa(port))
	case dialog.checked && dialog.checkedPort == port:
		status, detail = theme.Warning, locales.TexpFirewallManual()
	}
	old := slices.IndexFunc(dialog.records, func(record tunnelFirewallRecord) bool { return record.Port == port })
	if old >= 0 {
		detail += "\n" + locales.TexpFirewallOwned()
	}
	exposureStep(c, locales.TexpFirewall(), detail, status, func() {
		if old >= 0 {
			if ui.Button(c, locales.TexpRemoveRule()).Disabled(!dialog.online() || dialog.busy).Clicked() {
				a.confirmExposureFirewall(dialog, dialog.records[old], false)
			}
			return
		}
		valid := port > 0 && fw.backend != "" && !fw.allowed && !fw.inactive && !config.IsLoopback(dialog.spec.ListenHost)
		if port > 0 && ui.Button(c, locales.TexpAllowPort(strconv.Itoa(port))).Disabled(!valid || !dialog.online() || dialog.busy).Clicked() {
			record := tunnelFirewallRecord{HostID: dialog.host.ID, Addr: dialog.host.Addr, User: dialog.host.User, SSHPort: dialog.host.PortOr22(),
				TunnelID: dialog.spec.ID, Backend: fw.backend, Zone: fw.zone, Port: port, Tag: "Finallyshell " + config.NewTunnelID()}
			a.confirmExposureFirewall(dialog, record, true)
		}
	})
}

func (a *App) exposureRecordsView(c *ui.Context, dialog *tunnelExposure) {
	for index, record := range dialog.records {
		if record.TunnelID == "" || !slices.ContainsFunc(dialog.host.Tunnels, func(spec config.Tunnel) bool { return spec.ID == record.TunnelID }) {
			ui.Text(c.Key(index), locales.TexpFirewallOrphan(strconv.Itoa(record.Port))).FontSize(12).TextColor(theme.Warning)
		}
		exposureStep(c.Key(record.Tag), locales.TexpAllowPort(strconv.Itoa(record.Port)), exposureFirewallScope(record), theme.Warning, func() {
			if ui.Button(c, locales.TexpRemoveRule()).Disabled(!dialog.online() || dialog.busy).Clicked() {
				a.confirmExposureFirewall(dialog, record, false)
			}
		})
	}
}

func exposureFirewallScope(record tunnelFirewallRecord) string {
	if record.Backend == "ufw" {
		return locales.TexpFirewallUFW()
	}
	return "firewalld · " + record.Zone
}

func (a *App) confirmExposureFirewall(dialog *tunnelExposure, record tunnelFirewallRecord, add bool) {
	label := locales.TexpRemoveConfirm
	if add {
		label = locales.TexpAllowConfirm
	}
	dialog.confirm = &exposeAction{label(strconv.Itoa(record.Port), dialog.host.Title(), exposureFirewallScope(record)), func() {
		a.exposureWork(dialog, func(ctx context.Context, conn opsConnection) (string, error) {
			fw, err := tunnelFirewallStatus(ctx, conn, record.Port)
			if err != nil {
				return "", err
			}
			protected, err := readFirewallProtection(ctx, conn, record.SSHPort)
			if err != nil {
				return "", err
			}
			script, err := tunnelFirewallScript(record, fw, protected.ports, add)
			if err != nil {
				return "", err
			}
			if _, stderr, err := conn.root(ctx, script, nil); err != nil {
				return "", exposureCommandError(stderr, err)
			}
			if err := keepTunnelFirewallRecord(record, add); err != nil {
				return "", errors.New(locales.TexpFirewallRecordFailed(err.Error()))
			}
			if add {
				return locales.TexpFirewallSaved(), nil
			}
			return locales.TexpFirewallRemoved(), nil
		}, func(string) {
			if add {
				dialog.records = append(dialog.records, record)
			} else {
				dialog.records = slices.DeleteFunc(dialog.records, func(old tunnelFirewallRecord) bool { return old.Tag == record.Tag })
				dialog.showRecords = dialog.showRecords && len(dialog.records) > 0
			}
			a.checkTunnelExposure(dialog)
		})
	}}
}

func (a *App) exposureOutsideView(c *ui.Context, dialog *tunnelExposure) {
	port := dialog.listenerPort()
	addr, err := tunnelOutsideAddress(dialog.outside, port)
	loopback := config.IsLoopback(dialog.spec.ListenHost)
	detail, status := dialog.result, theme.TextMuted
	if loopback {
		detail = locales.TexpOutsideLoopback()
	} else if err != nil {
		detail = err.Error()
	} else if detail != "" {
		status = theme.Success
	}
	exposureStep(c, locales.TexpOutside(), detail, status, nil)
	if ui.TextInput(c, &dialog.outside).FillWidth().Label(locales.TexpOutsideHost()).Placeholder(locales.TexpOutsideHost()).Disabled(dialog.busy).Changed() {
		dialog.result = ""
	}
	if !loopback && err == nil {
		url := "http://" + addr
		ui.Text(c, url).Selectable().SingleLine().Tooltip(url)
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, locales.TexpCheckTCP()).Disabled(dialog.busy).Clicked() {
				a.probeTunnelExposure(dialog, false)
			}
			if ui.Button(c, locales.TexpCheckHTTP()).Disabled(dialog.busy).Clicked() {
				a.probeTunnelExposure(dialog, true)
			}
			if ui.Button(c, locales.Copy()).Clicked() {
				c.WriteClipboard(url)
			}
			if ui.Button(c, locales.TexpOpenBrowser()).Clicked() {
				c.OpenURL(url)
			}
		})
	}
}

func (a *App) probeTunnelExposure(dialog *tunnelExposure, http bool) {
	dialog.busy, dialog.problem, dialog.result = true, "", ""
	host, port := dialog.outside, dialog.listenerPort()
	go func() {
		status, err := tunnelOutsideProbe(dialog.ctx, host, port, http)
		a.post(func() {
			dialog.busy = false
			if err != nil {
				dialog.problem = locales.TexpError(err.Error())
			} else if http {
				dialog.result = locales.TexpOutsideHTTP(strconv.Itoa(status))
			} else {
				dialog.result = locales.TexpOutsideTCP()
			}
		})
	}()
}
