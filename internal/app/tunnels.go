package app

import (
	"errors"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// tunnelState is a session's tunnels, as the "Tunnels" tab shows them:
// those of its host when it last connected.
type tunnelState struct {
	runs      []*tunnelRun
	table     ui.ListState
	selection int
	confirm   *tunnelConfirmation
	log       *tunnelLog
}

// tunnelRun is one of a session's tunnels. The tunnel itself counts its
// connections and bytes, which the view reads as it draws.
type tunnelRun struct {
	spec     config.Tunnel
	fwd      *sshx.Tunnel // while it listens
	last     *sshx.Tunnel
	starting bool
	err      error // why it did not start, or stopped by itself
	// gen counts the starts and stops, so that a start that ends after
	// a stop, or another start, is undone.
	gen int
}

// startTunnels starts the enabled tunnels of tunnels on s's connection,
// which has just opened; the others are listed, stopped.
func (a *App) startTunnels(s *Session, tunnels []config.Tunnel) {
	s.stopTunnels()
	s.tunnels.runs = nil
	s.tunnels.selection = -1
	for _, t := range tunnels {
		if t.ID == "" {
			t.ID = config.NewTunnelID()
		}
		run := &tunnelRun{spec: t}
		s.tunnels.runs = append(s.tunnels.runs, run)
		if t.Enabled() && !t.ServerRelay {
			a.startTunnel(s, run)
		}
	}
}

// sessionTunnelRuns returns the session's tunnel definitions and live state on the UI thread.
func (s *Session) sessionTunnelRuns() []*tunnelRun { return s.tunnels.runs }

func (s *Session) tunnelByID(id string) *tunnelRun {
	for _, run := range s.sessionTunnelRuns() {
		if run.spec.ID == id {
			return run
		}
	}
	return nil
}

// startSessionTunnel starts a known stopped tunnel on the UI thread.
func (a *App) startSessionTunnel(s *Session, id string) bool {
	run := s.tunnelByID(id)
	if s.status != statusConnected || s.client == nil || run == nil || run.starting || run.fwd != nil {
		return false
	}
	a.startTunnel(s, run)
	return true
}

// stopSessionTunnel stops a known running or starting tunnel on the UI thread.
func (a *App) stopSessionTunnel(s *Session, id string) bool {
	run := s.tunnelByID(id)
	if run == nil || !run.starting && run.fwd == nil {
		return false
	}
	run.stop()
	return true
}

// startTunnel starts run on s's connection, on a goroutine: asking the
// server to listen takes a round trip.
func (a *App) startTunnel(s *Session, run *tunnelRun) {
	client := s.client
	if client == nil || run.fwd != nil || run.starting {
		return
	}
	if run.spec.ServerRelay {
		a.openTunnelExposure(s.Host, run.spec, s, run, nil, WindowMain)
		return
	}
	run.gen++
	gen, spec := run.gen, run.spec
	run.starting, run.err = true, nil
	// The tunnel tells of changes to its stats, and of stopping, from its
	// goroutines: posted, the windows draw them.
	notify := func() { a.post(run.refresh) }
	hostID := ""
	if s.Host != nil {
		hostID = s.Host.ID
	}
	a.tunnelOptions(hostID, spec, notify, func(opts sshx.TunnelOptions) {
		if run.gen != gen || s.client != client {
			return
		}
		go func() {
			fwd, err := sshx.StartTunnelWithOptions(client, spec, opts)
			a.post(func() {
				if run.gen != gen || s.client != client { // stopped meanwhile
					if fwd != nil {
						fwd.Stop()
					}
					return
				}
				run.starting, run.fwd, run.err = false, fwd, err
				if fwd != nil {
					run.last = fwd
					a.systemProxyTunnelStarted(s.Host, spec, fwd)
				}
			})
		}()
	})
}

// stop stops run, keeping it in the list.
func (run *tunnelRun) stop() {
	run.gen++
	run.starting, run.err = false, nil
	if run.fwd != nil {
		run.fwd.Stop()
		run.fwd = nil
	}
}

// refresh notes that run's tunnel stopped by itself, as when the server
// is gone.
func (run *tunnelRun) refresh() {
	if run.fwd == nil {
		return
	}
	select {
	case <-run.fwd.Done():
		run.err = run.fwd.Err()
		run.fwd = nil
	default:
	}
}

// stopTunnels stops s's tunnels, as it disconnects.
func (s *Session) stopTunnels() {
	s.tunnels.confirm = nil
	for _, run := range s.tunnels.runs {
		run.stop()
	}
}

// listening is how many of s's tunnels listen.
func (s *Session) listening() int {
	n := 0
	for _, run := range s.tunnels.runs {
		if run.fwd != nil {
			n++
		}
	}
	return n
}

// describeTunnelError says why a tunnel is not listening, in the
// language shown.
func describeTunnelError(err error) string {
	var te *sshx.TunnelError
	switch {
	case errors.Is(err, sshx.ErrUnknownTunnel):
		return locales.ErrTunnelUnknown()
	case errors.Is(err, sshx.ErrConnectionClosed):
		return locales.ErrTunnelLost()
	case errors.Is(err, sshx.ErrTunnelCredential):
		return locales.TunSecretMissing()
	case errors.Is(err, sshx.ErrTunnelAllowList):
		return locales.TunAllowError()
	case errors.Is(err, sshx.ErrTunnelSocket):
		return locales.TunSocketError()
	case errors.Is(err, sshx.ErrTunnelUpstream):
		return locales.TunUpstreamError()
	case errors.Is(err, sshx.ErrTunnelLimit):
		return locales.TunLimitError()
	case errors.Is(err, sshx.ErrTunnelTarget):
		return locales.TunInvalidTarget()
	case errors.As(err, &te):
		switch {
		case te.InUse:
			return locales.ErrTunnelInUse(te.Addr)
		case errors.Is(te.Err, sshx.ErrRemoteListenHost):
			return locales.ErrTunnelRemoteHostName(te.Addr)
		case te.Remote && strings.Contains(te.Err.Error(), "denied"):
			return locales.ErrTunnelRemoteDenied(te.Addr)
		}
		return locales.ErrTunnelListen(te.Addr, te.Err.Error())
	}
	return locales.ErrTunnelStopped(err.Error())
}

func tunnelStatusColumns(narrow bool) []ui.TableColumn {
	if narrow {
		return []ui.TableColumn{
			{ID: "type", Title: locales.TunNameAndType(), MinWidth: 85},
			{ID: "listen", Title: locales.ColTunnelListen(), MinWidth: 105},
			{ID: "status", Title: locales.ColTunnelStatus(), MinWidth: 128},
		}
	}
	return []ui.TableColumn{
		{ID: "type", Title: locales.TunNameAndType(), Width: 173},
		{ID: "listen", Title: locales.ColTunnelListen(), Width: 140},
		{ID: "target", Title: locales.TunTargetAndExit(), MinWidth: 130},
		{ID: "status", Title: locales.ColTunnelStatus(), MinWidth: 145},
		{ID: "connections", Title: locales.ColTunnelConnections(), Width: 122},
		{ID: "traffic", Title: locales.ColTunnelTraffic(), Width: 144},
		{ID: "action", Title: "", Width: 80, Align: ui.Center, Fixed: true},
	}
}

// tunnelPanel is the "Tunnels" tab: the active session's tunnels, their
// state and what they carried, with a button to start or stop each.
func (a *App) tunnelPanel(c *ui.Context) {
	s := a.connSession()
	if s == nil || s.isNewTab() {
		return
	}
	if s.isLocal() {
		localUnavailable(c)
		return
	}
	ts := &s.tunnels
	connected := s.status == statusConnected && s.client != nil
	width, _ := c.Size()
	narrow := width < 900 || !a.current().isPanelTab() && a.panelOnRight()
	cols := tunnelStatusColumns(narrow)
	ui.Column(c.Key(s.ID)).Grow(1).Children(func() {
		ui.Row(c).Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
			switch {
			case !connected:
				widgets.Muted(c, locales.TunnelsOffline()).Grow(1)
			case len(ts.runs) == 0:
				widgets.Muted(c, locales.TunnelsNone()).Grow(1)
			default:
				ui.Spacer(c)
			}
			if len(ts.runs) > 0 {
				ui.MenuButton(c, locales.TunActions(), func(menu *ui.Menu) {
					var run *tunnelRun
					if ts.selection >= 0 && ts.selection < len(ts.runs) {
						run = ts.runs[ts.selection]
					}
					a.tunnelMenu(c, menu, s, run, connected)
				}).Shrink(0)
			}
			if ui.Button(c, locales.TunnelsConfigure()).Clicked() {
				a.editTunnels(s.Host)
			}
			a.tunnelManagerButton(c)
		})
		runs := ts.runs
		ts.table.Selected = &ts.selection
		ts.table.Key = func(i int) any { return runs[i] }
		ts.table.Label = func(i int) string { return tunnelName(&runs[i].spec) + " " + runs[i].address() }
		ts.table.CellMenu = func(menu *ui.Menu, row, _ int) {
			a.tunnelMenu(c, menu, s, runs[row], connected)
			a.exposeRunMenu(menu, s, runs[row])
		}
		ts.table.HeaderMenu = func(menu *ui.Menu, _ int) { a.exposeRunMenu(menu, s, nil) }
		ui.Table(c, &ts.table, cols, len(runs), func(row, col int) {
			a.tunnelCell(c, s, runs[row], cols[col].ID, connected, narrow)
		}).Grow(1).MinHeight(0)
		a.tunnelDialogs(c, s)
	})
}

func (a *App) tunnelCell(c *ui.Context, s *Session, run *tunnelRun, col string, connected, narrow bool) {
	t := &run.spec
	switch col {
	case "type":
		tunnelTypeCell(c, t)
	case "listen":
		target := ""
		if narrow {
			target = tunnelTarget(t)
		}
		tunnelAddressCell(c, t, run.fwd, target)
	case "target":
		ui.Text(c, tunnelTarget(t)).SingleLine().Tooltip(tunnelTarget(t))
	case "status":
		ui.Column(c).Gap(2).MinWidth(0).Children(func() {
			color, text := theme.TextMuted, locales.TunnelStopped()
			switch {
			case run.starting:
				color, text = theme.Warning, locales.TunnelStarting()
			case run.fwd != nil:
				color, text = theme.Success, locales.TunnelListening()
			case run.err != nil:
				color, text = theme.Danger, describeTunnelError(run.err)
			}
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				widgets.StatusDot(c, color)
				st := ui.Text(c, text).MaxLines(2).Shrink(1).MinWidth(0).Tooltip(text)
				if run.err != nil && !run.starting {
					st.TextColor(theme.Danger)
				}
			})
			if run.last != nil {
				st := run.last.Stats()
				if !run.starting && run.err == nil && st.LastError != nil {
					msg := tunnelLogResult(st.LastError)
					widgets.Muted(c, msg).FontSize(11.5).SingleLine().Tooltip(msg)
				}
				if narrow {
					tunnelConnectionsCell(c, st)
					tunnelTrafficCell(c, st)
				}
			}
		})
	case "connections":
		if run.last != nil {
			tunnelConnectionsCell(c, run.last.Stats())
		}
	case "traffic":
		if run.last != nil {
			tunnelTrafficCell(c, run.last.Stats())
		}
	case "action":
		if run.fwd != nil || run.starting {
			if ui.Button(c, locales.TunnelStop()).Clicked() {
				s.tunnels.ask(run, false)
			}
		} else if ui.Button(c, locales.TunnelStart()).Disabled(!connected).Tooltip(tunnelStartReason(run, connected)).Clicked() && connected {
			s.tunnels.ask(run, true)
		}
	}
}

func tunnelAddressCell(c *ui.Context, spec *config.Tunnel, fwd *sshx.Tunnel, target string) {
	addr := spec.ListenAddr()
	automatic := fwd == nil && spec.ListenPort == 0
	if fwd != nil {
		addr = fwd.Addr().String()
	} else if automatic {
		addr = spec.ListenHostOrLoopback()
	}
	ui.Column(c).Gap(2).MinWidth(0).Shrink(1).Children(func() {
		ui.Text(c, addr).SingleLine().Tooltip(addr)
		if automatic {
			widgets.Muted(c, locales.TunAutomatic()).FontSize(11).SingleLine().Tooltip(locales.TunAutomatic())
		}
		if target != "" {
			widgets.Muted(c, target).FontSize(11).SingleLine().Tooltip(target)
		}
	})
}

func tunnelConnectionsCell(c *ui.Context, st sshx.TunnelStats) {
	text := locales.TunnelConnectionsCount(int(st.Active), int(st.Total))
	if st.Rejected != 0 {
		text += " · " + locales.TunRejectedCount(int(st.Rejected))
	}
	ui.Text(c, text).FontSize(12).SingleLine().Tooltip(text)
}

func tunnelTrafficCell(c *ui.Context, st sshx.TunnelStats) {
	text := locales.TunnelTraffic(formatSize(st.Up), formatSize(st.Down))
	ui.Column(c).MinWidth(0).Gap(2).Children(func() {
		ui.Text(c, text).FontSize(12).SingleLine().Tooltip(text + " · " + locales.TunBytesPerSecond(formatBytes(st.UpRate+st.DownRate)))
		widgets.BarChart(c, 17, sshx.TunnelRateSamples, [][]float64{st.UpHistory[:], st.DownHistory[:]}, []ui.Color{txChart, rxChart}, func(rate float64) string {
			return locales.TunBytesPerSecond(formatBytes(rate))
		})
	})
}

// editTunnels opens the connection editor on h's Tunnels page, or
// brings the open editor to the front as it is.
func (a *App) editTunnels(h *config.Host) {
	opening := !a.windowOpen(WindowEditor)
	a.editConnection(h)
	if opening && a.editor != nil {
		a.editor.nav.Page = editorTunnels
	}
}
