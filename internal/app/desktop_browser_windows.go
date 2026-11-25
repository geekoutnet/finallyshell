package app

import (
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func desktopBrowserRegistry(name string) []string {
	var paths []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, mode := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
			key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE|mode)
			if err != nil {
				continue
			}
			path, kind, err := key.GetStringValue("")
			key.Close()
			if err == nil && kind == registry.EXPAND_SZ {
				path, err = registry.ExpandString(path)
			}
			if err == nil {
				paths = append(paths, strings.Trim(strings.TrimSpace(path), `"`))
			}
		}
	}
	return paths
}

var desktopBrowserUser32 = windows.NewLazySystemDLL("user32.dll")
var desktopBrowserEnumWindows = desktopBrowserUser32.NewProc("EnumWindows")
var desktopBrowserWindowPID = desktopBrowserUser32.NewProc("GetWindowThreadProcessId")
var desktopBrowserPostMessage = desktopBrowserUser32.NewProc("PostMessageW")
var desktopBrowserCloseWindow = syscall.NewCallback(func(window, pid uintptr) uintptr {
	var owner uint32
	desktopBrowserWindowPID.Call(window, uintptr(unsafe.Pointer(&owner)))
	if uintptr(owner) == pid {
		desktopBrowserPostMessage.Call(window, 0x0010, 0, 0)
	}
	return 1
})

func desktopBrowserQuit(process *os.Process) {
	desktopBrowserEnumWindows.Call(desktopBrowserCloseWindow, uintptr(process.Pid))
}
