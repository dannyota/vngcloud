package monitor

import (
	"context"
	"errors"
	"time"
)

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

func poll(ctx context.Context, now clockFunc, sleep sleepFunc, interval, bound time.Duration, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error { //nolint:unparam // interval is 2s for every current caller, but each wait's own design states its own cadence, so it stays a parameter rather than a shared constant.
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	deadline := now().Add(bound)
	checkDeadline := func() error {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return errors.Join(onTimeout(), err)
			}
			return err
		}
		if !now().Before(deadline) {
			return errors.Join(onTimeout(), context.DeadlineExceeded)
		}
		return nil
	}
	for {
		if err := checkDeadline(); err != nil {
			return err
		}
		stop, err := step(ctx)
		if cause := checkDeadline(); cause != nil {
			return cause
		}
		if stop {
			return err
		}
		if err := sleep(ctx, min(interval, deadline.Sub(now()))); err != nil {
			return err
		}
	}
}
