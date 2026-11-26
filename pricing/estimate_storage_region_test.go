package pricing

import (
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateStorageCatalogRegion(t *testing.T) {
	for _, tc := range []struct {
		name, region, rows string
		valid              bool
	}{
		{"name", "DAD01", `[{"regionId":"region-2","regionName":"DAD01"}]`, true},
		{"id", "region-2", `[{"regionId":"region-2","regionName":"DAD01"}]`, true},
		{"unknown", "missing", `[{"regionId":"region-2","regionName":"DAD01"}]`, false},
		{"ambiguous", "DAD01", `[{"regionId":"region-2","regionName":"DAD01"},{"regionId":"region-3","regionName":"dad01"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quotes := 0
			handler := estimateStorageHandler(t, func(w http.ResponseWriter, r *http.Request) {
				quotes++
				testutil.WriteFixture(t, w, "../testdata/storage/discovery_quote_gold.json")
			})
			cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/internal/v1/regions" {
					_, _ = fmt.Fprintf(w, `{"success":true,"datas":%s}`, tc.rows)
					return
				}
				handler(w, r)
			}))
			out, err := New(cfg).Estimate(estimateContext(), &EstimateInput{Storage: []EstimateStorage{{Key: "storage", Region: tc.region, Type: "Gold", QuotaGB: 30, Quantity: 1}}})
			if tc.valid {
				if err != nil || out == nil || out.Items[0].Region != "DAD01" || quotes != 1 {
					t.Fatalf("output %+v, error %v, quotes %d", out, err, quotes)
				}
				return
			}
			if err == nil || quotes != 0 {
				t.Fatalf("error %v, quotes %d", err, quotes)
			}
		})
	}
}
