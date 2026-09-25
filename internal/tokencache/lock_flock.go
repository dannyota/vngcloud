//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package tokencache

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// acquireLock takes an exclusive, non-blocking flock on path, creating it if
// needed, retrying with backoff (from minBackoff up to maxBackoff) until ctx
// ends. The returned func releases the lock; the lock file itself is never
// deleted, since removing it would let a second process create and lock a
// different inode while a third still held the original one.
func acquireLock(ctx context.Context, path string, minBackoff, maxBackoff time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	backoff := minBackoff
	for {
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
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
