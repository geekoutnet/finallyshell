package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// owFake is an OpenWrt 24.10 router for the OpenWrt sections: it answers
// the reads with the samples of a real one, and records what runs as
// root.
type owFake struct {
	t    *testing.T
	root chan string
	sftp string // the SFTP server's path in the probe
}

func (f *owFake) sample(name string) string {
	b, err := os.ReadFile(filepath.Join("..", "monitor", "testdata", "openwrt", "24.10-"+name+".txt"))
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

const owFakeDHCP = "---FINALLYSHELL:now---\n1700000000\n---FINALLYSHELL:leases---\n" +
	"1700003600 aa:bb:cc:00:11:22 192.168.1.120 laptop 01:aa:bb:cc:00:11:22\n0 aa:bb:cc:00:11:44 192.168.1.10 nas *\n" +
	"---FINALLYSHELL:uci---\ndhcp.cfg0afc6b=host\ndhcp.cfg0afc6b.name='nas'\ndhcp.cfg0afc6b.mac='aa:bb:cc:00:11:44'\ndhcp.cfg0afc6b.ip='192.168.1.10'\n" +
	"---FINALLYSHELL:end---\n"

const owFakeFirewall = "firewall.cfg02dc81=zone\nfirewall.cfg02dc81.name='lan'\nfirewall.cfg03dc81=zone\nfirewall.cfg03dc81.name='wan'\n" +
	"firewall.cfg0c92bd=redirect\nfirewall.cfg0c92bd.name='NAS web'\nfirewall.cfg0c92bd.src='wan'\nfirewall.cfg0c92bd.dest='lan'\n" +
	"firewall.cfg0c92bd.proto='tcp'\nfirewall.cfg0c92bd.src_dport='8443'\nfirewall.cfg0c92bd.dest_ip='192.168.1.10'\nfirewall.cfg0c92bd.dest_port='443'\n"

func (f *owFake) run(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
	switch {
	case strings.Contains(script, "openwrt_release"):
		probe := f.sample("probe")
		if f.sftp != "" {
			probe = strings.Replace(probe, "---FINALLYSHELL:sftp---\n", "---FINALLYSHELL:sftp---\n"+f.sftp+"\n", 1)
		}
		return probe, "", nil
	case strings.Contains(script, "network.interface dump"):
		return f.sample("ifaces"), "", nil
	case strings.Contains(script, "dhcp.leases"):
		return owFakeDHCP, "", nil
	case strings.Contains(script, "show firewall"):
		return owFakeFirewall, "", nil
	case strings.Contains(script, "/etc/rc.d"):
		return f.sample("services"), "", nil
	case strings.Contains(script, "list-installed"):
		return f.sample("packages"), "", nil
	case strings.Contains(script, "opkg list '*sftp*'"):
		return "openssh-sftp-server - 9.9_p2-r1 - OpenSSH SFTP server.\n", "", nil
	case strings.Contains(script, "logread"):
		return "Thu Nov 26 01:04:43 2026 daemon.info dnsmasq[1]: started\nThu Nov 26 01:04:50 2026 daemon.warn odhcpd[970]: No default route present\n", "", nil
	}
	return "", "", nil
}

func (f *owFake) privilege(ctx context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
	if request.Probe {
		return monitor.PrivilegeResult{Stdout: "root"}, nil
	}
	f.root <- request.Action
	if strings.Contains(request.Action, "sysupgrade -b") {
		return monitor.PrivilegeResult{Started: true, Stdout: "\x1f\x8bbackup"}, nil
	}
	return monitor.PrivilegeResult{Started: true, Stdout: "done\n"}, nil
}

// owTestApp is a session on the fake router, its System tab open.
func owTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *owFake) {
	t.Helper()
	a, p := testApp(t)
	s := demoSession(a, "router", "192.168.1.1")
	fake := &owFake{t: t, root: make(chan string, 16)}
	s.server.services.run = fake.run
	s.overview.memory.privilegeRunner = fake.privilege
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	a.active = 0
	a.openPanelTab(s, tabSystem)
	tt.Frame()
	p.until(t, "the probe", func() bool { return s.server.openwrt.checked })
	tt.Frame()
	return a, p, tt, s, fake
}

// owSection shows section and waits for what it reads.
func owSection(t *testing.T, p *fakePlatform, tt *ui.Tester, s *Session, section int, loaded func() bool) {
	t.Helper()
	s.server.openwrt.section = section
	tt.Frame()
	p.until(t, owSectionNames()[section], loaded)
	tt.Frame()
}

// owConfirm confirms the dialog open and returns the command it ran.
func owConfirm(t *testing.T, p *fakePlatform, tt *ui.Tester, fake *owFake) string {
	t.Helper()
	systemClick(t, tt, locales.SvcConfirm())
	return systemNext2(t, p, fake.root)
}

func systemNext2(t *testing.T, p *fakePlatform, calls <-chan string) string {
	t.Helper()
	p.until(t, "a root command", func() bool { return len(calls) > 0 })
	return <-calls
}

func TestOpenWrtSections(t *testing.T) {
	a, p, tt, s, fake := owTestApp(t)
	ow := &s.server.openwrt
	if !s.isOpenWrt() || ow.info.Version != "24.10.4" {
		t.Fatalf("not detected: %+v", ow.info)
	}
	// The OpenWrt sections, not the systemd ones.
	wantTexts(t, tt, "row", locales.OwStatus(), locales.OwInterfaces(), locales.OwForwards(), locales.OwPackages())
	if tt.HasText(locales.ServerSecurity()) || tt.HasText(locales.ServerSites()) {
		t.Error("the systemd sections show")
	}
	wantTexts(t, tt, "status", "OpenWrt 24.10.4", "opkg", "fw4 (nftables)", locales.OwSFTPMissing(), locales.OwInstallSFTP())
	saveShot(t, tt, "openwrt-status-en.png")

	owSection(t, p, tt, s, owSecInterfaces, func() bool { return ow.ifaces.loaded })
	wantTexts(t, tt, "interfaces", "lan", "br-lan", "static", locales.OwUp())
	saveShot(t, tt, "openwrt-interfaces-en.png")
	ow.ifaces.selected.Add("lan")
	tt.Frame()
	systemClick(t, tt, locales.SvcActions())
	if err := tt.ChooseMenuItem(locales.OwIfaceRestart()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cmd := owConfirm(t, p, tt, fake); cmd != "ifup 'lan'" {
		t.Errorf("restart: %q", cmd)
	}
	ow.dialog = nil

	owSection(t, p, tt, s, owSecDHCP, func() bool { return ow.dhcp.loaded })
	wantTexts(t, tt, "leases", "laptop", "192.168.1.120", locales.OwNever())
	ow.dhcp.leaseSel.Add("aa:bb:cc:00:11:22")
	tt.Frame()
	systemClick(t, tt, locales.OwMakeStatic())
	if ow.dhcp.form != [3]string{"laptop", "aa:bb:cc:00:11:22", "192.168.1.120"} {
		t.Errorf("form %q", ow.dhcp.form)
	}
	if cmd := owConfirm(t, p, tt, fake); !strings.Contains(cmd, `uci set "dhcp.$s.ip"='192.168.1.120'`) || !strings.Contains(cmd, "/etc/init.d/dnsmasq reload") {
		t.Errorf("static lease: %q", cmd)
	}
	ow.dialog = nil
	ow.dhcp.view = 1
	tt.Frame()
	wantTexts(t, tt, "static leases", "nas", "aa:bb:cc:00:11:44")

	owSection(t, p, tt, s, owSecForwards, func() bool { return ow.forwards.loaded })
	wantTexts(t, tt, "forwards", "NAS web", "8443", "192.168.1.10", "wan → lan")
	saveShot(t, tt, "openwrt-forwards-en.png")
	systemClick(t, tt, locales.OwAddForward())
	// An invalid form stays open, saying so.
	systemClick(t, tt, locales.SvcConfirm())
	wantTexts(t, tt, "invalid form", locales.OwInvalid())
	ow.forwards.form.Name, ow.forwards.form.ExternalPort, ow.forwards.form.InternalIP = "SSH", "2222", "192.168.1.5"
	ow.forwards.form.InternalPort = "22"
	ow.forwards.proto = 1 // TCP
	tt.Frame()
	cmd := owConfirm(t, p, tt, fake)
	for _, want := range []string{`uci add firewall redirect`, `"firewall.$s.src"='wan'`, `"firewall.$s.dest"='lan'`, `"firewall.$s.proto"='tcp'`, `"firewall.$s.dest_port"='22'`} {
		if !strings.Contains(cmd, want) {
			t.Errorf("no %q in %q", want, cmd)
		}
	}
	ow.dialog = nil

	owSection(t, p, tt, s, owSecServices, func() bool { return ow.services.loaded })
	wantTexts(t, tt, "services", "dnsmasq", "dropbear", locales.SvcRunning(), locales.OwOneShot())
	saveShot(t, tt, "openwrt-services-en.png")
	ow.services.selected.Add("dropbear")
	tt.Frame()
	systemClick(t, tt, locales.SvcActions())
	if err := tt.ChooseMenuItem(locales.SvcRestart()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	wantTexts(t, tt, "restart dropbear", locales.OwCutsConnection())
	if cmd := owConfirm(t, p, tt, fake); !strings.Contains(cmd, "'/etc/init.d/dropbear' restart") {
		t.Errorf("restart: %q", cmd)
	}
	ow.dialog = nil

	owSection(t, p, tt, s, owSecPackages, func() bool { return ow.packages.loaded })
	wantTexts(t, tt, "packages", "busybox", locales.OwNoLists())
	systemClick(t, tt, locales.OwInstall())
	ow.packages.term = "sftp"
	systemClick(t, tt, locales.OwSearch())
	p.until(t, "the search", func() bool { return !ow.packages.searching && len(ow.packages.found) > 0 })
	tt.Frame()
	if err := tt.Click("openssh-sftp-server  9.9_p2-r1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cmd := owConfirm(t, p, tt, fake); cmd != "opkg install 'openssh-sftp-server'\n" {
		t.Errorf("install: %q", cmd)
	}
	ow.dialog = nil

	owSection(t, p, tt, s, owSecLogs, func() bool { return ow.logs.loaded })
	if !strings.Contains(ow.logs.shown, "odhcpd") {
		t.Errorf("log %q", ow.logs.shown)
	}
	ow.logs.filter = "dnsmasq"
	tt.Frame()
	if strings.Contains(ow.logs.shown, "odhcpd") || !strings.Contains(ow.logs.shown, "dnsmasq") {
		t.Errorf("filtered %q", ow.logs.shown)
	}

	// Reboot and backup, from the status.
	owSection(t, p, tt, s, owSecStatus, func() bool { return true })
	systemClick(t, tt, locales.OwReboot())
	wantTexts(t, tt, "reboot", locales.OwRebootNote())
	if cmd := owConfirm(t, p, tt, fake); cmd != monitor.OpenWrtRebootCommand {
		t.Errorf("reboot: %q", cmd)
	}
	ow.dialog = nil
	tt.Frame()
	dir := t.TempDir()
	a.settings.DownloadDir = dir
	systemClick(t, tt, locales.OwBackup())
	systemClick(t, tt, locales.SvcConfirm())
	<-fake.root
	p.until(t, "the backup", func() bool { return ow.backup != "" })
	if b, err := os.ReadFile(ow.backup); err != nil || string(b) != "\x1f\x8bbackup" || filepath.Dir(ow.backup) != dir {
		t.Errorf("backup %s: %q %v", ow.backup, b, err)
	}
}

// The Files tab of an OpenWrt router without SFTP says why, and installs
// it; on another server it says what it always did.
func TestOpenWrtSFTPHint(t *testing.T) {
	a, p, tt, s, fake := owTestApp(t)
	s.files.err = &sftpError{io.ErrUnexpectedEOF}
	a.openPanelTab(s, tabFiles)
	a.panelRowOf(s).panelTab = tabFiles
	tt.Frame()
	wantTexts(t, tt, "hint", locales.OwSFTPNote())
	saveShot(t, tt, "openwrt-sftp-en.png")
	systemClick(t, tt, locales.OwInstallSFTP())
	// Installed: the probe after finds the server, and the hint goes.
	fake.sftp = "/usr/libexec/sftp-server"
	if cmd := owConfirm(t, p, tt, fake); cmd != monitor.OpenWrtInstallSFTPCommand {
		t.Errorf("install: %q", cmd)
	}
	p.until(t, "the probe again", func() bool { return s.server.openwrt.info.SFTP != "" })
	tt.Frame()
	if tt.HasText(locales.OwSFTPNote()) {
		t.Error("the hint stays")
	}
}

// A server that is not OpenWrt keeps the systemd sections.
func TestOpenWrtNotDetected(t *testing.T) {
	_, _, tt, s, _ := systemTestApp(t)
	if s.isOpenWrt() || !s.server.openwrt.checked {
		t.Fatal("detected OpenWrt")
	}
	wantTexts(t, tt, "systemd sections", locales.ServerSecurity(), locales.ServerSites())
	if tt.HasText(locales.OwForwards()) {
		t.Error("the OpenWrt sections show")
	}
	if desktopAppByID(appOpenWrt).available(s) {
		t.Error("the OpenWrt app is there")
	}
}
