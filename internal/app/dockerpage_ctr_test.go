package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func ctrDemo() monitor.Docker {
	d := demoDocker()
	d.Images = []monitor.DockerImage{{Repository: "nginx", Tag: "alpine"}, {Repository: "gitea/gitea", Tag: "1.22"}}
	d.Networks = []monitor.DockerNetwork{{Name: "bridge"}, {Name: "app_net"}}
	d.Volumes = []monitor.DockerVolume{{Name: "app_data"}}
	for index := range d.Containers {
		ct := &d.Containers[index]
		ct.Inspect = json.RawMessage(fmt.Sprintf(`{"Id":%q,"Image":%q,"Config":{"Image":%q,"Cmd":["serve"],"Env":["TOKEN=sample-secret"],"WorkingDir":"/app"},"HostConfig":{"NetworkMode":"bridge","Memory":536870912},"State":{"Running":%t}}`, ct.ID, ct.ImageID, ct.Image, ct.Running))
	}
	worker := d.Containers[4]
	worker.Name, worker.ID = "worker-backup", strings.Repeat("b", 64)
	d.Containers = append(d.Containers, worker)
	return d
}

func ctrTestApp(t *testing.T, full bool) (*App, *fakePlatform, *ui.Tester, *fakeDocker, <-chan dockerActionCall) {
	t.Helper()
	a, platform, tester, reader := dockerPageApp(t, ctrDemo())
	s := a.sessions[0]
	calls := make(chan dockerActionCall, 16)
	s.overview.docker.run = func(ctx context.Context, _ *ssh.Client, script string, report func(string)) (string, error) {
		call := dockerActionCall{script: script, reply: make(chan string, 1), progress: report}
		select {
		case calls <- call:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		select {
		case reply := <-call.reply:
			if reply == "engine refused" {
				return reply, &monitor.DockerExit{Status: 1}
			}
			return reply, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	tester.SetSize(1280, 820)
	if full {
		a.openPanelTab(s, tabDocker)
	}
	tester.Frame()
	tester.Frame()
	return a, platform, tester, reader, calls
}

func ctrNothingRan(t *testing.T, calls <-chan dockerActionCall) {
	t.Helper()
	select {
	case call := <-calls:
		t.Fatalf("unexpected engine command %q", call.script)
	default:
	}
}

func ctrReply(t *testing.T, a *App, platform *fakePlatform, reader *fakeDocker, call dockerActionCall, output string) {
	t.Helper()
	state := &a.sessions[0].overview.docker
	call.reply <- output
	platform.until(t, "Docker refresh", func() bool { return state.loading })
	reader.next(t).reply <- dockerReply{d: state.d}
	platform.until(t, "Docker refreshed", func() bool { return !state.loading })
}

func TestDockerCtrSelection(t *testing.T) {
	a, platform, tester, reader, calls := ctrTestApp(t, true)
	s := a.sessions[0]
	page := &s.docker.containers
	wantTexts(t, tester, "containers", "shop", "shop-api-1", "report-worker", "worker-backup")
	clickNamed(t, tester, locales.DockerCtrShowStopped())
	if tester.HasText("report-worker") {
		t.Fatal("stopped row not filtered")
	}
	clickNamed(t, tester, locales.DockerCtrShowStopped())
	clickNamed(t, tester, locales.DockerGroupByProject())
	tester.Frame()
	if !page.flat || slices.Contains(tester.Texts(), "shop") {
		t.Fatal("flat list still shows Compose heading")
	}
	clickNamed(t, tester, locales.DockerSortBy(locales.Cpu()))
	if len(page.visible) == 0 || dockerContainer(s, page.visible[0]).Name != "shop-api-1" {
		t.Fatalf("CPU sort order: %v", ctrTargetNames(s, page.visible))
	}
	clickNamed(t, tester, locales.DockerFilter())
	tester.Type("worker")
	tester.Frame()
	if tester.HasText("gitea") || !tester.HasText("report-worker") {
		t.Fatal("search did not filter containers")
	}
	clickNamed(t, tester, locales.ManagerSelectRow("report-worker"))
	managerClickWith(t, tester, ui.Shift, "worker-backup")
	tester.Frame()
	if got := ctrTargetNames(s, page.selectedIDs()); !slices.Equal(got, []string{"report-worker", "worker-backup"}) {
		t.Fatalf("selection: %v", got)
	}
	open := func() {
		clickNamed(t, tester, locales.DockerCtrSelection(2))
		if err := tester.ChooseMenuItem(locales.DockerStop()); err == nil {
			t.Fatal("enabled stop on stopped containers")
		}
		chooseDockerAction(t, tester, locales.DockerRemove())
		wantTexts(t, tester, "batch confirmation", locales.DockerRemoveWarning())
	}
	open()
	clickButton(t, tester, locales.Cancel())
	ctrNothingRan(t, calls)
	open()
	clickButton(t, tester, locales.DockerRemove())
	want := "export LC_ALL=C\nexec 2>&1\ndocker 'rm' '" + s.overview.docker.d.Containers[4].ID + "' '" + strings.Repeat("b", 64) + "'\n"
	call := waitDockerAction(t, calls, want)
	ctrReply(t, a, platform, reader, call, "engine refused")
	wantTexts(t, tester, "failure output", locales.DockerActionFailed(), "engine refused")
}

func TestDockerCtrFormAndActions(t *testing.T) {
	a, platform, tester, reader, calls := ctrTestApp(t, true)
	s := a.sessions[0]
	ct := &s.overview.docker.d.Containers[3]
	tester.RightClickAt(rowCenter(t, tester, "shop-web-1"))
	if err := tester.ChooseMenuItem(locales.DockerCtrEdit()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Compose edit: %v", err)
	}
	tester.CloseMenu()
	tester.RightClickAt(rowCenter(t, tester, "report-worker"))
	if err := tester.ChooseMenuItem(locales.DockerCtrFiles()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("stopped files: %v", err)
	}
	tester.CloseMenu()
	clickButton(t, tester, locales.DockerCtrNew())
	form := a.ctrDlg.ctr.form
	form.config.Image, form.config.Name = "nginx:alpine", "demo-new"
	form.env = []ctrEnvRow{{"TOKEN", "sample-secret"}}
	form.showPreview = true
	tester.Frame()
	if tester.HasText("sample-secret") {
		t.Fatal("preview exposed environment value")
	}
	clickButton(t, tester, locales.DockerCtrCreate())
	clickButton(t, tester, locales.Cancel())
	ctrNothingRan(t, calls)
	clickButton(t, tester, locales.DockerCtrCreate())
	clickButton(t, tester, locales.DockerCtrCreate())
	call := waitDockerAction(t, calls, "docker 'run' '-d' '--name' 'demo-new' '--pull' 'missing' '--env' 'TOKEN="+form.env[0].value+"' '--restart' 'no' '--' '"+form.config.Image+"'\n")
	call.progress("pull 50%\n")
	call.progress("TOKEN=sample-")
	call.progress("secret\n")
	platform.until(t, "live pull output", func() bool { return tester.HasText("pull 50%") })
	if tester.HasText(form.env[0].value) {
		t.Fatal("streamed output exposed environment value")
	}
	ctrReply(t, a, platform, reader, call, "created TOKEN="+form.env[0].value)
	if tester.HasText(form.env[0].value) {
		t.Fatal("final output exposed environment value")
	}
	clickButton(t, tester, locales.Close())
	rowMenu(t, tester, "gitea", locales.DockerCtrEdit())
	form = a.ctrDlg.ctr.form
	if !form.edit || form.config.Name != "gitea" || form.config.WorkDir != "/app" || form.env[0].value != "sample-secret" {
		t.Fatal("edit not prefilled")
	}
	form.config.Memory = "64m"
	tester.Frame()
	clickButton(t, tester, locales.DockerCtrApply())
	clickButton(t, tester, locales.Cancel())
	ctrNothingRan(t, calls)
	clickButton(t, tester, locales.DockerCtrApply())
	clickButton(t, tester, locales.DockerCtrApply())
	call = waitDockerAction(t, calls, `"Memory":67108864`)
	if !strings.Contains(call.script, "'rename' '"+ct.ID+"'") || !strings.Contains(call.script, "rollback") {
		t.Fatal("edit did not use transactional replacement")
	}
	ctrReply(t, a, platform, reader, call, "replaced")
	clickButton(t, tester, locales.Close())
	rowMenu(t, tester, "gitea", locales.DockerCtrDuplicate())
	if config := a.ctrDlg.ctr.form.config; config.Name != "gitea-copy" || config.Image != ct.Image || config.IPv4 != "" {
		t.Fatalf("duplicate config: %q %q %q", config.Name, config.Image, config.IPv4)
	}
	clickButton(t, tester, locales.Cancel())
	for _, action := range []struct{ title, value, want string }{
		{locales.DockerCtrCommit(), "gitea:snapshot", "'commit' '--pause=true' '--message' '' '--author' '' '--' '" + ct.ID + "' 'gitea:snapshot'"},
		{locales.DockerCtrExport(), "/tmp/gitea.tar", "'export' '-o' \"$tmp\" '--' '" + ct.ID + "'"},
	} {
		rowMenu(t, tester, "gitea", action.title)
		clickButton(t, tester, locales.Cancel())
		ctrNothingRan(t, calls)
		rowMenu(t, tester, "gitea", action.title)
		a.ctrDlg.ctr.task.value = action.value
		tester.Frame()
		clickButton(t, tester, action.title)
		call = waitDockerAction(t, calls, action.want)
		ctrReply(t, a, platform, reader, call, "ok")
		clickButton(t, tester, locales.Close())
	}
}

const ctrDemoListing = `total 16
drwxr-xr-x 4 0 0 4096 1723161600 "."
drwxr-xr-x 4 0 0 4096 1723161600 ".."
drwxr-xr-x 2 1000 1000 4096 1723161600 "app"
drwx------ 2 0 0 4096 1723161600 "root"
-rw-r--r-- 1 1000 1000 512 1723161600 "read me.txt"
lrwxrwxrwx 1 0 0 4 1723161600 "lib" -> "/usr/lib"
`

func TestDockerCtrFilesAndPolling(t *testing.T) {
	a, platform, tester, reader, calls := ctrTestApp(t, true)
	id := a.sessions[0].overview.docker.d.Containers[3].ID
	rowMenu(t, tester, "gitea", locales.DockerCtrStats())
	call := waitDockerAction(t, calls, "'stats' '--no-stream' '--format' '{{json .}}' '"+id+"'")
	call.reply <- `{"Name":"gitea","CPUPerc":"7.5%","MemUsage":"12MiB / 32MiB","NetIO":"1kB / 2kB","PIDs":"2"}`
	platform.until(t, "stats sample", func() bool { return tester.HasText("7.5%") })
	tester.Advance(2100 * time.Millisecond)
	tester.Frame()
	call = waitDockerAction(t, calls, "'stats'")
	clickButton(t, tester, locales.Close())
	call.reply <- ""
	tester.Advance(5 * time.Second)
	tester.Frame()
	ctrNothingRan(t, calls)
	rowMenu(t, tester, "gitea", locales.DockerCtrFiles())
	call = waitDockerAction(t, calls, "'ls' '-lan' '--quoting-style=c' '--time-style=+%s' '--' '/.'")
	call.reply <- ctrDemoListing
	platform.until(t, "files listing", func() bool { return tester.HasText("read me.txt") })
	clickNamed(t, tester, "read me.txt")
	clickButton(t, tester, locales.DockerCtrCopyToServer())
	a.ctrDlg.ctr.task.file.value = "/tmp/read me.txt"
	tester.Frame()
	clickButton(t, tester, locales.Cancel())
	ctrNothingRan(t, calls)
	clickButton(t, tester, locales.DockerCtrCopyToServer())
	a.ctrDlg.ctr.task.file.value = "/tmp/read me.txt"
	tester.Frame()
	clickButton(t, tester, locales.DockerCtrCopyToServer())
	call = waitDockerAction(t, calls, "'cp' '--' '"+id+":/read me.txt' '/tmp/read me.txt'")
	ctrReply(t, a, platform, reader, call, "copied")
	clickButton(t, tester, locales.Close())
	call = waitDockerAction(t, calls, "'ls'")
	call.reply <- ctrDemoListing
	platform.until(t, "files refreshed", func() bool { return a.ctrDlg.ctr.files.cancel == nil })
	for _, action := range []struct {
		title, value, want string
		row                bool
	}{
		{locales.NewFolder(), "new folder", "'mkdir' '--' '/new folder'", false},
		{locales.DockerCtrCopyFromServer(), "/tmp/in.txt", "'cp' '--' '/tmp/in.txt' '" + id + ":/in.txt'", false},
		{locales.Rename(), "notes.txt", "'docker-rename' '/read me.txt' '/notes.txt'", true},
		{locales.Delete(), "", "'rm' '-r' '--' '/read me.txt'", true},
	} {
		open := func() {
			if action.row {
				rowMenu(t, tester, "read me.txt", action.title)
			} else {
				clickButton(t, tester, action.title)
			}
		}
		open()
		clickButton(t, tester, locales.Cancel())
		ctrNothingRan(t, calls)
		open()
		a.ctrDlg.ctr.task.file.value = action.value
		tester.Frame()
		clickButton(t, tester, action.title)
		call = waitDockerAction(t, calls, action.want)
		ctrReply(t, a, platform, reader, call, "ok")
		clickButton(t, tester, locales.Close())
		call = waitDockerAction(t, calls, "'ls'")
		call.reply <- ctrDemoListing
		platform.until(t, "files refreshed", func() bool { return a.ctrDlg.ctr.files.cancel == nil })
	}
}

func TestDockerCtrScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			a, platform, tester, _, calls := ctrTestApp(t, false)
			platform.SetLocale(lang)
			shots := func(scene string) {
				for _, style := range []string{"light", "dark"} {
					tester.SetDark(style == "dark")
					tester.Frame()
					tester.Frame()
					saveShot(t, tester, "docker-ctr-"+scene+"-"+style+"-"+lang+".png")
				}
			}
			shots("short")
			a.setPanelOnRight(true)
			a.rightWidth = 460
			shots("right")
			a.setPanelOnRight(false)
			s := a.sessions[0]
			a.openPanelTab(s, tabDocker)
			shots("full")
			clickNamed(t, tester, locales.ManagerSelectRow("report-worker"))
			clickNamed(t, tester, locales.ManagerSelectRow("worker-backup"))
			shots("selection")
			clickNamed(t, tester, locales.DockerCtrSelection(2))
			chooseDockerAction(t, tester, locales.DockerRemove())
			shots("remove")
			clickButton(t, tester, locales.Cancel())
			clickButton(t, tester, locales.DockerCtrNew())
			form := a.ctrDlg.ctr.form
			form.config.Image, form.config.Name, form.showPreview = "nginx:alpine", "web-demo", true
			form.config.WorkDir, form.config.User = "/usr/share/nginx/html", "101:101"
			form.command = `["nginx", "-g", "daemon off;"]`
			shots("create")
			clickButton(t, tester, locales.Cancel())
			rowMenu(t, tester, "gitea", locales.DockerCtrEdit())
			form = a.ctrDlg.ctr.form
			form.section, form.showPreview = 3, true
			shots("edit")
			clickButton(t, tester, locales.Cancel())
			rowMenu(t, tester, "gitea", locales.DockerCtrStats())
			stats := a.ctrDlg.ctr.stats
			call := waitDockerAction(t, calls, "'stats'")
			call.reply <- `{"Name":"gitea","CPUPerc":"7.5%","MemUsage":"12MiB / 32MiB","NetIO":"24kB / 16kB","BlockIO":"64kB / 8kB","PIDs":"2"}`
			platform.until(t, "first sample", func() bool { return stats.cancel == nil })
			series := stats.series[s.overview.docker.d.Containers[3].ID]
			for index := range 25 {
				sample := series.last
				sample.CPU = float64(4 + index*17%13)
				sample.MemUsed += uint64(index*11%10) << 16
				sample.NetRx += uint64(200 + index*113%500)
				sample.NetTx += uint64(100 + index*157%300)
				series.append(sample, series.at.Add(2*time.Second))
			}
			shots("stats")
			clickButton(t, tester, locales.Close())
			rowMenu(t, tester, "gitea", locales.DockerCtrFiles())
			call = waitDockerAction(t, calls, "'ls'")
			call.reply <- ctrDemoListing
			platform.until(t, "files", func() bool { return tester.HasText("read me.txt") })
			shots("files")
		})
	}
}
