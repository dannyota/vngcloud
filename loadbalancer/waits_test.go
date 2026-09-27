package loadbalancer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// pollRecorder is a fake clock and sleepFunc pair for poll: now returns cur,
// which sleep advances by exactly the requested duration, recording every
// sleep's duration in order. It lets a test assert both how long poll
// waited and in what order it read versus slept, without a real wait ever
// elapsing.
type pollRecorder struct {
	cur    time.Time
	sleeps []time.Duration
}

func newPollRecorder() *pollRecorder {
	return &pollRecorder{cur: time.Now()}
}

func (r *pollRecorder) now() time.Time { return r.cur }

func (r *pollRecorder) sleep(ctx context.Context, d time.Duration) error {
	r.sleeps = append(r.sleeps, d)
	r.cur = r.cur.Add(d)
	return ctx.Err()
}

// TestPollSleepsFirstDelayBeforeFirstStep checks poll's firstDelay: a
// positive value sleeps once, before step is ever called, rather than
// reading immediately. This is the fix a child settle wait needs, per
// waitChildSettled's own doc comment: an update or a members replace can
// leave the child's progressStatus unchanged from before the write, so an
// immediate read cannot tell that state apart from one taken before the
// write was even applied.
func TestPollSleepsFirstDelayBeforeFirstStep(t *testing.T) {
	r := newPollRecorder()
	var reads int
	err := poll(context.Background(), r.now, r.sleep, 5*time.Second, time.Second, time.Minute,
		func(context.Context) (bool, error) {
			reads++
			return true, nil
		},
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if err != nil {
		t.Fatalf("poll() error = %v", err)
	}
	if len(r.sleeps) != 1 || r.sleeps[0] != 5*time.Second {
		t.Fatalf("sleeps = %v, want one 5s sleep before the only read", r.sleeps)
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
}

// TestPollZeroFirstDelayReadsImmediately checks that firstDelay 0, used by
// every pre-write busy check, reads current state at once rather than
// waiting out of caution.
func TestPollZeroFirstDelayReadsImmediately(t *testing.T) {
	r := newPollRecorder()
	called := false
	err := poll(context.Background(), r.now, r.sleep, 0, time.Second, time.Minute,
		func(context.Context) (bool, error) {
			called = true
			return true, nil
		},
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if err != nil {
		t.Fatalf("poll() error = %v", err)
	}
	if !called {
		t.Fatal("step was never called")
	}
	if len(r.sleeps) != 0 {
		t.Fatalf("sleeps = %v, want none", r.sleeps)
	}
}

// TestPollSpacing checks that poll sleeps interval between reads, not just
// once: three reads take exactly two sleeps of that interval between them.
func TestPollSpacing(t *testing.T) {
	r := newPollRecorder()
	var steps int
	err := poll(context.Background(), r.now, r.sleep, 0, 2*time.Second, time.Minute,
		func(context.Context) (bool, error) {
			steps++
			return steps == 3, nil
		},
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if err != nil {
		t.Fatalf("poll() error = %v", err)
	}
	if len(r.sleeps) != 2 || r.sleeps[0] != 2*time.Second || r.sleeps[1] != 2*time.Second {
		t.Fatalf("sleeps = %v, want two 2s sleeps between three reads", r.sleeps)
	}
}

// TestPollBoundReachedCallsOnTimeout checks that poll gives up once bound
// has elapsed, by its injected clock, and returns onTimeout's error rather
// than looping forever on a step that never reports stop.
func TestPollBoundReachedCallsOnTimeout(t *testing.T) {
	r := newPollRecorder()
	wantErr := errors.New("bound reached")
	err := poll(context.Background(), r.now, r.sleep, 0, 10*time.Second, 25*time.Second,
		func(context.Context) (bool, error) { return false, nil },
		func() error { return wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	// 25s bound, 10s interval: reads at 0s, 10s, 20s, and 30s all run (each
	// read happens before the deadline check that follows it), and the
	// clock is past the 25s deadline only once it reaches 30s.
	if len(r.sleeps) != 3 {
		t.Fatalf("sleeps = %v, want 3 (10s gaps between four reads at 0s, 10s, 20s, 30s)", r.sleeps)
	}
}

// TestPollCanceledContextStopsWait checks that poll returns ctx's own error
// at once when ctx is already canceled, rather than calling onTimeout.
func TestPollCanceledContextStopsWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newPollRecorder()

	err := poll(ctx, r.now, r.sleep, 0, time.Second, time.Minute,
		func(context.Context) (bool, error) { return false, nil },
		func() error { t.Fatal("onTimeout called"); return nil },
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestCreateLoadBalancerWaitCreatingBillingKeptPending checks that
// CREATING-BILLING, a load balancer status this design names explicitly
// (unlike a listener, pool, or policy, which never carries it), keeps the
// create wait polling rather than treating it as settled or failed.
func TestCreateLoadBalancerWaitCreatingBillingKeptPending(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{lbStatusCreatingBilling, lbStatusCreatingBilling, lbStatusCreated}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.ProgressStatus != lbStatusCreated {
		t.Fatalf("ProgressStatus = %q, want CREATED", out.LoadBalancer.ProgressStatus)
	}
}

// TestCreateLoadBalancerWaitUnknownStatusKeptPending checks that a
// progressStatus this SDK does not recognize keeps the wait polling, rather
// than treating it as settled or failed, since a new status the API adds
// later must not be silently misread either way.
func TestCreateLoadBalancerWaitUnknownStatusKeptPending(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{"SOME-FUTURE-STATUS", lbStatusCreated}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.ProgressStatus != lbStatusCreated {
		t.Fatalf("ProgressStatus = %q, want CREATED", out.LoadBalancer.ProgressStatus)
	}
}

// TestWaitLoadBalancerCreatedCanceledContext checks that a context already
// canceled when the create wait starts ends it with that cancellation
// wrapped in ErrNotSettled, rather than reading at all or reaching the
// bound: GetLoadBalancer fails at once on a canceled ctx, before the request
// ever reaches the server.
func TestWaitLoadBalancerCreatedCanceledContext(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("a canceled context must fail before any request")
	}))
	withInstantSleep(c)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.waitLoadBalancerCreated(ctx, "loadbalancer.CreateLoadBalancer", createLoadBalancerUUID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}
