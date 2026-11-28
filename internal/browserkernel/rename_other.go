//go:build !windows

package browserkernel

import "os"

func rename(from, to string) error { return os.Rename(from, to) }

func removeAll(path string) error { return os.RemoveAll(path) }

func grantSandbox(string) error { return nil }
