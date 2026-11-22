package pricing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
)

type estimateDelayedDeadlineContext struct {
	context.Context
	done, checked chan struct{}
	propagate     chan func()
	checkOnce     sync.Once
}

func (c *estimateDelayedDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *estimateDelayedDeadlineContext) Err() error {
	select {
	case <-c.done:
		c.checkOnce.Do(func() { close(c.checked) })
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *estimateDelayedDeadlineContext) AfterFunc(f func()) func() bool {
	var once sync.Once
	c.propagate <- func() { once.Do(f) }
	return func() bool {
		stopped := false
		once.Do(func() { stopped = true })
		return stopped
	}
}

func TestEstimateProjectWaiterDeadlineBeforeSessionCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var projects, quotes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			projects.Add(1)
			close(entered)
			<-release
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3"}]}`))
		case "/v1/price":
			quotes.Add(1)
			_, _ = w.Write([]byte(`{"optimumPrice":10}`))
		default:
			t.Errorf("unexpected route %s", r.URL)
		}
	}))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithProjectID("")))
	in := &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}}
	holder := make(chan error, 1)
	go func() { _, err := client.Estimate(estimateContext(), in); holder <- err }()
	<-entered
	contexts := make(chan context.Context, 1)
	ctx := &estimateDelayedDeadlineContext{
		Context: discovery.WithTestContextObserver(estimateContext(), func(ctx context.Context) { contexts <- ctx }),
		done:    make(chan struct{}), checked: make(chan struct{}), propagate: make(chan func(), 1),
	}
	waiter := make(chan error, 1)
	go func() { _, err := client.Estimate(ctx, in); waiter <- err }()
	sessionCtx := <-contexts
	propagate := <-ctx.propagate
	defer propagate()
	close(ctx.done)
	select {
	case <-sessionCtx.Done():
		t.Fatal("session canceled before propagation")
	default:
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waiter:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter %v before session cancellation", err)
		}
	case <-ctx.checked:
		propagate()
		if err := <-waiter; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter %v after session cancellation", err)
		}
	}
	if projects.Load() != 1 || quotes.Load() != 1 {
		t.Fatalf("expired waiter scheduled work: projects %d quotes %d", projects.Load(), quotes.Load())
	}
}

func TestEstimateProjectDeadlineBeforeStart(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer srv.Close()
	client := New(estimateHTTPConfig(t, srv.URL, vngcloud.WithProjectID("")))
	ctx, cancel := context.WithDeadline(context.Background(), time.Time{})
	defer cancel()
	out, err := client.Estimate(ctx, &EstimateInput{PublicIPs: []EstimatePublicIP{{Key: "ip", Kind: "public-vip", Type: "public", Quantity: 1}}})
	if out != nil || !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 0 {
		t.Fatalf("output %v error %v requests %d", out, err, requests.Load())
	}
}
