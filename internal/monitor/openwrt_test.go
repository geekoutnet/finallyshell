package monitor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The samples in testdata/openwrt are the scripts' output on the official
// rootfs images of each release, and on ImmortalWrt's.
var openWrtReleases = []struct {
	name, distribution, version, pm, firewall string
}{
	{"21.02", "OpenWrt", "21.02.7", "opkg", "fw3"},
	{"22.03", "OpenWrt", "22.03.7", "opkg", "fw4"},
	{"23.05", "OpenWrt", "23.05.6", "opkg", "fw4"},
	{"24.10", "OpenWrt", "24.10.4", "opkg", "fw4"},
	{"25.12", "OpenWrt", "25.12.1", "apk", "fw4"},
	{"immortalwrt-24.10", "ImmortalWrt", "24.10.7", "opkg", "fw4"},
}

func openWrtSample(t *testing.T, release, script string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "openwrt", release+"-"+script+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func TestOpenWrtProbe(t *testing.T) {
	for _, r := range openWrtReleases {
		t.Run(r.name, func(t *testing.T) {
			info, err := ParseOpenWrtProbe(openWrtSample(t, r.name, "probe"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Distribution != r.distribution || info.Version != r.version || info.PackageManager != r.pm || info.Firewall != r.firewall {
				t.Errorf("%+v", info)
			}
			if info.SFTP != "" || info.Kernel == "" || info.Hostname == "" || info.Target != "x86/64" || info.MemTotal == 0 || info.MemAvailable == 0 || info.RootTotal == 0 || info.Uptime == 0 {
				t.Errorf("%+v", info)
			}
		})
	}
	if _, err := ParseOpenWrtProbe(procMark + "release---\n" + procMark + "end---\n"); err != ErrNotOpenWrt {
		t.Errorf("another system: %v", err)
	}
	// An SFTP server, and a release file without DISTRIB_ID.
	info, err := ParseOpenWrtProbe(procMark + "release---\nDISTRIB_DESCRIPTION=\"LEDE Reboot 17.01.7\"\n" + procMark + "sftp---\n/usr/libexec/sftp-server\n" + procMark + "end---\n")
	if err != nil || info.Distribution != "OpenWrt" || info.Description != "LEDE Reboot 17.01.7" || info.SFTP != "/usr/libexec/sftp-server" {
		t.Errorf("%+v, %v", info, err)
	}
}

func TestOpenWrtInterfaces(t *testing.T) {
	for _, r := range openWrtReleases {
		t.Run(r.name, func(t *testing.T) {
			ifaces, err := ParseOpenWrtInterfaces(openWrtSample(t, r.name, "ifaces"))
			if err != nil {
				t.Fatal(err)
			}
			// ImmortalWrt's x86 image has eth0 as its wan, without a lan.
			i := slices.IndexFunc(ifaces, func(it OpenWrtInterface) bool { return it.Up && len(it.IPv4) > 0 })
			if i < 0 || slices.ContainsFunc(ifaces, func(it OpenWrtInterface) bool { return it.Name == "loopback" }) {
				t.Fatalf("%+v", ifaces)
			}
			if up := ifaces[i]; up.Proto == "" || up.Device == "" || up.MAC == "" || up.RX == 0 {
				t.Errorf("%+v", up)
			}
		})
	}
	if _, err := ParseOpenWrtInterfaces(procMark + "dump---\n" + procMark + "end---\n"); err == nil {
		t.Error("no ubus: no error")
	}
}

func TestOpenWrtServices(t *testing.T) {
	for _, r := range openWrtReleases {
		t.Run(r.name, func(t *testing.T) {
			services := ParseOpenWrtServices(openWrtSample(t, r.name, "services"))
			get := func(name string) OpenWrtService {
				i := slices.IndexFunc(services, func(s OpenWrtService) bool { return s.Name == name })
				if i < 0 {
					t.Fatalf("no %s in %+v", name, services)
				}
				return services[i]
			}
			if d := get("dnsmasq"); !d.Enabled || !d.Managed || !d.Running || len(d.PIDs) == 0 {
				t.Errorf("dnsmasq %+v", d)
			}
			if d := get("dropbear"); !d.Enabled || !d.Managed {
				t.Errorf("dropbear %+v", d)
			}
			// A one-shot script procd does not run.
			if b := get("boot"); !b.Enabled || b.Managed || b.Running {
				t.Errorf("boot %+v", b)
			}
		})
	}
}

func TestOpenWrtPackages(t *testing.T) {
	for _, r := range openWrtReleases {
		t.Run(r.name, func(t *testing.T) {
			pm, pkgs, _ := ParseOpenWrtPackages(openWrtSample(t, r.name, "packages"))
			if pm != r.pm || len(pkgs) < 20 {
				t.Fatalf("%s, %d packages", pm, len(pkgs))
			}
			i := slices.IndexFunc(pkgs, func(p OpenWrtPackage) bool { return p.Name == "busybox" })
			if i < 0 || !strings.HasPrefix(pkgs[i].Version, "1.") {
				t.Errorf("busybox in %+v", pkgs)
			}
			if !slices.IsSortedFunc(pkgs, func(a, b OpenWrtPackage) int { return strings.Compare(a.Name, b.Name) }) {
				t.Error("not sorted")
			}
		})
	}
	// opkg's upgrades, apk's list without query, and its upgrades.
	opkg := procMark + "pm---\nopkg\n" + procMark + "installed---\nbusybox - 1.36.1-r2\nlibc - 1.2.5-r4\n" +
		procMark + "upgradable---\nbusybox - 1.36.1-r2 - 1.36.1-r3\n" + procMark + "lists---\n" + procMark + "end---\n"
	pm, pkgs, lists := ParseOpenWrtPackages(opkg)
	if pm != "opkg" || lists || len(pkgs) != 2 || pkgs[0].Upgrade != "1.36.1-r3" || pkgs[1].Upgrade != "" {
		t.Errorf("opkg %s %v %+v", pm, lists, pkgs)
	}
	apk := procMark + "pm---\napk\n" + procMark + "installed---\n" + procMark + "apklist---\n" +
		"apk-mbedtls-3.0.5-r2 x86_64 {feeds/base/system/apk} (GPL-2.0-only) [installed]\n" +
		"cgi-io-2025.12.11~658b14bd-r1 x86_64 {feeds/packages/net/cgi-io} (GPL-2.0-or-later) [installed]\n" +
		procMark + "upgradable---\ncgi-io-2026.01.02~aaaa-r1 x86_64 {feeds/packages/net/cgi-io} (GPL-2.0-or-later) [upgradable from: cgi-io-2025.12.11~658b14bd-r1]\n" +
		procMark + "lists---\nyes\n" + procMark + "end---\n"
	pm, pkgs, lists = ParseOpenWrtPackages(apk)
	want := []OpenWrtPackage{{Name: "apk-mbedtls", Version: "3.0.5-r2"}, {Name: "cgi-io", Version: "2025.12.11~658b14bd-r1", Upgrade: "2026.01.02~aaaa-r1"}}
	if pm != "apk" || !lists || !slices.Equal(pkgs, want) {
		t.Errorf("apk %s %v %+v", pm, lists, pkgs)
	}
}

func TestOpenWrtDHCPAndFirewall(t *testing.T) {
	for _, r := range openWrtReleases {
		t.Run(r.name, func(t *testing.T) {
			_, _, now := ParseOpenWrtDHCP(openWrtSample(t, r.name, "dhcp"))
			if now == 0 {
				t.Error("no clock")
			}
			_, zones := ParseOpenWrtFirewall(openWrtSample(t, r.name, "firewall"))
			if !slices.Contains(zones, "lan") || !slices.Contains(zones, "wan") {
				t.Errorf("zones %v", zones)
			}
		})
	}
	dhcp := procMark + "now---\n1700000000\n" + procMark + "leases---\n" +
		"1700003600 aa:bb:cc:00:11:22 192.168.1.120 laptop 01:aa:bb:cc:00:11:22\n0 AA:BB:CC:00:11:33 192.168.1.121 * *\n" +
		procMark + "uci---\ndhcp.cfg01411c=dnsmasq\ndhcp.cfg0afc6b=host\ndhcp.cfg0afc6b.name='nas'\ndhcp.cfg0afc6b.mac='AA:BB:CC:00:11:44' 'aa:bb:cc:00:11:45'\ndhcp.cfg0afc6b.ip='192.168.1.10'\n" +
		procMark + "end---\n"
	leases, static, now := ParseOpenWrtDHCP(dhcp)
	if now != 1700000000 || len(leases) != 2 || leases[0].Hostname != "laptop" || leases[1].Hostname != "" || leases[1].MAC != "aa:bb:cc:00:11:33" || leases[1].Expires != 0 {
		t.Errorf("leases %+v", leases)
	}
	if len(static) != 1 || static[0].Section != "cfg0afc6b" || static[0].Name != "nas" || !slices.Equal(static[0].MACs, []string{"aa:bb:cc:00:11:44", "aa:bb:cc:00:11:45"}) || static[0].IP != "192.168.1.10" {
		t.Errorf("static %+v", static)
	}
	fw := "firewall.cfg02dc81=zone\nfirewall.cfg02dc81.name='lan'\nfirewall.cfg03dc81=zone\nfirewall.cfg03dc81.name='wan'\n" +
		"firewall.cfg0c92bd=redirect\nfirewall.cfg0c92bd.name='NAS web'\nfirewall.cfg0c92bd.target='DNAT'\nfirewall.cfg0c92bd.src='wan'\nfirewall.cfg0c92bd.dest='lan'\n" +
		"firewall.cfg0c92bd.proto='tcp' 'udp'\nfirewall.cfg0c92bd.src_dport='8443'\nfirewall.cfg0c92bd.dest_ip='192.168.1.10'\nfirewall.cfg0c92bd.dest_port='443'\n" +
		"firewall.cfg0d92bd=redirect\nfirewall.cfg0d92bd.name='old'\nfirewall.cfg0d92bd.src='wan'\nfirewall.cfg0d92bd.src_dport='22'\nfirewall.cfg0d92bd.dest_ip='192.168.1.2'\nfirewall.cfg0d92bd.enabled='0'\n"
	forwards, zones := ParseOpenWrtFirewall(fw)
	want := []OpenWrtForward{
		{Section: "cfg0c92bd", Name: "NAS web", Proto: "tcp udp", SrcZone: "wan", DstZone: "lan", ExternalPort: "8443", InternalIP: "192.168.1.10", InternalPort: "443", Enabled: true, Target: "DNAT"},
		{Section: "cfg0d92bd", Name: "old", Proto: "tcp udp", SrcZone: "wan", ExternalPort: "22", InternalIP: "192.168.1.2", Target: "DNAT"},
	}
	if !slices.Equal(zones, []string{"lan", "wan"}) || !slices.Equal(forwards, want) {
		t.Errorf("%v %+v", zones, forwards)
	}
}

func TestParseUCIValues(t *testing.T) {
	for in, want := range map[string][]string{
		`'a'`:                             {"a"},
		`'tcp' 'udp'`:                     {"tcp", "udp"},
		`'it'\''s'`:                       {"it's"},
		`''`:                              {""},
		`'a b' 'c'`:                       {"a b", "c"},
		`'managed-config' 'other-config'`: {"managed-config", "other-config"},
	} {
		if got := parseUCIValues(in); !slices.Equal(got, want) {
			t.Errorf("%s: %q", in, got)
		}
	}
}

// What a command takes is checked, and quoted: nothing a user types runs.
func TestOpenWrtCommands(t *testing.T) {
	bad := []func() (string, error){
		func() (string, error) { return OpenWrtInterfaceCommand("lan; reboot", true) },
		func() (string, error) { return OpenWrtServiceCommand("../../bin/sh", "start") },
		func() (string, error) { return OpenWrtServiceCommand("dnsmasq", "rm") },
		func() (string, error) { return OpenWrtPackageCommand("opkg", "install", "x;reboot") },
		func() (string, error) { return OpenWrtPackageCommand("opkg", "install") },
		func() (string, error) { return OpenWrtPackageCommand("yum", "update") },
		func() (string, error) { return OpenWrtAddStaticLeaseCommand("nas", "aa:bb:cc:00:11", "192.168.1.10") },
		func() (string, error) { return OpenWrtAddStaticLeaseCommand("nas", "aa:bb:cc:00:11:22", "::1") },
		func() (string, error) {
			return OpenWrtAddStaticLeaseCommand("a b", "aa:bb:cc:00:11:22", "192.168.1.10")
		},
		func() (string, error) { return OpenWrtDeleteSectionCommand("dhcp", "@host[0]", "host", "dnsmasq") },
		func() (string, error) { return OpenWrtEnableForwardCommand("cfg1;x", true) },
		func() (string, error) { return OpenWrtSearchCommand("opkg", "*") },
	}
	for i, f := range bad {
		if script, err := f(); err == nil {
			t.Errorf("%d: %q", i, script)
		}
	}
	for _, f := range []OpenWrtForward{
		{Name: "x", Proto: "tcp", SrcZone: "wan", DstZone: "lan", ExternalPort: "0", InternalIP: "192.168.1.2"},
		{Name: "x", Proto: "tcp", SrcZone: "wan", DstZone: "lan", ExternalPort: "90-80", InternalIP: "192.168.1.2"},
		{Name: "x", Proto: "icmp", SrcZone: "wan", DstZone: "lan", ExternalPort: "80", InternalIP: "192.168.1.2"},
		{Name: "", Proto: "tcp", SrcZone: "wan", DstZone: "lan", ExternalPort: "80", InternalIP: "192.168.1.2"},
		{Name: "x\ny", Proto: "tcp", SrcZone: "wan", DstZone: "lan", ExternalPort: "80", InternalIP: "192.168.1.2"},
		{Name: "x", Proto: "tcp", SrcZone: "wan zone", DstZone: "lan", ExternalPort: "80", InternalIP: "192.168.1.2"},
	} {
		if script, err := OpenWrtAddForwardCommand(f); err == nil {
			t.Errorf("%+v: %q", f, script)
		}
	}

	script, err := OpenWrtAddForwardCommand(OpenWrtForward{Name: "NAS's web", Proto: "tcp udp", SrcZone: "wan", DstZone: "lan", ExternalPort: "8443", InternalIP: "192.168.1.10", InternalPort: "443"})
	for _, want := range []string{`uci set "firewall.$s.name"='NAS'\''s web'`, `uci add_list "firewall.$s.proto"='udp'`, `uci set "firewall.$s.dest_port"='443'`, "uci commit firewall && /etc/init.d/firewall reload"} {
		if err != nil || !strings.Contains(script, want) {
			t.Errorf("%v: no %q in\n%s", err, want, script)
		}
	}
	if script, _ := OpenWrtPackageCommand("apk", "install", "openssh-sftp-server", "luci-app-ttyd"); script != "apk add 'openssh-sftp-server' 'luci-app-ttyd'\n" {
		t.Errorf("%q", script)
	}
	if script, _ := OpenWrtServiceCommand("dnsmasq", "restart"); script != "[ -x '/etc/init.d/dnsmasq' ] || exit 1\n'/etc/init.d/dnsmasq' restart\n" {
		t.Errorf("%q", script)
	}
	if !ValidOpenWrtPorts("8000-8100") || ValidOpenWrtPorts("65536") || !ValidOpenWrtPorts("1") {
		t.Error("ports")
	}
}

func TestApkNameVersion(t *testing.T) {
	for line, want := range map[string][2]string{
		"apk-mbedtls-3.0.5-r2 x86_64 {x} (y) [installed]": {"apk-mbedtls", "3.0.5-r2"},
		"base-files-1696~b21cfa8f8c x86_64":               {"base-files", "1696~b21cfa8f8c"},
		"6in4-28":                                         {"6in4", "28"},
		"kmod-usb-net-rtl8152-6.12.74-r1":                 {"kmod-usb-net-rtl8152", "6.12.74-r1"},
	} {
		name, version, ok := apkNameVersion(line)
		if !ok || name != want[0] || version != want[1] {
			t.Errorf("%s: %s %s", line, name, version)
		}
	}
}
