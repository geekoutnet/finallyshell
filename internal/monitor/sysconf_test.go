package monitor

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDesktopSettingsParsers(t *testing.T) {
	info := ParseSysConfInfo(`---FINALLYSHELL:os-release---
ID="openEuler"
---FINALLYSHELL:virtualization---
kvm
---FINALLYSHELL:zone---
Asia/Shanghai
---FINALLYSHELL:addr---
1: lo inet 127.0.0.1/8 scope host lo
2: eth0@if42 inet 10.1.0.2/24 scope global eth0
2: eth0@if42 inet6 fd00::2/64 scope global
---FINALLYSHELL:mounts---
Filesystem 1024-blocks Used Available Capacity Mounted on
/dev/vda1 400 200 180 53% /data with spaces
---FINALLYSHELL:end---`)
	if info.Distro != "openeuler" || info.Virtualization != "kvm" || info.Zone != "Asia/Shanghai" || len(info.Addrs) != 2 || info.Addrs[0].Iface != "eth0" || len(info.Mounts) != 1 || info.Mounts[0].Device != "/dev/vda1" || info.Mounts[0].Mount != "/data with spaces" || info.Mounts[0].Avail != 180*1024 {
		t.Fatalf("system info: %+v", info)
	}
	resolv := ParseResolvConf("nameserver 1.1.1.1 # primary\nnameserver 2001:4860:4860::8888\nsearch home.example\noptions edns0 trust-ad\n")
	if !slices.Equal(resolv.Servers, []string{"1.1.1.1", "2001:4860:4860::8888"}) || !slices.Equal(resolv.Search, []string{"home.example"}) || !slices.Equal(resolv.Options, []string{"edns0", "trust-ad"}) {
		t.Fatalf("resolv.conf: %+v", resolv)
	}
	links := ParseResolvedStatus("Global\n    DNS Servers: 9.9.9.9\n                 149.112.112.112\n     DNS Domain: example.com ~.\n\nLink 2 (eth0)\n    Current DNS Server: 10.1.0.1\n    DNS Servers: 10.1.0.1\n")
	if len(links) != 2 || len(links[0].Servers) != 2 || links[0].Search[1] != "~." || links[1].Scope != "eth0" || links[1].Current != "10.1.0.1" {
		t.Fatalf("resolvectl: %+v", links)
	}
	nm := ParseSysConfDNS("---FINALLYSHELL:manager---\nMODE=networkmanager\nDEVICE=eth0\nNM=yes\n---FINALLYSHELL:file---\nnameserver 127.0.0.1\n---FINALLYSHELL:nm-device---\nIP4.DNS[1]:10.1.0.1\nIP6.DNS[1]:fd00\\:\\:1\nIP4.DOMAIN[1]:home.example\n---FINALLYSHELL:end---")
	if !nm.NetworkManager || !slices.Equal(nm.Edit.Servers, []string{"10.1.0.1", "fd00::1"}) || !slices.Equal(nm.Edit.Search, []string{"home.example"}) {
		t.Fatalf("NetworkManager: %+v", nm)
	}
	clock := ParseSysConfTime("---FINALLYSHELL:clock---\n1700000000\n+0800\n---FINALLYSHELL:timedatectl---\nTimezone=Asia/Shanghai\nNTP=yes\nNTPSynchronized=no\nCanNTP=yes\nLocalRTC=no\nRTCTimeUSec=Tue 2023-11-14 22:13:20 UTC\n---FINALLYSHELL:sync---\nchronyc\n---FINALLYSHELL:end---")
	if clock.Clock.Unix() != 1700000000 || clock.Zone != "Asia/Shanghai" || !clock.Timedatectl || clock.NTP != "yes" || clock.Synchronized != "no" || !clock.CanNTP || !strings.Contains(clock.RTC, "22:13:20") || clock.Sync != "chronyc" {
		t.Fatalf("clock: %+v", clock)
	}
	_, offset := clock.Clock.Zone()
	if offset != int(8*time.Hour/time.Second) {
		t.Fatalf("offset = %d", offset)
	}
	logins := ParseSysConfLogins("root pts/0 203.0.113.2 Mon Nov 18 09:40:33 2024 - Mon Nov 18 10:11:00 2024 (00:30)\nalice tty1 Mon Nov 18 11:30:00 2024 still logged in\n\nwtmp begins Mon Nov 18 00:00:00 2024\n", false)
	if len(logins) != 2 || logins[0].From != "203.0.113.2" || logins[1].From != "" || logins[1].User != "alice" || logins[0].Failed {
		t.Fatalf("logins: %+v", logins)
	}
}

func TestDesktopSettingsMountedResolver(t *testing.T) {
	dns := ParseSysConfDNS("---FINALLYSHELL:manager---\nMODE=managed\nMOUNT=yes\n---FINALLYSHELL:file---\nnameserver 127.0.0.11\n---FINALLYSHELL:end---\n")
	if dns.Mode != SysConfDNSManaged || !dns.MountPoint || !slices.Equal(dns.File.Servers, []string{"127.0.0.11"}) {
		t.Fatalf("mounted DNS status: %+v", dns)
	}
	if _, err := BuildSysConfDNS(dns, DNSValues{Servers: []string{"1.1.1.1"}}, false); !errors.Is(err, ErrSysConfDNSMode) {
		t.Fatalf("mounted DNS edit: %v", err)
	}
}

func TestDesktopSettingsCommands(t *testing.T) {
	good, err := ValidateSysConfDNS("2001:4860:4860::8888, 1.1.1.1", "home.example\nlab.example")
	if err != nil || len(good.Servers) != 2 || len(good.Search) != 2 {
		t.Fatalf("valid DNS: %+v, %v", good, err)
	}
	for _, invalid := range []string{"127.1", "1.1.1.1;id", "fe80::1%eth0", "1.1.1.1\x00", "'1.1.1.1'"} {
		if _, err := ValidateSysConfDNS(invalid, ""); err == nil {
			t.Fatalf("accepted server %q", invalid)
		}
	}
	for _, invalid := range []string{"-name.test", "name..test", "name.test\rInjected", "~name.test", "$(whoami)"} {
		if _, err := ValidateSysConfDNS("1.1.1.1", invalid); err == nil {
			t.Fatalf("accepted domain %q", invalid)
		}
	}
	for _, mode := range []SysConfDNSMode{SysConfDNSPlain, SysConfDNSResolved, SysConfDNSNetworkManager} {
		dns := SysConfDNS{Mode: mode, UUID: "c6aee012-6d61-437e-a46a-9fbb0e4182e3", Device: "eth0", Backup: true}
		change, err := BuildSysConfDNS(dns, good, false)
		if err != nil || !strings.Contains(change.Script, "1.1.1.1") || change.Target == "" {
			t.Fatalf("%v: %+v, %v", mode, change, err)
		}
		restore, err := BuildSysConfDNS(dns, DNSValues{}, true)
		if err != nil || !strings.Contains(restore.Script, ".bak") || restore.Target == "" {
			t.Fatalf("%v restore: %+v, %v", mode, restore, err)
		}
	}
	if _, err := BuildSysConfDNS(SysConfDNS{Mode: SysConfDNSManaged}, good, false); err == nil {
		t.Fatal("accepted unknown managed resolver")
	}
	if _, err := BuildSysConfDNS(SysConfDNS{Mode: SysConfDNSPlain}, DNSValues{Servers: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9", "1.0.0.1"}}, false); !errors.Is(err, ErrSysConfDNSLimit) {
		t.Fatalf("too many plain nameservers: %v", err)
	}
	if _, err := BuildSysConfDNS(SysConfDNS{Mode: SysConfDNSNetworkManager, UUID: "bogus; id"}, good, false); !errors.Is(err, ErrSysConfDNSMode) {
		t.Fatalf("invalid NetworkManager profile: %v", err)
	}
	resolved, err := BuildSysConfDNS(SysConfDNS{Mode: SysConfDNSResolved, Routing: []string{"~.", "~corp.example", "~$(id)"}}, good, false)
	if err != nil || !strings.Contains(resolved.Target, "~. ~corp.example") || strings.Contains(resolved.Target, "$(id)") {
		t.Fatalf("resolved routing domains: %+v, %v", resolved, err)
	}
	for _, invalid := range []string{"/etc/passwd", "../Etc/UTC", "Etc/../UTC", "Etc/\nUTC", "Etc/UTC\x00", "posix/Etc/UTC", "America/Bad'; id"} {
		if _, err := BuildSysConfZone(invalid, false); err == nil {
			t.Fatalf("accepted zone %q", invalid)
		}
	}
	zone, err := BuildSysConfZone("Etc/GMT+8", true)
	if err != nil || zone.Script != "'timedatectl' 'set-timezone' 'Etc/GMT+8'\n" {
		t.Fatalf("zone: %+v, %v", zone, err)
	}
	if got := BuildSysConfNTP(false).Script; got != "'timedatectl' 'set-ntp' 'false'\n" {
		t.Fatalf("NTP: %q", got)
	}
}

func TestDesktopSettingsShellSyntax(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell not installed")
	}
	scripts := map[string]string{
		"info": SysConfInfoCommand, "DNS": SysConfDNSCommand, "time": SysConfTimeCommand, "list zones": SysConfZonesCommand,
		"logins": SysConfLastCommand, "failed logins": SysConfLastbCommand, "public IP": SysConfPublicIPCommand,
	}
	for _, mode := range []SysConfDNSMode{SysConfDNSPlain, SysConfDNSResolved, SysConfDNSNetworkManager} {
		dns := SysConfDNS{Mode: mode, Backup: true, UUID: "c6aee012-6d61-437e-a46a-9fbb0e4182e3"}
		change, err := BuildSysConfDNS(dns, DNSValues{Servers: []string{"9.9.9.9", "fd00::1"}}, false)
		if err != nil {
			t.Fatal(err)
		}
		restore, err := BuildSysConfDNS(dns, DNSValues{}, true)
		if err != nil {
			t.Fatal(err)
		}
		scripts[string(mode)+" apply"], scripts[string(mode)+" restore"] = change.Script, restore.Script
	}
	fallback, err := BuildSysConfZone("Etc/UTC", false)
	if err != nil {
		t.Fatal(err)
	}
	scripts["fallback zone"] = fallback.Script
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			command := exec.Command(shell, "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("shell syntax: %v: %s", err, output)
			}
		})
	}
}
