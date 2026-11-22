package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOnceOversizedUnauthorizedInvalidatesSentToken(t *testing.T) {
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(strings.Repeat("x", 32)))
	}))
	defer server.Close()
	source := &invalidateTrackingSource{}
	c := New(Config{HTTPClient: server.Client(), TokenSource: source})
	req := Request{Operation: "test", Method: http.MethodPost, URL: server.URL, Once: true, MaxBody: 8}
	for range 2 {
		status, err := c.DoJSONStatus(context.Background(), req, nil)
		if status != http.StatusUnauthorized || err == nil {
			t.Fatalf("status = %d, error = %v", status, err)
		}
	}
	if len(sent) != 2 || sent[0] != "Bearer token-1" || sent[1] != "Bearer token-2" {
		t.Fatalf("sent = %v", sent)
	}
	if len(source.invalidated) != 2 || source.invalidated[0] != "token-1" || source.invalidated[1] != "token-2" {
		t.Fatalf("invalidated = %v", source.invalidated)
	}
}
