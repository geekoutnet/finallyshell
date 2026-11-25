//go:build windows

package rdp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"finallyshell/internal/config"
)

// createNoWindow starts a process without a console window. Not
// DETACHED_PROCESS, which leaves console programs without one to write
// to.
const createNoWindow = 0x08000000

// Start writes the .rdp file of r's host into a private folder, keeps
// its password, and its gateway's, in the Credential Manager, where
// mstsc reads them (credentials_windows.go), and starts mstsc on the
// file, with /admin for the console session. wait waits for mstsc to
// exit, then deletes the credentials and the file.
func Start(r Request) (wait func() error, err error) {
	exe, err := mstscPath()
	if err != nil {
		return nil, err
	}
	h := r.Host
	path, dir, err := writeFile(h)
	if err != nil {
		return nil, err
	}
	var releases []func()
	undo := func() {
		for _, release := range releases {
			release()
		}
		os.RemoveAll(dir)
	}
	if login := config.RDPLogin(h); login != "" && r.Password != "" {
		target := strings.TrimSpace(h.Addr)
		if r.sshForward {
			target = hostPort(target, h.PortOr22())
		}
		release, err := holdCredential(credTypeGeneric, "TERMSRV/"+target, login, r.Password)
		if err != nil {
			undo()
			return nil, err
		}
		releases = append(releases, release)
	}
	if g := config.RDPOf(h); g.UsesGateway() && !g.GatewaySameLogin && strings.TrimSpace(g.GatewayUser) != "" && r.GatewayPassword != "" {
		release, err := holdCredential(credTypeDomainPassword, gatewayName(g.GatewayHost), strings.TrimSpace(g.GatewayUser), r.GatewayPassword)
		if err != nil {
			undo()
			return nil, err
		}
		releases = append(releases, release)
	}
	args := []string{path}
	if config.RDPOf(h).Admin {
		args = append(args, "/admin")
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := cmd.Start(); err != nil {
		undo()
		return nil, err
	}
	return func() error {
		cmd.Wait() // mstsc's exit code says nothing of the connection
		undo()
		return nil
	}, nil
}

// mstscPath is mstsc.exe's path.
func mstscPath() (string, error) {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "System32", "mstsc.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath("mstsc.exe")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%w: mstsc.exe", ErrNoClient)
		}
		return "", err
	}
	return p, nil
}
