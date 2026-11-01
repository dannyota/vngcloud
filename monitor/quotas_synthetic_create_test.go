package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestSyntheticCreateValidation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*CreateSyntheticQuotaInput{nil, {}, {ClassID: "../bad", PackageID: "opaque", Quantity: 10}} {
		out, err := c.CreateSyntheticQuota(context.Background(), in)
		if out == nil || !errors.Is(err, core.ErrInvalidInput) || out.Sent != "no" || out.Stage != "preflight" || out.Created || out.SentAt != nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}

const createClassID = "e574cf4a-460b-48d8-a794-80367289a6c8"
const createPackageID = "32b3743d-94b8-49ab-984d-bd7ec99b46f6"

func TestSyntheticCreateClassPackages(t *testing.T) {
	classes, packages, settings := catalogBodies(t)
	out, err := catalogFixture(t, classes, packages, settings, nil).ListSyntheticPackages(context.Background(), nil)
	if err != nil || len(out.Items[0].Packages) != 1 || out.Items[0].Packages[0].API != 1 || out.Items[0].Packages[0].Price == nil || *out.Items[0].Packages[0].Price != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

type syntheticCreateServer struct {
	sends, reads, quotes               atomic.Int32
	send                               http.HandlerFunc
	quota                              func(int32) string
	classes, packages, settings, price string
}

func (s *syntheticCreateServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v1/synthetic/quota":
			n := s.reads.Add(1)
			if s.quota != nil {
				if body := s.quota(n); body != "" {
					_, _ = io.WriteString(w, body)
					return
				}
			}
			w.WriteHeader(404)
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDeleted.json")
		case "/billing-api/v2/synthetic/quota-class":
			if s.classes != "" {
				_, _ = io.WriteString(w, s.classes)
			} else {
				testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticQuotaClasses.json")
			}
		case "/billing-api/v2/synthetic/quota-class/" + createClassID + "/packages":
			if s.packages != "" {
				_, _ = io.WriteString(w, s.packages)
			} else {
				testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticClassPackages.json")
			}
		case "/billing-api/v1/settings":
			if s.settings != "" {
				_, _ = io.WriteString(w, s.settings)
			} else {
				testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaSettings.json")
			}
		case "/billing-api/v2/synthetic/prices/created-price", "/billing-api/v2/synthetic/quota":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			expected := map[string]any{"redirectUrl": "", "packageId": createPackageID, "quantity": float64(10), "buyWith": map[string]any{}}
			if r.URL.Path == "/billing-api/v2/synthetic/quota" {
				expected["redirectUrl"] = syntheticQuotaRedirect
				expected["monthPeriod"] = float64(1)
				expected["pay"] = true
			}
			if !reflect.DeepEqual(body, expected) {
				t.Errorf("body=%v want=%v", body, expected)
			}
			if r.URL.Path == "/billing-api/v2/synthetic/quota" {
				s.sends.Add(1)
				s.send(w, r)
			} else {
				s.quotes.Add(1)
				if s.price != "" {
					_, _ = io.WriteString(w, s.price)
				} else {
					testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateSyntheticQuota.json")
				}
			}
		case "/billing-api/v2/synthetic/quota-syn-2/quota-detail":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaCreatedDetail.json")
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}
}
func syntheticCreateClient(t *testing.T, s *syntheticCreateServer) *Client {
	t.Helper()
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	now := time.Date(2026, 11, 1, 6, 40, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return c
}
func syntheticCreateInput() *CreateSyntheticQuotaInput {
	return &CreateSyntheticQuotaInput{ClassID: createClassID, PackageID: createPackageID, Quantity: 10, NoWait: true}
}

func TestSyntheticCreateSingleSend(t *testing.T) {
	for _, status := range []int{200, 201, 204, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504, -1} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			// Create's empty success body is a mock from recorded status metadata.
			s := &syntheticCreateServer{send: func(w http.ResponseWriter, r *http.Request) {
				if status < 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(status)
				if status != 200 && status != 204 {
					_, _ = io.WriteString(w, `{"message":"peer-canary"}`)
				}
			}}
			c := syntheticCreateClient(t, s)
			out, err := c.CreateSyntheticQuota(context.Background(), syntheticCreateInput())
			if s.sends.Load() != 1 || out == nil || out.Created || out.SentAt == nil || s.quotes.Load() != 1 {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, s.sends.Load())
			}
			switch {
			case status == 200:
				if err != nil || out.Stage != "accepted" || out.Sent != "yes" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			case status >= 400 && status < 500:
				var api *core.APIError
				if !errors.As(err, &api) || api.StatusCode != status || out.Stage != "refused" || out.Sent != "yes" || c.quotaAttempts["synthetic"] != nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			default:
				if !errors.Is(err, dns.ErrNotSettled) || out.Stage != "unconfirmed" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				again, e := c.CreateSyntheticQuota(context.Background(), syntheticCreateInput())
				if !errors.Is(e, dns.ErrNotSettled) || again.Sent != out.Sent || s.sends.Load() != 1 {
					t.Fatalf("again=%+v err=%v sends=%d", again, e, s.sends.Load())
				}
			}
		})
	}
}
func TestSyntheticCreateConfirmationAndReconciliation(t *testing.T) {
	for _, mode := range []string{"confirmed", "old-start", "wrong-package", "creating", "changed-intent"} {
		t.Run(mode, func(t *testing.T) {
			s := &syntheticCreateServer{send: func(http.ResponseWriter, *http.Request) {}}
			body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
			switch mode {
			case "old-start":
				body = strings.ReplaceAll(body, `2026-11-01T06:54:54.000+00:00`, `2026-11-01T06:00:00Z`)
			case "wrong-package":
				body = strings.ReplaceAll(body, createPackageID, "other")
			case "creating":
				body = strings.ReplaceAll(body, `"ACTIVE"`, `"CREATING"`)
			}
			s.quota = func(int32) string {
				if s.sends.Load() > 0 {
					return body
				}
				return ""
			}
			c := syntheticCreateClient(t, s)
			in := syntheticCreateInput()
			out, err := c.CreateSyntheticQuota(context.Background(), in)
			if err != nil || out.Created {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "changed-intent" {
				in.Quantity = 11
			}
			again, err := c.CreateSyntheticQuota(context.Background(), in)
			switch mode {
			case "confirmed":
				if err != nil || !again.Created || again.Quota.ID != "quota-syn-2" || !again.SentAt.Equal(*out.SentAt) {
					t.Fatalf("again=%+v err=%v", again, err)
				}
			case "changed-intent":
				if !errors.Is(err, core.ErrInvalidInput) || again.Created {
					t.Fatalf("again=%+v err=%v", again, err)
				}
			default:
				if !errors.Is(err, dns.ErrNotSettled) || again.Created {
					t.Fatalf("again=%+v err=%v", again, err)
				}
			}
			if s.sends.Load() != 1 {
				t.Fatal("resent create")
			}
		})
	}
}
func TestSyntheticCreatePreparationRefusals(t *testing.T) {
	for _, mode := range []string{"existing", "quantity", "missing-price", "unpriced", "paid", "price-guard", "nan", "changed"} {
		t.Run(mode, func(t *testing.T) {
			s := &syntheticCreateServer{send: func(http.ResponseWriter, *http.Request) { t.Error("unexpected mutation") }}
			in := syntheticCreateInput()
			switch mode {
			case "existing":
				s.quota = func(int32) string {
					return testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
				}
			case "quantity":
				in.Quantity = 11
			case "missing-price":
				s.packages = strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json"), `0.0`, `null`)
			case "unpriced", "paid":
				s.packages = strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json"), `0.0`, `1`)
				if mode == "paid" {
					s.price = `{"optimumPrice":10}`
					in.MaxPrice = 10
				}
			case "price-guard":
				s.price = `{"optimumPrice":1}`
			case "nan":
				in.MaxPrice = math.NaN()
			case "changed":
				s.quota = func(n int32) string {
					if n > 1 {
						return testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
					}
					return ""
				}
			}
			out, err := syntheticCreateClient(t, s).CreateSyntheticQuota(context.Background(), in)
			if err == nil || out.Sent != "no" || out.Created || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "unpriced" && !errors.Is(err, ErrUnpriced) {
				t.Fatal(err)
			}
			if mode == "price-guard" && !errors.Is(err, ErrPriceAboveMax) {
				t.Fatal(err)
			}
			if mode == "changed" && !errors.Is(err, ErrQuotaChanged) {
				t.Fatal(err)
			}
		})
	}
}

func TestSyntheticCreateQuoteRawFixture(t *testing.T) {
	s := &syntheticCreateServer{send: func(http.ResponseWriter, *http.Request) { t.Error("quote ordered") }}
	in := syntheticCreateInput()
	in.MaxPrice = math.NaN()
	out, err := syntheticCreateClient(t, s).QuoteCreateSyntheticQuota(context.Background(), in)
	if err != nil || out.PackageID != createPackageID || out.Quantity != 10 || out.MonthPeriod != 1 || out.ResourceID != "" || out.ExpectedEndTime != nil || out.GuardPrice != 0 || out.Currency != "VND" || s.sends.Load() != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestSyntheticClassPackageValidation(t *testing.T) {
	raw := testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json")
	for _, mode := range []string{"null", "object", "duplicate", "fractional-api", "zero-api", "negative-tier", "null-price", "missing-price", "negative-price", "wrong-price", "empty-id"} {
		t.Run(mode, func(t *testing.T) {
			body := raw
			switch mode {
			case "null":
				body = "null"
			case "object":
				body = "{}"
			case "duplicate":
				body = strings.TrimSuffix(strings.TrimSpace(raw), "]") + "," + strings.TrimSpace(raw)[1:]
			case "fractional-api":
				body = strings.ReplaceAll(raw, `"api": 1`, `"api": 1.5`)
			case "zero-api":
				body = strings.ReplaceAll(raw, `"api": 1`, `"api": 0`)
			case "negative-tier":
				body = strings.ReplaceAll(raw, `"tierId": 1001`, `"tierId": -1`)
			case "null-price":
				body = strings.ReplaceAll(raw, `"price": 0.0`, `"price": null`)
			case "missing-price":
				body = strings.ReplaceAll(raw, `"price": 0.0,`, ``)
			case "negative-price":
				body = strings.ReplaceAll(raw, `"price": 0.0`, `"price": -1`)
			case "wrong-price":
				body = strings.ReplaceAll(raw, `"price": 0.0`, `"price": "0"`)
			case "empty-id":
				body = strings.ReplaceAll(raw, createPackageID, "")
			}
			out, err := decodeSyntheticClassPackages("monitor.ListSyntheticPackages", []byte(body))
			if mode == "null-price" || mode == "missing-price" {
				if err != nil || out[0].Price != nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			var api *core.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestSyntheticClassFanoutBound(t *testing.T) {
	for _, count := range []int{64, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			classes := make([]map[string]any, count)
			for i := range classes {
				classes[i] = map[string]any{"id": fmt.Sprintf("class-%02d", i), "name": "mock disabled class", "status": "DISABLED", "description": "synthetic mock", "priority": i}
			}
			raw, _ := json.Marshal(classes)
			var packageReads atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v2/synthetic/quota-class":
					_, _ = w.Write(raw)
				case "/billing-api/v1/synthetic/packages":
					_, _ = io.WriteString(w, "[]")
				case "/billing-api/v1/settings":
					testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaSettings.json")
				default:
					packageReads.Add(1)
					_, _ = io.WriteString(w, "[]")
				}
			}))
			out, err := c.ListSyntheticPackages(context.Background(), nil)
			if count == 64 {
				if err != nil || len(out.Items) != 64 || packageReads.Load() != 64 {
					t.Fatalf("out=%+v err=%v reads=%d", out, err, packageReads.Load())
				}
			} else if out != nil || err == nil || packageReads.Load() != 0 {
				t.Fatalf("out=%+v err=%v reads=%d", out, err, packageReads.Load())
			}
		})
	}
}

func TestSyntheticCreateTolerantResponse(t *testing.T) {
	for _, body := range []string{"", "null", "{}", `{"orderId":null,"paymentUrl":null,"amount":{}}`, `{"orderId":"order-1","paymentUrl":"` + syntheticQuotaRedirect + `"}`, `{"orderId":4}`, `{"paymentUrl":false}`, `{"paymentUrl":"https://peer-canary.invalid/pay"}`, "[]", "true", "{", strings.Repeat("x", quotaMaxBody+1)} {
		name := body
		if len(name) > 100 {
			name = "oversize"
		}
		t.Run(name, func(t *testing.T) {
			s := &syntheticCreateServer{send: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }}
			out, err := syntheticCreateClient(t, s).CreateSyntheticQuota(context.Background(), syntheticCreateInput())
			valid := body == "" || body == "null" || body == "{}" || strings.Contains(body, `"orderId":null`) || strings.Contains(body, `"order-1"`)
			if (err == nil) != valid || out.Created || out.Quota != nil || s.sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if err != nil && !errors.Is(err, dns.ErrNotSettled) {
				t.Fatal(err)
			}
			if strings.Contains(fmt.Sprint(out), "peer-canary") {
				t.Fatal("payment URL leaked")
			}
		})
	}
}

func TestSyntheticClassCatalogPeerFailures(t *testing.T) {
	for _, mode := range []string{"unsafe-id", "disabled-failure"} {
		t.Run(mode, func(t *testing.T) {
			classes, packages, settings := catalogBodies(t)
			if mode == "unsafe-id" {
				classes = strings.ReplaceAll(classes, createClassID, "../unsafe")
			}
			var packageReads atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v2/synthetic/quota-class":
					_, _ = io.WriteString(w, classes)
				case "/billing-api/v1/synthetic/packages":
					_, _ = io.WriteString(w, packages)
				case "/billing-api/v1/settings":
					_, _ = io.WriteString(w, settings)
				case "/billing-api/v2/synthetic/quota-class/" + createClassID + "/packages":
					packageReads.Add(1)
					testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticClassPackages.json")
				default:
					// Disabled-class 404 is a policy mock; no live failure was recorded.
					packageReads.Add(1)
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{"message":"peer-canary"}`)
				}
			}))
			out, err := c.ListSyntheticPackages(context.Background(), nil)
			if out != nil || err == nil {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "unsafe-id" && packageReads.Load() != 0 {
				t.Fatalf("unsafe peer path used: %d", packageReads.Load())
			}
			if mode == "disabled-failure" && packageReads.Load() != 2 {
				t.Fatalf("reads=%d", packageReads.Load())
			}
			if strings.Contains(err.Error(), "peer-canary") {
				t.Fatal("peer text leaked")
			}
		})
	}
}

func TestSyntheticCreateFrozenCatalogAndAbsence(t *testing.T) {
	for _, mode := range []string{"final-price", "final-option", "step", "forged-absence", "delayed"} {
		t.Run(mode, func(t *testing.T) {
			s := &syntheticCreateServer{send: func(http.ResponseWriter, *http.Request) {}}
			base := s.handler(t)
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case mode == "forged-absence" && r.URL.Path == "/billing-api/v1/synthetic/quota":
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{"message":"Not found the Resource"}`)
					return
				case r.URL.Path == "/billing-api/v2/synthetic/quota-class" && (mode == "step" || mode == "final-option" && s.quotes.Load() > 0):
					body := testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticQuotaClasses.json")
					body = strings.ReplaceAll(body, `"minApi": 10`, `"minApi": 9`)
					if mode == "step" {
						body = strings.ReplaceAll(body, `"step": 1`, `"step": 2`)
					}
					_, _ = io.WriteString(w, body)
					return
				case mode == "final-price" && strings.HasSuffix(r.URL.Path, "/packages") && s.quotes.Load() > 0:
					body := testutil.FixtureBody(t, "../testdata/monitor/ListSyntheticClassPackages.json")
					_, _ = io.WriteString(w, strings.ReplaceAll(body, `"price": 0.0`, `"price": 1`))
					return
				}
				base(w, r)
			})))
			start := time.Date(2026, 11, 1, 6, 40, 0, 0, time.UTC)
			now := start
			c.now = func() time.Time { return now }
			c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
			if mode == "delayed" {
				s.quota = func(n int32) string {
					if s.sends.Load() > 0 && n > 3 {
						return testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
					}
					return ""
				}
			}
			in := syntheticCreateInput()
			in.NoWait = false
			out, err := c.CreateSyntheticQuota(context.Background(), in)
			if mode == "delayed" {
				if err != nil || !out.Created || now.Sub(start) != 2*time.Second || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v elapsed=%s", out, err, now.Sub(start))
				}
				return
			}
			if err == nil || out.Sent != "no" || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if strings.HasPrefix(mode, "final-") && !errors.Is(err, ErrQuotaChanged) {
				t.Fatal(err)
			}
			if mode == "forged-absence" && s.quotes.Load() != 0 {
				t.Fatal("quoted forged absence")
			}
		})
	}
}

func TestSyntheticLifecycleEarlyErrorsRetainEvidence(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			for _, failure := range []string{"canceled", "locked cancellation", "invalid input", "invalid budget", "canceled stale guard"} {
				t.Run(fmt.Sprintf("delete=%t/unknown=%t/%s", deleting, unknown, failure), func(t *testing.T) {
					s := &syntheticCreateServer{send: func(w http.ResponseWriter, _ *http.Request) {
						if unknown {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
						}
					}}
					if deleting {
						s.quota = func(int32) string {
							return testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
						}
					}
					base := s.handler(t)
					c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if deleting && r.Method == http.MethodDelete {
							s.sends.Add(1)
							if unknown {
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
							} else {
								w.WriteHeader(502)
							}
							return
						}
						base(w, r)
					})))
					c.now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
					createIn := syntheticCreateInput()
					deleteIn := &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"}
					var sentAt *time.Time
					if deleting {
						_, err := c.DeleteSyntheticQuota(context.Background(), deleteIn)
						if !errors.Is(err, dns.ErrNotSettled) {
							t.Fatal(err)
						}
					} else {
						out, err := c.CreateSyntheticQuota(context.Background(), createIn)
						if err != nil && !errors.Is(err, dns.ErrNotSettled) {
							t.Fatal(err)
						}
						sentAt = out.SentAt
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch failure {
					case "canceled":
						cancel()
					case "locked cancellation":
						if err := c.quotaMu.acquire(context.Background()); err != nil {
							t.Fatal(err)
						}
						defer c.quotaMu.release()
						cancel()
					case "canceled stale guard":
						deleteIn.ExpectedQuotaID = "other"
						cancel()
					case "invalid budget":
						createIn.MaxPrice = -1
						deleteIn.Purge = true
					case "invalid input":
						createIn.ClassID = "invalid/id"
						deleteIn.ExpectedQuotaID = "invalid/id"
					}
					var err error
					if deleting {
						out, e := c.DeleteSyntheticQuota(ctx, deleteIn)
						err = e
						if out == nil || out.Stage != "sent" {
							t.Fatalf("out=%+v err=%v", out, err)
						}
					} else {
						out, e := c.CreateSyntheticQuota(ctx, createIn)
						err = e
						sent := "yes"
						status := 200
						if unknown {
							sent = "unknown"
							status = 0
						}
						if out.Sent != sent || out.Stage != "unconfirmed" || out.Error == nil || out.Error.Code != "NotSettled" || out.Error.Status != status || out.SentAt == nil || !out.SentAt.Equal(*sentAt) {
							t.Fatalf("out=%+v err=%v", out, err)
						}
					}
					if !errors.Is(err, dns.ErrNotSettled) || s.sends.Load() != 1 {
						t.Fatalf("err=%v sends=%d", err, s.sends.Load())
					}
					cause := core.ErrInvalidInput
					if failure != "invalid input" && failure != "invalid budget" {
						cause = context.Canceled
					}
					if !errors.Is(err, cause) {
						t.Fatal("lost early cause", err)
					}
					if deleting && failure == "canceled stale guard" && !errors.Is(err, core.ErrInvalidInput) {
						t.Fatal("lost stale guard cause", err)
					}
				})
			}
		}
	}
}

func TestSyntheticCreateNonEncodablePeerTime(t *testing.T) {
	for _, field := range []string{"startTime", "endTime", "createdAt", "updatedAt"} {
		t.Run(field, func(t *testing.T) {
			s := &syntheticCreateServer{send: func(http.ResponseWriter, *http.Request) {}}
			var q map[string]any
			if err := json.Unmarshal([]byte(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")), &q); err != nil {
				t.Fatal(err)
			}
			q[field] = "2501-01-01T00:00:00+24:00"
			raw, _ := json.Marshal(q)
			s.quota = func(int32) string {
				if s.sends.Load() > 0 {
					return string(raw)
				}
				return ""
			}
			c := syntheticCreateClient(t, s)
			in := syntheticCreateInput()
			in.NoWait = false
			out, err := c.CreateSyntheticQuota(context.Background(), in)
			if !errors.Is(err, dns.ErrNotSettled) || out.Created || out.Quota != nil || out.Sent != "yes" || out.Error.Code != "NotSettled" || s.sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
