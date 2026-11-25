package app

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestDesktopTaskLoadState(t *testing.T) {
	for _, test := range []struct {
		load  float64
		cores int
		state string
	}{
		{0.69, 1, locales.DesktopTaskSmooth()},
		{1.4, 2, locales.DesktopTaskBusy()},
		{2.0, 2, locales.DesktopTaskOverloaded()},
		{100, 0, "—"},
	} {
		if _, state := desktopTaskLoadState(test.load, test.cores); state != test.state {
			t.Errorf("load %.2f/%d: %q, want %q", test.load, test.cores, state, test.state)
		}
	}
}

func TestDesktopTaskProcessActions(t *testing.T) {
	a, p, tt, session := desktopTestApp(t)
	window := a.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	state.page, state.paused, state.processReady = taskProcesses, true, true
	state.processes = []monitor.TaskProcess{taskDemoProcess()}
	state.selected = taskProcessIDOf(state.processes[0])
	commands := make(chan string, 4)
	state.run = func(_ context.Context, script string) (string, string, error) {
		commands <- script
		if strings.Contains(script, "'STOP'") {
			return "", "unsupported operation", errors.New("exit")
		}
		return "", "", nil
	}
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click(locales.DesktopTaskTerminate())
	if state.action == nil || !strings.Contains(state.action.title(), "worker (PID 83)") {
		t.Fatalf("confirmation = %+v", state.action)
	}
	click(locales.Cancel())
	if len(commands) != 0 || len(a.desktopOf(session).log) != 0 {
		t.Fatal("cancel performed a remote action")
	}
	for _, test := range []struct {
		label   string
		suffix  string
		stopped bool
	}{
		{locales.DesktopTaskTerminate(), "kill '-s' 'TERM' '83'\n", false},
		{locales.DesktopTaskKill(), "kill '-s' 'KILL' '83'\n", false},
		{locales.DesktopTaskStop(), "kill '-s' 'STOP' '83'\n", false},
		{locales.DesktopTaskContinue(), "kill '-s' 'CONT' '83'\n", true},
		{locales.DesktopTaskRenice(), "renice '-n' '3' '-p' '83'\n", false},
	} {
		proc := taskDemoProcess()
		if test.stopped {
			proc.State = 'T'
		}
		state.processes = []monitor.TaskProcess{proc}
		state.processView = desktopTaskProcessView{}
		tt.Frame()
		name, found := tt.Find(proc.Name)
		if !found {
			t.Fatalf("process row not found: %q", tt.Texts())
		}
		tt.RightClickAt(name.X+8, name.Y+name.H/2)
		if err := tt.ChooseMenuItem(test.label); err != nil {
			t.Fatal(err)
		}
		if state.action == nil {
			t.Fatalf("no confirmation for %s", test.label)
		}
		click(locales.DesktopTaskConfirm())
		p.until(t, "action result", func() bool { return state.action == nil || !state.action.busy })
		select {
		case script := <-commands:
			if !strings.HasSuffix(script, test.suffix) || !strings.Contains(script, `"${20}" = '242'`) {
				t.Errorf("wrong script: %q", script)
			}
		default:
			t.Fatal("confirmed action did not run")
		}
		if state.action != nil {
			if state.action.err == nil || !strings.Contains(state.action.err.Error(), "unsupported operation") {
				t.Errorf("error not retained: %+v", state.action)
			}
			click(locales.Cancel())
		}
	}
	if len(a.desktopOf(session).log) != 5 {
		t.Fatalf("log = %+v", a.desktopOf(session).log)
	}
}

func taskDemoProcess() monitor.TaskProcess {
	return monitor.TaskProcess{Process: monitor.Process{PID: 83, PPID: 2, Name: "worker", User: "demo", RSS: 220_000_000, CPU: 12.4, State: 'S', Start: 242, Args: "/opt/service/worker --serve"}, Threads: 6, Nice: 3, Started: time.Date(2026, 11, 25, 11, 42, 9, 0, time.Local)}
}

func TestDesktopTaskReadLifecycle(t *testing.T) {
	a, _, tt, session := desktopTestApp(t)
	window := a.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	started := make(chan struct{}, 10)
	stopped := make(chan struct{}, 10)
	state.run = func(ctx context.Context, _ string) (string, string, error) {
		started <- struct{}{}
		<-ctx.Done()
		stopped <- struct{}{}
		return "", "", ctx.Err()
	}
	wait := func(events <-chan struct{}) {
		t.Helper()
		select {
		case <-events:
		case <-time.After(2 * time.Second):
			t.Fatal("poll lifecycle timed out")
		}
	}
	tt.Frame()
	wait(started)
	window.minimized = true
	tt.Frame()
	wait(stopped)
	if state.poll.run != nil {
		t.Fatal("minimized window still polls")
	}
	a.desktopFocus(window)
	tt.Frame()
	wait(started)
	a.desktopRequestClose(window)
	wait(stopped)
	if len(a.desktopOf(session).windowsOf(appTaskMgr)) != 0 {
		t.Fatal("window did not close")
	}
}

func TestDesktopTaskPropertyEscalation(t *testing.T) {
	const privateExample = "example-masked-value"
	a, p, tt, session := desktopTestApp(t)
	window := a.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	state.page, state.paused, state.processReady = taskProcesses, true, true
	proc := taskDemoProcess()
	state.selected, state.processes = taskProcessIDOf(proc), []monitor.TaskProcess{proc}
	state.run = func(_ context.Context, script string) (string, string, error) {
		if strings.Contains(script, "'envhex'") {
			return "---FINALLYSHELL:envhex:fail---\n---FINALLYSHELL:end---\n", "", nil
		}
		return "---FINALLYSHELL:stat:fail---\n", "", errors.New("exit")
	}
	requests := make(chan string, 4)
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root\n"}, nil
		}
		requests <- request.Action
		payload := "PATH=/usr/bin\x00" + "API_TOKEN=" + privateExample + "\x00"
		return monitor.PrivilegeResult{Started: true, Stdout: taskTestEnvOutput(payload)}, nil
	}
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click(locales.DesktopTaskProperties())
	p.until(t, "restricted basic information", func() bool { return state.details != nil && len(state.details.limited) == 1 })
	click(locales.DesktopTaskEnvironment())
	p.until(t, "restricted environment", func() bool { return state.details.tab == monitor.TaskEnv && len(state.details.limited) == 1 })
	if len(requests) != 0 {
		t.Fatal("opening a tab escalated without user action")
	}
	click(locales.DesktopTaskAsRoot())
	p.until(t, "root environment", func() bool { return len(state.details.result.Env) == 2 })
	if request := <-requests; !strings.Contains(request, `head '-c' '131073' "$path/environ" 2>/dev/null | od '-An' '-v' '-tx1'`) {
		t.Fatalf("unexpected request %q", request)
	}
	if _, visible := tt.Find(privateExample); visible {
		t.Fatal("secret exposed by default")
	}
	click(locales.DesktopTaskReveal())
	if _, visible := tt.Find(privateExample); !visible {
		t.Fatal("reveal did not show secret")
	}
	click(locales.DesktopTaskHide())
	if _, visible := tt.Find(privateExample); visible {
		t.Fatal("hiding did not remove secret")
	}
	click(locales.DesktopTaskReveal())
	window.minimized = true
	tt.Frame()
	a.desktopFocus(window)
	tt.Frame()
	if _, visible := tt.Find(privateExample); visible {
		t.Fatal("restoring window revealed sensitive values")
	}
}

func TestDesktopTaskMemoryActions(t *testing.T) {
	a, p, tt, session := desktopTestApp(t)
	state := a.desktopOpen(session, appTaskMgr, nil).state.(*desktopTaskState)
	desktopTaskDemo(t, state, session)
	state.metric = taskMemory
	commands := make(chan string, 4)
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root\n"}, nil
		}
		commands <- request.Action
		return monitor.PrivilegeResult{Started: true}, nil
	}
	tt.Frame()
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ label, script string }{
		{locales.DesktopTaskRelease(), "sync || exit $?\necho 3 > /proc/sys/vm/drop_caches"},
		{locales.MemoryCompact(), "echo 1 > /proc/sys/vm/compact_memory"},
	} {
		click(test.label)
		if state.action == nil {
			t.Fatalf("no confirmation for %s; UI=%q", test.label, tt.Texts())
		}
		click(locales.Cancel())
		if len(commands) > 0 {
			t.Fatal("cancelled memory operation was sent")
		}
		click(test.label)
		click(locales.DesktopTaskConfirm())
		p.until(t, "memory result", func() bool { return state.action == nil })
		if command := <-commands; !strings.HasSuffix(command, test.script) {
			t.Fatalf("memory script = %q", command)
		}
	}
	if len(a.desktopOf(session).log) != 2 {
		t.Fatal("missing memory action logs")
	}
}

func TestDesktopTaskSortAndTree(t *testing.T) {
	parent, child, independent := taskDemoProcess(), taskDemoProcess(), taskDemoProcess()
	child.PID, child.PPID, child.Name, child.CPU = 91, parent.PID, "child", 19
	independent.PID, independent.PPID, independent.Name, independent.CPU = 123, 1, "other", 5
	rows := taskProcessRows([]monitor.TaskProcess{child, parent, independent}, "", ui.SortOrder{Column: "cpu", Descending: true}, true)
	if len(rows) != 3 || rows[0].proc.PID != 83 || rows[1].proc.PID != 91 || rows[1].depth != 1 {
		t.Fatalf("tree = %+v", rows)
	}
	rows = taskProcessRows([]monitor.TaskProcess{child, parent, independent}, "91", ui.SortOrder{Column: "pid"}, true)
	if len(rows) != 1 || rows[0].proc.PID != 91 || rows[0].depth != 0 {
		t.Fatalf("filter = %+v", rows)
	}
}

func TestDesktopTaskNavigationReadsOnlyVisiblePage(t *testing.T) {
	a, p, tt, session := desktopTestApp(t)
	window := a.desktopOpen(session, appTaskMgr, nil)
	state := window.state.(*desktopTaskState)
	state.run = func(_ context.Context, script string) (string, string, error) {
		switch {
		case strings.Contains(script, "'/proc/diskstats'"):
			return "---FINALLYSHELL:uname---\nLinux\n---FINALLYSHELL:cpu---\ncpu 20 0 5 90\ncpu0 20 0 5 90\n---FINALLYSHELL:load---\n0.25 0.35 0.40 1/23 200\n---FINALLYSHELL:speed---\n2400000\n---FINALLYSHELL:end---\n", "", nil
		case strings.Contains(script, "'who'"):
			return "---FINALLYSHELL:uname---\nLinux\n---FINALLYSHELL:who---\nwindow-user pts/0 2026-11-25 10:50 (192.0.2.5)\n---FINALLYSHELL:end---\n", "", nil
		case strings.Contains(script, "list-units"):
			return `[{"unit":"demo.service","load":"loaded","active":"active","sub":"running","description":"Example background worker"}]`, "", nil
		case strings.Contains(script, "list-unit-files"):
			return "demo.service enabled\n", "", nil
		case strings.Contains(script, "'show'"):
			return "Id=demo.service\nMainPID=83\n", "", nil
		case strings.Contains(script, "'CLK_TCK'"):
			return "---FINALLYSHELL:uname---\nLinux\n---FINALLYSHELL:self---\n500\n---FINALLYSHELL:cpu---\ncpu 20 0 5 90\ncpu0 20 0 5 90\n---FINALLYSHELL:clock---\n100\n---FINALLYSHELL:ps---\n83 2 demo 64 10 /bin/worker\n---FINALLYSHELL:stat---\n83 (worker) S 2 0 0 0 0 0 0 0 0 0 5 3 0 0 20 0 7 0 242\n---FINALLYSHELL:end---\n", "", nil
		default:
			return "", "", errors.New("unexpected read")
		}
	}
	tt.Frame()
	p.until(t, "performance data", func() bool { return state.perfReady })
	if state.perf.GHz != 2.4 || state.perf.Threads != 23 || state.processReady || state.usersReady || state.servicesReady {
		t.Fatalf("wrong startup sample: %+v", state.perf)
	}
	clickButton(t, tt, locales.DesktopTaskUsers())
	p.until(t, "login data", func() bool { return state.usersReady && tt.HasText("window-user") })
	clickButton(t, tt, locales.DesktopTaskServices())
	p.until(t, "service data", func() bool { return state.servicesReady && tt.HasText("demo.service") })
	clickButton(t, tt, locales.DesktopTaskProcesses())
	p.until(t, "process data", func() bool { return state.processReady && tt.HasText("worker") })
	if err := tt.Click(locales.DesktopTaskPause()); err != nil {
		t.Fatal(err)
	}
	if !state.paused || state.poll.run != nil {
		t.Fatal("pausing did not stop process reads")
	}
	a.desktopRequestClose(window)
}

func TestDesktopTaskScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			mode := "light"
			if dark {
				mode = "dark"
			}
			t.Run(language+"/"+mode, func(t *testing.T) {
				a, p, tt, session := desktopTestApp(t)
				p.SetLocale(language)
				tt.SetDark(dark)
				window := a.desktopOpen(session, appTaskMgr, nil)
				state := window.state.(*desktopTaskState)
				desktopTaskDemo(t, state, session)
				for _, size := range []string{"wide", "small"} {
					if size == "small" {
						window.w, window.h = window.app.minSize.W, window.app.minSize.H+desktopTitleHeight
					}
					take := func(view string) {
						tt.Frame()
						tt.Frame()
						saveShot(t, tt, "taskmgr-"+view+"-"+language+"-"+mode+"-"+size+".png")
					}
					state.page, state.details = taskPerformance, nil
					for index, view := range []string{"load", "cpu", "memory", "disk", "network"} {
						state.metric = taskMetric(index)
						take(view)
					}
					state.page = taskProcesses
					take("processes")
					a.desktopTaskAsk(state, taskDemoProcess(), monitor.TaskKill)
					take("kill")
					state.action = nil
					state.details = &desktopTaskDetails{process: taskDemoProcess()}
					for index, view := range []string{"basic", "maps", "files", "environment", "connections"} {
						state.details.tab = monitor.TaskProperty(index)
						state.details.result = taskDemoDetails(t, monitor.TaskProperty(index))
						take(view)
					}
				}
			})
		}
	}
}

func desktopTaskDemo(t *testing.T, state *desktopTaskState, session *Session) {
	t.Helper()
	state.paused, state.processReady = true, true
	state.run = func(context.Context, string) (string, string, error) { return "", "", nil }
	state.processes = []monitor.TaskProcess{taskDemoProcess()}
	state.selected = taskProcessIDOf(state.processes[0])
	for index, name := range []string{"postgres", "caddy", "redis-server", "sshd", "systemd-journald", "systemd", "bash", "cron", "containerd", "dockerd", "dbus-daemon", "journald"} {
		proc := taskDemoProcess()
		proc.PID, proc.PPID, proc.Name, proc.Args = 180+index*3, 83, name, "/usr/bin/"+name
		proc.CPU, proc.RSS = 8.4/float64(index+1), proc.RSS/uint64(index+2)
		if index%2 == 0 {
			proc.User = "root"
		}
		state.processes = append(state.processes, proc)
	}
	mem, err := monitor.ReadMemory(t.Context(), func(context.Context, string) (string, string, error) {
		return "MemTotal: 4194304 kB\nMemFree: 910616 kB\nMemAvailable: 2109784 kB\nBuffers: 176292 kB\nCached: 1001346 kB\nShmem: 105344 kB\nSReclaimable: 355402 kB\nSlab: 561245 kB\nSwapTotal: 2097152 kB\nSwapFree: 1700000 kB\ncompact_memory 1\n", "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session.stats.Memory = mem
	session.stats.MemUsed, session.stats.MemTotal = 2134548480, 4294967296
	session.stats.SwapUsed, session.stats.SwapTotal = 406683648, 2147483648
	session.stats.CPU = 16
	session.stats.Ifaces = []monitor.Iface{
		{Name: "eth0", RxRate: 322134, TxRate: 878105, RxHist: session.stats.RxHist, TxHist: session.stats.TxHist},
		{Name: "lo", RxRate: 41, TxRate: 41, RxHist: session.stats.TxHist, TxHist: session.stats.TxHist},
	}
	read := monitor.TaskPerformance{
		Logical: 4, Processes: 94, Running: 4, Blocked: 1, Threads: 387, GHz: 3.42,
		Load: [3]float64{0.62, 0.45, 0.27},
		Hardware: monitor.TaskHardware{Model: "AMD EPYC 7B13", Sockets: 1, Cores: 2, Threads: 4, BaseGHz: 2.45, Virtualization: "AMD-V / KVM",
			Cache: [4]string{"32K", "32K", "512K", "32M"}, Addresses: map[string][]string{"eth0": {"10.0.0.11/24", "fe80::5001:3cff:fe20:4100/64"}}, LinkMbps: map[string]uint64{"eth0": 1000}},
		Disks: []monitor.TaskDisk{{Name: "vda", Capacity: 256060514304, Busy: 23, Read: 4.5e6, Write: 1.7e6, IOPS: 128, Measured: true},
			{Name: "vda1", Capacity: 255011938304, Busy: 22, Read: 4.3e6, Write: 1.6e6, IOPS: 119, Measured: true, Partition: true, Mounts: []string{"/", "/srv/data"}}},
		CPUs: []monitor.TaskCPU{{Name: "cpu", Time: [10]float64{15, 0, 5, 80, 0, 0, 0, 0, 0, 0}}},
	}
	for index := range 4 {
		read.CPUs = append(read.CPUs, monitor.TaskCPU{Name: "cpu" + fmt.Sprint(index)})
	}
	for index := range monitor.HistoryLen {
		read.Load[0] = (57 + float64((index*index+index*3)%25)) / 100
		read.Load[1] = (43 + float64((index*4+index/4)%8)) / 100
		read.Load[2] = (25 + float64((index+index/9)%6)) / 100
		for core := range read.CPUs {
			read.CPUs[core].Time[3] = 80 - float64((index*(core+5)+index/3)%29)
		}
		read.Disks[0].Read = float64(2500+(index*197)%3240) * 1000
		read.Disks[0].Write = float64(620+(index*317)%1800) * 1000
		session.stats.MemUsed = 2000000000 + uint64((index*910351)%60000000)
		state.sample(read, &session.stats)
		state.push("mem", ratio(session.stats.MemUsed, session.stats.MemTotal)*100)
		state.push("swap", ratio(session.stats.SwapUsed, session.stats.SwapTotal)*100)
		for _, iface := range session.stats.Ifaces {
			state.push("rx/"+iface.Name, iface.RxHist[index])
			state.push("tx/"+iface.Name, iface.TxHist[index])
		}
	}
	session.stats.MemUsed = 2134548480
}

func taskDemoDetails(t *testing.T, tab monitor.TaskProperty) monitor.TaskDetails {
	t.Helper()
	result := monitor.TaskDetails{}
	switch tab {
	case monitor.TaskBasic:
		proc := taskDemoProcess()
		result.Basic = monitor.ProcessDetails{PID: proc.PID, User: proc.User, Name: proc.Name, State: proc.State, Cwd: "/srv/worker", Path: "/opt/service/worker", Args: []string{"/opt/service/worker", "--serve"}}
		result.Status, result.Nice = monitor.TaskStatus{PPID: proc.PPID, Threads: proc.Threads, RSS: proc.RSS}, proc.Nice
	case monitor.TaskMaps:
		result.Maps = []monitor.TaskMap{{Range: "00400000-00408000", Access: "r-xp", Path: "/opt/service/worker", Bytes: 32768},
			{Range: "00608000-00609000", Access: "rw-p", Path: "/opt/service/worker", Bytes: 4096}, {Range: "01990000-019b1000", Access: "rw-p", Path: "[heap]", Bytes: 135168}}
	case monitor.TaskFiles:
		result.Files = []monitor.TaskFD{{Number: 0, Target: "/dev/null"}, {Number: 1, Target: "/var/log/worker.log"}, {Number: 2, Target: "/var/log/worker.log"}, {Number: 3, Target: "socket:[864321]"}}
	case monitor.TaskEnv:
		var err error
		result, err = monitor.ReadTaskDetails(t.Context(), func(context.Context, string) (string, string, error) {
			return taskTestEnvOutput("LANG=C.UTF-8\x00PATH=/usr/local/bin:/usr/bin\x00APP_TOKEN=demo-value\x00HOME=/srv/worker\x00"), "", nil
		}, 83, 242, tab)
		if err != nil {
			t.Fatal(err)
		}
	case monitor.TaskSockets:
		result.Sockets = []monitor.Socket{{Proto: "tcp", Local: netip.MustParseAddrPort("10.0.0.11:8443"), State: "LISTEN"},
			{Proto: "tcp", Local: netip.MustParseAddrPort("10.0.0.11:8443"), Remote: netip.MustParseAddrPort("10.0.0.50:43172"), State: "ESTAB"}}
	}
	return result
}

func taskTestEnvOutput(payload string) string {
	return "---FINALLYSHELL:envhex---\n" + hex.EncodeToString([]byte(payload)) + "\n---FINALLYSHELL:envhex:ok---\n---FINALLYSHELL:end---\n"
}
