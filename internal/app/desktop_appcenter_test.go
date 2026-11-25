package app

import (
	"slices"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/locales"
)

func TestDesktopToolsCenterSearchAndLayout(t *testing.T) {
	a, _, tester, session := desktopTestApp(t)
	w := a.desktopOpen(session, appAppCenter, nil)
	state := w.state.(*desktopCenterState)
	w.x, w.y = 120, 28
	tester.Frame()
	clickNamed(t, tester, locales.DesktopCenterSearch())
	tester.Type("mysql")
	if !tester.HasText(locales.DbTab()) || tester.HasText(locales.DesktopCenterDocker()) {
		t.Fatalf("search results: %v", tester.Texts())
	}
	systemClick(t, tester, locales.DesktopCenterTools())
	if !tester.HasText(locales.DesktopCenterEmpty()) {
		t.Fatal("category did not filter matches")
	}
	state.query = "firewall"
	systemClick(t, tester, locales.DesktopCenterServer())
	if !tester.HasText(locales.ServerFirewall()) {
		t.Fatal("server app missing from results")
	}
	app := desktopAppByID("srv-firewall")
	if a.desktopCenterPinned(app) || a.desktopCenterOnDesktop(session, app) {
		t.Fatal("unexpected defaults")
	}
	choose := func(label string) {
		systemClick(t, tester, locales.DesktopCenterActions())
		if err := tester.ChooseMenuItem(label); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	choose(locales.DesktopCenterPin())
	choose(locales.DesktopCenterAdd())
	previous := a.settings.Desktop
	if !a.desktopCenterPinned(app) || !a.desktopCenterOnDesktop(session, app) {
		t.Fatal("settings not applied")
	}
	choose(locales.DesktopCenterUnpin())
	choose(locales.DesktopCenterRemove())
	if !slices.Contains(*previous.Pinned, string(app.id)) || !slices.ContainsFunc(previous.Hosts[session.Host.ID].Icons, func(icon config.DesktopIcon) bool { return icon.App == string(app.id) }) {
		t.Fatal("previous settings were mutated")
	}
	if a.desktopCenterPinned(app) || a.desktopCenterOnDesktop(session, app) {
		t.Fatal("settings not removed")
	}
}

func TestDesktopToolsCenterMoveIconOutOfFolder(t *testing.T) {
	a, _, _, session := desktopTestApp(t)
	app := desktopAppByID("srv-firewall")
	previous := &config.DesktopSettings{Hosts: map[string]config.DesktopHost{
		session.Host.ID: {Icons: []config.DesktopIcon{{App: string(app.id), Folder: "tools", Col: 1, Row: 2}}},
	}}
	a.settings.Desktop = previous
	if a.desktopCenterOnDesktop(session, app) {
		t.Fatal("an icon inside a folder was counted as a desktop icon")
	}
	a.desktopCenterToggleOnDesktop(session, app)
	icons := a.settings.Desktop.Hosts[session.Host.ID].Icons
	if len(icons) != 1 || !a.desktopCenterOnDesktop(session, app) || icons[0].Col != -1 || icons[0].Row != -1 {
		t.Fatalf("icon was not moved to the first free desktop cell: %+v", icons)
	}
	if previous.Hosts[session.Host.ID].Icons[0].Folder != "tools" {
		t.Fatal("previous layout was mutated")
	}
}
