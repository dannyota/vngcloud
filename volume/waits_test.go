package volume

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPollRejectsLateSuccessAndBoundsRequests(t *testing.T) {
	now := time.Now()
	timeout := errors.New("poll timeout")
	err := poll(context.Background(), func() time.Time { return now }, contextSleep, func(ctx context.Context) (bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > volumeWaitBound {
			t.Error("poll request has no operation deadline")
		}
		now = now.Add(volumeWaitBound)
		return true, nil
	}, func() error { return timeout })
	if !errors.Is(err, timeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestPollEarlierCallerDeadlineRejectsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := poll(ctx, time.Now, contextSleep, func(context.Context) (bool, error) { cancel(); return true, nil }, func() error { return ErrNotSettled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}
