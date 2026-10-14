package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func notificationQuoteClient(t *testing.T, packages, quota, price string, calls *[]string) *Client {
	t.Helper()
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Path)
		if r.URL.RawQuery != "" {
			t.Error("unexpected query")
		}
		switch r.URL.Path {
		case "/billing-api/v1/email/packages":
			if r.Method != http.MethodGet {
				t.Error("packages method")
			}
			_, _ = w.Write([]byte(packages))
		case "/billing-api/v1/email/quota":
			if r.Method != http.MethodGet {
				t.Error("quota method")
			}
			if quota == "" {
				w.WriteHeader(404)
				testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaEmailNotFound.json")
			} else {
				_, _ = w.Write([]byte(quota))
			}
		case "/billing-api/v2/email/prices/created-price":
			if r.Method != http.MethodPost {
				t.Error("quote method")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body, map[string]any{"redirectUrl": "", "packageId": "opaque/package", "quantity": float64(1)}) {
				t.Errorf("body=%v", body)
			}
			_, _ = w.Write([]byte(price))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
}

func TestNotificationQuoteFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture     string
		list, quote float64
	}{{"QuoteCreateNotificationQuotaEmailFree", 0, 0}, {"QuoteCreateNotificationQuotaEmail", 110000, 100000}} {
		t.Run(tc.fixture, func(t *testing.T) {
			calls := []string{}
			c := notificationQuoteClient(t, `[{"id":"opaque/package","name":"package","amount":20,"price":`+jsonNumber(tc.list)+`}]`, "", string(quotaFixture(t, tc.fixture, nil)), &calls)
			out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "eMaIl", PackageID: "opaque/package", MaxPrice: math.NaN(), NoWait: true})
			if err != nil {
				t.Fatal(err)
			}
			if out.PackageID != "opaque/package" || out.Quantity != 1 || out.MonthPeriod != 1 || out.ListPrice != tc.list || out.OptimumPrice != tc.quote || out.GuardPrice != tc.list || out.Currency != "VND" || out.ResourceID != "" || out.ExpectedEndTime != nil || len(calls) != 3 {
				t.Fatalf("out=%+v calls=%v", out, calls)
			}
		})
	}
}

func jsonNumber(v float64) string { b, _ := json.Marshal(v); return string(b) }

func TestNotificationQuotePreparationRefusals(t *testing.T) {
	for _, tc := range []struct{ name, packages, quota, id string }{
		{name: "existing", packages: `[{"id":"opaque/package","name":"p","price":0,"amount":20}]`, quota: string(quotaFixture(t, "GetNotificationQuotaEmail", nil))},
		{name: "unknown", packages: `[]`, id: "unknown"},
		{name: "duplicate", packages: `[{"id":"p","name":"p","price":0,"amount":20},{"id":"p","name":"p","price":0,"amount":20}]`, id: "p"},
		{name: "ambiguous", packages: `[{"id":"p","name":"p","price":0,"amount":20},{"id":"q","name":"q","price":0,"amount":20}]`},
		{name: "missing price", packages: `[{"id":"p","name":"p","amount":20}]`, id: "p"},
		{name: "null price", packages: `[{"id":"p","name":"p","price":null,"amount":20}]`, id: "p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := []string{}
			c := notificationQuoteClient(t, tc.packages, tc.quota, `{"optimumPrice":0}`, &calls)
			out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", PackageID: tc.id})
			if out != nil || err == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			for _, path := range calls {
				if strings.Contains(path, "prices") {
					t.Fatal("quoted refused preparation")
				}
			}
		})
	}
}

func TestNotificationQuotePriceDecoder(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`, `{"optimumPrice":null}`, `{"optimumPrice":-1}`, `{"optimumPrice":"0"}`, `{"optimumPrice":1e309}`} {
		calls := []string{}
		c := notificationQuoteClient(t, `[{"id":"opaque/package","name":"p","price":0,"amount":20}]`, "", body, &calls)
		out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		var api *core.APIError
		if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
			t.Fatalf("body=%s out=%v err=%v", body, out, err)
		}
	}
	calls := []string{}
	c := notificationQuoteClient(t, `[{"id":"opaque/package","name":"p","price":110000,"amount":20}]`, "", `{"optimumPrice":0}`, &calls)
	out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", PackageID: "opaque/package"})
	if out != nil || !errors.Is(err, ErrUnpriced) || !errors.Is(err, vngcloud.ErrUnpriced) {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestNotificationFrozenTermsAndGuard(t *testing.T) {
	calls := []string{}
	c := notificationQuoteClient(t, `[{"id":"opaque/package","name":"p","price":110000,"amount":20}]`, "", `{"optimumPrice":100000}`, &calls)
	in := &CreateNotificationQuotaInput{Kind: "Email", PackageID: "opaque/package"}
	var credentials monitorReadCredentials
	terms, err := c.prepareNotificationCreate(context.Background(), "monitor.test", in, &credentials)
	if err != nil {
		t.Fatal(err)
	}
	in.PackageID = "changed"
	in.Kind = "SMS"
	if terms.quoteBody().PackageID != "opaque/package" || terms.monthPeriod != 1 || terms.kind != "email" {
		t.Fatalf("terms=%+v", terms)
	}
	body := terms.orderBody()
	encoded, _ := json.Marshal(body)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	want := map[string]any{"packageId": "opaque/package", "monthPeriod": float64(1), "redirectUrl": "https://vmonitor.console.vngcloud.vn/quota-usages/notification", "pay": true}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("order body=%s", encoded)
	}
	for _, tc := range []struct {
		list, quote, max float64
		want             error
	}{{0, 0, 0, nil}, {110000, 100000, 100000, ErrPriceAboveMax}, {110000, 100000, 110000, nil}, {100000, 110000, 100000, ErrPriceAboveMax}, {110000, 0, 110000, ErrUnpriced}, {0, 1, 0, ErrPriceAboveMax}, {0, 0, math.NaN(), core.ErrInvalidInput}, {0, 0, math.Inf(1), core.ErrInvalidInput}, {0, 0, -1, core.ErrInvalidInput}} {
		err := checkNotificationPrice("monitor.test", tc.list, tc.quote, tc.max)
		if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%+v err=%v", tc, err)
		}
	}
}

func TestNotificationQuoteSafety(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"oversize", 200, `{"extra":"` + strings.Repeat("x", quotaMaxBody) + `"}`},
		{"malformed", 200, `{"optimumPrice":private-canary`},
		{"unauthorized", 401, `{"message":"private-canary"}`},
		{"forbidden", 403, `{"message":"private-canary"}`},
		{"not found", 404, `{"message":"Not found email quota"}`},
		{"rate limited", 429, `{"message":"private-canary"}`},
		{"server error", 500, `{"message":"private-canary"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captures := 0
			c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v1/email/quota":
					w.WriteHeader(404)
					testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaEmailNotFound.json")
				case "/billing-api/v1/email/packages":
					_, _ = w.Write([]byte(`[{"id":"p","name":"p","price":0,"amount":20}]`))
				case "/billing-api/v2/email/prices/created-price":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				default:
					t.Fatal("unexpected request")
				}
			}), func(transport.Capture) { captures++ }))
			out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.StatusCode != tc.status || captures != 0 || strings.Contains(err.Error(), "private-canary") {
				t.Fatalf("out=%v err=%v captures=%d", out, err, captures)
			}
		})
	}
}

func TestNotificationQuoteRefreshAndRetry(t *testing.T) {
	target := "/billing-api/v2/email/prices/created-price"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v1/email/quota":
			w.WriteHeader(404)
			testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaEmailNotFound.json")
		case "/billing-api/v1/email/packages":
			_, _ = w.Write([]byte(`[{"id":"p","name":"p","price":0,"amount":20}]`))
		case target:
			_, _ = w.Write([]byte(`{"optimumPrice":0,"private":"synthetic-token-A synthetic-token-B"}`))
		default:
			t.Fatal("unexpected request")
		}
	})
	c := monitorRefreshClient(t, "synthetic-token-A", "synthetic-token-B", target, handler)
	out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if err != nil {
		t.Fatal(err)
	}
	assertMonitorCredentialsAbsent(t, out, err, "synthetic-token-A", "synthetic-token-B")
	attempts := 0
	c = New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == target {
			attempts++
			if attempts == 1 {
				w.WriteHeader(502)
				return
			}
		}
		handler.ServeHTTP(w, r)
	})))
	out, err = c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if err != nil || out == nil || attempts != 2 {
		t.Fatalf("out=%v err=%v attempts=%d", out, err, attempts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err = c.QuoteCreateNotificationQuota(ctx, &CreateNotificationQuotaInput{Kind: "Email"})
	if out != nil || !errors.Is(err, context.Canceled) || attempts != 2 {
		t.Fatalf("out=%v err=%v attempts=%d", out, err, attempts)
	}
}

func TestNotificationQuoteOperationCredentialRedaction(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, stage := range []string{"package", "quote error", "quote decode"} {
		t.Run(stage, func(t *testing.T) {
			quotes := 0
			c := monitorRefreshClient(t, first, second, emailQuotaPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case emailQuotaPath:
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"message":"Not found email quota"}`))
				case "/billing-api/v1/email/packages":
					id := "p"
					if stage == "package" {
						id = first
					}
					_, _ = w.Write([]byte(`[{"id":` + jsonString(id) + `,"name":"p","price":0,"amount":20}]`))
				case "/billing-api/v2/email/prices/created-price":
					quotes++
					if stage == "quote error" {
						w.WriteHeader(403)
					}
					body := `{"message":` + jsonString(first) + `}`
					if stage == "quote decode" {
						body = `{"optimumPrice":` + jsonString(first)
					}
					_, _ = w.Write([]byte(body))
				default:
					t.Error("unexpected request")
				}
			}))
			out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
			if out != nil || err == nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if stage == "package" && quotes != 0 {
				t.Error("credential package sent to quote")
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}
