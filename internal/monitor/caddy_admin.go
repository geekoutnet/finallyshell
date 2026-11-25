package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrCaddyAdminOff indicates that the configuration disabled the admin API.
var ErrCaddyAdminOff = errors.New("the Caddy admin API is disabled")

func caddyAdminURL(address string) (base, socket string, err error) {
	if address == "off" {
		return "", "", ErrCaddyAdminOff
	}
	if address == "" {
		address = "localhost:2019"
	}
	if name, ok := strings.CutPrefix(address, "unix/"); ok {
		if !caddyPath(name) {
			return "", "", ErrCaddyUnsafe
		}
		return "http://localhost", name, nil
	}
	address = strings.TrimPrefix(address, "tcp/")
	host, port, splitErr := net.SplitHostPort(address)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	if splitErr != nil && (address == "localhost" || net.ParseIP(address) != nil) {
		host, port, splitErr = address, "2019", nil
	}
	portNumber, portErr := strconv.Atoi(port)
	if splitErr != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || strings.ContainsAny(host, " \t/\\?%#@\r\n\x00") {
		return "", "", ErrCaddyUnsafe
	}
	return "http://" + net.JoinHostPort(host, port), "", nil
}

func caddyAdminPath(target string) bool {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.ContainsAny(target, "\\\x00\n\r") {
		return false
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.ContainsAny(parsed.Path, "\\\x00\r\n#") {
		return false
	}
	for segment := range strings.SplitSeq(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return parsed.Path == "/load" || parsed.Path == "/stop" || parsed.Path == "/adapt" || parsed.Path == "/metrics" || parsed.Path == "/reverse_proxy/upstreams" ||
		parsed.Path == "/config" || strings.HasPrefix(parsed.Path, "/config/") || strings.HasPrefix(parsed.Path, "/id/") || strings.HasPrefix(parsed.Path, "/pki/ca/")
}

// CaddyAdmin calls a documented admin API route using curl on the server (in
// the container's network namespace for Docker TCP). A body goes on stdin and is
// sent as JSON or as a Caddyfile for /load and /adapt. It returns the
// response, an HTTP/transport error, ErrCaddyAdminOff or ErrCaddyUnsafe.
func CaddyAdmin(ctx context.Context, run CaddyRunner, inst CaddyInstall, method, target, body string) (string, error) {
	inst, err := caddyNormalize(inst)
	if err != nil || !caddyAdminPath(target) || len(body) > caddyMaxTree || (method == "GET" && body != "") {
		return "", ErrCaddyUnsafe
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return "", ErrCaddyUnsafe
	}
	base, socket, err := caddyAdminURL(inst.AdminAddr)
	if err != nil {
		return "", err
	}
	args := []string{"curl", "-q", "--silent", "--show-error", "--fail", "--globoff", "--noproxy", "*", "--connect-timeout", "4", "--max-time", "20", "--request", method}
	if socket != "" {
		args = append(args, "--unix-socket", socket)
	}
	var stdin io.Reader
	if body != "" {
		media := "application/json"
		if (target == "/load" || target == "/adapt") && !json.Valid([]byte(body)) {
			media = "text/caddyfile"
		}
		args = append(args, "--header", "Content-Type: "+media, "--data-binary", "@-")
		stdin = strings.NewReader(body)
	}
	args = append(args, "--", base+target)
	command := SystemCommand(args...)
	if inst.Method == CaddyDocker {
		command = "pid=$(" + SystemCommand("docker", "inspect", "--type", "container", "--format", "{{.State.Pid}}", inst.Container) + ") || exit '1'\n" +
			"case \"$pid\" in ''|'0'|*[!0-9]*) exit '1' ;; esac\n"
		if socket != "" {
			for index, arg := range args {
				if arg == "--unix-socket" {
					command += SystemCommand(args[:index+1]...) + ` "/proc/$pid/root"` + privilegeQuote(socket) + " " + SystemCommand(args[index+2:]...)
					break
				}
			}
		} else {
			command += SystemCommand("nsenter", "--target") + ` "$pid" ` + SystemCommand("--net", "--") + " " + SystemCommand(args...)
		}
	}
	stdout, stderr, err := run(ctx, command, stdin, nil)
	if err != nil {
		return "", caddyCommandError(err, strings.TrimSpace(stderr))
	}
	return stdout, nil
}
