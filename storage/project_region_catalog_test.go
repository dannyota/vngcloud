package storage

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestProjectPricingRejectsUnresolvedRegions(t *testing.T) {
	for _, tc := range []struct{ name, region, catalog string }{
		{"unknown", "missing", `[{"regionId":"region-third","regionName":"DAD01"}]`},
		{"ambiguous", "DAD01", `[{"regionId":"region-third","regionName":"DAD01"},{"regionId":"region-fourth","regionName":"dad01"}]`},
		{"name matches another ID", "region-third", `[{"regionId":"region-third","regionName":"DAD01"},{"regionId":"region-fourth","regionName":"region-third"}]`},
	} {
		for _, method := range []string{"list", "quote", "discovery"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				requests := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/internal/v1/regions" {
						requests++
						t.Errorf("unexpected pricing request %s", r.URL.Path)
						return
					}
					_, _ = fmt.Fprintf(w, `{"success":true,"datas":%s}`, tc.catalog)
				}))
				var err error
				switch method {
				case "list":
					_, err = c.ListProjectTypes(context.Background(), &ListProjectTypesInput{Region: tc.region})
				case "quote":
					_, err = c.QuoteCreateProject(context.Background(), &CreateProjectInput{Region: tc.region, Type: "Gold", QuotaGB: 30})
				case "discovery":
					_, err = c.ListStoragePrices(context.Background(), &ListStoragePricesInput{Region: tc.region})
				}
				if err == nil || requests != 0 {
					t.Fatalf("error %v, pricing requests %d", err, requests)
				}
			})
		}
	}
}

func TestRegionCatalogIncompleteIsNeverCached(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(strconv.FormatBool(warm), func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if warm && calls == 1 {
					_, _ = fmt.Fprint(w, `{"success":true,"datas":[{"regionId":"region-2","regionName":"OTHER"}]}`)
					return
				}
				if r.URL.Query().Get("page") == "2" {
					_, _ = fmt.Fprint(w, `{"success":true,"datas":[{"regionId":"region-2","regionName":"DAD01"}]}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"success":true,"isNext":true,"datas":[{"regionId":"region-1","regionName":"DAD01"}]}`)
			}))
			if warm {
				if _, err := c.regionID(context.Background(), "test", "OTHER"); err != nil {
					t.Fatal(err)
				}
				if _, err := c.regionID(context.Background(), "test", "OTHER"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if _, err := c.regionID(context.Background(), "test", "DAD01"); err == nil {
					t.Fatal("accepted incomplete catalog")
				}
			}
			want := 2
			if warm {
				want++
			}
			if calls != want {
				t.Fatalf("catalog requests %d", calls)
			}
		})
	}
}

func TestRegionCatalogRejectsAmbiguousRowFields(t *testing.T) {
	for _, row := range []string{
		`{"regionId":"region-1","RegionID":"region-2","regionName":"DAD01"}`,
		`{"regionId":"region-1","regionId":"region-2","regionName":"DAD01"}`,
	} {
		t.Run(row, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				_, _ = fmt.Fprintf(w, `{"success":true,"datas":[%s]}`, row)
			}))
			for range 2 {
				if id, err := c.regionID(context.Background(), "test", "DAD01"); err == nil {
					t.Fatalf("accepted ambiguous row as %q", id)
				}
			}
			if calls != 2 {
				t.Fatalf("catalog requests %d", calls)
			}
		})
	}
}

func TestRegionCatalogRejectsInvalidID(t *testing.T) {
	for _, method := range []string{"management", "discovery"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/internal/v1/regions" {
					t.Errorf("unexpected request %s", r.URL.Path)
					return
				}
				_, _ = fmt.Fprint(w, `{"success":true,"datas":[{"regionId":"bad/id","regionName":"DAD01"}]}`)
			}))
			for range 2 {
				var err error
				if method == "management" {
					_, err = c.regionID(context.Background(), "test", "DAD01")
				} else {
					_, err = c.ListStoragePrices(context.Background(), &ListStoragePricesInput{Region: "DAD01"})
				}
				if err == nil {
					t.Fatal("accepted invalid region ID")
				}
			}
			if calls != 2 {
				t.Fatalf("catalog requests %d", calls)
			}
		})
	}
}

func TestDiscoveryRegionUUIDCanonicalName(t *testing.T) {
	regions := 0
	handler := storageDiscoveryHandler(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, pricingQuote)
	})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/regions" {
			regions++
			_, _ = fmt.Fprint(w, `{"success":true,"datas":[{"regionId":"region-2","regionName":"HCM04"}]}`)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	for range 2 {
		out, err := c.ListStoragePrices(context.Background(), &ListStoragePricesInput{Region: "region-2"})
		if err != nil || out == nil || len(out.Items) == 0 {
			t.Fatalf("output %+v error %v", out, err)
		}
		if out.Metadata.Region != "HCM04" || out.Items[0].Region != "HCM04" {
			t.Fatalf("output %+v", out)
		}
	}
	if regions != 1 {
		t.Fatalf("region requests %d", regions)
	}
}

func TestProjectPricingCatalogRegion(t *testing.T) {
	for _, region := range []string{"DAD01", "region-third"} {
		t.Run(region, func(t *testing.T) {
			prices := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/v1/regions" {
					_, _ = fmt.Fprint(w, `{"success":true,"datas":[{"regionId":"region-third","regionName":"DAD01"}]}`)
					return
				}
				if r.Header.Get("region") != "region-third" {
					t.Errorf("region header = %q", r.Header.Get("region"))
				}
				switch r.URL.Path {
				case "/internal/v1/billing/project_types":
					_, _ = fmt.Fprint(w, pricingTypes)
				case "/internal/v1/billing/purchase_types":
					_, _ = fmt.Fprint(w, pricingPurchases)
				case "/billing-api/v1/configurations":
					key := r.URL.Query().Get("keys")
					values := map[string]string{"vos_billing_normal_min_quota": "30", "vos_billing_normal_max_quota": "2000000", "max_project_per_user_per_region": "20", "enable_iam_checkout": "true"}
					_, _ = fmt.Fprintf(w, `{"success":true,"datas":[{"key":%q,"value":%q}]}`, key, values[key])
				case "/billing-api/v2/price":
					prices++
					_, _ = fmt.Fprint(w, pricingQuote)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
				}
			}))
			q, err := c.QuoteCreateProject(context.Background(), &CreateProjectInput{Region: region, Type: "Gold", QuotaGB: 30})
			if err != nil || q == nil || q.MonthlyPrice != 30000 || prices != 1 {
				t.Fatalf("quote %+v, error %v, price requests %d", q, err, prices)
			}
			types, err := c.ListProjectTypes(context.Background(), &ListProjectTypesInput{Region: region})
			if err != nil || types == nil || len(types.Items) != 1 || prices != 2 {
				t.Fatalf("types %+v, error %v, price requests %d", types, err, prices)
			}
			discovery, err := c.ListStoragePrices(context.Background(), &ListStoragePricesInput{Region: region})
			if err != nil || discovery == nil || len(discovery.Items) != 1 || discovery.Items[0].Region != "DAD01" || discovery.Metadata.Region != "DAD01" || prices != 3 {
				t.Fatalf("discovery %+v, error %v, price requests %d", discovery, err, prices)
			}
		})
	}
}
