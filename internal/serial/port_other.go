//go:build !windows && !linux && !darwin

package serial

import (
	"io"

	"finallyshell/internal/config"
)

func nativePorts() ([]string, error)                                   { return nil, ErrUnsupported }
func openPort(string, config.SerialConfig) (io.ReadWriteCloser, error) { return nil, ErrUnsupported }
