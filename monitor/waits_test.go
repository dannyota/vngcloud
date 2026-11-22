package monitor

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMonitorPollRejectsLateSuccess(t *testing.T) {
	clock := time.Unix(0, 0)
	timedOut := errors.New("wait timed out")
	err := poll(context.Background(), func() time.Time { return clock }, contextSleep, time.Second, time.Second,
		func(ctx context.Context) (bool, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("poll request has no deadline")
			}
			clock = clock.Add(2 * time.Second)
			return true, nil
		}, func() error { return timedOut })
	if !errors.Is(err, timedOut) {
		t.Fatalf("late successful read returned %v, want timeout", err)
	}
}

func TestMonitorPollRejectsCanceledSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := poll(ctx, time.Now, contextSleep, time.Second, time.Minute,
		func(context.Context) (bool, error) { cancel(); return true, nil },
		func() error { return errors.New("timeout") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled successful read returned %v", err)
	}
}

func TestMonitorPollClampsSleep(t *testing.T) {
	clock := time.Unix(0, 0)
	var slept time.Duration
	reads := 0
	timedOut := errors.New("timeout")
	err := poll(context.Background(), func() time.Time { return clock },
		func(_ context.Context, delay time.Duration) error {
			slept = delay
			clock = clock.Add(delay)
			return nil
		}, 2*time.Second, time.Second,
		func(context.Context) (bool, error) { reads++; return false, nil },
		func() error { return timedOut })
	if !errors.Is(err, timedOut) || slept != time.Second || reads != 1 {
		t.Fatalf("error = %v, sleep = %s, reads = %d", err, slept, reads)
	}
}

func TestMonitorPollBoundsStalledStep(t *testing.T) {
	timedOut := errors.New("timeout")
	err := poll(context.Background(), time.Now, contextSleep, time.Second, time.Millisecond,
		func(ctx context.Context) (bool, error) { <-ctx.Done(); return true, nil },
		func() error { return timedOut })
	if !errors.Is(err, timedOut) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled step returned %v, want timeout with deadline identity", err)
	}
}
