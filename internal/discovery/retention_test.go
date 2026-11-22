package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/transport"
)

func TestDiscoveryCumulativeRetentionLimit(t *testing.T) {
	ctx := WithTestClock(context.Background(), time.Now, func(context.Context, time.Duration) error { return nil })
	s, err := New(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := &fakeExchange{body: `"` + strings.Repeat("x", (8<<20)-2) + `"`}
	for i := range 4 {
		req := transport.Request{Method: "GET", URL: fmt.Sprintf("https://example.test/v1/projects?index=%d", i)}
		if _, err := s.DoJSON(f, req, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DoJSON(f, req, nil); err != nil {
			t.Fatal("cache hit consumed retention budget")
		}
	}
	f.body = `0`
	out := 42
	_, err = s.DoJSON(f, transport.Request{Method: "GET", URL: "https://example.test/v1/projects?overflow=1"}, &out)
	if !errors.Is(err, ErrLimit) || out != 42 || len(s.cache) != 4 {
		t.Fatal("retention overflow was decoded or cached")
	}
	if err := s.Preflight(1); !errors.Is(err, ErrLimit) {
		t.Fatal("retention overflow did not stop scheduling")
	}
}
