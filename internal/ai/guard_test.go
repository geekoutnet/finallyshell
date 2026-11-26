package ai

import "testing"

func TestCutsConnection(t *testing.T) {
	for command, want := range map[string]ConnectionCut{
		"systemctl stop sshd":                          CutSSH,
		"sudo systemctl restart ssh":                   CutSSH,
		"sudo -n systemctl disable --now sshd.service": CutSSH,
		"service ssh stop":                             CutSSH,
		"/etc/init.d/dropbear restart":                 CutSSH,
		"pkill sshd":                                   CutSSH,
		"killall -9 sshd":                              CutSSH,
		"systemctl restart NetworkManager":             CutNetwork,
		"systemctl stop networking":                    CutNetwork,
		"ifdown eth0":                                  CutNetwork,
		"ip link set eth0 down":                        CutNetwork,
		"ip link set dev ens3 down":                    CutNetwork,
		"nmcli networking off":                         CutNetwork,
		"nmcli con down 'Wired connection 1'":          CutNetwork,
		"ufw enable":                                   CutFirewall,
		"ufw --force enable":                           CutFirewall,
		"systemctl start firewalld":                    CutFirewall,
		"systemctl enable --now firewalld":             CutFirewall,
		"iptables -P INPUT DROP":                       CutFirewall,
		"nft add chain inet f input { type filter hook input priority 0; policy drop; }": CutFirewall,
		"pkill -u root":              CutProcesses,
		"kill -9 -1":                 CutProcesses,
		"df -h; systemctl stop sshd": CutSSH,
		// Look-alikes that keep the connection.
		"systemctl status sshd":                  CutNone,
		"systemctl reload sshd":                  CutNone,
		"systemctl restart nginx":                CutNone,
		"journalctl -u ssh -n 50":                CutNone,
		"grep sshd /var/log/auth.log":            CutNone,
		"ss -tlnp | grep ssh":                    CutNone,
		"ip link show":                           CutNone,
		"ufw status":                             CutNone,
		"ufw allow 22/tcp && ufw --force enable": CutNone,
		"ufw allow OpenSSH; ufw enable":          CutNone,
		"firewall-cmd --permanent --add-service=ssh && systemctl start firewalld": CutNone,
		"iptables -A INPUT -p tcp --dport 22 -j ACCEPT && iptables -P INPUT DROP": CutNone,
		"kill -9 1234": CutNone,
		"pkill nginx":  CutNone,
		"reboot":       CutNone, // high-risk, not blocked
	} {
		if got := CutsConnection(command); got != want {
			t.Errorf("CutsConnection(%q) = %v, want %v", command, got, want)
		}
	}
}
