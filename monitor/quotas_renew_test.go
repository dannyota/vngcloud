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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

type syntheticOrderServer struct {
	recovery                      bool
	sends, reads, quotes, details atomic.Int32
	quota                         func(int32) string
	detail                        func(int32) string
	price                         string
	send                          http.HandlerFunc
}

func (s *syntheticOrderServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v1/synthetic/quota":
			n := s.reads.Add(1)
			if body := s.quota(n); body != "" {
				_, _ = io.WriteString(w, body)
			} else {
				w.WriteHeader(404)
			}
		case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
			n := s.details.Add(1)
			body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
			if s.detail != nil {
				body = s.detail(n)
			}
			_, _ = io.WriteString(w, body)
		case "/billing-api/v2/synthetic/prices/created-price":
			s.quotes.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]any{"redirectUrl": "", "packageId": "32b3743d-94b8-49ab-984d-bd7ec99b46f6", "quantity": float64(10), "resourceId": "quota-syn-1"}) {
				t.Errorf("quote=%v", body)
			}
			if s.price != "" {
				_, _ = io.WriteString(w, s.price)
			} else {
				name := "QuoteRenewSyntheticQuota"
				if s.recovery {
					name = "QuoteRecoverSyntheticQuota"
				}
				testutil.WriteFixture(t, w, "../testdata/monitor/"+name+".json")
			}
		case "/billing-api/v2/synthetic/quota/extend-time", "/billing-api/v2/trash/synthetic/quota":
			if (strings.Contains(r.URL.Path, "trash")) != s.recovery {
				t.Errorf("wrong operation %s", r.URL.Path)
			}
			s.sends.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]any{"redirectUrl": syntheticQuotaRedirect, "monthPeriod": float64(1), "pay": true}) {
				t.Errorf("mutation=%v", body)
			}
			s.send(w, r)
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(500)
		}
	}
}
func syntheticFixture(t *testing.T, recovery bool) string {
	t.Helper()
	name := "GetSyntheticQuotaRecovered"
	if recovery {
		name = "GetSyntheticQuotaInTrash"
	}
	return testutil.FixtureBody(t, "../testdata/monitor/"+name+".json")
}
func syntheticTestClient(t *testing.T, s *syntheticOrderServer) *Client {
	t.Helper()
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	now := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return c
}
func syntheticCall(c *Client, recovery, noWait bool) (*syntheticMutationResult, error) {
	input := syntheticMutationInput{op: "monitor.RenewSyntheticQuota", expectedEnd: "2026-12-01T05:19:45Z", noWait: noWait}
	if recovery {
		input = syntheticMutationInput{op: "monitor.RecoverSyntheticQuota", recover: true, expectedID: "quota-syn-1", noWait: noWait}
	}
	result, err := c.runSyntheticMutation(context.Background(), input)
	return &result, err
}
func TestSyntheticMutationResponsePolicy(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, body := range []string{"", "null", "{}", `{"orderId":null,"paymentUrl":null,"amount":{}}`, `{"orderId":"order-1","paymentUrl":"` + syntheticQuotaRedirect + `","unknown":true}`, `{"orderId":3}`, `{"paymentUrl":false}`, `{"paymentUrl":"https://private-canary.invalid/pay"}`, "[]", "true", "{", strings.Repeat("x", quotaMaxBody+1)} {
			t.Run(fmtSyntheticCase(recovery, body), func(t *testing.T) {
				s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { return syntheticFixture(t, recovery) }, send: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }}
				c := syntheticTestClient(t, s)
				out, err := syntheticCall(c, recovery, true)
				valid := body == "" || body == "null" || body == "{}" || strings.Contains(body, `"orderId":null`) || strings.Contains(body, `"order-1"`)
				if (err == nil) != valid || out.outcome.Sent != "yes" || out.confirmed || out.quota != nil || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				if err != nil && (!errors.Is(err, dns.ErrNotSettled) || out.outcome.Error.Status != 200) {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				if strings.Contains(body, "private-canary") && !out.paymentPending {
					t.Fatal("missing payment pending")
				}
				again, e := syntheticCall(c, recovery, true)
				if !errors.Is(e, dns.ErrNotSettled) || again.outcome.Sent != "yes" || s.sends.Load() != 1 {
					t.Fatalf("again=%+v err=%v", again, e)
				}
			})
		}
	}
}
func fmtSyntheticCase(recovery bool, body string) string {
	if len(body) > 80 {
		body = "oversize"
	}
	if recovery {
		return "recover/" + body
	}
	return "renew/" + body
}
func TestSyntheticMutationReadbackAndGuardMismatch(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, confirmed := range []bool{false, true} {
			t.Run(fmtSyntheticCase(recovery, map[bool]string{false: "unresolved", true: "confirmed"}[confirmed]), func(t *testing.T) {
				s := &syntheticOrderServer{recovery: recovery, send: func(http.ResponseWriter, *http.Request) {}}
				settled := false
				s.quota = func(int32) string {
					body := syntheticFixture(t, recovery)
					if settled {
						body = syntheticFixture(t, false)
						if !recovery {
							body = strings.ReplaceAll(body, "2026-12-01", "2027-01-01")
						}
					}
					return body
				}
				c := syntheticTestClient(t, s)
				out, err := syntheticCall(c, recovery, true)
				if err != nil || out.outcome.Stage != "accepted" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				settled = confirmed
				in := syntheticMutationInput{op: "monitor.RenewSyntheticQuota", expectedEnd: "2026-12-02T05:19:45Z"}
				if recovery {
					in = syntheticMutationInput{op: "monitor.RecoverSyntheticQuota", recover: true, expectedID: "other"}
				}
				changed, e := c.runSyntheticMutation(context.Background(), in)
				if !errors.Is(e, ErrQuotaChanged) || errors.Is(e, dns.ErrNotSettled) == confirmed || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v sends=%d", changed, e, s.sends.Load())
				}
				if !confirmed && changed.outcome.Sent != "yes" {
					t.Fatalf("out=%+v", changed)
				}
			})
		}
	}
}
func TestRenewSyntheticQuotaGuardsAndQuote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     RenewSyntheticQuotaInput
		code   string
		quotes int32
	}{
		{"missing", RenewSyntheticQuotaInput{}, "InvalidUsage", 0},
		{"syntax", RenewSyntheticQuotaInput{ExpectedEndTime: "yesterday"}, "InvalidUsage", 0},
		{"overflow", RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", MaxDaysLeft: math.MaxInt}, "InvalidUsage", 0},
		{"negative", RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", MaxPrice: -1}, "InvalidUsage", 0},
		{"stale before skip", RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-02T05:19:45Z", MaxDaysLeft: 7}, "QuotaChanged", 0},
		{"skip", RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", MaxDaysLeft: 7}, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }, send: func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected mutation") }}
			c := syntheticTestClient(t, s)
			out, err := c.RenewSyntheticQuota(context.Background(), &tc.in)
			if out == nil || out.Sent != "no" || s.sends.Load() != 0 || s.quotes.Load() != tc.quotes {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if tc.code == "" {
				if err != nil || out.Stage != "not-due" || out.PreviousEndTime == nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			} else if err == nil || out.Error.Code != tc.code {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
	s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }}
	c := syntheticTestClient(t, s)
	quote, err := c.QuoteRenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T12:19:45+07:00", MaxPrice: math.NaN(), MaxDaysLeft: -1, NoWait: true})
	if err != nil || quote.Quantity != 10 || quote.ListPrice != 0 || quote.GuardPrice != 0 || s.sends.Load() != 0 || s.quotes.Load() != 1 {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
}
func TestRenewSyntheticQuotaEarlyConflictFixture(t *testing.T) {
	s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }, send: func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409)
		testutil.WriteFixture(t, w, "../testdata/monitor/RenewSyntheticQuotaEarlyConflict.json")
	}}
	c := syntheticTestClient(t, s)
	out, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z"})
	var api *core.APIError
	if !errors.As(err, &api) || api.StatusCode != 409 || out.Sent != "yes" || out.Stage != "refused" || out.Error.Code != "Conflict" || s.reads.Load() != 2 || len(c.quotaAttempts) != 0 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestSyntheticMutationDelayedReadback(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, intermediate := range []string{"404", "CREATING", "same expiry", "replacement id", "replacement package", "trash exit"} {
			t.Run(fmtSyntheticCase(recovery, intermediate), func(t *testing.T) {
				s := &syntheticOrderServer{recovery: recovery, send: func(http.ResponseWriter, *http.Request) {}}
				s.quota = func(n int32) string {
					baseline := syntheticFixture(t, recovery)
					if n <= 2 {
						return baseline
					}
					later := syntheticFixture(t, false)
					if !recovery {
						later = strings.ReplaceAll(later, "2026-12-01", "2027-01-01")
					}
					if n > 3 {
						return later
					}
					switch intermediate {
					case "404":
						return ""
					case "CREATING":
						return strings.ReplaceAll(later, `"ACTIVE"`, `"CREATING"`)
					case "same expiry":
						return baseline
					case "replacement id":
						return strings.ReplaceAll(later, "quota-syn-1", "quota-syn-2")
					case "replacement package":
						return strings.ReplaceAll(later, "32b3743d-94b8-49ab-984d-bd7ec99b46f6", "other")
					case "trash exit":
						return strings.ReplaceAll(baseline, `"IN-TRASH"`, `"ACTIVE"`)
					}
					return baseline
				}
				c := syntheticTestClient(t, s)
				out, err := syntheticCall(c, recovery, false)
				if err != nil || !out.confirmed || out.outcome.Stage != "confirmed" || out.quota == nil || s.sends.Load() != 1 || s.reads.Load() != 4 {
					t.Fatalf("out=%+v err=%v reads=%d", out, err, s.reads.Load())
				}
			})
		}
	}
}

func TestSyntheticMutationUnknownEvidenceAndChangedIntent(t *testing.T) {
	for _, sent := range []string{"yes", "unknown"} {
		for _, recovery := range []bool{false, true} {
			t.Run(sent+fmtSyntheticCase(recovery, ""), func(t *testing.T) {
				s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { return syntheticFixture(t, recovery) }, send: func(http.ResponseWriter, *http.Request) {}}
				c := syntheticTestClient(t, s)
				_, err := syntheticCall(c, recovery, true)
				if err != nil {
					t.Fatal(err)
				}
				a := c.quotaAttempts["synthetic"]
				a.result.sent = sent
				other := syntheticMutationInput{op: "monitor.RecoverSyntheticQuota", recover: true, expectedID: "other"}
				if recovery {
					other = syntheticMutationInput{op: "monitor.RenewSyntheticQuota", expectedEnd: "2026-12-02T05:19:45Z"}
				}
				result, err := c.runSyntheticMutation(context.Background(), other)
				if !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, ErrQuotaChanged) || result.outcome.Sent != sent || result.outcome.Stage != "unconfirmed" || s.sends.Load() != 1 || c.quotaAttempts["synthetic"] != a {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestSyntheticMutationLocalRefusal(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, price := range []float64{-1, math.NaN(), math.Inf(1)} {
			s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { t.Fatal("unexpected request"); return "" }}
			c := syntheticTestClient(t, s)
			var outcome QuotaMutationOutcome
			var err error
			if recovery {
				out, e := c.RecoverSyntheticQuota(context.Background(), &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1", MaxPrice: price})
				outcome, err = out.QuotaMutationOutcome, e
			} else {
				out, e := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", MaxPrice: price})
				outcome, err = out.QuotaMutationOutcome, e
			}
			if !errors.Is(err, core.ErrInvalidInput) || outcome.Sent != "no" || outcome.Error.Code != "InvalidUsage" || s.reads.Load() != 0 {
				t.Fatalf("outcome=%+v err=%v", outcome, err)
			}
		}
	}
}

func TestSyntheticMutationDisconnectRetainsUnknown(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { return syntheticFixture(t, recovery) }, send: func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}}
		c := syntheticTestClient(t, s)
		for range 2 {
			out, err := syntheticCall(c, recovery, true)
			if !errors.Is(err, dns.ErrNotSettled) || out.outcome.Sent != "unknown" || out.outcome.Stage != "unconfirmed" || s.sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, s.sends.Load())
			}
		}
	}
}

func TestSyntheticMutationConcurrentMatchingCalls(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		s := &syntheticOrderServer{recovery: recovery, send: func(http.ResponseWriter, *http.Request) {}}
		s.quota = func(int32) string {
			if s.sends.Load() == 0 {
				return syntheticFixture(t, recovery)
			}
			raw := syntheticFixture(t, false)
			if !recovery {
				raw = strings.ReplaceAll(raw, "2026-12-01", "2027-01-01")
			}
			return raw
		}
		c := syntheticTestClient(t, s)
		done := make(chan error, 2)
		for range 2 {
			go func() { _, err := syntheticCall(c, recovery, true); done <- err }()
		}
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if s.sends.Load() != 1 {
			t.Fatalf("sends=%d", s.sends.Load())
		}
	}
}

func TestRenewSyntheticQuotaEligibilityAndWindowBoundary(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
		code  string
	}{
		{"status", "IN-TRASH", "InvalidUsage"}, {"status", "CREATING", "InvalidUsage"}, {"status", "UNKNOWN", "UnexpectedStatus"},
		{"autoRenew", 1, "InvalidUsage"}, {"poc", 1, "InvalidUsage"},
		{"endTime", "2026-10-25T00:00:00Z", "InvalidUsage"}, {"endTime", "2026-11-01T06:00:00Z", "InvalidUsage"},
	} {
		t.Run(fmt.Sprint(tc.field, tc.value), func(t *testing.T) {
			var obj map[string]any
			if err := json.Unmarshal([]byte(syntheticFixture(t, false)), &obj); err != nil {
				t.Fatal(err)
			}
			obj[tc.field] = tc.value
			raw, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			s := &syntheticOrderServer{quota: func(int32) string { return string(raw) }, send: func(http.ResponseWriter, *http.Request) { t.Error("unexpected mutation") }}
			c := syntheticTestClient(t, s)
			out, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: obj["endTime"].(string), MaxDaysLeft: 7})
			if err == nil || out.Error.Code != tc.code || out.Sent != "no" || s.quotes.Load() != 0 || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
	end := time.Date(2026, 12, 1, 5, 19, 45, 0, time.UTC)
	for _, outside := range []bool{false, true} {
		s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }, send: func(http.ResponseWriter, *http.Request) {}}
		c := syntheticTestClient(t, s)
		now := end.Add(-7 * 24 * time.Hour)
		if outside {
			now = now.Add(-time.Nanosecond)
		}
		c.now = func() time.Time { return now }
		out, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: end.Format(time.RFC3339), MaxDaysLeft: 7, NoWait: true})
		if err != nil || (out.Stage == "not-due") != outside || s.sends.Load() != map[bool]int32{false: 1, true: 0}[outside] {
			t.Fatalf("outside=%t out=%+v err=%v", outside, out, err)
		}
	}
}

func TestRenewSyntheticQuotaExpiryCrossesDuringQuote(t *testing.T) {
	s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }, send: func(http.ResponseWriter, *http.Request) { t.Error("unexpected mutation") }}
	var quoted atomic.Bool
	handler := s.handler(t)
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "created-price") {
			quoted.Store(true)
		}
		handler(w, r)
	})))
	end := time.Date(2026, 12, 1, 5, 19, 45, 0, time.UTC)
	c.now = func() time.Time {
		if quoted.Load() {
			return end
		}
		return end.Add(-time.Second)
	}
	out, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: end.Format(time.RFC3339)})
	if !errors.Is(err, ErrQuotaChanged) || out.Sent != "no" || s.sends.Load() != 0 || s.quotes.Load() != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestQuoteRenewSyntheticQuotaRefreshRedactsFrozenIdentity(t *testing.T) {
	const first, second = "synthetic-token-A", "quota-syn-1"
	c := monitorRefreshClient(t, first, second, "/billing-api/v2/synthetic/prices/created-price", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v1/synthetic/quota":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaRecovered.json")
		case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
		case "/billing-api/v2/synthetic/prices/created-price":
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteRenewSyntheticQuota.json")
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
	out, err := c.QuoteRenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z"})
	if err != nil {
		t.Fatal(err)
	}
	assertMonitorCredentialsAbsent(t, out, err, first, second)
}

func TestSyntheticMutationEarlyErrorsRetainSendEvidence(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			for _, failure := range []string{"canceled", "canceled while locked", "invalid guard", "invalid budget"} {
				t.Run(fmtSyntheticCase(recovery, fmt.Sprint(unknown, "/", failure)), func(t *testing.T) {
					s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { return syntheticFixture(t, recovery) }, send: func(w http.ResponseWriter, _ *http.Request) {
						if unknown {
							conn, _, e := w.(http.Hijacker).Hijack()
							if e != nil {
								t.Error(e)
								return
							}
							_ = conn.Close()
						}
					}}
					c := syntheticTestClient(t, s)
					first, firstErr := syntheticCall(c, recovery, true)
					if firstErr != nil && !errors.Is(firstErr, dns.ErrNotSettled) {
						t.Fatal(firstErr)
					}
					in := syntheticMutationInput{op: "monitor.RenewSyntheticQuota", expectedEnd: "2026-12-01T05:19:45Z", noWait: true}
					if recovery {
						in = syntheticMutationInput{op: "monitor.RecoverSyntheticQuota", recover: true, expectedID: "quota-syn-1", noWait: true}
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch failure {
					case "canceled":
						cancel()
					case "canceled while locked":
						if e := c.quotaMu.acquire(context.Background()); e != nil {
							t.Fatal(e)
						}
						defer c.quotaMu.release()
						cancel()
					case "invalid guard":
						in.expectedID = "invalid/id"
						in.expectedEnd = "invalid"
					case "invalid budget":
						in.maxPrice = -1
					}
					out, err := c.runSyntheticMutation(ctx, in)
					if !errors.Is(err, dns.ErrNotSettled) || out.outcome.Sent != first.outcome.Sent || out.outcome.Stage != "unconfirmed" || out.outcome.Error.Code != "NotSettled" || out.outcome.Error.Status != map[bool]int{false: 200, true: 0}[unknown] || s.sends.Load() != 1 || out.previous == nil || !out.previous.Equal(*first.previous) {
						t.Fatalf("out=%+v error=%+v err=%v sends=%d", out, out.outcome.Error, err, s.sends.Load())
					}
					if strings.HasPrefix(failure, "canceled") && !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cancellation: %v", err)
					}
					if !strings.HasPrefix(failure, "canceled") && !errors.Is(err, core.ErrInvalidInput) {
						t.Fatalf("lost input cause: %v", err)
					}
				})
			}
		}
	}
}

func TestSyntheticMutationEarlyEvidenceConcurrentMembership(t *testing.T) {
	c := new(Client)
	a := &quotaAttempt{key: quotaAttemptKey{operation: "monitor.RenewSyntheticQuota", kind: "synthetic", expectedEndTime: time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)}, result: quotaWriteResult{sent: "yes", status: 200}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			if err := c.quotaMu.acquire(context.Background()); err != nil {
				t.Error(err)
				return
			}
			c.recordQuotaAttempt(a)
			c.clearQuotaAttempt("synthetic")
			c.quotaMu.release()
		}
	}()
	for range 200 {
		out, err := c.runSyntheticMutation(context.Background(), syntheticMutationInput{op: "monitor.RenewSyntheticQuota"})
		if !errors.Is(err, core.ErrInvalidInput) || out.outcome.Error == nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		if out.outcome.Sent != "no" && (out.outcome.Sent != "yes" || !errors.Is(err, dns.ErrNotSettled)) {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
	<-done
	out, err := c.runSyntheticMutation(context.Background(), syntheticMutationInput{op: "monitor.RenewSyntheticQuota"})
	if out.outcome.Sent != "no" || errors.Is(err, dns.ErrNotSettled) {
		t.Fatalf("retired evidence survived: out=%+v err=%v", out, err)
	}
}

func TestSyntheticMutationNonEncodablePeerTimes(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, field := range []string{"startTime", "endTime", "createdAt", "updatedAt"} {
			t.Run(fmtSyntheticCase(recovery, field), func(t *testing.T) {
				var body map[string]any
				if err := json.Unmarshal([]byte(syntheticFixture(t, recovery)), &body); err != nil {
					t.Fatal(err)
				}
				body[field] = "2501-01-01T00:00:00+24:00"
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				s := &syntheticOrderServer{recovery: recovery, quota: func(int32) string { return string(raw) }, send: func(http.ResponseWriter, *http.Request) { t.Error("unexpected mutation") }}
				c := syntheticTestClient(t, s)
				out, err := syntheticCall(c, recovery, false)
				if err == nil || out.outcome.Sent != "no" || out.outcome.Error.Code != "InvalidResponse" || out.previous != nil || s.sends.Load() != 0 || s.quotes.Load() != 0 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			})
		}
	}
}
