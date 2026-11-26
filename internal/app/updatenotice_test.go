package app

import (
	"testing"

	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// TestUpdateCard shows a newer version found in the background at the
// bottom right of the main window: Update installs it, with its progress
// on the card, and Later puts it off until a newer one comes.
func TestUpdateCard(t *testing.T) {
	a, p := testApp(t)
	demoSession(a, "web-1", "10.0.0.11")
	tt := ui.NewTester(a.View, 1100, 700)
	p.tt = tt
	p.status = UpdateStatus{Version: "2.0.23", Automatic: true}
	tt.Frame()
	if tt.HasText(locales.UpdateCardTitle("2.0.24")) {
		t.Fatal("a card with no newer version")
	}
	p.status.Available, p.status.CanInstall = "2.0.24", true
	tt.Frame()
	if !tt.HasText(locales.UpdateCardTitle("2.0.24")) || !tt.HasText(locales.UpdateCardCurrent("2.0.23")) {
		t.Fatalf("no card: %q", tt.Texts())
	}
	r, _ := tt.Find(locales.UpdateCardInstall())
	if r.X < 1100/2 || r.Y < 700/2 {
		t.Errorf("the card is not at the bottom right: %+v", r)
	}
	saveShot(t, tt, "update-card.png")
	if err := tt.Click(locales.UpdateCardInstall()); err != nil {
		t.Fatal(err)
	}
	if p.installs != 1 {
		t.Fatalf("%d installs", p.installs)
	}
	p.status.Downloading, p.status.Progress = true, 0.45
	tt.Frame()
	if !tt.HasText(locales.UpdatesDownloading("45")) || tt.HasText(locales.UpdateCardLater()) {
		t.Fatalf("downloading: %q", tt.Texts())
	}
	saveShot(t, tt, "update-card-downloading.png")
	// A failed download offers to try again.
	p.status.Downloading, p.status.InstallErr = false, true
	tt.Frame()
	if !tt.HasText(locales.UpdatesInstallFailed()) || !tt.HasText(locales.UpdateCardRetry()) {
		t.Fatalf("failed: %q", tt.Texts())
	}
	p.status.InstallErr = false
	tt.Frame()
	if err := tt.Click(locales.UpdateCardLater()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(locales.UpdateCardTitle("2.0.24")) {
		t.Fatal("Later left the card")
	}
	// A newer version comes back; one this copy cannot install links to
	// its download.
	p.status.Available, p.status.CanInstall = "2.0.25", false
	tt.Frame()
	if !tt.HasText(locales.UpdateCardTitle("2.0.25")) || !tt.HasText(locales.UpdateCardDownload()) || tt.HasText(locales.UpdateCardInstall()) {
		t.Fatalf("a newer version: %q", tt.Texts())
	}
	// A development build shows none.
	p.status.Blocked = UpdatesDevelopment
	tt.Frame()
	if tt.HasText(locales.UpdateCardTitle("2.0.25")) {
		t.Fatal("a development build shows the card")
	}
}

// TestCheckForUpdatesNow, the tray menu's, opens Options › General and
// checks with this project's updater.
func TestCheckForUpdatesNow(t *testing.T) {
	a, p := testApp(t)
	a.optionsNav.Page = pageSecurity
	a.CheckForUpdatesNow()
	if p.checks != 1 || !a.windowOpen(WindowOptions) || a.optionsNav.Page != pageGeneral {
		t.Fatalf("checks %d, options open %v on %q", p.checks, a.windowOpen(WindowOptions), a.optionsNav.Page)
	}
}
