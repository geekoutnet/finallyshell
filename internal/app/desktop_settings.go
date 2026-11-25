package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func init() {
	registerDesktopApp(desktopApp{
		id: appSettings, icon: "settings", tint: ui.Hex("#5a6578"), name: locales.DesktopAppSettings,
		category: desktopCatSystem, order: 50, onDesktop: true, pinned: true,
		size: desktopSize{W: 880, H: 580}, minSize: desktopSize{W: 450, H: 300}, single: true,
		view: (*App).desktopSettingsView, opened: desktopSettingsOpened, closing: desktopSettingsClosing,
	})
}

var desktopSettingsPages = []struct {
	id, icon string
	title    func() string
}{
	{"info", "monitor", locales.SystemInfo},
	{"personalize", "palette", locales.DesktopSetPersonalize},
	{"log", "layout-list", locales.DesktopSetLog},
	{"dns", "globe", locales.DesktopSetDNS},
	{"time", "clock", locales.DesktopSetTime},
	{"security", "shield", locales.DesktopSetSecurity},
}

type desktopSettingsState struct {
	nav    widgets.NavState
	client *ssh.Client
	online bool
	dialog *systemDialog
	clear  bool
	info   desktopSetInfoState
	log    desktopSetLogState
	dns    desktopSetDNSState
	clock  desktopSetTimeState
}

type desktopSetLoad struct {
	loaded  bool
	cancel  context.CancelFunc
	failure string
}

func desktopSettingsOpened(_ *App, window *desktopWindow, arg any) {
	state, ok := window.state.(*desktopSettingsState)
	if !ok {
		state = &desktopSettingsState{nav: widgets.NavState{Page: "info"}}
		window.state = state
	}
	if page, ok := arg.(string); ok {
		for _, item := range desktopSettingsPages {
			if page == item.id {
				state.nav.Page = page
				return
			}
		}
	}
}

func desktopSettingsClosing(_ *App, window *desktopWindow) bool {
	window.state.(*desktopSettingsState).stop()
	return true
}

func (state *desktopSettingsState) stop() {
	for _, load := range []*desktopSetLoad{&state.info.load, &state.info.detect, &state.log.load, &state.dns.load, &state.dns.probe, &state.clock.load} {
		if load.cancel != nil {
			load.cancel()
		}
	}
	if state.dialog != nil && state.dialog.cancel != nil {
		state.dialog.cancel()
	}
}

func (a *App) desktopSettingsView(c *ui.Context, window *desktopWindow) {
	state := window.state.(*desktopSettingsState)
	session := window.session
	online := systemReady(session, session.client) && (session.client != nil || session.server.services.run != nil)
	if state.online != online || state.client != session.client {
		state.stop()
		state = &desktopSettingsState{nav: state.nav, client: session.client, online: online}
		window.state = state
	}
	width := window.w
	layout := ui.Row(c).Fill().MinHeight(0).AlignItems(ui.Stretch).Background(colors.overviewPage)
	actual := layout.Bounds().W
	if actual > 0 {
		width = actual
	} else if window.maximized || window.snapped != "" {
		width = a.desktopOf(session).area.W
		if window.snapped != "" {
			width /= 2
		}
	}
	compact := width < 620
	navWidth := float32(widgets.NavWidth - 12)
	if compact {
		navWidth = 48
	}
	layout.Children(func() {
		ui.Scroll(c).Width(navWidth).Shrink(0).Background(colors.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).Children(func() {
			ui.Column(c).Padding(10, 5).Gap(4).Children(func() {
				for _, item := range desktopSettingsPages {
					label := item.title()
					active := state.nav.Page == item.id
					row := ui.Row(c.Key(item.id)).Height(36).Gap(8).AlignItems(ui.Center).PaddingX(8).Radius(7).Role(ui.RoleButton).Label(label).Tooltip(label)
					tint := theme.TextMuted
					if active {
						row.Background(theme.Accent.Alpha(0.16))
						tint = theme.Accent
					} else if row.Hovered() {
						row.Background(theme.Text.Alpha(0.06))
					}
					if row.Clicked() {
						state.nav.Page = item.id
					}
					row.Children(func() {
						ui.Icon(c, icons.Get(item.icon)).FontSize(16).TextColor(tint).Shrink(0)
						if !compact {
							ui.Text(c, label).FontSize(12).MinWidth(0)
						}
					})
				}
			})
		})
		for _, item := range desktopSettingsPages {
			if item.id != state.nav.Page {
				continue
			}
			ui.Column(c.Key(item.id)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				ui.Text(c, item.title()).FontSize(21).Bold().FillWidth().Padding(11, 15, 8, 15).Shrink(0)
				ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().KeepScrollbars(true).Children(func() {
					ui.Column(c).Padding(2, 14, 14, 14).Gap(9).FillWidth().Children(func() {
						switch item.id {
						case "info":
							a.desktopSetInfo(c, window, state, compact)
						case "personalize":
							a.desktopPersonalizePage(c)
						case "log":
							a.desktopSetLog(c, window, state)
						case "dns":
							a.desktopSetDNS(c, window, state)
						case "time":
							a.desktopSetTime(c, window, state)
						case "security":
							a.desktopSetSecurity(c, session)
						}
					})
				})
			})
			break
		}
	})
	if state.clear {
		ui.Modal(c, &state.clear, func() {
			ui.Column(c).Width(330).Gap(10).Children(func() {
				ui.Text(c, locales.DesktopSetClearQuestion()).FontSize(16).Bold()
				widgets.Muted(c, locales.DesktopSetClearNote())
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, locales.Cancel()).Clicked() {
						state.clear = false
					}
					if ui.PrimaryButton(c, locales.SvcConfirm()).Clicked() {
						a.desktopOf(session).log = nil
						state.log.app, state.log.search = "", ""
						state.clear = false
					}
				})
			})
		})
	}
	a.systemDialogView(c, session, &state.dialog)
}

func desktopSetCard(c *ui.Context) ui.Element {
	return ui.Column(c).FillWidth().MinWidth(0).Padding(10, 12).Gap(7).Radius(9).Background(colors.card).Border(1, colors.line)
}

func desktopSetHeading(c *ui.Context, icon, title string) {
	ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(15).TextColor(theme.Accent).Shrink(0)
		ui.Text(c, title).Bold().MinWidth(0)
	})
}

func desktopSetField(c *ui.Context, label, value string) {
	if value == "" {
		value = locales.DesktopSetUnknown()
	}
	ui.Column(c).MinWidth(0).Gap(1).Children(func() {
		widgets.Muted(c, label).FontSize(11)
		ui.Text(c, value).FillWidth().Selectable()
	})
}

func desktopSetReadNote(c *ui.Context, load *desktopSetLoad) {
	if load.cancel != nil {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Spinner(c).Label(locales.SvcWorking())
			widgets.Muted(c, locales.SvcWorking())
			if ui.Button(c, locales.Cancel()).Clicked() {
				load.cancel()
			}
		})
	}
	if load.failure != "" {
		ui.Text(c, load.failure).TextColor(theme.Danger).Selectable()
	}
}

func desktopSetOffline(c *ui.Context, session *Session) {
	if !execAllowed(session.Host) {
		widgets.Muted(c, locales.SysInfoOff())
	} else {
		widgets.Muted(c, locales.NotConnected())
	}
}

func desktopSetRead[T any](a *App, window *desktopWindow, state *desktopSettingsState, load *desktopSetLoad, work func(context.Context) (T, string, error), accept func(T)) {
	if !state.online || load.cancel != nil {
		return
	}
	ctx, cancel := context.WithTimeout(systemContext(window.session), 45*time.Second)
	load.cancel, load.failure = cancel, ""
	go func() {
		defer cancel()
		value, stderr, err := work(ctx)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		a.post(func() {
			if window.state != state || a.desktopOf(window.session) == nil || !systemReady(window.session, state.client) {
				return
			}
			load.cancel, load.loaded = nil, true
			load.failure = systemError(stderr, err)
			if err == nil {
				accept(value)
			}
		})
	}()
}

func (a *App) desktopSetRoot(session *Session) systemRunner {
	privilege := a.sessionPrivilege(session).runner
	return func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		return privilege.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input, ScriptStdin: true, Interrupt: progress != nil, Progress: progress})
	}
}

func (a *App) desktopSetChange(window *desktopWindow, state *desktopSettingsState, title, note string, change monitor.SysConfChange, verify bool, refresh func()) {
	if !state.online || state.dialog != nil {
		return
	}
	session := window.session
	dialog := &systemDialog{client: state.client, title: title, consequence: note, target: change.Target}
	state.dialog = dialog
	dialog.confirm = func() {
		root, read := a.desktopSetRoot(session), systemReader(session)
		logged := func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
			var streamed atomic.Bool
			out, stderr, err := root(ctx, script, input, func(chunk string) {
				streamed.Store(true)
				progress(chunk)
			})
			if strings.TrimSpace(stderr) == monitor.SysConfDNSChanged {
				stderr = locales.DesktopSetDNSChanged()
			}
			if err == nil && verify {
				if !streamed.Load() && strings.TrimSpace(out) != "" {
					progress(out)
				}
				progress("\n" + monitor.SysConfDNSProbe)
				value, verifyErr, probeErr := read(ctx, monitor.SysConfDNSProbe, nil, nil)
				progress(value)
				if probeErr != nil {
					stderr, err = verifyErr, probeErr
				}
			}
			a.post(func() {
				var logErr error
				if err != nil {
					logErr = errors.New(systemError(stderr, err))
				}
				a.desktopLog(session, appSettings, title+" · "+change.Target, logErr)
			})
			return out, stderr, err
		}
		a.systemWork(session, dialog, logged, change.Script, nil, func(*Session) {
			if window.state == state {
				refresh()
			}
		})
	}
}

func (a *App) desktopSetSecurity(c *ui.Context, session *Session) {
	for _, item := range []struct {
		icon, title, note string
		open              func()
	}{
		{"lock", locales.DesktopSetAppLock(), locales.DesktopSetAppLockNote(), func() {
			a.optionsNav.Page = pageSecurity
			a.openOptions()
			a.redraw()
		}},
		{"key-round", locales.KeysTitle(), locales.DesktopSetKeysNote(), func() { a.openKeys(WindowMain, "") }},
		{"shield", locales.DesktopSetServerSecurity(), locales.DesktopSetServerSecurityNote(), func() {
			if desktopAppByID("srv-security") != nil {
				a.desktopOpen(session, "srv-security", nil)
				return
			}
			session.server.section = serverSecSecurity
			a.openPanelTab(session, tabSystem)
			a.openBottomTab(tabSystem)
		}},
	} {
		desktopSetCard(c.Key(item.icon)).Children(func() {
			desktopSetHeading(c, item.icon, item.title)
			widgets.Muted(c, item.note).FillWidth()
			if ui.Button(c, locales.DesktopSetOpen(item.title)).AlignSelf(ui.Start).Clicked() {
				item.open()
			}
		})
	}
}
