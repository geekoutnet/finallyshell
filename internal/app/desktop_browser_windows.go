package app

import (
	"os"
	"strings"
	"sync"
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
var desktopBrowserVisible = desktopBrowserUser32.NewProc("IsWindowVisible")

// desktopBrowserClosing is the process tree desktopBrowserCloseWindow
// closes, for the length of one EnumWindows.
var desktopBrowserClosing struct {
	sync.Mutex
	root uint32
	tree map[uint32]bool
}

var desktopBrowserCloseWindow = syscall.NewCallback(func(window, _ uintptr) uintptr {
	var owner uint32
	desktopBrowserWindowPID.Call(window, uintptr(unsafe.Pointer(&owner)))
	if owner == desktopBrowserClosing.root || desktopBrowserClosing.tree[owner] {
		if visible, _, _ := desktopBrowserVisible.Call(window); owner == desktopBrowserClosing.root || visible != 0 {
			desktopBrowserPostMessage.Call(window, 0x0010, 0, 0)
		}
	}
	return 1
})

// desktopBrowserTree is pid and the processes it started, and theirs:
// Firefox's first process is a launcher that runs the browser as its
// child (and waits for it, with --wait-for-browser).
func desktopBrowserTree(pid uint32) map[uint32]bool {
	tree := map[uint32]bool{pid: true}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return tree
	}
	defer windows.CloseHandle(snapshot)
	parents := make(map[uint32]uint32)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID != 0 && entry.ProcessID != entry.ParentProcessID {
			parents[entry.ProcessID] = entry.ParentProcessID
		}
	}
	for grown := true; grown; {
		grown = false
		for child, parent := range parents {
			if tree[parent] && !tree[child] {
				tree[child], grown = true, true
			}
		}
	}
	return tree
}

// desktopBrowserQuit asks the browser to close: its own windows, and the
// visible ones of the processes it started.
func desktopBrowserQuit(process *os.Process) {
	tree := desktopBrowserTree(uint32(process.Pid))
	desktopBrowserClosing.Lock()
	defer desktopBrowserClosing.Unlock()
	desktopBrowserClosing.root, desktopBrowserClosing.tree = uint32(process.Pid), tree
	desktopBrowserEnumWindows.Call(desktopBrowserCloseWindow, 0)
	desktopBrowserClosing.tree = nil
}

// desktopBrowserKill ends the browser and every process it started.
func desktopBrowserKill(process *os.Process) {
	for pid := range desktopBrowserTree(uint32(process.Pid)) {
		if pid == uint32(process.Pid) {
			continue
		}
		if handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
			_ = windows.TerminateProcess(handle, 1)
			windows.CloseHandle(handle)
		}
	}
	_ = process.Kill()
}

// desktopBrowserWaitFlag keeps Firefox's launcher running until the
// browser it starts ends, so that the app knows when it does.
const desktopBrowserWaitFlag = "--wait-for-browser"
