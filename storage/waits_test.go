package storage

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
)

func TestPollRejectsLateSuccessAndBoundsRequests(t *testing.T) {
	now := time.Now()
	bound := time.Second
	timeout := errors.New("poll timeout")
	err := poll(context.Background(), func() time.Time { return now }, contextSleep, time.Millisecond, bound, func(ctx context.Context) (bool, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > bound {
			t.Error("poll request has no operation deadline")
		}
		now = now.Add(bound)
		return true, nil
	}, func() error { return timeout })
	if !errors.Is(err, timeout) {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestPollBoundsStalledRequestAndSleep(t *testing.T) {
	for _, stallRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "sleep", true: "read"}[stallRead], func(t *testing.T) {
			timeout := errors.New("poll timeout")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := poll(ctx, time.Now, func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }, time.Second, 20*time.Millisecond, func(ctx context.Context) (bool, error) {
				if stallRead {
					<-ctx.Done()
					return true, nil
				}
				return false, nil
			}, func() error { return timeout })
			if !errors.Is(err, timeout) {
				t.Fatalf("error = %v, want operation timeout", err)
			}
		})
	}
}

func TestDiscoveryRegionWaiterCancelsIndependently(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		testutil.WriteFixture(t, w, fixtures+"discovery_regions.json")
	}))
	s, err := discovery.New(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	holder := make(chan error, 1)
	go func() { _, _, err := c.discoveryRegion(s, "HCM04"); holder <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	waiterSession, err := discovery.New(ctx, 10)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer waiterSession.Close()
	waiter := make(chan error, 1)
	go func() { _, _, err := c.discoveryRegion(waiterSession, "HCM04"); waiter <- err }()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Error("canceled discovery waiter blocked behind holder")
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatalf("holder error = %v", err)
	}
}
