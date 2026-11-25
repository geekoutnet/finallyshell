package monitor

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

func TestUFWEnableGuardRecognizesVendorRules(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	script, err := UFWToggleScript(true, 22, netip.MustParseAddr("192.0.2.1"))
	if err != nil {
		t.Fatal(err)
	}
	guard, _, _ := strings.Cut(script, firewallBackups)
	guard = strings.NewReplacer("/etc/ufw/", "local/", "/usr/share/ufw/", "vendor/").Replace(guard)
	for _, changed := range []bool{false, true} {
		setup := "set -e; mkdir -p local vendor; printf '%s\n' '-A ufw-not-local -j DROP' >vendor/before.rules; cp vendor/before.rules local/before.rules\n"
		if changed {
			setup += "printf '%s\n' '-A ufw-before-input -p tcp --dport 22 -j DROP' >>local/before.rules\n"
		}
		cmd := exec.Command(shell, "-c", setup+guard)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if (err != nil) != changed || changed && !strings.Contains(string(out), "Custom UFW pre-rules") {
			t.Errorf("changed=%v: %v: %s", changed, err, out)
		}
	}
}

func TestOpsFirewallParsers(t *testing.T) {
	state := ParseUFW("Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n     To                         Action      From\n[ 1] 2200/tcp                   ALLOW IN    Anywhere\n[12] 8000:9000/udp (v6)         DENY IN     Anywhere (v6) # test\n")
	if !state.Active || len(state.Rules) != 2 || state.Rules[1].Number != 12 || state.Rules[1].Comment != "test" {
		t.Fatalf("ufw: %+v", state)
	}
	zones := ParseFirewallZones("public (active)\n  interfaces: eth0\n  services: ssh https\n  ports: 2200/tcp 1000-2000/udp\n  rich rules:\n\trule family=\"ipv4\" accept\ntrusted\n  target: ACCEPT\n")
	if len(zones) != 2 || !zones[0].Active || len(zones[0].Ports) != 2 || len(zones[0].Rich) != 1 {
		t.Fatalf("zones: %+v", zones)
	}
}

func TestOpsFirewallGuard(t *testing.T) {
	ports := FirewallSSHPorts(2222, "ESTAB 0 0 [::ffff:192.0.2.2]:2200 [::ffff:192.0.2.3]:39873 users:((\"sshd-session\",pid=233,fd=4))\n")
	if len(ports) != 2 || !FirewallPortProtected("2100:2300/tcp", ports) || FirewallPortProtected("2200/udp", ports) || !FirewallPortProtected("OpenSSH", ports) || !FirewallPortProtected("443/tcp", nil) {
		t.Fatalf("guard: %v", ports)
	}
	cmd, err := UFWAddScript("allow", "443", "tcp", "192.0.2.0/24", "a'b", ports)
	if err != nil || !strings.Contains(cmd, `'ufw' 'allow' 'proto' 'tcp' 'from' '192.0.2.0/24' 'to' 'any' 'port' '443' 'comment' 'a'\''b'`) {
		t.Fatalf("add: %q, %v", cmd, err)
	}
	if _, err := UFWAddScript("deny", "2200", "tcp", "", "", ports); err == nil {
		t.Fatal("denied SSH")
	}
	if _, err := FirewallZoneChangeScript("public", "port", "2100-2300/tcp", false, ports); err == nil {
		t.Fatal("removed firewalld SSH port range")
	}
	if _, err := FirewallZoneChangeScript("public", "service", "ssh", false, ports); err == nil {
		t.Fatal("removed firewalld SSH service")
	}
	change, err := FirewallZoneChangeScript("public", "service", "https", false, ports)
	if err != nil || !strings.Contains(change, `'firewall-cmd' '--info-service=https'`) || !strings.Contains(change, `'firewall-cmd' '--permanent' '--zone=public' '--remove-service=https'`) || strings.Contains(change, "--reload") {
		t.Fatalf("service change: %s, %v", change, err)
	}
	enable, err := UFWToggleScript(true, 2200, netip.MustParseAddr("2001:db8::1"))
	if err != nil || !strings.Contains(enable, "IPV6=yes") || strings.Index(enable, "'prepend'") > strings.Index(enable, "'enable'") {
		t.Fatalf("enable command: %s, %v", enable, err)
	}
}
