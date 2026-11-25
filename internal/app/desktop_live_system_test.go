package app

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type desktopLiveSystem struct {
	app      *App
	platform *fakePlatform
	tester   *ui.Tester
	session  *Session
	desktop  *desktopState
	secret   string
	prompts  int
}

func openDesktopLiveSystem(t *testing.T, mutate bool) *desktopLiveSystem {
	t.Helper()
	spec := os.Getenv("FINALLYSHELL_TEST_SSH")
	if spec == "" {
		t.Skip("FINALLYSHELL_TEST_SSH not set")
	}
	credentials, address, found := strings.Cut(spec, "@")
	user, secret, hasSecret := strings.Cut(credentials, ":")
	if !found || !hasSecret || address != "127.0.0.1:2222" || (user != "root" && user != "tester") {
		t.Fatal("desktop live tests only target root or tester on fs-testbox at 127.0.0.1:2222")
	}
	if mutate && os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" {
		t.Skip("FINALLYSHELL_TEST_MUTATE=1 is required for server changes")
	}
	app, platform, session := connectTestServer(t)
	live := &desktopLiveSystem{app: app, platform: platform, session: session, secret: secret}
	t.Cleanup(app.CloseAll)
	live.tester = ui.NewTester(app.View, 1440, 900)
	platform.tt = live.tester
	platform.SetLocale("en")
	live.tester.SetDark(false)
	app.openDesktop(session)
	live.desktop = app.desktopOf(session)
	live.tester.Frame()
	return live
}

func (live *desktopLiveSystem) pump(t *testing.T, name string, duration time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(duration)
		for {
			for range cap(live.platform.posts) {
				select {
				case fn := <-live.platform.posts:
					fn()
				default:
				}
				if len(live.platform.posts) == 0 {
					break
				}
			}
			live.tester.Frame()
			if time.Now().After(deadline) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(duration + 10*time.Second):
		t.Fatalf("desktop froze while drawing %s", name)
	}
}

func (live *desktopLiveSystem) until(t *testing.T, name string, ready func() bool) {
	t.Helper()
	live.platform.until(t, name, func() bool {
		if kind := privilegeWindow(live.session); live.app.windowOpen(kind) {
			if live.session.Host.User != "tester" {
				t.Fatal("root was unexpectedly prompted for sudo")
			}
			prompt := live.platform.show(t, kind)
			if prompt.HasText(locales.PrivWrongPassword()) {
				t.Fatal("sudo rejected the test account credential")
			}
			typeInto(t, prompt, locales.PrivPassword(), live.secret)
			clickButton(t, prompt, locales.PrivRun())
			live.prompts++
		}
		return ready()
	})
}

func (live *desktopLiveSystem) exec(t *testing.T, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, stderr, err := monitor.RunScript(ctx, live.session.client, script)
	if err != nil {
		t.Fatalf("remote check failed: %v (%s)", err, stderr)
	}
	return out
}

func (live *desktopLiveSystem) rootCheck(t *testing.T, script string) string {
	t.Helper()
	if live.session.Host.User == "root" {
		return live.exec(t, script)
	}
	channel, err := live.session.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	timeout := time.AfterFunc(15*time.Second, func() { channel.Close() })
	defer timeout.Stop()
	channel.Stdin = strings.NewReader(live.secret + "\n")
	out, err := channel.Output(monitor.SystemCommand("sudo", "-S", "-k", "-p", "", "--", "sh", "-c", script))
	if err != nil {
		t.Fatalf("independent privileged check failed: %v", err)
	}
	return string(out)
}

func (live *desktopLiveSystem) shot(t *testing.T, name string) {
	t.Helper()
	saveShot(t, live.tester, fmt.Sprintf("live-%s-%s-light-%s.png", live.session.Host.User, name, live.platform.locale))
}

func TestDesktopLiveApps(t *testing.T) {
	live := openDesktopLiveSystem(t, false)
	app, tester, session := live.app, live.tester, live.session
	live.pump(t, "desktop", 200*time.Millisecond)
	live.shot(t, "desktop")
	seen := 0
	for _, entry := range desktopAppsFor(session) {
		if strings.HasPrefix(string(entry.id), "srv-") {
			seen++
		}
		t.Run(string(entry.id), func(t *testing.T) {
			window := app.desktopOpen(session, entry.id, nil)
			if window == nil {
				t.Fatal("registered app did not open")
			}
			t.Cleanup(func() {
				if slices.Contains(live.desktop.windows, window) {
					live.closeWindow(t, window)
				}
			})
			live.pump(t, string(entry.id), 20*time.Second)
			texts := tester.Texts()
			t.Logf("visible: %q", firstTexts(texts, 32))
			live.shot(t, string(entry.id))
			if !tester.HasText(window.displayTitle()) || tester.HasText(locales.DesktopComingSoon()) {
				t.Errorf("window did not draw: %v", window.displayTitle())
			}
			if entry.id == "srv-sites" && session.server.sites.status.Caddy != "" {
				if button, ok := tester.AccessByRole(ui.RoleButton, locales.TabCaddy()); !ok || button.Disabled {
					t.Error("the Sites app does not offer to open the detected Caddy instance")
				} else {
					systemClick(t, tester, locales.TabCaddy())
					if focused := live.desktop.focused; focused == nil || focused.app.id != "srv-caddy" {
						t.Fatal("Caddy did not open on the same desktop")
					} else {
						live.closeWindow(t, focused)
					}
				}
			}
			for _, text := range texts {
				for _, unexpected := range []string{"exit status 127", "exited with status 255", "command not found", "unexpected end of", "runtime error", "nil pointer dereference"} {
					if strings.Contains(strings.ToLower(text), unexpected) {
						t.Errorf("unexpected server error: %s", text)
					}
				}
			}
			desktopShellClick(t, tester, locales.DesktopMaximize())
			if !window.maximized {
				t.Fatal("window did not maximize")
			}
			desktopShellClick(t, tester, locales.DesktopMinimize())
			if !window.minimized {
				t.Fatal("window did not minimize")
			}
			app.desktopFocus(window)
			tester.Frame()
			live.closeWindow(t, window)
			if slices.Contains(live.desktop.windows, window) {
				t.Fatal("window did not close")
			}
		})
	}
	if seen != len(desktopServerTools()) {
		t.Fatalf("drew %d server tools, want %d", seen, len(desktopServerTools()))
	}
	live.platform.SetLocale("zh-Hans")
	app.desktopOpen(session, appTaskMgr, nil)
	app.desktopOpen(session, appTerminal, nil)
	live.pump(t, "localized desktop", time.Second)
	live.shot(t, "desktop")
	app.backToTerminal(app.desktopRowOf(session))
	tester.Frame()
	if app.current() != session {
		t.Fatal("terminal row is not active")
	}
	app.openDesktop(session)
	tester.Frame()
	client, ctx := session.client, session.ctx
	app.closeSession(session)
	live.pump(t, "closed session", 500*time.Millisecond)
	if app.desktopOf(session) != nil || session.client != nil || ctx.Err() == nil {
		t.Fatal("closing the session did not stop its desktop and SSH connection")
	}
	if channel, err := client.NewSession(); err == nil {
		channel.Close()
		t.Fatal("the closed SSH connection still accepts exec channels")
	}
}

func (live *desktopLiveSystem) closeWindow(t *testing.T, window *desktopWindow) {
	t.Helper()
	live.app.desktopFocus(window)
	live.app.desktopRequestClose(window)
	live.tester.Frame()
	if window.app.id == appTerminal && slices.Contains(live.desktop.windows, window) {
		systemClick(t, live.tester, locales.DesktopToolsCloseShell())
	}
	if slices.Contains(live.desktop.windows, window) {
		t.Fatal("window did not close")
	}
}

func TestDesktopLiveBrowser(t *testing.T) {
	live := openDesktopLiveSystem(t, false)
	app, session, tester := live.app, live.session, live.tester
	window := app.desktopOpen(session, appBrowser, nil)
	state := window.state.(*desktopBrowserState)
	live.until(t, "browser discovery and Caddy port", func() bool {
		return !state.discovering && slices.Contains(desktopBrowserPorts(session.net.snap), 80)
	})
	state.choices, state.choice = nil, desktopBrowserChoice{}
	tester.Frame()
	wantTexts(t, tester, "missing browser", locales.DesktopBrowserNoChoice())
	systemClick(t, tester, locales.DesktopBrowserChoose())
	if err := tester.ChooseMenuItem(locales.DesktopBrowserBrowse()); err != nil {
		t.Fatal(err)
	}
	live.until(t, "browser file chooser", func() bool { return len(live.platform.typedPicks()) == 1 })
	if pick := live.platform.typedPicks()[0]; pick.parent != WindowMain || pick.typeName != locales.DesktopBrowserExecutable() {
		t.Fatal("browser did not request a local executable")
	}
	if state.choice.path != "" || state.run != nil {
		t.Fatal("cancelled chooser selected or launched a browser")
	}
	const target = "http://127.0.0.1/"
	expected := live.exec(t, "cat /srv/www/index.html")
	state.choice = desktopBrowserChoice{name: "Chromium fixture", path: "fixture"}
	for _, stop := range []string{"button", "disconnect"} {
		process := &desktopTestBrowserProcess{done: make(chan struct{})}
		args := make(chan []string, 1)
		state.launch = func(run *desktopBrowserRun, url string) (desktopBrowserProcess, error) {
			args <- desktopBrowserArgs(run.choice, run.profile, run.addr, url, false)
			return process, nil
		}
		state.url = target
		tester.Frame()
		systemClick(t, tester, locales.DesktopBrowserStart())
		live.until(t, "browser proxy listening", func() bool { return state.run != nil || state.issue != nil })
		if state.run == nil {
			t.Fatal(state.issue())
		}
		address := state.run.addr
		argv := <-args
		if !slices.Contains(argv, "--proxy-server=socks5://"+address) || !slices.Contains(argv, "--proxy-bypass-list=<-loopback>") || argv[len(argv)-1] != target {
			t.Fatalf("browser arguments do not send remote localhost through SOCKS: %v", argv)
		}
		transport := &http.Transport{DialContext: desktopBrowserDialer(address).DialContext}
		client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
		response, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != expected {
			t.Fatalf("remote Caddy response differs from the file: status %d, error %v", response.StatusCode, err)
		}
		live.shot(t, "browser-running")
		if stop == "button" {
			systemClick(t, tester, locales.DesktopBrowserStop())
		} else {
			app.closeSession(session)
		}
		live.until(t, "browser proxy stopped", func() bool { return state.run == nil })
		select {
		case <-process.done:
		default:
			t.Fatal("browser process was not stopped")
		}
		if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
			conn.Close()
			t.Fatal("SOCKS listener survived " + stop)
		}
	}
}

func TestDesktopLiveTaskPerformance(t *testing.T) {
	live := openDesktopLiveSystem(t, false)
	app, session := live.app, live.session
	window := app.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	var reads atomic.Int64
	client := session.client
	state.run = func(ctx context.Context, script string) (string, string, error) {
		reads.Add(1)
		return monitor.RunScript(ctx, client, script)
	}
	live.until(t, "two Task Manager samples", func() bool {
		return state.perfErr != nil || len(state.history["cpu"]) >= 2 && len(state.history["rx/eth0"]) >= 2
	})
	perf := state.perf
	if state.perfErr != nil || perf.Logical < 1 || perf.Processes < 2 || perf.Threads < perf.Processes || perf.Hardware.Model == "" || len(perf.CPUs) < 2 {
		t.Fatalf("incomplete performance sample: error %v, cores=%d, processes=%d, threads=%d, model=%q", state.perfErr, perf.Logical, perf.Processes, perf.Threads, perf.Hardware.Model)
	}
	for _, load := range perf.Load {
		if math.IsNaN(load) || math.IsInf(load, 0) || load < 0 {
			t.Fatalf("invalid load: %v", perf.Load)
		}
	}
	for _, cpu := range perf.CPUs {
		var total float64
		for _, percent := range cpu.Time {
			total += percent
		}
		if math.Abs(total-100) > 0.01 || cpu.Usage() > 100 {
			t.Fatalf("invalid CPU split for %s: %v", cpu.Name, cpu.Time)
		}
	}
	if session.stats.MemUsed == 0 || session.stats.MemUsed > session.stats.MemTotal || len(state.history["mem"]) < 2 {
		t.Fatal("invalid memory samples")
	}
	selected, disks := state.diskChoice()
	_, ifaces := state.ifaceChoice(&session.stats)
	if len(disks) == 0 || !slices.Contains(ifaces, "eth0") || !slices.Contains(ifaces, "lo") {
		t.Fatalf("missing selector data: disks %v, interfaces %v", disks, ifaces)
	}
	if !slices.ContainsFunc(perf.Disks, func(disk monitor.TaskDisk) bool {
		return disk.Measured && disk.Capacity > 0 && disk.Read >= 0 && disk.Write >= 0 && disk.Busy >= 0 && disk.Busy <= 100
	}) {
		t.Fatal("no real block device has usable counters")
	}
	if len(selected.Mounts) == 0 && slices.ContainsFunc(perf.Disks, func(disk monitor.TaskDisk) bool { return len(disk.Mounts) != 0 }) {
		t.Fatal("the disk selector defaulted to an unmounted device")
	}
	t.Logf("CPU %q, cache %v; disks %v; interfaces %v; load %v", perf.Hardware.Model, perf.Hardware.Cache, disks, ifaces, perf.Load)
	for metric, name := range []string{"load", "cpu", "memory", "disk", "network"} {
		state.metric = taskMetric(metric)
		live.tester.Frame()
		live.shot(t, "task-"+name)
	}
	watch := state.poll.run
	if watch == nil {
		t.Fatal("visible performance poll not running")
	}
	desktopShellClick(t, live.tester, locales.DesktopMinimize())
	live.pump(t, "minimized Task Manager", 500*time.Millisecond)
	readCount := reads.Load()
	live.pump(t, "no minimized SSH reads", procRefresh+100*time.Millisecond)
	if state.poll.run != nil || reads.Load() != readCount {
		t.Fatal("Task Manager still polling while minimized")
	}
	app.desktopFocus(window)
	live.until(t, "restored performance poll", func() bool { return state.poll.run != nil && state.poll.run != watch })
	live.closeWindow(t, window)
	live.pump(t, "closed Task Manager", 300*time.Millisecond)
	readCount = reads.Load()
	live.pump(t, "no closed SSH reads", procRefresh+100*time.Millisecond)
	if state.poll.run != nil || reads.Load() != readCount {
		t.Fatal("Task Manager still polling after close")
	}
}

func TestDesktopLiveTaskProcesses(t *testing.T) {
	live := openDesktopLiveSystem(t, true)
	app, session, tester := live.app, live.session, live.tester
	delete(app.passwords, session.Host.ID)
	scratch := strings.TrimSpace(live.exec(t, "mktemp -d /tmp/fs-sys-XXXXXXXX"))
	if !strings.HasPrefix(scratch, "/tmp/fs-sys-") || strings.Contains(scratch[len("/tmp/fs-sys-"):], "/") {
		t.Fatal("invalid scratch directory from the test box")
	}
	t.Cleanup(func() { live.exec(t, monitor.SystemCommand("rm", "-rf", "--", scratch)) })
	file := scratch + "/进程属性与网络连接-" + strings.Repeat("long-path-", 10) + ".txt"
	const synthetic = "synthetic-masked-value"
	const fixture = `import os, socket, subprocess, sys
f = open(sys.argv[1], 'w')
listener = socket.socket()
listener.bind(('127.0.0.1', 0))
listener.listen()
children = [subprocess.Popen(['sleep', '9999']) for i in range(2)]
print(os.getpid(), listener.getsockname()[1], *(child.pid for child in children), flush=True)
try:
    sys.stdin.buffer.read(1)
finally:
    for child in children:
        if child.poll() is None:
            child.kill()
        child.wait()
`
	channel, err := session.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { channel.Close() })
	input, err := channel.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		maximum := time.AfterFunc(5*time.Second, func() { channel.Close() })
		defer maximum.Stop()
		if err := channel.Wait(); err != nil {
			t.Errorf("clean process fixture: %v", err)
		}
	})
	output, err := channel.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.Start("exec " + monitor.SystemCommand("env", "-i", "PATH=/usr/bin:/bin", "FS_SECRET_TOKEN="+synthetic, "python3", "-c", fixture, file)); err != nil {
		t.Fatal(err)
	}
	maximum := time.AfterFunc(8*time.Second, func() { channel.Close() })
	line, err := bufio.NewReader(output).ReadString('\n')
	maximum.Stop()
	var parentPID, port, sleepPID, killPID int
	if _, scanErr := fmt.Sscan(line, &parentPID, &port, &sleepPID, &killPID); err != nil || scanErr != nil || parentPID <= 1 || sleepPID <= 1 || killPID <= 1 || port <= 0 {
		t.Fatalf("invalid synthetic process fixture: %v, %v", err, scanErr)
	}
	other, err := session.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	otherPIDFile := scratch + "/other.pid"
	otherCommand := "printf '%s\\n' \"$$\" > " + monitor.SystemCommand(otherPIDFile) + "; exec sleep 9999"
	if session.Host.User == "tester" {
		other.Stdin = strings.NewReader(live.secret + "\n")
		otherCommand = "exec " + monitor.SystemCommand("sudo", "-S", "-k", "-p", "", "--", "sh", "-c", otherCommand)
	} else {
		live.exec(t, monitor.SystemCommand("chown", "tester", scratch))
		otherCommand = "exec " + monitor.SystemCommand("runuser", "-u", "tester", "--", "sh", "-c", otherCommand)
	}
	var otherPID int
	t.Cleanup(func() {
		if otherPID > 1 {
			live.rootCheck(t, fmt.Sprintf("kill -KILL %d 2>/dev/null || :", otherPID))
		}
		other.Close()
	})
	if err := other.Start(otherCommand); err != nil {
		t.Fatal(err)
	}
	otherPID, _ = strconv.Atoi(strings.TrimSpace(live.exec(t, "for attempt in 1 2 3 4 5 6 7 8 9 10; do if test -s "+monitor.SystemCommand(otherPIDFile)+"; then cat "+monitor.SystemCommand(otherPIDFile)+"; exit; fi; sleep .2; done; exit 1")))
	if otherPID <= 1 {
		t.Fatal("the cross-user process did not start")
	}
	window := app.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	tester.Frame()
	systemClick(t, tester, locales.DesktopTaskProcesses())
	find := func(pid int) monitor.TaskProcess {
		for _, row := range state.processes {
			if row.PID == pid {
				return row
			}
		}
		return monitor.TaskProcess{}
	}
	live.until(t, "real processes", func() bool { return state.processErr != nil || find(otherPID).Start != 0 && find(sleepPID).Start != 0 })
	count, _ := strconv.Atoi(strings.TrimSpace(live.exec(t, "ps -e -o pid= | wc -l")))
	if state.processErr != nil || count < 3 || math.Abs(float64(len(state.processes)-count)) > float64(max(count/3, 12)) {
		t.Fatalf("process count = %d, ps = %d: %v", len(state.processes), count, state.processErr)
	}
	rows := taskProcessRows(state.processes, "", ui.SortOrder{Column: "pid"}, false)
	if !slices.IsSortedFunc(rows, func(left, right taskProcessRow) int { return left.proc.PID - right.proc.PID }) {
		t.Fatal("processes are not sorted by PID")
	}
	rows = taskProcessRows(state.processes, "", ui.SortOrder{Column: "pid"}, true)
	if !slices.ContainsFunc(rows, func(row taskProcessRow) bool {
		return row.proc.PID == sleepPID && row.proc.PPID == parentPID && row.depth > 0
	}) {
		t.Fatal("tree did not nest the sleep under its real parent")
	}
	state.filter = "进程属性与网络连接-"
	if rows := state.shownProcesses(); len(rows) != 1 || rows[0].proc.PID != parentPID {
		t.Fatalf("search did not isolate the fixture; got %d rows", len(rows))
	}
	tester.Frame()
	live.shot(t, "task-processes")
	state.filter = ""
	app.desktopTaskOpenDetails(state, find(parentPID))
	for _, tab := range []struct {
		property monitor.TaskProperty
		name     string
		check    func(monitor.TaskDetails) bool
	}{
		{monitor.TaskBasic, "basic", func(d monitor.TaskDetails) bool {
			return strings.Contains(strings.Join(d.Basic.Args, " "), scratch) && d.Status.Threads > 0
		}},
		{monitor.TaskMaps, "maps", func(d monitor.TaskDetails) bool { return len(d.Maps) > 2 && d.Maps[0].Bytes > 0 }},
		{monitor.TaskFiles, "files", func(d monitor.TaskDetails) bool {
			return slices.ContainsFunc(d.Files, func(fd monitor.TaskFD) bool { return fd.Target == file })
		}},
		{monitor.TaskEnv, "environment", func(d monitor.TaskDetails) bool {
			return slices.ContainsFunc(d.Env, func(v monitor.TaskEnvVar) bool {
				return v.Name == "FS_SECRET_TOKEN" && v.Sensitive && v.Display(false) != synthetic && v.Display(true) == synthetic
			})
		}},
		{monitor.TaskSockets, "sockets", func(d monitor.TaskDetails) bool {
			return slices.ContainsFunc(d.Sockets, func(socket monitor.Socket) bool {
				return socket.Listening() && socket.Local == netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(port))
			})
		}},
	} {
		detail := state.details
		app.desktopTaskSelectProperty(detail, tab.property, false)
		live.until(t, tab.name, func() bool { return !detail.load })
		if detail.err != nil || !tab.check(detail.result) {
			t.Errorf("%s: err=%v, missing=%v", tab.name, detail.err, detail.result.Missing)
		}
		if tab.property == monitor.TaskEnv {
			tester.Frame()
			wantTexts(t, tester, "masked environment", "FS_SECRET_TOKEN", "••••••")
		}
		if tester.HasText(synthetic) {
			t.Fatal("the task UI exposed the synthetic secret without consent")
		}
		live.shot(t, "task-property-"+tab.name)
	}
	app.desktopTaskCloseDetails(state)
	proc := find(otherPID)
	if proc.User == session.Host.User || proc.Name != "sleep" {
		t.Fatalf("fixture did not exercise a process owned by the other account: user %q, name %q", proc.User, proc.Name)
	}
	act := func(proc monitor.TaskProcess, kind monitor.TaskAction, nice int) {
		t.Helper()
		app.desktopTaskAsk(state, proc, kind)
		action := state.action
		if action == nil {
			t.Fatalf("action %v not available for pid %d", kind, proc.PID)
		}
		app.desktopTaskRunAction(session, state, action, nice)
		live.until(t, "real process action", func() bool { return !action.busy })
		if action.err != nil || !state.succeeded {
			t.Fatalf("action %v for pid %d: %v", kind, proc.PID, action.err)
		}
	}
	ps := func(pid int, field string) string {
		return strings.TrimSpace(live.exec(t, fmt.Sprintf("ps -p %d -o %s= || :", pid, field)))
	}
	act(proc, monitor.TaskStop, 0)
	if ps(otherPID, "state") != "T" {
		t.Fatal("Stop did not suspend the cross-user sleep")
	}
	proc.State = 'T'
	act(proc, monitor.TaskContinue, 0)
	if ps(otherPID, "state") == "T" {
		t.Fatal("Continue did not resume the cross-user sleep")
	}
	act(proc, monitor.TaskRenice, 7)
	if ps(otherPID, "nice") != "7" {
		t.Fatal("renice did not set priority")
	}
	act(proc, monitor.TaskTerminate, 0)
	live.until(t, "TERM means gone", func() bool { state := ps(otherPID, "state"); return state == "" || state == "Z" })
	otherPID = 0
	act(find(killPID), monitor.TaskKill, 0)
	live.until(t, "KILL means gone", func() bool { state := ps(killPID, "state"); return state == "" || state == "Z" })
	if session.Host.User == "tester" && live.prompts == 0 {
		t.Fatal("cross-user actions did not exercise the sudo credential prompt")
	}
	state.metric, state.page = taskMemory, taskPerformance
	app.stopPoll(&state.poll)
	live.until(t, "memory counters", func() bool { return memoryActionBlock(session.stats.Memory, monitor.MemoryDropCaches) == nil })
	app.desktopTaskAskMemory(state, monitor.MemoryDropCaches)
	action := state.action
	app.desktopTaskRunAction(session, state, action, 0)
	live.until(t, "release memory result", func() bool { return !action.busy })
	if action.err != nil || !state.succeeded {
		t.Fatalf("real cache release failed: %v", action.err)
	}
	if len(live.desktop.log) < 6 || live.desktop.log[len(live.desktop.log)-1].err != "" {
		t.Fatal("successful app actions were not logged")
	}
	app.desktopOpen(session, appSettings, "log")
	tester.Frame()
	wantTexts(t, tester, "last Task Manager action in Settings", live.desktop.log[len(live.desktop.log)-1].text)
}

func TestDesktopLiveSettingsInfo(t *testing.T) {
	live := openDesktopLiveSystem(t, false)
	app, session, tester := live.app, live.session, live.tester
	delete(app.passwords, session.Host.ID)
	window := app.desktopOpen(session, appSettings, "info")
	settings := func() *desktopSettingsState { return window.state.(*desktopSettingsState) }
	live.until(t, "system info", func() bool { return settings().info.load.loaded })
	state := settings()
	info := state.info
	host, kernel, _ := strings.Cut(strings.TrimSpace(live.exec(t, "hostname; uname -r")), "\n")
	if info.load.failure != "" || info.summary.Hostname != host || info.summary.Kernel != kernel || !strings.Contains(info.summary.OS, "Debian GNU/Linux 12") ||
		info.summary.Uptime <= 0 || info.summary.CPUModel == "" || info.summary.MemTotal == 0 || len(info.summary.Disks) == 0 ||
		info.details.Distro != "debian" || !slices.ContainsFunc(info.details.Mounts, func(mount monitor.SysConfMount) bool { return mount.Mount == "/" && mount.Size > 0 }) {
		t.Fatalf("system info did not match the box: %+v; %+v; %s", info.summary, info.details, info.load.failure)
	}
	if !slices.ContainsFunc(info.details.Addrs, func(item monitor.Addr) bool {
		prefix, err := netip.ParsePrefix(item.Addr)
		return err == nil && prefix.Addr().IsPrivate() && item.Iface == "eth0"
	}) {
		t.Fatal("the real eth0 private address was missing")
	}
	if info.details.Virtualization != "docker" {
		t.Errorf("the Docker guest is displayed as %q", info.details.Virtualization)
	}
	live.shot(t, "settings-info")
	systemClick(t, tester, locales.RenameConnection())
	typeInto(t, tester, locales.DesktopSetAlias(), "现场 Debian · live fixture")
	systemClick(t, tester, locales.Save())
	if session.Host.Name != "现场 Debian · live fixture" {
		t.Fatal("the connection alias was not saved")
	}
	live.platform.SetLocale("zh-Hans")
	for _, page := range []string{"info", "personalize", "security"} {
		app.desktopOpen(session, appSettings, page)
		live.pump(t, page, 250*time.Millisecond)
		live.shot(t, "settings-"+page)
	}
	live.platform.SetLocale("en")
	app.desktopOpen(session, appSettings, "log")
	tester.Frame()
	live.shot(t, "settings-operations")
	state.log.tab = 1
	live.until(t, "last logins", func() bool { return state.log.load.loaded })
	if state.log.load.failure != "" {
		t.Fatal(state.log.load.failure)
	}
	if expected := monitor.ParseSysConfLogins(live.exec(t, "LC_ALL=C last -F -n 30"), false); len(expected) > 0 && len(state.log.logins) == 0 {
		t.Fatal("last returned records but none were shown")
	}
	systemClick(t, tester, locales.DesktopSetLoadFailed())
	live.until(t, "lastb", func() bool { return state.log.load.cancel == nil })
	if !state.log.failedLoad || state.log.loginErr != "" || session.Host.User == "tester" && live.prompts == 0 {
		t.Fatalf("lastb did not finish through the privilege layer: %s", state.log.loginErr)
	}
	live.shot(t, "settings-logins")
}

func (live *desktopLiveSystem) settingAction(t *testing.T, state *desktopSettingsState, label string) string {
	t.Helper()
	systemClick(t, live.tester, label)
	if state.dialog == nil {
		t.Fatalf("no confirmation for %s", label)
	}
	systemClick(t, live.tester, locales.SvcConfirm())
	live.until(t, label, func() bool { return state.dialog != nil && state.dialog.done })
	failure := state.dialog.failure
	systemClick(t, live.tester, locales.Close())
	return failure
}

func TestDesktopLiveSettingsDNS(t *testing.T) {
	live := openDesktopLiveSystem(t, true)
	delete(live.app.passwords, live.session.Host.ID)
	const resolv, backup = monitor.SysConfResolvPath, monitor.SysConfResolvBackup
	before := strings.TrimSpace(live.exec(t, "base64 -w0 "+resolv))
	if data, err := base64.StdEncoding.DecodeString(before); err != nil || len(data) == 0 {
		t.Fatal("could not preserve the original resolver bytes")
	}
	if live.rootCheck(t, "if test -e "+backup+" || test -L "+backup+"; then echo exists; fi") != "" {
		t.Fatal("an existing Finallyshell DNS backup must not be touched")
	}
	t.Cleanup(func() {
		live.rootCheck(t, "set -e; if [ \"$(base64 -w0 "+resolv+")\" != "+monitor.SystemCommand(before)+" ]; then printf %s "+monitor.SystemCommand(before)+" | base64 -d > "+resolv+"; fi; rm -f -- "+backup)
		if got := strings.TrimSpace(live.exec(t, "base64 -w0 "+resolv)); got != before {
			t.Error("original resolver bytes were not restored")
		}
	})
	window := live.app.desktopOpen(live.session, appSettings, "dns")
	live.until(t, "DNS read", func() bool { return window.state.(*desktopSettingsState).dns.load.loaded })
	state := window.state.(*desktopSettingsState)
	if state.dns.load.failure != "" || len(state.dns.data.File.Servers) == 0 {
		t.Fatalf("DNS read failed: %s", state.dns.load.failure)
	}
	decoded, _ := base64.StdEncoding.DecodeString(before)
	if !slices.Equal(state.dns.data.File.Servers, monitor.ParseResolvConf(string(decoded)).Servers) {
		t.Fatal("DNS read differs from the resolver file")
	}
	systemClick(t, live.tester, locales.DesktopSetDNSTest())
	live.until(t, "DNS resolution", func() bool { return state.dns.probe.loaded && state.dns.probe.cancel == nil })
	if !strings.Contains(state.dns.result, "example.com") {
		t.Errorf("DNS probe failed: %s", state.dns.probe.failure)
	}
	live.shot(t, "settings-dns")
	mount := strings.TrimSpace(live.exec(t, "findmnt -rn -o TARGET --target "+resolv))
	if mount == resolv && state.dns.data.Mode == monitor.SysConfDNSManaged {
		wantTexts(t, live.tester, "mount-managed DNS explanation", locales.DesktopSetDNSMounted())
		for _, label := range []string{locales.DesktopSetDNSApply(), locales.DesktopSetDNSRestore()} {
			if button, ok := live.tester.AccessByRole(ui.RoleButton, label); !ok || !button.Disabled {
				t.Fatalf("managed DNS action should be disabled: %s", label)
			}
		}
		if _, err := monitor.BuildSysConfDNS(state.dns.data, monitor.DNSValues{Servers: []string{"1.1.1.1"}}, false); err == nil {
			t.Fatal("DNS builder allowed an edit on a managed mount")
		}
		stale, err := monitor.BuildSysConfDNS(monitor.SysConfDNS{Mode: monitor.SysConfDNSPlain}, monitor.DNSValues{Servers: strings.Fields(desktopSetDNSPresets[1].servers)}, false)
		if err != nil {
			t.Fatal(err)
		}
		live.app.desktopSetChange(window, state, locales.DesktopSetDNSApply(), "", stale, false, func() {})
		live.tester.Frame()
		systemClick(t, live.tester, locales.SvcConfirm())
		live.until(t, "stale DNS edit rejected", func() bool { return state.dialog.done })
		if state.dialog.failure != locales.DesktopSetDNSChanged() || live.session.Host.User == "tester" && live.prompts == 0 ||
			live.rootCheck(t, "if test -e "+backup+"; then echo exists; fi") != "" {
			t.Fatalf("stale resolver change was not safely rejected: %s", state.dialog.failure)
		}
		if got := strings.TrimSpace(live.exec(t, "base64 -w0 "+resolv)); got != before {
			t.Fatal("reading and probing managed DNS changed it")
		}
		systemClick(t, live.tester, locales.Close())
		live.checkPlainResolver(t, window, state, before)
		return
	}
	if state.dns.data.Mode != monitor.SysConfDNSPlain {
		t.Fatalf("unexpected resolver mode for fs-testbox: %s", state.dns.data.Mode)
	}
	state.dns.servers = desktopSetDNSPresets[1].servers
	live.tester.Frame()
	failure := live.settingAction(t, state, locales.DesktopSetDNSApply())
	live.until(t, "DNS refresh", func() bool { return state.dns.load.cancel == nil })
	if mount == resolv {
		t.Fatalf("mounted resolv.conf was presented as writable; apply returned %q, backup=%v", failure, state.dns.data.Backup)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if got := monitor.ParseResolvConf(live.exec(t, "cat "+resolv)); !slices.Equal(got.Servers, strings.Fields(desktopSetDNSPresets[1].servers)) {
		t.Fatal("the preset was not applied")
	}
	if failure = live.settingAction(t, state, locales.DesktopSetDNSRestore()); failure != "" {
		t.Fatal(failure)
	}
	if got := strings.TrimSpace(live.exec(t, "base64 -w0 "+resolv)); got != before {
		t.Fatal("DNS backup restore changed original bytes")
	}
}

func (live *desktopLiveSystem) checkPlainResolver(t *testing.T, window *desktopWindow, state *desktopSettingsState, original string) {
	t.Helper()
	scratch := strings.TrimSpace(live.exec(t, "mktemp -d /tmp/fs-sys-XXXXXXXX"))
	if !strings.HasPrefix(scratch, "/tmp/fs-sys-") || strings.Contains(scratch[len("/tmp/fs-sys-"):], "/") {
		t.Fatal("invalid scratch directory from the test box")
	}
	t.Cleanup(func() { live.rootCheck(t, monitor.SystemCommand("rm", "-rf", "--", scratch)) })
	fixture := scratch + "/resolv.conf"
	live.exec(t, "printf %s "+monitor.SystemCommand(original)+" | base64 -d > "+monitor.SystemCommand(fixture))
	values := monitor.DNSValues{Servers: strings.Fields(desktopSetDNSPresets[1].servers), Search: state.dns.data.File.Search}
	plain := monitor.SysConfDNS{Mode: monitor.SysConfDNSPlain}
	for _, restore := range []bool{false, true} {
		label := locales.DesktopSetDNSApply()
		if restore {
			plain.Backup, label = true, locales.DesktopSetDNSRestore()
		}
		change, err := monitor.BuildSysConfDNS(plain, values, restore)
		if err != nil {
			t.Fatal(err)
		}
		change.Target = strings.ReplaceAll(change.Target, monitor.SysConfResolvPath, fixture)
		change.Script = strings.ReplaceAll(change.Script, monitor.SysConfResolvPath, fixture)
		live.app.desktopSetChange(window, state, label, "", change, false, func() {})
		live.tester.Frame()
		systemClick(t, live.tester, locales.SvcConfirm())
		live.until(t, label, func() bool { return state.dialog.done })
		if state.dialog.failure != "" {
			t.Fatal(state.dialog.failure)
		}
		systemClick(t, live.tester, locales.Close())
		if restore {
			if got := strings.TrimSpace(live.exec(t, "base64 -w0 "+fixture)); got != original {
				t.Fatal("restoring the writable resolver fixture did not recover its exact bytes")
			}
		} else {
			got := monitor.ParseResolvConf(live.exec(t, "cat "+fixture))
			if !slices.Equal(got.Servers, values.Servers) || !slices.Equal(got.Options, state.dns.data.File.Options) {
				t.Fatalf("fixture preset did not preserve options: %+v", got)
			}
		}
	}
}

func TestDesktopLiveSettingsTime(t *testing.T) {
	live := openDesktopLiveSystem(t, true)
	delete(live.app.passwords, live.session.Host.ID)
	original := strings.TrimSpace(live.exec(t, "timedatectl show -p Timezone --value"))
	link := strings.TrimSpace(live.exec(t, "readlink /etc/localtime"))
	before := live.exec(t, "readlink /etc/localtime; sha256sum /etc/timezone; timedatectl show -p NTP --value")
	if original == "Asia/Shanghai" || original == "" {
		t.Fatal("the test box needs its original non-Shanghai time zone")
	}
	t.Cleanup(func() {
		live.rootCheck(t, monitor.SystemCommand("timedatectl", "set-timezone", original))
		live.rootCheck(t, monitor.SystemCommand("ln", "-sfn", "--", link, "/etc/localtime"))
		if got := live.exec(t, "readlink /etc/localtime; sha256sum /etc/timezone; timedatectl show -p NTP --value"); got != before {
			t.Error("time zone files or NTP state were not restored")
		}
	})
	window := live.app.desktopOpen(live.session, appSettings, "time")
	live.until(t, "clock and time zones", func() bool { return window.state.(*desktopSettingsState).clock.load.loaded })
	state := window.state.(*desktopSettingsState)
	clock := &state.clock
	if clock.load.failure != "" || clock.data.Clock.IsZero() || clock.data.Zone != original || len(clock.zones) < 100 || !slices.Contains(clock.zones, "Asia/Shanghai") {
		t.Fatalf("invalid clock or timezone list: %s, zone %q, %d choices", clock.load.failure, clock.data.Zone, len(clock.zones))
	}
	clock.query, clock.zone = "Shanghai", "Asia/Shanghai"
	live.tester.Frame()
	if failure := live.settingAction(t, state, locales.DesktopSetSetZone()); failure != "" {
		t.Fatal(failure)
	}
	live.until(t, "updated time zone", func() bool { return clock.data.Zone == "Asia/Shanghai" })
	if got := strings.TrimSpace(live.exec(t, "timedatectl show -p Timezone --value; date +%z")); got != "Asia/Shanghai\n+0800" {
		t.Fatalf("the real time zone did not change: %q", got)
	}
	if !clock.data.CanNTP {
		t.Log("timedatectl reports CanNTP=no: no service may be toggled on this box")
		if button, ok := live.tester.AccessByRole(ui.RoleButton, locales.DesktopSetEnableNTP()); !ok || !button.Disabled {
			t.Fatal("NTP toggle was not disabled on an unsupported server")
		}
		live.app.desktopSetChange(window, state, locales.DesktopSetEnableNTP(), "", monitor.BuildSysConfNTP(true), false, func() {})
		live.tester.Frame()
		systemClick(t, live.tester, locales.SvcConfirm())
		live.until(t, "unsupported NTP operation", func() bool { return state.dialog.done })
		if !strings.Contains(strings.ToLower(state.dialog.failure), "ntp not supported") {
			t.Fatalf("timedatectl did not report understandable NTP failure: %s", state.dialog.failure)
		}
		systemClick(t, live.tester, locales.Close())
	}
	if _, supported := monitor.BuildSysConfSync(clock.data.Sync); !supported {
		t.Log("no supported synchronization service installed: Sync now is unavailable")
		if !clock.data.CanNTP {
			wantTexts(t, live.tester, "unsupported time services", locales.DesktopSetSyncUnavailable())
		}
		if button, ok := live.tester.AccessByRole(ui.RoleButton, locales.DesktopSetSyncNow()); !ok || !button.Disabled {
			t.Fatal("manual synchronization was not disabled without a supported service")
		}
	}
	if live.session.Host.User == "tester" && live.prompts == 0 {
		t.Fatal("time zone change did not exercise sudo")
	}
	live.platform.SetLocale("zh-Hans")
	live.tester.Frame()
	live.shot(t, "settings-time")
}
