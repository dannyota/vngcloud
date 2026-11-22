package loadbalancer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestPollRejectsLateSuccess(t *testing.T) {
	r := newPollRecorder()
	timedOut := errors.New("wait expired")
	err := poll(context.Background(), r.now, r.sleep, 0, time.Second, time.Minute,
		func(context.Context) (bool, error) {
			r.cur = r.cur.Add(time.Minute)
			return true, nil
		}, func() error { return timedOut })
	if !errors.Is(err, timedOut) {
		t.Fatalf("error = %v, want timeout after late success", err)
	}
}

func TestPollRejectsCanceledSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newPollRecorder()
	err := poll(ctx, r.now, r.sleep, 0, time.Second, time.Minute,
		func(context.Context) (bool, error) {
			cancel()
			return true, nil
		}, func() error { return ErrNotSettled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestPollClampsSleepAndStopsBeforeExpiredRead(t *testing.T) {
	for _, firstDelay := range []time.Duration{0, time.Minute} {
		t.Run(firstDelay.String(), func(t *testing.T) {
			r := newPollRecorder()
			start := r.now()
			reads := 0
			err := poll(context.Background(), r.now, r.sleep, firstDelay, time.Minute, 5*time.Second,
				func(context.Context) (bool, error) { reads++; return false, nil },
				func() error { return ErrNotSettled })
			wantReads := 1
			if firstDelay > 0 {
				wantReads = 0
			}
			if !errors.Is(err, ErrNotSettled) || reads != wantReads || r.now().Sub(start) != 5*time.Second {
				t.Fatalf("error = %v, reads = %d, elapsed = %v", err, reads, r.now().Sub(start))
			}
		})
	}
}

func TestPollBoundsStalledRequest(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var requestErr error
	err := poll(ctx, time.Now, contextSleep, 0, time.Second, 20*time.Millisecond,
		func(readCtx context.Context) (bool, error) {
			deadline, ok := readCtx.Deadline()
			if !ok || time.Until(deadline) > 100*time.Millisecond {
				t.Error("poll request did not receive the operation deadline")
				return true, nil
			}
			_, requestErr = c.GetLoadBalancer(readCtx, &GetLoadBalancerInput{LoadBalancerID: "lb-1"})
			return true, requestErr
		}, func() error { return ErrNotSettled })
	if !errors.Is(err, ErrNotSettled) || !errors.Is(requestErr, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("poll error = %v, request error = %v, caller error = %v", err, requestErr, ctx.Err())
	}
}

func TestPollRequestDeadlineUsesRemainingBound(t *testing.T) {
	r := newPollRecorder()
	err := poll(context.Background(), r.now, r.sleep, 9*time.Second, time.Second, 10*time.Second,
		func(ctx context.Context) (bool, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > time.Second {
				t.Error("request context exceeds the remaining operation bound")
			}
			return true, nil
		}, func() error { return ErrNotSettled })
	if err != nil {
		t.Fatal(err)
	}
}
