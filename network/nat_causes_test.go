package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATSettlementRetainsSafeCauses(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			s := &natScenario{ordered: method == http.MethodDelete}
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }), vngcloud.WithTransport(natRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method == method && ((method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats")) || (method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/autoRenew")) || method == http.MethodDelete) {
					return nil, fmt.Errorf("synthetic-secret-canary: %w", context.Canceled)
				}
				return http.DefaultTransport.RoundTrip(r)
			})))
			var err error
			if method == http.MethodDelete {
				_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1", NoWait: true})
			} else {
				_, err = c.CreateNATInstance(context.Background(), natTestInput())
			}
			if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) {
				t.Fatalf("lost safe cause: %v", err)
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "synthetic-secret-canary") {
				t.Fatal("cause text escaped")
			}
		})
	}
}

func TestNATLateInventoryRetainsDeadline(t *testing.T) {
	s := new(natScenario)
	var mu sync.Mutex
	now := time.Unix(0, 0)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ordered && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") {
			mu.Lock()
			now = now.Add(16 * time.Minute)
			mu.Unlock()
		}
		s.serve(t, w, r)
	}))
	c.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if out == nil || !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
