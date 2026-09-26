package transport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestOnceNoRetryAfter429 checks ADR 0003 rule 3: a request sent with Once
// is never retried, even after a 429, which every other idempotent request
// (PUT included) would otherwise retry. Retryable stays true, since a 429
// still means the server never acted on it.
func TestOnceNoRetryAfter429(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{204},
		SkipAuth:  true,
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want true for a 429")
	}
}

// TestOnceNoRetryAfter502 checks that a 5xx on a Once request is sent only
// once, unlike the ordinary PUT retry a non-Once request would get, and that
// Retryable is false: a 5xx may have reached a handler on the one attempt
// Once allows, so ADR 0003 never marks it as provably safe to resend.
func TestOnceNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{204},
		SkipAuth:  true,
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for a 502 on a Once request")
	}
}

// TestOnceNoRetryAfterNetworkError checks that a Once PUT sends exactly one
// attempt after a network error that is not a failed dial (the connection
// was established, so the server may have acted), and that Retryable is
// false: PUT is normally idempotent and would retry on this error, but Once
// narrows retryability to a failed dial alone.
func TestOnceNoRetryAfterNetworkError(t *testing.T) {
	rt := &stubRoundTripper{failErr: &net.OpError{Op: "read", Err: errors.New("connection reset")}}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{204},
		SkipAuth:  true,
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", rt.calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true, want false for a non-dial network error on a Once request")
	}
}

// TestOnceNoRetryAfterFailedDial checks that a Once PUT sends exactly one
// attempt after a failed dial, and that Retryable stays true: the server
// never received the request, so rerunning the whole operation is safe.
func TestOnceNoRetryAfterFailedDial(t *testing.T) {
	rt := &stubRoundTripper{failErr: &net.OpError{Op: "dial", Err: errors.New("refused")}}
	c := New(Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       "http://vngcloud.invalid/",
		OK:        []int{204},
		SkipAuth:  true,
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", rt.calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want true for a failed dial")
	}
}

// TestOnceRefusesRedirect checks that a Once request never follows a 307 or
// 308 to the same host: net/http would otherwise resend req's method and
// body at the Location the response names, sending a toggle PUT twice.
func TestOnceRefusesRedirect(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", r.URL.String())
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	c := New(Config{HTTPClient: server.Client()})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{204},
		SkipAuth:  true,
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error: a 307 is not in OK")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1: a Once request must never follow a redirect", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("StatusCode = %d, want %d: the caller sees the redirect itself", apiErr.StatusCode, http.StatusTemporaryRedirect)
	}
}

// invalidateTrackingSource issues sequential tokens and records every value
// Invalidate is called with, so a test can prove exactly which token, if
// any, a 401 recovery invalidated.
type invalidateTrackingSource struct {
	count       atomic.Int64
	invalidated []string
}

func (s *invalidateTrackingSource) Token(context.Context) (Token, error) {
	n := s.count.Add(1)
	return Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *invalidateTrackingSource) Invalidate(token string) {
	s.invalidated = append(s.invalidated, token)
}

// TestOnceNoResendAfter401 checks ADR 0003 rule 3's 401 case: a Once
// request that gets a 401 invalidates the token it sent, the same as an
// ordinary request would, but never resends, unlike an ordinary request
// which retries once with a refreshed token.
func TestOnceNoResendAfter401(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	source := &invalidateTrackingSource{}
	c := New(Config{HTTPClient: server.Client(), TokenSource: source})
	err := c.DoJSON(context.Background(), Request{
		Operation: "Op",
		Method:    http.MethodPut,
		URL:       server.URL,
		OK:        []int{204},
		Once:      true,
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1: a Once request must never resend after a 401", calls.Load())
	}
	if len(source.invalidated) != 1 || source.invalidated[0] != "token-1" {
		t.Fatalf("invalidated = %v, want [\"token-1\"]", source.invalidated)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", apiErr.StatusCode)
	}

	// A later call on the same Client must not reuse the invalidated token:
	// invalidateOnce clears it from the transport's own cache, not only from
	// the token source, so EnsureToken fetches a fresh one instead of
	// reusing "token-1", which the server already rejected.
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	if got := c.currentToken().AccessToken; got != "token-2" {
		t.Fatalf("token after invalidateOnce = %q, want %q", got, "token-2")
	}
}

// TestInvalidateOnceKeepsAccessTokenForConcurrentReaders proves the fix
// deterministically, with no dependence on goroutine scheduling: right
// after invalidateOnce, a reader must still see a token to send, and
// NeedsRefresh must be true so the next real request fetches a
// replacement instead of resending the rejected token.
func TestInvalidateOnceKeepsAccessTokenForConcurrentReaders(t *testing.T) {
	source := &invalidateTrackingSource{}
	c := New(Config{TokenSource: source})
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	sent := c.currentToken().AccessToken
	if sent == "" {
		t.Fatal("seed token is empty")
	}

	c.invalidateOnce(sent)

	if got := c.currentToken().AccessToken; got == "" {
		t.Fatal("AccessToken is empty after invalidateOnce: a concurrent reader would fail with a synthetic 401 unrelated to the real one")
	}
	if !c.currentToken().NeedsRefresh() {
		t.Fatal("NeedsRefresh() = false after invalidateOnce, want true")
	}
}

// TestInvalidateOnceRaceKeepsTokenForConcurrentReaders is a race test: a
// reader spinning on currentToken() concurrently with invalidateOnce must
// never observe an empty AccessToken. Before the fix, invalidateOnce wrote
// Token{} (both fields empty), a state that persisted until some other call
// fetched a replacement; here nothing does, so a reader spinning throughout
// the invalidateOnce calls below would reliably catch it. Run with -race.
func TestInvalidateOnceRaceKeepsTokenForConcurrentReaders(t *testing.T) {
	source := &invalidateTrackingSource{}
	c := New(Config{TokenSource: source})
	if err := c.EnsureToken(context.Background()); err != nil {
		t.Fatalf("EnsureToken() error = %v", err)
	}
	sent := c.currentToken().AccessToken

	var stop atomic.Bool
	var sawEmpty atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if c.currentToken().AccessToken == "" {
					sawEmpty.Store(true)
				}
			}
		}()
	}

	for range 50 {
		c.invalidateOnce(sent)
	}
	stop.Store(true)
	wg.Wait()

	if sawEmpty.Load() {
		t.Fatal("a concurrent reader observed an empty AccessToken after invalidateOnce")
	}
}
