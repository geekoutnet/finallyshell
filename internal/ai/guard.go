package ai

import (
	"regexp"
	"strings"
)

// Commands that would cut the SSH connection the assistant works over:
// stopping the SSH server, the network, or a firewall turned on with no
// way in for SSH. The app never runs them, approved or not; the model is
// told to leave them to the user, in the terminal. Rebooting stays a
// high-risk command the user may run with RUN typed.

// ConnectionCut is why a command would cut the assistant's connection.
type ConnectionCut int

const (
	CutNone ConnectionCut = iota
	CutSSH
	CutNetwork
	CutFirewall
	CutProcesses
)

var (
	cutSystemctl = regexp.MustCompile(`^systemctl(\s+-\S+)*\s+(stop|restart|disable|mask|kill|try-restart|reload-or-restart|isolate)\b(.*)$`)
	cutService   = regexp.MustCompile(`^service\s+(\S+)\s+(stop|restart|force-reload)\b`)
	cutRC        = regexp.MustCompile(`^/etc/init\.d/(\S+)\s+(stop|restart)\b`)
	sshUnit      = regexp.MustCompile(`^(ssh|sshd|openssh|openssh-server|dropbear)(\.service|\.socket)?$`)
	netUnit      = regexp.MustCompile(`^(network|networking|NetworkManager|systemd-networkd|wicked|netplan.*)(\.service)?$`)
	firewallUnit = regexp.MustCompile(`^(firewalld|ufw|nftables|iptables)(\.service)?$`)
	cutKillSSH   = regexp.MustCompile(`^(pkill|killall)(\s+-\S+)*\s+(sshd|ssh|dropbear)\b`)
	cutKillUser  = regexp.MustCompile(`^(pkill|killall)\b.*\s-u\b|^kill(\s+-(9|KILL|SIGKILL|s\s+(9|KILL|SIGKILL)))?\s+-1\b`)
	cutIfdown    = regexp.MustCompile(`^(ifdown|ifconfig\s+\S+\s+down|ip\s+(link|l)\s+set\s+(dev\s+)?\S+\s+down|nmcli\s+(networking\s+off|radio\s+all\s+off|(c|con|connection)\s+down|(d|dev|device)\s+(disconnect|down))\b)`)
	cutUFW       = regexp.MustCompile(`^ufw(\s+--force)?\s+enable\b`)
	cutFirewalld = regexp.MustCompile(`^systemctl(\s+-\S+)*\s+(start|enable|restart)\b.*\bfirewalld\b`)
	cutPolicy    = regexp.MustCompile(`^(iptables|ip6tables)\s+(-t\s+filter\s+)?-P\s+INPUT\s+(DROP|REJECT)\b`)
	// nft's rules hold semicolons of their own: read as one.
	cutNft     = regexp.MustCompile(`\bnft\b.*\bhook\s+input\b.*\bpolicy\s+drop\b`)
	sshAllowed = regexp.MustCompile(`\b(22|ssh|OpenSSH)\b`)
	segments   = regexp.MustCompile(`\s*(;|&&|\|\||\||\n)\s*`)
	privileged = regexp.MustCompile(`^(sudo(\s+-\S+)*|doas|nohup|nice(\s+-n\s*\S+|\s+-\S+)*|setsid)\s+`)
)

// CutsConnection reports whether command would cut the SSH connection the
// assistant itself works over, and how.
func CutsConnection(command string) ConnectionCut {
	whole := strings.Join(strings.Fields(command), " ")
	if cutNft.MatchString(whole) && !sshAllowed.MatchString(whole) {
		return CutFirewall
	}
	for _, part := range segments.Split(command, -1) {
		part = strings.Join(strings.Fields(part), " ")
		for {
			stripped := privileged.ReplaceAllString(part, "")
			if stripped == part {
				break
			}
			part = stripped
		}
		if cut := cutsConnection(part, whole); cut != CutNone {
			return cut
		}
	}
	return CutNone
}

func cutsConnection(part, whole string) ConnectionCut {
	if m := cutSystemctl.FindStringSubmatch(part); m != nil {
		for _, unit := range strings.Fields(m[3]) {
			switch {
			case strings.HasPrefix(unit, "-"):
			case sshUnit.MatchString(unit):
				return CutSSH
			case netUnit.MatchString(unit):
				return CutNetwork
			}
		}
	}
	for _, re := range []*regexp.Regexp{cutService, cutRC} {
		if m := re.FindStringSubmatch(part); m != nil {
			switch {
			case sshUnit.MatchString(m[1]):
				return CutSSH
			case netUnit.MatchString(m[1]):
				return CutNetwork
			}
		}
	}
	switch {
	case cutKillSSH.MatchString(part):
		return CutSSH
	case cutKillUser.MatchString(part):
		return CutProcesses
	case cutIfdown.MatchString(part):
		return CutNetwork
	case cutUFW.MatchString(part), cutFirewalld.MatchString(part), cutPolicy.MatchString(part):
		// Turned on with SSH let in, in the same command, it keeps the
		// connection.
		if !sshAllowed.MatchString(whole) {
			return CutFirewall
		}
	}
	return CutNone
}

// String is the reason, for the model.
func (cut ConnectionCut) String() string {
	switch cut {
	case CutSSH:
		return "it stops or restarts the SSH server"
	case CutNetwork:
		return "it takes the network down"
	case CutFirewall:
		return "it turns on a firewall that does not let SSH in"
	case CutProcesses:
		return "it kills every process of a user, the SSH session's included"
	}
	return ""
}
