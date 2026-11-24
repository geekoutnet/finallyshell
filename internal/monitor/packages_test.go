package monitor

import (
	"testing"
)

func TestOpsPackageParsers(t *testing.T) {
	apt := ParsePackageUpdates("apt", "Listing...\nopenssl/bookworm-security 3.0.15 amd64 [upgradable from: 3.0.14]\nbash/bookworm 5.2 amd64 [upgradable from: 5.1]\n", "")
	if len(apt) != 2 || !apt[0].Security || apt[0].Current != "3.0.14" || apt[1].Security {
		t.Fatalf("apt: %+v", apt)
	}
	dnf := ParsePackageUpdates("dnf", "openssl.x86_64 1:3.0-2 updates\nbash.x86_64 5.1-2 updates\nObsoleting Packages\nother.x86_64 1 extras\n", "RHSA-2024:0042 Important/Sec. openssl-1:3.0-2.x86_64\n")
	if len(dnf) != 2 || !dnf[0].Security || dnf[1].Security {
		t.Fatalf("dnf: %+v", dnf)
	}
	if !ParseRebootRequired("required\n") || ParseRebootRequired("unknown") {
		t.Fatal("unexpected reboot state")
	}
}

func TestOpsPackageCommands(t *testing.T) {
	for _, check := range []struct {
		manager, scope string
		names          []string
		want           string
	}{
		{"apt", "selected", []string{"libssl3:amd64"}, "LC_ALL=C DEBIAN_FRONTEND=noninteractive 'apt-get' 'install' '--only-upgrade' '--no-remove' '-y' '--' 'libssl3:amd64'"},
		{"apt", "security", []string{"libssl3:amd64=1:3.1+deb12u1"}, "LC_ALL=C DEBIAN_FRONTEND=noninteractive 'apt-get' 'install' '--only-upgrade' '--no-remove' '-y' '--' 'libssl3:amd64=1:3.1+deb12u1'"},
		{"apt", "security", []string{"libssl3:amd64"}, ""},
		{"dnf", "security", nil, "LC_ALL=C 'dnf' 'upgrade' '-y' '--security'"},
		{"yum", "selected", []string{"--installroot"}, ""},
		{"apk", "all", nil, ""},
	} {
		got, err := PackageUpgradeScript(check.manager, check.scope, check.names)
		if got != check.want || (err != nil) != (check.want == "") {
			t.Errorf("%s %s: %q, %v", check.manager, check.scope, got, err)
		}
	}
}
