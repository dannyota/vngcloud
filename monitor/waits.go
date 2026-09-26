package monitor

import (
	"context"
	"time"
)

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

// poll runs step at once, then again every interval, until step reports
// stop true or bound has elapsed, by now, since poll's first call to step.
// Elapsed time is read from now rather than counted in interval steps, so a
// step that itself takes real time, such as a slow read, counts against
// the bound instead of only the sleeps between steps; a test injects both
// a fake clock and a sleepFunc that returns quickly.
//
// step reports stop true for two different reasons, told apart by its own
// err: a settled outcome (err nil), or an outright read failure (a plain
// error, which poll returns unchanged). onTimeout is called, and its
// result returned, only when the bound elapses with step never reporting
// stop.
//
// This is the same shape as vDNS's own unexported poll, duplicated here
// rather than shared: monitor's log project waits need their own interval
// and bound per operation (vDNS's single pollInterval and pollBound cover
// every one of its own waits), and neither service package imports the
// other for this.
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
