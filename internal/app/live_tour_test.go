package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestLiveTour opens every tab of the bottom panel, every section of the
// System tab and every app of the desktop on a real server
// (FINALLYSHELL_TEST_SSH), only reading: each must draw without freezing.
// With SHOT_DIR, it saves a screenshot of each, to look over what a kind
// of server shows, as on OpenWrt's BusyBox.
func TestLiveTour(t *testing.T) {
	a, p, s := connectTestServer(t)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	// A panel row keeps the tab it opened on: each is chosen in it.
	show := func(tab int) {
		a.openPanelTab(s, tab)
		a.panelRowOf(s).panelTab = tab
	}
	for tab, b := range a.bottomTabs() {
		if tab == tabSystem {
			p.until(t, "the OpenWrt probe", func() bool { return s.server.openwrt.checked })
			if s.isOpenWrt() {
				for section, name := range owSectionNames() {
					t.Run("openwrt-"+name, func(t *testing.T) {
						show(tabSystem)
						s.server.openwrt.section = section
						a.refreshServerSection(s)
						tourFrames(t, p, tt, "tour-openwrt-"+name)
					})
				}
				continue
			}
			for section, name := range serverSectionNames() {
				t.Run("system-"+name, func(t *testing.T) {
					show(tabSystem)
					s.server.section = section
					a.refreshServerSection(s)
					tourFrames(t, p, tt, "tour-system-"+name)
				})
			}
			continue
		}
		t.Run("tab-"+b.label, func(t *testing.T) {
			show(tab)
			tourFrames(t, p, tt, "tour-tab-"+b.label)
		})
	}
	for _, app := range desktopApps {
		// The server tools are the tabs above, in a window.
		if strings.HasPrefix(string(app.id), "srv-") || app.available != nil && !app.available(s) {
			continue
		}
		t.Run("desktop-"+string(app.id), func(t *testing.T) {
			a.openDesktop(s)
			w := a.desktopOpen(s, app.id, nil)
			if w == nil {
				t.Fatal("no window")
			}
			tourFrames(t, p, tt, "tour-desktop-"+string(app.id))
			a.desktopClose(w)
		})
	}
}

// tourFrames draws about 6 seconds of frames, running what the background
// reads post, then logs the texts on screen and saves a screenshot.
func tourFrames(t *testing.T, p *fakePlatform, tt *ui.Tester, name string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 60 {
			tt.Frame()
			for pending := true; pending; {
				select {
				case fn := <-p.posts:
					fn()
				default:
					pending = false
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s froze", name)
	}
	t.Logf("%s: %q", name, firstTexts(tt.Texts(), 80))
	saveShot(t, tt, name+".png")
}
