package app

import (
	"testing"

	"finallyshell/locales"
)

// The System tab shows its sections and switches between them.
func TestServerPage(t *testing.T) {
	a, _, tt, _ := overviewApp(t)
	a.openPanelTab(a.sessions[0], tabSystem)
	tt.Frame()
	wantTexts(t, tt, "sections", locales.ServerServices(), locales.ServerLogs(), locales.ServerCron(), locales.ServerDisk(),
		locales.ServerUpdates(), locales.ServerSecurity(), locales.ServerFirewall(), locales.ServerSites())
	for section := range serverSectionNames() {
		a.sessions[0].server.section = section
		tt.Frame()
	}
	a.openServerPage(a.sessions[0], serverSecCron)
	tt.Frame()
	if *a.shownBottomTab() != tabSystem || a.sessions[0].server.section != serverSecCron {
		t.Fatalf("tab %d section %d", *a.shownBottomTab(), a.sessions[0].server.section)
	}
	saveShot(t, tt, "server-page.png")
}
