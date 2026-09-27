package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func validCreateLoadBalancerInput() *CreateLoadBalancerInput {
	return &CreateLoadBalancerInput{
		Name:      "lb-1",
		PackageID: "pkg-1",
		Type:      TypeLayer4,
		Scheme:    SchemeInternal,
		SubnetID:  "subnet-1",
		ZoneID:    "zone-1",
	}
}

func TestQuoteCreateLoadBalancerSendsQuoteBody(t *testing.T) {
	var gotPath string
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/quote_create_load_balancer.json")
	}))

	out, err := c.QuoteCreateLoadBalancer(context.Background(), validCreateLoadBalancerInput())
	if err != nil {
		t.Fatalf("QuoteCreateLoadBalancer() error = %v", err)
	}
	if gotPath != "/v1/price" {
		t.Fatalf("path = %s, want /v1/price", gotPath)
	}
	if body["resourceType"] != "load-balancer" || body["action"] != "create" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info, ok := body["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", body)
	}
	want := map[string]any{
		"packageId":    "pkg-1",
		"zoneId":       "zone-1",
		"period":       float64(1),
		"isPoc":        false,
		"isBuyMorePoc": false,
	}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v (info = %+v)", k, info[k], v, info)
		}
	}
	if len(info) != len(want) {
		t.Fatalf("resourceInfo = %+v, want exactly %+v", info, want)
	}
	if out.OptimumPrice != 400000 {
		t.Fatalf("OptimumPrice = %v, want 400000", out.OptimumPrice)
	}
	if len(out.Properties) != 1 || out.Properties[0].Name != "LOAD BALANCER" {
		t.Fatalf("unexpected properties: %+v", out.Properties)
	}
}

// TestQuoteCreateLoadBalancerIgnoresMaxPriceAndNoWait checks that MaxPrice
// and NoWait, which govern only a future CreateLoadBalancer, change nothing
// about the quote sent here.
func TestQuoteCreateLoadBalancerIgnoresMaxPriceAndNoWait(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/quote_create_load_balancer.json")
	}))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 1
	in.NoWait = true
	if _, err := c.QuoteCreateLoadBalancer(context.Background(), in); err != nil {
		t.Fatalf("QuoteCreateLoadBalancer() error = %v", err)
	}
}

func TestQuoteCreateLoadBalancerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	fields := []struct {
		name string
		zero func(in *CreateLoadBalancerInput)
	}{
		{"Name", func(in *CreateLoadBalancerInput) { in.Name = "" }},
		{"PackageID", func(in *CreateLoadBalancerInput) { in.PackageID = "" }},
		{"Type", func(in *CreateLoadBalancerInput) { in.Type = "" }},
		{"Scheme", func(in *CreateLoadBalancerInput) { in.Scheme = "" }},
		{"SubnetID", func(in *CreateLoadBalancerInput) { in.SubnetID = "" }},
		{"ZoneID", func(in *CreateLoadBalancerInput) { in.ZoneID = "" }},
	}
	for _, f := range fields {
		in := validCreateLoadBalancerInput()
		f.zero(in)
		if _, err := c.QuoteCreateLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("%s empty: err = %v, want ErrInvalidInput", f.name, err)
		}
	}
	if _, err := c.QuoteCreateLoadBalancer(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteCreateLoadBalancerRejectsBadBodyIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	fields := []struct {
		name string
		set  func(in *CreateLoadBalancerInput, v string)
	}{
		{"PackageID", func(in *CreateLoadBalancerInput, v string) { in.PackageID = v }},
		{"SubnetID", func(in *CreateLoadBalancerInput, v string) { in.SubnetID = v }},
	}
	for _, f := range fields {
		for _, bad := range []string{"..", ".", "/", "?"} {
			in := validCreateLoadBalancerInput()
			f.set(in, bad)
			if _, err := c.QuoteCreateLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("%s=%q err = %v, want ErrInvalidInput", f.name, bad, err)
			}
		}
	}
}

// TestQuoteCreateLoadBalancerUnknownPackagePassesThrough checks that the
// billing gateway's 500 for an unknown or missing packageId reaches the
// caller unchanged, as any other status the pricing quote returns does.
func TestQuoteCreateLoadBalancerUnknownPackagePassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Internal Server Error"}`))
	}))
	in := validCreateLoadBalancerInput()
	in.PackageID = "unknown-package"
	_, err := c.QuoteCreateLoadBalancer(context.Background(), in)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 {
		t.Fatalf("err = %v, want *vngcloud.APIError with status 500", err)
	}
}
