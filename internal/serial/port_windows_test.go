package serial

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"finallyshell/internal/config"
	"golang.org/x/sys/windows"
)

func TestSerialWindowsSettings(t *testing.T) {
	const modem = 1 | 1<<4
	for _, tc := range []struct {
		flow  string
		flags uint32
	}{
		{"none", modem | 1<<12},
		{"rtscts", modem | 2<<12 | 1<<2},
		{"xonxoff", modem | 1<<12 | 1<<8 | 1<<9},
	} {
		settings := config.SerialConfig{Baud: 12345, DataBits: 7, StopBits: 2, Parity: "even", Flow: tc.flow}
		dcb := windows.DCB{Flags: ^uint32(0)}
		configureDCB(&dcb, settings)
		if dcb.BaudRate != 12345 || dcb.ByteSize != 7 || dcb.StopBits != 2 || dcb.Parity != 2 || dcb.Flags != tc.flags|1<<1 || dcb.XonChar != 0x11 || dcb.XoffChar != 0x13 {
			t.Errorf("%s: incorrect serial settings: %+v", tc.flow, dcb)
		}
	}
}

func TestSerialWindowsConcurrentIO(t *testing.T) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\serial-io-test-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	port := &windowsPort{handle: handle}
	t.Cleanup(func() { port.Close() })
	peer, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(peer) })
	if err := windows.ConnectNamedPipe(handle, nil); !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		t.Fatalf("connect test pipe: %v", err)
	}
	pending := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		_, err := port.transfer(make([]byte, 1), func(handle windows.Handle, data []byte, count *uint32, overlap *windows.Overlapped) error {
			err := windows.ReadFile(handle, data, count, overlap)
			close(started)
			return err
		})
		pending <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read did not start")
	}
	written := make(chan error, 1)
	go func() {
		_, err := port.Write([]byte("console"))
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending read blocked the write")
	}
	var buf [7]byte
	var count uint32
	if err := windows.ReadFile(peer, buf[:], &count, nil); err != nil || count != uint32(len(buf)) || string(buf[:]) != "console" {
		t.Fatalf("peer read %q (%d bytes): %v", buf, count, err)
	}
	if err := port.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-pending; !errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
		t.Fatalf("pending read: %v", err)
	}
}
