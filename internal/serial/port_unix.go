//go:build linux || darwin

package serial

import (
	"io"
	"os"

	"finallyshell/internal/config"
	"golang.org/x/sys/unix"
)

func nativePorts() ([]string, error) {
	entries, err := os.ReadDir("/dev")
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeCharDevice != 0 {
			names = append(names, entry.Name())
		}
	}
	return names, err
}

func openPort(name string, cfg config.SerialConfig) (io.ReadWriteCloser, error) {
	fd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	var info unix.Stat_t
	if err = unix.Fstat(fd, &info); err == nil && info.Mode&unix.S_IFMT != unix.S_IFCHR {
		err = ErrPort
	}
	if err == nil {
		err = unix.IoctlSetInt(fd, unix.TIOCEXCL, 0)
	}
	if err == nil {
		err = setTermios(fd, cfg)
	}
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func configureTermios(settings *unix.Termios, cfg config.SerialConfig) {
	settings.Iflag, settings.Oflag, settings.Lflag = 0, 0, 0
	settings.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB | hardwareFlow
	settings.Cflag |= unix.CLOCAL | unix.CREAD | [...]uint32or64{unix.CS5, unix.CS6, unix.CS7, unix.CS8}[cfg.DataBits-5]
	if cfg.Parity == "even" || cfg.Parity == "odd" {
		settings.Cflag |= unix.PARENB
		settings.Iflag |= unix.INPCK
	}
	if cfg.Parity == "odd" {
		settings.Cflag |= unix.PARODD
	}
	if cfg.StopBits == 2 {
		settings.Cflag |= unix.CSTOPB
	}
	switch cfg.Flow {
	case "rtscts":
		settings.Cflag |= hardwareFlow
	case "xonxoff":
		settings.Iflag |= unix.IXON | unix.IXOFF
		settings.Cc[unix.VSTART], settings.Cc[unix.VSTOP] = 0x11, 0x13
	}
	settings.Cc[unix.VMIN], settings.Cc[unix.VTIME] = 1, 0
}
