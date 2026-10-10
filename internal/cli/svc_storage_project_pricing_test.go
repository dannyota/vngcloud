package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

const storageProjectTypesBody = `{"success":true,"datas":[{"id":1,"name":"Gold","title":"Gold Type","status":1,"allowPeriod":[1,3],"storageClass":{"storagePolicy":"Gold"},"billableResources":[{"id":6,"priceKey":"objng-quota","purchaseTypeId":4,"projectTypeId":1,"period":null}]}]}`
const storageProjectPurchasesBody = `{"success":true,"datas":[{"id":4,"name":"Normal","title":"Pay monthly","status":1}]}`
const storageProjectPriceBody = `{"success":true,"data":{"optimumPrice":30000,"originalPrice":32000,"discountPrice":2000,"discountPercent":null,"propertiesPrice":[{"optimumPrice":30000,"monthlyPrice":30000,"discountPercent":null,"name":null,"description":null}]}}`

func storageProjectPricingRoutes(t *testing.T, region string, quota int64, prices *int) map[string]func(http.ResponseWriter, *http.Request) {
	t.Helper()
	check := func(r *http.Request, method string) {
		t.Helper()
		if r.Method != method || r.Header.Get("Authorization") != "Bearer test-token" ||
			r.Header.Get("region") != region || r.Header.Get("region_id") != region {
			t.Errorf("%s: method or headers differ from the storage contract", r.URL.Path)
		}
		if r.URL.Query().Has("project_id") {
			t.Error("pricing request sent project_id")
		}
	}
	routes := map[string]func(http.ResponseWriter, *http.Request){}
	for path, body := range map[string]string{
		"/internal/v1/billing/project_types":  storageProjectTypesBody,
		"/internal/v1/billing/purchase_types": storageProjectPurchasesBody,
	} {
		routes[path] = func(w http.ResponseWriter, r *http.Request) {
			check(r, http.MethodGet)
			if r.URL.RawQuery != "" {
				t.Errorf("catalog query = %q, want empty", r.URL.RawQuery)
			}
			jsonHandler(http.StatusOK, body)(w, r)
		}
	}
	routes["/billing-api/v1/configurations"] = func(w http.ResponseWriter, r *http.Request) {
		check(r, http.MethodGet)
		values := map[string]string{
			"vos_billing_normal_min_quota": "30", "vos_billing_normal_max_quota": "2000000",
			"max_project_per_user_per_region": "20", "enable_iam_checkout": "true",
		}
		key := r.URL.Query().Get("keys")
		value, ok := values[key]
		if !ok || len(r.URL.Query()) != 2 || r.URL.Query().Get("region_id") != region {
			t.Errorf("configuration query = %q", r.URL.RawQuery)
		}
		jsonHandler(http.StatusOK, fmt.Sprintf(`{"success":true,"datas":[{"key":%q,"value":%q}]}`, key, value))(w, r)
	}
	routes["/billing-api/v2/price"] = func(w http.ResponseWriter, r *http.Request) {
		check(r, http.MethodPost)
		*prices++
		if len(r.URL.Query()) != 1 || r.URL.Query().Get("region_id") != region {
			t.Errorf("price query = %q", r.URL.RawQuery)
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode price body: %v", err)
			return
		}
		want := map[string]any{"resourceType": "object_storage", "action": "create",
			"resourceInfo": map[string]any{"quota": float64(quota), "purchaseTypeId": float64(4), "projectType": float64(1)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("price body = %#v, want %#v", got, want)
		}
		jsonHandler(http.StatusOK, storageProjectPriceBody)(w, r)
	}
	return routes
}

func TestStorageProjectPricingRequestsAndJSON(t *testing.T) {
	for _, op := range []string{"list-project-types", "quote-create-project"} {
		for _, region := range []string{"hcm-3", "han-1"} {
			t.Run(op+"/"+region, func(t *testing.T) {
				id := "region-hcm"
				if region == "han-1" {
					id = "region-han"
				}
				prices := 0
				args := []string{"--read-only", "storage", op, "--region", region}
				if op == "quote-create-project" {
					args = append(args, "--type", "Gold", "--quota-gb", "30")
				}
				r := runStorage(t, storageProjectPricingRoutes(t, id, 30, &prices), args...)
				if r.err != nil {
					t.Fatalf("execute: %v (%s)", r.err, r.stderr)
				}
				if prices != 1 || r.fixture.requestCount() != 8 {
					t.Fatalf("prices = %d, requests = %d, want 1 and 8", prices, r.fixture.requestCount())
				}
				if op == "list-project-types" {
					var got struct{ Items []map[string]any }
					if err := json.Unmarshal([]byte(r.stdout), &got); err != nil || len(got.Items) != 1 {
						t.Fatalf("catalog JSON = %s, error = %v", r.stdout, err)
					}
					want := map[string]any{"PurchaseTypeID": float64(4), "PurchaseTypeName": "Pay monthly",
						"PriceKey": "objng-quota", "Period": nil, "MinQuotaGB": float64(30),
						"MaxQuotaGB": float64(2000000), "StepQuotaGB": nil, "QuotedQuotaGB": float64(30),
						"MonthlyPrice": float64(30000), "Currency": "VND"}
					if got.Items[0]["Name"] != "Gold" || !reflect.DeepEqual(got.Items[0]["Offers"], []any{want}) {
						t.Fatalf("catalog JSON = %s", r.stdout)
					}
				} else {
					var got map[string]any
					if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
						t.Fatal(err)
					}
					want := map[string]any{"OptimumPrice": float64(30000), "OriginalPrice": float64(32000),
						"DiscountPrice": float64(2000), "DiscountPercent": nil, "MonthlyPrice": float64(30000),
						"TotalPrice": float64(30000), "Currency": "VND", "Properties": []any{map[string]any{
							"OptimumPrice": float64(30000), "MonthlyPrice": float64(30000), "DiscountPercent": nil,
							"Name": nil, "Description": nil}}}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("quote JSON = %s", r.stdout)
					}
				}
			})
		}
	}
}

func TestStorageProjectQuoteRequiredFlags(t *testing.T) {
	for _, args := range [][]string{nil, {"--type", "Gold"}, {"--quota-gb", "30"}} {
		prices := 0
		r := runStorage(t, storageProjectPricingRoutes(t, "region-hcm", 30, &prices),
			append([]string{"storage", "quote-create-project"}, args...)...)
		if r.err == nil || exitCode(r.err) != 2 || r.fixture.requestCount() != 0 {
			t.Fatalf("%v: error = %v, requests = %d, want exit 2 before requests", args, r.err, r.fixture.requestCount())
		}
	}
}

func TestStorageProjectQuoteBelowMinimum(t *testing.T) {
	prices := 0
	r := runStorage(t, storageProjectPricingRoutes(t, "region-hcm", 29, &prices),
		"storage", "quote-create-project", "--type", "Gold", "--quota-gb", "29")
	if r.err == nil || exitCode(r.err) != 2 || prices != 0 || r.fixture.requestCount() != 7 {
		t.Fatalf("error = %v, prices = %d, requests = %d, want exit 2 after configuration only", r.err, prices, r.fixture.requestCount())
	}
}

func TestStorageProjectPricingInputJSONAndReadOnlyProfile(t *testing.T) {
	for _, op := range []string{"list-project-types", "quote-create-project"} {
		t.Run(op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			prices := 0
			routes := storageProjectPricingRoutes(t, "region-han", 31, &prices)
			input := `{"Region":"HAN02","Type":"Gold","QuotaGB":31,"Name":"ignored","MaxPrice":-1,"NoWait":true}`
			if op == "list-project-types" {
				routes = storageProjectPricingRoutes(t, "region-han", 30, &prices)
				input = `{"Region":"HAN02"}`
			}
			routes["/internal/v1/regions"] = jsonHandler(http.StatusOK, storageRegionsBody)
			fixture := newSvcFixture(routes)
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs([]string{"--profile", "agent", "storage", op, "--cli-input-json", input})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (%s)", err, stderr.String())
			}
			if prices != 1 || stdout.Len() == 0 {
				t.Fatalf("prices = %d, stdout = %s", prices, stdout.String())
			}
		})
	}
}

func TestStorageProjectQuoteHidesPurchaseFlags(t *testing.T) {
	for _, flag := range []string{"name", "max-price", "no-wait"} {
		prices := 0
		r := runStorage(t, storageProjectPricingRoutes(t, "region-hcm", 30, &prices),
			"storage", "quote-create-project", "--type", "Gold", "--quota-gb", "30", "--"+flag+"=ignored")
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "unknown flag") || r.fixture.requestCount() != 0 {
			t.Fatalf("--%s: error = %v, requests = %d, want unknown flag before requests", flag, r.err, r.fixture.requestCount())
		}
	}
}

func TestStorageProjectPricingWithholdsUpstreamBody(t *testing.T) {
	for _, op := range []string{"list-project-types", "quote-create-project"} {
		t.Run(op, func(t *testing.T) {
			prices := 0
			routes := storageProjectPricingRoutes(t, "region-hcm", 30, &prices)
			routes["/billing-api/v2/price"] = jsonHandler(http.StatusBadRequest, `upstream-private-body`)
			args := []string{"--debug", "storage", op}
			if op == "quote-create-project" {
				args = append(args, "--type", "Gold", "--quota-gb", "30")
			}
			r := runStorage(t, routes, args...)
			if r.err == nil || exitCode(r.err) != 1 || r.stdout != "" {
				t.Fatalf("error = %v, stdout = %s, want exit 1 with empty stdout", r.err, r.stdout)
			}
			var printed bytes.Buffer
			printError(&printed, r.err)
			if strings.Contains(printed.String()+r.stderr, "upstream-private-body") || strings.Contains(printed.String()+r.stderr, "test-token") {
				t.Fatalf("error or debug output exposed upstream body or token")
			}
		})
	}
}
