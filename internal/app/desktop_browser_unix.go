//go:build !windows

package app

import (
	"os"
	"syscall"
)

func desktopBrowserRegistry(string) []string { return nil }

func desktopBrowserQuit(process *os.Process) { _ = process.Signal(syscall.SIGTERM) }
