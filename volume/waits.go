package volume

import (
	"context"
	"strings"
	"time"
)

// isVolumeError reports whether status is the volume ERROR status, from the
// design's status table.
func isVolumeError(status string) bool {
	return strings.EqualFold(status, "ERROR")
}

// isVolumeDeleted reports whether status is the volume DELETED status: a
// delete wait settles on either this or a 404 from GetVolume.
func isVolumeDeleted(status string) bool {
	return strings.EqualFold(status, "DELETED")
}

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the waits below
// never really elapse.
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
// the sleeps between steps. This is the same shape as vDNS's and network's
// own unexported poll, duplicated here rather than shared, since neither
// package imports this one and each service's writes need their own
// interval and bound per operation.
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

// Poll timing per the design's wait table.
const (
	volumePollInterval = 2 * time.Second
	volumeCreateBound  = 5 * time.Minute
	volumeDeleteBound  = 5 * time.Minute
)
