package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func TestDockerResourcesViews(t *testing.T) {
	a, _, tt, _ := dockerPageApp(t, demoDocker())
	s := a.sessions[0]
	state := a.dockerResources(s)
	state.networks.read = true
	state.networks.rows = []monitor.DockerNetworkDetails{{ID: "aa", Name: "bridge", Driver: "bridge"}, {ID: "bb", Name: "docs-net", Driver: "bridge"}}
	s.docker.section = dockerSecNetworks
	tt.Frame()
	wantTexts(t, tt, "networks", "docs-net", "bridge")
	if reason := networkRemoveReason(state.networks.rows[0]); reason != locales.DockerResProtected() {
		t.Fatal(reason)
	}
	state.networks.table.query = "docs-net"
	tt.Frame()
	wantTexts(t, tt, "network filter", locales.DockerResCount(1))
	state.volumes.read = true
	state.volumes.rows = []monitor.DockerVolumeDetails{{Name: "docs-data", Driver: "local", Mountpoint: "/example/docs-data"}}
	s.docker.section = dockerSecVolumes
	tt.Frame()
	wantTexts(t, tt, "volumes", "docs-data", "/example/docs-data")
	s.docker.section = dockerSecSystem
	state.system.disk.read = true
	state.system.disk.rows = []monitor.DockerResourceDisk{{Type: "Images", Size: "101MB", Reclaimable: "40MB"}}
	tt.Frame()
	wantTexts(t, tt, "disk", "101MB", "40MB")
}

type resourceTestCall struct{ script string }

func resourceTestApp(t *testing.T) (*App, *fakePlatform, *Session, *ui.Tester, *dockerResourcesPage, chan resourceTestCall) {
	t.Helper()
	a, p := testApp(t)
	s := demoSession(a, "example", "203.0.113.10")
	done := make(chan struct{})
	term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
	if err != nil {
		close(done)
		t.Fatal(err)
	}
	t.Cleanup(func() { close(done); term.Close() })
	s.setTerm(term)
	term.Feed([]byte("user@example:~$ docker ps\r\nCONTAINER ID   IMAGE     STATUS\r\na1             docs      Up 2 hours\r\nuser@example:~$ "))
	d := demoDocker()
	d.Containers = []monitor.Container{
		{ID: "a1", Name: "docs", Running: true, Status: "running", Mounts: []monitor.Mount{{Type: "volume", Name: "docs-data"}}},
		{ID: "b1", Name: "worker", Status: "exited"},
	}
	s.overview.docker.d, s.overview.docker.read, s.overview.docker.at = d, true, time.Now()
	calls := make(chan resourceTestCall, 100)
	s.overview.docker.run = func(_ context.Context, _ *ssh.Client, script string, progress func(string)) (string, error) {
		calls <- resourceTestCall{script}
		switch {
		case strings.Contains(script, "'network' 'ls'"):
			return "{\"ID\":\"abcd\",\"Name\":\"docs-net\"}\n{\"ID\":\"aa\",\"Name\":\"bridge\"}\n{\"ID\":\"bb\",\"Name\":\"retired\"}", nil
		case strings.Contains(script, "'network' 'inspect'"):
			return `[{"Name":"docs-net","Id":"abcd","Created":"2026-01-02T03:04:05Z","Scope":"local","Driver":"bridge","IPAM":{"Driver":"default","Config":[{"Subnet":"192.0.2.0/24","Gateway":"192.0.2.1"}]},"Containers":{"a1":{"Name":"docs","IPv4Address":"192.0.2.2/24","MacAddress":"02:42:c0:00:02:02"}}},{"Name":"bridge","Id":"aa","Scope":"local","Driver":"bridge"},{"Name":"retired","Id":"bb","Scope":"local","Driver":"bridge","Internal":true}]`, nil
		case strings.Contains(script, "'volume' 'ls' '--filter'"):
			return `{"Name":"scratch"}`, nil
		case strings.Contains(script, "'volume' 'ls'"):
			return "{\"Name\":\"docs-data\"}\n{\"Name\":\"scratch\"}", nil
		case strings.Contains(script, "'volume' 'inspect'"):
			return `[{"Name":"docs-data","Driver":"local","Scope":"local","CreatedAt":"2026-01-02T03:04:05Z","Mountpoint":"/example/docs-data","Labels":{"team":"docs"}},{"Name":"scratch","Driver":"local","Scope":"local","CreatedAt":"2026-01-02T03:05:06Z","Mountpoint":"/example/scratch","Labels":{"com.docker.volume.anonymous":""}}]`, nil
		case strings.Contains(script, "'system' 'df' '-v'"):
			return `{"Images":[],"Containers":[],"Volumes":[{"Name":"scratch","Links":"0","Size":"12.4MB"}],"BuildCache":[]}`, nil
		case strings.Contains(script, "'system' 'df'"):
			return strings.Join([]string{
				`{"Type":"Images","TotalCount":"7","Active":"4","Size":"101MB","Reclaimable":"40MB (40%)"}`,
				`{"Type":"Containers","TotalCount":"5","Active":"4","Size":"15MB","Reclaimable":"10MB (66%)"}`,
				`{"Type":"Local Volumes","TotalCount":"3","Active":"1","Size":"20MB","Reclaimable":"12.4MB (62%)"}`,
				`{"Type":"Build Cache","TotalCount":"3","Active":"0","Size":"10MB","Reclaimable":"10MB"}`,
			}, "\n"), nil
		case strings.Contains(script, "'info'"):
			return `{"Architecture":"x86_64","OperatingSystem":"Linux","KernelVersion":"6.8","NCPU":4,"MemTotal":4294967296,"DockerRootDir":"/var/lib/docker","Runtimes":{"runc":{}},"Driver":"overlay2","CgroupVersion":"2"}`, nil
		case strings.Contains(script, "'version'"):
			return `{"Client":{"Version":"27.0","ApiVersion":"1.46"},"Server":{"Version":"27.0","ApiVersion":"1.46","Os":"linux"}}`, nil
		case strings.Contains(script, "Unable to read registry metadata."):
			return `{"registries":["registry.example"],"store":"pass"}`, nil
		case strings.Contains(script, "'network' 'disconnect'"):
			return "test engine rejected disconnect", errors.New("exit status 1")
		case strings.Contains(script, "'prune'"):
			progress("Total reclaimed space: 10MB\n")
			return "Total reclaimed space: 10MB\n", nil
		case strings.Contains(script, "Refusing to read a symbolic link"):
			return "{\"registry-mirrors\":[\"https://mirror.example\"]}\n", nil
		case strings.Contains(script, "printf 'local"):
			return "local\n", nil
		default:
			return "done\n", nil
		}
	}
	a.readDocker = func(context.Context, *ssh.Client) (monitor.Docker, error) {
		calls <- resourceTestCall{"overview"}
		return d, nil
	}
	a.active = 0
	a.openDockerPage(s, dockerSecNetworks)
	a.terminalPct = 62
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	page := a.dockerResources(s)
	page.input = func(_ context.Context, _ *ssh.Client, input monitor.DockerInput, progress func(string)) (string, error) {
		calls <- resourceTestCall{input.Script()}
		return "fake saved\n", nil
	}
	page.events = func(ctx context.Context, _ *ssh.Client, script string, report func(string)) (string, error) {
		calls <- resourceTestCall{script}
		report("{\"Type\":\"container\",\"Action\":\"start\",\"time\":1700000000,\"Actor\":{\"ID\":\"a1\",\"Attributes\":{\"name\":\"docs\"}}}\n{\"Type\":\"network\",\"Action\":\"create\",\"time\":1700000001,\"Actor\":{\"ID\":\"bb\",\"Attributes\":{\"name\":\"retired\"}}}\n")
		<-ctx.Done()
		calls <- resourceTestCall{"events stopped"}
		return "", ctx.Err()
	}
	t.Cleanup(func() { a.stopPoll(&page.system.events.poll) })
	tt.Frame()
	p.until(t, "network list", func() bool { return page.networks.read })
	return a, p, s, tt, page, calls
}

func resourceCall(t *testing.T, calls <-chan resourceTestCall, snippet string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case item := <-calls:
			if strings.Contains(item.script, snippet) {
				return item.script
			}
		case <-deadline:
			t.Fatalf("missing call %q", snippet)
		}
	}
}

func resourceNoCall(t *testing.T, calls <-chan resourceTestCall, snippet string) {
	t.Helper()
	for len(calls) > 0 {
		if item := <-calls; strings.Contains(item.script, snippet) {
			t.Fatalf("unexpected call: %q", item.script)
		}
	}
}

func resourceType(t *testing.T, tt *ui.Tester, name, value string) {
	t.Helper()
	field, ok := tt.AccessByRole(ui.RoleTextField, name)
	if !ok {
		t.Fatalf("missing input %q", name)
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(value)
	tt.Frame()
}

func resourceClickButton(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	item, ok := tt.AccessByRole(ui.RoleButton, name)
	if !ok || item.Disabled {
		t.Fatalf("button unavailable %q: %+v \n %q", name, item, tt.Texts())
	}
	tt.ClickAt(item.Bounds.X+item.Bounds.W/2, item.Bounds.Y+item.Bounds.H/2)
	tt.Frame()
}

func TestDockerResourcesActions(t *testing.T) {
	a, p, s, tt, page, calls := resourceTestApp(t)
	resourceCall(t, calls, "'network' 'inspect' 'abcd' 'aa' 'bb'")
	cols := networkCols()
	page.networks.table.sort = ui.SortOrder{Column: "name", Descending: true}
	if rows := page.networks.table.shown(networkRows(page.networks.rows, &s.overview.docker.d), cols); len(rows) != 3 || rows[0].name != "retired" {
		t.Fatalf("unexpected sort: %+v", rows)
	}
	page.networks.table.sort = ui.SortOrder{}
	tt.Frame()
	tt.RightClickAt(rowCenter(t, tt, "bridge"))
	if err := tt.ChooseMenuItem(locales.DockerResNetRemove()); err == nil {
		t.Fatal("protected network was removable")
	}
	tt.Key(0, ui.KeyEscape)
	a.confirmNetworkRemove(s, page, networkCandidates(s, page.networks.rows), true)
	tt.Frame()
	wantTexts(t, tt, "network confirmation", "retired", locales.DockerResNetworkWarning())
	resourceClickButton(t, tt, locales.Cancel())
	resourceNoCall(t, calls, "'network' 'rm'")
	a.confirmNetworkRemove(s, page, networkCandidates(s, page.networks.rows), true)
	tt.Frame()
	resourceClickButton(t, tt, locales.DockerResNetPrune())
	if got := resourceCall(t, calls, "'network' 'rm'"); got != "export LC_ALL=C\nexec 2>&1\ndocker 'network' 'rm' 'bb'\n" {
		t.Fatal(got)
	}
	p.until(t, "network refresh", func() bool { return page.work == nil && !page.networks.loading })
	var sawOverview, sawInspect bool
	for len(calls) > 0 {
		call := <-calls
		sawOverview = sawOverview || call.script == "overview"
		sawInspect = sawInspect || strings.Contains(call.script, "'network' 'inspect'")
	}
	if !sawOverview || !sawInspect {
		t.Fatalf("refresh calls: overview=%v inspect=%v", sawOverview, sawInspect)
	}
	page.closeDialog()
	a.createDockerNetwork(s, page)
	tt.Frame()
	resourceType(t, tt, locales.FieldName(), "qa-net")
	resourceType(t, tt, "IPv4 · "+locales.DockerResSubnet(), "192.0.2.42/24")
	if button, ok := tt.AccessByRole(ui.RoleButton, locales.DockerResCreate()); !ok || !button.Disabled {
		t.Fatal("bad network CIDR enabled create")
	}
	resourceType(t, tt, "IPv4 · "+locales.DockerResSubnet(), "192.0.2.0/24")
	resourceClickButton(t, tt, locales.DockerResCreate())
	resourceCall(t, calls, "'network' 'create' '--driver' 'bridge' '--subnet' '192.0.2.0/24' 'qa-net'")
	p.until(t, "network create refresh", func() bool { return page.work == nil && !page.networks.loading })
	page.closeDialog()
	network, _ := lookupNetwork(s, page, "abcd")
	a.openNetworkEndpoint(s, page, network, false)
	tt.Frame()
	resourceType(t, tt, locales.DockerResIPv4(), "192.0.2.6")
	resourceType(t, tt, locales.DockerResAliases(), "service,worker")
	resourceClickButton(t, tt, locales.DockerResConnect())
	resourceCall(t, calls, "'network' 'connect' '--ip' '192.0.2.6' '--alias' 'service' '--alias' 'worker' 'abcd' 'b1'")
	p.until(t, "network connect refresh", func() bool { return page.work == nil && !page.networks.loading })
	page.closeDialog()
	a.openNetworkEndpoint(s, page, network, true)
	tt.Frame()
	resourceClickButton(t, tt, locales.DockerResDisconnect())
	resourceCall(t, calls, "'network' 'disconnect' 'abcd' 'a1'")
	p.until(t, "failed disconnect", func() bool { return page.work == nil })
	wantTexts(t, tt, "disconnect failure", "test engine rejected disconnect", locales.DockerActionFailed())
	page.closeDialog()
	s.docker.section = dockerSecVolumes
	tt.Frame()
	p.until(t, "volume list", func() bool { return page.volumes.read && page.volumes.unused.read })
	if reason := volumeRemoveReason(s, page, "docs-data", volumeUsers(s), false); reason != locales.DockerResVolumeInUse() {
		t.Fatal(reason)
	}
	a.createDockerVolume(s, page)
	tt.Frame()
	resourceType(t, tt, locales.FieldName(), "--invalid")
	if button, ok := tt.AccessByRole(ui.RoleButton, locales.DockerResCreate()); !ok || !button.Disabled {
		t.Fatalf("invalid create enabled: %+v", button)
	}
	resourceType(t, tt, locales.FieldName(), "new-data")
	resourceClickButton(t, tt, locales.DockerResCreate())
	resourceCall(t, calls, "'volume' 'create' '--driver' 'local' 'new-data'")
	p.until(t, "volume refresh", func() bool { return page.work == nil && !page.volumes.loading && !page.volumes.unused.loading })
	page.closeDialog()
	a.confirmVolumePrune(s, page)
	tt.Frame()
	resourceClickButton(t, tt, locales.DockerResVolumePrune())
	resourceCall(t, calls, "'volume' 'rm' 'scratch'")
	p.until(t, "prune refresh", func() bool { return page.work == nil && !page.volumes.loading && !page.volumes.unused.loading })
	resourceCall(t, calls, "overview")
	page.closeDialog()
	volume, _ := lookupVolume(page, "docs-data")
	a.confirmVolumeRemove(s, page, []monitor.DockerVolumeDetails{volume}, true)
	tt.Frame()
	wantTexts(t, tt, "force removal", locales.DockerResVolumeForceWarning())
	resourceClickButton(t, tt, locales.Cancel())
	resourceNoCall(t, calls, "'volume' 'rm' '--force'")
	a.confirmVolumeRemove(s, page, []monitor.DockerVolumeDetails{volume}, true)
	tt.Frame()
	resourceClickButton(t, tt, locales.DockerResVolumeForce())
	resourceCall(t, calls, "'volume' 'rm' '--force' 'docs-data'")
}

func TestDockerSystemControls(t *testing.T) {
	a, p, s, tt, page, calls := resourceTestApp(t)
	s.docker.section = dockerSecSystem
	tt.Frame()
	resourceCall(t, calls, "'events' '--since' '30m'")
	p.until(t, "system lists", func() bool { return page.system.info.read && page.system.disk.read })
	a.openDockerCleanup(s, page)
	tt.Frame()
	choice := locales.DockerSysStopped() + " · 10MB (66%)"
	clickNamed(t, tt, choice)
	clickNamed(t, tt, locales.DockerSysCleanupAck())
	resourceClickButton(t, tt, locales.Cancel())
	resourceNoCall(t, calls, "'container' 'prune'")
	a.openDockerCleanup(s, page)
	tt.Frame()
	clickNamed(t, tt, choice)
	clickNamed(t, tt, locales.DockerSysCleanupAck())
	resourceClickButton(t, tt, locales.DockerSysCleanup())
	resourceCall(t, calls, "'container' 'prune' '--force'")
	p.until(t, "cleanup output", func() bool { return page.work == nil })
	wantTexts(t, tt, "cleanup output", "Total reclaimed space: 10MB")
	page.closeDialog()
	p.until(t, "cleanup candidates", func() bool {
		return page.networks.read && !page.networks.loading && page.volumes.read && !page.volumes.loading && page.volumes.unused.read && !page.volumes.unused.loading && page.volumes.usage.read && !page.volumes.usage.loading
	})
	a.openDockerCleanup(s, page)
	tt.Frame()
	clickNamed(t, tt, locales.DockerSysUnusedNets()+" · "+locales.DockerResCount(1))
	var volumeEstimate string
	for _, choice := range cleanupChoices(s, page) {
		if choice.kind == "volume" {
			volumeEstimate = choice.estimate
		}
	}
	clickNamed(t, tt, locales.DockerSysUnusedVols()+" · "+volumeEstimate)
	wantTexts(t, tt, "exact cleanup targets", "retired", "scratch")
	clickNamed(t, tt, locales.DockerSysCleanupAck())
	resourceClickButton(t, tt, locales.DockerSysCleanup())
	resourceCall(t, calls, "'network' 'rm' 'bb'")
	resourceCall(t, calls, "'volume' 'rm' 'scratch'")
	resourceNoCall(t, calls, "'volume' 'prune'")
	p.until(t, "exact cleanup output", func() bool { return page.work == nil })
	page.closeDialog()
	page.system.mode = dockerSystemEvents
	tt.Frame()
	p.until(t, "events", func() bool { return len(page.system.events.rows) == 2 })
	page.system.events.table.query = "retired"
	tt.Frame()
	wantTexts(t, tt, "event filter", locales.DockerResCount(1), "retired")
	if tt.HasText("docs") {
		t.Fatal("event filter showed unrelated actor")
	}
	page.system.events.table.query = ""
	page.system.events.kind = "container"
	tt.Frame()
	wantTexts(t, tt, "event type", locales.DockerResCount(1), "docs")
	clickNamed(t, tt, locales.DockerSysPause())
	resourceCall(t, calls, "events stopped")
	resourceClickButton(t, tt, locales.DockerSysClear())
	if len(page.system.events.rows) != 0 {
		t.Fatal("events did not clear")
	}
	clickNamed(t, tt, locales.DockerSysPause())
	resourceCall(t, calls, "'events' '--since' '2023-")
	s.docker.section = dockerSecNetworks
	tt.Frame()
	resourceCall(t, calls, "events stopped")
	s.docker.section = dockerSecSystem
	page.system.mode = dockerSystemRegistries
	tt.Frame()
	p.until(t, "registry metadata", func() bool { return page.system.registries.read })
	a.openDockerLogin(s, page)
	tt.Frame()
	resourceType(t, tt, locales.FieldUser(), "reader")
	resourceType(t, tt, locales.FieldPassword(), "synthetic-password")
	resourceClickButton(t, tt, locales.DockerSysLogin())
	if script := resourceCall(t, calls, "'--password-stdin' 'docker.io'"); strings.Contains(script, "synthetic-password") {
		t.Fatal("secret in command")
	}
	p.until(t, "login finished", func() bool { return page.work == nil })
	if tt.HasText("fake saved") {
		t.Fatal("login output was not hidden")
	}
	page.closeDialog()
	a.openDockerLogout(s, page, "registry.example")
	tt.Frame()
	resourceClickButton(t, tt, locales.Cancel())
	resourceNoCall(t, calls, "'logout'")
	a.openDockerLogout(s, page, "registry.example")
	tt.Frame()
	resourceClickButton(t, tt, locales.DockerSysLogout())
	resourceCall(t, calls, "'logout' 'registry.example'")
	p.until(t, "logout finished", func() bool { return page.work == nil })
}

func TestDockerSystemDaemonConfirmation(t *testing.T) {
	a, p, s, tt, page, calls := resourceTestApp(t)
	s.docker.section = dockerSecSystem
	tt.Frame()
	a.openDaemonConfig(s, page)
	tt.Frame()
	p.until(t, "daemon editor", func() bool { return tt.HasText(locales.DockerSysAddMirror()) })
	resourceType(t, tt, locales.DockerSysMirrors(), "http://mirror.example")
	if button, ok := tt.AccessByRole(ui.RoleButton, locales.Save()); !ok || !button.Disabled {
		t.Fatal("invalid mirror enabled save")
	}
	resourceType(t, tt, locales.DockerSysMirrors(), "https://fast.example")
	resourceClickButton(t, tt, locales.Save())
	if script := resourceCall(t, calls, "'tee'"); !strings.Contains(script, "daemon.json.bak-") || strings.Contains(script, "fast.example") {
		t.Fatal("unsafe config command")
	}
	p.until(t, "restart offer", func() bool { return tt.HasText(locales.DockerSysRestartLater()) })
	resourceNoCall(t, calls, "'restart' 'docker.service'")
	resourceClickButton(t, tt, locales.DockerSysRestart())
	if button, ok := tt.AccessByRole(ui.RoleButton, locales.DockerSysRestart()); !ok || !button.Disabled {
		t.Fatal("restart did not require explicit acknowledgment")
	}
	resourceClickButton(t, tt, locales.Cancel())
	resourceNoCall(t, calls, "'restart' 'docker.service'")
	a.confirmDockerRestart(s, page, "{}\n")
	tt.Frame()
	clickNamed(t, tt, locales.DockerSysRestartAck())
	resourceClickButton(t, tt, locales.DockerSysRestart())
	resourceCall(t, calls, "'systemctl' 'restart' 'docker.service'")
	p.until(t, "restart completion", func() bool { return page.work == nil })
}

func TestDockerResourcesScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				a, p, s, tt, page, _ := resourceTestApp(t)
				p.SetLocale(language)
				tt.SetDark(appearance == "dark")
				shot := func(scene string) {
					tt.Frame()
					tt.Frame()
					saveShot(t, tt, "docker-res-"+scene+"-"+appearance+"-"+language+".png")
				}
				for _, section := range []struct {
					id   int
					name string
				}{{dockerSecNetworks, "networks"}, {dockerSecVolumes, "volumes"}, {dockerSecSystem, "system"}} {
					s.docker.section = section.id
					tt.Frame()
					if section.id == dockerSecVolumes {
						a.loadDockerVolumeUsage(s, page, false)
						p.until(t, "volume usage", func() bool { return page.volumes.read && page.volumes.unused.read && page.volumes.usage.read })
					}
					if section.id == dockerSecSystem {
						p.until(t, "disk usage", func() bool { return page.system.disk.read && page.system.info.read })
					}
					shot(section.name + "-short")
					a.openPanelTab(s, tabDocker)
					shot(section.name + "-full")
					a.backToTerminal(a.current())
				}
				a.bottomSide = config.PanelRight
				for _, section := range []struct {
					id   int
					name string
				}{{dockerSecNetworks, "networks"}, {dockerSecVolumes, "volumes"}, {dockerSecSystem, "system"}} {
					s.docker.section = section.id
					shot(section.name + "-right")
				}
				a.bottomSide = ""
				a.openPanelTab(s, tabDocker)
				for _, dialog := range []struct {
					id   int
					name string
					show func()
				}{
					{dockerSecNetworks, "network-details", func() { a.openNetworkDetails(s, page, "abcd") }},
					{dockerSecNetworks, "network-create", func() { a.createDockerNetwork(s, page) }},
					{dockerSecNetworks, "network-prune", func() { a.confirmNetworkRemove(s, page, networkCandidates(s, page.networks.rows), true) }},
					{dockerSecNetworks, "network-connect", func() { network, _ := lookupNetwork(s, page, "abcd"); a.openNetworkEndpoint(s, page, network, false) }},
					{dockerSecNetworks, "network-disconnect", func() { network, _ := lookupNetwork(s, page, "abcd"); a.openNetworkEndpoint(s, page, network, true) }},
					{dockerSecVolumes, "volume-details", func() { a.openVolumeDetails(s, page, "docs-data") }},
					{dockerSecVolumes, "volume-create", func() { a.createDockerVolume(s, page) }},
					{dockerSecVolumes, "volume-prune", func() { a.confirmVolumePrune(s, page) }},
					{dockerSecSystem, "engine-info", func() { a.openEngineInfo(s, page) }},
					{dockerSecSystem, "cleanup", func() { a.openDockerCleanup(s, page) }},
					{dockerSecSystem, "config", func() {
						a.openDaemonConfig(s, page)
						tt.Frame()
						p.until(t, "daemon mirrors", func() bool { return tt.HasText(locales.DockerSysAddMirror()) })
					}},
					{dockerSecSystem, "restart", func() { a.confirmDockerRestart(s, page, "{}\n") }},
				} {
					s.docker.section = dialog.id
					dialog.show()
					shot(dialog.name)
					page.closeDialog()
				}
				page.system.mode = dockerSystemEvents
				tt.Frame()
				p.until(t, "event rows", func() bool { return len(page.system.events.rows) == 2 })
				shot("events")
				page.system.mode = dockerSystemRegistries
				tt.Frame()
				p.until(t, "registry rows", func() bool { return page.system.registries.read })
				shot("registries")
				a.openDockerLogin(s, page)
				shot("login")
				a.openDockerLogout(s, page, "registry.example")
				shot("logout")
			})
		}
	}
}
