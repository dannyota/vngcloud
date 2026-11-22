package vks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func fakeVKSClock(c *Client, cancel context.CancelFunc) *[]time.Duration {
	now := time.Unix(100, 0)
	delays := []time.Duration{}
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		if cancel != nil {
			cancel()
			return ctx.Err()
		}
		now = now.Add(d)
		return nil
	}
	return &delays
}

func checkVKSDelays(t *testing.T, delays []time.Duration, count int) {
	t.Helper()
	if len(delays) != count {
		t.Fatalf("delays=%v, want %d", delays, count)
	}
	for _, d := range delays {
		if d != 10*time.Second {
			t.Fatalf("delay=%v, want 10s", d)
		}
	}
}

func TestCreateWaitClock(t *testing.T) {
	for _, tc := range []struct {
		name          string
		statuses      []string
		want          error
		last          string
		reads, sleeps int
		cancel        bool
	}{
		{"active", []string{"CREATING", "ACTIVE"}, nil, "ACTIVE", 2, 1, false},
		{"error", []string{"ERROR"}, ErrFailed, "ERROR", 1, 0, false},
		{"failed", []string{"CREATE_FAILED"}, ErrFailed, "CREATE_FAILED", 1, 0, false},
		{"timeout", []string{"CREATING"}, ErrNotSettled, "CREATING", 180, 180, false},
		{"cancel", []string{"CREATING"}, ErrNotSettled, "CREATING", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(202)
					_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
					return
				}
				idx := reads
				reads++
				if idx >= len(tc.statuses) {
					idx = len(tc.statuses) - 1
				}
				_, _ = fmt.Fprintf(w, `{"id":"cluster-1","status":%q}`, tc.statuses[idx])
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stop context.CancelFunc
			if tc.cancel {
				stop = cancel
			}
			delays := fakeVKSClock(c, stop)
			out, err := c.CreateCluster(ctx, validCreate())
			if !errors.Is(err, tc.want) || out == nil || out.ID != "cluster-1" || out.Status != tc.last || reads != tc.reads {
				t.Fatalf("out=%+v err=%v reads=%d", out, err, reads)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want cancellation", err)
			}
			if tc.name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v, want deadline", err)
			}
			checkVKSDelays(t, *delays, tc.sleeps)
		})
	}
}

func TestDeleteWaitClock(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		absentAt, reads, sleeps int
		want                    error
		cancel                  bool
	}{
		{"absent", 3, 3, 1, nil, false},
		{"timeout", 0, 181, 180, ErrNotSettled, false},
		{"cancel", 0, 2, 1, ErrNotSettled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(202)
					return
				}
				reads++
				if reads == tc.absentAt {
					http.NotFound(w, r)
					return
				}
				_, _ = fmt.Fprint(w, `{"id":"cluster-1","status":"ACTIVE"}`)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stop context.CancelFunc
			if tc.cancel {
				stop = cancel
			}
			delays := fakeVKSClock(c, stop)
			out, err := c.DeleteCluster(ctx, &DeleteClusterInput{ClusterID: "cluster-1"})
			if !errors.Is(err, tc.want) || out == nil || out.Deleted != (tc.want == nil) || reads != tc.reads {
				t.Fatalf("out=%+v err=%v reads=%d", out, err, reads)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v, want cancellation", err)
			}
			if tc.name == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v, want deadline", err)
			}
			checkVKSDelays(t, *delays, tc.sleeps)
		})
	}
}
