package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATSlowOrderPreservesSettlementWindow(t *testing.T) {
	s := new(natScenario)
	var mu sync.Mutex
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var orderFinished time.Time
	orderBounded := false
	rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
			deadline, ok := r.Context().Deadline()
			orderBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 15*time.Minute
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
			mu.Lock()
			now = now.Add(14 * time.Minute)
			orderFinished = now
			mu.Unlock()
		}
		if s.ordered && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") {
			mu.Lock()
			provisioning := now.Sub(orderFinished) < 14*time.Minute
			mu.Unlock()
			row := natTestRow
			if provisioning {
				row = strings.Replace(row, "ACTIVE", "PROVISIONING", 1)
			}
			_, _ = w.Write([]byte(natInventory(row)))
			return
		}
		s.serve(t, w, r)
	}), vngcloud.WithTransport(rt))
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	c.sleep = func(ctx context.Context, _ time.Duration) error {
		mu.Lock()
		now = now.Add(time.Minute)
		mu.Unlock()
		return ctx.Err()
	}
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if err != nil || out == nil || out.NATInstance == nil || out.NATInstance.Status != "ACTIVE" || out.AutoRenew == nil || *out.AutoRenew || s.orders != 1 || s.puts != 1 || !orderBounded {
		t.Fatalf("slow order lost settlement window: %v", err)
	}
}

func TestNATErrorRequiresAcceptedOrder(t *testing.T) {
	for _, reply := range []string{"accepted", "malformed", "lost"} {
		t.Run(reply, func(t *testing.T) {
			s := new(natScenario)
			rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
				if reply == "lost" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
					s.orders++
					s.ordered = true
					return nil, errors.New("synthetic lost order reply")
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if reply == "malformed" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
					s.orders++
					s.ordered = true
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{`))
					return
				}
				if s.ordered && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") {
					_, _ = w.Write([]byte(natInventory(strings.Replace(natTestRow, "ACTIVE", "ERROR", 1))))
					return
				}
				s.serve(t, w, r)
			}), vngcloud.WithTransport(rt))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			want := ErrNotSettled
			if reply == "accepted" {
				want = ErrFailed
			}
			if !errors.Is(err, want) || (reply != "accepted" && errors.Is(err, ErrFailed)) || out == nil || out.NATInstance == nil || out.NATInstance.Status != "ERROR" || s.orders != 1 || s.puts != 0 {
				t.Fatalf("incorrect ERROR settlement: %v", err)
			}
		})
	}
}
