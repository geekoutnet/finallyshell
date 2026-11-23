//go:build !windows && !darwin

package rdp

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Start starts FreeRDP's client on r's host (FreeRDPArgs), writing the
// password to its standard input, so that it is not on its command line.
// It fails with ErrNoClient when no FreeRDP client is installed. wait
// waits for the client to exit, and returns an ExitError when it failed,
// with the end of what it wrote.
func Start(r Request) (wait func() error, err error) {
	name, path := findFreeRDP()
	if path == "" {
		return nil, ErrNoClient
	}
	version := freeRDPVersion(name, versionOutput(name, path))
	stdinPassword := r.Password != "" && strings.TrimSpace(r.Host.User) != ""
	cmd := exec.Command(path, FreeRDPArgs(r.Host, version, stdinPassword, r.GatewayPassword)...)
	out := &tail{n: 4096}
	cmd.Stdout, cmd.Stderr = out, out
	if stdinPassword {
		cmd.Stdin = strings.NewReader(r.Password + "\n")
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() error {
		err := cmd.Wait()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &ExitError{Code: exit.ExitCode(), Output: out.String()}
		}
		return err
	}, nil
}

// findFreeRDP is the first FreeRDP client on the PATH, its name and path:
// "" when there is none.
func findFreeRDP() (name, path string) {
	for _, n := range freeRDPClients {
		if p, err := exec.LookPath(n); err == nil {
			return n, p
		}
	}
	return "", ""
}

// versionOutput is what the client prints for --version, when its name
// does not tell its version.
func versionOutput(name, path string) string {
	if strings.HasSuffix(name, "3") {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	return string(out)
}
