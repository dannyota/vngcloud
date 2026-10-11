//go:build windows

package tokencache

import (
	"context"
	"errors"
	"os"
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
	return acquireWindowsLock(ctx, path, minBackoff, maxBackoff, false)
}

func acquireServiceAccountLock(ctx context.Context, path string, minBackoff, maxBackoff time.Duration) (func(), error) {
	return acquireWindowsLock(ctx, path, minBackoff, maxBackoff, true)
}

func acquireWindowsLock(ctx context.Context, path string, minBackoff, maxBackoff time.Duration, strict bool) (func(), error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	flags := uint32(syscall.FILE_ATTRIBUTE_NORMAL)
	if strict {
		flags |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	}
	backoff := minBackoff
	for {
		if strict {
			if err := checkServiceAccountPath(path, true); err != nil {
				return nil, err
			}
		}
		handle, err := syscall.CreateFile(
			pathPtr,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE,
			0, // share mode 0: exclusive access
			nil,
			syscall.OPEN_ALWAYS,
			flags,
			0,
		)
		if err == nil {
			if strict {
				file := os.NewFile(uintptr(handle), path)
				if err := validateServiceAccountFile(path, file); err != nil {
					_ = file.Close()
					return nil, err
				}
				return func() { _ = file.Close() }, nil
			}
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
