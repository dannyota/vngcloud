package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestRegionResponseCannotSelectCredentialDestination(t *testing.T) {
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		foreignCalls.Add(1)
		_, _ = w.Write([]byte(`{"data":[],"page":1,"size":10,"totalPage":0,"total":0}`))
	}))
	t.Cleanup(foreign.Close)
	served := 0
	c := natTestClient(t, "hcm-3", "project-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/regions") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"uuid": "zone-1", "code": "hcm-3", "vnetworkDashboard": foreign.URL}}})
			return
		}
		served++
		_, _ = w.Write([]byte(`{"data":[],"page":1,"size":10,"totalPage":0,"total":0}`))
	}))
	_, _ = c.ListEndpoints(context.Background(), nil)
	if foreignCalls.Load() != 0 || served != 1 {
		t.Fatalf("foreign=%d configured=%d", foreignCalls.Load(), served)
	}
}

func TestRegionMatchingUsesExactIdentity(t *testing.T) {
	for _, r := range []VNetworkRegion{{Name: "prefix-hcm-3-suffix"}, {Code: "hcm-30"}, {DashboardURL: "https://hcm-3.attacker.test"}, {GatewayURL: "https://attacker.test/hcm-3"}} {
		if r.matches("hcm-3") {
			t.Fatal("substring established regional identity")
		}
	}
}

func TestRoutesReplaceSentOnce(t *testing.T) {
	for _, status := range []int{503, 401, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			puts, redirects, reads := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					redirects++
					w.WriteHeader(200)
					return
				}
				if r.Method == http.MethodPut {
					puts++
					w.Header().Set("Location", "/redirected")
					w.WriteHeader(status)
					return
				}
				reads++
				_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
			}))
			t.Cleanup(srv.Close)
			cfg, configErr := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(&ruleRefreshCredentials{}), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(3, time.Millisecond))
			if configErr != nil {
				t.Fatal(configErr)
			}
			c := withInstantSleep(New(cfg))
			out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "203.0.113.0/24", Target: "10.0.0.1"})
			if err == nil || puts != 1 || redirects != 0 {
				t.Fatalf("err=%v puts=%d redirects=%d", err, puts, redirects)
			}
			if status != 401 && (!errors.Is(err, ErrNotSettled) || out == nil || reads < 3) {
				t.Fatalf("missing reconciliation: out=%+v err=%v reads=%d", out, err, reads)
			}
		})
	}
}

func TestACLInventoryRejectsIncompleteEvidence(t *testing.T) {
	for _, body := range []string{`{}`, `{"listData":null,"page":1,"pageSize":1,"totalPage":0,"totalItem":0}`, `{"listData":[],"page":1,"pageSize":1,"totalPage":0,"totalItem":0,"TotalItem":1}`, `{"listData":[{"uuid":"other"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`, `{"listData":[{"uuid":"other"},{"uuid":"other"}],"page":1,"pageSize":2,"totalPage":1,"totalItem":2}`, `{"listData":[{"uuid":"other"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":0}`} {
		t.Run(body, func(t *testing.T) {
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/list") {
					_, _ = w.Write([]byte(body))
					return
				}
				if r.Method == http.MethodDelete {
					w.WriteHeader(500)
					return
				}
				_, _ = w.Write([]byte(aclJSON("ACTIVE", false, nil, []string{})))
			})))
			if _, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"}); err == nil {
				t.Fatal("incomplete inventory confirmed deletion")
			}
		})
	}
}

func TestNetworkPollRejectsLateSuccess(t *testing.T) {
	now := time.Unix(0, 0)
	err := poll(context.Background(), func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("unexpected sleep"); return nil }, time.Second, time.Second, func(context.Context) (bool, error) { now = now.Add(time.Second); return true, nil }, func() error { return context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late success: %v", err)
	}
}

func TestNetworkPollPropagatesDeadlineAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := poll(ctx, time.Now, contextSleep, time.Second, time.Second, func(context.Context) (bool, error) { calls++; return true, nil }, func() error { return context.DeadlineExceeded })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancellation: %v calls=%d", err, calls)
	}
	err = poll(context.Background(), time.Now, contextSleep, time.Second, time.Second, func(ctx context.Context) (bool, error) {
		if _, ok := ctx.Deadline(); !ok {
			return true, errors.New("missing operation deadline")
		}
		return true, nil
	}, func() error { return context.DeadlineExceeded })
	if err != nil {
		t.Fatal(err)
	}
}

func TestACLGuardRequiresExplicitMatchingState(t *testing.T) {
	valid := aclJSON("ACTIVE", false, nil, []string{})
	for _, body := range []string{
		strings.Replace(valid, `"uuid":"acl-1"`, `"uuid":"acl-2"`, 1),
		strings.Replace(valid, `"subnetAssociationList":[]`, `"subnetAssociationList":null`, 1),
		strings.Replace(valid, `"subnetAssociationList":[],`, "", 1),
		strings.Replace(valid, `"defaultAcl":false,`, "", 1),
		strings.Replace(valid, `"defaultAcl":false`, `"defaultAcl":true,"DefaultAcl":false`, 1),
	} {
		t.Run(body, func(t *testing.T) {
			writes := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					writes++
					w.WriteHeader(204)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			if _, err := c.DeleteNetworkACL(context.Background(), &DeleteNetworkACLInput{NetworkACLID: "acl-1"}); err == nil || writes != 0 {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
		})
	}
}

func TestRegionReadRejectsConflictingKeys(t *testing.T) {
	for _, body := range []string{`{"data":[{"uuid":"a","UUID":"b","code":"hcm-3"}]}`, `{"data":[],"Data":[{"uuid":"a","code":"hcm-3"}]}`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		if out, err := c.ListVNetworkRegions(context.Background(), nil); err == nil || out != nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}

func TestRouteGuardRejectsConflictingFields(t *testing.T) {
	for _, body := range []string{`{"data":{"uuid":"rt-2","status":"ERROR","Status":"ACTIVE","routes":[]}}`, `{"data":{"uuid":"other","status":"ACTIVE","routes":[]}}`, `{"data":{"uuid":"rt-2","status":"ACTIVE","routes":null}}`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		if out, err := c.GetRouteTable(context.Background(), &GetRouteTableInput{RouteTableID: "rt-2"}); out != nil || err == nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}
