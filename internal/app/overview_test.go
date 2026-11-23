package app

import (
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestVirtualIface(t *testing.T) {
	for _, tc := range []struct {
		name    string
		virtual bool
	}{
		{"lo", true},
		{"lo:1", true},
		{"docker0", true},
		{"docker_gwbridge", true},
		{"br-3f2a1b9c0d4e", true},
		{"veth1a2b3c@if12", true},
		{"virbr0", true},
		{"cni0", true},
		{"flannel.1", true},
		{"cali8d2e1f", true},
		{"vxlan.calico", true},
		{"tun0", true},
		{"tap0", true},
		{"eth0", false},
		{"ens3", false},
		{"enp0s31f6", false},
		{"wlan0", false},
		{"bond0", false},
		{"br0", false}, // a bridge made by hand: the server's own network
		{"wg0", false},
		{"local", false},
	} {
		if got := virtualIface(tc.name); got != tc.virtual {
			t.Errorf("virtualIface(%q) = %v, want %v", tc.name, got, tc.virtual)
		}
	}

	real, virtual := splitAddrs(demoSysInfo().Addrs)
	if len(real) != 2 || real[0].Iface != "eth0" || len(virtual) != 4 {
		t.Errorf("split into %v and %v", real, virtual)
	}
	// Only virtual ones: all show.
	only := []monitor.Addr{{Iface: "docker0", Addr: "172.17.0.1/16"}}
	if real, virtual := splitAddrs(only); len(real) != 1 || virtual != nil {
		t.Errorf("only virtual: %v and %v", real, virtual)
	}
}

// demoSysInfo is a Docker host's system info.
func demoSysInfo() monitor.SysInfo {
	return monitor.SysInfo{
		Hostname: "web-1", OS: "Ubuntu 22.04.4 LTS", KernelOS: "Linux", Kernel: "5.15.0-105-generic", Arch: "x86_64",
		CPUModel: "Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz", CPUs: 4, Cores: 2,
		MemTotal: 4_080_000_000, SwapTotal: 2_360_000_000, Uptime: 410*24*time.Hour + 17*time.Minute,
		Addrs: []monitor.Addr{
			{Iface: "lo", Addr: "127.0.0.1/8"},
			{Iface: "eth0", Addr: "45.56.78.90/24"},
			{Iface: "eth0", Addr: "2600:3c01::f03c:93ff:fe4a:1b2c/64"},
			{Iface: "docker0", Addr: "172.17.0.1/16"},
			{Iface: "br-3f2a1b9c0d4e", Addr: "172.18.0.1/16"},
			{Iface: "br-9c8d7e6f5a4b", Addr: "172.19.0.1/16"},
		},
	}
}

// overviewApp is splitApp with the session's disks filled in and its
// system info read by the test.
func overviewApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *fakeSysInfo) {
	a, p, tt := splitApp(t)
	f := newFakeSysInfo(a)
	s := a.sessions[0]
	s.stats.Load = [3]float64{3.2, 2.4, 1.9}
	s.stats.CPU = 23
	s.stats.Disks = []monitor.Disk{
		{Mount: "/", Avail: 2_000_000_000, Size: 25_000_000_000},
		{Mount: "/boot", Avail: 820_000_000, Size: 1_000_000_000},
		{Mount: "/data", Avail: 210_000_000_000, Size: 500_000_000_000},
	}
	s.stats.Procs = append(s.stats.Procs, monitor.Proc{RSS: 96_000_000, CPU: 0.7, Name: "dockerd"}, monitor.Proc{RSS: 41_000_000, CPU: 0.1, Name: "containerd"})
	return a, p, tt, f
}

func TestOverviewTab(t *testing.T) {
	a, p, tt, f := overviewApp(t)
	a.openBottomTab(tabOverview)
	tt.Frame()

	// Shown the first time: it reads the system info.
	r := f.next(t)
	if !a.sessions[0].overview.loading {
		t.Fatal("not reading")
	}
	r.reply <- sysInfoReply{info: demoSysInfo()}
	p.until(t, "the system info", func() bool { return !a.sessions[0].overview.loading })
	tt.Frame() // the columns follow the width of the last frame
	for _, want := range []string{
		"web-1", "Ubuntu 22.04.4 LTS  ·  5.15.0-105-generic  ·  x86_64", "410 days, 0:00", "254ms",
		locales.OverviewLoad(), locales.Cpu(), locales.SysMemory(), locales.OverviewDisk(),
		"80%", "23%", "71%", "92%", "3.20 / 2.40 / 1.90", "2 cores, 4 threads",
		"/boot", "/data", locales.OverviewDiskUsage(formatBytes(23e9), formatBytes(2e9), formatBytes(25e9)),
		"45.56.78.90/24", locales.OverviewShowAll(6), "java", "dockerd",
	} {
		if !tt.HasText(want) {
			t.Errorf("missing %q; texts %q", want, tt.Texts())
		}
	}
	for _, hidden := range []string{"172.18.0.1/16", "127.0.0.1/8"} {
		if tt.HasText(hidden) {
			t.Errorf("%q shows before Show all", hidden)
		}
	}
	if f := a.sessions[0].overview.info; f.Hostname != "web-1" {
		t.Errorf("info %+v", f)
	}
	saveShot(t, tt, "overview-short-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-short-dark-en.png")
	tt.SetDark(false)

	// As a panel row: the whole main area.
	a.openPanelTab(a.sessions[0], tabOverview)
	tt.Frame()
	tt.Frame()
	if err := tt.Click(locales.OverviewShowAll(6)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"172.18.0.1/16", "br-9c8d7e6f5a4b", "127.0.0.1/8", locales.OverviewShowLess()} {
		if !tt.HasText(want) {
			t.Errorf("Show all: missing %q", want)
		}
	}
	saveShot(t, tt, "overview-panel-en.png")
	tt.SetDark(true)
	tt.Frame()
	saveShot(t, tt, "overview-panel-dark-en.png")
	tt.SetDark(false)
	if err := tt.Click(locales.OverviewShowLess()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("172.18.0.1/16") {
		t.Error("Show less left the virtual addresses")
	}

	// A click on an address copies it, without its prefix; Copy all
	// copies every one.
	if err := tt.Click("45.56.78.90/24"); err != nil {
		t.Fatal(err)
	}
	if got := tt.Clipboard(); got != "45.56.78.90" {
		t.Errorf("clipboard %q", got)
	}
	clickButton(t, tt, locales.CopyAll())
	if got := tt.Clipboard(); !strings.Contains(got, "eth0  45.56.78.90/24\n") || !strings.Contains(got, "br-3f2a1b9c0d4e  172.18.0.1/16\n") {
		t.Errorf("copy all: %q", got)
	}

	// Refresh reads again.
	clickButton(t, tt, locales.Refresh())
	r = f.next(t)
	info := demoSysInfo()
	info.Hostname = "web-1b"
	r.reply <- sysInfoReply{info: info}
	p.until(t, "the second read", func() bool { return tt.HasText("web-1b") })

	// In Chinese.
	a.setLanguage("zh-Hans")
	tt.Frame()
	for _, want := range []string{"负载", "内存", "磁盘", "IP 地址", "内存占用最多的进程", "显示全部（6）"} {
		if !tt.HasText(want) {
			t.Errorf("Chinese: missing %q; texts %q", want, tt.Texts())
		}
	}
	saveShot(t, tt, "overview-panel-zh.png")
}

// The tab in the right column, the narrowest it shows in.
func TestOverviewNarrow(t *testing.T) {
	a, p, tt, f := overviewApp(t)
	a.setPanelOnRight(true)
	a.openBottomTab(tabOverview)
	tt.Frame()
	f.next(t).reply <- sysInfoReply{info: demoSysInfo()}
	p.until(t, "the system info", func() bool { return !a.sessions[0].overview.loading })
	tt.Frame()
	for _, want := range []string{"web-1", locales.OverviewDisk(), "/data"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q", want)
		}
	}
	saveShot(t, tt, "overview-right-en.png")
}

func TestOverviewStates(t *testing.T) {
	a, p, tt, f := overviewApp(t)
	s := a.sessions[0]
	a.openBottomTab(tabOverview)

	// Reading, before the monitor's first stats.
	s.stats.OK = false
	tt.Frame()
	if !tt.HasText(locales.ReadingServerInfo()) {
		t.Errorf("reading: texts %q", tt.Texts())
	}
	saveShot(t, tt, "overview-reading-en.png")

	// Failed, then Retry.
	f.next(t).reply <- sysInfoReply{err: monitor.ErrSysInfoOutput}
	p.until(t, "the error", func() bool { return !s.overview.loading })
	if want := locales.SysInfoFailed(locales.SysInfoNoShell()); !tt.HasText(want) {
		t.Errorf("failed: texts %q", tt.Texts())
	}
	saveShot(t, tt, "overview-failed-en.png")
	clickButton(t, tt, locales.Retry())

	// Not Linux.
	info := demoSysInfo()
	info.KernelOS = "FreeBSD"
	f.next(t).reply <- sysInfoReply{info: info}
	p.until(t, "the retry", func() bool { return !s.overview.loading })
	if !tt.HasText(locales.ProcOnlyLinux()) {
		t.Errorf("not Linux: texts %q", tt.Texts())
	}

	// The monitor turned off for the host.
	s.Host.NoMonitor = true
	tt.Frame()
	if !tt.HasText(locales.SysInfoOff()) {
		t.Errorf("monitor off: texts %q", tt.Texts())
	}
	s.Host.NoMonitor = false

	// Not connected.
	s.status = statusClosed
	tt.Frame()
	if !tt.HasText(s.statusText()) || !tt.HasText(locales.Reconnect()) {
		t.Errorf("closed: texts %q", tt.Texts())
	}
	saveShot(t, tt, "overview-closed-en.png")
}

// The popover's addresses of virtual interfaces wait behind Show all,
// and its Overview button opens the tab.
func TestSysInfoPopoverAddrs(t *testing.T) {
	a, p, tt, f := overviewApp(t)
	clickButton(t, tt, locales.SystemInfo())
	f.next(t).reply <- sysInfoReply{info: demoSysInfo()}
	p.until(t, "the system info", func() bool { return !a.sysInfo.loading })
	if !tt.HasText("eth0  45.56.78.90/24") || tt.HasText("br-3f2a1b9c0d4e  172.18.0.1/16") {
		t.Errorf("collapsed: texts %q", tt.Texts())
	}
	saveShot(t, tt, "popover-addrs-en.png")
	if err := tt.Click(locales.OverviewShowAll(6)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("br-3f2a1b9c0d4e  172.18.0.1/16") {
		t.Errorf("Show all: texts %q", tt.Texts())
	}
	clickButton(t, tt, locales.Copy())
	if got := tt.Clipboard(); !strings.Contains(got, "  eth0  45.56.78.90/24\n") || !strings.Contains(got, "  br-9c8d7e6f5a4b  172.19.0.1/16\n") {
		t.Errorf("summary: %q", got)
	}

	clickButton(t, tt, locales.TabOverview())
	if a.sysInfo.open || a.bottomTab != tabOverview {
		t.Errorf("open %v, tab %d", a.sysInfo.open, a.bottomTab)
	}
}
