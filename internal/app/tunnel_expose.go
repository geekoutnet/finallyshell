package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const windowTunnelExpose WindowKind = "tunnel-exposure"

type exposeAction struct {
	message string
	apply   func()
}

type tunnelExposure struct {
	ctx                      context.Context
	host                     *config.Host
	spec                     config.Tunnel
	session                  *Session
	run                      *tunnelRun
	save                     func(config.Tunnel) error
	connection               opsConnection
	login                    string
	bind, port, outside      string
	records                  []tunnelFirewallRecord
	showRecords              bool
	checks                   exposeChecks
	checked                  bool
	checkedPort              int
	busy, needsReconnect     bool
	confirm                  *exposeAction
	message, problem, result string
}

type exposeChecks struct {
	gateway             monitor.GatewayPorts
	gatewayErr, fireErr error
	tool                string
	firewall            exposeFirewall
}

func (a *App) exposeRunMenu(menu *ui.Menu, s *Session, run *tunnelRun) {
	if run != nil {
		a.exposeMenu(menu, run.spec, func() { a.openTunnelExposure(s.Host, run.spec, s, run, nil, WindowMain) })
	}
	if rules, err := tunnelFirewallRecords(); err == nil && s.Host != nil && slices.ContainsFunc(rules, func(rule tunnelFirewallRecord) bool { return rule.belongs(s.Host) }) {
		if menu.Item(locales.TexpFirewallRemovals()).Chosen() {
			a.openTunnelExposure(s.Host, config.Tunnel{Kind: config.TunnelRemote}, s, nil, nil, WindowMain)
		}
	}
}

// exposeEditorHeaderMenu is the editor's tunnel table header menu: removing
// the firewall rules the app added for the host's tunnels.
func (a *App) exposeEditorHeaderMenu(ed *connEditor) func(*ui.Menu, int) {
	return func(menu *ui.Menu, _ int) {
		if host := a.hosts.Host(ed.id); host.IsSSH() && !host.Deleted() {
			if rules, err := tunnelFirewallRecords(); err == nil && slices.ContainsFunc(rules, func(rule tunnelFirewallRecord) bool { return rule.belongs(host) }) {
				if menu.Item(locales.TexpFirewallRemovals()).Chosen() {
					a.openTunnelExposure(host, config.Tunnel{Kind: config.TunnelRemote}, a.connectedTunnelSession(host), nil, nil, WindowEditor)
				}
			}
		}
	}
}

func (a *App) exposeEditorItem(menu *ui.Menu, ed *connEditor, index int) {
	spec := ed.tunnels.list[index]
	a.exposeMenu(menu, spec, func() {
		host := a.hosts.Host(ed.id)
		if host == nil {
			host = &config.Host{Name: strings.TrimSpace(ed.name), Addr: strings.TrimSpace(ed.addr)}
		}
		save := func(updated config.Tunnel) error {
			if a.editor != ed || index >= len(ed.tunnels.list) || !reflect.DeepEqual(ed.tunnels.list[index], spec) {
				return errors.New(locales.TexpTunnelChanged())
			}
			ed.tunnels.list[index], spec = updated, updated
			return nil
		}
		a.openTunnelExposure(host, spec, a.connectedTunnelSession(host), nil, save, WindowEditor)
	})
}

func (a *App) exposeMenu(menu *ui.Menu, spec config.Tunnel, open func()) {
	if !spec.Kind.Known() {
		return
	}
	if spec.Kind.IsRemote() {
		label := locales.TexpPublic()
		if config.IsLoopback(spec.ListenHost) {
			label = locales.TexpMakePublic()
		}
		if menu.Item(label).Chosen() {
			open()
		}
	} else {
		loopback := config.IsLoopback(spec.ListenHost)
		item := menu.Item(locales.TexpLAN()).Disabled(loopback)
		if loopback {
			item.Subtitle(locales.TexpLANLoopback())
		}
		if item.Chosen() && !loopback {
			open()
		}
	}
}

func (a *App) openTunnelExposure(host *config.Host, spec config.Tunnel, session *Session, run *tunnelRun, save func(config.Tunnel) error, parent WindowKind) {
	if !spec.Kind.IsRemote() {
		a.openTunnelLAN(spec, run, parent)
		return
	}
	if a.windowOpen(windowTunnelExpose) {
		a.plat.FocusWindow(windowTunnelExpose)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	dialog := &tunnelExposure{ctx: ctx, host: host.Clone(), spec: spec, session: session, run: run, save: save,
		bind: spec.ListenHost, port: strconv.Itoa(spec.ListenPort), outside: host.Addr, showRecords: run == nil && save == nil}
	if config.IsLoopback(dialog.bind) {
		dialog.bind = "0.0.0.0"
	}
	if run == nil && session != nil {
		for _, candidate := range session.tunnels.runs {
			if sameExposureSpec(candidate.spec, spec) {
				dialog.run = candidate
				break
			}
		}
	}
	if records, err := tunnelFirewallRecords(); err == nil {
		dialog.records = slices.DeleteFunc(records, func(record tunnelFirewallRecord) bool { return !record.belongs(host) })
	} else {
		dialog.problem = locales.TexpFirewallRecordFailed(err.Error())
	}
	if session != nil {
		dialog.connection, _ = a.opsConnection(session, &session.server.firewall.opsState)
		if dialog.connection.current != nil {
			current := dialog.connection.current
			dialog.connection.current = func() bool { return current() && hostMatchesPrivilege(session.Host, dialog.host) }
		}
		dialog.login = a.passwords[host.ID]
	}
	a.openWindow(WindowSpec{Kind: windowTunnelExpose, Parent: parent, Title: locales.TexpTitle, Width: 690, Height: 620,
		MinWidth: 500, MinHeight: 425, Modal: true, OwnKeys: true, Closed: cancel, CloseRequested: func() bool { return !dialog.busy }}, func(c *ui.Context) {
		if dialog.confirm == nil && !dialog.busy && (c.Shortcut(0, ui.KeyEscape) || a.closeWindowShortcut(c)) {
			a.closeWindow(windowTunnelExpose)
			return
		}
		a.exposureView(c, dialog)
	})
}

func (dialog *tunnelExposure) listenerPort() int {
	if dialog.spec.ListenPort == 0 {
		if dialog.run != nil && dialog.run.fwd != nil && sameExposureSpec(dialog.run.spec, dialog.spec) {
			if addr, ok := dialog.run.fwd.Addr().(*net.TCPAddr); ok {
				return addr.Port
			}
		}
		return 0
	}
	return dialog.spec.ListenPort
}

func (dialog *tunnelExposure) online() bool {
	return dialog.connection.current != nil && dialog.connection.current() && dialog.connection.ctx.Err() == nil
}

func readTunnelExposure(ctx context.Context, conn opsConnection, port int) exposeChecks {
	var out, stderr string
	var checks exposeChecks
	out, stderr, checks.gatewayErr = conn.root(ctx, monitor.GatewayPortsReadScript, nil)
	if checks.gatewayErr == nil {
		checks.gateway, checks.gatewayErr = monitor.ParseGatewayPorts(out)
	} else if strings.TrimSpace(stderr) != "" {
		checks.gatewayErr = errors.New(opsTail(strings.TrimSpace(stderr)))
	}
	if !errors.Is(checks.gatewayErr, monitor.ErrPrivilegeCancelled) {
		if output, _, err := conn.normal(ctx, monitor.TunnelRelayDetectScript, nil); err == nil {
			checks.tool = strings.TrimSpace(output)
		}
		if port > 0 {
			checks.firewall, checks.fireErr = tunnelFirewallStatus(ctx, conn, port)
		}
	}
	return checks
}

func (a *App) checkTunnelExposure(dialog *tunnelExposure) {
	if dialog.busy || !dialog.online() {
		return
	}
	dialog.busy, dialog.problem = true, ""
	port, conn := dialog.listenerPort(), dialog.connection
	go func() {
		ctx, cancel := context.WithTimeout(dialog.ctx, 2*time.Minute)
		defer cancel()
		checks := readTunnelExposure(ctx, conn, port)
		a.post(func() {
			dialog.busy = false
			if dialog.ctx.Err() == nil && dialog.online() {
				dialog.checks, dialog.checked, dialog.checkedPort = checks, true, port
			}
		})
	}()
}

func (a *App) exposureWork(dialog *tunnelExposure, work func(context.Context, opsConnection) (string, error), success func(string)) {
	if dialog.busy || !dialog.online() {
		return
	}
	dialog.busy, dialog.problem, dialog.message = true, "", ""
	conn := dialog.connection
	go func() {
		ctx, cancel := context.WithTimeout(dialog.ctx, 2*time.Minute)
		defer cancel()
		stop := context.AfterFunc(conn.ctx, cancel)
		defer stop()
		message, err := work(ctx, conn)
		a.post(func() {
			dialog.busy = false
			if dialog.ctx.Err() != nil {
				return
			}
			if err != nil {
				dialog.problem = locales.TexpError(opsSafeOutput(err.Error(), dialog.login))
				return
			}
			dialog.message = message
			success(message)
		})
	}()
}

func (a *App) changeExposureBind(dialog *tunnelExposure, spec config.Tunnel) {
	if spec.ServerRelay {
		spec.Disabled, spec.Background = true, false
	}
	if dialog.save != nil {
		if err := dialog.save(spec); err != nil {
			dialog.problem = err.Error()
			return
		}
	} else {
		host := a.hosts.Host(dialog.host.ID)
		if !host.IsSSH() || !hostMatchesPrivilege(host, dialog.host) {
			dialog.problem = locales.TexpTunnelChanged()
			return
		}
		index := slices.IndexFunc(host.Tunnels, func(saved config.Tunnel) bool { return sameExposureSpec(saved, dialog.spec) })
		if index < 0 {
			dialog.problem = locales.TexpTunnelChanged()
			return
		}
		if spec.ID == "" {
			spec.ID = config.NewTunnelID()
		}
		before := host.Tunnels[index]
		host.Tunnels[index] = spec
		if err := a.saveHosts(a.hosts); err != nil {
			host.Tunnels[index] = before
			dialog.problem = locales.TexpError(err.Error())
			return
		}
		if dialog.run != nil {
			run := dialog.run
			restart := run.starting || run.fwd != nil || !dialog.spec.ServerRelay && spec.ServerRelay
			run.stop()
			run.spec = spec
			if restart && spec.ServerRelay && dialog.online() {
				a.runExposureRelay(dialog.session, run, spec, dialog.checks.tool)
			} else if restart && dialog.online() && !dialog.needsReconnect && dialog.checks.gateway.Mode == "clientspecified" && !dialog.checks.gateway.Match {
				a.startTunnel(dialog.session, run)
			}
		}
	}
	dialog.spec = spec
	dialog.result, dialog.problem, dialog.message = "", "", locales.TexpPersisted()
	dialog.checked = false
	if dialog.online() {
		a.checkTunnelExposure(dialog)
	}
}

func sameExposureSpec(left, right config.Tunnel) bool {
	if left.ID == "" || right.ID == "" {
		left.ID, right.ID = "", ""
	}
	return reflect.DeepEqual(left, right)
}

func exposureBind(value, port string) (netip.Addr, int, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || ip.Zone() != "" || ip.IsLoopback() || ip.Is4In6() || ip.IsMulticast() {
		return netip.Addr{}, 0, false
	}
	if strings.TrimSpace(port) == "0" || strings.TrimSpace(port) == "" {
		return ip, 0, true
	}
	number, valid := validPort(port)
	return ip, number, valid
}

func tunnelOutsideAddress(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if port < 1 || port > 65535 || !validHost(host) {
		return "", errors.New(locales.TexpOutsideBad())
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func tunnelOutsideProbe(ctx context.Context, host string, port int, useHTTP bool) (int, error) {
	addr, err := tunnelOutsideAddress(host, port)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dialer := &net.Dialer{}
	if !useHTTP {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			conn.Close()
		}
		return 0, err
	}
	transport := &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
