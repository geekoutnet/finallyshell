package config

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

type SSHForwards struct {
	Tunnels              []Tunnel
	ExitOnForwardFailure *bool
}

var ErrSSHForward = errors.New("invalid or unsupported OpenSSH forwarding syntax")

func ParseSSHForwards(text string) (SSHForwards, error) {
	var result SSHForwards
	var command bool
	for index, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		fail := fmt.Errorf("line %d: %w", index+1, ErrSSHForward)
		if command {
			return SSHForwards{}, fail
		}
		first, _, _ := strings.Cut(strings.ReplaceAll(line, "\t", " "), " ")
		if first == "ssh" || first == "ssh.exe" {
			if len(result.Tunnels) != 0 || result.ExitOnForwardFailure != nil || result.parseCommand(line) != nil {
				return SSHForwards{}, fail
			}
			command = true
		} else if err := result.parseDirective(line); err != nil {
			return SSHForwards{}, fail
		}
	}
	return result, nil
}

func (result *SSHForwards) parseCommand(line string) error {
	args, err := sshForwardWords(line, true)
	if err != nil {
		return err
	}
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			if len(args[index+1:]) != 1 {
				return ErrSSHForward
			}
			return nil
		}
		if !strings.HasPrefix(arg, "-") {
			if index != len(args)-1 {
				return ErrSSHForward
			}
			return nil
		}
		if len(arg) == 1 {
			return ErrSSHForward
		}
		for flags := arg[1:]; flags != ""; flags = flags[1:] {
			flag := flags[0]
			if strings.ContainsRune("NfT46qv", rune(flag)) {
				continue
			}
			if !strings.ContainsRune("LRDopliJF", rune(flag)) {
				return ErrSSHForward
			}
			value := flags[1:]
			if value == "" {
				index++
				if index >= len(args) {
					return ErrSSHForward
				}
				value = args[index]
			}
			switch flag {
			case 'L', 'R', 'D':
				forward, err := parseSSHForward(flag, value, "", false)
				if err != nil {
					return err
				}
				result.Tunnels = append(result.Tunnels, forward)
			case 'o':
				if err := result.parseDirective(value); err != nil {
					return err
				}
			}
			break
		}
	}
	return ErrSSHForward
}

func (result *SSHForwards) parseDirective(line string) error {
	nameEnd := strings.IndexAny(line, " \t=")
	if nameEnd < 0 {
		return ErrSSHForward
	}
	name := line[:nameEnd]
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line[nameEnd:]), "="))
	args, err := sshForwardWords(value, false)
	if err != nil {
		return err
	}
	forward, exit, err := ParseSSHForwardDirective(name, args)
	if err != nil {
		return err
	}
	if exit != nil {
		if result.ExitOnForwardFailure == nil {
			result.ExitOnForwardFailure = exit
		}
	} else {
		result.Tunnels = append(result.Tunnels, forward)
	}
	return nil
}

func ParseSSHForwardDirective(name string, args []string) (Tunnel, *bool, error) {
	var flag byte
	switch strings.ToLower(name) {
	case "localforward":
		flag = 'L'
	case "remoteforward":
		flag = 'R'
	case "dynamicforward":
		flag = 'D'
	case "exitonforwardfailure":
		if len(args) == 1 {
			switch strings.ToLower(args[0]) {
			case "yes", "no":
				value := strings.EqualFold(args[0], "yes")
				return Tunnel{}, &value, nil
			}
		}
		return Tunnel{}, nil, ErrSSHForward
	default:
		return Tunnel{}, nil, ErrSSHForward
	}
	if len(args) == 0 || len(args) > 2 || flag == 'L' && len(args) != 2 || flag == 'D' && len(args) != 1 {
		return Tunnel{}, nil, ErrSSHForward
	}
	target := ""
	if len(args) == 2 {
		target = args[1]
		if target == "" {
			return Tunnel{}, nil, ErrSSHForward
		}
	}
	forward, err := parseSSHForward(flag, args[0], target, true)
	return forward, nil, err
}

func parseSSHForward(flag byte, listen, target string, directive bool) (Tunnel, error) {
	first, rest, sep := sshForwardSplit(listen)
	var bind, port string
	if !sep || !directive && sshForwardDigits(first) {
		port = first
	} else {
		bind = first
		port, rest, sep = sshForwardSplit(rest)
		if bind == "" || bind == "*" {
			bind = "0.0.0.0"
		} else if !sshForwardHost(bind) {
			return Tunnel{}, ErrSSHForward
		}
		if flag == 'R' && !strings.EqualFold(bind, "localhost") {
			if _, err := netip.ParseAddr(bind); err != nil {
				return Tunnel{}, ErrSSHForward
			}
		}
	}
	if sep {
		if directive || flag == 'D' || rest == "" {
			return Tunnel{}, ErrSSHForward
		}
		target = rest
	}
	listenPort, ok := sshForwardPort(port, flag == 'R')
	if !ok {
		return Tunnel{}, ErrSSHForward
	}
	if flag == 'R' && strings.EqualFold(bind, "localhost") {
		bind = ""
	}
	forward := Tunnel{ListenHost: bind, ListenPort: listenPort}
	switch flag {
	case 'D':
		forward.Kind = TunnelDynamic
	case 'R':
		if target == "" {
			forward.Kind = TunnelRemoteDynamic
			return forward, nil
		}
		forward.Kind = TunnelRemote
	case 'L':
		forward.Kind = TunnelLocal
	}
	if flag == 'D' {
		return forward, nil
	}
	if strings.HasPrefix(target, "/") && !strings.ContainsAny(target, ":%") && !strings.ContainsFunc(target, unicode.IsControl) && flag == 'L' {
		forward.TargetSocket = target
		return forward, nil
	}
	host, port, found := sshForwardSplit(target)
	if !found || !sshForwardHost(host) {
		return Tunnel{}, ErrSSHForward
	}
	targetPort, ok := sshForwardPort(port, false)
	if !ok {
		return Tunnel{}, ErrSSHForward
	}
	forward.TargetHost, forward.TargetPort = host, targetPort
	return forward, nil
}

func sshForwardSplit(value string) (string, string, bool) {
	if strings.HasPrefix(value, "[") {
		inner, rest, ok := strings.Cut(value[1:], "]:")
		addr, err := netip.ParseAddr(inner)
		if ok && err == nil && addr.Is6() && addr.Zone() == "" {
			return inner, rest, true
		}
		return value, "", false
	}
	return strings.Cut(value, ":")
}

func sshForwardHost(value string) bool {
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.Zone() == ""
	}
	return value != "" && !strings.HasPrefix(value, "-") && !strings.ContainsFunc(value, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._", char))
	})
}

func sshForwardDigits(value string) bool {
	return value != "" && !strings.ContainsFunc(value, func(char rune) bool { return char < '0' || char > '9' })
}

func sshForwardPort(value string, zero bool) (int, bool) {
	if !sshForwardDigits(value) {
		return 0, false
	}
	port, err := strconv.ParseUint(value, 10, 16)
	return int(port), err == nil && (port != 0 || zero)
}

func sshForwardWords(line string, shell bool) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote byte
	started := false
	flush := func() {
		if started {
			words = append(words, word.String())
			word.Reset()
			started = false
		}
	}
	for index := 0; index < len(line); index++ {
		char := line[index]
		switch {
		case char < 32 && char != '\t' || char == 127:
			return nil, ErrSSHForward
		case quote != 0 && char == quote:
			quote = 0
		case char == '\\' && (!shell || quote != '\''):
			if index+1 == len(line) {
				return nil, ErrSSHForward
			}
			next := line[index+1]
			if next < 32 || next == 127 {
				return nil, ErrSSHForward
			}
			if quote == 0 || next == quote || next == '\\' || shell && strings.ContainsRune("$`", rune(next)) {
				index++
				char = next
			}
			word.WriteByte(char)
			started = true
		case shell && (quote == 0 && strings.ContainsRune(";&|<>()$`", rune(char)) || quote == '"' && strings.ContainsRune("$`", rune(char))):
			return nil, ErrSSHForward
		case quote == 0 && (char == '\t' || char == ' '):
			flush()
		case quote == 0 && (char == '\'' || char == '"'):
			quote, started = char, true
		case quote == 0 && char == '#' && !started:
			return words, nil
		default:
			word.WriteByte(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, ErrSSHForward
	}
	flush()
	return words, nil
}
