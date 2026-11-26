package app

import (
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

// TestDesktopServerInfo shows the server's details on the desktop, folds
// them to its name and back, and keeps the choice.
func TestDesktopServerInfo(t *testing.T) {
	a, _, tt, s := desktopTestApp(t)
	s.overview.read, s.overview.client = true, s.client
	s.overview.info = monitor.SysInfo{
		Hostname: "web-1.example", OS: "Debian GNU/Linux 12 (bookworm)", Kernel: "6.1.0-31-amd64", Arch: "x86_64",
		CPUModel: "Intel(R) Xeon(R) Platinum 8269CY CPU @ 2.50GHz", CPUs: 4, Cores: 2,
		MemTotal: 4 << 30, SwapTotal: 2 << 30, Uptime: 146 * 24 * time.Hour,
		Addrs: []monitor.Addr{{Iface: "lo", Addr: "127.0.0.1/8"}, {Iface: "eth0", Addr: "47.116.62.152/20"}},
		Disks: []monitor.BlockDevice{{Name: "vda", Size: 40 << 30, Model: "Virtio Block Device"}},
	}
	tt.Frame()
	for _, text := range []string{"web-1.example", "6.1.0-31-amd64", "47.116.62.152/20", locales.DesktopInfoCores("2", "4")} {
		if !tt.HasText(text) {
			t.Fatalf("no %q on the desktop: %q", text, tt.Texts())
		}
	}
	if tt.HasText("127.0.0.1/8") {
		t.Error("the loopback address shows")
	}
	saveShot(t, tt, "desktop-server-info.png")
	if err := tt.Click(locales.DesktopInfoHide()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.desktopPreferences().HideServerInfo || tt.HasText("6.1.0-31-amd64") || !tt.HasText("web-1.example") {
		t.Fatal("folding left the details, or took the name away")
	}
	saveShot(t, tt, "desktop-server-info-folded.png")
	if err := tt.Click(locales.DesktopInfoShow()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.desktopPreferences().HideServerInfo || !tt.HasText("6.1.0-31-amd64") {
		t.Fatal("the details did not come back")
	}
}
