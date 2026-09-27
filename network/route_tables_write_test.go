package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// routeTableJSON builds a {"data": {...}} route table envelope matching
// GetRouteTable's decode shape, for tests that need to control the route
// list or status a GET returns. Every table it builds belongs to "vpc-1",
// the fixed VPC id the DeleteRouteTable guard tests key their VPC and
// subnet responses on.
func routeTableJSON(uuid, name, status string, routes []routeEntry) string {
	type routeJSON struct {
		UUID                 string `json:"uuid"`
		RouteTableID         string `json:"routeTableId"`
		RoutingType          string `json:"routingType"`
		DestinationCIDRBlock string `json:"destinationCidrBlock"`
		Target               string `json:"target"`
		Status               string `json:"status"`
	}
	rs := make([]routeJSON, len(routes))
	for i, r := range routes {
		rs[i] = routeJSON{
			UUID: "route-x", RouteTableID: uuid, RoutingType: "internet",
			DestinationCIDRBlock: r.DestinationCIDRBlock, Target: r.Target, Status: "ACTIVE",
		}
	}
	var fixture struct {
		Data struct {
			UUID      string      `json:"uuid"`
			Name      string      `json:"name"`
			Status    string      `json:"status"`
			NetworkID string      `json:"networkId"`
			CreatedAt string      `json:"createdAt"`
			Routes    []routeJSON `json:"routes"`
		} `json:"data"`
	}
	fixture.Data.UUID = uuid
	fixture.Data.Name = name
	fixture.Data.Status = status
	fixture.Data.NetworkID = "vpc-1"
	fixture.Data.CreatedAt = "2026-01-01T00:00:00Z"
	fixture.Data.Routes = rs
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// scriptedRouteTableGets serves the strings in bodies, each a 200 response,
// to successive GET requests in order, repeating the last one once they run
// out, and delegates every other method to other.
func scriptedRouteTableGets(t *testing.T, bodies []string, other http.HandlerFunc) http.Handler {
	t.Helper()
	var n atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if other == nil {
				t.Fatalf("unexpected %s request", r.Method)
			}
			other(w, r)
			return
		}
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	})
}

// --- GetRouteTable ---

func TestGetRouteTableDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/project-1/route-table/rt-2" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/get_route_table.json")
	}))

	out, err := c.GetRouteTable(context.Background(), &GetRouteTableInput{RouteTableID: "rt-2"})
	if err != nil {
		t.Fatalf("GetRouteTable() error = %v", err)
	}
	if out.RouteTable.UUID != "rt-2" || out.RouteTable.NetworkID != "vpc-1" || out.RouteTable.Status != "ACTIVE" {
		t.Fatalf("unexpected table: %+v", out.RouteTable)
	}
	if len(out.RouteTable.Routes) != 1 || out.RouteTable.Routes[0].DestinationCIDRBlock != "<cidr>" || out.RouteTable.Routes[0].Target != "<ip>" {
		t.Fatalf("unexpected routes: %+v", out.RouteTable.Routes)
	}
}

// --- CreateRouteTable ---

func TestCreateRouteTableRequestBody(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/project-1/route-table" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeBody(t, r)
		if body["name"] != "rt-web" || body["networkId"] != "vpc-1" {
			t.Fatalf("body = %+v, want name=rt-web networkId=vpc-1", body)
		}
		if _, hasRoutes := body["routes"]; hasRoutes {
			t.Fatalf("body = %+v, want no routes field on create", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
	}))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web", NoWait: true})
	if err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	if out.RouteTable.UUID != "rt-2" {
		t.Fatalf("UUID = %q, want rt-2", out.RouteTable.UUID)
	}
}

func TestCreateRouteTableDecodesFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
		testutil.WriteFixture(t, w, "../testdata/network/create_route_table.json")
	}))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "<name>", NoWait: true})
	if err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	if out.RouteTable.UUID != "rt-2" || out.RouteTable.Name != "<name>" || out.RouteTable.NetworkID != "vpc-1" {
		t.Fatalf("unexpected table: %+v", out.RouteTable)
	}
}

func TestCreateRouteTableNoIDFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	}))

	_, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web", NoWait: true})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list route tables") {
		t.Fatalf("err = %v, want an APIError naming list route tables before creating again", err)
	}
}

func TestCreateRouteTableNoRetryAfter502(t *testing.T) {
	var calls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"upstream error"}`))
	}))

	_, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web", NoWait: true})
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a create must never be retried after a 5xx", calls.Load())
	}
	if !strings.Contains(err.Error(), "list route tables") {
		t.Fatalf("err = %v, want a hint to list route tables before creating again", err)
	}
}

func TestCreateRouteTableRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.CreateRouteTable(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

// --- CreateRouteTable's post-create wait ---

func TestCreateRouteTableWaitSettlesToActive(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "rt-web", "CREATING", nil),
		routeTableJSON("rt-2", "rt-web", "ACTIVE", nil),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		}
	})))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"})
	if err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	if out.RouteTable.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.RouteTable.Status)
	}
	if len(out.RouteTable.Routes) != 0 {
		t.Fatalf("Routes = %+v, want none on a new table", out.RouteTable.Routes)
	}
}

func TestCreateRouteTableWaitErrFailed(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "rt-web", "CREATING", nil),
		routeTableJSON("rt-2", "rt-web", "ERROR", nil),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		}
	})))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.RouteTable.Status != "ERROR" {
		t.Fatalf("out = %+v, want a non-nil Output holding the ERROR table", out)
	}
}

func TestCreateRouteTableWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		case http.MethodGet:
			n := getCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "rt-web", "ACTIVE", nil)))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"})
	if err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	if out.RouteTable.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.RouteTable.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateRouteTableWaitBoundErrNotSettled(t *testing.T) {
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "rt-web", "CREATING", nil),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		}
	})))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if out == nil || out.RouteTable.UUID != "rt-2" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the table id", out)
	}
}

func TestCreateRouteTableWaitPollSpacing(t *testing.T) {
	var sleeps []time.Duration
	c := newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "rt-web", "CREATING", nil),
		routeTableJSON("rt-2", "rt-web", "CREATING", nil),
		routeTableJSON("rt-2", "rt-web", "ACTIVE", nil),
	}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		}
	}))
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"}); err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	for _, d := range sleeps {
		if d != pollInterval {
			t.Fatalf("sleep duration = %s, want %s", d, pollInterval)
		}
	}
	if len(sleeps) == 0 {
		t.Fatal("no sleep calls recorded")
	}
}

func TestCreateRouteTableWaitCancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "rt-web", "CREATING", nil)))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	c.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}

	out, err := c.CreateRouteTable(ctx, &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if out == nil || out.RouteTable.UUID != "rt-2" {
		t.Fatalf("out = %+v, want a non-nil Output carrying the table id", out)
	}
}

func TestCreateRouteTableNoWaitSkipsPoll(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s: NoWait must not poll", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"uuid":"rt-2"}`))
	}))

	out, err := c.CreateRouteTable(context.Background(), &CreateRouteTableInput{VPCID: "vpc-1", Name: "rt-web", NoWait: true})
	if err != nil {
		t.Fatalf("CreateRouteTable() error = %v", err)
	}
	if out.RouteTable.UUID != "rt-2" || out.RouteTable.Name != "rt-web" || out.RouteTable.NetworkID != "vpc-1" {
		t.Fatalf("unexpected table: %+v", out.RouteTable)
	}
}

// --- DeleteRouteTable ---

func TestDeleteRouteTableMainTableRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/route-table/rt-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-1", "main", "ACTIVE", nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"vpc-1","routeTableId":"rt-1"}`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: "rt-1"})
	if !errors.Is(err, ErrDefaultResource) {
		t.Fatalf("err = %v, want ErrDefaultResource", err)
	}
}

func TestDeleteRouteTableNamedBySubnetRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/route-table/rt-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"vpc-1","routeTableId":"rt-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"uuid":"sub-1","routeTableUuid":"rt-2"}]`))
		default:
			t.Fatalf("unexpected request: %s %s (delete must send no DELETE)", r.Method, r.URL.Path)
		}
	}))

	_, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: "rt-2"})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteRouteTableSuccess(t *testing.T) {
	var getTableCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/route-table/rt-2":
			n := getTableCalls.Add(1)
			if n == 1 {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"vpc-1","routeTableId":"rt-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/project-1/route-table/rt-2":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	if _, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: "rt-2"}); err != nil {
		t.Fatalf("DeleteRouteTable() error = %v", err)
	}
	if getTableCalls.Load() < 2 {
		t.Fatalf("GetRouteTable calls = %d, want at least 2 (guard read, then the delete wait's 404)", getTableCalls.Load())
	}
}

func TestDeleteRouteTableUnknownReturnsNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))

	_, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: "rt-x"})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
}

func TestDeleteRouteTableNoWaitSkipsPoll(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/route-table/rt-2":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(routeTableJSON("rt-2", "custom", "ACTIVE", nil)))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"vpc-1","routeTableId":"rt-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/networks/vpc-1/subnets":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s: NoWait must not poll after the DELETE", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: "rt-2", NoWait: true}); err != nil {
		t.Fatalf("DeleteRouteTable() error = %v", err)
	}
}

// --- Path ID checks ---

func TestRouteTablePathIDRejection(t *testing.T) {
	badIDs := []string{"..", ".", "a/b", "a?b", ""}

	for _, id := range badIDs {
		t.Run("id="+id, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("no request expected for a malformed path ID")
			}))

			if _, err := c.GetRouteTable(context.Background(), &GetRouteTableInput{RouteTableID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("GetRouteTable() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.DeleteRouteTable(context.Background(), &DeleteRouteTableInput{RouteTableID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("DeleteRouteTable() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: id, DestinationCIDR: "10.0.0.0/24", Target: "10.0.0.1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("AddRoute() err = %v, want ErrInvalidInput", err)
			}
			if _, err := c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: id, DestinationCIDR: "10.0.0.0/24"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Errorf("RemoveRoute() err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

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

func TestAddRouteConfirmMismatchErrNotSettled(t *testing.T) {
	unexpected := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "203.0.113.1"}
	c := withInstantSleep(newTestClient(t, scriptedRouteTableGets(t, []string{
		routeTableJSON("rt-2", "custom", "ACTIVE", nil),                      // pre-write read
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
	if getCalls.Load() != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-write read only): NoWait must skip the post-write poll", getCalls.Load())
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
