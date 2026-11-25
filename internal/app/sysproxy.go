package app

import (
	"errors"
	"net"
	"path/filepath"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/sysproxy"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type systemProxyRuntime struct {
	manager *sysproxy.Manager
	tail    chan struct{}
	owner   *sshx.Tunnel
	closed  bool
}

func (a *App) initSystemProxy() {
	a.sysProxy.manager = sysproxy.New(filepath.Join(config.Dir(), sysproxy.BackupName), sysproxy.NativeBackend())
	a.restoreSystemProxy()
}

func (a *App) queueSystemProxy(change func() error) {
	state := &a.sysProxy
	if state.closed || state.manager == nil {
		return
	}
	previous, done := state.tail, make(chan struct{})
	state.tail = done
	go func() {
		if previous != nil {
			<-previous
		}
		err := change()
		close(done)
		if err != nil {
			a.post(func() {
				if a.sysProxy.closed {
					return
				}
				message := locales.SysproxyFailed()
				if errors.Is(err, sysproxy.ErrUnsupported) {
					message = locales.SysproxyUnsupported()
				}
				a.notify(message)
			})
		}
	}()
}

func systemProxyAllowed(tunnel *config.Tunnel) bool {
	return (tunnel.Kind == config.TunnelDynamic || tunnel.Kind == config.TunnelHTTP) &&
		config.IsLoopback(tunnel.ListenHost) && tunnel.ProxyUser == "" && len(tunnel.AllowFrom) == 0
}

func (a *App) applySystemProxy(_ *config.Host, tunnel config.Tunnel, addr net.Addr, owner ...*sshx.Tunnel) {
	if !tunnel.SystemProxy || addr == nil || !systemProxyAllowed(&tunnel) {
		return
	}
	settings := sysproxy.Settings{Address: addr.String(), SOCKS: tunnel.Kind == config.TunnelDynamic && !tunnel.Mixed, PAC: append([]string(nil), tunnel.PAC...)}
	var run *sshx.Tunnel
	if len(owner) != 0 {
		run = owner[0]
	}
	a.queueSystemProxy(func() error {
		if run != nil {
			select {
			case <-run.Done():
				return nil
			default:
			}
		}
		if err := a.sysProxy.manager.Apply(settings); err != nil {
			return err
		}
		a.sysProxy.owner = run
		return nil
	})
}

func (a *App) systemProxyTunnelStarted(host *config.Host, tunnel config.Tunnel, run *sshx.Tunnel) {
	if run == nil || !tunnel.SystemProxy || !systemProxyAllowed(&tunnel) || a.sysProxy.closed {
		return
	}
	select {
	case <-run.Done():
		return
	default:
	}
	a.applySystemProxy(host, tunnel, run.Addr(), run)
	go func() {
		<-run.Done()
		a.post(func() {
			a.queueSystemProxy(func() error {
				if a.sysProxy.owner == run {
					return a.restoreSystemProxyNow()
				}
				return nil
			})
		})
	}()
}

func (a *App) restoreSystemProxy() {
	a.queueSystemProxy(a.restoreSystemProxyNow)
}

func (a *App) restoreSystemProxyNow() error {
	a.sysProxy.owner = nil
	return a.sysProxy.manager.Restore()
}

func (a *App) closeSystemProxy() {
	state := &a.sysProxy
	if state.closed || state.manager == nil {
		return
	}
	state.closed = true
	if state.tail != nil {
		<-state.tail
	}
	_ = a.restoreSystemProxyNow()
}

func (a *App) confirmRestoreSystemProxy() {
	const kind WindowKind = "restore-system-proxy"
	a.openWindow(WindowSpec{Kind: kind, Title: locales.SysproxyRestore, Width: 430, Height: 165, Modal: true}, func(c *ui.Context) {
		ui.Column(c).Fill().Padding(16).Gap(10).Children(func() {
			ui.Text(c, locales.SysproxyRestoreConfirm()).FillWidth()
			ui.Spacer(c)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					a.closeWindow(kind)
				}
				if ui.PrimaryButton(c, locales.SysproxyRestore()).Clicked() {
					a.restoreSystemProxy()
					a.closeWindow(kind)
				}
			})
		})
	})
}

func (a *App) systemProxyControls(c *ui.Context, tunnel *config.Tunnel, draft ...*string) {
	systemProxyControls(c, tunnel, draft...)
}

// systemProxyControls is the "use as system proxy" switch and its PAC
// rules of a local proxy tunnel, as the tunnel dialog shows them.
func systemProxyControls(c *ui.Context, tunnel *config.Tunnel, draft ...*string) {
	if tunnel.Kind != config.TunnelDynamic && tunnel.Kind != config.TunnelHTTP {
		return
	}
	allowed := systemProxyAllowed(tunnel)
	if !allowed {
		tunnel.SystemProxy = false
	}
	ui.Column(c).Gap(5).FillWidth().Children(func() {
		ui.Checkbox(c, &tunnel.SystemProxy, locales.SysproxyEnable()).Disabled(!allowed)
		if !allowed {
			widgets.Muted(c, locales.SysproxyLoopbackOnly()).FontSize(12).FillWidth()
			return
		}
		if !tunnel.SystemProxy {
			return
		}
		widgets.Muted(c, locales.SysproxyNote()).FontSize(12).FillWidth()
		text := strings.Join(tunnel.PAC, ", ")
		value := &text
		if len(draft) != 0 && draft[0] != nil {
			value = draft[0]
		}
		if ui.TextInput(c, value).Label(locales.SysproxyPAC()).Placeholder(locales.SysproxyPACExample()).FillWidth().Changed() || value != &text {
			tunnel.PAC = splitProxyRules(*value)
		}
		if _, err := sysproxy.BuildPAC(sysproxy.Settings{Address: "127.0.0.1:1", PAC: tunnel.PAC}); err != nil {
			ui.Text(c, locales.SysproxyInvalidPAC()).TextColor(theme.Danger).FillWidth()
		}
	})
}

func splitProxyRules(text string) []string {
	return strings.FieldsFunc(text, func(char rune) bool { return strings.ContainsRune(",; \t\r\n", char) })
}
