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
	"danny.vn/vngcloud/pricing"
)

// TestGoldenLoadBalancerQuoteCreateLoadBalancer checks
// quote-create-load-balancer's exact output shape: the same
// pricing.GetQuoteOutput shape pricing get-quote, compute quote-create-server,
// and volume quote-create-volume all use.
func TestGoldenLoadBalancerQuoteCreateLoadBalancer(t *testing.T) {
	v := &pricing.GetQuoteOutput{
		OptimumPrice:  400000,
		OriginalPrice: 400000,
		Properties:    []pricing.PriceProperty{{Name: "LOAD BALANCER", OptimumPrice: 400000, MonthlyPrice: 400000}},
	}
	checkGolden(t, "loadbalancer-quote-create-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-quote-create-load-balancer.table.golden", "table", "", v)
}

// TestGoldenLoadBalancerQuoteResizeLoadBalancer checks
// quote-resize-load-balancer's exact output shape, the same
// pricing.GetQuoteOutput shape quote-create-load-balancer uses.
func TestGoldenLoadBalancerQuoteResizeLoadBalancer(t *testing.T) {
	v := &pricing.GetQuoteOutput{
		OptimumPrice:  800000,
		OriginalPrice: 800000,
		Properties:    []pricing.PriceProperty{{Name: "LOAD BALANCER", OptimumPrice: 800000, MonthlyPrice: 800000}},
	}
	checkGolden(t, "loadbalancer-quote-resize-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-quote-resize-load-balancer.table.golden", "table", "", v)
}

// validQuoteCreateLoadBalancerArgs sets every quote-create-load-balancer
// flag, so the tests below can confirm the unpriced flags are accepted and
// still stay out of the request body.
var validQuoteCreateLoadBalancerArgs = []string{
	"loadbalancer", "quote-create-load-balancer",
	"--name", "lb-1", "--package-id", "pkg-1", "--type", "Layer 4",
	"--scheme", "Internal", "--subnet-id", "subnet-1", "--zone-id", "zone-1",
}

// TestLoadBalancerQuoteCreateLoadBalancerSendsRequestBody drives
// quote-create-load-balancer with every flag set and checks that the request
// body holds only the priced keys: the other flags are accepted but never
// sent.
func TestLoadBalancerQuoteCreateLoadBalancerSendsRequestBody(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validQuoteCreateLoadBalancerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/price"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["resourceType"] != "load-balancer" || decoded["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", decoded)
	}
	info, ok := decoded["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", decoded)
	}
	want := map[string]any{
		"packageId": "pkg-1", "zoneId": "zone-1",
		"period": float64(1), "isPoc": false, "isBuyMorePoc": false,
	}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v", k, info[k], v)
		}
	}

	var out pricing.GetQuoteOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.OptimumPrice != 400000 {
		t.Fatalf("OptimumPrice = %v, want 400000", out.OptimumPrice)
	}
}

// TestLoadBalancerQuoteCreateLoadBalancerHasNoMaxPriceOrNoWaitFlag checks
// that MaxPrice and NoWait, which only govern a future create-load-balancer,
// register no flag on quote-create-load-balancer.
func TestLoadBalancerQuoteCreateLoadBalancerHasNoMaxPriceOrNoWaitFlag(t *testing.T) {
	cmd := newLoadBalancerCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"quote-create-load-balancer"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, name := range []string{"max-price", "no-wait"} {
		if f := sub.Flags().Lookup(name); f != nil {
			t.Fatalf("quote-create-load-balancer registered its own --%s flag: %+v", name, f)
		}
	}
}

// TestLoadBalancerQuoteCreateLoadBalancerMissingRequiredFieldExitsWithZeroRequests
// checks that quote-create-load-balancer without --package-id, a priced
// field, fails the required-field check before any request.
func TestLoadBalancerQuoteCreateLoadBalancerMissingRequiredFieldExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "quote-create-load-balancer",
		"--name", "lb-1", "--type", "Layer 4", "--scheme", "Internal",
		"--subnet-id", "subnet-1", "--zone-id", "zone-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --package-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerQuoteCreateLoadBalancerIsAReadUnderReadOnly checks the CLI
// design's rule that quotes are reads: a read-only profile must not refuse
// quote-create-load-balancer the way it would a real write, and the request
// still reaches the fixture.
func TestLoadBalancerQuoteCreateLoadBalancerIsAReadUnderReadOnly(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`),
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent"}, validQuoteCreateLoadBalancerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-create-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (quote-create-load-balancer must run as a read under read-only)", n)
	}
}

// validQuoteResizeLoadBalancerArgs is the flag set quote-resize-load-balancer
// needs to pass its required-field check.
var validQuoteResizeLoadBalancerArgs = []string{
	"loadbalancer", "quote-resize-load-balancer",
	"--load-balancer-id", "lb-1", "--package-id", "pkg-2",
}

// TestLoadBalancerQuoteResizeLoadBalancerSendsRequestBody drives
// quote-resize-load-balancer with every required flag, checking the exact
// request body the CLI builds from that merge: the SDK's own test
// (loadbalancer.TestQuoteResizeLoadBalancerSendsQuoteBody) checks the body
// builder itself, not that the flags reach it field for field.
func TestLoadBalancerQuoteResizeLoadBalancerSendsRequestBody(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validQuoteResizeLoadBalancerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-resize-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/v1/price"); !ok || got != http.MethodPost {
		t.Fatalf("method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["resourceType"] != "load-balancer" || decoded["action"] != "resize" {
		t.Fatalf("unexpected resourceType/action: %+v", decoded)
	}
	info, ok := decoded["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", decoded)
	}
	want := map[string]any{"packageId": "pkg-2", "loadBalancerId": "lb-1"}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v", k, info[k], v)
		}
	}

	var out pricing.GetQuoteOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if out.OptimumPrice != 800000 {
		t.Fatalf("OptimumPrice = %v, want 800000", out.OptimumPrice)
	}
}

// TestLoadBalancerQuoteResizeLoadBalancerHasNoMaxPriceOrNoWaitFlag checks
// that MaxPrice and NoWait, which only govern a future resize-load-balancer,
// register no flag on quote-resize-load-balancer.
func TestLoadBalancerQuoteResizeLoadBalancerHasNoMaxPriceOrNoWaitFlag(t *testing.T) {
	cmd := newLoadBalancerCmd(&env{flags: &globalFlags{}})
	sub, _, err := cmd.Find([]string{"quote-resize-load-balancer"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, name := range []string{"max-price", "no-wait"} {
		if f := sub.Flags().Lookup(name); f != nil {
			t.Fatalf("quote-resize-load-balancer registered its own --%s flag: %+v", name, f)
		}
	}
}

// TestLoadBalancerQuoteResizeLoadBalancerMissingRequiredFieldExitsWithZeroRequests
// checks that quote-resize-load-balancer without --package-id fails the
// required-field check before any request.
func TestLoadBalancerQuoteResizeLoadBalancerMissingRequiredFieldExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "quote-resize-load-balancer",
		"--load-balancer-id", "lb-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error for a missing --package-id")
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode(err), stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestLoadBalancerQuoteResizeLoadBalancerIsAReadUnderReadOnly checks the CLI
// design's rule that quotes are reads: a read-only profile must not refuse
// quote-resize-load-balancer the way it would a real write, and the request
// still reaches the fixture.
func TestLoadBalancerQuoteResizeLoadBalancerIsAReadUnderReadOnly(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`),
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent"}, validQuoteResizeLoadBalancerArgs...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("quote-resize-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (quote-resize-load-balancer must run as a read under read-only)", n)
	}
}
