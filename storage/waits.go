package storage

import (
	"context"
	"time"
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the delete wait
// never really elapses.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it to bound a wait by elapsed
// wall time, so a slow read counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

// contextSleep is the real sleepFunc.
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

// poll runs step at once, then again every interval, until step reports stop
// true or bound has elapsed, by now, since poll began. step reports stop true
// for a settled outcome (err nil) or a final failure (err returned
// unchanged). onTimeout is called, and its result returned, only when the
// bound elapses first.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(bound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, interval); err != nil {
			return err
		}
	}
}
