package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// --- AddRoute and RemoveRoute shape checks ---

func TestAddRouteBadDestinationCIDR(t *testing.T) {
	cases := []string{"10.20.1.0/16", "not-a-cidr", ""}
	for _, cidr := range cases {
		t.Run(cidr, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected")
			}))
			_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: cidr, Target: "10.20.1.1"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestAddRouteBadTarget(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.20.1.0/24", Target: "not-an-address"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestAddRouteTargetWithZoneRejected(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "fe80::/64", Target: "fe80::1%eth0"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRemoveRouteBadDestinationCIDR(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.20.1.0/16"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// --- AddRoute read-merge ---

func TestAddRouteRequestBodyHoldsExistingAndNewRoutes(t *testing.T) {
	existing := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.1"}
	added := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}),        // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}),        // pre-PUT recheck
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing, added}), // post-write confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		routes, _ := body["routes"].([]any)
		if len(routes) != 2 {
			t.Fatalf("routes in body = %+v, want 2 entries", routes)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: added.DestinationCIDRBlock, Target: added.Target})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.RouteTable.Routes) != 2 {
		t.Fatalf("Routes = %+v, want 2 entries", out.RouteTable.Routes)
	}
}

func TestAddRouteAlreadyPresentSameTargetNoOp(t *testing.T) {
	existing := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing})))
		default:
			t.Fatalf("unexpected method %s: an unchanged add must send no PUT", r.Method)
		}
	}))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10"})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAddRouteAlreadyPresentCanonicalDestinationNoOp(t *testing.T) {
	// "2001:0DB8::/32" and "2001:db8::/32" name the same prefix, so this
	// must be treated the same as an exact-text match: a no-op, no PUT.
	existing := routeEntry{DestinationCIDRBlock: "2001:0DB8::/32", Target: "2001:db8::1"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing})))
		default:
			t.Fatalf("unexpected method %s: an equivalent destination must send no PUT", r.Method)
		}
	}))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "2001:db8::/32", Target: "2001:db8::1"})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAddRouteAlreadyPresentCanonicalTargetNoOp(t *testing.T) {
	// "2001:DB8::10" and "2001:db8::10" name the same address, so this must
	// be treated the same as an exact-text match: a no-op, no PUT.
	existing := routeEntry{DestinationCIDRBlock: "2001:db8::/32", Target: "2001:DB8::10"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing})))
		default:
			t.Fatalf("unexpected method %s: an equivalent target must send no PUT", r.Method)
		}
	}))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "2001:db8::/32", Target: "2001:db8::10"})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAddRouteSendsCanonicalDestinationAndTarget(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
		case http.MethodPut:
			body := decodeBody(t, r)
			routes, _ := body["routes"].([]any)
			entry, _ := routes[0].(map[string]any)
			if entry["destinationCidrBlock"] != "2001:db8::/32" || entry["target"] != "2001:db8::10" {
				t.Fatalf("route in body = %+v, want the canonical destination and target", entry)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "2001:DB8::/32", Target: "2001:DB8::10", NoWait: true})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
}

func TestAddRouteConflictingTargetErrInvalidInput(t *testing.T) {
	existing := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing})))
		default:
			t.Fatalf("unexpected method %s: a conflicting add must send no PUT", r.Method)
		}
	}))

	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.99"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "10.251.200.10") {
		t.Fatalf("err = %v, want it to name the current target", err)
	}
}

func TestAddRouteUnknownRouteTableReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a missing table must send no PUT", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-x", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestRemoveRouteUnknownRouteTableReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s: a missing table must send no PUT", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-x", DestinationCIDR: "10.251.200.0/24"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestAddRoutePreWriteBoundErrBusy(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "UPDATING", nil)))
		default:
			t.Fatalf("unexpected method %s: a busy table must send no PUT", r.Method)
		}
	})))

	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddRoutePreWriteRecheckMismatchErrBusy(t *testing.T) {
	existing := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.1"}
	changedByOther := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.9"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}),       // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{changedByOther}), // pre-PUT recheck: changed
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		}
	})))

	_, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestAddRouteConfirmMismatchErrNotSettled(t *testing.T) {
	unexpected := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.1"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                      // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                      // pre-PUT recheck: unchanged
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{unexpected}), // confirm read: wrong routes
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil {
		t.Fatal("out = nil, want a non-nil Output carrying the last read table")
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true: the PUT was sent even though the confirm read did not match")
	}
}

func TestAddRouteConfirmCanonicalizedDestinationMatches(t *testing.T) {
	// The confirm read names the same prefix AddRoute sent, just spelled
	// with different letter case; that must still settle, not ErrNotSettled.
	added := routeEntry{DestinationCIDRBlock: "2001:db8::/32", Target: "2001:db8::10"}
	confirmed := routeEntry{DestinationCIDRBlock: "2001:DB8::/32", Target: "2001:db8::10"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                     // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                     // pre-PUT recheck
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{confirmed}), // post-write confirm: same prefix, different case
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
		}
	})))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: added.DestinationCIDRBlock, Target: added.Target})
	if err != nil {
		t.Fatalf("AddRoute() error = %v, want nil: an equivalent destination spelling must still settle", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestAddRouteNoWaitSkipsPostWritePoll(t *testing.T) {
	var getCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
		case http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24", Target: "10.251.200.10", NoWait: true})
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls.Load() != 2 {
		t.Fatalf("GET calls = %d, want 2 (the pre-write read and the pre-PUT recheck): NoWait must skip only the post-write poll", getCalls.Load())
	}
	if len(out.RouteTable.Routes) != 1 {
		t.Fatalf("Routes = %+v, want the one route just sent", out.RouteTable.Routes)
	}
}

// --- RemoveRoute read-merge ---

func TestRemoveRouteRequestBodyDropsOnlyTheNamedRoute(t *testing.T) {
	kept := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.1"}
	removed := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{kept, removed}), // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{kept, removed}), // pre-PUT recheck
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{kept}),          // post-write confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		routes, _ := body["routes"].([]any)
		if len(routes) != 1 {
			t.Fatalf("routes in body = %+v, want 1 entry", routes)
		}
		entry, _ := routes[0].(map[string]any)
		if entry["destinationCidrBlock"] != kept.DestinationCIDRBlock {
			t.Fatalf("remaining route = %+v, want the one not removed", entry)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: removed.DestinationCIDRBlock})
	if err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if len(out.RouteTable.Routes) != 1 {
		t.Fatalf("Routes = %+v, want 1 entry", out.RouteTable.Routes)
	}
}

func TestRemoveRouteCanonicalDestinationMatches(t *testing.T) {
	// "2001:0DB8::/32" and "2001:db8::/32" name the same prefix, so
	// RemoveRoute must find and drop it even though the caller spelled the
	// destination differently than the route it read.
	existing := routeEntry{DestinationCIDRBlock: "2001:0DB8::/32", Target: "2001:db8::1"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}), // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}), // pre-PUT recheck
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                    // post-write confirm read
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		body := decodeBody(t, r)
		routes, _ := body["routes"].([]any)
		if len(routes) != 0 {
			t.Fatalf("routes in body = %+v, want none: the only route matched the destination", routes)
		}
		w.WriteHeader(http.StatusOK)
	})))

	out, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: "2001:db8::/32"})
	if err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
}

func TestRemoveRouteMultipleMatchesRefused(t *testing.T) {
	dup1 := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	dup2 := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.11"}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{dup1, dup2})))
		default:
			t.Fatalf("unexpected method %s: an ambiguous remove must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "2 routes") {
		t.Fatalf("err = %v, want it to name the match count", err)
	}
}

func TestRemoveRouteAbsentReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
		default:
			t.Fatalf("unexpected method %s: removing an absent route must send no PUT", r.Method)
		}
	}))

	_, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want core.ErrNotFound", err)
	}
}

func TestRemoveRoutePreWriteRecheckMismatchErrBusy(t *testing.T) {
	existing := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.10"}
	changedByOther := routeEntry{DestinationCIDRBlock: "10.251.200.0/24", Target: "10.251.200.99"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{existing}),       // pre-write read
		routeTableJSON("rt-2", "custom", "ACTIVE", []routeEntry{changedByOther}), // pre-PUT recheck: changed
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			t.Fatal("unexpected PUT: a mismatched recheck must send nothing")
		}
	})))

	_, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: "10.251.200.0/24"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}
