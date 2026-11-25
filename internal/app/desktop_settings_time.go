package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopSetTimeState struct {
	load        desktopSetLoad
	data        monitor.SysConfTime
	observed    time.Time
	zones       []string
	query, zone string
}

func (a *App) desktopSetRefreshTime(window *desktopWindow, state *desktopSettingsState) {
	page, run := &state.clock, systemReader(window.session)
	needZones := len(page.zones) == 0
	type result struct {
		data     monitor.SysConfTime
		observed time.Time
		zones    []string
	}
	desktopSetRead(a, window, state, &page.load, func(ctx context.Context) (result, string, error) {
		started := time.Now()
		out, stderr, err := run(ctx, monitor.SysConfTimeCommand, nil, nil)
		value := result{data: monitor.ParseSysConfTime(out), observed: started.Add(time.Since(started) / 2)}
		if err == nil && (!monitor.ValidSysConfOutput(out) || value.data.Clock.IsZero()) {
			err = monitor.ErrSysInfoOutput
		}
		if err == nil && needZones {
			out, _, _ = run(ctx, monitor.SysConfZonesCommand, nil, nil)
			value.zones = monitor.ParseSysConfZones(out)
		}
		return value, stderr, err
	}, func(value result) {
		page.data, page.observed = value.data, value.observed
		if needZones {
			page.zones = value.zones
		}
		page.zone = page.data.Zone
	})
}

func (a *App) desktopSetTime(c *ui.Context, window *desktopWindow, state *desktopSettingsState) {
	if !state.online {
		desktopSetOffline(c, window.session)
		return
	}
	page := &state.clock
	if !page.load.loaded {
		a.desktopSetRefreshTime(window, state)
	}
	desktopSetReadNote(c, &page.load)
	desktopSetCard(c).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			desktopSetHeading(c, "clock", locales.DesktopSetTimeStatus())
			ui.Box(c).Grow(1)
			if widgets.IconButton(c, "refresh-cw", locales.ServerRefresh()).Disabled(page.load.cancel != nil || state.dialog != nil).Clicked() {
				a.desktopSetRefreshTime(window, state)
			}
		})
		if !page.data.Clock.IsZero() {
			c.After(time.Second)
			clock := page.data.Clock.Add(time.Since(page.observed))
			ui.Text(c, clock.Format("2006-01-02  15:04:05  -07:00")).FontSize(20).Bold().FillWidth().Selectable()
			widgets.Muted(c, locales.DesktopSetTimeSkew(page.data.Clock.Sub(page.observed).Round(time.Second).String())).FontSize(11)
		}
		ui.Row(c).Gap(10).Wrap().Children(func() {
			desktopSetField(c, locales.DesktopSetZone(), page.data.Zone)
			desktopSetField(c, locales.DesktopSetNTP(), desktopSetTimeYesNo(page.data.NTP))
			desktopSetField(c, locales.DesktopSetSynced(), desktopSetTimeYesNo(page.data.Synchronized))
			desktopSetField(c, locales.DesktopSetRTC(), page.data.RTC)
			if page.data.LocalRTC != "" {
				desktopSetField(c, locales.DesktopSetRTCLocal(), desktopSetTimeYesNo(page.data.LocalRTC))
			}
		})
	})
	ready := page.load.loaded && !page.data.Clock.IsZero() && page.load.cancel == nil && page.load.failure == "" && state.dialog == nil
	refresh := func() { a.desktopSetRefreshTime(window, state) }
	a.desktopSetZoneChooser(c, window, state, ready, refresh)
	a.desktopSetTimeSync(c, window, state, ready, refresh)
}

func (a *App) desktopSetZoneChooser(c *ui.Context, window *desktopWindow, state *desktopSettingsState, ready bool, refresh func()) {
	page := &state.clock
	desktopSetCard(c).Children(func() {
		desktopSetHeading(c, "globe", locales.DesktopSetZone())
		if len(page.zones) == 0 && page.load.loaded {
			widgets.Muted(c, locales.DesktopSetNoTimeZones())
		}
		ui.TextInput(c, &page.query).FillWidth().Label(locales.DesktopSetSearchZones()).Placeholder(locales.DesktopSetSearchZones()).Disabled(!ready || len(page.zones) == 0)
		query := strings.ToLower(strings.TrimSpace(page.query))
		var filtered []string
		for _, zone := range page.zones {
			if strings.Contains(strings.ToLower(zone), query) {
				filtered = append(filtered, zone)
			}
		}
		if len(filtered) > 0 {
			label := desktopSetValue(page.zone, locales.DesktopSetChooseZone())
			ui.Button(c, label+" ▾").AlignSelf(ui.Start).Disabled(!ready).PopoverMenuButton(func(menu *ui.Menu) {
				for _, zone := range filtered[:min(16, len(filtered))] {
					if menu.Item(zone).Radio(page.zone == zone).Chosen() {
						page.zone = zone
					}
				}
			})
			if len(filtered) > 16 {
				widgets.Muted(c, locales.DesktopSetZoneMatches(len(filtered))).FontSize(11)
			}
		} else if query != "" {
			widgets.Muted(c, locales.DesktopSetNoZoneMatches())
		}
		change, err := monitor.BuildSysConfZone(page.zone, page.data.Timedatectl)
		valid := err == nil && ready && page.zone != page.data.Zone && slices.Contains(page.zones, page.zone)
		if ui.PrimaryButton(c, locales.DesktopSetSetZone()).AlignSelf(ui.Start).Disabled(!valid).Clicked() {
			a.desktopSetChange(window, state, locales.DesktopSetSetZone(), locales.DesktopSetZoneNote(), change, false, refresh)
		}
	})
}

func (a *App) desktopSetTimeSync(c *ui.Context, window *desktopWindow, state *desktopSettingsState, ready bool, refresh func()) {
	page := &state.clock
	ntp := page.data.NTP == "yes"
	ntpLabel := locales.DesktopSetEnableNTP()
	if ntp {
		ntpLabel = locales.DesktopSetDisableNTP()
	}
	sync, supported := monitor.BuildSysConfSync(page.data.Sync)
	canNTP := ready && page.data.Timedatectl && page.data.CanNTP && (page.data.NTP == "yes" || page.data.NTP == "no")
	desktopSetCard(c).ContextMenu(func(menu *ui.Menu) {
		if menu.Item(ntpLabel).Disabled(!canNTP).Chosen() {
			a.desktopSetChange(window, state, ntpLabel, locales.DesktopSetNTPNote(), monitor.BuildSysConfNTP(!ntp), false, refresh)
		}
		if menu.Item(locales.DesktopSetSyncNow()).Disabled(!ready || !supported).Chosen() {
			a.desktopSetChange(window, state, locales.DesktopSetSyncNow(), locales.DesktopSetSyncNote(), sync, false, refresh)
		}
	}).Children(func() {
		desktopSetHeading(c, "refresh-cw", locales.DesktopSetSync())
		widgets.Muted(c, locales.DesktopSetSyncNote()).FillWidth()
		if page.load.loaded && !page.data.CanNTP && !supported {
			widgets.Muted(c, locales.DesktopSetSyncUnavailable()).FillWidth()
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c, ntpLabel).Disabled(!canNTP).Clicked() {
				a.desktopSetChange(window, state, ntpLabel, locales.DesktopSetNTPNote(), monitor.BuildSysConfNTP(!ntp), false, refresh)
			}
			if ui.Button(c, locales.DesktopSetSyncNow()).Disabled(!ready || !supported).Clicked() {
				a.desktopSetChange(window, state, locales.DesktopSetSyncNow(), locales.DesktopSetSyncNote(), sync, false, refresh)
			}
		})
	})
}

func desktopSetTimeYesNo(value string) string {
	switch value {
	case "yes":
		return locales.DesktopSetYes()
	case "no":
		return locales.DesktopSetNo()
	default:
		return locales.DesktopSetUnknown()
	}
}
