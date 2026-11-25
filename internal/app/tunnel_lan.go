package app

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const windowTunnelLAN WindowKind = "tunnel-lan"

type tunnelLANInterface struct {
	name      string
	flags     net.Flags
	addresses []string
}

type tunnelLANDialog struct {
	spec                   config.Tunnel
	port                   int
	addresses              []netip.Prefix
	bindUnknown            bool
	windows                bool
	check                  func(context.Context, int) string
	add                    func(int) error
	busy                   bool
	confirm                *exposeAction
	status, note, errorMsg string
}

func tunnelLANInterfaces() []tunnelLANInterface {
	interfaces, _ := net.Interfaces()
	result := make([]tunnelLANInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		entry := tunnelLANInterface{name: iface.Name, flags: iface.Flags}
		for _, addr := range addresses {
			entry.addresses = append(entry.addresses, addr.String())
		}
		result = append(result, entry)
	}
	return result
}

func tunnelLANAddresses(interfaces []tunnelLANInterface, bind netip.Addr) []netip.Prefix {
	if !bind.IsValid() || bind.IsLoopback() {
		return nil
	}
	var addresses []netip.Prefix
	for _, iface := range interfaces {
		if iface.flags&net.FlagUp == 0 || iface.flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 || tunnelVirtualAdapter(iface.name) {
			continue
		}
		for _, value := range iface.addresses {
			prefix, err := netip.ParsePrefix(value)
			addr := prefix.Addr()
			if err != nil || !addr.IsGlobalUnicast() || addr.Is4In6() || bind.Is4() && !addr.Is4() || !bind.IsUnspecified() && addr != bind {
				continue
			}
			addresses = append(addresses, prefix)
		}
	}
	slices.SortFunc(addresses, func(left, right netip.Prefix) int { return left.Addr().Compare(right.Addr()) })
	return slices.Compact(addresses)
}

func tunnelVirtualAdapter(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{"vethernet", "vmware", "virtualbox", "vbox", "docker", "utun", "veth", "virbr", "br-", "wg", "tailscale", "zerotier", "wsl", "hyper-v", "tun", "tap"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func tunnelLANRule(port int) string {
	if port < 1 || port > 65535 {
		return ""
	}
	return fmt.Sprintf(`netsh advfirewall firewall add rule name="Finallyshell tunnel %d" dir=in action=allow protocol=TCP localport=%d`, port, port)
}

func tunnelShareAddress(addr string, kind config.TunnelKind) string {
	switch kind {
	case config.TunnelHTTP:
		return "http://" + addr
	case config.TunnelDynamic:
		return "socks5://" + addr
	}
	return addr
}

func (a *App) openTunnelLAN(spec config.Tunnel, run *tunnelRun, parent WindowKind) {
	if a.windowOpen(windowTunnelLAN) {
		a.plat.FocusWindow(windowTunnelLAN)
		return
	}
	bind, port := spec.ListenHost, spec.ListenPort
	if run != nil && run.fwd != nil {
		if addr, ok := run.fwd.Addr().(*net.TCPAddr); ok {
			bind, port = addr.IP.String(), addr.Port
		}
	}
	address, _ := netip.ParseAddr(bind)
	dialog := &tunnelLANDialog{spec: spec, port: port, addresses: tunnelLANAddresses(tunnelLANInterfaces(), address), bindUnknown: !address.IsValid(),
		windows: runtime.GOOS == "windows", check: tunnelLANFirewallStatus, add: addTunnelLANFirewallRule}
	a.openWindow(WindowSpec{Kind: windowTunnelLAN, Parent: parent, Title: locales.TexpLANTitle, Width: 610, Height: 435, MinWidth: 480, MinHeight: 340, Modal: true, OwnKeys: true,
		CloseRequested: func() bool { return !dialog.busy }}, func(c *ui.Context) {
		if dialog.confirm == nil && !dialog.busy && (c.Shortcut(0, ui.KeyEscape) || a.closeWindowShortcut(c)) {
			a.closeWindow(windowTunnelLAN)
			return
		}
		a.tunnelLANView(c, dialog)
	})
}

func (a *App) tunnelLANView(c *ui.Context, dialog *tunnelLANDialog) {
	ui.Column(c).Fill().Padding(14, 18).Gap(9).Children(func() {
		ui.Text(c, locales.TexpLANHint()).TextColor(theme.Warning).FontSize(12)
		if dialog.spec.Kind.IsProxy() && dialog.spec.ProxyUser == "" {
			ui.Text(c, locales.TexpProxyWarn()).TextColor(theme.Danger).FontSize(12).Bold()
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(9).Padding(0, 8, 2, 0).Children(func() {
				tunnelLANAddressView(c, dialog)
				a.tunnelLANFirewallView(c, dialog)
			})
		})
		exposureFooter(c, dialog.busy, dialog.note, dialog.errorMsg, func() { a.closeWindow(windowTunnelLAN) })
	})
	exposureConfirmation(c, &dialog.confirm)
}

func tunnelLANAddressView(c *ui.Context, dialog *tunnelLANDialog) {
	ui.Text(c, locales.TexpLANAddresses()).Bold()
	if dialog.port == 0 {
		widgets.Muted(c, locales.TexpLANPortPending())
	} else if dialog.bindUnknown {
		widgets.Muted(c, locales.TexpLANBindUnknown())
	} else if len(dialog.addresses) == 0 {
		widgets.Muted(c, locales.TexpLANNoAddresses())
	}
	for _, address := range dialog.addresses {
		ui.Row(c.Key(address)).Gap(8).AlignItems(ui.Center).Children(func() {
			label := address.Addr().String()
			if dialog.port > 0 {
				label = tunnelShareAddress(net.JoinHostPort(label, strconv.Itoa(dialog.port)), dialog.spec.Kind)
			}
			ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, label).Selectable().SingleLine().Tooltip(label)
				widgets.Muted(c, address.Masked().String()).FontSize(11).SingleLine()
			})
			if ui.Button(c, locales.Copy()).Clicked() {
				c.WriteClipboard(label)
			}
		})
	}
	widgets.Muted(c, locales.TexpLANAllowlist()).FontSize(12)
}

func (a *App) tunnelLANFirewallView(c *ui.Context, dialog *tunnelLANDialog) {
	if !dialog.windows {
		exposureStep(c, locales.TexpLANFirewall(), locales.TexpLANFirewallOther(), theme.TextMuted, nil)
		return
	}
	status := dialog.status
	if status == "" {
		status = locales.TexpUnchecked()
	}
	if dialog.port == 0 {
		status = locales.TexpLANPortPending()
	}
	exposureStep(c, locales.TexpLANFirewall(), status, theme.TextMuted, func() {
		if ui.Button(c, locales.TexpLANFirewallCheck()).Disabled(dialog.busy || dialog.port == 0).Clicked() {
			dialog.busy = true
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				value := dialog.check(ctx, dialog.port)
				a.post(func() { dialog.busy, dialog.status = false, value })
			}()
		}
	})
	if cmd := tunnelLANRule(dialog.port); cmd != "" {
		ui.Text(c, cmd).Font("monospace").FontSize(11).Selectable()
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, locales.Copy()).Clicked() {
				c.WriteClipboard(cmd)
			}
			if ui.Button(c, locales.TexpLANFirewallAdd()).Disabled(dialog.busy).Clicked() {
				dialog.confirm = &exposeAction{locales.TexpLANFirewallConfirm(strconv.Itoa(dialog.port)), func() { a.changeTunnelLANFirewall(dialog) }}
			}
		})
	}
}

func (a *App) changeTunnelLANFirewall(dialog *tunnelLANDialog) {
	dialog.busy, dialog.errorMsg, dialog.note = true, "", ""
	go func() {
		err := dialog.add(dialog.port)
		a.post(func() {
			dialog.busy = false
			if err != nil {
				dialog.errorMsg = locales.TexpError(err.Error())
			} else {
				dialog.note = locales.TexpLANFirewallOpened()
			}
		})
	}()
}
