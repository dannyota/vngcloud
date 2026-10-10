package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestProjectDuplicateResponseKeys(t *testing.T) {
	for _, tc := range []struct{ name, path, body, message string }{
		{"quote", "/billing-api/v2/price", testutil.FixtureBody(t, fixtures+"quote_duplicate_keys.json"), "quote response had no valid price"},
		{"project types", "/internal/v1/billing/project_types", strings.Replace(pricingTypes, `"id":1`, `"id":2,"id":1`, 1), "malformed catalog data"},
		{"purchase types", "/internal/v1/billing/purchase_types", strings.Replace(pricingPurchases, `"id":4`, `"id":2,"id":4`, 1), "malformed catalog data"},
		{"nested catalog", "/internal/v1/billing/project_types", strings.Replace(pricingTypes, `"storagePolicy":"Gold"`, `"storagePolicy":"Other","storagePolicy":"Gold"`, 1), "malformed catalog data"},
		{"quote envelope", "/billing-api/v2/price", `{"success":false,"success":true,"data":{"optimumPrice":10000}}`, "quote response had no valid price"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			overrides := map[string]string{tc.path: tc.body}
			check := func(t *testing.T, err error) {
				t.Helper()
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != 200 || api.Message != tc.message {
					t.Fatalf("error = %v, want malformed response APIError", err)
				}
			}
			t.Run("quote", func(t *testing.T) {
				prices := 0
				out, err := pricingClient(t, overrides, &prices, nil).QuoteCreateProject(context.Background(), validProjectCreate())
				check(t, err)
				if out != nil || (tc.path != "/billing-api/v2/price" && prices != 0) {
					t.Fatalf("output = %+v, prices = %d", out, prices)
				}
			})
			t.Run("create", func(t *testing.T) {
				s := &projectWriteServer{overrides: overrides, after: projectList(newProjectJSON)}
				_, err := newTestClient(t, s.handler(t)).CreateProject(context.Background(), validProjectCreate())
				if s.orders != 0 {
					t.Fatalf("orders = %d, want zero", s.orders)
				}
				check(t, err)
			})
			t.Run("auto renew", func(t *testing.T) {
				s := &autoRenewServer{overrides: overrides}
				_, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
				if s.puts != 0 {
					t.Fatalf("PUTs = %d, want zero", s.puts)
				}
				check(t, err)
			})
		})
	}
}

func TestProjectValidResponseKeys(t *testing.T) {
	prices := 0
	out, err := pricingClient(t, nil, &prices, nil).QuoteCreateProject(context.Background(), validProjectCreate())
	if err != nil || out == nil || out.TotalPrice != 30000 || prices != 1 {
		t.Fatalf("output = %+v, error = %v, prices = %d", out, err, prices)
	}
}
