package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"danny.vn/vngcloud/internal/transport"
)

type delayedDeadlineContext struct {
	context.Context
	done      chan struct{}
	propagate func()
}

func (c *delayedDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *delayedDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// AfterFunc holds child cancellation until the test releases propagation.
func (c *delayedDeadlineContext) AfterFunc(f func()) func() bool {
	active := true
	c.propagate = func() {
		if active {
			active = false
			f()
		}
	}
	return func() bool {
		wasActive := active
		active = false
		return wasActive
	}
}

func TestSessionDeadlineWaitsForChildCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		contexts := make(chan context.Context, 1)
		parent := &delayedDeadlineContext{
			Context: WithTestContextObserver(context.Background(), func(ctx context.Context) { contexts <- ctx }),
			done:    make(chan struct{}),
		}
		s, err := New(parent, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if len(contexts) != 1 || parent.propagate == nil {
			t.Fatal("missing session context or cancellation callback")
		}
		observed := <-contexts
		released := make(chan struct{})
		go func() {
			<-observed.Done()
			close(released)
		}()
		close(parent.done)
		synctest.Wait()
		if !errors.Is(parent.Err(), context.DeadlineExceeded) || observed.Err() != nil {
			t.Fatal("did not hold cancellation between parent and child")
		}
		select {
		case <-released:
			t.Fatal("released holder before child cancellation")
		default:
		}
		parent.propagate()
		synctest.Wait()
		select {
		case <-released:
		default:
			t.Fatal("did not release holder after child cancellation")
		}
		if err := observed.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("session error = %v", err)
		}
	})
}

func TestSessionTestContextObserver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	contexts := make(chan context.Context, 1)
	calls := 0
	s, err := New(WithTestContextObserver(ctx, func(ctx context.Context) {
		contexts <- ctx
		calls++
	}), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if calls != 1 {
		t.Fatalf("observer calls = %d, want 1", calls)
	}
	observed := <-contexts
	if observed != s.Context() {
		t.Fatal("observer did not receive the session context")
	}
	if observed.Done() == ctx.Done() {
		t.Fatal("observer received the caller context")
	}
	deadline, ok := observed.Deadline()
	if d := time.Until(deadline); !ok || d > 10*time.Minute || d < 9*time.Minute {
		t.Fatalf("session deadline remaining = %v, present = %t", d, ok)
	}
	cancel()
	<-observed.Done()
	if err := observed.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("observed context error = %v", err)
	}
	ordinary, err := New(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	if calls != 1 || ordinary.Context().Err() != nil {
		t.Fatal("observer escaped its invocation")
	}
}

func TestSessionTestClock(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.FixedZone("test", 7*60*60))
	start := now
	var waits []time.Duration
	ctx, cancel := context.WithCancel(WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		waits = append(waits, d)
		now = now.Add(d)
		return nil
	}))
	defer cancel()
	s, err := New(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.CompletedAt(); got != start.UTC() {
		t.Fatalf("CompletedAt = %v, want %v", got, start.UTC())
	}
	for range 2 {
		if err := s.BeforeAttempt(s.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(waits) != 1 || waits[0] != 500*time.Millisecond || s.CompletedAt() != start.Add(500*time.Millisecond).UTC() {
		t.Fatalf("waits = %v, CompletedAt = %v", waits, s.CompletedAt())
	}
	before := time.Now()
	ordinary, err := New(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	if got := ordinary.CompletedAt(); got.Before(before) || got.After(time.Now()) {
		t.Fatalf("test clock escaped its context: %v", got)
	}
	cancel()
	if err := s.BeforeAttempt(s.Context()); !errors.Is(err, context.Canceled) || len(waits) != 1 {
		t.Fatalf("canceled attempt error = %v, waits = %v", err, waits)
	}
}

func TestSessionLimitsAndPacing(t *testing.T) {
	for _, n := range []int{1, 2000} {
		s, err := New(context.Background(), n)
		if err != nil {
			t.Fatal(err)
		}
		if s.MaxRequests() != n {
			t.Fatal(s.MaxRequests())
		}
		s.Close()
	}
	for _, n := range []int{-1, 2001} {
		if _, err := New(context.Background(), n); err == nil {
			t.Fatalf("accepted %d", n)
		}
	}
	s, err := New(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.MaxRequests() != 500 {
		t.Fatal(s.MaxRequests())
	}
	deadline, _ := s.Context().Deadline()
	if d := time.Until(deadline); d > 10*time.Minute || d < 9*time.Minute {
		t.Fatal(d)
	}
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	if err := s.BeforeAttempt(s.Context()); err != nil {
		t.Fatal(err)
	}
	start := now
	if err := s.BeforeAttempt(s.Context()); err != nil {
		t.Fatal(err)
	}
	if now.Sub(start) != 500*time.Millisecond {
		t.Fatal(now.Sub(start))
	}
	if err := s.AddRows(10000); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRows(1); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if err := s.Preflight(500); err != nil {
		t.Fatal(err)
	}
	if err := s.Preflight(501); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}

type fakeExchange struct {
	status int
	calls  int
	err    error
	body   string
}

func (f *fakeExchange) DoDiscoveryJSONStatus(ctx context.Context, req transport.Request, out any, before func(context.Context) error) (int, error) {
	f.calls++
	if err := before(ctx); err != nil {
		return 0, err
	}
	if f.err != nil {
		status := f.status
		if status == 0 {
			status = 500
		}
		return status, f.err
	}
	return 200, json.Unmarshal([]byte(f.body), out)
}

func TestSessionBudgetsMemoizationAndFreshInvocations(t *testing.T) {
	s, _ := New(context.Background(), 1)
	defer s.Close()
	f := &fakeExchange{body: `{"items":[]}`}
	req := transport.Request{Method: "GET", URL: "https://example.test/v1/p-1/zones"}
	var out any
	if _, err := s.DoJSON(f, req, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DoJSON(f, req, &out); err != nil || f.calls != 1 || s.Requests() != 1 {
		t.Fatalf("calls=%d requests=%d err=%v", f.calls, s.Requests(), err)
	}
	req.URL = "https://example.test/v1/p-1/volume_type_zones"
	if _, err := s.DoJSON(f, req, &out); !errors.Is(err, ErrLimit) || f.calls != 1 {
		t.Fatalf("calls=%d err=%v", f.calls, err)
	}
	fresh, _ := New(context.Background(), 1)
	defer fresh.Close()
	if _, err := fresh.DoJSON(f, req, &out); err != nil || f.calls != 2 {
		t.Fatalf("calls=%d err=%v", f.calls, err)
	}
}

func TestDiscoveryAllowlist(t *testing.T) {
	for _, req := range []transport.Request{
		{Method: "DELETE", URL: "https://example.test/v1/p-1/zones"},
		{Method: "GET", URL: "https://example.test/internal/v1/billing/project_types/1/details"},
		{Method: "GET", URL: "https://example.test/v1/p-1/servers"},
		{Method: "POST", URL: "https://example.test/v1/price", Body: map[string]any{"action": "resize", "resourceType": "volume", "resourceInfo": map[string]any{"size": 1}}},
		{Method: "POST", URL: "https://example.test/v1/price", Body: map[string]any{"action": "create", "resourceType": "volume", "resourceInfo": map[string]any{"period": 12}}},
		{Method: "POST", URL: "https://example.test/v1/price", Body: map[string]any{"action": "create", "resourceType": "server", "resourceInfo": map[string]any{"password": "secret"}}},
	} {
		if err := Allowed(req); err == nil {
			t.Fatalf("accepted %s %s", req.Method, req.URL)
		}
	}
}

func TestSessionCancellationAndFailedMemo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := New(ctx, 3)
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	s.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	original := errors.New("quote failed")
	f := &fakeExchange{err: original}
	req := transport.Request{Method: "GET", URL: "https://example.test/v1/p-1/zones"}
	for range 2 {
		if _, err := s.DoJSON(f, req, nil); !errors.Is(err, original) {
			t.Fatal(err)
		}
	}
	if f.calls != 2 {
		t.Fatal("memoized failed response")
	}
	cancel()
	if _, err := s.DoJSON(f, req, nil); !errors.Is(err, context.Canceled) || f.calls != 2 {
		t.Fatalf("calls=%d err=%v", f.calls, err)
	}
}

func (*fakeExchange) WrapDiscoveryError(err error) error { return err }

func TestStorageRegionHeadersAndQuoteBodies(t *testing.T) {
	// Quote bodies are synthetic variants of the approved wire contracts.
	cases := []struct {
		path, kind string
		info       map[string]any
	}{
		{"/v1/price", "volume", map[string]any{"zoneId": "zone-1", "volumeTypeId": "type-1", "size": 1, "period": 1, "isPoc": false}},
		{"/v1/price", "server", map[string]any{"zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1", "rootDiskTypeId": "type-1", "rootDiskSize": 20, "period": 1, "isPoc": false, "encryptionVolume": false}},
		{"/v1/price", "public-vip", map[string]any{"type": "public", "isBuyMorePo": false, "isPoc": false}},
		{"/billing-api/v2/price", "object_storage", map[string]any{"projectType": 1, "purchaseTypeId": 4, "quota": 30}},
	}
	for _, tc := range cases {
		req := transport.Request{Method: "POST", URL: "https://example.test" + tc.path, Body: map[string]any{"resourceType": tc.kind, "action": "create", "resourceInfo": tc.info}}
		if tc.kind == "object_storage" {
			req.Headers = map[string]string{"region": "region-1", "region_id": "region-1"}
		}
		if err := Allowed(req); err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
	}
	req := transport.Request{Method: "GET", URL: "https://example.test/internal/v1/billing/project_types", Headers: map[string]string{"region": "region-1", "region_id": "region-1"}}
	if err := Allowed(req); err != nil {
		t.Fatal(err)
	}
	req.Headers["Cookie"] = "session=canary"
	if err := Allowed(req); err == nil {
		t.Fatal("accepted cookie header")
	}
}

type transportExchange struct{ *transport.Client }

func (*transportExchange) WrapDiscoveryError(err error) error { return err }

type sessionTokenSource struct{ calls int }

func (s *sessionTokenSource) Token(context.Context) (transport.Token, error) {
	s.calls++
	return transport.Token{AccessToken: "safe-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*sessionTokenSource) Invalidate(string) {}

func TestSessionPacesRetriesAndRefreshWithinOneLogicalRequest(t *testing.T) {
	statuses := []int{503, 401, 503, 200}
	var attempts atomic.Int32
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statuses[attempts.Add(1)-1])
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	source := &sessionTokenSource{}
	c := &transportExchange{transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: source, RetryCount: 1, RetryInterval: time.Nanosecond})}
	s, _ := New(context.Background(), 1)
	defer s.Close()
	// Track starts independently of clock reads inside the pacing calculation.
	var physical []time.Time
	s.now = func() time.Time { return now }
	s.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	// The transport calls BeforeAttempt once per physical attempt.
	exchange := &recordingExchange{Exchanger: c, record: func() { physical = append(physical, now) }}
	var out any
	if _, err := s.DoJSON(exchange, transport.Request{Method: "GET", URL: srv.URL + "/v1/p-1/zones"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(physical) != 4 || s.Requests() != 1 || source.calls != 2 {
		t.Fatalf("physical=%d logical=%d tokens=%d", len(physical), s.Requests(), source.calls)
	}
	for i := 1; i < len(physical); i++ {
		if physical[i].Sub(physical[i-1]) < 500*time.Millisecond {
			t.Fatal("retry or refresh bypassed pacing")
		}
	}
}

type recordingExchange struct {
	Exchanger
	record func()
}

func (c *recordingExchange) DoDiscoveryJSONStatus(ctx context.Context, req transport.Request, out any, before func(context.Context) error) (int, error) {
	return c.Exchanger.DoDiscoveryJSONStatus(ctx, req, out, func(ctx context.Context) error {
		if err := before(ctx); err != nil {
			return err
		}
		c.record()
		return nil
	})
}

func TestSessionDoesNotMemoizeRejectedSchema(t *testing.T) {
	s, _ := New(context.Background(), 2)
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	s.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	f := &fakeExchange{body: `{"optimumPrice":0}`}
	req := transport.Request{Method: "GET", URL: "https://example.test/v1/p-1/zones"}
	original := errors.New("schema refused")
	validate := func(int, json.RawMessage) error { return original }
	for range 2 {
		if _, err := s.DoJSON(f, req, nil, validate); !errors.Is(err, original) {
			t.Fatal(err)
		}
	}
	if f.calls != 2 || s.Requests() != 2 {
		t.Fatalf("calls=%d requests=%d", f.calls, s.Requests())
	}
}

func TestSessionStopsAfterAuthenticationOrThrottleFailure(t *testing.T) {
	for _, status := range []int{401, 429} {
		s, _ := New(context.Background(), 2)
		now := time.Now()
		s.now = func() time.Time { return now }
		s.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
		original := &transport.APIError{StatusCode: status}
		f := &fakeExchange{err: original, status: status}
		req := transport.Request{Method: "GET", URL: "https://example.test/v1/p-1/zones"}
		for range 2 {
			if _, err := s.DoJSON(f, req, nil); !errors.Is(err, original) {
				t.Fatalf("lost original error: %v", err)
			}
		}
		if f.calls != 1 || s.Requests() != 1 {
			t.Fatalf("status=%d calls=%d requests=%d", status, f.calls, s.Requests())
		}
		s.Close()
	}
}

type sessionRotatingSource struct{ calls atomic.Int32 }

func (s *sessionRotatingSource) Token(context.Context) (transport.Token, error) {
	token := "old-token"
	if s.calls.Add(1) > 1 {
		token = "new-token"
	}
	return transport.Token{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*sessionRotatingSource) Invalidate(string) {}

func TestSessionRetainsSentCredentialsAcrossReads(t *testing.T) {
	source := &sessionRotatingSource{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer old-token" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		if r.URL.Path == "/v1/p-1/zones" {
			_, _ = w.Write([]byte(`{"name":"old\u002dtoken"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &transportExchange{transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: source})}
	s, _ := New(context.Background(), 2)
	defer s.Close()
	s.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if _, err := s.DoJSON(c, transport.Request{Method: "GET", URL: srv.URL + "/v1/projects"}, nil); err != nil {
		t.Fatal(err)
	}
	var out json.RawMessage
	_, err := s.DoJSON(c, transport.Request{Method: "GET", URL: srv.URL + "/v1/p-1/zones"}, &out)
	var api *transport.APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || len(out) != 0 {
		t.Fatalf("err=%v", err)
	}
	fresh, _ := New(context.Background(), 1)
	defer fresh.Close()
	if _, err := fresh.DoJSON(c, transport.Request{Method: "GET", URL: srv.URL + "/v1/p-1/zones"}, &out); err != nil {
		t.Fatalf("credentials crossed invocations: %v", err)
	}
}
