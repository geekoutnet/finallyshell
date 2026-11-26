package app

import (
	"strconv"

	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The update card: when a check in the background finds a newer version,
// a card at the bottom right of the main window says so, as most desktop
// apps do, with Update and Later. Update downloads and installs it,
// showing the progress on the card, and the app restarts; Later hides the
// card for this version until the app starts again. Options › General ›
// Updates installs it at any time.

// CheckForUpdatesNow is the tray menu's Check for Updates: Options ›
// General, which shows how the check goes, checking at once. A newer
// version shows on the update card too.
func (a *App) CheckForUpdatesNow() {
	if a.Locked() {
		return
	}
	a.optionsNav.Page = pageGeneral
	a.openOptions()
	a.plat.CheckForUpdates()
	a.redraw()
}

// updateCardW is the card's width.
const updateCardW = 352

// updateCard shows the newer version the updater found, unless the user
// put it off, or the app is locked.
func (a *App) updateCard(c *ui.Context) {
	st := a.plat.Updates()
	busy := st.Downloading || st.Installing
	if st.Blocked != UpdatesEnabled || st.Available == "" || a.Locked() || a.fullScreen.on ||
		a.updateLater == st.Available && !busy {
		return
	}
	t := c.Theme()
	ui.Overlay(c, func() {
		card := ui.Column(c.Key("update-card")).Absolute().Right(18).Bottom(18).Width(updateCardW).MaxWidthPercent(90).
			Padding(14, 16, 14, 16).Gap(10).Radius(12).Background(t.Background).Border(1, t.Border).
			Shadow(0, 12, 32, 0, ui.RGBA(8, 11, 28, 0.22)).Role(ui.RoleAlert).Label(locales.UpdateCardTitle(st.Available))
		card.Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Start).FillWidth().Children(func() {
				ui.Box(c).Size(36, 36).Radius(10).Center().Shrink(0).Background(t.Accent.Alpha(0.14)).Children(func() {
					ui.Icon(c, icons.Get("circle-arrow-down")).FontSize(19).TextColor(t.Accent)
				})
				ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, locales.UpdateCardTitle(st.Available)).Bold().FontSize(14).MaxWidthPercent(100)
					sub := locales.UpdateCardCurrent(st.Version)
					if st.CanInstall {
						sub += " · " + locales.UpdateCardRestart()
					}
					widgets.Muted(c, sub).FontSize(12).MaxWidthPercent(100)
					ui.Link(c, locales.UpdateCardNotes(), releasesURL+"/tag/v"+st.Available).FontSize(12)
				})
				if !busy && widgets.IconButton(c, "x", locales.UpdateCardLater()).Clicked() {
					a.updateLater = st.Available
				}
			})
			switch {
			case st.Installing:
				widgets.Muted(c, locales.UpdatesRestarting()).FontSize(12)
			case st.Downloading:
				ui.Column(c).Gap(5).FillWidth().Children(func() {
					ui.Row(c).FillWidth().Children(func() {
						widgets.Muted(c, locales.UpdatesDownloading(strconv.Itoa(int(st.Progress*100)))).FontSize(12)
					})
					ui.Box(c).FillWidth().Height(5).Radius(3).Background(t.Text.Alpha(0.08)).Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * min(max(st.Progress, 0), 1), H: r.H}, t.Accent, 3)
					})
				})
			default:
				if st.InstallErr {
					ui.Text(c, locales.UpdatesInstallFailed()).FontSize(12).TextColor(t.Danger).MaxWidthPercent(100)
				}
				ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
					if ui.Button(c, locales.UpdateCardLater()).Clicked() {
						a.updateLater = st.Available
					}
					switch {
					case !st.CanInstall:
						ui.Link(c, locales.UpdateCardDownload(), releasesURL+"/tag/v"+st.Available)
					case st.InstallErr:
						if ui.PrimaryButton(c, locales.UpdateCardRetry()).Clicked() {
							a.plat.InstallUpdate()
						}
					default:
						if ui.PrimaryButton(c, locales.UpdateCardInstall()).Clicked() {
							a.plat.InstallUpdate()
						}
					}
				})
			}
		})
	})
}
