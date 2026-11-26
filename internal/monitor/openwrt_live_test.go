package monitor

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// TestOpenWrtLive reads a real OpenWrt system (FINALLYSHELL_TEST_SSH),
// as the OpenWrt test machine (.claude/skills/testbox/openwrt.sh) of any
// release. With FINALLYSHELL_TEST_MUTATE=1, as root on 127.0.0.1, it
// also changes it the way the OpenWrt sections do and checks each
// change, undoing them: a port forward, a static lease, an interface, a
// service, a backup, and installing the SFTP server, which needs the
// box to reach the package feeds.
func TestOpenWrtLive(t *testing.T) {
	client := dialTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	run := func(t *testing.T, script string) string {
		t.Helper()
		out, stderr, err := RunScript(ctx, client, script)
		if err != nil {
			t.Fatalf("%v: %s%s", err, stderr, out)
		}
		return out
	}
	info, err := ParseOpenWrtProbe(run(t, OpenWrtProbeScript))
	if err != nil {
		t.Skip("not an OpenWrt system")
	}
	t.Logf("%s %s, %s, %s", info.Distribution, info.Version, info.PackageManager, info.Firewall)
	if info.PackageManager == "" || info.Firewall == "" || info.Kernel == "" || info.MemTotal == 0 || info.RootTotal == 0 {
		t.Errorf("probe %+v", info)
	}
	ifaces, err := ParseOpenWrtInterfaces(run(t, OpenWrtInterfacesScript))
	if err != nil || !slices.ContainsFunc(ifaces, func(i OpenWrtInterface) bool { return i.Up && len(i.IPv4) > 0 }) {
		t.Errorf("interfaces %+v, %v", ifaces, err)
	}
	services := ParseOpenWrtServices(run(t, OpenWrtServicesScript))
	if !slices.ContainsFunc(services, func(s OpenWrtService) bool { return s.Name == "dnsmasq" && s.Running && s.Enabled }) {
		t.Errorf("services %+v", services)
	}
	pm, pkgs, _ := ParseOpenWrtPackages(run(t, OpenWrtPackagesScript))
	if pm != info.PackageManager || !slices.ContainsFunc(pkgs, func(p OpenWrtPackage) bool { return p.Name == "busybox" }) {
		t.Errorf("packages %s %d", pm, len(pkgs))
	}
	if _, zones := ParseOpenWrtFirewall(run(t, OpenWrtFirewallScript)); !slices.Contains(zones, "lan") {
		t.Errorf("zones %v", zones)
	}
	if log := run(t, OpenWrtLogCommand(OpenWrtLogSystem, 50)); strings.TrimSpace(log) == "" {
		t.Error("empty system log")
	}

	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" || client.User() != "root" || !strings.HasPrefix(client.RemoteAddr().String(), "127.0.0.1:") {
		t.Log("read only: changes need FINALLYSHELL_TEST_MUTATE=1, root, and the local test machine")
		return
	}
	script := func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	t.Run("forward", func(t *testing.T) {
		name := "fslive-" + time.Now().Format("150405")
		run(t, script(OpenWrtAddForwardCommand(OpenWrtForward{Name: name, Proto: "tcp", SrcZone: "wan", DstZone: "lan", ExternalPort: "18443", InternalIP: "192.168.1.250", InternalPort: "443"})))
		find := func() OpenWrtForward {
			forwards, _ := ParseOpenWrtFirewall(run(t, OpenWrtFirewallScript))
			i := slices.IndexFunc(forwards, func(f OpenWrtForward) bool { return f.Name == name })
			if i < 0 {
				return OpenWrtForward{}
			}
			return forwards[i]
		}
		f := find()
		t.Cleanup(func() {
			if f.Section != "" {
				RunScript(context.Background(), client, "uci -q delete firewall."+f.Section+"; uci commit firewall; /etc/init.d/firewall reload")
			}
		})
		if f.Section == "" || f.Proto != "tcp" || f.ExternalPort != "18443" || f.InternalPort != "443" || !f.Enabled {
			t.Fatalf("added %+v", f)
		}
		// The firewall took it: fw4's nft ruleset, or the rules fw3 makes
		// of it (it loads none for a zone without a device, as the test
		// machine's wan).
		rules := run(t, "{ nft list ruleset; iptables-save -t nat; fw3 -4 print; } 2>/dev/null; true\n")
		if !strings.Contains(rules, "18443") {
			t.Error("the port forward is not in the firewall's rules")
		}
		run(t, script(OpenWrtEnableForwardCommand(f.Section, false)))
		if find().Enabled {
			t.Error("still enabled")
		}
		run(t, script(OpenWrtEnableForwardCommand(f.Section, true)))
		if !find().Enabled {
			t.Error("not enabled again")
		}
		run(t, script(OpenWrtDeleteSectionCommand("firewall", f.Section, "redirect", "firewall")))
		if find().Section != "" {
			t.Error("not deleted")
		}
		// A section of another type is not deleted as a redirect.
		var zone string
		for _, s := range parseUCI(run(t, OpenWrtFirewallScript)) {
			if s.Type == "zone" {
				zone = s.Name
				break
			}
		}
		if _, _, err := RunScript(ctx, client, script(OpenWrtDeleteSectionCommand("firewall", zone, "redirect", "firewall"))); err == nil {
			t.Errorf("deleted the zone %s as a redirect", zone)
		}
	})

	t.Run("static lease", func(t *testing.T) {
		run(t, script(OpenWrtAddStaticLeaseCommand("fslive-nas", "02:00:00:fe:fe:01", "192.168.1.251")))
		_, static, _ := ParseOpenWrtDHCP(run(t, OpenWrtDHCPScript))
		i := slices.IndexFunc(static, func(s OpenWrtStaticLease) bool { return s.Name == "fslive-nas" })
		if i < 0 || static[i].IP != "192.168.1.251" || !slices.Equal(static[i].MACs, []string{"02:00:00:fe:fe:01"}) {
			t.Fatalf("static %+v", static)
		}
		section := static[i].Section
		t.Cleanup(func() {
			RunScript(context.Background(), client, "uci -q delete dhcp."+section+"; uci commit dhcp; /etc/init.d/dnsmasq reload")
		})
		run(t, script(OpenWrtDeleteSectionCommand("dhcp", section, "host", "dnsmasq")))
		if _, static, _ = ParseOpenWrtDHCP(run(t, OpenWrtDHCPScript)); slices.ContainsFunc(static, func(s OpenWrtStaticLease) bool { return s.Section == section }) {
			t.Error("not deleted")
		}
	})

	t.Run("interface", func(t *testing.T) {
		// One of its own: lan carries the connection.
		run(t, "uci set network.fslive=interface && uci set network.fslive.proto=static && uci set network.fslive.device=lo && uci set network.fslive.ipaddr=127.0.0.42/32 && uci commit network && ubus call network reload")
		t.Cleanup(func() {
			RunScript(context.Background(), client, "ifdown fslive; uci -q delete network.fslive; uci commit network; ubus call network reload")
		})
		state := func() OpenWrtInterface {
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
				ifaces, _ := ParseOpenWrtInterfaces(run(t, OpenWrtInterfacesScript))
				if i := slices.IndexFunc(ifaces, func(it OpenWrtInterface) bool { return it.Name == "fslive" }); i >= 0 && !ifaces[i].Pending {
					return ifaces[i]
				}
			}
			return OpenWrtInterface{}
		}
		run(t, script(OpenWrtInterfaceCommand("fslive", true)))
		if it := state(); !it.Up || !slices.Contains(it.IPv4, "127.0.0.42/32") {
			t.Errorf("up: %+v", it)
		}
		run(t, script(OpenWrtInterfaceCommand("fslive", false)))
		for deadline := time.Now().Add(10 * time.Second); state().Up && time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		}
		if state().Up {
			t.Error("still up")
		}
	})

	t.Run("service", func(t *testing.T) {
		before := slices.IndexFunc(services, func(s OpenWrtService) bool { return s.Name == "dnsmasq" })
		run(t, script(OpenWrtServiceCommand("dnsmasq", "restart")))
		for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(500 * time.Millisecond) {
			now := ParseOpenWrtServices(run(t, OpenWrtServicesScript))
			i := slices.IndexFunc(now, func(s OpenWrtService) bool { return s.Name == "dnsmasq" })
			if i >= 0 && now[i].Running && !slices.Equal(now[i].PIDs, services[before].PIDs) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("dnsmasq not restarted: %+v", now[i])
			}
		}
	})

	t.Run("backup", func(t *testing.T) {
		out := run(t, OpenWrtBackupCommand)
		if len(out) < 100 || out[0] != 0x1f || out[1] != 0x8b {
			t.Errorf("not a gzip: %d bytes", len(out))
		}
	})

	t.Run("sftp", func(t *testing.T) {
		if _, _, err := RunScript(ctx, client, "wget -q -T 5 -O /dev/null http://downloads.openwrt.org/ 2>/dev/null || uclient-fetch -q -T 5 -O /dev/null https://downloads.openwrt.org/"); err != nil {
			t.Skip("the feeds cannot be reached")
		}
		if info.SFTP == "" {
			out, stderr, err := RunScript(ctx, client, OpenWrtInstallSFTPCommand)
			if err != nil {
				t.Fatalf("install: %v\n%s%s", err, out, stderr)
			}
		}
		after, _ := ParseOpenWrtProbe(run(t, OpenWrtProbeScript))
		if after.SFTP == "" {
			t.Fatal("no SFTP server after the install")
		}
		sc, err := sftp.NewClient(client)
		if err != nil {
			t.Fatal(err)
		}
		defer sc.Close()
		if _, err := sc.Stat("/etc/openwrt_release"); err != nil {
			t.Error(err)
		}
	})
}
