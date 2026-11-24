//go:build !windows && !linux && !darwin

package zmodem

import "os"

func moveNoReplace(temp, path string) error { return os.Link(temp, path) }
