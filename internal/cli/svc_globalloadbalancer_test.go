package cli

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/globalloadbalancer"
)

// exampleGLB is a representative LoadBalancer, reused by the golden tests
// below so they exercise a realistic row instead of an empty struct.
// 203.0.113.10 is a documentation-reserved address (RFC 5737), not a real
// VIP.
func exampleGLB() globalloadbalancer.LoadBalancer {
	return globalloadbalancer.LoadBalancer{
		ID:        "glb-1",
		Name:      "my-glb",
		Status:    "ACTIVE",
		Package:   "basic",
		Type:      "DNS",
		CreatedAt: "2026-01-01T00:00:00Z",
		VIPs: []globalloadbalancer.VIP{
			{ID: 1, Address: "203.0.113.10", Status: "ACTIVE", Region: "hcm-3", GlobalLoadBalancerID: "glb-1"},
		},
	}
}

// TestGoldenGlobalLoadBalancerListLoadBalancers checks list-load-balancers'
// exact output shape through the generic renderer, using the real
// globalloadbalancer.ListLoadBalancersOutput type.
func TestGoldenGlobalLoadBalancerListLoadBalancers(t *testing.T) {
	v := &globalloadbalancer.ListLoadBalancersOutput{Items: []globalloadbalancer.LoadBalancer{exampleGLB()}, Offset: 0, Limit: 25, Total: 1}
	checkGolden(t, "globalloadbalancer-list-load-balancers.json.golden", "json", "", v)
	checkGolden(t, "globalloadbalancer-list-load-balancers.table.golden", "table", "", v)
}

// TestGoldenGlobalLoadBalancerGetLoadBalancer checks get-load-balancer's
// exact output shape, {"LoadBalancer": {...}}, the shape every Get in this
// design wraps its resource in.
func TestGoldenGlobalLoadBalancerGetLoadBalancer(t *testing.T) {
	v := &globalloadbalancer.GetLoadBalancerOutput{LoadBalancer: exampleGLB()}
	checkGolden(t, "globalloadbalancer-get-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "globalloadbalancer-get-load-balancer.table.golden", "table", "", v)
}

// TestGlobalLoadBalancerCommandsMatchDesignTable checks the CLI reads
// design's "globalloadbalancer" table: the ten command names, each with the
// flags the table names.
func TestGlobalLoadBalancerCommandsMatchDesignTable(t *testing.T) {
	wantFlags := map[string][]string{
		"list-packages":        nil,
		"list-regions":         nil,
		"list-load-balancers":  {"name", "offset", "limit"},
		"get-load-balancer":    {"load-balancer-id"},
		"list-pools":           {"load-balancer-id"},
		"list-listeners":       {"load-balancer-id"},
		"get-listener":         {"load-balancer-id", "listener-id"},
		"list-pool-members":    {"load-balancer-id", "pool-id"},
		"get-pool-member":      {"load-balancer-id", "pool-id", "pool-member-id"},
		"list-usage-histories": {"load-balancer-id", "from", "to", "type"},
	}
	if got := opNames(globalLoadBalancerOps); len(got) != len(wantFlags) {
		t.Fatalf("globalloadbalancer ops = %v, want %d commands", got, len(wantFlags))
	}
	for _, op := range globalLoadBalancerOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Fatalf("unexpected globalloadbalancer command %q", op.name)
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatalf("%s: flagSpecsFor: %v", op.name, err)
		}
		var got []string
		for _, s := range specs {
			got = append(got, s.flagName)
		}
		if !equalStringSlices(got, want) {
			t.Errorf("%s flags = %v, want %v", op.name, got, want)
		}
	}
}

// TestGlobalLoadBalancerGetLoadBalancerMissingIDStopsBeforeAnyRequest checks
// the required-flag guard: get-load-balancer's --load-balancer-id is
// vngcloud:"required", so a missing flag must refuse the command with exit
// code 2 before any request reaches the server.
func TestGlobalLoadBalancerGetLoadBalancerMissingIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/global-load-balancers/": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "globalloadbalancer", "get-load-balancer"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --load-balancer-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestGlobalLoadBalancerGetPoolMemberMissingPoolMemberIDStopsBeforeAnyRequest
// is the same required-flag guard for get-pool-member, which has three
// required flags: giving only the first two must still refuse before any
// request.
func TestGlobalLoadBalancerGetPoolMemberMissingPoolMemberIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/global-load-balancers/": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "globalloadbalancer", "get-pool-member",
		"--load-balancer-id", "glb-1", "--pool-id", "pool-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --pool-member-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestGlobalLoadBalancerListLoadBalancersUsesTheGlobalScopedPathWithNoProjectID
// checks that list-load-balancers (a Global-scoped read, per the CLI reads
// design's scope table) sends its request with no project in the path and
// needs no --project-id, with its Name/Offset/Limit flags reaching the
// query string.
func TestGlobalLoadBalancerListLoadBalancersUsesTheGlobalScopedPathWithNoProjectID(t *testing.T) {
	body := `{"items":[{"id":"glb-1","name":"my-glb","status":"ACTIVE"}],"limit":10,"total":1,"offset":2}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/global-load-balancers": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "globalloadbalancer", "list-load-balancers",
		"--name", "my-glb", "--offset", "2", "--limit", "10",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	q, ok := fixture.queryFor("/v1/global-load-balancers")
	if !ok {
		t.Fatalf("no request observed")
	}
	if q != "limit=10&name=my-glb&offset=2" {
		t.Fatalf("query string = %q, want limit=10&name=my-glb&offset=2", q)
	}
	if got, ok := fixture.methodFor("/v1/global-load-balancers"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if got := stdout.String(); got == "" {
		t.Fatalf("stdout is empty")
	}
}
