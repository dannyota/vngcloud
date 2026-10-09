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

// TestGoldenLoadBalancerResizeLoadBalancer checks resize-load-balancer's
// exact output shape: {"LoadBalancer": {...}, "QuotedPrice": ..., "Changed": ...}.
func TestGoldenLoadBalancerResizeLoadBalancer(t *testing.T) {
	v := &loadbalancer.ResizeLoadBalancerOutput{LoadBalancer: exampleLoadBalancer(), QuotedPrice: 400000, Changed: true}
	checkGolden(t, "loadbalancer-resize-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-resize-load-balancer.table.golden", "table", "", v)
}

// getLoadBalancerJSON renders one GetLoadBalancer response for lb-1, CREATED
// and holding packageID: every resize test below reads a load balancer
// already settled, never a busy one.
func getLoadBalancerJSON(packageID string) string {
	return `{"data":{"uuid":"lb-1","packageId":"` + packageID + `","progressStatus":"CREATED"}}`
}

// validResizeLoadBalancerArgs is the flag set resize-load-balancer needs to
// pass its required-field check.
var validResizeLoadBalancerArgs = []string{
	"loadbalancer", "resize-load-balancer",
	"--load-balancer-id", "lb-1", "--package-id", "pkg-2", "--no-wait",
}

// TestLoadBalancerResizeLoadBalancerSendsRequestBody drives resize-load-balancer
// end to end with --no-wait: it reads the load balancer, quotes the change,
// then sends the resize PUT with packageId alone, per the vLB writes design.
func TestLoadBalancerResizeLoadBalancerSendsRequestBody(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, getLoadBalancerJSON("pkg-1")),
		"/v1/price":                     jsonHandler(http.StatusOK, `{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers/lb-1/resize": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Fatalf("method = %s, want PUT", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validResizeLoadBalancerArgs, "--max-price", "800000")...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resize-load-balancer: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("resize body is not valid JSON: %v (%s)", err, putBody)
	}
	if decoded["packageId"] != "pkg-2" {
		t.Fatalf("resize body = %+v, want packageId pkg-2", decoded)
	}
	if got := stdout.String(); !strings.Contains(got, `"QuotedPrice": 800000`) || !strings.Contains(got, `"Changed": true`) {
		t.Fatalf("stdout = %s, want QuotedPrice 800000 and Changed true", got)
	}
}

// TestLoadBalancerResizeLoadBalancerSamePackageIsANoOp checks that
// --package-id equal to the load balancer's current package returns Changed
// false, quoting and sending nothing beyond the initial read.
func TestLoadBalancerResizeLoadBalancerSamePackageIsANoOp(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, getLoadBalancerJSON("pkg-2")),
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/v2/proj-1/loadBalancers/lb-1/resize": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validResizeLoadBalancerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("resize-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, `"Changed": false`) {
		t.Fatalf("stdout = %s, want Changed false", got)
	}
	if got := fixture.requestCount(); got != 1 {
		t.Fatalf("requestCount = %d, want 1 (the read only)", got)
	}
}

// TestLoadBalancerResizeLoadBalancerDefaultMaxPriceRefusesOrder checks the
// design's price guard: --max-price left at its default of 0 refuses a
// resize whose quote prices above 0, with PriceAboveMax and exit code 1, and
// no resize PUT ever sent.
func TestLoadBalancerResizeLoadBalancerDefaultMaxPriceRefusesOrder(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, getLoadBalancerJSON("pkg-1")),
		"/v1/price":                     jsonHandler(http.StatusOK, `{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers/lb-1/resize": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validResizeLoadBalancerArgs...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a PriceAboveMax refusal")
	}
	if got := classify(err).Code; got != "PriceAboveMax" {
		t.Fatalf("Code = %q, want PriceAboveMax (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if got := fixture.requestCount(); got != 3 {
		t.Fatalf("requestCount = %d, want 3 (the current-package read, the pre-write busy read, and the quote)", got)
	}
}

// TestLoadBalancerResizeLoadBalancerMaxPriceNaNExitsWithZeroRequests checks
// that a NaN --max-price is refused with InvalidUsage and exit code 2 before
// any request.
func TestLoadBalancerResizeLoadBalancerMaxPriceNaNExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validResizeLoadBalancerArgs, "--max-price", "NaN")...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a NaN MaxPrice refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}

// TestLoadBalancerResizeLoadBalancerBusyPutRefusalExitsResourceBusy checks
// that a busy refusal from the resize PUT itself (a load balancer that
// turned busy between the pre-write read and the PUT) classifies as
// ResourceBusy with exit code 1, and is never resent: resize's own Once
// rule means the design's busy resend never applies to it.
func TestLoadBalancerResizeLoadBalancerBusyPutRefusalExitsResourceBusy(t *testing.T) {
	var resizeCalls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": jsonHandler(http.StatusOK, getLoadBalancerJSON("pkg-1")),
		"/v1/price":                     jsonHandler(http.StatusOK, `{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers/lb-1/resize": func(w http.ResponseWriter, r *http.Request) {
			resizeCalls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"load balancer id lb-1 is not ready"}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validResizeLoadBalancerArgs, "--max-price", "800000")...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a busy refusal")
	}
	if got := classify(err).Code; got != "ResourceBusy" {
		t.Fatalf("Code = %q, want ResourceBusy (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if resizeCalls != 1 {
		t.Fatalf("resize PUT calls = %d, want 1 (never resent)", resizeCalls)
	}
}

// TestLoadBalancerResizeLoadBalancerReadOnlyRefusedWithZeroRequests checks
// the design's read-only rule: resize-load-balancer is a Write, so a
// read-only profile refuses it with exit 2 before any request, including
// the initial read.
func TestLoadBalancerResizeLoadBalancerReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent"}, validResizeLoadBalancerArgs...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if got := classify(err).Code; got != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2", got)
	}
	if got := fixture.requestCount(); got != 0 {
		t.Fatalf("requestCount = %d, want 0", got)
	}
}
