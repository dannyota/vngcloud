package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

// examplePool is a realistic Pool value, reused by the golden tests below.
func examplePool() loadbalancer.Pool {
	return loadbalancer.Pool{
		UUID:              "pool-1",
		Name:              "pool-1",
		Protocol:          "HTTP",
		LoadBalanceMethod: "ROUND_ROBIN",
		DisplayStatus:     "ACTIVE",
		ProgressStatus:    "CREATED",
	}
}

// poolLBReadyJSON is a not-busy GetLoadBalancer response for lb-1.
const poolLBReadyJSON = `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`

func TestGoldenLoadBalancerCreatePool(t *testing.T) {
	v := &loadbalancer.CreatePoolOutput{Pool: examplePool()}
	checkGolden(t, "loadbalancer-create-pool.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-create-pool.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerUpdatePool(t *testing.T) {
	v := &loadbalancer.UpdatePoolOutput{Pool: examplePool()}
	checkGolden(t, "loadbalancer-update-pool.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-update-pool.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerDeletePool(t *testing.T) {
	v := &loadbalancer.DeletePoolOutput{}
	checkGolden(t, "loadbalancer-delete-pool.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-delete-pool.table.golden", "table", "", v)
}

// TestLoadBalancerCreatePoolSendsRequestBody drives create-pool end to end
// with --no-wait: every flag reaches the create body, with the design's
// defaults applied for the fields left unset.
func TestLoadBalancerCreatePoolSendsRequestBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v (%s)", err, data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"pool-1"}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-pool",
		"--load-balancer-id", "lb-1", "--name", "pool-1", "--protocol", "HTTP",
		"--health-check-protocol", "HTTP", "--health-check-domain-name", "example.com", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-pool: %v (stderr=%s)", err, stderr.String())
	}
	if body["poolName"] != "pool-1" || body["poolProtocol"] != "HTTP" || body["algorithm"] != "ROUND_ROBIN" {
		t.Fatalf("body = %+v, want poolName pool-1, poolProtocol HTTP, algorithm ROUND_ROBIN", body)
	}
	hm, ok := body["healthMonitor"].(map[string]any)
	if !ok {
		t.Fatalf("body[healthMonitor] missing or wrong type: %+v", body)
	}
	if hm["healthCheckProtocol"] != "HTTP" || hm["domainName"] != "example.com" || hm["healthyThreshold"] != 3.0 {
		t.Fatalf("healthMonitor = %+v, want the design's HTTP fields and default thresholds", hm)
	}
}

// TestLoadBalancerCreatePoolHTTPFieldOnNonHTTPCheckExitsWithZeroRequests
// checks that an HTTP health check field set alongside a non-HTTP
// --health-check-protocol is refused with InvalidUsage before any request.
func TestLoadBalancerCreatePoolHTTPFieldOnNonHTTPCheckExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-pool",
		"--load-balancer-id", "lb-1", "--name", "pool-1", "--protocol", "TCP",
		"--health-check-protocol", "TCP", "--health-check-path", "/",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an InvalidUsage refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerUpdatePoolSendsMergedBody checks update-pool's read-merge:
// --algorithm alone still resends the health monitor exactly as read.
func TestLoadBalancerUpdatePoolSendsMergedBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"pool-1","loadBalanceMethod":"ROUND_ROBIN","progressStatus":"CREATED"}}`)(w, r)
			case http.MethodPut:
				defer func() { _ = r.Body.Close() }()
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &body); err != nil {
					t.Fatalf("decode body: %v (%s)", err, data)
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1/healthMonitor": jsonHandler(http.StatusOK,
			`{"data":{"healthCheckProtocol":"TCP","healthyThreshold":5,"unhealthyThreshold":5,"interval":20,"timeout":10}}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-pool",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--algorithm", "LEAST_CONNECTIONS", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-pool: %v (stderr=%s)", err, stderr.String())
	}
	if body["algorithm"] != "LEAST_CONNECTIONS" {
		t.Fatalf("body[algorithm] = %v, want LEAST_CONNECTIONS", body["algorithm"])
	}
	hm, ok := body["healthMonitor"].(map[string]any)
	if !ok {
		t.Fatalf("body[healthMonitor] missing or wrong type: %+v", body)
	}
	if hm["healthyThreshold"] != 5.0 || hm["timeout"] != 10.0 {
		t.Fatalf("healthMonitor = %+v, want the read values resent unchanged", hm)
	}
}

// TestLoadBalancerUpdatePoolNoFieldSetExitsWithZeroRequests checks that
// update-pool with no field set is refused with InvalidUsage before any
// request.
func TestLoadBalancerUpdatePoolNoFieldSetExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-pool",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an InvalidUsage refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerDeletePoolWithoutYesExitsWithZeroRequests checks that
// delete-pool, Destructive per the design, is refused without --yes before
// any request.
func TestLoadBalancerDeletePoolWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/listeners": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-pool",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a destructive refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerDeletePoolInUseExitsResourceInUse checks the design's
// pre-delete guard: a listener naming the pool as its default pool refuses
// the delete with ResourceInUse and exit code 1, before any DELETE.
func TestLoadBalancerDeletePoolInUseExitsResourceInUse(t *testing.T) {
	var deleteCalled bool
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/listeners": jsonHandler(http.StatusOK, `{"data":[{"uuid":"listener-1","defaultPoolId":"pool-1"}]}`),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete {
				deleteCalled = true
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-pool",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--yes", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a ResourceInUse refusal")
	}
	if got := classify(err).Code; got != "ResourceInUse" {
		t.Fatalf("Code = %q, want ResourceInUse (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if deleteCalled {
		t.Fatal("expected no DELETE request")
	}
}

// TestLoadBalancerDeletePoolWithYesSendsDelete drives delete-pool with --yes
// and --no-wait past the pre-delete listener check.
func TestLoadBalancerDeletePoolWithYesSendsDelete(t *testing.T) {
	var deleteCalled bool
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1/listeners": jsonHandler(http.StatusOK, `{"data":[]}`),
		"/v2/proj-1/loadBalancers/lb-1":           jsonHandler(http.StatusOK, poolLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"pool-1","progressStatus":"CREATED"}}`)(w, r)
			case http.MethodDelete:
				deleteCalled = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-pool",
		"--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--yes", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-pool: %v (stderr=%s)", err, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected a DELETE request")
	}
}

// TestLoadBalancerPoolWritesReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule across every pool write: each is refused with
// exit 2 before any request.
func TestLoadBalancerPoolWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	tests := []struct {
		name string
		args []string
	}{
		{"create-pool", []string{"loadbalancer", "create-pool", "--load-balancer-id", "lb-1", "--name", "p", "--protocol", "HTTP", "--health-check-protocol", "TCP"}},
		{"update-pool", []string{"loadbalancer", "update-pool", "--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--algorithm", "SOURCE_IP"}},
		{"delete-pool", []string{"loadbalancer", "delete-pool", "--load-balancer-id", "lb-1", "--pool-id", "pool-1", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refuse := func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/loadBalancers/lb-1":              refuse,
				"/v2/proj-1/loadBalancers/lb-1/pools":        refuse,
				"/v2/proj-1/loadBalancers/lb-1/pools/pool-1": refuse,
				"/v2/proj-1/loadBalancers/lb-1/listeners":    refuse,
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent"}, tt.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := fixture.requestCount(); got != 0 {
				t.Fatalf("requestCount = %d, want 0", got)
			}
		})
	}
}
