package serial

import (
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"finallyshell/internal/config"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func nativePorts() ([]string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer key.Close()
	values, err := key.ReadValueNames(-1)
	var names []string
	for _, value := range values {
		if name, _, err := key.GetStringValue(value); err == nil {
			names = append(names, name)
		}
	}
	return names, err
}

func configureDCB(dcb *windows.DCB, cfg config.SerialConfig) {
	const (
		binary       = 1 << 0
		parityCheck  = 1 << 1
		outCTS       = 1 << 2
		dtrEnable    = 1 << 4
		outX         = 1 << 8
		inX          = 1 << 9
		rtsEnable    = 1 << 12
		rtsHandshake = 2 << 12
	)
	dcb.BaudRate, dcb.ByteSize = uint32(cfg.Baud), uint8(cfg.DataBits)
	dcb.Parity, dcb.StopBits = 0, 0
	dcb.Flags = binary | dtrEnable
	switch cfg.Parity {
	case "odd":
		dcb.Parity, dcb.Flags = 1, dcb.Flags|parityCheck
	case "even":
		dcb.Parity, dcb.Flags = 2, dcb.Flags|parityCheck
	}
	switch cfg.StopBits {
	case 1.5:
		dcb.StopBits = 1
	case 2:
		dcb.StopBits = 2
	}
	switch cfg.Flow {
	case "rtscts":
		dcb.Flags |= rtsHandshake | outCTS
	case "xonxoff":
		dcb.Flags |= rtsEnable | outX | inX
	default:
		dcb.Flags |= rtsEnable
	}
	dcb.XonChar, dcb.XoffChar = 0x11, 0x13
}

func openPort(name string, cfg config.SerialConfig) (io.ReadWriteCloser, error) {
	path, err := windows.UTF16PtrFromString(`\\.\` + name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	dcb := windows.DCB{DCBlength: uint32(unsafe.Sizeof(windows.DCB{}))}
	if err = windows.GetCommState(handle, &dcb); err == nil {
		configureDCB(&dcb, cfg)
		err = windows.SetCommState(handle, &dcb)
	}
	if err == nil {
		err = windows.SetCommTimeouts(handle, &windows.CommTimeouts{
			ReadIntervalTimeout: ^uint32(0), ReadTotalTimeoutMultiplier: ^uint32(0),
			ReadTotalTimeoutConstant: 500, WriteTotalTimeoutConstant: 3000,
		})
	}
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return &windowsPort{handle: handle}, nil
}

type windowsPort struct {
	handle windows.Handle
	mu     sync.Mutex
	closed bool
	active sync.WaitGroup
}

func (port *windowsPort) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for {
		count, err := port.transfer(dst, windows.ReadFile)
		if count != 0 || err != nil {
			return count, err
		}
	}
}

func (port *windowsPort) Write(src []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	return port.transfer(src, windows.WriteFile)
}

func (port *windowsPort) transfer(buf []byte, start func(windows.Handle, []byte, *uint32, *windows.Overlapped) error) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	overlap := windows.Overlapped{HEvent: event}
	var pins runtime.Pinner
	pins.Pin(&overlap)
	pins.Pin(&buf[0])
	defer pins.Unpin()
	var count uint32
	port.mu.Lock()
	if port.closed {
		port.mu.Unlock()
		return 0, os.ErrClosed
	}
	port.active.Add(1)
	err = start(port.handle, buf, &count, &overlap)
	port.mu.Unlock()
	defer port.active.Done()
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(port.handle, &overlap, &count, true)
	}
	return int(count), err
}

func (port *windowsPort) Close() error {
	port.mu.Lock()
	if port.closed {
		port.mu.Unlock()
		return nil
	}
	port.closed = true
	windows.CancelIoEx(port.handle, nil)
	port.mu.Unlock()
	port.active.Wait()
	return windows.CloseHandle(port.handle)
}
