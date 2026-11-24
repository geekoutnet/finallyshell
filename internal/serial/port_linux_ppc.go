//go:build linux && (ppc64 || ppc64le)

package serial

import (
	"finallyshell/internal/config"
	"golang.org/x/sys/unix"
)

type uint32or64 = uint32

const hardwareFlow = unix.CRTSCTS

func setTermios(fd int, cfg config.SerialConfig) error {
	settings, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	configureTermios(settings, cfg)
	settings.Cflag &^= unix.CBAUD | unix.CIBAUD
	settings.Cflag |= unix.BOTHER
	settings.Ispeed, settings.Ospeed = uint32(cfg.Baud), uint32(cfg.Baud)
	return unix.IoctlSetTermios(fd, unix.TCSETS, settings)
}
