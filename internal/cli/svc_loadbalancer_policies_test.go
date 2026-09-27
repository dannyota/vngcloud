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

// examplePolicy is a realistic Policy value, reused by the golden tests
// below.
func examplePolicy() loadbalancer.Policy {
	return loadbalancer.Policy{
		UUID:           "policy-1",
		Name:           "route-api",
		Action:         "REDIRECT_TO_POOL",
		RedirectPoolID: "pool-1",
		DisplayStatus:  "ACTIVE",
	}
}

// policyLBReadyJSON is a not-busy GetLoadBalancer response for lb-1.
const policyLBReadyJSON = `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`

func TestGoldenLoadBalancerCreatePolicy(t *testing.T) {
	v := &loadbalancer.CreatePolicyOutput{Policy: examplePolicy()}
	checkGolden(t, "loadbalancer-create-policy.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-create-policy.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerUpdatePolicy(t *testing.T) {
	v := &loadbalancer.UpdatePolicyOutput{Policy: examplePolicy()}
	checkGolden(t, "loadbalancer-update-policy.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-update-policy.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerDeletePolicy(t *testing.T) {
	v := &loadbalancer.DeletePolicyOutput{}
	checkGolden(t, "loadbalancer-delete-policy.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-delete-policy.table.golden", "table", "", v)
}

// TestLoadBalancerCreatePolicySendsRequestBody drives create-policy end to
// end with --no-wait, including Rules through --cli-input-json.
func TestLoadBalancerCreatePolicySendsRequestBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, policyLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v (%s)", err, data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"policy-1"}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--name", "route-api",
		"--action", "REDIRECT_TO_POOL", "--redirect-pool-id", "pool-1",
		"--cli-input-json", `{"Rules":[{"Type":"PATH","CompareType":"STARTS_WITH","Value":"/api"}]}`,
		"--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-policy: %v (stderr=%s)", err, stderr.String())
	}
	if body["name"] != "route-api" || body["action"] != "REDIRECT_TO_POOL" || body["redirectPoolId"] != "pool-1" {
		t.Fatalf("body = %+v, want name route-api, action REDIRECT_TO_POOL, redirectPoolId pool-1", body)
	}
	rules, ok := body["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("body[rules] = %+v, want 1 entry", body["rules"])
	}
	rule, ok := rules[0].(map[string]any)
	if !ok || rule["ruleType"] != "PATH" || rule["compareType"] != "STARTS_WITH" || rule["ruleValue"] != "/api" {
		t.Fatalf("rules[0] = %+v, want the PATH rule", rules[0])
	}
}

// TestLoadBalancerCreatePolicyRedirectFieldMismatchExitsWithZeroRequests
// checks that REDIRECT_TO_POOL with --redirect-url set is refused with
// InvalidUsage before any request.
func TestLoadBalancerCreatePolicyRedirectFieldMismatchExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--name", "route-api",
		"--action", "REDIRECT_TO_POOL", "--redirect-pool-id", "pool-1", "--redirect-url", "https://example.com",
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

// TestLoadBalancerUpdatePolicySendsMergedBody drives update-policy end to
// end with --no-wait: --action alone still resends the read redirect pool,
// KeepQueryString, and Rules unchanged.
func TestLoadBalancerUpdatePolicySendsMergedBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, policyLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"policy-1","action":"REDIRECT_TO_POOL",`+
					`"redirectPoolId":"pool-1","keepQueryString":true,`+
					`"l7Rules":[{"compareType":"EQUAL_TO","ruleValue":"/","ruleType":"PATH"}],`+
					`"progressStatus":"CREATED"}}`)(w, r)
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
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1",
		"--redirect-http-code", "301", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-policy: %v (stderr=%s)", err, stderr.String())
	}
	if body["redirectHttpCode"] != 301.0 {
		t.Fatalf("body[redirectHttpCode] = %v, want 301", body["redirectHttpCode"])
	}
	if body["action"] != "REDIRECT_TO_POOL" || body["redirectPoolId"] != "pool-1" || body["keepQueryString"] != true {
		t.Fatalf("body = %+v, want the read action, redirectPoolId, and keepQueryString resent", body)
	}
	rules, ok := body["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("body[rules] = %+v, want the read rule resent", body["rules"])
	}
}

// TestLoadBalancerUpdatePolicyNoFieldSetExitsWithZeroRequests checks that
// update-policy with no field set is refused with InvalidUsage before any
// request.
func TestLoadBalancerUpdatePolicyNoFieldSetExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1",
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

// TestLoadBalancerDeletePolicyWithoutYesExitsWithZeroRequests checks that
// delete-policy, Destructive per the design, is refused without --yes
// before any request.
func TestLoadBalancerDeletePolicyWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1", "--no-wait",
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

// TestLoadBalancerDeletePolicyWithYesSendsDelete drives delete-policy with
// --yes and --no-wait.
func TestLoadBalancerDeletePolicyWithYesSendsDelete(t *testing.T) {
	var deleteCalled bool
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, policyLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies/policy-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"policy-1","progressStatus":"CREATED"}}`)(w, r)
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
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-policy",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1", "--yes", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-policy: %v (stderr=%s)", err, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected a DELETE request")
	}
}

// TestLoadBalancerPolicyWritesReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule across every policy write.
func TestLoadBalancerPolicyWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	tests := []struct {
		name string
		args []string
	}{
		{"create-policy", []string{"loadbalancer", "create-policy", "--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--name", "p", "--action", "REDIRECT_TO_POOL", "--redirect-pool-id", "pool-1"}},
		{"update-policy", []string{"loadbalancer", "update-policy", "--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1", "--keep-query-string"}},
		{"delete-policy", []string{"loadbalancer", "delete-policy", "--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--policy-id", "policy-1", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refuse := func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/loadBalancers/lb-1":                                          refuse,
				"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies":          refuse,
				"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1/l7policies/policy-1": refuse,
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
