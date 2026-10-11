//go:build unix

package tokencache

import (
	"os"
	"syscall"
)

// O_NOFOLLOW refuses a symlink swapped in after Lstat. O_NONBLOCK prevents
// an intervening FIFO from blocking before the opened-inode check.
func openServiceAccountFile(path string, flags int) (*os.File, error) {
	if err := checkServiceAccountPath(path, flags&os.O_CREATE != 0); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600) //nolint:gosec // The digest-named cache entry is opened without following symlinks and checked by inode.
	if err != nil {
		return nil, err
	}
	if err := validateServiceAccountFile(path, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
