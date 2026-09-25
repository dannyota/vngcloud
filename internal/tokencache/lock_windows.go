//go:build windows

package tokencache

import (
	"context"
	"errors"
	"syscall"
	"time"
)

// errSharingViolation is the Win32 ERROR_SHARING_VIOLATION code (0x20),
// returned by CreateFile when another handle already holds an incompatible
// share mode on the file. The standard library's syscall package does not
// define this constant for Windows (golang.org/x/sys/windows does, but the
// SDK stays standard-library only).
const errSharingViolation = syscall.Errno(32)

// acquireLock takes an exclusive lock on path by opening it with share mode
// 0 (no other handle, read or write, may be open at the same time),
// retrying with backoff (from minBackoff up to maxBackoff) until ctx ends.
// The returned func closes the handle, which releases the lock; the lock
// file itself is never deleted.
func acquireLock(ctx context.Context, path string, minBackoff, maxBackoff time.Duration) (func(), error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	backoff := minBackoff
	for {
		handle, err := syscall.CreateFile(
			pathPtr,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			0, // share mode 0: exclusive access
			nil,
			syscall.OPEN_ALWAYS,
			syscall.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			return func() { _ = syscall.CloseHandle(handle) }, nil
		}
		if !errors.Is(err, errSharingViolation) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
