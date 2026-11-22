package containerregistry

import (
	"context"
	"time"
)

// repoPollInterval and repoPollBound set CreateRepository and
// DeleteRepository's post-write waits, per the design's Waits table: both
// poll every 2 seconds for up to 60 seconds of elapsed time.
const (
	repoPollInterval = 2 * time.Second
	repoPollBound    = 60 * time.Second
)

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the real
// 2-second and 60-second bounds never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
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

func poll(ctx context.Context, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	ctx, cancel := context.WithTimeout(ctx, repoPollBound)
	defer cancel()
	deadline := now().Add(repoPollBound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		stop, err := step(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if stop {
			return err
		}
		if err := sleep(ctx, min(repoPollInterval, deadline.Sub(now()))); err != nil {
			return err
		}
	}
}
