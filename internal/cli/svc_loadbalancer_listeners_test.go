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

// exampleListener is a realistic Listener value, reused by the golden tests
// below.
func exampleListener() loadbalancer.Listener {
	return loadbalancer.Listener{
		UUID:          "listener-1",
		Name:          "https",
		Protocol:      "HTTPS",
		ProtocolPort:  443,
		AllowedCIDRs:  "10.0.0.0/24",
		DisplayStatus: "ACTIVE",
	}
}

// listenerLBReadyJSON is a not-busy GetLoadBalancer response for lb-1.
const listenerLBReadyJSON = `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`

func TestGoldenLoadBalancerCreateListener(t *testing.T) {
	v := &loadbalancer.CreateListenerOutput{Listener: exampleListener()}
	checkGolden(t, "loadbalancer-create-listener.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-create-listener.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerUpdateListener(t *testing.T) {
	v := &loadbalancer.UpdateListenerOutput{Listener: exampleListener()}
	checkGolden(t, "loadbalancer-update-listener.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-update-listener.table.golden", "table", "", v)
}

func TestGoldenLoadBalancerDeleteListener(t *testing.T) {
	v := &loadbalancer.DeleteListenerOutput{}
	checkGolden(t, "loadbalancer-delete-listener.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-delete-listener.table.golden", "table", "", v)
}

// TestLoadBalancerCreateListenerSendsRequestBody drives create-listener end
// to end with --no-wait: --allowed-cidrs is split on commas and joined back
// with commas on the wire, matching what the caller gave.
func TestLoadBalancerCreateListenerSendsRequestBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, listenerLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			data, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v (%s)", err, data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"uuid":"listener-1"}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-listener",
		"--load-balancer-id", "lb-1", "--name", "https", "--protocol", "HTTP", "--port", "80",
		"--allowed-cidrs", "10.0.0.0/24, 172.16.0.0/16", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-listener: %v (stderr=%s)", err, stderr.String())
	}
	if body["allowedCidrs"] != "10.0.0.0/24,172.16.0.0/16" {
		t.Fatalf("body[allowedCidrs] = %v, want the split-and-rejoined value", body["allowedCidrs"])
	}
	if body["listenerName"] != "https" || body["listenerProtocol"] != "HTTP" || body["listenerProtocolPort"] != 80.0 {
		t.Fatalf("body = %+v, want listenerName https, listenerProtocol HTTP, listenerProtocolPort 80", body)
	}
}

// TestLoadBalancerCreateListenerMissingAllowedCIDRsExitsWithZeroRequests
// checks that create-listener without --allowed-cidrs is refused with
// InvalidUsage before any request, per the design's required field.
func TestLoadBalancerCreateListenerMissingAllowedCIDRsExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-listener",
		"--load-balancer-id", "lb-1", "--name", "https", "--protocol", "HTTP", "--port", "80",
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

// TestLoadBalancerCreateListenerOpenCIDRRequiresYes checks the design's
// exposure guard: an AllowedCIDRs entry with prefix length 0 needs --yes;
// a narrower prefix needs none. The guard runs before any request.
func TestLoadBalancerCreateListenerOpenCIDRRequiresYes(t *testing.T) {
	tests := []struct {
		name        string
		cidrs       string
		yes         bool
		wantBlocked bool
	}{
		{"open CIDR without --yes", "0.0.0.0/0", false, true},
		{"open CIDR with --yes", "0.0.0.0/0", true, false},
		{"open CIDR among others without --yes", "10.0.0.0/24,0.0.0.0/0", false, true},
		{"narrow CIDR needs no --yes", "10.0.0.0/24", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/loadBalancers/lb-1":           jsonHandler(http.StatusOK, listenerLBReadyJSON),
				"/v2/proj-1/loadBalancers/lb-1/listeners": jsonHandler(http.StatusOK, `{"uuid":"listener-1"}`),
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := []string{
				"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-listener",
				"--load-balancer-id", "lb-1", "--name", "https", "--protocol", "HTTP", "--port", "80",
				"--allowed-cidrs", tt.cidrs, "--no-wait",
			}
			if tt.yes {
				args = append(args, "--yes")
			}
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if tt.wantBlocked {
				if err == nil {
					t.Fatal("expected the guard to refuse this command")
				}
				if got := exitCode(err); got != 2 {
					t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
				}
				if n := fixture.requestCount(); n != 0 {
					t.Fatalf("requestCount = %d, want 0", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("create-listener: %v (stderr=%s)", err, stderr.String())
			}
		})
	}
}

// TestLoadBalancerUpdateListenerSendsMergedBody drives update-listener end
// to end with --no-wait: --timeout-client alone still resends the read
// AllowedCIDRs and InsertHeaders unchanged.
func TestLoadBalancerUpdateListenerSendsMergedBody(t *testing.T) {
	var body map[string]any
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, listenerLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"listener-1","protocol":"HTTP","protocolPort":80,`+
					`"timeoutClient":60,"timeoutMember":60,"timeoutConnection":10,`+
					`"allowedCidrs":"10.0.0.0/24","progressStatus":"CREATED"}}`)(w, r)
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
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-listener",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--timeout-client", "30", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-listener: %v (stderr=%s)", err, stderr.String())
	}
	if body["timeoutClient"] != 30.0 {
		t.Fatalf("body[timeoutClient] = %v, want 30", body["timeoutClient"])
	}
	if body["allowedCidrs"] != "10.0.0.0/24" {
		t.Fatalf("body[allowedCidrs] = %v, want the read value resent", body["allowedCidrs"])
	}
}

// TestLoadBalancerUpdateListenerOpenCIDRRequiresYes checks that
// update-listener's own --allowed-cidrs needs --yes for a /0 prefix, the
// same guard create-listener runs.
func TestLoadBalancerUpdateListenerOpenCIDRRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-listener",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--allowed-cidrs", "0.0.0.0/0", "--no-wait",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected the guard to refuse this command")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerUpdateListenerNoFieldSetExitsWithZeroRequests checks that
// update-listener with no field set is refused with InvalidUsage before any
// request.
func TestLoadBalancerUpdateListenerNoFieldSetExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "update-listener",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1",
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

// TestLoadBalancerDeleteListenerWithoutYesExitsWithZeroRequests checks that
// delete-listener, Destructive per the design, is refused without --yes
// before any request.
func TestLoadBalancerDeleteListenerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-listener",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--no-wait",
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

// TestLoadBalancerDeleteListenerWithYesSendsDelete drives delete-listener
// with --yes and --no-wait.
func TestLoadBalancerDeleteListenerWithYesSendsDelete(t *testing.T) {
	var deleteCalled bool
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, listenerLBReadyJSON),
		"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"listener-1","progressStatus":"CREATED"}}`)(w, r)
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
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "delete-listener",
		"--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--yes", "--no-wait",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-listener: %v (stderr=%s)", err, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected a DELETE request")
	}
}

// TestLoadBalancerListenerWritesReadOnlyRefusedWithZeroRequests checks the
// design's read-only rule across every listener write.
func TestLoadBalancerListenerWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	tests := []struct {
		name string
		args []string
	}{
		{"create-listener", []string{"loadbalancer", "create-listener", "--load-balancer-id", "lb-1", "--name", "https", "--protocol", "HTTP", "--port", "80", "--allowed-cidrs", "10.0.0.0/24"}},
		{"update-listener", []string{"loadbalancer", "update-listener", "--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--timeout-client", "30"}},
		{"delete-listener", []string{"loadbalancer", "delete-listener", "--load-balancer-id", "lb-1", "--listener-id", "listener-1", "--yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refuse := func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v2/proj-1/loadBalancers/lb-1":                      refuse,
				"/v2/proj-1/loadBalancers/lb-1/listeners":            refuse,
				"/v2/proj-1/loadBalancers/lb-1/listeners/listener-1": refuse,
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
