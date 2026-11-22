package storage

import (
	"context"
	"errors"
	"time"
)

type sleepFunc func(ctx context.Context, d time.Duration) error

type clockFunc func() time.Time

func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func poll(ctx context.Context, now clockFunc, sleep sleepFunc, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return onTimeout()
		}
		waitCtx, cancel := context.WithTimeout(ctx, remaining)
		stop, err := step(waitCtx)
		if ctx.Err() != nil {
			cancel()
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if !now().Before(deadline) || waitCtx.Err() != nil {
			timeoutErr := errors.Join(onTimeout(), waitCtx.Err())
			cancel()
			return timeoutErr
		}
		if stop || err != nil {
			cancel()
			return err
		}
		err = sleep(waitCtx, min(interval, deadline.Sub(now())))
		waitErr := waitCtx.Err()
		cancel()
		if ctx.Err() != nil {
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if !now().Before(deadline) || waitErr != nil {
			return errors.Join(onTimeout(), waitErr)
		}
		if err != nil {
			return err
		}
	}
}
