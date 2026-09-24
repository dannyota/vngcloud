package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testTokenSource struct {
	count atomic.Int64
}

func (s *testTokenSource) Token(context.Context) (Token, error) {
	n := s.count.Add(1)
	return Token{
		AccessToken: fmt.Sprintf("token-%d", n),
		ExpiresAt:   time.Now().Add(time.Hour),
	}, nil
}

func TestDoJSONRefreshesOn401(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-2" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	source := &testTokenSource{}
	client := New(Config{
		HTTPClient:  server.Client(),
		TokenSource: source,
	})

	var out struct {
		OK bool `json:"ok"`
	}
	if err := client.DoJSON(context.Background(), Request{
		Operation: "test",
		Method:    http.MethodGet,
		URL:       server.URL,
		OK:        []int{200},
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if !out.OK {
		t.Fatal("response was not decoded")
	}
	if source.count.Load() != 2 {
		t.Fatalf("token refresh count = %d", source.count.Load())
	}
}

func TestDoJSONCapturesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"id":"one"}]}`))
	}))
	defer server.Close()

	var captured Capture
	client := New(Config{
		HTTPClient: server.Client(),
		Capture: func(c Capture) {
			captured = c
			captured.Body = append([]byte(nil), c.Body...)
			c.Body[0] = '['
		},
	})

	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := client.DoJSON(context.Background(), Request{
		Operation: "Inventory.List",
		Method:    http.MethodGet,
		URL:       server.URL + "/resources",
		OK:        []int{200},
		SkipAuth:  true,
	}, &out); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}

	if captured.Operation != "Inventory.List" {
		t.Fatalf("captured operation = %q", captured.Operation)
	}
	if captured.Method != http.MethodGet {
		t.Fatalf("captured method = %q", captured.Method)
	}
	if captured.URL != server.URL+"/resources" {
		t.Fatalf("captured URL = %q", captured.URL)
	}
	if captured.StatusCode != http.StatusOK {
		t.Fatalf("captured status = %d", captured.StatusCode)
	}
	if string(captured.Body) != `{"items":[{"id":"one"}]}` {
		t.Fatalf("captured body = %s", captured.Body)
	}

	if out.Items[0].ID != "one" {
		t.Fatal("captured body mutation changed decoded output")
	}
}

func TestDoJSONContextCancelDuringRetryWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: 10 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := c.DoJSON(ctx, Request{Operation: "Op", URL: server.URL, SkipAuth: true}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("retry wait ignored context cancellation (took %s)", time.Since(start))
	}
}

func TestBackoffBounds(t *testing.T) {
	c := New(Config{RetryInterval: time.Second})
	for attempt := 0; attempt < 10; attempt++ {
		d := c.backoff(attempt, 0)
		if d <= 0 || d > 30*time.Second {
			t.Fatalf("attempt %d: backoff %s out of bounds", attempt, d)
		}
	}
	if d := c.backoff(0, 5*time.Second); d != 5*time.Second {
		t.Fatalf("expected Retry-After to win, got %s", d)
	}
	if d := c.backoff(0, 10*time.Minute); d != 30*time.Second {
		t.Fatalf("expected Retry-After to be capped, got %s", d)
	}
}

func TestRetryAfterHint(t *testing.T) {
	h := http.Header{}
	if retryAfterHint(h) != 0 {
		t.Fatal("expected 0 for missing header")
	}
	h.Set("Retry-After", "7")
	if retryAfterHint(h) != 7*time.Second {
		t.Fatalf("unexpected hint: %s", retryAfterHint(h))
	}
	h.Set("Retry-After", "garbage")
	if retryAfterHint(h) != 0 {
		t.Fatal("expected 0 for unparseable header")
	}
}

func TestDecodeErrorArrayBody(t *testing.T) {
	err := decodeError(Request{Operation: "Compute.ListServers", Method: http.MethodGet}, 403, []byte(`[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "IAM_PERMISSION_DENIED" {
		t.Fatalf("unexpected code: %q", apiErr.Code)
	}
	if apiErr.Message != "IAM denied action" {
		t.Fatalf("unexpected message: %q", apiErr.Message)
	}
}

func TestDecodeErrorObjectBody(t *testing.T) {
	err := decodeError(Request{Operation: "Op", Method: http.MethodGet}, 404, []byte(`{"code":"NOT_FOUND","message":"missing"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "NOT_FOUND" || apiErr.Message != "missing" {
		t.Fatalf("unexpected error fields: %+v", apiErr)
	}
}

// stubRoundTripper fails the first RoundTrip with failErr, then returns a
// 200 with an empty JSON body, so a test can force one transport error
// before the real request would succeed.
type stubRoundTripper struct {
	calls   atomic.Int64
	failErr error
}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	if s.calls.Add(1) == 1 {
		return nil, s.failErr
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Header:     make(http.Header),
	}, nil
}

func TestPostNotRetriedAfter502(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		SkipAuth:  true,
	}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadGateway)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestIdempotentPostRetried(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation:  "Op",
		Method:     http.MethodPost,
		URL:        server.URL,
		SkipAuth:   true,
		Idempotent: true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestPostRetriedAfter429(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestPutRetriedAfter502(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestPostRetriedAfterDialError(t *testing.T) {
	rt := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
	if rt.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", rt.calls.Load())
	}

	rt2 := &stubRoundTripper{failErr: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	c2 := New(Config{HTTPClient: &http.Client{Transport: rt2}, RetryCount: 3, RetryInterval: time.Millisecond})
	err = c2.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPost,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{200},
		SkipAuth:  true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if rt2.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", rt2.calls.Load())
	}
}

func TestRetryableMatchesRetryRule(t *testing.T) {
	server502 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server502.Close()
	server429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server429.Close()

	cases := []struct {
		name   string
		client *Client
		method string
		url    string
		want   bool
	}{
		{"POST 502", New(Config{HTTPClient: server502.Client()}), http.MethodPost, server502.URL, false},
		{"GET 502", New(Config{HTTPClient: server502.Client()}), http.MethodGet, server502.URL, true},
		{"POST 429", New(Config{HTTPClient: server429.Client()}), http.MethodPost, server429.URL, true},
	}
	for _, tc := range cases {
		err := tc.client.DoJSON(context.Background(), Request{
			Operation: "Op",
			Method:    tc.method,
			URL:       tc.url,
			SkipAuth:  true,
		}, nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: expected *APIError, got %T", tc.name, err)
		}
		if apiErr.Retryable != tc.want {
			t.Fatalf("%s: Retryable = %v, want %v", tc.name, apiErr.Retryable, tc.want)
		}
	}

	rtRead := &stubRoundTripper{failErr: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	cRead := New(Config{HTTPClient: &http.Client{Transport: rtRead}})
	err := cRead.DoJSON(context.Background(), Request{Operation: "Op", Method: http.MethodPost, URL: "http://vngcloud.invalid/", SkipAuth: true}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("POST read error: expected *APIError, got %T", err)
	}
	if apiErr.Retryable {
		t.Fatal("POST read error: Retryable = true, want false")
	}

	rtDial := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	cDial := New(Config{HTTPClient: &http.Client{Transport: rtDial}})
	err = cDial.DoJSON(context.Background(), Request{Operation: "Op", Method: http.MethodPost, URL: "http://vngcloud.invalid/", SkipAuth: true}, nil)
	if !errors.As(err, &apiErr) {
		t.Fatalf("POST dial error: expected *APIError, got %T", err)
	}
	if !apiErr.Retryable {
		t.Fatal("POST dial error: Retryable = false, want true")
	}
}

func TestDecodeErrorEnvelopeCode(t *testing.T) {
	req := Request{Operation: "Op", Method: http.MethodGet}
	cases := []struct {
		name string
		body string
		want string
	}{
		{"numeric code", `{"code":400,"message":"m"}`, "400"},
		{"null code", `{"code":null,"message":"m"}`, ""},
		{"string code", `{"code":"X","message":"m"}`, "X"},
	}
	for _, tc := range cases {
		err := decodeError(req, 400, []byte(tc.body))
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("%s: expected *APIError, got %T", tc.name, err)
		}
		if apiErr.Code != tc.want {
			t.Fatalf("%s: Code = %q, want %q", tc.name, apiErr.Code, tc.want)
		}
	}

	err := decodeError(req, 403, []byte(`[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("array form: expected *APIError, got %T", err)
	}
	if apiErr.Code != "IAM_PERMISSION_DENIED" {
		t.Fatalf("array form: Code = %q", apiErr.Code)
	}
}
