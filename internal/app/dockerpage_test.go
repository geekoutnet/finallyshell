package app

import (
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// dockerPageApp is an app on the Docker tab of a server whose Docker is
// d, read by the fake.
func dockerPageApp(t *testing.T, d monitor.Docker) (*App, *fakePlatform, *ui.Tester, *fakeDocker) {
	t.Helper()
	a, p, tt, fd := dockerApp(t, d)
	a.openDockerPage(a.sessions[0], dockerSecContainers)
	tt.Frame()
	tt.Frame()
	return a, p, tt, fd
}

// The Overview's Docker card opens the Docker tab, which shows the
// sections and switches between them.
func TestDockerPage(t *testing.T) {
	a, _, tt, _ := dockerApp(t, demoDocker())
	a.openPanelTab(a.sessions[0], tabOverview) // room for the card
	tt.Frame()
	b, _ := tt.Access(locales.DockerPageManage())
	tt.Scroll(b.Bounds.X, 300, 0, 5000) // the card is at the Overview's bottom
	tt.Frame()
	clickNamed(t, tt, locales.DockerPageManage())
	if *a.shownBottomTab() != tabDocker {
		t.Fatalf("tab %d, want the Docker tab", *a.shownBottomTab())
	}
	wantTexts(t, tt, "sections", locales.DockerContainers(), locales.DockerImages(), locales.DockerPageCompose(),
		locales.DockerNetworks(), locales.DockerVolumes(), locales.DockerPageSystem())
	for section := range dockerSectionNames() {
		a.sessions[0].docker.section = section
		tt.Frame()
	}
	saveShot(t, tt, "docker-page.png")
}

// A server without Docker says so.
func TestDockerPageNone(t *testing.T) {
	_, _, tt, _ := dockerPageApp(t, monitor.Docker{})
	wantTexts(t, tt, "no Docker", locales.DockerPageNone())
}
