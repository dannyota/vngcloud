//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func openConfigLock(ctx context.Context, path string) (*os.File, error) {
	backoff := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.Errno(32)) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 160*time.Millisecond {
			backoff *= 2
		}
	}
}

func acquireConfigLock(ctx context.Context, path string) (func(), error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	backoff := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			return func() { _ = syscall.CloseHandle(h) }, nil
		}
		if !errors.Is(err, syscall.Errno(32)) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 160*time.Millisecond {
			backoff *= 2
		}
	}
}
