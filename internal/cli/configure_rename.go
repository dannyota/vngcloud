package cli

import (
	"context"
	"time"
)

func renameWithRetry(ctx context.Context, old, new string, rename func(string, string) error, retryable func(error) bool) error {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	backoff := 10 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := rename(old, new)
		if err == nil || !retryable(err) {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return err
		case <-timer.C:
		}
		if backoff < 160*time.Millisecond {
			backoff *= 2
		}
	}
}
