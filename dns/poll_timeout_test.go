package dns

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollStalledReadTimeoutIdentity(t *testing.T) {
	for _, kind := range []string{"operation", "caller cancellation", "caller deadline"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			cancel := func() {}
			bound := 25 * time.Millisecond
			want := ErrZoneBusy
			switch kind {
			case "caller cancellation":
				ctx, cancel = context.WithCancel(ctx)
				bound = time.Second
				want = context.Canceled
			case "caller deadline":
				ctx, cancel = context.WithTimeout(ctx, 25*time.Millisecond)
				bound = time.Second
				want = context.DeadlineExceeded
			}
			defer cancel()
			var writes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if kind == "caller cancellation" {
					cancel()
				}
				<-r.Context().Done()
			}))
			err := pollWithBound(ctx, bound, time.Now, contextSleep,
				func(ctx context.Context) (bool, error) {
					_, err := c.getHostedZone(ctx, "dns.UpdateHostedZone", "zone-1")
					return true, err
				}, func() error { return ErrZoneBusy })
			if !errors.Is(err, want) || writes.Load() != 0 {
				t.Fatalf("error=%v, want %v; mutations=%d", err, want, writes.Load())
			}
			if kind != "operation" && errors.Is(err, ErrZoneBusy) {
				t.Fatalf("caller cancellation became operation timeout: %v", err)
			}
		})
	}
}

func TestPollSleepOperationTimeout(t *testing.T) {
	err := pollWithBound(context.Background(), time.Millisecond, time.Now, contextSleep,
		func(context.Context) (bool, error) { return false, nil },
		func() error { return ErrZoneBusy })
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("error=%v, want ErrZoneBusy", err)
	}
}

func TestWaitZoneReadyTimeoutBeforeFirstSuccessfulRead(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	start := time.Unix(0, 0)
	calls := 0
	c.now = func() time.Time {
		calls++
		if calls > 2 {
			return start.Add(pollBound)
		}
		return start
	}
	_, err := c.waitZoneReady(context.Background(), "dns.UpdateHostedZone", "zone-1")
	if !errors.Is(err, ErrZoneBusy) {
		t.Fatalf("error=%v, want ErrZoneBusy", err)
	}
}
