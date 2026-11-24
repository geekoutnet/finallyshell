package serial

import (
	"unsafe"

	"finallyshell/internal/config"
	"golang.org/x/sys/unix"
)

type uint32or64 = uint64

const hardwareFlow = unix.CRTSCTS

func setTermios(fd int, cfg config.SerialConfig) error {
	settings, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}
	configureTermios(settings, cfg)
	switch cfg.Baud {
	case unix.B50, unix.B75, unix.B110, unix.B134, unix.B150, unix.B200, unix.B300, unix.B600, unix.B1200,
		unix.B1800, unix.B2400, unix.B4800, unix.B7200, unix.B9600, unix.B14400, unix.B19200, unix.B28800,
		unix.B38400, unix.B57600, unix.B76800, unix.B115200, unix.B230400:
		settings.Ispeed, settings.Ospeed = uint64(cfg.Baud), uint64(cfg.Baud)
		return unix.IoctlSetTermios(fd, unix.TIOCSETA, settings)
	}
	settings.Ispeed, settings.Ospeed = unix.B9600, unix.B9600
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, settings); err != nil {
		return err
	}
	const iosSioSpeed = 0x80085402
	speed := uint64(cfg.Baud)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), iosSioSpeed, uintptr(unsafe.Pointer(&speed)))
	if errno != 0 {
		return errno
	}
	return nil
}
