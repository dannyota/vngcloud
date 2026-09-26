package cli

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud/loadbalancer"
)

// exampleLoadBalancer is a realistic LoadBalancer value, reused by the
// golden tests below so they exercise every field rather than an empty
// struct.
func exampleLoadBalancer() loadbalancer.LoadBalancer {
	return loadbalancer.LoadBalancer{
		UUID:               "lb-1",
		Name:               "example-lb",
		DisplayStatus:      "ACTIVE",
		Address:            "203.0.113.10",
		PrivateSubnetID:    "subnet-1",
		PrivateSubnetCIDR:  "10.0.0.0/24",
		Type:               "Layer 4",
		DisplayType:        "Layer 4",
		LoadBalancerSchema: "INTERNET",
		PackageID:          "pkg-1",
		Description:        "example load balancer",
		Location:           "hcm-3",
		CreatedAt:          "2026-01-01T00:00:00Z",
		UpdatedAt:          "2026-01-02T00:00:00Z",
		Status:             "ACTIVE",
		BackendSubnetID:    "subnet-1",
		AutoScalable:       true,
		ZoneID:             "zone-a",
		MinSize:            1,
		MaxSize:            2,
		TotalNodes:         1,
		Nodes: []loadbalancer.Node{
			{Status: "ACTIVE", ZoneID: "zone-a", ZoneName: "Zone A", SubnetID: "subnet-1"},
		},
	}
}

// TestGoldenLoadBalancerListLoadBalancers checks list-load-balancers' exact
// output shape: {"Items": [...]} plus the PagedList's page fields.
func TestGoldenLoadBalancerListLoadBalancers(t *testing.T) {
	v := &loadbalancer.ListLoadBalancersOutput{
		Items: []loadbalancer.LoadBalancer{exampleLoadBalancer()},
		Page:  1, PageSize: 10, TotalPage: 1, TotalItem: 1,
	}
	checkGolden(t, "loadbalancer-list-load-balancers.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-list-load-balancers.table.golden", "table", "", v)
}

// TestGoldenLoadBalancerGetLoadBalancer checks get-load-balancer's exact
// output shape, {"LoadBalancer": {...}}, per the CLI reads design's
// "Commands": every Get wraps one resource.
func TestGoldenLoadBalancerGetLoadBalancer(t *testing.T) {
	v := &loadbalancer.GetLoadBalancerOutput{LoadBalancer: exampleLoadBalancer()}
	checkGolden(t, "loadbalancer-get-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-get-load-balancer.table.golden", "table", "", v)
}

// TestLoadBalancerCommandsMatchDesignTable checks the CLI reads design's
// "loadbalancer" table: all 14 command names, their flags in Input
// declaration order, and which of those flags are required.
func TestLoadBalancerCommandsMatchDesignTable(t *testing.T) {
	type wantFlag struct {
		name     string
		required bool
	}
	wantFlags := map[string][]wantFlag{
		"list-load-balancers":     {{"name", false}, {"page", false}, {"size", false}},
		"get-load-balancer":       {{"load-balancer-id", true}},
		"list-packages":           {{"zone-id", false}},
		"list-certificates":       {{"name", false}, {"page", false}, {"size", false}},
		"get-certificate":         {{"certificate-id", true}},
		"list-listeners":          {{"load-balancer-id", true}},
		"get-listener":            {{"load-balancer-id", true}, {"listener-id", true}},
		"list-pools":              {{"load-balancer-id", true}},
		"get-pool":                {{"load-balancer-id", true}, {"pool-id", true}},
		"get-pool-health-monitor": {{"load-balancer-id", true}, {"pool-id", true}},
		"list-pool-members":       {{"load-balancer-id", true}, {"pool-id", true}},
		"list-policies":           {{"load-balancer-id", true}, {"listener-id", true}},
		"get-policy":              {{"load-balancer-id", true}, {"listener-id", true}, {"policy-id", true}},
		"list-tags":               {{"load-balancer-id", true}},
	}
	if got := opNames(loadbalancerOps); len(got) != len(wantFlags) {
		t.Fatalf("loadbalancer ops = %v, want %d commands", got, len(wantFlags))
	}
	for _, op := range loadbalancerOps {
		want, ok := wantFlags[op.name]
		if !ok {
			t.Fatalf("unexpected loadbalancer command %q", op.name)
		}
		specs, err := flagSpecsFor(op.newInput())
		if err != nil {
			t.Fatalf("%s: flagSpecsFor: %v", op.name, err)
		}
		required := requiredFieldNames(op.newInput())
		var got []wantFlag
		for _, s := range specs {
			got = append(got, wantFlag{s.flagName, required[s.fieldName]})
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s flags = %+v, want %+v", op.name, got, want)
		}
	}
}

// requiredFieldNames returns the set of inputPtr's exported field names
// tagged vngcloud:"required", by Go field name.
func requiredFieldNames(inputPtr any) map[string]bool {
	v := reflect.ValueOf(inputPtr).Elem()
	t := v.Type()
	names := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("vngcloud") == "required" {
			names[f.Name] = true
		}
	}
	return names
}

// TestLoadBalancerGetPolicyMissingPolicyIDStopsBeforeAnyRequest checks the
// CLI design's required-flag guard against get-policy, the one loadbalancer
// command with three required flags: giving only the first two must still
// refuse the command with exit code 2 before any request reaches the
// server.
func TestLoadBalancerGetPolicyMissingPolicyIDStopsBeforeAnyRequest(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies/pol-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "get-policy", "--load-balancer-id", "lb-1", "--listener-id", "listener-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --policy-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerListListenersUsesTheLoadBalancerScopedPath checks that
// list-listeners (a Project-scoped read, per the CLI reads design's scope
// table) sends its request under the given --project-id and
// --load-balancer-id, and decodes the listener's fields.
func TestLoadBalancerListListenersUsesTheLoadBalancerScopedPath(t *testing.T) {
	body := `{"data":[{"uuid":"listener-1","name":"https","protocol":"HTTPS","protocolPort":443,"displayStatus":"ACTIVE"}]}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/listeners": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "list-listeners", "--load-balancer-id", "lb-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v2/proj-1/loadBalancers/lb-1/listeners"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"Name": "https"`) {
		t.Fatalf("stdout = %s, want the decoded listener name", stdout.String())
	}
}

// TestLoadBalancerGetPoolHealthMonitorUsesNestedPath checks that
// get-pool-health-monitor threads both required flags into the two-level
// nested path the SDK builds (loadBalancers/{id}/pools/{id}/healthMonitor),
// and decodes a non-empty result, so a Get that decoded empty would fail
// here even though HealthMonitor carries no ID field to compare against a
// list, unlike the vMonitor-style Get checks.
func TestLoadBalancerGetPoolHealthMonitorUsesNestedPath(t *testing.T) {
	body := `{"data":{"healthCheckProtocol":"HTTP","interval":30,"healthyThreshold":3,"unhealthyThreshold":3,"timeout":5,"displayStatus":"ACTIVE"}}`
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/healthMonitor": jsonHandler(http.StatusOK, body),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1",
		"loadbalancer", "get-pool-health-monitor", "--load-balancer-id", "lb-1", "--pool-id", "pool-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v2/proj-1/loadBalancers/lb-1/pools/pool-1/healthMonitor"); !ok || got != http.MethodGet {
		t.Fatalf("method = %q, ok=%v, want GET", got, ok)
	}
	if !strings.Contains(stdout.String(), `"HealthCheckProtocol": "HTTP"`) {
		t.Fatalf("stdout = %s, want the decoded health monitor protocol", stdout.String())
	}
}

// TestLoadBalancerListPackagesZoneIDIsOptional checks that --zone-id sets
// the zoneId query parameter when given, and that list-packages sends no
// such parameter at all when it is left unset, matching ListPackagesInput's
// only field having no vngcloud:"required" tag.
func TestLoadBalancerListPackagesZoneIDIsOptional(t *testing.T) {
	const path = "/v2/proj-1/loadBalancers/packages"
	body := `{"listData":[{"uuid":"pkg-1","name":"standard","type":"Layer 4"}]}`
	newFixture := func() *svcFixture {
		return newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
			path: jsonHandler(http.StatusOK, body),
		})
	}

	t.Run("unset", func(t *testing.T) {
		fixture := newFixture()
		root, _, stderr := newSvcRoot(t, fixture)
		root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "list-packages"})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		q, ok := fixture.queryFor(path)
		if !ok {
			t.Fatalf("no request observed")
		}
		if strings.Contains(q, "zoneId") {
			t.Fatalf("query string = %q, want no zoneId", q)
		}
	})

	t.Run("set", func(t *testing.T) {
		fixture := newFixture()
		root, _, stderr := newSvcRoot(t, fixture)
		root.SetArgs([]string{
			"--region", "hcm-3", "--project-id", "proj-1",
			"loadbalancer", "list-packages", "--zone-id", "zone-a",
		})
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		q, ok := fixture.queryFor(path)
		if !ok {
			t.Fatalf("no request observed")
		}
		if !strings.Contains(q, "zoneId=zone-a") {
			t.Fatalf("query string = %q, want zoneId=zone-a", q)
		}
	})
}
