package zmodem

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func moveNoReplace(temp, path string) error {
	err := unix.Renameat2(unix.AT_FDCWD, temp, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
		return os.Link(temp, path)
	}
	return err
}
