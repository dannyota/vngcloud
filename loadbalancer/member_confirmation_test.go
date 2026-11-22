package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestMemberConfirmationBusyErrorNeverResends(t *testing.T) {
	for _, phase := range []string{"pool", "members"} {
		t.Run(phase, func(t *testing.T) {
			writes := 0
			refused := false
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
					writes++
				case r.URL.Path == memberLBPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)
				case r.URL.Path == memberPoolPath:
					if writes == 1 && phase == "pool" && !refused {
						refused = true
						w.WriteHeader(http.StatusConflict)
						_, _ = fmt.Fprint(w, `{"message":"pool is updating"}`)
						return
					}
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)
				case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
					if writes == 1 && phase == "members" && !refused {
						refused = true
						w.WriteHeader(http.StatusConflict)
						_, _ = fmt.Fprint(w, `{"message":"pool is updating"}`)
						return
					}
					members := []PoolMember{{Address: "10.0.0.1", ProtocolPort: 80, Weight: 1}}
					if writes >= 2 {
						members = append(members, PoolMember{Address: "10.0.0.2", ProtocolPort: 80, Weight: 1})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": members})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			withInstantSleep(c)
			out, err := c.AddPoolMember(context.Background(), &AddPoolMemberInput{
				LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.2", Port: 80,
			})
			var apiErr *vngcloud.APIError
			if writes != 1 || !errors.Is(err, ErrNotSettled) || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
				t.Fatalf("writes = %d, output = %+v, error = %v; want one PUT and unsettled 409", writes, out, err)
			}
			if !refused || out == nil || !out.Changed || out.Pool.UUID != "pool-1" {
				t.Fatalf("refused = %v, output = %+v; want partial output after accepted PUT", refused, out)
			}
		})
	}
}

func TestMemberFinalConfirmationSharesPostWriteDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		delay   time.Duration
		wantErr bool
	}{
		{"before deadline", 9*time.Minute + 50*time.Second, false},
		{"at deadline", 9*time.Minute + 55*time.Second, true},
		{"after deadline", 10 * time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var elapsed atomic.Int64
			writes, memberReads := 0, 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
					writes++
				case r.URL.Path == memberLBPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)
				case r.URL.Path == memberPoolPath:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)
				case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
					memberReads++
					members := []PoolMember{{Address: "10.0.0.1", ProtocolPort: 80, Weight: 1}}
					if writes > 0 {
						elapsed.Add(int64(tc.delay))
						members = append(members, PoolMember{Address: "10.0.0.2", ProtocolPort: 80, Weight: 1})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": members})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			start := time.Now()
			c.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
			c.sleep = func(ctx context.Context, delay time.Duration) error {
				elapsed.Add(int64(delay))
				return ctx.Err()
			}
			out, err := c.AddPoolMember(context.Background(), &AddPoolMemberInput{
				LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.2", Port: 80,
			})
			if writes != 1 || memberReads != 2 || out == nil || !out.Changed || out.Pool.UUID != "pool-1" {
				t.Fatalf("writes = %d, member reads = %d, output = %+v", writes, memberReads, out)
			}
			if tc.wantErr {
				if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("elapsed = %v, error = %v; want unsettled deadline", time.Duration(elapsed.Load()), err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemberFinalConfirmationRequestUsesRemainingDeadline(t *testing.T) {
	var elapsed atomic.Int64
	var wrote atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == memberMembersPath:
			wrote.Store(true)
		case r.URL.Path == memberLBPath:
			_, _ = fmt.Fprint(w, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)
		case r.URL.Path == memberPoolPath:
			if wrote.Load() {
				elapsed.Add(int64(9*time.Minute + 50*time.Second))
			}
			_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)
		case r.Method == http.MethodGet && r.URL.Path == memberMembersPath:
			if wrote.Load() {
				_, _ = fmt.Fprint(w, `{"data":[{"address":"10.0.0.2","protocolPort":80,"weight":1}]}`)
			} else {
				_, _ = fmt.Fprint(w, `{"data":[]}`)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	httpClient := server.Client()
	checked := false
	httpClient.Transport = memberContextRoundTripper{base: httpClient.Transport, check: func(r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == memberMembersPath && wrote.Load() {
			checked = true
			deadline, ok := r.Context().Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Error("final member request lacks the remaining five-second operation deadline")
			}
		}
	}}
	c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VLB: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: httpClient})))
	start := time.Now()
	c.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	c.sleep = func(ctx context.Context, delay time.Duration) error {
		elapsed.Add(int64(delay))
		return ctx.Err()
	}
	_, err := c.AddPoolMember(context.Background(), &AddPoolMemberInput{
		LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.2", Port: 80,
	})
	if err != nil || !checked {
		t.Fatalf("checked = %v, error = %v", checked, err)
	}
}

type memberContextRoundTripper struct {
	base  http.RoundTripper
	check func(*http.Request)
}

func (rt memberContextRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.check(r)
	return rt.base.RoundTrip(r)
}
