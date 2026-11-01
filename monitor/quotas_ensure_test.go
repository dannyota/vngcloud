package monitor

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// State variants below are labeled mocks based on the existing raw quota.
func TestEnsureSyntheticQuotaSelection(t *testing.T) {
	for _, tc := range []struct {
		name, status, end, id, action, stage string
		days                                 int
		cause                                error
		sends                                int32
	}{
		{name: "not due", status: "ACTIVE", end: "2026-12-01T05:19:45Z", days: 7, action: "none", stage: "not-due"},
		{name: "guard before skip", status: "ACTIVE", end: "2026-12-01T05:19:45Z", days: 7, id: "other", action: "none", stage: "preflight", cause: ErrQuotaChanged},
		{name: "zero threshold", status: "ACTIVE", end: "2026-12-01T05:19:45Z", action: "renew", stage: "accepted", sends: 1},
		{name: "threshold boundary", status: "ACTIVE", end: "2026-11-08T06:00:00Z", days: 7, action: "renew", stage: "accepted", sends: 1},
		{name: "outside boundary", status: "ACTIVE", end: "2026-11-08T06:00:00.000000001Z", days: 7, action: "none", stage: "not-due"},
		{name: "recover", status: "IN-TRASH", end: "2026-11-01T05:59:59.999999999Z", days: 7, action: "recover", stage: "accepted", sends: 1},
		{name: "active exact expiry", status: "ACTIVE", end: "2026-11-01T06:00:00Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "trash exact expiry", status: "IN-TRASH", end: "2026-11-01T06:00:00Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "active expired", status: "ACTIVE", end: "2026-10-31T06:00:00Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "trash unexpired", status: "IN-TRASH", end: "2026-12-01T05:19:45Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "creating", status: "CREATING", end: "2026-12-01T05:19:45Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "error", status: "ERROR", end: "2026-12-01T05:19:45Z", action: "none", stage: "preflight", cause: core.ErrInvalidInput},
		{name: "unknown", status: "FUTURE", end: "2026-12-01T05:19:45Z", action: "none", stage: "preflight", cause: ErrUnexpectedStatus},
		{name: "missing", action: "none", stage: "preflight", cause: core.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &syntheticOrderServer{recovery: tc.action == "recover", send: func(http.ResponseWriter, *http.Request) {}}
			s.quota = func(int32) string {
				if tc.name == "missing" {
					return ""
				}
				body := syntheticFixture(t, false)
				body = strings.ReplaceAll(body, "2026-12-01T05:19:45.000+00:00", tc.end)
				return strings.ReplaceAll(body, `"status": "ACTIVE"`, `"status": "`+tc.status+`"`)
			}
			c := syntheticTestClient(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := c.EnsureSyntheticQuota(ctx, &EnsureSyntheticQuotaInput{ExpectedQuotaID: tc.id, MaxDaysLeft: tc.days, NoWait: true})
			if !errors.Is(err, tc.cause) || out.Action != tc.action || out.Stage != tc.stage || s.sends.Load() != tc.sends || out.Renewed || out.Recovered || out.Quota != nil {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, s.sends.Load())
			}
			wantReads := int32(1)
			wantQuotes := int32(0)
			if tc.sends > 0 {
				wantReads = 2
				wantQuotes = 1
			}
			if s.reads.Load() != wantReads || s.quotes.Load() != wantQuotes {
				t.Fatalf("reads=%d quotes=%d", s.reads.Load(), s.quotes.Load())
			}
		})
	}
}

func TestEnsureSyntheticQuotaInvalidInput(t *testing.T) {
	s := &syntheticOrderServer{}
	c := syntheticTestClient(t, s)
	for _, in := range []*EnsureSyntheticQuotaInput{nil, {ExpectedQuotaID: "../other"}, {MaxDaysLeft: -1}, {MaxDaysLeft: math.MaxInt}, {MaxPrice: -1}, {MaxPrice: math.Inf(1)}, {MaxPrice: math.NaN()}} {
		out, err := c.EnsureSyntheticQuota(context.Background(), in)
		if !errors.Is(err, core.ErrInvalidInput) || out.Sent != "no" || out.Action != "none" || out.Error.Operation != "monitor.EnsureSyntheticQuota" || s.reads.Load() != 0 {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}

func TestEnsureSyntheticQuotaFinalRace(t *testing.T) {
	for _, mode := range []string{"replacement", "state", "price"} {
		t.Run(mode, func(t *testing.T) {
			s := &syntheticOrderServer{send: func(http.ResponseWriter, *http.Request) {}}
			s.quota = func(n int32) string {
				body := syntheticFixture(t, false)
				if n == 2 {
					if mode == "replacement" {
						body = strings.ReplaceAll(body, "quota-syn-1", "quota-syn-2")
					}
					if mode == "state" {
						body = strings.ReplaceAll(body, "ACTIVE", "IN-TRASH")
					}
				}
				return body
			}
			if mode == "price" {
				s.price = `{"optimumPrice":1}`
			}
			// Replacement must fail at the identity guard, before its detail request.
			handler := s.handler(t)
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "quota-syn-2/quota-detail") {
					body := strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json"), "quota-syn-1", "quota-syn-2")
					_, _ = io.WriteString(w, body)
					return
				}
				handler(w, r)
			})))
			c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
			out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{})
			want := ErrQuotaChanged
			if mode == "price" {
				want = ErrPriceAboveMax
			}
			if !errors.Is(err, want) || out.Action != "renew" || out.Sent != "no" || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestEnsureSyntheticQuotaRetainedAction(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, sent := range []string{"yes", "unknown"} {
			t.Run(fmtSyntheticCase(recovery, sent), func(t *testing.T) {
				settled := false
				s := &syntheticOrderServer{recovery: recovery, send: func(w http.ResponseWriter, r *http.Request) {
					if sent == "unknown" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					}
				}}
				s.quota = func(int32) string {
					if settled {
						return strings.ReplaceAll(syntheticFixture(t, false), "2026-12-01", "2027-01-01")
					}
					return syntheticFixture(t, recovery)
				}
				c := syntheticTestClient(t, s)
				in := &EnsureSyntheticQuotaInput{NoWait: true}
				out, err := c.EnsureSyntheticQuota(context.Background(), in)
				if out.Sent != sent || (sent == "yes" && err != nil) || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				action := out.Action
				in.ExpectedQuotaID = "other"
				out, err = c.EnsureSyntheticQuota(context.Background(), in)
				if !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, ErrQuotaChanged) || out.Sent != sent || out.Action != action || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				in.ExpectedQuotaID = ""
				settled = true
				out, err = c.EnsureSyntheticQuota(context.Background(), in)
				if err != nil || out.Action != action || out.Sent != sent || out.Stage != "confirmed" || out.Recovered != recovery || out.Renewed == recovery || s.sends.Load() != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			})
		}
	}
}

func TestEnsureSyntheticQuotaForbidden(t *testing.T) {
	for _, quote := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutation", true: "quote"}[quote], func(t *testing.T) {
			s := &syntheticOrderServer{recovery: true, quota: func(int32) string { return syntheticFixture(t, true) }, send: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(403)
				_, _ = io.WriteString(w, `{"message":"secret-canary"}`)
			}}
			handler := s.handler(t)
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if quote && strings.Contains(r.URL.Path, "created-price") {
					s.quotes.Add(1)
					w.WriteHeader(403)
					return
				}
				handler(w, r)
			})))
			c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
			out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{})
			var api *core.APIError
			if !errors.As(err, &api) || api.Code != "Forbidden" || api.Operation != "monitor.EnsureSyntheticQuota" || out.Action != "recover" || out.Error.Code != "Forbidden" || strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if quote && (out.Sent != "no" || s.sends.Load() != 0) || !quote && (out.Sent != "yes" || out.Stage != "refused" || s.sends.Load() != 1) {
				t.Fatalf("out=%+v sends=%d", out, s.sends.Load())
			}
		})
	}
}

func TestEnsureSyntheticQuotaSharedLedger(t *testing.T) {
	for _, ensureFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "renew then ensure", true: "ensure then renew"}[ensureFirst], func(t *testing.T) {
			settled := false
			s := &syntheticOrderServer{send: func(http.ResponseWriter, *http.Request) {}}
			s.quota = func(int32) string {
				raw := syntheticFixture(t, false)
				if settled {
					raw = strings.ReplaceAll(raw, "2026-12-01", "2027-01-01")
				}
				return raw
			}
			c := syntheticTestClient(t, s)
			call := func(ensure bool) (QuotaMutationOutcome, bool, error) {
				if ensure {
					out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{NoWait: true})
					return out.QuotaMutationOutcome, out.Renewed, err
				}
				out, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", NoWait: true})
				return out.QuotaMutationOutcome, out.Renewed, err
			}
			if _, _, err := call(ensureFirst); err != nil {
				t.Fatal(err)
			}
			out, confirmed, err := call(!ensureFirst)
			if !errors.Is(err, dns.ErrNotSettled) || out.Sent != "yes" || confirmed || s.sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			settled = true
			out, confirmed, err = call(!ensureFirst)
			if !errors.Is(err, core.ErrInvalidInput) || out.Sent != "no" || confirmed || s.sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestEnsureSyntheticQuotaReconcileErrorOperation(t *testing.T) {
	s := &syntheticOrderServer{quota: func(int32) string { return syntheticFixture(t, false) }, send: func(http.ResponseWriter, *http.Request) {}}
	handler := s.handler(t)
	failRead := false
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failRead && r.URL.Path == "/billing-api/v1/synthetic/quota" {
			w.WriteHeader(403)
			return
		}
		handler(w, r)
	})))
	c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
	if _, err := c.RenewSyntheticQuota(context.Background(), &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	failRead = true
	out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{})
	var api *core.APIError
	if !errors.Is(err, dns.ErrNotSettled) || !errors.As(err, &api) || api.Operation != "monitor.EnsureSyntheticQuota" || out.Error.Operation != "monitor.EnsureSyntheticQuota" || out.Action != "none" || out.Sent != "yes" || s.sends.Load() != 1 {
		t.Fatalf("out=%+v api=%+v err=%v", out, api, err)
	}
}

func TestEnsureSyntheticQuotaEligibilityBeforeSkip(t *testing.T) {
	for _, mode := range []string{"auto-renew", "POC", "class", "capacity", "price"} {
		t.Run(mode, func(t *testing.T) {
			s := &syntheticOrderServer{send: func(http.ResponseWriter, *http.Request) { t.Error("ineligible send") }}
			s.quota = func(int32) string {
				raw := syntheticFixture(t, false)
				if mode == "auto-renew" {
					raw = strings.ReplaceAll(raw, `"autoRenew": 0`, `"autoRenew": 1`)
				}
				if mode == "POC" {
					raw = strings.ReplaceAll(raw, `"poc": 0`, `"poc": 1`)
				}
				return raw
			}
			s.detail = func(int32) string {
				raw := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
				switch mode {
				case "class":
					return strings.ReplaceAll(raw, `"Basic"`, `"Pro"`)
				case "capacity":
					return strings.ReplaceAll(raw, `"api": 10`, `"api": 20`)
				case "price":
					return strings.ReplaceAll(raw, `"price": 0.0`, `"price": 10`)
				}
				return raw
			}
			c := syntheticTestClient(t, s)
			out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{MaxDaysLeft: 7})
			if err == nil || out.Stage != "preflight" || out.Sent != "no" || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestEnsureSyntheticQuotaLoadedProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	t.Cleanup(server.Close)
	for _, value := range []string{"true", "1", "invalid"} {
		c := New(lifecycleProfileConfig(t, server, value))
		out, err := c.EnsureSyntheticQuota(context.Background(), &EnsureSyntheticQuotaInput{})
		assertLifecycleProfileError(t, err, value == "invalid", "monitor.EnsureSyntheticQuota")
		if out.Sent != "no" || out.Action != "none" {
			t.Fatalf("out=%+v", out)
		}
	}
}

func TestEnsureSyntheticQuotaConcurrentCalls(t *testing.T) {
	s := &syntheticOrderServer{send: func(http.ResponseWriter, *http.Request) {}}
	s.quota = func(int32) string {
		raw := syntheticFixture(t, false)
		if s.sends.Load() > 0 {
			raw = strings.ReplaceAll(raw, "2026-12-01", "2027-01-01")
		}
		return raw
	}
	c := syntheticTestClient(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for range 2 {
		go func() {
			out, err := c.EnsureSyntheticQuota(ctx, &EnsureSyntheticQuotaInput{NoWait: true})
			if err == nil && (out.Action != "renew" || out.Sent != "yes" || out.Recovered) {
				err = errors.New("invalid concurrent outcome")
			}
			done <- err
		}()
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

// Permission failures are mocks; each origin sends through the real transport
// with normal retries enabled before the same client changes public operation.
func TestSyntheticReconciliationCallerOperation(t *testing.T) {
	operations := []string{"EnsureSyntheticQuota", "RenewSyntheticQuota", "RecoverSyntheticQuota", "CreateSyntheticQuota", "DeleteSyntheticQuota"}
	for _, origin := range operations {
		for _, caller := range operations {
			for _, unknown := range []bool{false, true} {
				t.Run(origin+"-to-"+caller+map[bool]string{false: "/response", true: "/disconnect"}[unknown], func(t *testing.T) {
					var sends, quotes, denied atomic.Int32
					failRead := false
					c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch {
						case r.Method == http.MethodDelete || r.Method == http.MethodPost && (r.URL.Path == "/billing-api/v2/synthetic/quota" || r.URL.Path == "/billing-api/v2/synthetic/quota/extend-time" || r.URL.Path == "/billing-api/v2/trash/synthetic/quota"):
							expected := "/billing-api/v2/synthetic/quota/extend-time"
							switch origin {
							case "CreateSyntheticQuota":
								expected = "/billing-api/v2/synthetic/quota"
							case "RecoverSyntheticQuota":
								expected = "/billing-api/v2/trash/synthetic/quota"
							case "DeleteSyntheticQuota":
								expected = "/billing-api/v1/synthetic/quota"
							}
							if r.URL.Path != expected {
								t.Errorf("wrong mutation route: %s", r.URL.Path)
							}
							sends.Add(1)
							_, _ = io.Copy(io.Discard, r.Body)
							if unknown {
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
							} else if origin == "DeleteSyntheticQuota" {
								w.WriteHeader(502)
							}
						case r.URL.Path == "/billing-api/v1/synthetic/quota":
							if failRead {
								denied.Add(1)
								w.WriteHeader(403)
								_, _ = io.WriteString(w, `{"message":"peer-canary"}`)
								return
							}
							if origin == "CreateSyntheticQuota" {
								w.WriteHeader(404)
								testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDeleted.json")
								return
							}
							testutil.WriteFixture(t, w, "../testdata/monitor/"+map[bool]string{true: "GetSyntheticQuotaInTrash", false: "GetSyntheticQuotaRecovered"}[origin == "RecoverSyntheticQuota"]+".json")
						case r.URL.Path == "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
							testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
						case r.URL.Path == "/billing-api/v2/synthetic/quota-class":
							testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticQuotaClasses.json")
						case r.URL.Path == "/billing-api/v2/synthetic/quota-class/"+createClassID+"/packages":
							testutil.WriteFixture(t, w, "../testdata/monitor/ListSyntheticClassPackages.json")
						case r.URL.Path == "/billing-api/v1/settings":
							testutil.WriteFixture(t, w, "../testdata/monitor/GetQuotaSettings.json")
						case r.URL.Path == "/billing-api/v2/synthetic/prices/created-price":
							quotes.Add(1)
							_, _ = io.WriteString(w, `{"optimumPrice":0}`)
						default:
							t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(400)
						}
					})))
					c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					invoke := func(operation string) (*QuotaMutationOutcome, error) {
						switch operation {
						case "EnsureSyntheticQuota":
							out, err := c.EnsureSyntheticQuota(ctx, &EnsureSyntheticQuotaInput{NoWait: true})
							if out.Renewed || out.Recovered {
								t.Error("claimed ensure confirmation")
							}
							return &out.QuotaMutationOutcome, err
						case "RenewSyntheticQuota":
							out, err := c.RenewSyntheticQuota(ctx, &RenewSyntheticQuotaInput{ExpectedEndTime: "2026-12-01T05:19:45Z", NoWait: true})
							if out.Renewed {
								t.Error("claimed renewal confirmation")
							}
							return &out.QuotaMutationOutcome, err
						case "RecoverSyntheticQuota":
							out, err := c.RecoverSyntheticQuota(ctx, &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1", NoWait: true})
							if out.Recovered {
								t.Error("claimed recovery confirmation")
							}
							return &out.QuotaMutationOutcome, err
						case "CreateSyntheticQuota":
							out, err := c.CreateSyntheticQuota(ctx, syntheticCreateInput())
							if out.Created {
								t.Error("claimed create confirmation")
							}
							return &out.QuotaMutationOutcome, err
						default:
							out, err := c.DeleteSyntheticQuota(ctx, &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1"})
							if out.Stage == "settled" || out.Purged {
								t.Error("claimed delete confirmation")
							}
							return nil, err
						}
					}
					_, err := invoke(origin)
					if err != nil && !errors.Is(err, dns.ErrNotSettled) {
						t.Fatal(err)
					}
					attempt := c.quotaAttempts["synthetic"]
					if attempt == nil || sends.Load() != 1 {
						t.Fatalf("attempt=%+v sends=%d", attempt, sends.Load())
					}
					key, result, evidence := attempt.key, attempt.result, c.quotaMu.syntheticEvidence
					failRead = true
					for repeat := int32(1); repeat <= 2; repeat++ {
						outcome, err := invoke(caller)
						var api *core.APIError
						if !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, core.ErrPermission) || !errors.As(err, &api) || api.Operation != "monitor."+caller || api.Code != "Forbidden" || api.StatusCode != 403 || strings.Contains(err.Error(), "peer-canary") {
							t.Fatalf("caller=%s api=%+v err=%v", caller, api, err)
						}
						if c.quotaAttempts["synthetic"] != attempt || !attempt.key.matches(key) || attempt.result != result || c.quotaMu.syntheticEvidence != evidence || sends.Load() != 1 || denied.Load() != repeat {
							t.Fatalf("retained attempt changed: before=%+v after=%+v sends=%d reads=%d", key, attempt.key, sends.Load(), denied.Load())
						}
						wantStatus := result.status
						if wantStatus == 0 {
							wantStatus = 403
						}
						if outcome != nil && (outcome.Sent != result.sent || outcome.Stage != "unconfirmed" || outcome.Error.Operation != "monitor."+caller || outcome.Error.Code != "NotSettled" || outcome.Error.Status != wantStatus) {
							t.Fatalf("outcome=%+v error=%+v", outcome, outcome.Error)
						}
					}
					wantQuotes := int32(1)
					if origin == "DeleteSyntheticQuota" {
						wantQuotes = 0
					}
					if quotes.Load() != wantQuotes {
						t.Fatalf("quotes=%d want=%d", quotes.Load(), wantQuotes)
					}
				})
			}
		}
	}
}
