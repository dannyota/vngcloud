//go:build live

package vngcloud_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

func testLiveStoragePricing(ctx context.Context, t *testing.T) {
	bodies := map[string]json.RawMessage{}
	counts := map[string]int{}
	var priceBodies []json.RawMessage
	capture := vngcloud.WithResponseCapture(func(response vngcloud.ResponseCapture) {
		u, err := url.Parse(response.URL)
		if err != nil {
			t.Fatal("capture URL failed")
		}
		name := ""
		switch u.Path {
		case "/internal/v1/regions":
			name = "pricing-regions"
		case "/internal/v1/billing/project_types":
			name = "project-types"
		case "/internal/v1/billing/purchase_types":
			name = "purchase-types"
		case "/billing-api/v1/configurations":
			name = u.Query().Get("keys")
		case "/billing-api/v2/price":
			name = "project-price"
		default:
			return
		}
		bodies[name] = append(json.RawMessage(nil), response.Body...)
		counts[name]++
		if name == "project-price" {
			priceBodies = append(priceBodies, bodies[name])
		}
		writeStoragePricingOutput(t, "raw", name+"-"+strconv.Itoa(counts[name]), struct {
			Body json.RawMessage `json:"body"`
		}{response.Body})
	})
	cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"), vngcloud.WithTokenCache(filepath.Join(liveHome, ".vngcloud", "cache")), vngcloud.WithConfigFile(emptyFile(t, "config")), vngcloud.WithSharedCredentialsFile(emptyFile(t, "credentials")), capture)
	if err != nil {
		t.Fatal("storage pricing configuration failed")
	}
	client := storage.New(cfg)
	catalog, err := client.ListProjectTypes(ctx, nil)
	if err != nil {
		t.Fatal("ListProjectTypes failed")
	}
	writeStoragePricingOutput(t, "sdk", "project-types", catalog)
	var envelope struct {
		Datas []map[string]any `json:"datas"`
	}
	if json.Unmarshal(bodies["project-types"], &envelope) != nil {
		t.Fatal("catalog raw decode failed")
	}
	var decoded []map[string]any
	encoded, err := json.Marshal(catalog.Items)
	if err != nil || json.Unmarshal(encoded, &decoded) != nil || len(decoded) != len(envelope.Datas) {
		t.Fatal("catalog comparison failed")
	}
	for i, row := range envelope.Datas {
		compareStoragePricingFields(t, row, decoded[i])
	}
	configurationQuota := func(key string) int64 {
		var raw struct {
			Datas []struct {
				Value string `json:"value"`
			} `json:"datas"`
		}
		if json.Unmarshal(bodies[key], &raw) != nil || len(raw.Datas) != 1 {
			t.Fatal("quota comparison decode failed")
		}
		n, err := strconv.ParseInt(raw.Datas[0].Value, 10, 64)
		if err != nil {
			t.Fatal("quota comparison parse failed")
		}
		return n
	}
	minQuota := configurationQuota("vos_billing_normal_min_quota")
	maxQuota := configurationQuota("vos_billing_normal_max_quota")
	catalogQuotes := 0
	for _, typ := range catalog.Items {
		for _, offer := range typ.Offers {
			if offer.MinQuotaGB != minQuota || offer.MaxQuotaGB != maxQuota {
				t.Fatal("catalog quota comparison failed")
			}
			if offer.QuotedQuotaGB == 0 {
				continue
			}
			var raw struct {
				Data struct {
					OptimumPrice float64 `json:"optimumPrice"`
				} `json:"data"`
			}
			if catalogQuotes >= len(priceBodies) || json.Unmarshal(priceBodies[catalogQuotes], &raw) != nil {
				t.Fatal("catalog price comparison decode failed")
			}
			if offer.QuotedQuotaGB != minQuota || offer.MonthlyPrice != raw.Data.OptimumPrice {
				t.Fatal("catalog price comparison failed")
			}
			catalogQuotes++
		}
	}
	if catalogQuotes != len(priceBodies) {
		t.Fatal("catalog price count differs")
	}
	quotes := 0
	for _, name := range []string{"Gold", "Instant-Archive-2"} {
		quota := int64(0)
		for _, typ := range catalog.Items {
			if typ.Name != name {
				continue
			}
			for _, offer := range typ.Offers {
				if offer.PurchaseTypeID == 4 {
					quota = offer.MinQuotaGB
				}
			}
		}
		if quota <= 0 {
			t.Fatal("minimum quota unavailable")
		}
		quote, err := client.QuoteCreateProject(ctx, &storage.CreateProjectInput{Type: name, QuotaGB: quota})
		if err != nil {
			t.Fatal("QuoteCreateProject failed")
		}
		writeStoragePricingOutput(t, "sdk", "project-price-"+strconv.Itoa(counts["project-price"]), quote)
		var raw struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(bodies["project-price"], &raw) != nil {
			t.Fatal("price raw decode failed")
		}
		wire := map[string]any{"optimumPrice": quote.OptimumPrice, "originalPrice": quote.OriginalPrice, "discountPrice": quote.DiscountPrice, "discountPercent": quote.DiscountPercent, "propertiesPrice": quote.Properties}
		encoded, err := json.Marshal(wire)
		var actual map[string]any
		if err != nil || json.Unmarshal(encoded, &actual) != nil {
			t.Fatal("price comparison encode failed")
		}
		compareStoragePricingFields(t, raw.Data, actual)
		if quote.MonthlyPrice <= 0 || quote.MonthlyPrice != quote.OptimumPrice || quote.TotalPrice != quote.MonthlyPrice || quote.Currency != "VND" {
			t.Fatal("price totals failed")
		}
		quotes++
	}
	t.Logf("types=%d catalog-quotes=%d quotes=%d: pass", len(catalog.Items), catalogQuotes, quotes)
}

func writeStoragePricingOutput(t *testing.T, kind, name string, value any) {
	t.Helper()
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal("pricing output encode failed")
	}
	dir := filepath.Join("examples/basic/output", kind, "storage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal("pricing output directory failed")
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), body, 0o600); err != nil {
		t.Fatal("pricing output write failed")
	}
}

func compareStoragePricingFields(t *testing.T, raw, decoded map[string]any) {
	t.Helper()
	for key, value := range raw {
		actual, ok := decoded[key]
		if !ok {
			t.Fatalf("unmodeled pricing field %s", key)
		}
		if nested, ok := value.(map[string]any); ok {
			other, ok := actual.(map[string]any)
			if !ok {
				t.Fatal("pricing object shape differs")
			}
			compareStoragePricingFields(t, nested, other)
			continue
		}
		if rows, ok := value.([]any); ok {
			other, ok := actual.([]any)
			if !ok || len(rows) != len(other) {
				t.Fatal("pricing array shape differs")
			}
			for i, row := range rows {
				if object, ok := row.(map[string]any); ok {
					decoded, ok := other[i].(map[string]any)
					if !ok {
						t.Fatal("pricing item shape differs")
					}
					compareStoragePricingFields(t, object, decoded)
				} else if !reflect.DeepEqual(row, other[i]) {
					t.Fatal("pricing array value differs")
				}
			}
			continue
		}
		if !reflect.DeepEqual(value, actual) {
			t.Fatalf("pricing field %s differs", key)
		}
	}
}
