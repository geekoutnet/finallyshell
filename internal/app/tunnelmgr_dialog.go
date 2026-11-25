package app

import (
	"strconv"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) tunnelManagerDialogs(c *ui.Context) {
	view := &a.tmgr.view
	switch {
	case view.ask != nil:
		a.tunnelManagerConfirm(c, view.ask)
	case view.log != nil:
		a.tunnelManagerLog(c, *view.log)
	case view.chooseHost:
		a.tunnelManagerChooseHost(c)
	case view.autostart != nil:
		a.tunnelManagerAutostart(c, *view.autostart)
	}
}

func (a *App) tunnelManagerConfirm(c *ui.Context, question *tunnelManagerQuestion) {
	view := &a.tmgr.view
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(520).Gap(10).Children(func() {
			ui.Text(c, locales.TmgrConfirmAction(question.action.label(), len(question.rows))).Bold().FontSize(16)
			switch question.action {
			case tunnelActionStart:
				widgets.Muted(c, locales.TmgrStartNote())
			case tunnelActionStartBackground:
				widgets.Muted(c, locales.TmgrStartBackgroundNote())
			case tunnelActionStop, tunnelActionRestart:
				widgets.Muted(c, locales.TmgrDisconnectNote())
			case tunnelActionEnableBackground, tunnelActionDisableBackground:
				widgets.Muted(c, locales.TmgrBackgroundNote())
			}
			ui.Scroll(c).MaxHeight(230).Padding(9).Gap(8).Radius(5).Background(colors.note).Children(func() {
				for _, row := range question.rows {
					ui.Column(c.Key(row.key)).Gap(3).FillWidth().Children(func() {
						label := row.host + " · " + row.name() + " · " + row.bind()
						ui.Text(c, label).MaxLines(2).Tooltip(label)
						if question.action != tunnelActionStop && question.action != tunnelActionDisableBackground && !config.IsLoopback(row.spec.ListenHost) {
							warning := locales.TmgrPublicLocal(row.spec.ListenHost)
							if row.spec.Kind.IsRemote() {
								warning = locales.TmgrPublicRemote(row.spec.ListenHost)
							}
							ui.Text(c, warning).TextColor(theme.Warning).FontSize(12)
							if row.spec.Kind.IsProxy() && row.spec.ProxyUser == "" {
								ui.Text(c, locales.TmgrPublicProxy()).TextColor(theme.Danger).Bold().FontSize(12)
							}
						}
					})
				}
			})
			tunnelManagerConfirmButtons(c, &open, func() { a.applyTunnelManager(question) })
		})
	})
	if !open {
		view.ask = nil
	}
}

func tunnelManagerConfirmButtons(c *ui.Context, open *bool, confirm func()) {
	ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
		if ui.Button(c, locales.Cancel()).AutoFocus().Clicked() {
			*open = false
		}
		if ui.PrimaryButton(c, locales.TmgrConfirm()).Clicked() {
			*open = false
			confirm()
		}
	})
}

func (a *App) tunnelManagerLog(c *ui.Context, row tunnelManagerRow) {
	if host, spec := a.tunnelSpec(row.key); spec != nil {
		row = a.tunnelManagerRow(host, *spec)
	}
	if row.err == nil {
		row.err = row.lastErr
	}
	detail := row.errorText()
	if detail == "" {
		detail = locales.TmgrNoError()
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(500).Gap(10).Children(func() {
			ui.Text(c, locales.TmgrLog()).Bold().FontSize(16)
			widgets.Muted(c, row.host+" · "+row.name()+" · "+row.bind())
			ui.Text(c, locales.TmgrLastError()).Bold()
			ui.Scroll(c).MaxHeight(180).Padding(9).Background(colors.note).Radius(5).Children(func() {
				ui.Text(c, detail).FillWidth()
			})
			if !row.retryAt.IsZero() {
				widgets.Muted(c, locales.TmgrNextRetry(row.retryAt.Local().Format(time.TimeOnly), strconv.Itoa(row.attempt)))
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Close()).AutoFocus().Clicked() {
					open = false
				}
			})
		})
	})
	if !open {
		a.tmgr.view.log = nil
	}
}

func (a *App) tunnelManagerChooseHost(c *ui.Context) {
	view := &a.tmgr.view
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(420).Gap(10).Children(func() {
			ui.Text(c, locales.TmgrChooseHost()).Bold().FontSize(16)
			ui.Scroll(c).MaxHeight(250).Gap(5).Children(func() {
				for _, host := range a.hosts.Live() {
					if tunnelManagerHost(host) && ui.Button(c.Key(host.ID), tunnelHostLabel(host)).FillWidth().Clicked() {
						open = false
						a.editTunnelFromManager(host.ID, "")
					}
				}
			})
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
			})
		})
	})
	view.chooseHost = open
}

func (a *App) tunnelManagerAutostart(c *ui.Context, enabled bool) {
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(440).Gap(10).Children(func() {
			text := locales.TmgrAutostartOff()
			if enabled {
				text = locales.TmgrAutostartOn()
			}
			ui.Text(c, text).Bold().FontSize(16)
			widgets.Muted(c, locales.TmgrBackgroundNote())
			tunnelManagerConfirmButtons(c, &open, func() { a.setTunnelAutostart(enabled) })
		})
	})
	if !open {
		a.tmgr.view.autostart = nil
	}
}
