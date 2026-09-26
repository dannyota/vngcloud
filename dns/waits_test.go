package dns

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPollStopsOnFirstStep checks that poll never sleeps, and never calls
// onTimeout, when step reports stop on its first call.
func TestPollStopsOnFirstStep(t *testing.T) {
	sleeps := 0
	err := poll(context.Background(),
		func(ctx context.Context, d time.Duration) error { sleeps++; return nil },
		func(ctx context.Context) (bool, error) { return true, nil },
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if err != nil {
		t.Fatalf("poll() error = %v", err)
	}
	if sleeps != 0 {
		t.Fatalf("sleeps = %d, want 0", sleeps)
	}
}

// TestPollStepCountAndSpacing checks the design's own accounting: a wait
// that never settles calls step 31 times (once at the start of each of the
// 30 pollInterval-wide windows within pollBound, plus the final check
// exactly at the bound) and sleeps pollInterval, never any other duration,
// between them.
func TestPollStepCountAndSpacing(t *testing.T) {
	steps := 0
	var sleptFor []time.Duration
	err := poll(context.Background(),
		func(ctx context.Context, d time.Duration) error { sleptFor = append(sleptFor, d); return nil },
		func(ctx context.Context) (bool, error) { steps++; return false, nil },
		func() error { return ErrZoneBusy },
	)
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("err = %v, want ErrZoneBusy", err)
	}
	const wantSteps = int(pollBound/pollInterval) + 1
	if steps != wantSteps {
		t.Fatalf("steps = %d, want %d", steps, wantSteps)
	}
	if len(sleptFor) != wantSteps-1 {
		t.Fatalf("sleeps = %d, want %d", len(sleptFor), wantSteps-1)
	}
	for _, d := range sleptFor {
		if d != pollInterval {
			t.Fatalf("slept %s, want %s", d, pollInterval)
		}
	}
}

// TestPollCanceledContext checks that a context canceled during a sleep
// ends the wait with that context's own error, never onTimeout or a step
// error.
func TestPollCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := poll(ctx,
		func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() },
		func(ctx context.Context) (bool, error) { calls++; return false, nil },
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Fatalf("step calls = %d, want 1", calls)
	}
}

// TestPollPropagatesReadError checks that a step reporting stop with a plain
// error (an outright read failure) returns that error unwrapped, without
// ever consulting onTimeout.
func TestPollPropagatesReadError(t *testing.T) {
	readErr := errors.New("read failed")
	err := poll(context.Background(),
		func(ctx context.Context, d time.Duration) error { t.Fatal("sleep called"); return nil },
		func(ctx context.Context) (bool, error) { return true, readErr },
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want %v", err, readErr)
	}
}

// TestContextSleepReturnsOnCancel checks the real sleepFunc: it returns the
// context's error as soon as ctx ends, rather than waiting out d.
func TestContextSleepReturnsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := contextSleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("contextSleep() error = %v, want context.Canceled", err)
	}
}

// TestContextSleepZeroDuration checks that a non-positive duration returns
// at once without touching ctx.
func TestContextSleepZeroDuration(t *testing.T) {
	if err := contextSleep(context.Background(), 0); err != nil {
		t.Fatalf("contextSleep(0) error = %v", err)
	}
}

func TestEqualStringSets(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil and empty", nil, []string{}, true},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different contents", []string{"a", "b"}, []string{"a", "c"}, false},
		{"duplicate counts differ", []string{"a", "a"}, []string{"a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := equalStringSets(tc.a, tc.b); got != tc.want {
				t.Fatalf("equalStringSets(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
