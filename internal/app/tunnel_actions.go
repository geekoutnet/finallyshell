package app

import (
	"errors"
	"net"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type tunnelConfirmation struct {
	run        *tunnelRun
	start      bool
	generation int
}

type tunnelLog struct {
	run   *tunnelRun
	query string
	table ui.ListState
}

func (run *tunnelRun) address() string {
	if run.fwd != nil {
		return run.fwd.Addr().String()
	}
	return configuredTunnelAddr(&run.spec)
}

func (run *tunnelRun) proxyScheme() string {
	switch run.spec.Kind {
	case config.TunnelDynamic, config.TunnelRemoteDynamic:
		return "socks5"
	case config.TunnelHTTP, config.TunnelRemoteHTTP:
		return "http"
	}
	return ""
}

func (run *tunnelRun) copyAddress() string {
	if run.fwd == nil {
		return ""
	}
	addr := run.address()
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			if ip.To4() == nil {
				addr = net.JoinHostPort("::1", port)
			} else {
				addr = net.JoinHostPort(config.LoopbackHost, port)
			}
		}
	}
	if scheme := run.proxyScheme(); scheme != "" {
		return scheme + "://" + addr
	}
	return addr
}

func (run *tunnelRun) command() string {
	addr := run.copyAddress()
	if addr == "" {
		return ""
	}
	switch run.proxyScheme() {
	case "http":
		return "export http_proxy=" + tunnelShellArg(addr) + " https_proxy=" + tunnelShellArg(addr)
	case "socks5":
		return "export all_proxy=" + tunnelShellArg(strings.Replace(addr, "socks5://", "socks5h://", 1))
	}
	if strings.HasSuffix(run.spec.TargetSocket, "/docker.sock") {
		return "docker -H " + tunnelShellArg("tcp://"+addr) + " version"
	}
	return ""
}

func tunnelShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func tunnelStartReason(run *tunnelRun, connected bool) string {
	switch {
	case run == nil:
		return locales.TunSelectOne()
	case run.fwd != nil || run.starting:
		return locales.TunAlreadyRunning()
	case !connected:
		return locales.TunnelsOffline()
	}
	return ""
}

func (ts *tunnelState) ask(run *tunnelRun, start bool) {
	ts.confirm = &tunnelConfirmation{run, start, run.gen}
}

func (a *App) tunnelMenu(c *ui.Context, menu *ui.Menu, s *Session, run *tunnelRun, connected bool) {
	if resourceMenuItem(menu, locales.TunnelStart(), tunnelStartReason(run, connected), false) {
		s.tunnels.ask(run, true)
	}
	stopReason, addressReason, commandReason, logReason, editReason := "", "", "", "", ""
	if run == nil {
		stopReason, addressReason, commandReason, logReason, editReason = locales.TunSelectOne(), locales.TunSelectOne(), locales.TunSelectOne(), locales.TunSelectOne(), locales.TunSelectOne()
	} else {
		if !run.starting && run.fwd == nil {
			stopReason = locales.TunNotRunning()
		}
		if run.fwd == nil {
			addressReason, commandReason = locales.TunNotRunning(), locales.TunNotRunning()
		} else if run.command() == "" {
			commandReason = locales.TunNoCommand()
		}
		if run.last == nil {
			logReason = locales.TunNoLog()
		}
		if s.Host == nil || s.Host.Deleted() {
			editReason = locales.ErrHostDeleted()
		}
	}
	if resourceMenuItem(menu, locales.TunnelStop(), stopReason, false) {
		s.tunnels.ask(run, false)
	}
	if resourceMenuItem(menu, locales.TunCopyAddress(), addressReason, false) {
		c.WriteClipboard(run.copyAddress())
	}
	if resourceMenuItem(menu, locales.TunCopyCommand(), commandReason, false) {
		c.WriteClipboard(run.command())
	}
	if resourceMenuItem(menu, locales.TunConnectionLog(), logReason, false) {
		s.tunnels.log = &tunnelLog{run: run}
	}
	if resourceMenuItem(menu, locales.TunnelEdit(), editReason, false) {
		a.editTunnels(s.Host)
		if a.editor != nil && a.editor.id == s.Host.ID && !a.editor.tunnels.dialog && !a.editor.tunnels.removeOpen {
			a.editor.nav.Page = editorTunnels
			for idx, spec := range a.editor.tunnels.list {
				if spec.ID == run.spec.ID || spec.ID == "" && spec.Kind == run.spec.Kind && spec.ListenAddr() == run.spec.ListenAddr() && tunnelTarget(&spec) == tunnelTarget(&run.spec) {
					a.editor.tunnels.open(idx)
					break
				}
			}
		}
	}
}

func (a *App) tunnelDialogs(c *ui.Context, s *Session) {
	ts := &s.tunnels
	if ask := ts.confirm; ask != nil {
		open := true
		ui.Modal(c.Key("tunnel-confirm"), &open, func() {
			name, addr := tunnelName(&ask.run.spec), ask.run.address()
			label, text := locales.TunnelStop(), locales.TunStopQuestion(name, addr)
			if ask.start {
				label, text = locales.TunnelStart(), locales.TunStartQuestion(name, addr)
			}
			width, _ := c.Size()
			ui.Column(c).Width(min(400, width-48)).Gap(12).Children(func() {
				ui.Text(c, text).FillWidth()
				if ask.start && !config.IsLoopback(ask.run.spec.ListenHost) {
					warning := publicWarning(ask.run.spec.Kind)
					if ask.run.spec.Kind.IsProxy() {
						warning = locales.TunPublicAuth()
						if ask.run.spec.ProxyUser == "" {
							warning = locales.TunOpenProxy()
						}
					}
					ui.Text(c, warning).TextColor(theme.Warning).FillWidth()
				}
				if bulkConfirmButtons(c, &open, label) && ask.generation == ask.run.gen {
					if ask.start {
						a.startSessionTunnel(s, ask.run.spec.ID)
					} else {
						a.stopSessionTunnel(s, ask.run.spec.ID)
					}
				}
			})
		})
		if !open {
			ts.confirm = nil
		}
	}
	if log := ts.log; log != nil {
		open := true
		ui.Modal(c.Key("tunnel-log"), &open, func() { log.view(c, &open) })
		if !open {
			ts.log = nil
		}
	}
}

func tunnelLogResult(err error) string {
	switch {
	case err == nil:
		return locales.TunLogOK()
	case errors.Is(err, sshx.ErrTunnelPeerDenied):
		return locales.TunLogRejected()
	case errors.Is(err, sshx.ErrTunnelCapacity):
		return locales.TunLogAtCapacity()
	case errors.Is(err, sshx.ErrTunnelIdle):
		return locales.TunLogIdle()
	case errors.Is(err, sshx.ErrProxyAuth):
		return locales.TunLogAuth()
	case errors.Is(err, sshx.ErrConnectionClosed):
		return locales.ErrTunnelLost()
	case errors.Is(err, sshx.ErrTunnelDialDenied):
		return locales.TunTargetDenied()
	case errors.Is(err, sshx.ErrTunnelDialRefused):
		return locales.TunTargetRefused()
	case errors.Is(err, sshx.ErrTunnelDialNetwork):
		return locales.TunTargetNetwork()
	case errors.Is(err, sshx.ErrTunnelDialTimeout):
		return locales.TunTargetTimeout()
	default:
		return locales.TunLogFailed()
	}
}

func tunnelLogValues(entry sshx.TunnelConn) []string {
	return []string{entry.Started.Format("2006-01-02 15:04:05"), entry.Client, orDash(entry.Target),
		locales.TunnelTraffic(formatSize(entry.Up), formatSize(entry.Down)), entry.Duration.Round(time.Millisecond).String(), tunnelLogResult(entry.Err)}
}

func (log *tunnelLog) view(c *ui.Context, open *bool) {
	width, height := c.Size()
	cols := []ui.TableColumn{
		{ID: "time", Title: locales.TunLogTime(), Width: 146}, {ID: "client", Title: locales.TunLogClient(), MinWidth: 127},
		{ID: "target", Title: locales.ColTunnelTarget(), MinWidth: 120}, {ID: "bytes", Title: locales.ColTunnelTraffic(), Width: 138},
		{ID: "duration", Title: locales.TunLogDuration(), Width: 85}, {ID: "result", Title: locales.TunLogResult(), MinWidth: 120},
	}
	rows := make([][]string, 0)
	query := strings.ToLower(strings.TrimSpace(log.query))
	for _, entry := range log.run.last.Conns() {
		row := tunnelLogValues(entry)
		if strings.Contains(strings.ToLower(strings.Join(row, " ")), query) {
			rows = append(rows, row)
		}
	}
	ui.Column(c).Width(min(850, width-48)).Height(min(490, height-95)).Gap(8).Children(func() {
		ui.Text(c, locales.TunConnectionLog()+" · "+tunnelName(&log.run.spec)).Bold().SingleLine()
		ui.Row(c).Gap(8).Children(func() {
			ui.SearchField(c, &log.query).Grow(1).MinWidth(0).Placeholder(locales.TunLogFilter()).Label(locales.TunLogFilter())
			if ui.Button(c, locales.Copy()).Disabled(len(rows) == 0).Clicked() {
				var text strings.Builder
				for _, row := range rows {
					text.WriteString(strings.Join(row, "\t") + "\n")
				}
				c.WriteClipboard(text.String())
			}
		})
		ui.Table(c, &log.table, cols, len(rows), func(row, col int) {
			text := rows[row][col]
			ui.Text(c, text).SingleLine().Tooltip(text)
		}).Grow(1).MinHeight(0).Children(func() {
			if len(rows) == 0 {
				widgets.Muted(c, locales.TunNoLog()).Padding(10)
			}
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Close()).Clicked() {
				*open = false
			}
		})
	})
}
