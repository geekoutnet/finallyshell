//go:build !windows

package app

import (
	"os"
	"syscall"
)

func desktopBrowserRegistry(string) []string { return nil }

func desktopBrowserQuit(process *os.Process) { _ = process.Signal(syscall.SIGTERM) }

func desktopBrowserKill(process *os.Process) { _ = process.Kill() }

// desktopBrowserWaitFlag is "": Firefox's first process is the browser.
const desktopBrowserWaitFlag = ""
