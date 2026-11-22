//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func openConfigLock(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("config lock is not a regular file")
	}
	return f, nil
}

func acquireConfigLock(ctx context.Context, path string) (func(), error) {
	f, err := openConfigLock(ctx, path)
	if err != nil {
		return nil, err
	}
	backoff := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 160*time.Millisecond {
			backoff *= 2
		}
	}
}
