//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package tokencache

import (
	"context"
	"sync"
	"time"
)

// This platform has no file-locking syscall wired up, so the lock is
// process-local only: a sync.Mutex per lock path. It still serializes
// Cache.Get calls that share a path within one process, but two separate
// processes sharing a cache directory are not protected from logging in at
// the same time.
var (
	otherLocksMu sync.Mutex
	otherLocks   = map[string]*sync.Mutex{}
)

func lockFor(path string) *sync.Mutex {
	otherLocksMu.Lock()
	defer otherLocksMu.Unlock()
	m, ok := otherLocks[path]
	if !ok {
		m = &sync.Mutex{}
		otherLocks[path] = m
	}
	return m
}

func acquireLock(ctx context.Context, path string, minBackoff, maxBackoff time.Duration) (func(), error) {
	m := lockFor(path)
	backoff := minBackoff
	for {
		if m.TryLock() {
			return m.Unlock, nil
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
