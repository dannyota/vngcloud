package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestQuotaUsageFixture(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/billing-api/v1/quotas/usages" || r.URL.RawQuery != "" {
			t.Fatalf("request = %s %s", r.Method, r.URL)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaUsage.json")
	}))
	out, err := c.GetQuotaUsage(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	u := out.Usage
	if len(u.SyntheticUsages) != 1 || u.SyntheticUsages[0].API != 1 || len(u.EmailUsages) != 1 || u.EmailUsages[0].Amount != 4 || u.SyntheticSeverity != "warning" || u.EmailSeverity != "good" || u.NotificationSeverity != "good" || u.FreeEmailQuota != 0 || u.FreeSMSQuota != 0 || len(u.UnmodeledKinds) != 0 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestQuotaNotificationPackagesFixtures(t *testing.T) {
	for _, tt := range []struct {
		kind, segment, fixture string
		count                  int
	}{
		{"eMaIl", "email", "ListNotificationPackagesEmail", 6},
		{"sMs", "sms", "ListNotificationPackagesSMS", 8},
	} {
		t.Run(tt.segment, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/billing-api/v1/"+tt.segment+"/packages" {
					t.Fatalf("request = %s %s", r.Method, r.URL)
				}
				testutil.WriteFixture(t, w, "../testdata/monitor/"+tt.fixture+".json")
			}))
			out, err := c.ListNotificationPackages(context.Background(), &ListNotificationPackagesInput{Kind: tt.kind})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != tt.count || out.Items[0].ID == "" || out.Items[0].Price == nil || out.Items[0].Amount == 0 {
				t.Fatalf("packages = %+v", out)
			}
			if tt.segment == "email" && out.Items[3].ID != "vMonitor-Platform-Notification-email5k" {
				t.Fatalf("opaque ID = %q", out.Items[3].ID)
			}
		})
	}
}

func TestQuotaNotificationKindValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected request") }))
	for _, in := range []*ListNotificationPackagesInput{nil, {}, {Kind: "../email"}, {Kind: "Slack"}, {Kind: " Email"}} {
		out, err := c.ListNotificationPackages(context.Background(), in)
		if out != nil || !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("output = %+v, error = %v", out, err)
		}
	}
}

func TestQuotaSyntheticFixtures(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/billing-api/v1/synthetic/quota":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
		case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDetail.json")
		case "/billing-api/v1/synthetic/quota/usages":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaUsage.json")
		case "/billing-api/v1/synthetic/packages":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticPackages.json")
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	out, err := c.GetSyntheticQuota(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Quota.ID != "quota-syn-1" || out.Quota.Status != QuotaStatusActive || out.Quota.StartTime.IsZero() || out.Quota.EndTime.IsZero() || out.Quota.CreatedAt.IsZero() || out.Quota.UpdatedAt.IsZero() || out.Quota.AutoRenew || out.Quota.POC || out.Quota.System || out.Detail.API != 10 || out.Detail.Location != 1 || out.Detail.Price == nil || *out.Detail.Price != 0 || out.Detail.ClassName != "Basic" {
		t.Fatalf("quota = %+v", out)
	}
	usage, err := c.GetSyntheticQuotaUsage(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Usage.Counts.API != 1 || usage.Usage.Descriptions["api"].Name != "Number of API tests" || usage.Usage.Descriptions["api"].Unit != "/month" {
		t.Fatalf("usage = %+v", usage)
	}
	packages, err := c.listSyntheticPackages(context.Background(), "monitor.GetSyntheticQuota")
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].API != 10 || packages[0].Location != 1 || packages[0].Price == nil || *packages[0].Price != 0 {
		t.Fatalf("packages = %+v", packages)
	}
	if calls != 4 {
		t.Fatalf("calls = %d", calls)
	}
}

func quotaFixture(t *testing.T, name string, change func(map[string]json.RawMessage)) []byte {
	t.Helper()
	b, err := os.ReadFile("../testdata/monitor/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(obj)
	}
	b, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestQuotaUsageUnmodeledAndIntegralCounts(t *testing.T) {
	body := quotaFixture(t, "GetQuotaUsage", func(m map[string]json.RawMessage) {
		for _, key := range []string{"logUsages", "metricUsages", "smsUsages"} {
			m[key] = json.RawMessage(`[{"private":"discard me"}]`)
		}
		m["freeEmailQuota"] = json.RawMessage(`20.0`)
		m["emailUsages"] = json.RawMessage(`[{"amount":4.0}]`)
	})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	out, err := c.GetQuotaUsage(context.Background(), &GetQuotaUsageInput{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Usage.UnmodeledKinds, ",") != "log,metric,sms" || out.Usage.FreeEmailQuota != 20 || out.Usage.EmailUsages[0].Amount != 4 {
		t.Fatalf("usage = %+v", out)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "discard me") {
		t.Fatalf("unmodeled data exposed: %s", b)
	}
}

func TestQuotaSyntheticInvalidResponses(t *testing.T) {
	for _, tt := range []struct{ name, fixture, key, value string }{
		{"bad path", "GetSyntheticQuota", "id", `"../secret"`},
		{"empty id", "GetSyntheticQuota", "id", `""`},
		{"bad time", "GetSyntheticQuota", "endTime", `"secret"`},
		{"missing time", "GetSyntheticQuota", "startTime", `null`},
		{"boolean flag", "GetSyntheticQuota", "autoRenew", `true`},
		{"unknown flag", "GetSyntheticQuota", "poc", `2`},
		{"string flag", "GetSyntheticQuota", "poc", `"0"`},
		{"missing flag", "GetSyntheticQuota", "autoRenew", `null`},
		{"fractional period", "GetSyntheticQuota", "autoRenewPeriod", `0.5`},
		{"resource mismatch", "GetSyntheticQuotaDetail", "resourceId", `"other"`},
		{"package mismatch", "GetSyntheticQuotaDetail", "packageId", `"other"`},
		{"fractional capacity", "GetSyntheticQuotaDetail", "api", `10.5`},
		{"negative price", "GetSyntheticQuotaDetail", "price", `-1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := quotaFixture(t, tt.fixture, func(m map[string]json.RawMessage) { m[tt.key] = json.RawMessage(tt.value) })
			calls := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 && tt.fixture == "GetSyntheticQuotaDetail" {
					testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
					return
				}
				_, _ = w.Write(body)
			}))
			out, err := c.GetSyntheticQuota(context.Background(), nil)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.Operation != "monitor.GetSyntheticQuota" || strings.Contains(err.Error(), "secret") {
				t.Fatalf("output = %+v, error = %+v", out, err)
			}
			want := 1
			if tt.fixture == "GetSyntheticQuotaDetail" {
				want = 2
			}
			if calls != want {
				t.Fatalf("calls = %d, want %d", calls, want)
			}
		})
	}
}

func TestQuotaSyntheticUnknownStatusAndNumericFlags(t *testing.T) {
	body := quotaFixture(t, "GetSyntheticQuota", func(m map[string]json.RawMessage) {
		m["status"] = json.RawMessage(`"FUTURE"`)
		m["autoRenew"] = json.RawMessage(`1`)
		m["poc"] = json.RawMessage(`1.0`)
	})
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v1/synthetic/quota" {
			_, _ = w.Write(body)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDetail.json")
	}))
	out, err := c.GetSyntheticQuota(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Quota.Status != "FUTURE" || !out.Quota.AutoRenew || !out.Quota.POC {
		t.Fatalf("quota = %+v", out.Quota)
	}
}

func TestQuotaSyntheticSecondReadFailure(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v1/synthetic/quota" {
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"private-code","message":"private-message"}`))
	}))
	out, err := c.GetSyntheticQuota(context.Background(), nil)
	var api *core.APIError
	if out != nil || !errors.Is(err, core.ErrNotFound) || !errors.As(err, &api) || api.Operation != "monitor.GetSyntheticQuota" || api.Code != "NotFound" || strings.Contains(err.Error(), "private") {
		t.Fatalf("output = %+v, error = %+v", out, err)
	}
}

func TestQuotaMissingPrices(t *testing.T) {
	for _, price := range []string{"", `,"price":null`} {
		t.Run(price, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`[{"id":"opaque/package","name":"free","amount":20` + price + `}]`))
			}))
			out, err := c.ListNotificationPackages(context.Background(), &ListNotificationPackagesInput{Kind: NotificationKindEmail})
			if err != nil {
				t.Fatal(err)
			}
			if out.Items[0].Price != nil {
				t.Fatalf("price = %v", out.Items[0].Price)
			}
		})
	}
}

func TestQuotaMalformedUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]json.RawMessage)
		raw    string
	}{
		{name: "null object", raw: `null`},
		{name: "array object", raw: `[]`},
		{name: "missing usage", change: func(m map[string]json.RawMessage) { delete(m, "usage") }},
		{name: "missing description", change: func(m map[string]json.RawMessage) { delete(m, "description") }},
		{name: "null usage", change: func(m map[string]json.RawMessage) { m["usage"] = json.RawMessage(`null`) }},
		{name: "fractional count", change: func(m map[string]json.RawMessage) {
			m["usage"] = json.RawMessage(`{"api":1.5,"location":0,"email":0,"sms":0,"slack":0,"webhook":0,"telegram":0,"teams":0}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.raw)
			if tc.raw == "" {
				body = quotaFixture(t, "GetSyntheticQuotaUsage", tc.change)
			}
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
			out, err := c.GetSyntheticQuotaUsage(context.Background(), nil)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("output=%v error=%v", out, err)
			}
		})
	}
}

func TestQuotaResponseSafety(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"oversize", `{"private":"` + strings.Repeat("x", 1<<20) + `"}`, 200},
		{"decode", `{"emailSeverity":"private`, 200},
		{"error", `{"code":"private-code","message":"private-message"}`, 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			captures := 0
			c := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}), func(transport.Capture) { captures++ }))
			out, err := c.GetQuotaUsage(context.Background(), nil)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || strings.Contains(err.Error(), "private") || strings.Contains(api.Code, "private") || captures != 0 {
				t.Fatalf("output = %+v, error = %+v, captures = %d", out, err, captures)
			}
			if tt.name == "oversize" && !errors.Is(err, transport.ErrBodyTooLarge) {
				t.Fatal("oversize lost transport sentinel")
			}
			if tt.name != "oversize" && api.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", api.StatusCode, tt.status)
			}
		})
	}
}

func TestQuotaRequiredCompositeFields(t *testing.T) {
	for _, key := range []string{"syntheticUsages", "emailUsages", "logUsages", "freeEmailQuota", "emailSeverity"} {
		t.Run(key, func(t *testing.T) {
			body := quotaFixture(t, "GetQuotaUsage", func(m map[string]json.RawMessage) { delete(m, key) })
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
			out, err := c.GetQuotaUsage(context.Background(), nil)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("output = %+v, error = %v", out, err)
			}
		})
	}
}

func TestQuotaMalformedCountsAndPackages(t *testing.T) {
	for _, value := range []string{`4.5`, `"4"`, `null`, `9223372036854775808`, `1e100000000`} {
		t.Run(value, func(t *testing.T) {
			body := quotaFixture(t, "GetQuotaUsage", func(m map[string]json.RawMessage) { m["emailUsages"] = json.RawMessage(`[{"amount":` + value + `}]`) })
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
			out, err := c.GetQuotaUsage(context.Background(), nil)
			if out != nil || err == nil {
				t.Fatalf("output = %+v, error = %v", out, err)
			}
		})
	}
	for _, body := range []string{`null`, `{}`, `[null]`, `[{"id":"pkg","name":"free","amount":20,"price":-1}]`, `[{"id":"pkg","name":"free","amount":20,"price":1e309}]`, `[{"id":"pkg","name":"free","price":0}]`} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		out, err := c.ListNotificationPackages(context.Background(), &ListNotificationPackagesInput{Kind: NotificationKindSMS})
		if out != nil || err == nil {
			t.Fatalf("body = %s, output = %+v, error = %v", body, out, err)
		}
	}
}

func TestQuotaSyntheticDetailMissingPrice(t *testing.T) {
	for _, value := range []string{"missing", "null"} {
		t.Run(value, func(t *testing.T) {
			body := quotaFixture(t, "GetSyntheticQuotaDetail", func(m map[string]json.RawMessage) {
				if value == "missing" {
					delete(m, "price")
				} else {
					m["price"] = json.RawMessage(`null`)
				}
			})
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/billing-api/v1/synthetic/quota" {
					testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
					return
				}
				_, _ = w.Write(body)
			}))
			out, err := c.GetSyntheticQuota(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Detail.Price != nil {
				t.Fatalf("price = %v", out.Detail.Price)
			}
		})
	}
}

func TestQuotaSuccessCredentialRedaction(t *testing.T) {
	captures := 0
	credential := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		body := quotaFixture(t, "GetQuotaUsage", func(m map[string]json.RawMessage) {
			encoded, err := json.Marshal("prefix " + credential + " suffix")
			if err != nil {
				t.Fatal(err)
			}
			m["emailSeverity"] = encoded
		})
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{
		HTTPClient: server.Client(), TokenSource: quotaTestTokenSource{}, Capture: func(transport.Capture) { captures++ },
	})))
	out, err := c.GetQuotaUsage(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if credential == "" || strings.Contains(out.Usage.EmailSeverity, credential) || captures != 0 {
		t.Fatalf("credential redaction or capture suppression failed")
	}
}

type quotaTestTokenSource struct{}

func (quotaTestTokenSource) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: "synthetic-quota-test-credential", ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func (quotaTestTokenSource) Invalidate(string) {}

func TestQuotaCancellationAndReadRetry(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected request") }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := c.GetQuotaUsage(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("output = %+v, error = %v", out, err)
	}
	calls := 0
	c = New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaUsage.json")
	})))
	if _, err := c.GetQuotaUsage(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func monitorReadFailures() map[string]func(*Client) error {
	return map[string]func(*Client) error{
		"quota": func(c *Client) error {
			out, err := c.GetQuotaUsage(context.Background(), nil)
			if out != nil {
				return errors.New("unexpected output")
			}
			return err
		},
		"alarm": func(c *Client) error {
			out, err := c.GetCheckAlarm(context.Background(), &GetCheckAlarmInput{CheckID: "chk-1"})
			if out != nil {
				return errors.New("unexpected output")
			}
			return err
		},
	}
}

func TestMonitorReadsWithholdRedirectCause(t *testing.T) {
	for name, read := range monitorReadFailures() {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://private.svc.cluster.local/", http.StatusFound)
			}))
			err := read(c)
			var api *core.APIError
			if !errors.As(err, &api) || strings.Contains(err.Error(), "cluster.local") || (api.Err != nil && strings.Contains(api.Err.Error(), "cluster.local")) {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

type monitorFailureHTTP func(*http.Request) (*http.Response, error)

func (f monitorFailureHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMonitorReadsKeepSafeSentinels(t *testing.T) {
	for name, read := range monitorReadFailures() {
		for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, net.ErrClosed, syscall.ECONNREFUSED, syscall.ECONNRESET} {
			t.Run(name+sentinel.Error(), func(t *testing.T) {
				tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: monitorFailureHTTP(func(*http.Request) (*http.Response, error) {
					return nil, fmt.Errorf("private.svc.cluster.local: %w", sentinel)
				})}})
				c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: "https://example.invalid/"}, tc))
				err := read(c)
				var api *core.APIError
				if !errors.Is(err, sentinel) || !errors.As(err, &api) || strings.Contains(err.Error(), "cluster.local") {
					t.Fatalf("sentinel lost or unsafe error: %v", err)
				}
			})
		}
	}
}

func TestMonitorReadsDecodeStatus(t *testing.T) {
	for name, read := range monitorReadFailures() {
		for _, status := range []int{200, 401, 403, 404, 429} {
			t.Run(fmt.Sprintf("%s/%d", name, status), func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(`{"bad":`)) }))
				err := read(c)
				var api *core.APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("status lost: %v", err)
				}
				for code, sentinel := range map[int]error{401: core.ErrAuth, 403: core.ErrPermission, 404: core.ErrNotFound, 429: core.ErrRateLimited} {
					if errors.Is(err, sentinel) != (status == code) {
						t.Fatalf("status %d sentinel %v: %v", status, sentinel, err)
					}
				}
			})
		}
	}
}

type monitorRefreshTokens struct {
	first, second string
	calls         int
}

func (s *monitorRefreshTokens) Token(context.Context) (transport.Token, error) {
	s.calls++
	value := s.first
	if s.calls > 1 {
		value = s.second
	}
	return transport.Token{AccessToken: value, ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func (*monitorRefreshTokens) Invalidate(string) {}

func monitorRefreshClient(t *testing.T, first, second, target string, handler http.Handler) *Client {
	t.Helper()
	source := &monitorRefreshTokens{first: first, second: second}
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == target {
			attempts++
			want := second
			if attempts == 1 {
				want = first
			}
			if r.Header.Get("Authorization") != "Bearer "+want {
				t.Error("unexpected attempt credential")
			}
			if attempts == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		if attempts != 2 || source.calls != 2 {
			t.Errorf("attempts=%d token calls=%d, want 2 each", attempts, source.calls)
		}
	})
	return New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
}

func assertMonitorCredentialsAbsent(t *testing.T, out any, err error, credentials ...string) {
	t.Helper()
	encoded, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	texts := []string{string(encoded), fmt.Sprintf("%+v", out)}
	var inspect func(error)
	inspect = func(cause error) {
		if cause == nil {
			return
		}
		texts = append(texts, cause.Error(), fmt.Sprintf("%+v", cause))
		var api *core.APIError
		if errors.As(cause, &api) {
			texts = append(texts, api.Operation, api.Code, api.Message)
		}
		if joined, ok := cause.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				inspect(child)
			}
		} else {
			inspect(errors.Unwrap(cause))
		}
	}
	inspect(err)
	for _, text := range texts {
		for _, credential := range credentials {
			if strings.Contains(text, credential) {
				t.Error("SDK output or error retained a sent credential")
			}
		}
	}
}

func TestQuotaRefreshCredentialRedaction(t *testing.T) {
	for _, pair := range [][2]string{
		{"synthetic-token-A", "synthetic-token-B"},
		{"synthetic-token-long", "synthetic-token"},
		{"synthetic-token", "synthetic-token-long"},
	} {
		for _, result := range []string{"success", "error", "decode"} {
			t.Run(pair[0]+"/"+pair[1]+"/"+result, func(t *testing.T) {
				body := `[{"id":"free","name":` + jsonString(pair[0]+" "+pair[1]) + `,"amount":20}]`
				status := http.StatusOK
				if result == "error" {
					status = http.StatusForbidden
					body = `{"message":` + jsonString(pair[0]+" "+pair[1]) + `}`
				}
				if result == "decode" {
					body = `[{"name":` + jsonString(pair[0]+" "+pair[1])
				}
				c := monitorRefreshClient(t, pair[0], pair[1], "/billing-api/v1/email/packages", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
				out, err := c.ListNotificationPackages(context.Background(), &ListNotificationPackagesInput{Kind: "Email"})
				if result == "success" {
					if err != nil || out == nil {
						t.Fatalf("output=%v error=%v", out, err)
					}
					if out.Items[0].Name != "[redacted] [redacted]" {
						t.Error("overlapping credentials were not completely redacted")
					}
				} else {
					var api *core.APIError
					if out != nil || !errors.As(err, &api) || api.StatusCode != status {
						t.Fatalf("output=%v error=%v", out, err)
					}
				}
				assertMonitorCredentialsAbsent(t, out, err, pair[0], pair[1])
			})
		}
	}
}

func TestMonitorReadsOversizedStatus(t *testing.T) {
	for name, read := range monitorReadFailures() {
		for _, status := range []int{200, 401, 403, 404, 429, 500} {
			t.Run(fmt.Sprintf("%s/%d", name, status), func(t *testing.T) {
				calls := 0
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"private-body-canary","extra":"` + strings.Repeat("x", quotaMaxBody) + `"}`))
				}))
				err := read(c)
				var api *core.APIError
				if !errors.As(err, &api) || api.StatusCode != status || calls != 1 {
					t.Fatalf("error=%v calls=%d", err, calls)
				}
				for code, sentinel := range map[int]error{401: core.ErrAuth, 403: core.ErrPermission, 404: core.ErrNotFound, 429: core.ErrRateLimited} {
					if errors.Is(err, sentinel) != (status == code) {
						t.Errorf("status %d lost sentinel %v", status, sentinel)
					}
				}
				wantCode := core.ResolvedCode(status, "")
				wantMessage := "quota response withheld"
				if name == "alarm" {
					wantMessage = "check alarm request failed; response withheld"
				}
				if status == 200 {
					wantCode, wantMessage = "ResponseTooLarge", "quota response exceeded size limit"
					if name == "alarm" {
						wantCode, wantMessage = "ResponseLimitExceeded", "check alarm response exceeds safety limit"
					}
					if !errors.Is(err, transport.ErrBodyTooLarge) {
						t.Error("oversized success lost body limit sentinel")
					}
				}
				if api.Code != wantCode || api.Message != wantMessage {
					t.Fatalf("code=%q message=%q", api.Code, api.Message)
				}
				assertMonitorCredentialsAbsent(t, nil, err, "private-body-canary")
			})
		}
	}
}

func TestQuotaUsageCountsCompatibility(t *testing.T) {
	// Existing callers assign a value and take the address of Counts.
	out := QuotaUsageDetail{Counts: UsageCounts{API: 1}}
	counts := &out.Counts
	if counts.API != 1 {
		t.Fatal("Counts lost value semantics")
	}
	for _, tc := range []struct {
		usage   QuotaUsageDetail
		present bool
	}{{QuotaUsageDetail{Amount: 4}, false}, {out, true}} {
		data, err := json.Marshal(tc.usage)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["Counts"]; ok != tc.present {
			t.Fatalf("Counts presence: %s", data)
		}
	}
}

func TestNotificationUsageFixtureOmitsCounts(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/billing-api/v1/email/quota/usages" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetNotificationQuotaUsageEmail.json")
	}))
	out, err := c.GetNotificationQuotaUsage(context.Background(), &GetNotificationQuotaUsageInput{Kind: "Email"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage.Counts != (UsageCounts{}) {
		t.Fatalf("notification Counts=%+v", out.Usage.Counts)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Counts") {
		t.Fatalf("Counts serialized: %s", data)
	}
}
