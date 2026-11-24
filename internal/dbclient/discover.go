package dbclient

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

const ListenScript = `if command -v 'ss' >/dev/null 2>&1; then
  'ss' '-tlnp'
elif command -v 'netstat' >/dev/null 2>&1; then
  'netstat' '-tln'
fi`

const DockerScript = `'docker' 'ps' '--format' '{{.Ports}}'`

type Endpoint struct {
	Engine Engine
	Host   string
	Port   int
}

func Discover(listeners, docker string) []Endpoint {
	var results []Endpoint
	add := func(address string, kind Engine) {
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			last := strings.LastIndexByte(address, ':')
			if last < 0 {
				return
			}
			host, portText = address[:last], address[last+1:]
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return
		}
		if kind.Port() == 0 {
			for _, engine := range []Engine{MySQL, Postgres, Redis} {
				if engine.Port() == port {
					kind = engine
				}
			}
		}
		if kind.Port() == 0 {
			return
		}
		switch host {
		case "*", "0.0.0.0":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		if _, err := netip.ParseAddr(host); err != nil || !validHost(host) {
			return
		}
		endpoint := Endpoint{kind, host, port}
		if !slices.Contains(results, endpoint) {
			results = append(results, endpoint)
		}
	}
	for line := range strings.SplitSeq(listeners, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		kind := Engine("")
		for _, engine := range []struct {
			process string
			kind    Engine
		}{{`"mysqld"`, MySQL}, {`"mariadbd"`, MySQL}, {`"postgres"`, Postgres}, {`"redis-server"`, Redis}} {
			if strings.Contains(line, engine.process) {
				kind = engine.kind
				break
			}
		}
		if fields[0] == "LISTEN" || fields[0] == "tcp" || fields[0] == "tcp6" {
			add(fields[3], kind)
		}
	}
	for line := range strings.SplitSeq(docker, "\n") {
		for binding := range strings.SplitSeq(line, ",") {
			address, containerPort, ok := strings.Cut(strings.TrimSpace(binding), "->")
			if !ok {
				continue
			}
			for _, engine := range []Engine{MySQL, Postgres, Redis} {
				if containerPort == strconv.Itoa(engine.Port())+"/tcp" {
					add(address, engine)
				}
			}
		}
	}
	return results
}
