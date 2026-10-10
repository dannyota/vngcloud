package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const pricingTypes = `{"success":true,"datas":[{"id":1,"name":"Gold","title":"Gold Type","group":"Gold","status":1,"storageClass":{"storagePolicy":"Gold"},"allowPeriod":[1,3],"sku":["vStorage-Gold"],"skuMappings":{"a":"b"},"billingUnitPrice":{"vStorage-Gold":{"traffic_unit_price":280}},"billableResources":[{"id":6,"name":"Normal - Gold","priceKey":"objng-quota","purchaseTypeId":4,"projectTypeId":1,"period":null}]}]}`
const pricingPurchases = `{"success":true,"datas":[{"id":4,"name":"Normal","title":"Pay monthly","status":1}]}`
const pricingQuote = `{"success":true,"data":{"optimumPrice":30000,"originalPrice":30000,"discountPrice":0,"discountPercent":null,"propertiesPrice":[{"optimumPrice":30000,"monthlyPrice":30000,"discountPercent":null,"name":null,"description":null}]}}`

func pricingClient(t *testing.T, override map[string]string, prices *int, configKeys map[string]int) *Client {
	t.Helper()
	return newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		if r.URL.Query().Has("project_id") {
			t.Error("project_id sent")
		}
		body := ""
		switch r.URL.Path {
		case "/internal/v1/billing/project_types":
			body = pricingTypes
		case "/internal/v1/billing/purchase_types":
			body = pricingPurchases
		case "/billing-api/v1/configurations":
			key := r.URL.Query().Get("keys")
			if configKeys != nil {
				configKeys[key]++
			}
			values := map[string]string{"vos_billing_normal_min_quota": "30", "vos_billing_normal_max_quota": "2000000", "max_project_per_user_per_region": "20", "enable_iam_checkout": "true"}
			value, ok := values[key]
			if !ok {
				t.Errorf("unexpected key %q", key)
			}
			if len(r.URL.Query()) != 2 || r.URL.Query().Get("region_id") != "<region-id-2>" {
				t.Error("configuration query")
			}
			body = fmt.Sprintf(`{"success":true,"datas":[{"key":%q,"value":%q}]}`, key, value)
		case "/billing-api/v2/price":
			*prices++
			if r.Method != http.MethodPost || len(r.URL.Query()) != 1 || r.URL.Query().Get("region_id") != "<region-id-2>" {
				t.Error("price route")
			}
			var got map[string]any
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			purchaseID := float64(4)
			if strings.Contains(override["/internal/v1/billing/purchase_types"], `"id":42`) {
				purchaseID = 42
			}
			quota := got["resourceInfo"].(map[string]any)["quota"]
			want := map[string]any{"resourceType": "object_storage", "action": "create", "resourceInfo": map[string]any{"quota": quota, "purchaseTypeId": purchaseID, "projectType": float64(1)}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("price body = %#v", got)
			}
			body = pricingQuote
		default:
			t.Errorf("unapproved route %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path != "/billing-api/v2/price" && r.Method != http.MethodGet {
			t.Error("non-read request")
		}
		if replacement, ok := override[r.URL.Path]; ok {
			body = replacement
		}
		if replacement, ok := override[r.URL.Query().Get("keys")]; ok {
			body = replacement
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestProjectPricingReads(t *testing.T) {
	prices := 0
	keys := map[string]int{}
	c := pricingClient(t, nil, &prices, keys)
	out, err := c.ListProjectTypes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || len(out.Items[0].Offers) != 1 {
		t.Fatalf("catalog = %+v", out)
	}
	offer := out.Items[0].Offers[0]
	if offer.MonthlyPrice != 30000 || offer.Currency != "VND" || offer.QuotedQuotaGB != 30 || offer.MinQuotaGB != 30 || offer.MaxQuotaGB != 2000000 || offer.StepQuotaGB != nil || offer.Period != nil || offer.PurchaseTypeName != "Pay monthly" {
		t.Fatalf("offer = %+v", offer)
	}
	if out.Items[0].StoragePolicy != "Gold" || len(out.Items[0].SKUMappings) == 0 || len(out.Items[0].BillingUnitPrice) == 0 {
		t.Fatal("metadata missing")
	}
	for _, quota := range []int64{30, 31, 2000000} {
		q, err := c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: "Gold", QuotaGB: quota, Name: "ignored", MaxPrice: -1, NoWait: true})
		if err != nil {
			t.Fatal(err)
		}
		if q.MonthlyPrice != 30000 || q.TotalPrice != q.MonthlyPrice || q.OptimumPrice != q.MonthlyPrice || q.Currency != "VND" || len(q.Properties) != 1 || q.Properties[0].Name != nil {
			t.Fatalf("quote = %+v", q)
		}
	}
	if prices != 4 {
		t.Fatalf("prices = %d", prices)
	}
	for key, count := range keys {
		if count != 4 {
			t.Errorf("%s read %d times", key, count)
		}
	}
	if len(keys) != 4 {
		t.Fatalf("configuration keys = %v", keys)
	}
}

func TestProjectPricingInput(t *testing.T) {
	for _, in := range []*CreateProjectInput{nil, {}, {Type: "Gold"}, {Type: "Gold", QuotaGB: -1}, {Type: "Gold", QuotaGB: 29}, {Type: "Gold", QuotaGB: 2000001}, {Type: "gold", QuotaGB: 30}, {Type: "Gold", QuotaGB: 30, Region: "unknown"}} {
		prices := 0
		c := pricingClient(t, nil, &prices, nil)
		_, err := c.QuoteCreateProject(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) || prices != 0 {
			t.Fatalf("input %+v: error %v, prices %d", in, err, prices)
		}
	}
}

func TestProjectPricingFailures(t *testing.T) {
	cases := []struct {
		name, path, body  string
		unpriced, invalid bool
	}{
		{"missing price", "/billing-api/v2/price", `{"success":true,"data":{}}`, false, false},
		{"null price", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":null}}`, false, false},
		{"zero price", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":0}}`, true, false},
		{"negative price", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":-1}}`, true, false},
		{"string price", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":"NaN"}}`, false, false},
		{"overflow price", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":1e999}}`, false, false},
		{"refusal", "/billing-api/v2/price", `{"success":false,"code":114,"errorMsg":"refused"}`, false, false},
		{"missing success", "/billing-api/v2/price", `{"data":{"optimumPrice":30000}}`, false, false},
		{"bad envelope", "/billing-api/v2/price", `invalid`, false, false},
		{"missing limits", "vos_billing_normal_min_quota", `{"success":true,"datas":[]}`, false, false},
		{"null limits", "vos_billing_normal_min_quota", `{"success":true,"datas":null}`, false, false},
		{"malformed limit", "vos_billing_normal_min_quota", `{"success":true,"datas":[{"key":"vos_billing_normal_min_quota","value":"30.5"}]}`, false, false},
		{"negative limit", "vos_billing_normal_min_quota", `{"success":true,"datas":[{"key":"vos_billing_normal_min_quota","value":"-30"}]}`, false, false},
		{"inconsistent limits", "vos_billing_normal_max_quota", `{"success":true,"datas":[{"key":"vos_billing_normal_max_quota","value":"20"}]}`, false, false},
		{"duplicate limits", "vos_billing_normal_min_quota", `{"success":true,"datas":[{"key":"vos_billing_normal_min_quota","value":"30"},{"key":"vos_billing_normal_min_quota","value":"30"}]}`, false, false},
		{"bad boolean", "enable_iam_checkout", `{"success":true,"datas":[{"key":"enable_iam_checkout","value":"TRUE"}]}`, false, false},
		{"missing catalog", "/internal/v1/billing/project_types", `{"success":true}`, false, false},
		{"disabled type", "/internal/v1/billing/project_types", `{"success":true,"datas":[{"id":1,"name":"Gold","status":0,"allowPeriod":[1]}]}`, false, true},
		{"no monthly period", "/internal/v1/billing/project_types", `{"success":true,"datas":[{"id":1,"name":"Gold","status":1,"allowPeriod":[3]}]}`, false, true},
		{"missing monthly purchase", "/internal/v1/billing/purchase_types", `{"success":true,"datas":[]}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prices := 0
			c := pricingClient(t, map[string]string{tc.path: tc.body}, &prices, nil)
			_, err := c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: "Gold", QuotaGB: 30})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.unpriced {
				if !errors.Is(err, vngcloud.ErrUnpriced) {
					t.Fatal(err)
				}
				return
			}
			if tc.invalid {
				if !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatal(err)
				}
				return
			}
			var api *vngcloud.APIError
			if !errors.As(err, &api) {
				t.Fatalf("expected APIError: %v", err)
			}
			if tc.path != "/billing-api/v2/price" && prices != 0 {
				t.Fatal("price called before validation")
			}
		})
	}
}

func TestProjectPricingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prices := 0
	c := pricingClient(t, nil, &prices, nil)
	_, err := c.QuoteCreateProject(ctx, &CreateProjectInput{Type: "Gold", QuotaGB: 30})
	if !errors.Is(err, context.Canceled) || prices != 0 {
		t.Fatalf("error %v, prices %d", err, prices)
	}
}

func TestProjectPricingCatalogVariants(t *testing.T) {
	for _, tc := range []struct {
		name, types, purchases string
		wantOffers, wantPrices int
		wantError              bool
	}{
		{"unknown purchase", pricingTypes, `{"success":true,"datas":[{"id":9,"name":"Other","title":"Other purchase","status":1}]}`, 1, 0, false},
		{"disabled type", strings.Replace(pricingTypes, `"status":1`, `"status":0`, 1), pricingPurchases, 1, 0, false},
		{"disabled purchase", pricingTypes, strings.Replace(pricingPurchases, `"status":1`, `"status":0`, 1), 1, 0, false},
		{"duplicate type", `{"success":true,"datas":[` + strings.TrimSuffix(strings.TrimPrefix(pricingTypes, `{"success":true,"datas":[`), `]}`) + `,` + strings.TrimSuffix(strings.TrimPrefix(pricingTypes, `{"success":true,"datas":[`), `]}`) + `]}`, pricingPurchases, 0, 0, true},
		{"duplicate purchase", pricingTypes, `{"success":true,"datas":[{"id":4,"name":"Normal","title":"Pay monthly","status":1},{"id":4,"name":"Normal","title":"Pay monthly","status":1}]}`, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prices := 0
			c := pricingClient(t, map[string]string{"/internal/v1/billing/project_types": tc.types, "/internal/v1/billing/purchase_types": tc.purchases}, &prices, nil)
			out, err := c.ListProjectTypes(context.Background(), nil)
			if tc.wantError {
				if !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Items[0].Offers) != tc.wantOffers || prices != tc.wantPrices {
				t.Fatalf("offers %d prices %d", len(out.Items[0].Offers), prices)
			}
		})
	}
}

func TestProjectPricingRawFixtures(t *testing.T) {
	prices := 0
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		file := ""
		switch r.URL.Path {
		case "/internal/v1/billing/project_types":
			file = "project_types.json"
		case "/internal/v1/billing/purchase_types":
			file = "project_purchase_types.json"
		case "/billing-api/v1/configurations":
			file = "project_configuration_" + r.URL.Query().Get("keys") + ".json"
		case "/billing-api/v2/price":
			prices++
			var body struct {
				ResourceInfo struct {
					ProjectType int `json:"projectType"`
				} `json:"resourceInfo"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			file = "project_quote_gold.json"
			if body.ResourceInfo.ProjectType == 7 {
				file = "project_quote_instant_archive.json"
			}
		default:
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, fixtures+file)
	}))
	out, err := c.ListProjectTypes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || prices != 2 {
		t.Fatalf("types %d prices %d", len(out.Items), prices)
	}
	for _, typ := range out.Items {
		if typ.Order != nil || typ.IsDefault != nil || typ.DescriptionEn == nil || typ.DescriptionVi == nil || len(typ.SKU) == 0 || len(typ.SKUMappings) == 0 || len(typ.BillableResources) != 1 || len(typ.Offers) != 1 {
			t.Fatalf("missing catalog metadata for %s", typ.Name)
		}
		want := float64(30000)
		if typ.Name == "Instant-Archive-2" {
			want = 15900
		}
		if typ.Offers[0].MonthlyPrice != want {
			t.Fatalf("price = %v", typ.Offers[0].MonthlyPrice)
		}
		quote, err := c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: typ.Name, QuotaGB: typ.Offers[0].MinQuotaGB})
		if err != nil {
			t.Fatal(err)
		}
		if quote.MonthlyPrice != want || quote.TotalPrice != want || quote.Properties[0].Description != nil || quote.DiscountPercent == nil {
			t.Fatalf("quote metadata for %s", typ.Name)
		}
	}
}

func TestProjectPricingRetry(t *testing.T) {
	calls := 0
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/billing-api/v2/price" {
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(pricingQuote))
	})))
	_, err := c.sendProjectQuote(context.Background(), "storage.QuoteCreateProject", "<region-id-2>", projectPurchaseSpec{quota: 30, purchaseTypeID: 4, projectTypeID: 1})
	if err != nil || calls != 2 {
		t.Fatalf("error %v calls %d", err, calls)
	}
}

func TestProjectPricingRegionFailure(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/regions" {
			t.Fatalf("unexpected route %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":false,"code":403,"errorMsg":"denied"}`))
	}))
	_, err := c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: "Gold", QuotaGB: 30})
	if !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatal(err)
	}
	_, err = c.ListProjectTypes(context.Background(), nil)
	if !errors.Is(err, vngcloud.ErrPermission) {
		t.Fatal(err)
	}
}

func TestProjectPricingCatalogFailsOnUnpricedOffer(t *testing.T) {
	prices := 0
	c := pricingClient(t, map[string]string{"/billing-api/v2/price": `{"success":true,"data":{"optimumPrice":0}}`}, &prices, nil)
	out, err := c.ListProjectTypes(context.Background(), nil)
	if out != nil || !errors.Is(err, vngcloud.ErrUnpriced) || prices != 1 {
		t.Fatalf("output %v error %v prices %d", out, err, prices)
	}
}

func TestProjectPriceRejectsRedirects(t *testing.T) {
	for _, status := range []int{307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path == "/billing-api/v2/price" {
					w.Header().Set("Location", "/internal/v2/orders")
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(pricingQuote))
			}))
			_, err := c.sendProjectQuote(context.Background(), "storage.QuoteCreateProject", "region", projectPurchaseSpec{quota: 30, purchaseTypeID: 4, projectTypeID: 1})
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != status || calls != 1 {
				t.Fatalf("error %v, requests %d, want redirect error and one request", err, calls)
			}
		})
	}
}

type pricingTokenSource struct{ calls atomic.Int64 }

func (s *pricingTokenSource) Token(context.Context) (transport.Token, error) {
	s.calls.Add(1)
	return transport.Token{AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (*pricingTokenSource) Invalidate(string) {}

func TestProjectPricingRegionsBeforeAuthentication(t *testing.T) {
	for _, method := range []string{"list", "quote"} {
		for _, regions := range [][2]string{{"hcm-3", "unknown"}, {"unknown", "HCM04"}, {"unknown", ""}} {
			t.Run(method+"/"+regions[0]+"/"+regions[1], func(t *testing.T) {
				requests := 0
				source := &pricingTokenSource{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.URL.Path == "/internal/v1/regions" {
						testutil.WriteFixture(t, w, fixtures+"list_regions.json")
						return
					}
					_, _ = w.Write([]byte(pricingQuote))
				}))
				defer server.Close()
				tr := transport.New(transport.Config{TokenSource: source, HTTPClient: server.Client()})
				c := New(core.NewTestConfig(regions[0], "", endpoints.Set{Storage: server.URL + "/"}, tr))
				var err error
				if method == "list" {
					_, err = c.ListProjectTypes(context.Background(), &ListProjectTypesInput{Region: regions[1]})
				} else {
					_, err = c.QuoteCreateProject(context.Background(), &CreateProjectInput{Region: regions[1], Type: "Gold", QuotaGB: 30})
				}
				if !errors.Is(err, vngcloud.ErrInvalidInput) || source.calls.Load() != 0 || requests != 0 {
					t.Fatalf("error %v, authentication %d, requests %d", err, source.calls.Load(), requests)
				}
			})
		}
	}
}

func TestProjectPricingMonthlyCatalogID(t *testing.T) {
	prices := 0
	types := strings.ReplaceAll(pricingTypes, `"purchaseTypeId":4`, `"purchaseTypeId":42`)
	purchases := strings.ReplaceAll(pricingPurchases, `"id":4`, `"id":42`)
	c := pricingClient(t, map[string]string{"/internal/v1/billing/project_types": types, "/internal/v1/billing/purchase_types": purchases}, &prices, nil)
	out, err := c.ListProjectTypes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Items[0].Offers[0].PurchaseTypeID != 42 || prices != 1 {
		t.Fatalf("output %+v, prices %d", out, prices)
	}
	_, err = c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: "Gold", QuotaGB: 30})
	if err != nil || prices != 2 {
		t.Fatalf("error %v, prices %d", err, prices)
	}
}

func TestProjectPricingConflictingIdentities(t *testing.T) {
	typeRow := strings.TrimSuffix(strings.TrimPrefix(pricingTypes, `{"success":true,"datas":[`), `]}`)
	for _, method := range []string{"list", "quote"} {
		for _, tc := range []struct{ path, body string }{
			{"/internal/v1/billing/project_types", `{"success":true,"datas":[` + typeRow + `,` + strings.Replace(typeRow, `"name":"Gold"`, `"name":"Other"`, 1) + `]}`},
			{"/internal/v1/billing/purchase_types", `{"success":true,"datas":[{"id":4,"name":"Normal","title":"Pay monthly","status":1},{"id":4,"name":"Other","title":"Other","status":1}]}`},
		} {
			t.Run(method+tc.path, func(t *testing.T) {
				prices := 0
				c := pricingClient(t, map[string]string{tc.path: tc.body}, &prices, nil)
				var err error
				if method == "list" {
					_, err = c.ListProjectTypes(context.Background(), nil)
				} else {
					_, err = c.QuoteCreateProject(context.Background(), &CreateProjectInput{Type: "Gold", QuotaGB: 30})
				}
				if !errors.Is(err, vngcloud.ErrInvalidInput) || prices != 0 {
					t.Fatalf("error %v, prices %d", err, prices)
				}
			})
		}
	}
}
