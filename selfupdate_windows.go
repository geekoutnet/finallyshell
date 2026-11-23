//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// installerName is the asset of a release that installs the app here.
func installerName(version string) string {
	return "finallyshell-setup-" + version + "-amd64.exe"
}

// canInstallUpdate reports whether this copy was installed by the setup
// program, which an update's setup program installs over: its
// Uninstall.exe is beside it. A portable copy (the .zip) has none.
func canInstallUpdate() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "Uninstall.exe"))
	return err == nil
}

// startInstall starts, detached, what installs setup once this process
// has ended: the setup program, silent (/S), which installs where the app
// is, then the app again. The app quits after it.
func startInstall(setup string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startInstallAfter(os.Getpid(), setup, exe)
}

// startInstallAfter starts, detached, what runs setup /S once the process
// pid has ended, waits for it, then starts exe.
func startInstallAfter(pid int, setup, exe string) error {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := fmt.Sprintf(
		"Wait-Process -Id %d -ErrorAction SilentlyContinue; "+
			"Start-Process -FilePath %s -ArgumentList '/S' -Wait; "+
			"Start-Process -FilePath %s",
		pid, quote(setup), quote(exe))
	// No window, in a process group of its own, and out of the app's job
	// when the job lets it, so that the app's end does not end it. Not
	// DETACHED_PROCESS: PowerShell without a console runs nothing.
	const (
		createNewProcessGroup  = 0x00000200
		createBreakawayFromJob = 0x01000000
		createNoWindow         = 0x08000000
	)
	start := func(flags uint32) error {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-Command", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		return cmd.Start()
	}
	if err := start(createNoWindow | createNewProcessGroup | createBreakawayFromJob); err == nil {
		return nil
	}
	return start(createNoWindow | createNewProcessGroup)
}
