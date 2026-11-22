package pricequote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/quoteinput"
	"danny.vn/vngcloud/internal/testutil"
)

func TestEstimateRawQuotes(t *testing.T) {
	for _, tc := range []struct {
		file    string
		storage bool
		want    float64
	}{
		{"compute/discovery_quote_server_s2.json", false, 347800},
		{"volume/discovery_quote_plain.json", false, 3200},
		{"volume/discovery_quote_encrypted.json", false, 3200},
		{"network/discovery_quote_public_vm.json", false, 120000},
		{"storage/discovery_quote_gold.json", true, 30000},
	} {
		t.Run(tc.file, func(t *testing.T) {
			price, err := DecodeEstimate("quote", 200, json.RawMessage(testutil.FixtureBody(t, "../../testdata/"+tc.file)), tc.storage)
			if err != nil || price.MonthlyPrice == nil || *price.MonthlyPrice != tc.want || price.Currency != "VND" || len(price.Breakdown) == 0 {
				t.Fatalf("price %+v error %v", price, err)
			}
		})
	}
}

func TestEstimateQuoteBoundaries(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"optimumPrice":null}`, `{"optimumPrice":0}`, `{"optimumPrice":-1}`, `{"optimumPrice":1e999}`, `{"optimumPrice":"1"}`,
		`{"optimumPrice":1,"optimumPrice":2}`, `{"optimumPrice":1,"currency":"USD"}`, `{"optimumPrice":1,"currency":null}`, `{"optimumPrice":1,"currency":""}`,
		`{"optimumPrice":1,"Currency":"USD"}`, `{"OptimumPrice":2,"optimumPrice":1}`, `{"optimumPrice":1,"propertiesPrice":[{"name":"line","MonthlyPrice":2,"monthlyPrice":1}]}`,
		`{"optimumPrice":1,"originalPrice":-1}`, `{"optimumPrice":1,"discountPercent":"0"}`, `{"optimumPrice":1,"success":false}`, `{"optimumPrice":1,"errorCode":"bad"}`, `{"optimumPrice":1,"isNext":false}`,
		`{"optimumPrice":1,"propertiesPrice":[{"name":"line","monthlyPrice":-1}]}`,
		`{"optimumPrice":1,"propertiesPrice":[{"name":"line","monthlyPrice":"1"}]}`,
		`{"optimumPrice":1,"propertiesPrice":[{"name":"line","monthlyPrice":1,"monthlyPrice":2}]}`,
		`{"optimumPrice":1,"propertiesPrice":[null]}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, err := DecodeEstimate("quote", 200, json.RawMessage(body), false)
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
				t.Fatalf("error %v", err)
			}
		})
	}
	for _, body := range []string{`{"data":{"optimumPrice":1}}`, `{"success":null,"data":{"optimumPrice":1}}`, `{"success":true,"data":null}`, `{"success":true,"data":{"optimumPrice":1},"isNext":true}`} {
		if _, err := DecodeEstimate("quote", 200, json.RawMessage(body), true); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestQuotePresenceAndAccountingModes(t *testing.T) {
	raw := json.RawMessage(`{"optimumPrice":9,"propertiesPrice":[{"name":"INSTANCE TYPE","monthlyPrice":3,"optimumPrice":3},{"name":"ROOT DISK","monthlyPrice":null},{"name":"DATA DISK","currentPrice":2},{"name":"CES","monthlyPrice":0}]}`)
	full, err := DecodeEstimate("quote", 200, raw, false)
	if err != nil || *full.MonthlyPrice != 9 || full.OriginalPrice != nil || full.DiscountPercent != nil || len(full.Breakdown) != 4 || full.Breakdown[1].MonthlyPrice != nil || full.Breakdown[2].MonthlyPrice != nil || !full.BreakdownMismatch {
		t.Fatalf("full %+v error %v", full, err)
	}
	instance, err := DecodeInstance("quote", 200, raw)
	if err != nil || *instance.MonthlyPrice != 3 || instance.OriginalPrice != nil {
		t.Fatalf("instance %+v error %v", instance, err)
	}
	legacy, err := DecodeMonthly("quote", 200, json.RawMessage(`{"optimumPrice":1,"currency":"USD","propertiesPrice":"ignored"}`))
	if err != nil || legacy.Currency != "USD" {
		t.Fatalf("legacy %+v error %v", legacy, err)
	}
}

func TestGuardedStorageQuoteRefusalAndMemoization(t *testing.T) {
	for _, code := range []int{112, 401, 403, 404, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "code": code, "errorMsg": "refused"})
			})))
			s, _ := discovery.New(context.Background(), 2)
			defer s.Close()
			_, err := Quote(c, s, pricecatalog.StorageQuote(c, "quote", "region-2", quoteinput.Storage(30, 4, 1)), true)
			var api *core.APIError
			if !errors.As(err, &api) || api.StatusCode != 200 || calls != 1 {
				t.Fatalf("error %v calls %d", err, calls)
			}
			want := map[int]error{112: core.ErrInvalidInput, 401: core.ErrAuth, 403: core.ErrPermission, 404: core.ErrNotFound, 429: core.ErrRateLimited}[code]
			if want != nil && !errors.Is(err, want) {
				t.Fatalf("error %v, want %v", err, want)
			}
		})
	}
}

func TestGuardedQuoteRejectsBeforeMemoization(t *testing.T) {
	calls := 0
	c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"optimumPrice":1,"optimumPrice":2}`))
			return
		}
		_, _ = w.Write([]byte(`{"optimumPrice":3}`))
	})))
	now := time.Unix(0, 0)
	ctx := discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
	s, _ := discovery.New(ctx, 2)
	defer s.Close()
	req := pricecatalog.RegionalQuote(c, "quote", quoteinput.PublicIP("public-vm"))
	if _, err := Quote(c, s, req, false); err == nil {
		t.Fatal("accepted duplicate quote keys")
	}
	for range 2 {
		price, err := Quote(c, s, req, false)
		if err != nil || *price.MonthlyPrice != 3 {
			t.Fatalf("price %+v error %v", price, err)
		}
	}
	if calls != 2 || s.Requests() != 2 {
		t.Fatalf("calls %d requests %d", calls, s.Requests())
	}
}

func TestGuardedStorageRefusalWithholdsServerText(t *testing.T) {
	c := core.ClientOf(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"code":"reflected-code","errorMsg":"reflected-message"}`))
	})))
	s, _ := discovery.New(context.Background(), 1)
	defer s.Close()
	_, err := Quote(c, s, pricecatalog.StorageQuote(c, "quote", "region-2", quoteinput.Storage(30, 4, 1)), true)
	var api *core.APIError
	if !errors.As(err, &api) || api.StatusCode != 200 || strings.Contains(err.Error(), "reflected") || strings.Contains(api.Code, "reflected") {
		t.Fatalf("unsafe refusal error %v", err)
	}
}
