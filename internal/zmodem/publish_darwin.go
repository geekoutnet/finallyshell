package zmodem

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func moveNoReplace(temp, path string) error {
	err := unix.RenamexNp(temp, path, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) {
		return os.Link(temp, path)
	}
	return err
}
