//go:build !windows

package sshx

import "os"

func protectKeyFile(file *os.File) error { return file.Chmod(0o600) }
