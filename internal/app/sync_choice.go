package app

// The choice of a sync that may hold an old copy (choiceRequest): this
// device's connections and the remote's differ, and nothing tells which
// side is newer, as on a computer that never synced there, whose base no
// longer opens (another changed the sync password), or that has not
// synced for a month. The window shows both sides, and syncs as the user
// chooses: merged, the remote's version where they differ; the remote's,
// here; or this device's, on the remote. The Sync page's sync and the
// account's each have their own.

import (
	"time"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The windows of the choices.
const (
	WindowSyncChoice    WindowKind = "sync-choice"
	WindowAccountChoice WindowKind = "account-sync-choice"
)

// staleAfter is how long without a sync makes this device's copy one
// that may be old: its next sync asks, as a first one does.
const staleAfter = 30 * 24 * time.Hour

// The choices.
const (
	choiceMerge  = "merge"  // both sides, the remote's where they differ
	choiceRemote = "remote" // the remote's, here
	choiceLocal  = "local"  // this device's, on the remote
)

// choiceRequest sets how a sync merges. One the user did not choose asks
// when it may hold an old copy (cloudsync.Request.AskChoice), stale
// after staleAfter; one chosen runs as chosen: merged, keeping the
// remote's where they differ, or KeepServer or Force, set from options.
func choiceRequest(req *cloudsync.Request, state config.SyncState, options syncOptions, now time.Time) {
	switch {
	case options.choice == choiceMerge:
		req.PreferRemote = true
	case options.choice != "", options.force, options.keepServer:
	default:
		req.AskChoice = true
		req.Stale = !state.At.IsZero() && now.Sub(state.At) > staleAfter
	}
}

// choiceWindow is the window of the choice of the sync t.
func choiceWindow(t syncTarget) WindowKind {
	if t.account {
		return WindowAccountChoice
	}
	return WindowSyncChoice
}

// syncReadyFor says whether the sync t can run now.
func (a *App) syncReadyFor(t syncTarget) bool {
	if t.account {
		return a.accountReady()
	}
	return a.syncReady()
}

// openSyncChoice opens the window of the choice of the sync t, merging
// chosen.
func (a *App) openSyncChoice(t syncTarget) {
	if t.st.choice == nil {
		return
	}
	t.st.choicePick = choiceMerge
	a.openWindow(WindowSpec{
		Kind: choiceWindow(t), Title: locales.AccountChoiceTitle,
		Width: 560, Height: 560, MinWidth: 480, MinHeight: 520,
	}, func(c *ui.Context) { a.syncChoiceView(c, t) })
}

// chooseSync syncs t as the user chose. Taking the remote's applies what
// it has then, a write of another device since included; the deletions a
// choice makes are approved with it.
func (a *App) chooseSync(t syncTarget, choice string, now time.Time) {
	if t.st.choice == nil || a.syncBusy() || !a.syncReadyFor(t) {
		return
	}
	if t.account {
		a.logAccount(config.AccountEvent{Action: config.AccountChoice, Reason: choice, Result: "ok"})
	}
	options := syncOptions{reason: cloudsync.ReasonManual, choice: choice}
	switch choice {
	case choiceRemote:
		options.keepServer, options.allowDeletion = true, true
	case choiceLocal:
		options.force, options.allowDeletion = true, true
	}
	t.st.choice = nil
	a.closeWindow(choiceWindow(t))
	a.startSyncOn(t, options, now)
}

// syncChoiceView is the window of the choice of the sync t.
func (a *App) syncChoiceView(c *ui.Context, t syncTarget) {
	ch, st := t.st.choice, t.st
	if ch == nil {
		return
	}
	stale := !st.state.At.IsZero()
	ui.Column(c).Fill().Gap(14).Padding(18).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Get("cloud-alert")).FontSize(22).TextColor(theme.Warning)
			ui.Text(c, locales.AccountChoiceTitle()).Bold().FontSize(16)
		})
		intro := locales.AccountChoiceFirst()
		if stale {
			days := int(c.Now().Sub(st.state.At).Hours() / 24)
			intro = locales.AccountChoiceStale(days)
		}
		widgets.Muted(c, intro).FontSize(12)

		// The two sides.
		ui.Row(c).Gap(10).Children(func() {
			side := func(icon, title, count, detail string) {
				ui.Column(c).Grow(1).Basis(0).Gap(4).Padding(10, 12).Radius(8).Border(1, theme.Border).Background(colors.note).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons.Get(icon)).FontSize(13).TextColor(theme.TextMuted)
						widgets.Muted(c, title).FontSize(12)
					})
					ui.Text(c, count).Bold().FontSize(18)
					if detail != "" {
						widgets.Muted(c, detail).FontSize(11).SingleLine().MinWidth(0)
					}
				})
			}
			side("monitor", locales.AccountChoiceHere(), locales.AccountChoiceCount(ch.Local), deviceName())
			detail := ch.RemoteDevice
			if !ch.RemoteAt.IsZero() {
				detail = locales.AccountChoiceRemoteBy(ch.RemoteDevice, ch.RemoteAt.Local().Format("2006-01-02 15:04"))
			}
			side("cloud", locales.AccountChoiceCloud(), locales.AccountChoiceCount(ch.Remote), detail)
		})
		widgets.Muted(c, locales.AccountChoiceDiff(ch.OnlyLocal, ch.OnlyRemote, ch.Differ, ch.Same)).FontSize(12)

		// The choices.
		option := func(value, title, detail string, color ui.Color) {
			on := st.choicePick == value
			card := ui.Row(c.Key("choice|"+value)).Gap(10).Padding(10, 12).Radius(8).Role(ui.RoleRadio).Checked(on).Label(title)
			if on {
				card.Border(1.5, theme.Accent).Background(theme.Accent.Alpha(0.06))
			} else {
				card.Border(1, theme.Border)
				if card.Hovered() {
					card.Background(theme.SurfaceHover)
				}
			}
			if card.Clicked() {
				st.choicePick = value
			}
			card.Children(func() {
				ring := theme.Border
				if on {
					ring = theme.Accent
				}
				ui.Box(c).Size(16, 16).Radius(8).Border(1.5, ring).Center().Shrink(0).Margin(1, 0, 0, 0).Children(func() {
					if on {
						ui.Box(c).Size(8, 8).Radius(4).Background(theme.Accent)
					}
				})
				ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
					ui.Text(c, title).Bold().FontSize(13).TextColor(color)
					widgets.Muted(c, detail).FontSize(12)
				})
			})
		}
		option(choiceMerge, locales.AccountChoiceMergeTitle(), locales.AccountChoiceMergeDetail(ch.Merged), theme.Text)
		option(choiceRemote, locales.AccountChoiceRemoteTitle(), locales.AccountChoiceRemoteDetail(ch.Remote, ch.OnlyLocal), theme.Text)
		option(choiceLocal, locales.AccountChoiceLocalTitle(), locales.AccountChoiceLocalDetail(ch.Local, ch.OnlyRemote), theme.Danger)

		ui.Spacer(c)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.AccountChoiceLater()).Clicked() {
				a.closeWindow(choiceWindow(t))
			}
			label := locales.AccountChoiceConfirm()
			if st.choicePick == choiceLocal {
				label = locales.AccountChoiceLocalConfirm()
			}
			if ui.PrimaryButton(c, label).Disabled(a.syncBusy() || !a.syncReadyFor(t)).Clicked() {
				a.chooseSync(t, st.choicePick, c.Now())
			}
		})
	})
}
