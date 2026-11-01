package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

// These three fixtures are sanitized raw billing-api 200 responses supplied
// by the manager. Catalog IDs are preserved; the two ID spaces differ.
func catalogFixture(t *testing.T, classes, packages, settings string, after func()) *Client {
	t.Helper()
	bodies := []string{classes, packages, settings}
	paths := []string{"/billing-api/v2/synthetic/quota-class", "/billing-api/v1/synthetic/packages", "/billing-api/v1/settings"}
	// Disabled class responses are synthetic empty-array mocks, not live evidence.
	for _, id := range []string{createClassID, "70b57f46-edaf-4f6f-9293-acf816a5b81d", "d8c1d829-205b-4ce4-9c1c-32756f748d25"} {
		paths = append(paths, "/billing-api/v2/synthetic/quota-class/"+id+"/packages")
		body := "[]"
		if id == createClassID {
			body = testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json")
		}
		bodies = append(bodies, body)
	}
	calls := 0
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls >= len(paths) || r.URL.Path != paths[calls] || r.Method != "GET" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(bodies[calls]))
		calls++
		if after != nil {
			after()
		}
	}))
}

func catalogBodies(t *testing.T) (string, string, string) {
	t.Helper()
	return testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticQuotaClasses.json"), testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticPackages.json"), testutil.FixtureBody(t, "../testdata/monitor/GetQuotaSettings.json")
}

func TestSyntheticCatalogRawFixtures(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	out, err := catalogFixture(t, classes, packages, settings, nil).ListSyntheticPackages(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 3 || out.Items[0].Name != "Basic" || out.Items[1].Priority != 2 || len(out.Items[1].Options) != 0 || out.Items[1].Description != "Coming soon." || len(out.Packages) != 1 {
		t.Fatalf("catalog = %+v", out)
	}
	option, pkg := out.Items[0].Options[0], out.Packages[0]
	if option.PackageID == pkg.ID || option.Price != nil || option.AutoRenewEligible == nil || *option.AutoRenewEligible || pkg.AutoRenewEligible != nil || pkg.Price == nil || *pkg.Price != 0 || pkg.TierID != 24 || pkg.API != 10 || pkg.Location != 1 {
		t.Fatalf("option=%+v package=%+v", option, pkg)
	}
	if !reflect.DeepEqual(option.MonthPeriods, []int{1}) || !reflect.DeepEqual(pkg.MonthPeriods, []int{1}) || !reflect.DeepEqual(out.MonthPeriods, []int{1, 3, 6, 12, 24, 36}) || out.MaxMonthPeriodForFreeQuota != 1 || out.PaymentMethod != "PRE-PAID" {
		t.Fatalf("periods = %+v", out)
	}
}

func TestSyntheticCatalogInvalidStructure(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	for _, tc := range []struct{ name, classes, packages, settings string }{
		{"null-classes", "null", packages, settings},
		{"missing-config", strings.ReplaceAll(classes, `"config": {`, `"other": {`), packages, settings},
		{"null-config", strings.ReplaceAll(classes, `"locations": [`, `"locations": null, "unused": [`), packages, settings},
		{"fractional-bound", strings.ReplaceAll(classes, `"minApi": 10`, `"minApi": 10.5`), packages, settings},
		{"negative-location", strings.ReplaceAll(classes, `"amount": 1`, `"amount": -1`), packages, settings},
		{"bad-bounds", strings.ReplaceAll(classes, `"minApi": 10`, `"minApi": 11`), packages, settings},
		{"zero-step", strings.ReplaceAll(classes, `"step": 1`, `"step": 0`), packages, settings},
		{"empty-option-id", strings.ReplaceAll(classes, `"packageId": "32b3743d-94b8-49ab-984d-bd7ec99b46f6"`, `"packageId": ""`), packages, settings},
		{"negative-price", classes, strings.ReplaceAll(packages, `0.0`, `-1`), settings},
		{"nonfinite-price", classes, strings.ReplaceAll(packages, `0.0`, `1e999`), settings},
		{"negative-count", classes, strings.ReplaceAll(packages, `"api": 10`, `"api": -1`), settings},
		{"fractional-count", classes, strings.ReplaceAll(packages, `"api": 10`, `"api": 1.5`), settings},
		{"duplicate-period", classes, packages, strings.ReplaceAll(settings, `3,`, `1,`)},
		{"zero-period", classes, packages, strings.ReplaceAll(settings, `3,`, `0,`)},
		{"zero-free-max", classes, packages, strings.ReplaceAll(settings, `"maxMonthPeriodForFreeQuota": 1`, `"maxMonthPeriodForFreeQuota": 0`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := catalogFixture(t, tc.classes, tc.packages, tc.settings, nil).ListSyntheticPackages(context.Background(), nil)
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.Operation != "monitor.ListSyntheticPackages" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestSyntheticCatalogSeparateIdentities(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	var cs []map[string]any
	if err := json.Unmarshal([]byte(classes), &cs); err != nil {
		t.Fatal(err)
	}
	basic := cs[2]
	config := basic["config"].(map[string]any)
	option := config["locations"].([]any)[0].(map[string]any)
	for _, mode := range []string{"duplicate-class", "duplicate-package", "duplicate-option", "shared-option", "unknown-status", "null-price"} {
		t.Run(mode, func(t *testing.T) {
			cbody, pbody := classes, packages
			switch mode {
			case "duplicate-class":
				b, _ := json.Marshal(append(cs, basic))
				cbody = string(b)
			case "duplicate-package":
				pbody = strings.TrimSuffix(strings.TrimSpace(packages), "]") + "," + strings.TrimSpace(packages)[1:]
			case "duplicate-option", "shared-option":
				copied := make(map[string]any)
				for k, v := range option {
					copied[k] = v
				}
				if mode == "shared-option" {
					copied["amount"] = 2
				}
				config["locations"] = []any{option, copied}
				b, _ := json.Marshal(cs)
				cbody = string(b)
				config["locations"] = []any{option}
			case "unknown-status":
				cbody = strings.ReplaceAll(classes, `"ACTIVE"`, `"FUTURE"`)
			case "null-price":
				pbody = strings.ReplaceAll(packages, `0.0`, `null`)
			}
			out, err := catalogFixture(t, cbody, pbody, settings, nil).ListSyntheticPackages(context.Background(), &ListSyntheticPackagesInput{})
			if strings.HasPrefix(mode, "duplicate") {
				if out != nil || err == nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "shared-option" && len(out.Items[0].Options) != 2 {
				t.Fatal("shared ID across locations lost")
			}
			if mode == "unknown-status" && (out.Items[0].Status != "FUTURE" || out.Items[0].Options[0].AutoRenewEligible != nil) {
				t.Fatal("unknown status inferred eligibility")
			}
			if mode == "null-price" && (out.Packages[0].Price != nil || len(out.Packages[0].MonthPeriods) != 0) {
				t.Fatal("unknown price inferred free periods")
			}
		})
	}
}

func TestSyntheticCatalogCancellation(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := catalogFixture(t, classes, packages, settings, cancel)
	out, err := c.ListSyntheticPackages(ctx, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestSyntheticCatalogOrderingAndPrices(t *testing.T) {
	classes, _, settings := catalogBodies(t)
	packages := `[{"id":"z","name":"same","tierId":1,"api":10,"location":1,"price":4},{"id":"a","name":"same","tierId":2,"api":10,"location":1,"price":null}]`
	out, err := catalogFixture(t, classes, packages, settings, nil).ListSyntheticPackages(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Packages[0].ID != "a" || len(out.Packages[0].MonthPeriods) != 0 || out.Packages[1].ID != "z" || len(out.Packages[1].MonthPeriods) != 6 || out.Packages[1].AutoRenewEligible != nil || out.Items[0].Options[0].Price != nil {
		t.Fatalf("catalog=%+v", out)
	}
}

func TestSyntheticNullableEmail(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v1/synthetic/quota" {
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
			return
		}
		body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
		_, _ = w.Write([]byte(strings.ReplaceAll(body, `"email": 0`, `"email": null`)))
	}))
	out, err := c.GetSyntheticQuota(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Detail.ChannelLimits.Email != nil || out.Detail.Email != 0 {
		t.Fatalf("detail=%+v", out.Detail)
	}
}

func TestSyntheticCatalogReadLimitsAndRetries(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	for _, mode := range []string{"oversize", "near-limit", "retry"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			bodies := map[string]string{"/billing-api/v2/synthetic/quota-class": classes, "/billing-api/v1/synthetic/packages": packages, "/billing-api/v1/settings": settings}
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "retry" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"message":"withheld peer"}`))
					return
				}
				body := bodies[r.URL.Path]
				if strings.HasPrefix(r.URL.Path, "/billing-api/v2/synthetic/quota-class/") {
					body = testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json")
				}
				if body == "" {
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if mode == "oversize" {
					body += strings.Repeat(" ", quotaMaxBody)
				} else {
					body += strings.Repeat(" ", quotaMaxBody-len(body))
				}
				_, _ = w.Write([]byte(body))
			})))
			out, err := c.ListSyntheticPackages(context.Background(), nil)
			switch mode {
			case "retry":
				if out != nil || err == nil || calls != 4 {
					t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
				}
			case "oversize":
				var api *core.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "ResponseTooLarge" || calls != 1 {
					t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
				}
			case "near-limit":
				if err != nil || out == nil || calls != 6 {
					t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
				}
			}
		})
	}
}

func TestSyntheticCoverageCredentialSafety(t *testing.T) {
	for _, kind := range []string{"catalog", "status"} {
		t.Run(kind, func(t *testing.T) {
			classes, packages, settings := catalogBodies(t)
			const secret = "synthetic-quota-test-credential"
			calls, captures := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("missing synthetic credential")
				}
				var body string
				switch r.URL.Path {
				case "/billing-api/v2/synthetic/quota-class":
					body = strings.ReplaceAll(classes, "Coming soon.", secret)
				case "/billing-api/v1/synthetic/packages":
					body = strings.ReplaceAll(packages, "free10", secret)
				case "/billing-api/v2/synthetic/quota-class/" + createClassID + "/packages", "/billing-api/v2/synthetic/quota-class/70b57f46-edaf-4f6f-9293-acf816a5b81d/packages", "/billing-api/v2/synthetic/quota-class/d8c1d829-205b-4ce4-9c1c-32756f748d25/packages":
					body = testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json")
				case "/billing-api/v1/settings":
					body = strings.ReplaceAll(settings, "PRE-PAID", secret)
				case "/billing-api/v1/synthetic/quota":
					body = strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuota.json"), "synthetic test", secret)
				case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
					body = strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json"), "Basic", secret)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: quotaTestTokenSource{}, Capture: func(transport.Capture) { captures++ }})))
			var out any
			var err error
			if kind == "catalog" {
				out, err = c.ListSyntheticPackages(context.Background(), nil)
			} else {
				out, err = c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: "synthetic"})
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), secret) || captures != 0 || calls < 2 || !strings.Contains(string(body), "[redacted]") {
				t.Fatal("unsafe output or capture")
			}
		})
	}
}

type quotaBudgetAttempt struct {
	path         string
	status, read int
	closed       bool
}

type quotaBudgetBody struct {
	io.ReadCloser
	attempt *quotaBudgetAttempt
	cancel  context.CancelFunc
}

func (b *quotaBudgetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.attempt.read += n
	return n, err
}

func (b *quotaBudgetBody) Close() error {
	b.attempt.closed = true
	err := b.ReadCloser.Close()
	if b.cancel != nil {
		b.cancel()
	}
	return err
}

type quotaBudgetTransport struct {
	base     http.RoundTripper
	attempts []*quotaBudgetAttempt
	cancel   context.CancelFunc
}

func (r *quotaBudgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err != nil {
		return response, err
	}
	attempt := &quotaBudgetAttempt{path: req.URL.Path, status: response.StatusCode}
	r.attempts = append(r.attempts, attempt)
	response.Body = &quotaBudgetBody{ReadCloser: response.Body, attempt: attempt, cancel: r.cancel}
	return response, nil
}

type quotaBudgetTokens struct {
	quotaTestTokenSource
	refreshes int
}

func (s *quotaBudgetTokens) Invalidate(string) { s.refreshes++ }

// These are synthetic retry/refresh and overflow probes over real loopback
// HTTP. Retained body bytes exclude decoded objects and allocator overhead.
func TestQuotaCompositeRetainedBodyBudgetWithRetries(t *testing.T) {
	t.Run("retained-counter", func(t *testing.T) {
		calls := 0
		body := quotaBudgetPaddedBody(t, `{"component":"synthetic mock"}`)
		c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls%4 != 0 {
				w.WriteHeader(503)
			}
			_, _ = io.WriteString(w, body)
		})))
		var credentials monitorReadCredentials
		reader := quotaCompositeReader{client: c, credentials: &credentials}
		for i := 1; i <= 8; i++ {
			raw, err := reader.read(context.Background(), "monitor.ListSyntheticPackages", "v1", "component")
			if err != nil || len(raw) != quotaMaxBody || reader.retained != i*quotaMaxBody || reader.retained > 8<<20 || calls != i*4 {
				t.Fatalf("component=%d retained=%d calls=%d err=%v", i, reader.retained, calls, err)
			}
		}
		raw, err := reader.read(context.Background(), "monitor.ListSyntheticPackages", "v1", "component")
		if raw != nil || !errors.Is(err, transport.ErrBodyTooLarge) || reader.retained != 8<<20 || !reader.exhausted || calls != 36 {
			t.Fatalf("overflow retained=%d calls=%d err=%v", reader.retained, calls, err)
		}
		raw, err = reader.read(context.Background(), "monitor.ListSyntheticPackages", "v1", "component")
		var api *core.APIError
		if raw != nil || !errors.As(err, &api) || api.Code != "RequestFailed" || reader.retained != 8<<20 || calls != 36 {
			t.Fatalf("exhausted retained=%d calls=%d err=%v", reader.retained, calls, err)
		}
	})
	const retries = 3
	const budget = 8 << 20
	for _, operation := range []string{"catalog", "status"} {
		for _, mode := range []string{"retry", "refresh", "oversize", "cancel", "overflow", "redirect"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				fixtures := map[string]string{
					"/billing-api/v2/synthetic/quota-class":              "ListSyntheticQuotaClasses",
					"/billing-api/v1/synthetic/packages":                 "ListSyntheticPackages",
					"/billing-api/v1/settings":                           "GetQuotaSettings",
					"/billing-api/v1/synthetic/quota":                    "GetSyntheticQuota",
					"/billing-api/v2/synthetic/quota-syn-1/quota-detail": "GetSyntheticQuotaDetailNullable",
					"/billing-api/v1/email/quota":                        "GetNotificationQuotaEmail",
					"/billing-api/v1/email/packages":                     "ListNotificationPackagesEmail",
					"/billing-api/v1/sms/quota":                          "GetNotificationQuotaSMS",
					"/billing-api/v1/sms/packages":                       "ListNotificationPackagesSMS",
				}
				// These class-package responses are synthetic for disabled classes.
				for _, id := range []string{createClassID, "70b57f46-edaf-4f6f-9293-acf816a5b81d", "d8c1d829-205b-4ce4-9c1c-32756f748d25"} {
					fixtures["/billing-api/v2/synthetic/quota-class/"+id+"/packages"] = "ListSyntheticClassPackages"
				}
				bodies := make(map[string]string, len(fixtures))
				for path, name := range fixtures {
					bodies[path] = quotaBudgetPaddedBody(t, testutil.FixtureBody(t, "../testdata/monitor/"+name+".json"))
				}
				first := "/billing-api/v2/synthetic/quota-class"
				if operation == "status" {
					first = "/billing-api/v1/synthetic/quota"
				}
				retryBody := quotaBudgetPaddedBody(t, `{"message":"synthetic retry"}`)
				var mu sync.Mutex
				calls := map[string]int{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					calls[r.URL.Path]++
					attempt := calls[r.URL.Path]
					mu.Unlock()
					body, ok := bodies[r.URL.Path]
					if !ok {
						w.WriteHeader(400)
						return
					}
					if r.Method != "GET" {
						t.Error("unexpected mutation")
					}
					if mode == "redirect" && r.URL.Path == first {
						w.Header().Set("Location", "/escaped-attempt")
						w.WriteHeader(302)
						_, _ = io.WriteString(w, retryBody)
						return
					}
					if mode == "oversize" && r.URL.Path == first {
						_, _ = io.WriteString(w, body+strings.Repeat(" ", 32))
						return
					}
					if mode == "overflow" && (operation == "catalog" || r.URL.Path != "/billing-api/v2/synthetic/quota-syn-1/quota-detail") {
						// Replace only the synthetic padding, leaving fixture identities intact.
						start := strings.Index(body, `"padding":"`) + len(`"padding":"`)
						end := start + strings.IndexByte(body[start:], '"')
						body = body[:start] + strings.Repeat("<", end-start) + body[end:]
					}
					switch {
					case mode == "refresh" && attempt == retries+1:
						w.WriteHeader(401)
						body = retryBody
					case (mode == "retry" || mode == "refresh" || mode == "cancel") && attempt%(retries+1) != 0:
						w.WriteHeader(503)
						body = retryBody
					}
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				counted := &quotaBudgetTransport{base: server.Client().Transport}
				if mode == "cancel" {
					counted.cancel = cancel
				}
				tokens := &quotaBudgetTokens{}
				httpClient := server.Client()
				httpClient.Transport = counted
				c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: httpClient, TokenSource: tokens, RetryCount: retries, RetryInterval: time.Millisecond})))
				var catalog *ListSyntheticPackagesOutput
				var status *GetQuotaStatusOutput
				var err error
				if operation == "catalog" {
					catalog, err = c.ListSyntheticPackages(ctx, nil)
				} else {
					status, err = c.GetQuotaStatus(ctx, nil)
				}
				total, returned := 0, 0
				attempts := map[string]int{}
				for _, a := range counted.attempts {
					if a.read > quotaMaxBody+1 || !a.closed {
						t.Fatalf("attempt=%+v", a)
					}
					attempts[a.path]++
					total += a.read
					if a.status == 200 && a.read <= quotaMaxBody {
						returned += a.read
					}
				}
				for path, count := range attempts {
					if count > 2*(retries+1) {
						t.Fatalf("%s attempts=%d", path, count)
					}
				}
				if total > (quotaMaxBody+1)*len(counted.attempts) {
					t.Fatalf("total read=%d", total)
				}
				if mode == "retry" || mode == "refresh" {
					if err != nil {
						t.Fatal(err)
					}
					components := 6
					if operation == "status" {
						components = 6
						if !status.Complete {
							t.Fatal("incomplete success")
						}
					}
					wantAttempts := retries + 1
					if mode == "refresh" {
						wantAttempts *= 2
						if tokens.refreshes != components {
							t.Fatalf("refreshes=%d", tokens.refreshes)
						}
					}
					if len(attempts) != components || len(counted.attempts) != components*wantAttempts || total != components*wantAttempts*quotaMaxBody || total <= budget || returned != components*quotaMaxBody || returned > budget {
						t.Fatalf("attempts=%v total=%d returned=%d", attempts, total, returned)
					}
					for _, a := range counted.attempts {
						if a.read != quotaMaxBody {
							t.Fatalf("short attempt=%+v", a)
						}
					}
					return
				}
				if err == nil {
					t.Fatal("expected refusal")
				}
				if operation == "catalog" {
					if catalog != nil {
						t.Fatal("catalog returned partial output")
					}
				} else if status == nil || status.Complete || !errors.Is(err, ErrQuotaStatusIncomplete) {
					t.Fatalf("status=%+v err=%v", status, err)
				}
				switch mode {
				case "oversize":
					if counted.attempts[0].read != quotaMaxBody+1 || !errors.Is(err, transport.ErrBodyTooLarge) {
						t.Fatalf("oversize read=%d err=%v", counted.attempts[0].read, err)
					}
				case "cancel":
					if len(counted.attempts) != 1 || !errors.Is(err, context.Canceled) {
						t.Fatalf("attempts=%d err=%v", len(counted.attempts), err)
					}
					if status != nil && (status.Items[1].Error.Code != "RequestFailed" || status.Items[2].Error.Code != "RequestFailed") {
						t.Fatal("unattempted cancellation rows not marked")
					}
				case "overflow":
					var api *core.APIError
					wantAttempts := 2
					if operation == "status" {
						wantAttempts = 3
					}
					if !errors.As(err, &api) || api.Code != "ResponseTooLarge" || len(counted.attempts) != wantAttempts {
						t.Fatalf("attempts=%d err=%v", len(counted.attempts), err)
					}
					if status != nil && (status.Items[0].Status != "ACTIVE" || status.Items[0].Quota == nil || status.Items[1].Error.Code != "ResponseTooLarge" || status.Items[2].Error.Code != "RequestFailed") {
						t.Fatalf("overflow status=%+v", status)
					}
				case "redirect":
					if attempts["/escaped-attempt"] != 0 {
						t.Fatal("redirect escaped attempt accounting")
					}
				}
			})
		}
	}
}

func quotaBudgetPaddedBody(t *testing.T, body string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	const field = `"padding":"",`
	padding := quotaMaxBody - len(canonical) - len(field)
	if padding < 0 {
		t.Fatal("fixture exceeds cap")
	}
	return strings.Replace(string(canonical), "{", `{"padding":"`+strings.Repeat("a", padding)+`",`, 1)
}
