package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDesktopTaskCPU(t *testing.T) {
	before := parseTaskCPU("cpu 100 20 20 100 10 5 5 10 10 2\ncpu0 100 20 20 100 10 5 5 10 10 2\n")
	after := parseTaskCPU("cpu 120 26 30 150 14 7 7 16 14 4\ncpu0 120 26 30 150 14 7 7 16 14 4\n")
	usage, ok := taskCPUPercent(before["cpu"], after["cpu"])
	if !ok || usage != ([10]float64{16, 4, 10, 50, 4, 2, 2, 6, 4, 2}) {
		t.Fatalf("split = %v, valid = %v", usage, ok)
	}
	if _, ok := taskCPUPercent(after["cpu0"], before["cpu0"]); ok {
		t.Fatal("counter reset produced a sample")
	}
}

func TestDesktopTaskDisk(t *testing.T) {
	before := parseTaskDisk("252  1 vda1 1 0 10 0 2 0 50 0 0 7 0\n252 2 broken 1 0 bad 0 2 0 50 0 0 7 0")
	after := parseTaskDisk("252  1 vda1 5 0 30 0 8 0 80 0 0 1007 0 9 0 8 7")
	read, write, iops, busy, ok := taskDiskRates(before["vda1"], after["vda1"], 2)
	if len(before) != 1 || !ok || read != 5120 || write != 7680 || iops != 5 || busy != 50 {
		t.Fatalf("disk rates = %v/%v, %v IOPS, %v%%, valid=%v", read, write, iops, busy, ok)
	}
	if _, _, _, _, ok := taskDiskRates(after["vda1"], before["vda1"], 2); ok {
		t.Fatal("counter reset produced a sample")
	}
}

func TestDesktopTaskHardware(t *testing.T) {
	info := parseTaskHardware(map[string]string{
		"cpuinfo": "model name : example\ncpu MHz : 2200.0\n",
		"lscpu":   "Model name: Test CPU\nSocket(s): 2\nCore(s) per socket: 4\nCPU(s): 16\nVirtualization: AMD-V\n",
		"cache":   "1 Data 32K\n1 Instruction 64K\n2 Unified 512K\n3 Unified 16M\n42 Unified 0K",
		"block":   "vda1 1024\n",
		"mount":   `30 1 252:1 / /test\040disk rw - ext4 /dev/vda1 rw`,
		"addr":    "2: eth0    inet 10.0.0.2/24 brd 10.0.0.255 scope global eth0\n2: eth0@if3    inet6 fe80::ff/64 scope link\n",
		"link":    "eth0 1000\nlo -1\n",
		"base":    "2400000",
	})
	if info.Model != "Test CPU" || info.GHz != 2.2 || info.BaseGHz != 2.4 || info.Sockets != 2 || info.Cores != 8 || info.Threads != 16 || info.Virtualization != "AMD-V" {
		t.Fatalf("CPU hardware = %+v", info)
	}
	if info.Cache != [4]string{"32K", "64K", "512K", "16M"} || info.Capacity["vda1"] != 524288 || info.Mounts["252:1"][0] != "/test disk" || len(info.Addresses["eth0"]) != 2 || info.LinkMbps["eth0"] != 1000 {
		t.Fatalf("extra hardware = %+v", info)
	}
	fallback := parseTaskHardware(map[string]string{"cpuinfo": "physical id : 0\ncore id : 0\nphysical id : 0\ncore id : 0\nphysical id : 2\ncore id : 0\nflags : fpu vmx"})
	if fallback.Sockets != 2 || fallback.Cores != 2 || fallback.Virtualization != "VT-x" {
		t.Fatalf("cpuinfo fallback = %+v", fallback)
	}
}

func TestDesktopTaskProperties(t *testing.T) {
	status := parseTaskStatus("Name:\tapp\nState:\tS (sleeping)\nPPid:\t2\nThreads:\t4\nVmRSS:\t91 kB\n")
	if status.PPID != 2 || status.Threads != 4 || status.RSS != 91*1024 {
		t.Fatalf("status = %+v", status)
	}
	maps := parseTaskMaps("00400000-00401000 r-xp 00000000 08:01 22 /a path (deleted)\ninvalid\n")
	if len(maps) != 1 || maps[0].Bytes != 4096 || maps[0].Path != "/a path (deleted)" {
		t.Fatalf("maps = %+v", maps)
	}
	env := parseTaskEnvironment("a=1\x00api_Key=secret=rest\x00PASS=x\x00NO_VALUE\x00auth_token=z\x00SECRET=q\x00PARTIAL=z")
	if len(env) != 5 || env[0].Display(false) != "1" || env[1].Display(false) != "••••••" || env[1].Display(true) != "secret=rest" || !env[2].Sensitive || !env[3].Sensitive || !env[4].Sensitive {
		t.Fatalf("environment = %+v", env)
	}
}

func TestDesktopTaskAction(t *testing.T) {
	for _, test := range []struct {
		kind TaskAction
		nice int
		want string
	}{
		{TaskTerminate, 0, "kill '-s' 'TERM' '83'"},
		{TaskKill, 0, "kill '-s' 'KILL' '83'"},
		{TaskStop, 0, "kill '-s' 'STOP' '83'"},
		{TaskContinue, 0, "kill '-s' 'CONT' '83'"},
		{TaskRenice, -6, "renice '-n' '-6' '-p' '83'"},
	} {
		script, err := taskActionScript(83, 242, test.kind, test.nice)
		if err != nil || !strings.Contains(script, "cat '/proc/83/stat'") || !strings.Contains(script, `"${20}" = '242'`) || !strings.HasSuffix(script, test.want+"\n") {
			t.Fatalf("action script = %q, %v", script, err)
		}
	}
	if _, err := taskActionScript(1, 0, TaskKill, 0); err == nil {
		t.Fatal("allowed an unchecked mutation")
	}
	if _, err := taskActionScript(83, 242, TaskRenice, -21); err == nil {
		t.Fatal("allowed an invalid priority")
	}
	denied := errors.New("exit")
	var normal, elevated int
	root := func(_ context.Context, script string) (string, string, error) {
		elevated++
		if !strings.HasSuffix(script, "kill '-s' 'TERM' '83'\n") {
			t.Fatalf("unexpected escalated script %q", script)
		}
		return "", "", nil
	}
	run := func(_ context.Context, _ string) (string, string, error) {
		normal++
		return "", "kill: Operation not permitted", denied
	}
	if err := RunTaskAction(t.Context(), run, root, 83, 242, TaskTerminate, 0); err != nil || normal != 1 || elevated != 1 {
		t.Fatalf("action = %v, runs = %d/%d", err, normal, elevated)
	}
	if err := RunTaskAction(t.Context(), func(context.Context, string) (string, string, error) {
		return signalGone, "", denied
	}, root, 83, 242, TaskTerminate, 0); !errors.Is(err, ErrNoProcess) || elevated != 1 {
		t.Fatalf("stale PID = %v, escalations = %d", err, elevated)
	}
}

func TestDesktopTaskProcessList(t *testing.T) {
	const stat = "83 (worker ) one) S 2 0 0 0 0 0 0 0 0 0 5 3 0 0 20 -4 7 0 200"
	out := taskTestSection("uname", "Linux") + taskTestSection("self", "899") + taskTestSection("clock", "100") +
		taskTestSection("cpu", "cpu 20 0 5 90\ncpu0 20 0 5 90\nbtime 1700000000") +
		taskTestSection("ps", "83 2 demo 64 10 /bin/worker --once 中文.log") + taskTestSection("stat", stat) + taskTestSection("end", "")
	var lister TaskProcessLister
	rows, err := lister.List(t.Context(), func(_ context.Context, script string) (string, string, error) {
		if !strings.Contains(script, "LC_ALL='C.UTF-8' ps '-e' '-ww'") {
			t.Error("procps must preserve UTF-8 and full command lines")
		}
		return out, "", nil
	})
	if err != nil || len(rows) != 1 || rows[0].Name != "worker ) one" || rows[0].Args != "/bin/worker --once 中文.log" || rows[0].PPID != 2 || rows[0].Threads != 7 || rows[0].Nice != -4 || !rows[0].Started.Equal(time.Unix(1700000002, 0)) {
		t.Fatalf("list = %+v, %v", rows, err)
	}
}

func TestDesktopTaskSocketsFollowPIDFileDescriptors(t *testing.T) {
	const rows = "0: 0100007F:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 13568\n" +
		"1: 0100007F:22B8 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 13569\n"
	out := taskTestSection("fd", "2\t/var/log/line\nbreak\x004\tsocket:[13568]\x00") + taskTestSection("fd:ok", "") +
		taskTestSection("tcp", rows) + taskTestSection("tcp:ok", "") + taskTestSection("partial-fd", "") + taskTestSection("end", "")
	result, err := ReadTaskDetails(t.Context(), func(context.Context, string) (string, string, error) { return out, "", nil }, 83, 242, TaskSockets)
	if err != nil || len(result.Sockets) != 1 || result.Sockets[0].Local.Port() != 8443 || len(result.Missing) != 1 || len(result.Files) != 2 || result.Files[0].Target != "/var/log/line\nbreak" {
		t.Fatalf("socket details = %+v, %v", result, err)
	}
}

func TestDesktopTaskLogins(t *testing.T) {
	logins := parseTaskLogins("alice pts/3 2026-11-25 11:42 (192.0.2.13)\nbob tty1 Nov 25 10:04\ninvalid\n")
	if len(logins) != 2 || logins[0] != (TaskLogin{"alice", "pts/3", "2026-11-25 11:42", "192.0.2.13"}) || logins[1].Started != "Nov 25 10:04" {
		t.Fatalf("logins = %+v", logins)
	}
}

func taskTestSection(name, body string) string { return procMark + name + "---\n" + body + "\n" }
