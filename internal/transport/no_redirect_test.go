package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestNoRedirect(t *testing.T) {
	for _, status := range []int{307, 308, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/price" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if calls == 1 {
					w.Header().Set("Location", "/orders")
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			c := New(Config{HTTPClient: server.Client(), RetryCount: 3, RetryInterval: time.Millisecond})
			err := c.DoJSON(context.Background(), Request{Operation: "Price", Method: http.MethodPost, URL: server.URL + "/price", Body: map[string]int{"quota": 30}, APIKey: "test-token", Idempotent: true, NoRedirect: true, OK: []int{204}}, nil)
			if status < 400 {
				if err == nil || calls != 1 {
					t.Fatalf("error %v, requests %d, want error and one request", err, calls)
				}
			} else if err != nil || calls != 2 {
				t.Fatalf("error %v, requests %d, want success and two requests", err, calls)
			}
		})
	}
}
