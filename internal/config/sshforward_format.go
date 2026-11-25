package config

import (
	"net"
	"strconv"
	"strings"
	"unicode"
)

func FormatSSHCommand(host *Host) string {
	var options, notes []string
	for index, tunnel := range host.Tunnels {
		flag, listen, target, note := sshForwardOption(tunnel)
		if flag == "" {
			notes = append(notes, sshForwardNote(index, note))
			continue
		}
		if note != "" {
			notes = append(notes, sshForwardNote(index, note))
		}
		if target != "" {
			listen += ":" + target
		}
		options = append(options, "-"+flag, sshForwardQuote(listen))
	}
	if len(options) == 0 {
		return strings.Join(notes, "\n")
	}
	if !sshForwardHost(host.Addr) || strings.ContainsFunc(host.User, unicode.IsControl) || host.PortOr22() < 1 || host.PortOr22() > 65535 {
		return strings.Join(append(notes, "# Host has an invalid OpenSSH destination."), "\n")
	}
	args := []string{"ssh", "-N", "-o", "'ExitOnForwardFailure=yes'", "-p", sshForwardQuote(strconv.Itoa(host.PortOr22()))}
	if host.User != "" {
		args = append(args, "-l", sshForwardQuote(host.User))
	}
	if ProxyOf(host) != nil {
		notes = append(notes, "# Configure this host's connection proxy separately before using this command.")
	}
	args = append(args, options...)
	args = append(args, sshForwardQuote(host.Addr))
	return strings.Join(append(notes, strings.Join(args, " ")), "\n")
}

func FormatSSHConfig(tunnels []Tunnel) string {
	var lines []string
	for index, tunnel := range tunnels {
		flag, listen, target, note := sshForwardOption(tunnel)
		if note != "" {
			lines = append(lines, sshForwardNote(index, note))
		}
		name := map[string]string{"L": "LocalForward", "R": "RemoteForward", "D": "DynamicForward"}[flag]
		if name == "" {
			continue
		}
		line := name + " " + sshForwardConfigQuote(listen)
		if target != "" {
			line += " " + sshForwardConfigQuote(target)
		}
		lines = append(lines, line)
	}
	if len(lines) != 0 {
		lines = append(lines, "ExitOnForwardFailure yes")
	}
	return strings.Join(lines, "\n")
}

func sshForwardOption(tunnel Tunnel) (flag, listen, target, note string) {
	if tunnel.Disabled {
		return "", "", "", "disabled (not exported)"
	}
	if tunnel.ProxyUser != "" || len(tunnel.AllowFrom) != 0 || tunnel.ServerRelay {
		return "", "", "", "requires authentication, an allowlist or a relay (not exported)"
	}
	if tunnel.Mixed || tunnel.Upstream != "" || tunnel.MaxConns != 0 || tunnel.RateKBps != 0 || tunnel.IdleSeconds != 0 {
		return "", "", "", "uses a mixed proxy, upstream or limits (not exported)"
	}
	switch tunnel.Kind {
	case TunnelLocal:
		flag = "L"
		if tunnel.TargetSocket != "" {
			target = tunnel.TargetSocket
		} else {
			target = net.JoinHostPort(tunnel.TargetHost, strconv.Itoa(tunnel.TargetPort))
		}
	case TunnelRemote:
		flag = "R"
		target = net.JoinHostPort(tunnel.TargetHost, strconv.Itoa(tunnel.TargetPort))
	case TunnelDynamic:
		flag = "D"
	case TunnelRemoteDynamic:
		flag = "R"
	default:
		return "", "", "", "unsupported proxy kind (not exported)"
	}
	listen = tunnel.ListenAddr()
	if _, err := parseSSHForward(flag[0], listen, target, true); err != nil {
		return "", "", "", "address or automatic port not supported by OpenSSH (not exported)"
	}
	if tunnel.SystemProxy || tunnel.Background || len(tunnel.PAC) != 0 {
		note = "system proxy, PAC and background management are not exported"
	}
	return flag, listen, target, note
}

func sshForwardNote(index int, note string) string {
	return "# Tunnel " + strconv.Itoa(index+1) + ": " + note
}

func sshForwardConfigQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func sshForwardQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
