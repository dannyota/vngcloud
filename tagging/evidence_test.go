package tagging

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestReplacementRequiresCompleteTags(t *testing.T) {
	for _, body := range []string{`null`, `[{"key":"keep","systemTag":false}]`, `[{"key":"keep","value":"v"}]`, `[{"key":"keep","value":"v","systemTag":false},{"key":"keep","value":"other","systemTag":false}]`, `[{"key":"keep","Key":"other","value":"v","systemTag":false}]`} {
		t.Run(body, func(t *testing.T) {
			var writes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				writeJSON(t, w, body)
			}))
			out, err := c.TagResource(context.Background(), &TagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "new", Value: "v"})
			if err == nil || out != nil || writes.Load() != 0 {
				t.Fatalf("incomplete snapshot: output=%v error=%v writes=%d", out, err, writes.Load())
			}
		})
	}
}

func TestReplacementRetainsUncertainOutcome(t *testing.T) {
	var writes atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, `[{"key":"keep","value":"v","systemTag":false}]`)
			return
		}
		writes.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	out, err := c.TagResource(context.Background(), &TagResourceInput{ResourceID: "res-1", ResourceType: "SERVER", Key: "new", Value: "v"})
	if out == nil || !out.Changed || len(out.Tags) != 1 || !errors.Is(err, ErrNotSettled) || writes.Load() != 1 {
		t.Fatalf("lost uncertain replacement: output=%v error=%v writes=%d", out, err, writes.Load())
	}
}
