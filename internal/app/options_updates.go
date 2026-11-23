package app

import (
	"strconv"

	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// releasesURL is where the releases are published: a newer version a
// check found links to its page there, to download it by hand.
const releasesURL = "https://github.com/geekoutnet/finallyshell/releases"

// UpdateStatus is what the app's updater knows, for the Updates row of
// Options › General.
type UpdateStatus struct {
	// Version is the version running, "" for a development build.
	Version string
	// Blocked is why this copy cannot update itself; UpdatesEnabled when
	// it can.
	Blocked UpdateBlock
	// Automatic is whether it checks for updates in the background.
	Automatic bool
	// Available is the newer version the last check found, "" for none
	// (or one the user skipped).
	Available string
	// Checking is a check under way; UpToDate and Failed tell how the
	// check the user asked for ended: no newer version, or none read.
	Checking, UpToDate, Failed bool
	// CanInstall is whether this copy installs Available itself
	// (InstallUpdate); else its release page is linked.
	CanInstall bool
	// Downloading is the download of Available, Progress of it from 0 to
	// 1; InstallErr is a download or install that failed, Installing one
	// that waits for the app to quit.
	Downloading, InstallErr, Installing bool
	Progress                            float32
}

// UpdateBlock is why a copy of the app cannot update itself.
type UpdateBlock int

const (
	UpdatesEnabled UpdateBlock = iota
	// UpdatesDevelopment: a development build (go run, mygo dev).
	UpdatesDevelopment
	// UpdatesManaged: it cannot write where it is installed, as the .deb
	// in /opt, which the package manager updates.
	UpdatesManaged
)

// updatesSection is the Updates row of the General page: the version, and
// checking for updates now or in the background, or why this copy cannot
// update itself and where to get a new version. The update window itself
// is MyGo's updater plugin's (main.go).
func (a *App) updatesSection(c *ui.Context) {
	st := a.plat.Updates()
	ui.Column(c).Gap(8).Padding(8, 0, 0, 0).Children(func() {
		ui.Text(c, locales.OptionsUpdates()).Bold()
		if st.Version != "" {
			ui.Text(c, locales.UpdatesVersion(st.Version))
		} else {
			ui.Text(c, locales.UpdatesVersionDevelopment())
		}
		if st.Blocked != UpdatesEnabled {
			why := locales.UpdatesManaged()
			if st.Blocked == UpdatesDevelopment {
				why = locales.UpdatesDevelopment()
			}
			widgets.Muted(c, why).FontSize(12)
			ui.Link(c, locales.UpdatesReleasesPage(), releasesURL)
			return
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, locales.CheckForUpdates()).Clicked() {
				a.plat.CheckForUpdates()
			}
			switch {
			case st.Checking:
				widgets.Muted(c, locales.UpdatesChecking())
			case st.Available != "" && st.CanInstall:
				a.updateInstallRow(c, st)
			case st.Available != "":
				ui.Link(c, locales.UpdatesAvailable(st.Available), releasesURL+"/tag/v"+st.Available)
			case st.Failed:
				widgets.Muted(c, locales.UpdatesCheckFailed())
			case st.UpToDate:
				widgets.Muted(c, locales.UpdatesUpToDate())
			}
		})
		auto := st.Automatic
		if ui.Checkbox(c, &auto, locales.OptionCheckUpdatesAutomatically()).Changed() {
			a.plat.SetAutomaticUpdateChecks(auto)
			a.redraw()
		}
	})
}

// updateInstallRow is the newer version found, with the button that
// downloads and installs it, or how that goes.
func (a *App) updateInstallRow(c *ui.Context, st UpdateStatus) {
	ui.Text(c, locales.UpdatesAvailable(st.Available)).TextColor(c.Theme().Accent)
	switch {
	case st.Installing:
		widgets.Muted(c, locales.UpdatesRestarting())
	case st.Downloading:
		widgets.Muted(c, locales.UpdatesDownloading(strconv.Itoa(int(st.Progress*100))))
	default:
		if ui.Button(c, locales.UpdatesInstall()).Clicked() {
			a.plat.InstallUpdate()
		}
		if st.InstallErr {
			widgets.Muted(c, locales.UpdatesInstallFailed())
		}
	}
}
