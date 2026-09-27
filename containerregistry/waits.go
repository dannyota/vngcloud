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

// poll runs step at once, then again every interval, until step reports
// stop true or bound has elapsed, by now, since poll's first call to step.
// Elapsed time is read from now rather than counted in interval steps, so a
// step that itself takes real time counts against the bound instead of only
// the sleeps between steps; a test injects both a fake clock and a
// sleepFunc that returns quickly. This is the same shape as network's and
// dns's own unexported poll, duplicated here rather than shared, since none
// of these packages imports another.
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
