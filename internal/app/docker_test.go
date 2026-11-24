package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// fakeDocker answers the app's Docker reads when the test says.
type fakeDocker struct{ reads chan dockerRead }

type dockerRead struct {
	ctx   context.Context
	reply chan dockerReply
}

type dockerReply struct {
	d   monitor.Docker
	err error
}

func newFakeDocker(a *App) *fakeDocker {
	f := &fakeDocker{reads: make(chan dockerRead, 8)}
	a.readDocker = func(ctx context.Context, _ *ssh.Client) (monitor.Docker, error) {
		r := dockerRead{ctx, make(chan dockerReply, 1)}
		f.reads <- r
		select {
		case rep := <-r.reply:
			return rep.d, rep.err
		case <-ctx.Done():
			return monitor.Docker{}, ctx.Err()
		}
	}
	return f
}

// next is the read the app started last.
func (f *fakeDocker) next(t *testing.T) dockerRead {
	t.Helper()
	select {
	case r := <-f.reads:
		return r
	default:
		t.Fatal("no Docker read started")
	}
	return dockerRead{}
}

// demoContainer is a running container of image, with its stats.
func demoContainer(name, image string, up time.Duration, cpu float64, mem uint64) monitor.Container {
	id := fmt.Sprintf("%x", name+strings.Repeat("0", 32))[:64]
	ct := monitor.Container{
		ID: id, Name: name, Image: image, ImageID: "sha256:" + id,
		Created: time.Now().Add(-up - time.Minute), StartedAt: time.Now().Add(-up),
		Status: "running", Running: true, PID: 4000, RestartPolicy: "always", LogDriver: "json-file",
		NetworkMode: "bridge", Hostname: id[:12],
		Networks: []monitor.ContainerNetwork{{Name: "bridge", IP: "172.17.0.9", PrefixLen: 16, Gateway: "172.17.0.1", MAC: "02:42:ac:11:00:09"}},
		Stats:    &monitor.ContainerStats{CPU: cpu, MemUsed: mem, MemLimit: 8 << 30, MemPercent: 100 * float64(mem) / float64(8<<30), PIDs: 7},
	}
	ct.Inspect = json.RawMessage(fmt.Sprintf(`{"Id":%q,"Name":"/%s","Config":{"Image":%q}}`, ct.ID, name, image))
	return ct
}

func inProject(ct monitor.Container, project, service string) monitor.Container {
	ct.Project, ct.Service = project, service
	ct.Labels = map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": service}
	ct.ComposeDir = "/srv/" + project
	return ct
}

// demoDocker is a server with a Compose project of three containers and
// three others: one stopped, one paused and one unhealthy.
func demoDocker() monitor.Docker {
	web := inProject(demoContainer("shop-web-1", "nginx:1.27-alpine", 3*24*time.Hour, 0.4, 24<<20), "shop", "web")
	web.Networks = []monitor.ContainerNetwork{{Name: "shop_default", IP: "172.20.0.3", PrefixLen: 16, Gateway: "172.20.0.1",
		IPv6: "fd00:20::3", IPv6PrefixLen: 64, MAC: "02:42:ac:14:00:03", Aliases: []string{"shop-web-1", "web"}}}
	web.NetworkMode = "shop_default"
	web.Ports = []monitor.PortBinding{
		{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8080, AlsoIPv6: true},
		{Port: 443, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8443, AlsoIPv6: true},
	}
	web.Exposed = []string{"9113/tcp"}
	web.Mounts = []monitor.Mount{
		{Type: "bind", Source: "/srv/shop/html", Destination: "/usr/share/nginx/html", Mode: "ro"},
		{Type: "volume", Name: "shop_logs", Source: "/var/lib/docker/volumes/shop_logs/_data", Destination: "/var/log/nginx", RW: true},
	}
	web.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin", "DB_PASSWORD=s3cr3t", "TZ=Asia/Shanghai"}
	web.Labels["com.docker.compose.project.config_files"] = "/srv/shop/compose.yaml"
	web.Command = []string{"nginx", "-g", "daemon off;"}
	web.Entrypoint = []string{"/docker-entrypoint.sh"}
	web.Memory = 512 << 20
	web.NanoCPUs = 1_500_000_000
	api := inProject(demoContainer("shop-api-1", "ghcr.io/example/shop-api:2.14.0", 3*24*time.Hour, 12.6, 410<<20), "shop", "api")
	api.Health = "healthy"
	api.Ports = []monitor.PortBinding{{Port: 9000, Proto: "tcp", HostIP: "127.0.0.1", HostPort: 9000}}
	db := inProject(demoContainer("shop-db-1", "postgres:16", 3*24*time.Hour, 1.2, 230<<20), "shop", "db")
	db.Health = "unhealthy"
	db.FailingStreak = 4
	gitea := demoContainer("gitea", "gitea/gitea:1.22", 41*24*time.Hour, 0.1, 160<<20)
	gitea.Ports = []monitor.PortBinding{
		{Port: 22, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 2222, AlsoIPv6: true},
		{Port: 3000, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 3000, AlsoIPv6: true},
	}
	worker := demoContainer("report-worker", "localhost/worker:dev", time.Hour, 0, 0)
	worker.Status, worker.Running, worker.ExitCode, worker.Stats = "exited", false, 137, nil
	worker.FinishedAt = time.Now().Add(-5 * time.Hour)
	cache := demoContainer("cache", "redis:7", 9*time.Hour, 0, 8<<20)
	cache.Status, cache.Paused = "paused", true
	cache.NetworkMode, cache.Networks = "host", []monitor.ContainerNetwork{{Name: "host"}}
	return monitor.Docker{
		Tool: "docker", Version: "26.1.4", APIVersion: "1.45", OS: "Ubuntu 22.04.4 LTS", StorageDriver: "overlay2",
		CgroupDriver: "systemd", CgroupVersion: "2", RootDir: "/var/lib/docker", LoggingDriver: "json-file",
		CPUs: 4, MemTotal: 8 << 30, NumContainers: 6, Running: 4, Paused: 1, Stopped: 1, NumImages: 9,
		Warnings:   []string{"WARNING: bridge-nf-call-iptables is disabled"},
		Containers: []monitor.Container{web, api, db, gitea, worker, cache},
		Volumes:    make([]monitor.DockerVolume, 4),
		Networks:   make([]monitor.DockerNetwork, 5),
		Disk: []monitor.DockerDiskUsage{
			{Type: "Images", Total: "9", Active: "6", Size: "3.214GB", Reclaimable: "1.02GB (31%)"},
			{Type: "Containers", Total: "6", Active: "4", Size: "88.1MB", Reclaimable: "12MB (13%)"},
			{Type: "Local Volumes", Total: "4", Active: "3", Size: "1.9GB", Reclaimable: "0B (0%)"},
			{Type: "Build Cache", Total: "0", Active: "0", Size: "0B", Reclaimable: "0B"},
		},
	}
}

// dockerApp is overviewApp with the Overview tab showing, its system info
// read and its Docker read answered with d.
func dockerApp(t *testing.T, d monitor.Docker) (*App, *fakePlatform, *ui.Tester, *fakeDocker) {
	a, p, tt, f := overviewApp(t)
	fd := newFakeDocker(a)
	a.openBottomTab(tabOverview)
	tt.Frame()
	f.next(t).reply <- sysInfoReply{info: demoSysInfo()}
	fd.next(t).reply <- dockerReply{d: d}
	ds := &a.sessions[0].overview.docker
	p.until(t, "the reads", func() bool { return !a.sessions[0].overview.loading && !ds.loading })
	tt.Frame()
	return a, p, tt, fd
}

// clickNamed clicks the element the screen reader knows by name.
func clickNamed(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	b, ok := tt.Access(name)
	if !ok {
		t.Fatalf("no %q; texts %q", name, tt.Texts())
	}
	tt.ClickAt(b.Bounds.X+b.Bounds.W/2, b.Bounds.Y+b.Bounds.H/2)
	tt.Frame()
}

func wantTexts(t *testing.T, tt *ui.Tester, what string, texts ...string) {
	t.Helper()
	for _, want := range texts {
		if !tt.HasText(want) {
			t.Errorf("%s: missing %q; texts %q", what, want, tt.Texts())
		}
	}
}

func TestOverviewDocker(t *testing.T) {
	a, p, tt, fd := dockerApp(t, demoDocker())
	s := a.sessions[0]
	// As a panel row: the whole main area, every column of the list.
	a.openPanelTab(s, tabOverview)
	tt.Frame()
	tt.Frame()
	wantTexts(t, tt, "panel",
		"Docker", "26.1.4 (API 1.45)", locales.DockerContainerCounts(6, 4, 1, 1), "overlay2, /var/lib/docker", "systemd, v2",
		locales.DockerDiskLine("3.214GB", "1.02GB (31%)"), locales.DockerWarnings(1),
		locales.DockerContainers(), locales.DockerCount(6, 4), "shop", locales.DockerOther(),
		"shop-web-1", "nginx:1.27-alpine", "0.0.0.0:8080→80/tcp  +1", "172.20.0.3  +1", "12.6%", "24.0M / 512M",
		locales.DockerUpHealth(locales.DurDays(3), locales.DockerUnhealthy()),
		locales.DockerExited(137, locales.DurHours(5)), locales.DockerPaused(), "host",
	)
	saveShot(t, tt, "overview-docker-panel-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-docker-panel-dark-en.png")
	tt.SetDark(false)
	tt.Scroll(700, 500, 0, 900)
	tt.Frame()
	saveShot(t, tt, "overview-docker-panel-containers-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-docker-panel-containers-dark-en.png")
	tt.SetDark(false)

	// A tall window, where every line is on screen to click.
	tt.SetSize(winW, 2400)
	tt.Frame()
	tt.Frame()

	// The filter.
	ds := &s.overview.docker
	clickNamed(t, tt, locales.DockerFilter())
	tt.Type("gitea")
	tt.Frame()
	if ds.filter != "gitea" || !tt.HasText("gitea") || tt.HasText("shop-web-1") || !tt.HasText(locales.DockerCountFiltered(1, 6)) {
		t.Errorf("filter %q: texts %q", ds.filter, tt.Texts())
	}
	ds.filter = "172.20.0.3" // by address
	tt.Frame()
	if !tt.HasText("shop-web-1") || tt.HasText("gitea") {
		t.Errorf("filter by address: texts %q", tt.Texts())
	}
	ds.filter = "nothing like it"
	tt.Frame()
	wantTexts(t, tt, "no match", locales.DockerNoMatch())
	ds.filter = ""
	tt.Frame()

	// A project closes and opens again.
	clickNamed(t, tt, "shop")
	if tt.HasText("shop-api-1") || !tt.HasText("gitea") {
		t.Errorf("closed: texts %q", tt.Texts())
	}
	clickNamed(t, tt, "shop")
	wantTexts(t, tt, "opened", "shop-api-1")
	// Not grouped.
	clickNamed(t, tt, locales.DockerGroupByProject())
	if tt.HasText(locales.DockerOther()) || !tt.HasText("shop-api-1") {
		t.Errorf("flat: texts %q", tt.Texts())
	}

	// Sorted by CPU: the busiest first.
	clickNamed(t, tt, locales.DockerSortBy(locales.Cpu()))
	groups, _ := ds.ctrGroups(time.Now())
	if len(groups) != 1 || groups[0].list[0].Name != "shop-api-1" || groups[0].list[1].Name != "shop-db-1" {
		t.Errorf("by CPU: %v", groups)
	}
	clickNamed(t, tt, locales.DockerSortBy(locales.Cpu()))
	if groups, _ := ds.ctrGroups(time.Now()); groups[0].list[0].Name == "shop-api-1" {
		t.Error("CPU again did not turn the order")
	}
	clickNamed(t, tt, locales.DockerSortBy(locales.DockerColName()))
	clickNamed(t, tt, locales.DockerGroupByProject())

	// A container's details.
	clickNamed(t, tt, "shop-web-1")
	if a.ctrDlg == nil {
		t.Fatal("no details")
	}
	wantTexts(t, tt, "details",
		locales.DockerSecGeneral(), locales.DockerSecNetwork(), "shop_default", "172.20.0.3/16", "172.20.0.1", "fd00:20::3/64",
		"02:42:ac:14:00:03", "shop-web-1, web", "0.0.0.0:8080, [::]:8080", locales.DockerNotPublished(),
		"/srv/shop/html → /usr/share/nginx/html  ("+locales.DockerReadOnlyMount()+")",
		"shop_logs → /var/log/nginx  ("+locales.DockerReadWrite()+")",
		"nginx -g 'daemon off;'", locales.DockerCPUs("1.5"), formatSize(512<<20), "DB_PASSWORD", "TZ", secretMask,
		"com.docker.compose.project", "/srv/shop/compose.yaml",
	)
	if tt.HasText("s3cr3t") {
		t.Error("the environment's values show before Show values")
	}
	tt.SetSize(winW, winH)
	tt.Frame()
	saveShot(t, tt, "overview-docker-details-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-docker-details-dark-en.png")
	tt.SetDark(false)
	tt.Scroll(640, 400, 0, 700)
	tt.Frame()
	saveShot(t, tt, "overview-docker-details-more-en.png")
	tt.SetSize(winW, 2400)
	tt.Frame()
	tt.Frame()

	// Copies: a line's value, an environment variable masked, the JSON.
	clickNamed(t, tt, locales.DockerIPv4()+": 172.20.0.3/16")
	if got := tt.Clipboard(); got != "172.20.0.3/16" {
		t.Errorf("IPv4 copied %q", got)
	}
	clickNamed(t, tt, "DB_PASSWORD: "+secretMask)
	if got := tt.Clipboard(); got != "DB_PASSWORD=s3cr3t" {
		t.Errorf("env copied %q", got)
	}
	clickButton(t, tt, locales.DockerShowValues())
	tt.Frame()
	wantTexts(t, tt, "shown", "s3cr3t", locales.DockerHideValues())
	clickButton(t, tt, locales.DockerCopyJSON())
	if got := tt.Clipboard(); !strings.Contains(got, `"Id": "`+demoDocker().Containers[0].ID+`"`) || !strings.Contains(got, "\n  ") {
		t.Errorf("JSON %q", got)
	}
	clickButton(t, tt, locales.Close())
	tt.Frame()
	if a.ctrDlg != nil || tt.HasText(locales.DockerSecGeneral()) {
		t.Error("Close left the details")
	}

	// Refresh in the card reads again; the cards stay meanwhile.
	clickButton(t, tt, locales.DockerRefresh())
	r := fd.next(t)
	wantTexts(t, tt, "refreshing", "shop-web-1")
	d := demoDocker()
	d.Containers = d.Containers[3:]
	r.reply <- dockerReply{d: d}
	p.until(t, "the second read", func() bool { return len(ds.d.Containers) == 3 })
	if tt.HasText("shop-api-1") {
		t.Error("the second read left the containers gone")
	}

	// Shown in every frame, or again soon: not read again; shown again
	// (no frame showed it since frame) after dockerStale: read again.
	ds.frame = 0
	tt.Frame()
	select {
	case <-fd.reads:
		t.Error("read again while fresh")
	default:
	}
	ds.at = time.Now().Add(-time.Minute)
	tt.Frame()
	select {
	case <-fd.reads:
		t.Error("read again while shown")
	default:
	}
	ds.frame = 0
	tt.Frame()
	fd.next(t).reply <- dockerReply{d: demoDocker()}
	p.until(t, "the read of the stale data", func() bool { return len(ds.d.Containers) == 6 })

	// In Chinese.
	a.setLanguage("zh-Hans")
	tt.SetSize(winW, winH)
	tt.Frame()
	tt.Scroll(700, 500, 0, 900)
	tt.Frame()
	wantTexts(t, tt, "Chinese", "容器", "其他容器", "磁盘占用", locales.DockerCount(6, 4))
	saveShot(t, tt, "overview-docker-panel-zh.png")
}

// Under the terminal, and in the right column: fewer columns.
func TestOverviewDockerNarrow(t *testing.T) {
	a, _, tt, _ := dockerApp(t, demoDocker())
	wantTexts(t, tt, "short", "shop-web-1", "12.6%")
	tt.Scroll(700, 700, 0, 5000) // to the end: the containers
	tt.Frame()
	saveShot(t, tt, "overview-docker-short-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-docker-short-dark-en.png")
	tt.SetDark(false)
	a.setPanelOnRight(true)
	tt.Frame()
	tt.Frame()
	wantTexts(t, tt, "right", "shop-web-1")
	tt.Scroll(1050, 500, 0, 5000)
	tt.Frame()
	saveShot(t, tt, "overview-docker-right-en.png")
	tt.Scroll(1050, 500, 0, -500)
	tt.Frame()
	saveShot(t, tt, "overview-docker-right-card-en.png")
}

// A server with dozens of containers, as the owner's: the page scrolls,
// nothing is cut.
func TestOverviewDockerMany(t *testing.T) {
	d := demoDocker()
	for i := range 60 {
		ct := demoContainer(fmt.Sprintf("app_%02d_with_a_rather_long_container_name", i), fmt.Sprintf("registry.example.com/team/service-%02d:2026.11.%d", i, i), time.Duration(i+1)*time.Hour, float64(i)/10, uint64(i+1)<<22)
		ct.Networks = []monitor.ContainerNetwork{{Name: fmt.Sprintf("app_%02d_default", i), IP: fmt.Sprintf("172.%d.0.2", 18+i), PrefixLen: 16}}
		ct.Ports = []monitor.PortBinding{{Port: 8000, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 18000 + i, AlsoIPv6: true}}
		if i%7 == 0 {
			ct = inProject(ct, fmt.Sprintf("stack%d", i/14), "svc")
		}
		d.Containers = append(d.Containers, ct)
	}
	d.NumContainers = len(d.Containers)
	a, _, tt, _ := dockerApp(t, d)
	a.openPanelTab(a.sessions[0], tabOverview)
	tt.Frame()
	tt.Frame()
	wantTexts(t, tt, "many", locales.DockerCount(66, 64))
	saveShot(t, tt, "overview-docker-many-en.png")
	// Scrolled down to the end.
	tt.Scroll(700, 500, 0, 1200)
	tt.Frame()
	saveShot(t, tt, "overview-docker-many-middle-en.png")
	tt.Scroll(700, 500, 0, 20000)
	tt.Frame()
	wantTexts(t, tt, "the end", "app_59_with_a_rather_long_container_name")
	saveShot(t, tt, "overview-docker-many-end-en.png")
}

func TestOverviewDockerStates(t *testing.T) {
	// No Docker: no card.
	_, _, tt, _ := dockerApp(t, monitor.Docker{})
	if tt.HasText(locales.DockerContainers()) || tt.HasText("Docker") {
		t.Errorf("no Docker: texts %q", tt.Texts())
	}

	// Denied: the card says what to do, without the containers.
	_, _, tt, _ = dockerApp(t, monitor.Docker{Tool: "docker", State: monitor.DockerDenied,
		Error: "permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"})
	wantTexts(t, tt, "denied", "Docker", locales.DockerDenied())
	if tt.HasText(locales.DockerContainers()) {
		t.Error("denied: the containers card shows")
	}
	tt.Scroll(700, 700, 0, 5000)
	tt.Frame()
	saveShot(t, tt, "overview-docker-denied-en.png")

	// The daemon does not run.
	_, _, tt, _ = dockerApp(t, monitor.Docker{Tool: "docker", State: monitor.DockerDown, Error: "Cannot connect to the Docker daemon"})
	wantTexts(t, tt, "down", locales.DockerDown())

	// Podman, with no containers.
	_, _, tt, _ = dockerApp(t, monitor.Docker{Tool: "podman", Version: "5.0.2"})
	wantTexts(t, tt, "podman", "Podman", locales.DockerNoContainers())

	// The read timed out: the card says so, once it knows Docker is there.
	a, p, tt, f := overviewApp(t)
	fd := newFakeDocker(a)
	a.openBottomTab(tabOverview)
	tt.Frame()
	f.next(t).reply <- sysInfoReply{info: demoSysInfo()}
	fd.next(t).reply <- dockerReply{d: monitor.Docker{Tool: "docker"}, err: context.DeadlineExceeded}
	p.until(t, "the timeout", func() bool { return !a.sessions[0].overview.docker.loading })
	tt.Frame()
	wantTexts(t, tt, "timeout", locales.DockerFailed(locales.SysInfoTimeout()))
}

func TestPortTexts(t *testing.T) {
	got := portTexts([]monitor.PortBinding{
		{Port: 53, Proto: "udp", HostIP: "::1", HostPort: 5353},
		{Port: 80, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 8080, AlsoIPv6: true},
		{Port: 8000, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 18000},
		{Port: 8001, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 18001},
		{Port: 8002, Proto: "tcp", HostIP: "0.0.0.0", HostPort: 18002},
		{Port: 9000, Proto: "tcp", HostPort: 9000},
	})
	want := []string{"[::1]:5353→53/udp", "0.0.0.0:8080→80/tcp", "0.0.0.0:18000-18002→8000-8002/tcp", "9000→9000/tcp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %q", got)
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, locales.DurSeconds(30)}, {5 * time.Minute, locales.DurMinutes(5)}, {47 * time.Hour, locales.DurHours(47)},
		{3 * 24 * time.Hour, locales.DurDays(3)}, {20 * 24 * time.Hour, locales.DurWeeks(2)}, {90 * 24 * time.Hour, locales.DurMonths(3)},
		{800 * 24 * time.Hour, locales.DurYears(2)},
	} {
		if got := humanDuration(tc.d); got != tc.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
