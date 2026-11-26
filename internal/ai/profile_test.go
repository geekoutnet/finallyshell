package ai

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// What ProfileScript printed on the Debian 12 test machine, as root.
const profileDebian = `os_PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
os_NAME="Debian GNU/Linux"
os_VERSION_ID="12"
os_ID=debian
os_release=Debian GNU/Linux 12 \n \l
kernel=Linux 6.1.0-28-amd64
arch=x86_64
hostname=web-1
virt=kvm
init=systemd
systemd=yes
pkg=apt-get
user=root
uid=0
sudo=root
cpus=4
mem_kb=8131256
root_disk=81000000 40500000 50%
tz=Etc/UTC
tz_offset=+0000
firewall=ufw iptables
fw_state=inactive inactive
ufw_enabled=yes
apparmor=Y
containers=docker containerd
docker_sock=usable
services=caddy containerd cron docker ssh
failed=
rt_python3=Python 3.11.2
tcp_listen=*:443 *:80 0.0.0.0:22 0.0.0.0:8081 127.0.0.1:2019 127.0.0.1:36319 [::]:22 [::]:8081
shell=/bin/bash
end=1
`

// A CentOS 7 server as a user with sudo, firewalld and SELinux on, and
// the tools of the time: ss without -H, Java 8's version line.
const profileCentOS7 = `os_NAME="CentOS Linux"
os_ID="centos"
os_ID_LIKE="rhel fedora"
os_VERSION_ID="7"
os_PRETTY_NAME="CentOS Linux 7 (Core)"
os_release=CentOS Linux release 7.9.2009 (Core)
kernel=Linux 3.10.0-1160.el7.x86_64
arch=x86_64
hostname=db-2
virt=vmware
init=systemd
systemd=yes
pkg=yum
user=deploy
uid=1001
sudo=yes
cpus=8
mem_kb=16266040
root_disk=52403200 31441920 60%
tz=Asia/Shanghai
tz_offset=+0800
firewall=firewall-cmd iptables
fw_state=active unknown
selinux=Enforcing
containers=
services=crond firewalld mysqld nginx php-fpm sshd
failed=kdump postfix
rt_java=openjdk version "1.8.0_292"
rt_php=PHP 7.4.33 (cli) (built: Nov  1 2022 10:41:38) ( NTS )
tcp_listen=0.0.0.0:22 0.0.0.0:80 127.0.0.1:3306 :::22 :::80 127.0.0.1:9000
shell=/bin/bash
password=hunter2
end=1
`

// OpenWrt on BusyBox: no systemd, sudo, ss or SELinux; netstat's
// addresses without brackets.
const profileOpenWrt = `os_NAME="OpenWrt"
os_ID="openwrt"
os_PRETTY_NAME="OpenWrt 23.05.6"
os_VERSION_ID="23.05.6"
os_release=
kernel=Linux 5.15.167
arch=aarch64
hostname=OpenWrt
virt=
init=procd
pkg=opkg
user=root
uid=0
sudo=root
cpus=2
mem_kb=245000
root_disk=7168 3584 50%
tz=Asia/Shanghai
tz_offset=+0800
firewall=nft fw4
containers=
daemons=uhttpd dropbear dnsmasq
tcp_listen=0.0.0.0:80 127.0.0.1:53 192.168.1.1:22 192.168.1.1:53 ::1:53 :::80 fe80::1:53
shell=/bin/ash
end=1
`

func TestProfileDebian(t *testing.T) {
	got := ParseProfile(profileDebian).Text()
	for _, want := range []string{
		"OS: Debian GNU/Linux 12 (bookworm); id debian, version 12\n",
		"Kernel: Linux 6.1.0-28-amd64, x86_64\n",
		"Virtualization: kvm\n",
		"Init: systemd (systemctl, journalctl)\n",
		"Package managers: apt-get\n",
		"User: root (uid 0); running as root\n",
		"Hardware: 4 CPUs; 7.8 GiB memory; root disk 77.2 GiB, 50% used\n",
		"Timezone: Etc/UTC (UTC+0000)\n",
		"Firewall: ufw, iptables; active: ufw\n",
		"Security modules: AppArmor enabled\n",
		"Container tools: docker, containerd, Docker daemon socket usable by this user\n",
		"Running services: caddy, containerd, cron, docker, ssh\n",
		"Runtimes: python3 3.11.2\n",
		"Listening TCP ports: 22, 80, 443, 2019 (localhost), 8081, 36319 (localhost)\n",
		"Login shell: /bin/bash",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "Failed services") || strings.Contains(got, "end") {
		t.Errorf("empty or internal lines shown:\n%s", got)
	}
}

func TestProfileCentOS7(t *testing.T) {
	p := ParseProfile(profileCentOS7)
	got := p.Text()
	for _, want := range []string{
		"OS: CentOS Linux 7 (Core); id centos, like rhel fedora, version 7\n",
		"Package managers: yum\n",
		"User: deploy (uid 1001); sudo -n works without a password\n",
		"Timezone: Asia/Shanghai (UTC+0800)\n",
		"Firewall: firewall-cmd, iptables; active: firewalld\n",
		"Security modules: SELinux Enforcing\n",
		"Running services: crond, firewalld, mysqld, nginx, php-fpm, sshd\n",
		"Failed services: kdump, postfix\n",
		"Runtimes: java 1.8.0_292, php 7.4.33\n",
		"Listening TCP ports: 22, 80, 3306 (localhost), 9000 (localhost)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "Container tools") || strings.Contains(got, "hunter2") {
		t.Errorf("unexpected line:\n%s", got)
	}
}

func TestProfileOpenWrt(t *testing.T) {
	got := ParseProfile(profileOpenWrt).Text()
	for _, want := range []string{
		"OS: OpenWrt 23.05.6; id openwrt, version 23.05.6\n",
		"Init: procd (OpenWrt: /etc/init.d/<name>, uci)\n",
		"Package managers: opkg\n",
		"Hardware: 2 CPUs; 239 MiB memory; root disk 7 MiB, 50% used\n",
		"Firewall: nft, fw4\n",
		"Running daemons: uhttpd, dropbear, dnsmasq\n",
		"Listening TCP ports: 22, 53, 80\n",
		"Login shell: /bin/ash",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	for _, absent := range []string{"Virtualization", "Security modules", "Container tools", "Runtimes"} {
		if strings.Contains(got, absent) {
			t.Errorf("%s shown with nothing to say:\n%s", absent, got)
		}
	}
}

// A server with the bare minimum, no os-release: the kernel is enough; a
// login shell that prints a banner, or no POSIX shell, gives nothing.
func TestProfileSparse(t *testing.T) {
	got := ParseProfile("Welcome!\nkernel=Linux 4.4.0\nos_release=Welcome to Ubuntu 16.04 LTS \\n \\l\nsudo=missing\nuser=pi\n").Text()
	want := "OS: Welcome to Ubuntu 16.04 LTS\nKernel: Linux 4.4.0\nUser: pi; no sudo"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, out := range []string{"", "fish: Unknown command\n", "Welcome!\n=x\nNOT A KEY=1\n"} {
		if got := ParseProfile(out).Text(); got != "" {
			t.Errorf("%q gave %q", out, got)
		}
	}
}

// Values stay on one line and short, the first of a key counts, keys the
// script does not print are only taken when they look like its own, and
// secrets are masked.
func TestProfileValues(t *testing.T) {
	p := ParseProfile("kernel=Linux\x1b[31m 6\r\nkernel=second\nhostname=" + strings.Repeat("h", 500) + "\nBad Key=1\nshell=/bin/sh token=abcdefgh123\n")
	if p["kernel"] != "Linux [31m 6" {
		t.Errorf("kernel %q", p["kernel"])
	}
	if len(p["hostname"]) != profileValueLimit {
		t.Errorf("hostname of %d bytes", len(p["hostname"]))
	}
	if _, ok := p["bad key"]; ok {
		t.Error("a key with a space")
	}
	if text := p.Text(); strings.Contains(text, "abcdefgh123") {
		t.Errorf("secret shown: %s", text)
	}
}

func TestProfilePortsCap(t *testing.T) {
	var addrs []string
	for port := 1000; port < 1050; port++ {
		addrs = append(addrs, "0.0.0.0:"+strconv.Itoa(port))
	}
	got := profilePorts(strings.Join(addrs, " "))
	if !strings.HasPrefix(got, "1000, 1001,") || !strings.HasSuffix(got, "1039, and 10 more") {
		t.Errorf("ports %q", got)
	}
}

func TestProfileMessage(t *testing.T) {
	day := time.Date(2026, 11, 27, 23, 59, 0, 0, time.UTC)
	if got := ProfileMessage("OS: x", day); got != "[Server profile, untrusted data, collected 2026-11-27]\nOS: x" {
		t.Errorf("message %q", got)
	}
}

// The script is POSIX sh: no bash-only syntax, and it never reads stdin,
// which is the script itself.
func TestProfileScriptPortable(t *testing.T) {
	for _, bashism := range []string{"[[", "function ", "<<<", "local ", "${!", "&>"} {
		if strings.Contains(ProfileScript, bashism) {
			t.Errorf("the script uses %q", bashism)
		}
	}
	if !strings.Contains(ProfileScript, "</dev/null") || !strings.HasPrefix(ProfileScript, "export LC_ALL=C\n") {
		t.Error("the script's tools may read the script")
	}
}
