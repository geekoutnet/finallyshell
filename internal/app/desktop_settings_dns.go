package app

import (
	"context"
	"errors"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopSetDNSState struct {
	load, probe     desktopSetLoad
	data            monitor.SysConfDNS
	servers, search string
	result          string
}

var desktopSetDNSPresets = []struct{ name, servers string }{
	{"Google", "8.8.8.8\n8.8.4.4"}, {"Cloudflare", "1.1.1.1\n1.0.0.1"}, {"Quad9", "9.9.9.9\n149.112.112.112"},
	{"AliDNS", "223.5.5.5\n223.6.6.6"}, {"DNSPod", "119.29.29.29"}, {"114DNS", "114.114.114.114"},
}

func (a *App) desktopSetRefreshDNS(window *desktopWindow, state *desktopSettingsState) {
	page, run := &state.dns, systemReader(window.session)
	desktopSetRead(a, window, state, &page.load, func(ctx context.Context) (monitor.SysConfDNS, string, error) {
		out, stderr, err := run(ctx, monitor.SysConfDNSCommand, nil, nil)
		if err == nil && !monitor.ValidSysConfOutput(out) {
			err = monitor.ErrSysInfoOutput
		}
		return monitor.ParseSysConfDNS(out), stderr, err
	}, func(dns monitor.SysConfDNS) {
		page.data = dns
		page.servers, page.search = strings.Join(dns.Edit.Servers, "\n"), strings.Join(dns.Edit.Search, " ")
	})
}

func desktopSetDNSMode(mode monitor.SysConfDNSMode) string {
	switch mode {
	case monitor.SysConfDNSResolved:
		return "systemd-resolved"
	case monitor.SysConfDNSNetworkManager:
		return "NetworkManager"
	case monitor.SysConfDNSPlain:
		return monitor.SysConfResolvPath
	case monitor.SysConfDNSManaged:
		return locales.DesktopSetDNSExternal()
	default:
		return locales.DesktopSetUnknown()
	}
}

func (a *App) desktopSetDNS(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	if !state.online {
		desktopSetOffline(c, window.session)
		return
	}
	page := &state.dns
	if !page.load.loaded {
		a.desktopSetRefreshDNS(window, state)
	}
	desktopSetReadNote(c, &page.load)
	a.desktopSetDNSStatus(c, window, state)
	a.desktopSetDNSEditor(c, window, state)
}

func (a *App) desktopSetDNSStatus(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	page := &state.dns
	desktopSetCard(c).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			desktopSetHeading(c, "globe", locales.DesktopSetDNSStatus())
			ui.Box(c).Grow(1)
			if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Disabled(page.load.cancel != nil || state.dialog != nil).Clicked() {
				a.desktopSetRefreshDNS(window, state)
			}
		})
		desktopSetField(c, locales.DesktopSetDNSBackend(), desktopSetDNSMode(page.data.Mode))
		if page.data.Symlink != "" {
			widgets.Muted(c, monitor.SysConfResolvPath+" → "+page.data.Symlink).Selectable()
		}
		if page.data.NetworkManager {
			widgets.Muted(c, locales.DesktopSetDNSNetworkManager(desktopSetValue(page.data.Device, locales.DesktopSetUnknown())))
		}
		if page.data.Mode == monitor.SysConfDNSManaged || page.data.Mode == monitor.SysConfDNSUnknown {
			ui.Text(c, locales.DesktopSetDNSManaged()).TextColor(colors.warningText)
		}
		for index, link := range page.data.Links {
			ui.Column(c.Key(index)).Gap(2).Children(func() {
				ui.Text(c, desktopSetValue(link.Scope, locales.DesktopSetDNSGlobal())).Bold()
				if len(link.Servers) > 0 {
					desktopSetField(c, locales.DesktopSetDNSServers(), strings.Join(link.Servers, "  "))
				}
				if link.Current != "" {
					desktopSetField(c, locales.DesktopSetDNSCurrent(), link.Current)
				}
				if len(link.Search) > 0 {
					desktopSetField(c, locales.DesktopSetDNSSearch(), strings.Join(link.Search, "  "))
				}
			})
		}
		if len(page.data.Links) == 0 || page.data.Symlink != "" {
			if len(page.data.File.Servers) > 0 {
				desktopSetField(c, locales.DesktopSetDNSFileServers(), strings.Join(page.data.File.Servers, "  "))
			}
			if len(page.data.File.Search) > 0 {
				desktopSetField(c, locales.DesktopSetDNSSearch(), strings.Join(page.data.File.Search, "  "))
			}
		}
		if len(page.data.File.Options) > 0 {
			desktopSetField(c, locales.DesktopSetDNSOptions(), strings.Join(page.data.File.Options, "  "))
		}
		if page.data.Mode == monitor.SysConfDNSResolved {
			widgets.Muted(c, locales.DesktopSetDNSLinksStay()).FontSize(11)
		}
	})
}

func (a *App) desktopSetDNSEditor(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	page := &state.dns
	writable := page.load.loaded && page.load.cancel == nil && page.load.failure == "" && state.dialog == nil &&
		(page.data.Mode == monitor.SysConfDNSPlain || page.data.Mode == monitor.SysConfDNSResolved || page.data.Mode == monitor.SysConfDNSNetworkManager)
	note := locales.DesktopSetDNSConfirmNote()
	if page.data.Mode == monitor.SysConfDNSNetworkManager {
		note += " " + locales.DesktopSetDNSDisconnect()
	}
	refresh := func() { a.desktopSetRefreshDNS(window, state) }
	restore, restoreErr := monitor.BuildSysConfDNS(page.data, monitor.DNSValues{}, true)
	values, err := monitor.ValidateSysConfDNS(page.servers, page.search)
	apply, applyErr := monitor.BuildSysConfDNS(page.data, values, false)
	applyOK := writable && err == nil && applyErr == nil
	restoreOK := writable && restoreErr == nil
	desktopSetCard(c).ContextMenu(func(menu *ui.Menu) {
		if menu.Item(locales.DesktopSetDNSApply()).Disabled(!applyOK).Chosen() {
			a.desktopSetChange(window, state, locales.DesktopSetDNSApply(), note, apply, true, refresh)
		}
		if menu.Item(locales.DesktopSetDNSRestore()).Disabled(!restoreOK).Chosen() {
			a.desktopSetChange(window, state, locales.DesktopSetDNSRestore(), note, restore, true, refresh)
		}
	}).Children(func() {
		desktopSetHeading(c, "settings", locales.DesktopSetDNSEdit())
		ui.Button(c, locales.DesktopSetDNSPresets()+" ▾").AlignSelf(ui.Start).Disabled(!writable).PopoverMenuButton(func(menu *ui.Menu) {
			for _, preset := range desktopSetDNSPresets {
				if menu.Item(preset.name + " · " + strings.ReplaceAll(preset.servers, "\n", ", ")).Chosen() {
					page.servers = preset.servers
				}
			}
		})
		widgets.Muted(c, locales.DesktopSetDNSServers()).FontSize(11)
		ui.TextArea(c, &page.servers).FillWidth().Height(62).Label(locales.DesktopSetDNSServers()).Placeholder("1.1.1.1\n2606:4700:4700::1111").ReadOnly(!writable)
		widgets.Muted(c, locales.DesktopSetDNSSearch()).FontSize(11)
		ui.TextInput(c, &page.search).FillWidth().Label(locales.DesktopSetDNSSearch()).Placeholder("example.com").ReadOnly(!writable)
		if writable && (err != nil && page.servers != "" || errors.Is(applyErr, monitor.ErrSysConfDNSLimit)) {
			message := locales.DesktopSetDNSInvalidIP()
			if errors.Is(err, monitor.ErrSysConfDNSDomain) {
				message = locales.DesktopSetDNSInvalidDomain()
			}
			if errors.Is(applyErr, monitor.ErrSysConfDNSLimit) {
				message = locales.DesktopSetDNSLimit()
			}
			ui.Text(c, message).TextColor(theme.Danger)
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.PrimaryButton(c, locales.DesktopSetDNSApply()).Disabled(!applyOK).Clicked() {
				a.desktopSetChange(window, state, locales.DesktopSetDNSApply(), note, apply, true, refresh)
			}
			if ui.Button(c, locales.DesktopSetDNSRestore()).Disabled(!restoreOK).Clicked() {
				a.desktopSetChange(window, state, locales.DesktopSetDNSRestore(), note, restore, true, refresh)
			}
			if ui.Button(c, locales.DesktopSetDNSTest()).Disabled(page.probe.cancel != nil || state.dialog != nil).Clicked() {
				run := systemReader(window.session)
				page.result = ""
				desktopSetRead(a, window, state, &page.probe, func(ctx context.Context) (string, string, error) {
					return run(ctx, monitor.SysConfDNSProbe, nil, nil)
				}, func(out string) { page.result = strings.TrimSpace(out) })
			}
		})
		desktopSetReadNote(c, &page.probe)
		if page.result != "" {
			desktopSetField(c, "example.com", page.result)
		}
	})
}
