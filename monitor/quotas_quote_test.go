package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
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

func TestNotificationEmailReads(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RawQuery != "" {
			t.Fatalf("request=%s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/billing-api/v1/email/quota":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaEmail.json")
		case "/billing-api/v1/email/quota/usages":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaUsageEmail.json")
		case "/billing-api/v1/quotas/usages":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaUsageEmail.json")
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	out, err := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: "eMaIl"})
	if err != nil || out == nil || out.Quota.ID != "quota-email-1" || out.Quota.Status != QuotaStatusActive {
		t.Fatalf("out=%v err=%v", out, err)
	}
	usage, err := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
	if err != nil || usage == nil || usage.Usage.Amount != 0 || usage.Usage.Descriptions["amount"].Unit != "/month" {
		t.Fatalf("out=%v err=%v", usage, err)
	}
	composite, err := c.GetQuotaUsage(context.Background(), nil)
	if err != nil || len(composite.Usage.EmailUsages) != 1 || composite.Usage.EmailUsages[0].Amount != 0 {
		t.Fatalf("out=%v err=%v", composite, err)
	}
}

func TestNotificationAbsence(t *testing.T) {
	for _, tc := range []struct {
		body     string
		verified bool
	}{
		{`{"message":"Not found email quota"}`, true},
		{`{"message":"Not found the Resource"}`, false},
		{`{"message":"private proxy missing"}`, false},
		{`{"message":"Not found email quota","extra":true}`, false},
		{`{"message":"Not found email quota"`, false},
	} {
		calls := 0
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(404)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, err := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: "Email"})
		var absent *notificationQuotaAbsent
		if !errors.Is(err, core.ErrNotFound) || errors.As(err, &absent) != tc.verified || strings.Contains(err.Error(), "private") {
			t.Fatalf("err=%v verified=%v", err, tc.verified)
		}
		out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		if out != nil || err == nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
		if !tc.verified && calls != 2 {
			t.Fatalf("arbitrary 404 permitted preparation: calls=%d", calls)
		}
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaUsageEmailNotFound.json")
	}))
	out, err := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
	var absent *notificationQuotaAbsent
	if out != nil || !errors.Is(err, core.ErrNotFound) || errors.As(err, &absent) {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestNotificationLocalSupportLimit(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected request") }))
	for _, kind := range []string{"", "../email", "Slack"} {
		_, err := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: kind})
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Fatal(err)
		}
		_, err = c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: kind})
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Fatal(err)
		}
		_, err = c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: kind})
		if !errors.Is(err, core.ErrInvalidInput) {
			t.Fatal(err)
		}
		if strings.EqualFold(kind, "SMS") && !strings.Contains(err.Error(), "SMS") {
			t.Fatal("missing support limit")
		}
	}
}

func TestNotificationMalformedReads(t *testing.T) {
	for _, fixture := range []string{"GetNotificationQuotaEmail", "GetNotificationQuotaUsageEmail"} {
		keys := []string{"id", "packageId", "endTime", "autoRenew", "poc", "system"}
		if strings.Contains(fixture, "Usage") {
			keys = []string{"usage", "description"}
		}
		for _, key := range keys {
			for _, value := range []string{"missing", "null", "invalid"} {
				t.Run(fixture+"/"+key+"/"+value, func(t *testing.T) {
					body := quotaFixture(t, fixture, func(m map[string]json.RawMessage) {
						switch value {
						case "missing":
							delete(m, key)
						case "null":
							m[key] = json.RawMessage(`null`)
						default:
							m[key] = json.RawMessage(`42`)
						}
					})
					c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
					var err error
					if strings.Contains(fixture, "Usage") {
						out, e := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
						err = e
						if out != nil {
							t.Fatal("partial output")
						}
					} else {
						out, e := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: "Email"})
						err = e
						if out != nil {
							t.Fatal("partial output")
						}
					}
					var api *core.APIError
					if !errors.As(err, &api) || api.Code != "InvalidResponse" || strings.Contains(err.Error(), "private-canary") {
						t.Fatalf("err=%v", err)
					}
				})
			}
		}
	}
	for _, amount := range []string{`null`, `1.5`, `"1"`, `1e309`} {
		body := `{"usage":{"amount":` + amount + `},"description":{}}`
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		out, err := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
		if out != nil || err == nil {
			t.Fatalf("out=%v err=%v", out, err)
		}
	}
}

func TestNotificationRefreshRedaction(t *testing.T) {
	for _, usage := range []bool{false, true} {
		target := "/billing-api/v1/email/quota"
		fixture := "GetNotificationQuotaEmail"
		if usage {
			target += "/usages"
			fixture = "GetNotificationQuotaUsageEmail"
		}
		body := quotaFixture(t, fixture, func(m map[string]json.RawMessage) {
			if usage {
				m["description"] = json.RawMessage(`{"synthetic-token-A":{"name":"synthetic-token-B","specs":"synthetic-token-A","unit":"synthetic-token-B"}}`)
			} else {
				m["name"] = json.RawMessage(`"synthetic-token-A synthetic-token-B"`)
			}
		})
		c := monitorRefreshClient(t, "synthetic-token-A", "synthetic-token-B", target, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
		if usage {
			out, err := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
			if err != nil {
				t.Fatal(err)
			}
			assertMonitorCredentialsAbsent(t, out, err, "synthetic-token-A", "synthetic-token-B")
		} else {
			out, err := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: "Email"})
			if err != nil {
				t.Fatal(err)
			}
			assertMonitorCredentialsAbsent(t, out, err, "synthetic-token-A", "synthetic-token-B")
		}
	}
}

func TestSyntheticQuotaOperationCredentialRedaction(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, result := range []string{"success", "error", "decode", "route"} {
		t.Run(result, func(t *testing.T) {
			calls := 0
			c := monitorRefreshClient(t, first, second, "/billing-api/v1/synthetic/quota", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				name := "GetSyntheticQuota"
				if calls > 1 {
					name = "GetSyntheticQuotaDetail"
				}
				body := checkAlarmFixture(t, name)
				if result == "route" {
					body = strings.ReplaceAll(body, "quota-syn-1", first)
				}
				if calls > 1 {
					body = strings.ReplaceAll(body, "Basic", first+" "+second)
					if result == "error" {
						w.WriteHeader(403)
						body = `{"message":` + jsonString(first) + `}`
					}
					if result == "decode" {
						body = `{"className":` + jsonString(first)
					}
				}
				_, _ = w.Write([]byte(body))
			}))
			out, err := c.GetSyntheticQuota(context.Background(), nil)
			if result == "success" {
				if err != nil || out == nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if err == nil || out != nil {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if result == "route" && calls != 1 {
				t.Errorf("credential ID routed: calls=%d", calls)
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}

func TestCheckAlarmCredentialReferenceRefused(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, history := range []bool{false, true} {
		t.Run(strconv.FormatBool(history), func(t *testing.T) {
			calls := 0
			c := monitorRefreshClient(t, first, second, "/vmonitor-uptime-manager/v1/uptimes/chk-1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls != 1 {
					t.Error("credential reference routed")
					w.WriteHeader(403)
					return
				}
				_, _ = w.Write([]byte(strings.ReplaceAll(checkAlarmFixture(t, "GetCheck"), "alarm-1", first)))
			}))
			var out any
			var err error
			if history {
				out, err = c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
			} else {
				out, err = c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("err=%v", err)
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}

type monitorFailureTokens struct{ monitorRefreshTokens }

func (s *monitorFailureTokens) Token(ctx context.Context) (transport.Token, error) {
	if s.calls == 2 {
		return transport.Token{}, fmt.Errorf("%s %s: %w", s.first, s.second, core.ErrAuth)
	}
	return s.monitorRefreshTokens.Token(ctx)
}

func TestMonitorOperationTokenSourceErrorWithheld(t *testing.T) {
	for _, operation := range []string{"quota", "alarm", "history"} {
		t.Run(operation, func(t *testing.T) {
			const first, second = "synthetic-token-A", "synthetic-token-B"
			source := &monitorFailureTokens{monitorRefreshTokens{first: first, second: second}}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls != 2 {
					w.WriteHeader(401)
					return
				}
				name := "GetCheck"
				if operation == "quota" {
					name = "GetSyntheticQuota"
				}
				_, _ = w.Write([]byte(checkAlarmFixture(t, name)))
			}))
			defer server.Close()
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
			var out any
			var err error
			switch operation {
			case "quota":
				out, err = c.GetSyntheticQuota(context.Background(), nil)
			case "alarm":
				out, err = c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
			case "history":
				out, err = c.ListCheckAlarmHistory(context.Background(), &ListCheckAlarmHistoryInput{CheckID: "chk-1"})
			}
			if !errors.Is(err, core.ErrAuth) || calls != 3 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}

func TestMonitorReadsKeepLoginErrors(t *testing.T) {
	operations := monitorReadFailures()
	operations["GetNotificationQuota"] = func(c *Client) error {
		out, err := c.GetNotificationQuota(context.Background(), &GetNotificationQuotaInput{Kind: "Email"})
		if out != nil {
			return errors.New("unexpected output")
		}
		return err
	}
	operations["QuoteCreateNotificationQuota"] = func(c *Client) error {
		out, err := c.QuoteCreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		if out != nil {
			return errors.New("unexpected output")
		}
		return err
	}
	operations["CreateNotificationQuota"] = func(c *Client) error {
		out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		if out != nil {
			return errors.New("unexpected output")
		}
		return err
	}
	for reader, read := range operations {
		for _, kind := range []string{"password", "service-account", "cache"} {
			t.Run(reader+"/"+kind, func(t *testing.T) {
				monitorCalls, loginCalls := 0, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/billing-api/") || strings.HasPrefix(r.URL.Path, "/vmonitor-") {
						monitorCalls++
						w.WriteHeader(500)
						return
					}
					loginCalls++
					_, _ = w.Write([]byte(`<html><input name="_csrf" value="synthetic-csrf"></html>`))
				}))
				defer server.Close()
				opts := []vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: server.URL, Dashboard: server.URL + "/", Monitor: server.URL})}
				if kind == "password" {
					opts = append(opts, vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "synthetic-root", Username: "synthetic-user", Password: "synthetic-password"}))
				} else {
					opts = append(opts, vngcloud.WithServiceAccount(&vngcloud.ServiceAccountAuth{ClientID: "synthetic-client", ClientSecret: "synthetic-secret"}), core.WithServiceAccountTestTransport(monitorFailureHTTP(func(*http.Request) (*http.Response, error) {
						loginCalls++
						return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
					}), time.Now))
					if kind == "cache" {
						path := filepath.Join(t.TempDir(), "cache-file")
						if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
							t.Fatal(err)
						}
						opts = append(opts, vngcloud.WithTokenCache(path))
					}
				}
				cfg, err := vngcloud.NewConfig(opts...)
				if err != nil {
					t.Fatal(err)
				}
				err = read(New(cfg))
				if monitorCalls != 0 {
					t.Errorf("monitor requests = %d", monitorCalls)
				}
				switch kind {
				case "password":
					var login *vngcloud.LoginError
					if !errors.As(err, &login) || login.Status != 200 || !login.CaptchaSuspected || !errors.Is(err, vngcloud.ErrAuth) {
						t.Errorf("login metadata lost: %v", err)
					}
				case "service-account":
					var login *vngcloud.ServiceAccountLoginError
					if !errors.As(err, &login) || login.Status != 401 || !errors.Is(err, vngcloud.ErrAuth) {
						t.Errorf("exchange identity lost: %v", err)
					}
				case "cache":
					if !errors.Is(err, vngcloud.ErrTokenCache) {
						t.Errorf("cache identity lost: %v", err)
					}
				}
				if (kind == "cache" && loginCalls != 0) || (kind != "cache" && loginCalls == 0) {
					t.Errorf("login calls = %d", loginCalls)
				}
			})
		}
	}
}
