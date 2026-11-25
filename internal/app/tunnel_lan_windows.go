package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"golang.org/x/sys/windows"
)

func tunnelNetsh() string { return filepath.Join(windowsSystemDir(), "netsh.exe") }

func windowsSystemDir() string {
	if dir, err := windows.GetSystemDirectory(); err == nil {
		return dir
	}
	return filepath.Join(os.Getenv("SystemRoot"), "System32")
}

func tunnelLANFirewallStatus(ctx context.Context, port int) string {
	cmd := exec.CommandContext(ctx, tunnelNetsh(), "advfirewall", "firewall", "show", "rule", "name=all", "verbose")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	exe, exeErr := os.Executable()
	if err != nil || exeErr != nil {
		return locales.TexpLANFirewallUnknown()
	}
	switch tunnelLANFirewallMatch(string(output), exe, port) {
	case "block":
		return locales.TexpLANFirewallBlock()
	case "allow":
		return locales.TexpLANFirewallMatch()
	}
	return locales.TexpLANFirewallUnknown()
}

func addTunnelLANFirewallRule(port int) error {
	cmd := tunnelLANRule(port)
	if cmd == "" {
		return monitor.ErrOpsInvalid
	}
	exe, err := windows.UTF16PtrFromString(tunnelNetsh())
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	args, _ := windows.UTF16PtrFromString(strings.TrimPrefix(cmd, "netsh "))
	return windows.ShellExecute(0, verb, exe, args, nil, windows.SW_HIDE)
}
