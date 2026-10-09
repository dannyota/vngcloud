package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

// TestGoldenLoadBalancerCreateLoadBalancer checks create-load-balancer's
// exact output shape: {"LoadBalancer": {...}, "QuotedPrice": ...}.
func TestGoldenLoadBalancerCreateLoadBalancer(t *testing.T) {
	v := &loadbalancer.CreateLoadBalancerOutput{LoadBalancer: exampleLoadBalancer(), QuotedPrice: 400000}
	checkGolden(t, "loadbalancer-create-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-create-load-balancer.table.golden", "table", "", v)
}

// TestGoldenLoadBalancerDeleteLoadBalancer checks delete-load-balancer's
// exact output shape: an empty object.
func TestGoldenLoadBalancerDeleteLoadBalancer(t *testing.T) {
	v := &loadbalancer.DeleteLoadBalancerOutput{}
	checkGolden(t, "loadbalancer-delete-load-balancer.json.golden", "json", "", v)
	checkGolden(t, "loadbalancer-delete-load-balancer.table.golden", "table", "", v)
}

// validCreateLoadBalancerArgs is the flag set create-load-balancer needs to
// pass its required-field check, priced within maxPriceFor's budget, and
// scoped Internal so it needs no --yes.
var validCreateLoadBalancerArgs = []string{
	"loadbalancer", "create-load-balancer",
	"--name", "lb-1", "--package-id", "pkg-1", "--type", "Layer 4",
	"--scheme", "Internal", "--subnet-id", "subnet-1", "--zone-id", "zone-1", "--no-wait",
}

// TestLoadBalancerCreateLoadBalancerSendsRequestBody drives create-load-balancer
// end to end with --no-wait (skipping the post-create wait, already covered by
// the loadbalancer package's own tests): the quote's own resourceInfo and the
// create's own request body, per the vLB writes design.
func TestLoadBalancerCreateLoadBalancerSendsRequestBody(t *testing.T) {
	var createBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			defer func() { _ = r.Body.Close() }()
			createBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"uuid":"lb-1"}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateLoadBalancerArgs, "--max-price", "400000")...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-load-balancer: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(createBody, &decoded); err != nil {
		t.Fatalf("create body is not valid JSON: %v (%s)", err, createBody)
	}
	want := map[string]any{
		"name": "lb-1", "packageId": "pkg-1", "scheme": "Internal", "subnetId": "subnet-1",
		"type": "Layer 4", "zoneId": "zone-1", "autoScalable": false, "isPoc": false,
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Fatalf("create body[%q] = %v, want %v (body=%s)", k, decoded[k], v, createBody)
		}
	}
	if got := fixture.requestCount(); got != 2 {
		t.Fatalf("requestCount = %d, want 2 (quote and create)", got)
	}
	if got := stdout.String(); !strings.Contains(got, `"QuotedPrice": 400000`) {
		t.Fatalf("stdout = %s, want QuotedPrice 400000", got)
	}
}

// TestLoadBalancerCreateLoadBalancerDefaultMaxPriceRefusesOrder checks the
// design's price guard: --max-price left at its default of 0 refuses an
// order whose quote prices above 0, with the PriceAboveMax error class and
// exit code 1, and no create request ever sent.
func TestLoadBalancerCreateLoadBalancerDefaultMaxPriceRefusesOrder(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validCreateLoadBalancerArgs...))
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
	if got := fixture.requestCount(); got != 1 {
		t.Fatalf("requestCount = %d, want 1 (the quote only)", got)
	}
}

// TestLoadBalancerCreateLoadBalancerInvalidMaxPriceExitsWithZeroRequests
// checks that a NaN, +Inf, or negative --max-price is refused with
// InvalidUsage and exit code 2 before any request, mirroring monitor's own
// create-log-project test.
func TestLoadBalancerCreateLoadBalancerInvalidMaxPriceExitsWithZeroRequests(t *testing.T) {
	for _, maxPrice := range []string{"NaN", "Inf", "-1"} {
		t.Run(maxPrice, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
				append(validCreateLoadBalancerArgs, "--max-price", maxPrice)...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a %s MaxPrice refusal", maxPrice)
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
			}
			if got := fixture.requestCount(); got != 0 {
				t.Fatalf("requestCount = %d, want 0", got)
			}
		})
	}
}

// TestLoadBalancerCreateLoadBalancer502KeepsListAdvice checks that a 502 on
// the create POST is sent exactly once (Once: the order is never resent)
// and that the CLI's own error envelope keeps the SDK's advice to check
// list-load-balancers --name before ordering again, not just the bare
// "Bad Gateway" text the wrapped *APIError alone carries.
func TestLoadBalancerCreateLoadBalancer502KeepsListAdvice(t *testing.T) {
	var createCalls int
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": jsonHandler(http.StatusOK, `{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`),
		"/v2/proj-1/loadBalancers": func(w http.ResponseWriter, _ *http.Request) {
			createCalls++
			w.WriteHeader(http.StatusBadGateway)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validCreateLoadBalancerArgs, "--max-price", "400000")...))
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a 502 error")
	}
	if createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1 (Once: never resent)", createCalls)
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", got, stderr.String())
	}
	msg := classify(err).Message
	if !strings.Contains(msg, "list-load-balancers --name") {
		t.Fatalf("Message = %q, want it to name list-load-balancers --name", msg)
	}
	if !strings.Contains(msg, "may have already reached the server") {
		t.Fatalf("Message = %q, want the ambiguous-create advice", msg)
	}
}

// TestLoadBalancerCreateLoadBalancerSchemeRequiresYes checks the design's
// exposure guard: every Scheme except Internal, trimmed of surrounding
// space and matched case-insensitively, needs --yes, including a value the
// SDK will itself go on to refuse as neither Internet nor Internal. The
// guard runs before any request, including the quote.
func TestLoadBalancerCreateLoadBalancerSchemeRequiresYes(t *testing.T) {
	tests := []struct {
		name        string
		scheme      string
		yes         bool
		wantBlocked bool
	}{
		{"Internet without --yes", "Internet", false, true},
		{"Internet with --yes", "Internet", true, false},
		{"lowercase internet without --yes", "internet", false, true},
		{"uppercase INTERNET without --yes", "INTERNET", false, true},
		{"leading space Internet without --yes", " Internet", false, true},
		{"trailing space Internet without --yes", "Internet ", false, true},
		{"a value the SDK will itself refuse without --yes", "Public", false, true},
		{"Internal needs no --yes", "Internal", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/v1/price":                jsonHandler(http.StatusOK, `{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"propertiesPrice":[]}`),
				"/v2/proj-1/loadBalancers": jsonHandler(http.StatusOK, `{"uuid":"lb-1"}`),
			})
			root, _, stderr := newSvcRoot(t, fixture)
			args := []string{
				"--region", "hcm-3", "--project-id", "proj-1",
				"loadbalancer", "create-load-balancer",
				"--name", "lb-1", "--package-id", "pkg-1", "--type", "Layer 4",
				"--scheme", tt.scheme, "--subnet-id", "subnet-1", "--zone-id", "zone-1", "--no-wait",
				"--max-price", "400000",
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
				t.Fatalf("create-load-balancer: %v (stderr=%s)", err, stderr.String())
			}
			if n := fixture.requestCount(); n != 2 {
				t.Fatalf("requestCount = %d, want 2", n)
			}
		})
	}
}

// TestRequireYesUnlessSchemeInternalIsCaseInsensitiveAndTrims checks
// requireYesUnlessSchemeInternal directly, independent of the SDK's own
// separate, exact-match check on CreateLoadBalancer (Scheme must read
// exactly "Internet" or "Internal"): the guard's own case-insensitive,
// trimmed match against Internal is a CLI-only convenience so an agent's
// stray casing or whitespace does not turn a private load balancer into one
// this guard fails to warn about, and it must not depend on the SDK ever
// accepting the same value.
func TestRequireYesUnlessSchemeInternalIsCaseInsensitiveAndTrims(t *testing.T) {
	tests := []struct {
		scheme       string
		wantNeedsYes bool
	}{
		{"Internal", false},
		{"internal", false},
		{"INTERNAL", false},
		{" Internal ", false},
		{"Internet", true},
		{"internet", true},
		{"", true},
	}
	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.Flags().Bool("yes", false, "")
			err := requireYesUnlessSchemeInternal(cmd, &loadbalancer.CreateLoadBalancerInput{Scheme: tt.scheme})
			if tt.wantNeedsYes && err == nil {
				t.Fatalf("Scheme %q: expected the guard to require --yes", tt.scheme)
			}
			if !tt.wantNeedsYes && err != nil {
				t.Fatalf("Scheme %q: expected no --yes requirement, got %v", tt.scheme, err)
			}
		})
	}
}

// TestLoadBalancerCreateLoadBalancerCLIInputJSONSchemeRequiresYes checks that
// the Scheme guard runs on the merged Input, so a Scheme set only through
// --cli-input-json still needs --yes: --cli-input-json cannot bypass it.
func TestLoadBalancerCreateLoadBalancerCLIInputJSONSchemeRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "loadbalancer", "create-load-balancer",
		"--name", "lb-1", "--package-id", "pkg-1", "--type", "Layer 4",
		"--subnet-id", "subnet-1", "--zone-id", "zone-1", "--no-wait",
		"--cli-input-json", `{"Scheme":"Internet"}`,
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

// TestLoadBalancerCreateLoadBalancerReadOnlyRefusedWithZeroRequests checks
// the design's read-only rule: create-load-balancer is a Write, so a
// read-only profile refuses it with exit 2 before any request, including
// the quote.
func TestLoadBalancerCreateLoadBalancerReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs(append([]string{"--profile", "agent"}, validCreateLoadBalancerArgs...))
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

// validDeleteLoadBalancerArgs is the flag set delete-load-balancer needs to
// pass its required-field check.
var validDeleteLoadBalancerArgs = []string{"loadbalancer", "delete-load-balancer", "--load-balancer-id", "lb-1", "--no-wait"}

// TestLoadBalancerDeleteLoadBalancerWithoutYesExitsWithZeroRequests checks
// that delete-load-balancer, Destructive per the design, is refused without
// --yes before any request.
func TestLoadBalancerDeleteLoadBalancerWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"}, validDeleteLoadBalancerArgs...))
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

// TestLoadBalancerDeleteLoadBalancerWithYesSendsDelete drives
// delete-load-balancer with --yes and --no-wait: it reads the load balancer
// first, then sends the DELETE.
func TestLoadBalancerDeleteLoadBalancerWithYesSendsDelete(t *testing.T) {
	var deleteCalled bool
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/loadBalancers/lb-1": func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				jsonHandler(http.StatusOK, `{"data":{"uuid":"lb-1","progressStatus":"CREATED"}}`)(w, r)
			case http.MethodDelete:
				deleteCalled = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs(append([]string{"--region", "hcm-3", "--project-id", "proj-1"},
		append(validDeleteLoadBalancerArgs, "--yes")...))
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-load-balancer: %v (stderr=%s)", err, stderr.String())
	}
	if !deleteCalled {
		t.Fatal("expected a DELETE request")
	}
}

// TestLoadBalancerDeleteLoadBalancerReadOnlyRefusedWithZeroRequests checks
// the design's read-only rule for delete-load-balancer.
func TestLoadBalancerDeleteLoadBalancerReadOnlyRefusedWithZeroRequests(t *testing.T) {
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
	root.SetArgs(append([]string{"--profile", "agent"}, append(validDeleteLoadBalancerArgs, "--yes")...))
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
