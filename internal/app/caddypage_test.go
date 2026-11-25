package app

import (
	"testing"

	"finallyshell/locales"
)

// The Caddy tab shows its sections and switches between them.
func TestCaddyPage(t *testing.T) {
	a, _, tt, _ := overviewApp(t)
	a.openPanelTab(a.sessions[0], tabCaddy)
	tt.Frame()
	wantTexts(t, tt, "sections", locales.CaddyOverview(), locales.CaddySites(), locales.CaddyGlobal(), locales.CaddyConfig(),
		locales.CaddyCerts(), locales.CaddyLogs(), locales.CaddyInstall())
	for section := range caddySectionNames() {
		a.sessions[0].caddy.section = section
		tt.Frame()
	}
	a.openCaddyPage(a.sessions[0], caddySecConfig)
	tt.Frame()
	if *a.shownBottomTab() != tabCaddy || a.sessions[0].caddy.section != caddySecConfig {
		t.Fatalf("tab %d section %d", *a.shownBottomTab(), a.sessions[0].caddy.section)
	}
}
