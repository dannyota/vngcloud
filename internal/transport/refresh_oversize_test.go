package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type trackedRefreshSource struct {
	testTokenSource
	mu          sync.Mutex
	invalidated []string
}

func (s *trackedRefreshSource) Invalidate(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidated = append(s.invalidated, token)
}

func TestOversizedRefreshedUnauthorizedInvalidatesFinalToken(t *testing.T) {
	var requests []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Header.Get("Authorization"))
		switch len(requests) {
		case 1:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{}`))
		case 2:
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(strings.Repeat("x", 32)))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	source := &trackedRefreshSource{}
	client := New(Config{HTTPClient: server.Client(), TokenSource: source})
	req := Request{URL: server.URL, MaxBody: 8, BodyLimitOverridesStatus: true}
	status, err := client.DoJSONStatus(context.Background(), req, nil)
	if status != http.StatusUnauthorized || !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("status %d, error %v", status, err)
	}
	if source.count.Load() != 2 {
		t.Fatalf("unexpected third token fetch: %d", source.count.Load())
	}
	if err := client.DoJSON(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || requests[0] != "Bearer token-1" || requests[1] != "Bearer token-2" || requests[2] != "Bearer token-3" {
		t.Fatal("unexpected credential sequence")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.invalidated) != 2 || source.invalidated[0] != "token-1" || source.invalidated[1] != "token-2" {
		t.Fatal("unexpected invalidation sequence")
	}
}
