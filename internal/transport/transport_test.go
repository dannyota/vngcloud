package transport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	err := decodeError("Compute.ListServers", 403, []byte(`[{"code":"IAM_PERMISSION_DENIED","message":"IAM denied action"}]`))
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
	err := decodeError("Op", 404, []byte(`{"code":"NOT_FOUND","message":"missing"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Code != "NOT_FOUND" || apiErr.Message != "missing" {
		t.Fatalf("unexpected error fields: %+v", apiErr)
	}
}
