package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func discoveryTestContext() context.Context {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		now = now.Add(d)
		return nil
	})
}

func TestListStoragePricesValidationBeforeReads(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []ListStoragePricesInput{{QuotaGB: -1}, {Region: "bad"}, {MaxRequests: -1}, {MaxRequests: 2001}} {
		if out, err := c.ListStoragePrices(discoveryTestContext(), &in); out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("input %+v: output %v error %v", in, out, err)
		}
	}
}

func storageDiscoveryHandler(t *testing.T, quote func(http.ResponseWriter, *http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var file string
		switch r.URL.Path {
		case "/internal/v1/regions":
			file = "discovery_regions.json"
		case "/internal/v1/billing/project_types":
			file = "discovery_project_types.json"
		case "/internal/v1/billing/purchase_types":
			file = "discovery_purchase_types.json"
		case "/billing-api/v1/configurations":
			file = map[string]string{"vos_billing_normal_min_quota": "discovery_config_min_quota.json", "vos_billing_normal_max_quota": "discovery_config_max_quota.json", "max_project_per_user_per_region": "discovery_config_max_projects.json", "enable_iam_checkout": "discovery_config_iam_checkout.json"}[r.URL.Query().Get("keys")]
		case "/billing-api/v2/price":
			quote(w, r)
			return
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		testutil.WriteFixture(t, w, fixtures+file)
	})
}

func TestListStoragePricesFixturesAndBodies(t *testing.T) {
	t.Parallel()
	quotes := 0
	c := newTestClient(t, storageDiscoveryHandler(t, func(w http.ResponseWriter, r *http.Request) {
		quotes++
		var body projectPriceBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Action != "create" || body.ResourceType != "object_storage" || body.ResourceInfo.Quota != 30 || body.ResourceInfo.PurchaseTypeID != 4 {
			t.Errorf("body %+v", body)
		}
		file := "discovery_quote_gold.json"
		if body.ResourceInfo.ProjectType == 7 {
			file = "discovery_quote_archive.json"
		}
		testutil.WriteFixture(t, w, fixtures+file)
	}))
	out, err := c.ListStoragePrices(discoveryTestContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || quotes != 2 || out.Metadata.Requests != 9 || !out.Metadata.Complete || out.Metadata.Region != "HCM04" {
		t.Fatalf("output %+v quotes %d", out, quotes)
	}
	for _, row := range out.Items {
		if row.QuotaGB != 30 || row.Price.MonthlyPrice == nil || row.PricePerGB == nil || row.Price.Currency != "VND" {
			t.Fatalf("row %+v", row)
		}
	}
}

func TestListStoragePricesBudgetAndRegionCache(t *testing.T) {
	t.Parallel()
	quotes := 0
	c := newTestClient(t, storageDiscoveryHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		quotes++
		testutil.WriteFixture(t, w, fixtures+"discovery_quote_gold.json")
	}))
	out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{MaxRequests: 8, AllowPartial: true})
	if out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || out.Metadata.Requests != 7 || quotes != 0 {
		t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
	}
	out, err = c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{MaxRequests: 8})
	if err != nil || out.Metadata.Requests != 8 || quotes != 2 {
		t.Fatalf("output %+v error %v quotes %d", out, err, quotes)
	}
}

func TestListStoragePricesDefaultAndPartialOrder(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			t.Parallel()
			base := storageDiscoveryHandler(t, func(w http.ResponseWriter, r *http.Request) {
				var body projectPriceBody
				_ = json.NewDecoder(r.Body).Decode(&body)
				if partial && body.ResourceInfo.ProjectType == 1 {
					w.WriteHeader(500)
					return
				}
				testutil.WriteFixture(t, w, fixtures+"discovery_quote_gold.json")
			})
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/v1/billing/project_types" {
					var env map[string]json.RawMessage
					body := testutil.FixtureBody(t, fixtures+"discovery_project_types.json")
					_ = json.Unmarshal([]byte(body), &env)
					var rows []json.RawMessage
					_ = json.Unmarshal(env["datas"], &rows)
					slices.Reverse(rows)
					env["datas"], _ = json.Marshal(rows)
					_ = json.NewEncoder(w).Encode(env)
					return
				}
				base.ServeHTTP(w, r)
			}))
			out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{AllowPartial: partial})
			if partial && !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
				t.Fatal(err)
			}
			if !partial && err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != 2 || out.Items[0].Type >= out.Items[1].Type {
				t.Fatalf("order %+v", out)
			}
		})
	}
}

func TestListStoragePricesExactFiltersAndQuotaBounds(t *testing.T) {
	for _, in := range []ListStoragePricesInput{{Type: "unknown"}, {Type: "gold"}, {QuotaGB: 1}, {QuotaGB: 999999999}} {
		t.Run(fmt.Sprintf("%+v", in), func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, storageDiscoveryHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") }))
			out, err := c.ListStoragePrices(discoveryTestContext(), &in)
			if err != nil || !out.Metadata.Complete {
				t.Fatalf("output %+v error %v", out, err)
			}
			if in.Type != "" && len(out.Items) != 0 {
				t.Fatal("filter not exact")
			}
			for _, row := range out.Items {
				if row.Price.Status != "inapplicable" || row.Price.MonthlyPrice != nil {
					t.Fatalf("row %+v", row)
				}
			}
		})
	}
}

func TestListStoragePricesQuoteErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, partial), func(t *testing.T) {
				t.Parallel()
				attempts := 0
				c := newTestClient(t, storageDiscoveryHandler(t, func(w http.ResponseWriter, _ *http.Request) {
					attempts++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"Failure"}`))
				}))
				out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{Type: "Gold", AllowPartial: partial})
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status || attempts != 1 {
					t.Fatalf("output %+v error %v attempts %d", out, err, attempts)
				}
				if partial && (out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete)) {
					t.Fatalf("partial output %v error %v", out, err)
				}
				if !partial && out != nil {
					t.Fatal("unexpected output")
				}
			})
		}
	}
}

func TestListStoragePricesMalformedQuote(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"success":true,"data":{}}`, `{"success":true,"data":{"optimumPrice":null}}`, `{"success":true,"data":{"optimumPrice":0}}`, `{"success":true,"data":{"optimumPrice":-1}}`, `{"success":true,"data":{"optimumPrice":1e999}}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			c := newTestClient(t, storageDiscoveryHandler(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			_, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{Type: "Gold"})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestListStoragePricesCatalogValidation(t *testing.T) {
	for name, body := range map[string]string{"missing": `{"success":true}`, "null": `{"success":true,"datas":null}`, "paging": `{"success":true,"datas":[],"nextPage":1}`, "duplicate": `{"success":true,"datas":[{"id":1,"name":"a"},{"id":1,"name":"a"}]}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := storageDiscoveryHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") })
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/v1/billing/project_types" {
					_, _ = w.Write([]byte(body))
					return
				}
				base.ServeHTTP(w, r)
			}))
			out, err := c.ListStoragePrices(discoveryTestContext(), nil)
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) {
				t.Fatalf("output %v error %v", out, err)
			}
		})
	}
}

func TestListStoragePricesMissingCatalogStatus(t *testing.T) {
	t.Parallel()
	base := storageDiscoveryHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") })
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/billing/project_types" {
			_, _ = w.Write([]byte(`{"success":true,"datas":[{"id":1,"name":"Gold","allowPeriod":[1],"billableResources":[{"purchaseTypeId":4,"projectTypeId":1}]}]}`))
			return
		}
		base.ServeHTTP(w, r)
	}))
	out, err := c.ListStoragePrices(discoveryTestContext(), nil)
	if out != nil || err == nil {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestListStoragePricesRowLimitPartial(t *testing.T) {
	t.Parallel()
	base := storageDiscoveryHandler(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected quote") })
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/billing/project_types" {
			base.ServeHTTP(w, r)
			return
		}
		var types []map[string]any
		for i := 1; i <= 6000; i++ {
			types = append(types, map[string]any{"id": i, "name": fmt.Sprintf("Type-%04d", i), "status": 1, "allowPeriod": []int{1}, "billableResources": []map[string]any{{"purchaseTypeId": 4, "projectTypeId": i}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "datas": types})
	}))
	out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{AllowPartial: true})
	if out == nil || len(out.Items) == 0 || !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
		t.Fatalf("partial output missing; error %v", err)
	}
}

func TestListStoragePricesIndependentOfferPartial(t *testing.T) {
	t.Parallel()
	base := storageDiscoveryHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"discovery_quote_archive.json")
	})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/billing/project_types" {
			base.ServeHTTP(w, r)
			return
		}
		var env map[string]json.RawMessage
		_ = json.Unmarshal([]byte(testutil.FixtureBody(t, fixtures+"discovery_project_types.json")), &env)
		var types []ProjectType
		_ = json.Unmarshal(env["datas"], &types)
		types[0].BillableResources = []ProjectBillableResource{}
		env["datas"], _ = json.Marshal(types)
		_ = json.NewEncoder(w).Encode(env)
	}))
	out, err := c.ListStoragePrices(discoveryTestContext(), &ListStoragePricesInput{AllowPartial: true})
	if out == nil || len(out.Items) != 1 || out.Items[0].Price.Status != "quoted" || !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestStorageDiscoveryTaxMetadata(t *testing.T) {
	c := &Client{}
	for _, tax := range []string{"", `,"tax":"inclusive"`} {
		price, err := c.decodeStorageDiscoveryPrice("storage.QuoteProject", 200, json.RawMessage(`{"success":true,"data":{"optimumPrice":30000,"originalPrice":40000,"discountPrice":10000`+tax+`}}`))
		if err != nil || price.Tax != "as-returned" || *price.MonthlyPrice != 30000 || *price.OriginalPrice != 40000 || *price.DiscountPrice != 10000 {
			t.Fatalf("price %+v error %v", price, err)
		}
	}
}
