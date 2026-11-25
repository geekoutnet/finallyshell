package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The Docker and System tabs against a real server (FINALLYSHELL_TEST_SSH):
// every section reads the server and draws, without freezing or crashing.
// They only read. A section that does not finish in time fails the test.
func TestDockerPanelLive(t *testing.T) {
	a, p, s := connectTestServer(t)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	a.openPanelTab(s, tabDocker)
	for section, name := range dockerSectionNames() {
		t.Run(name, func(t *testing.T) {
			s.docker.section = section
			livePanelFrames(t, p, tt, name)
		})
	}
}

func TestServerPanelLive(t *testing.T) {
	a, p, s := connectTestServer(t)
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	a.openPanelTab(s, tabSystem)
	for section, name := range serverSectionNames() {
		t.Run(name, func(t *testing.T) {
			s.server.section = section
			a.refreshServerSection(s)
			livePanelFrames(t, p, tt, name)
		})
	}
}

// livePanelFrames draws about 20 seconds of frames, running what the
// background reads post, then logs the texts on screen.
func livePanelFrames(t *testing.T, p *fakePlatform, tt *ui.Tester, name string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
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
	case <-time.After(60 * time.Second):
		t.Fatalf("the %s section froze", name)
	}
	t.Logf("%s: %q", name, firstTexts(tt.Texts(), 60))
	saveShot(t, tt, "live-"+name+".png")
}
