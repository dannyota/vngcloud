package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestSyntheticDeleteRequiredGuardOutput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	for _, in := range []*DeleteSyntheticQuotaInput{nil, {}, {ExpectedQuotaID: "../bad"}, {ExpectedQuotaID: "quota-1", Purge: true}} {
		out, err := c.DeleteSyntheticQuota(context.Background(), in)
		if out == nil || !errors.Is(err, core.ErrInvalidInput) || out.Quota != nil || out.Stage != "" || out.Purged {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
}

func TestSyntheticDeleteSingleSend(t *testing.T) {
	for _, status := range []int{204, 200, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504, -1} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var sends atomic.Int32
			settled := false
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete && r.URL.Path == "/billing-api/v1/synthetic/quota":
					sends.Add(1)
					body, _ := io.ReadAll(r.Body)
					if len(body) != 0 {
						t.Errorf("body=%s", body)
					}
					if status < 0 {
						conn, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = conn.Close()
						return
					}
					// Empty 204 is constructed from recorded response metadata.
					w.Header().Set("Location", "/must-not-follow")
					w.WriteHeader(status)
					if status != 204 {
						_, _ = io.WriteString(w, `{"message":"peer-canary"}`)
					}
				case r.URL.Path == "/billing-api/v1/synthetic/quota":
					if settled || (sends.Load() > 0 && status == 204) {
						w.WriteHeader(404)
						testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDeleted.json")
						return
					}
					testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaCreated.json")
				case r.URL.Path == "/billing-api/v2/synthetic/quota-syn-2/quota-detail":
					testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaCreatedDetail.json")
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			})))
			now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return now }
			c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
			in := &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"}
			out, err := c.DeleteSyntheticQuota(context.Background(), in)
			if sends.Load() != 1 || out == nil || out.Quota == nil || out.Purged {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, sends.Load())
			}
			switch {
			case status == 204:
				if err != nil || out.Stage != "settled" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			case status >= 400 && status < 500:
				var api *core.APIError
				if !errors.As(err, &api) || api.StatusCode != status || out.Stage != "prepared" || c.quotaAttempts["synthetic"] != nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			default:
				if !errors.Is(err, dns.ErrNotSettled) || out.Stage != "sent" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				in.ExpectedQuotaID = "other"
				again, e := c.DeleteSyntheticQuota(context.Background(), in)
				if !errors.Is(e, dns.ErrNotSettled) || !errors.Is(e, core.ErrInvalidInput) || again.Stage != "sent" {
					t.Fatalf("again=%+v err=%v", again, e)
				}
				settled = true
				again, e = c.DeleteSyntheticQuota(context.Background(), in)
				if !errors.Is(e, core.ErrInvalidInput) || again.Stage == "settled" {
					t.Fatalf("again=%+v err=%v", again, e)
				}
				if sends.Load() != 1 {
					t.Fatal("resent delete")
				}
			}
		})
	}
}
func TestSyntheticDeleteGuardsAndExactAbsence(t *testing.T) {
	for _, mode := range []string{"expired", "id", "package-change", "expiry-change", "poc", "auto-renew", "status", "unknown-status", "detail-404", "false-absence", "exact-absence"} {
		t.Run(mode, func(t *testing.T) {
			var sends, reads atomic.Int32
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					sends.Add(1)
					w.WriteHeader(204)
					return
				}
				if r.URL.Path == "/billing-api/v1/synthetic/quota" {
					n := reads.Add(1)
					if sends.Load() > 0 {
						w.WriteHeader(404)
						if mode == "false-absence" {
							_, _ = io.WriteString(w, `{"message":"proxy not found"}`)
						} else {
							testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDeleted.json")
						}
						return
					}
					body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
					switch mode {
					case "expired":
						body = strings.ReplaceAll(body, "2026-12-01", "2026-10-01")
					case "package-change":
						if n > 1 {
							body = strings.ReplaceAll(body, createPackageID, "other")
						}
					case "expiry-change":
						if n > 1 {
							body = strings.ReplaceAll(body, "2026-12-01", "2026-12-02")
						}
					case "poc":
						body = strings.ReplaceAll(body, `"poc": 0`, `"poc": 1`)
					case "auto-renew":
						body = strings.ReplaceAll(body, `"autoRenew": 0`, `"autoRenew": 1`)
					case "status":
						body = strings.ReplaceAll(body, `"ACTIVE"`, `"ERROR"`)
					case "unknown-status":
						body = strings.ReplaceAll(body, `"ACTIVE"`, `"FUTURE"`)
					}
					_, _ = io.WriteString(w, body)
					return
				}
				if mode == "detail-404" {
					w.WriteHeader(404)
					testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaDeleted.json")
					return
				}
				body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreatedDetail.json")
				if mode == "package-change" && reads.Load() > 1 {
					body = strings.ReplaceAll(body, createPackageID, "other")
				}
				_, _ = io.WriteString(w, body)
			})))
			c.now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
			in := &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"}
			if mode == "id" {
				in.ExpectedQuotaID = "other"
			}
			out, err := c.DeleteSyntheticQuota(context.Background(), in)
			if mode == "exact-absence" {
				if err != nil || out.Stage != "settled" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			if err == nil || out == nil {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "false-absence" {
				if !errors.Is(err, dns.ErrNotSettled) || out.Stage != "sent" {
					t.Fatal(err)
				}
			} else if sends.Load() != 0 {
				t.Fatal("sent during preflight refusal")
			}
			if mode == "unknown-status" && !errors.Is(err, ErrUnexpectedStatus) {
				t.Fatal(err)
			}
		})
	}
}

func TestSyntheticDeleteSettlementBound(t *testing.T) {
	var sends, details atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			sends.Add(1)
			w.WriteHeader(204)
		case r.URL.Path == "/billing-api/v1/synthetic/quota":
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaCreated.json")
		case strings.HasSuffix(r.URL.Path, "/quota-detail"):
			details.Add(1)
			testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuotaCreatedDetail.json")
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(400)
		}
	})))
	start := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	now := start
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if d != 2*time.Second {
			t.Errorf("delay=%s", d)
		}
		now = now.Add(d)
		return ctx.Err()
	}
	out, err := c.DeleteSyntheticQuota(context.Background(), &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"})
	if !errors.Is(err, dns.ErrNotSettled) || out.Stage != "sent" || now.Sub(start) != 60*time.Second || sends.Load() != 1 || details.Load() != 2 {
		t.Fatalf("out=%+v err=%v elapsed=%s sends=%d details=%d", out, err, now.Sub(start), sends.Load(), details.Load())
	}
}

func TestSyntheticDeleteFullFinalSnapshot(t *testing.T) {
	for _, field := range []string{"id", "packageId", "endTime", "autoRenew", "poc", "status", "api"} {
		t.Run(field, func(t *testing.T) {
			var reads, sends atomic.Int32
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					sends.Add(1)
					w.WriteHeader(204)
					return
				}
				if r.URL.Path == "/billing-api/v1/synthetic/quota" {
					body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")
					if reads.Add(1) > 1 {
						switch field {
						case "id":
							body = strings.ReplaceAll(body, "quota-syn-2", "replacement")
						case "packageId":
							body = strings.ReplaceAll(body, createPackageID, "replacement")
						case "endTime":
							body = strings.ReplaceAll(body, "2026-12-01", "2026-12-02")
						case "autoRenew":
							body = strings.ReplaceAll(body, `"autoRenew": 0`, `"autoRenew": 1`)
						case "poc":
							body = strings.ReplaceAll(body, `"poc": 0`, `"poc": 1`)
						case "status":
							body = strings.ReplaceAll(body, `"ACTIVE"`, `"ERROR"`)
						}
					}
					_, _ = io.WriteString(w, body)
					return
				}
				body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreatedDetail.json")
				if reads.Load() > 1 {
					switch field {
					case "id":
						body = strings.ReplaceAll(body, "quota-syn-2", "replacement")
					case "packageId":
						body = strings.ReplaceAll(body, createPackageID, "replacement")
					case "api":
						body = strings.ReplaceAll(body, `"api": 10`, `"api": 11`)
					}
				}
				_, _ = io.WriteString(w, body)
			})))
			c.now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
			out, err := c.DeleteSyntheticQuota(context.Background(), &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"})
			if !errors.Is(err, core.ErrInvalidInput) || out.Stage != "prepared" || out.Quota.ID != "quota-syn-2" || sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, sends.Load())
			}
		})
	}
}

func TestSyntheticQuoteDeleteNonEncodablePeerTimes(t *testing.T) {
	for _, field := range []string{"startTime", "endTime", "createdAt", "updatedAt"} {
		t.Run(field, func(t *testing.T) {
			var q map[string]any
			if err := json.Unmarshal([]byte(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaCreated.json")), &q); err != nil {
				t.Fatal(err)
			}
			q[field] = "2501-01-01T00:00:00+24:00"
			raw, _ := json.Marshal(q)
			s := &syntheticCreateServer{quota: func(int32) string { return string(raw) }, send: func(http.ResponseWriter, *http.Request) { t.Error("unexpected mutation") }}
			c := syntheticCreateClient(t, s)
			quote, err := c.QuoteCreateSyntheticQuota(context.Background(), syntheticCreateInput())
			var api *core.APIError
			if quote != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || s.quotes.Load() != 0 {
				t.Fatalf("quote=%+v err=%v", quote, err)
			}
			out, err := c.DeleteSyntheticQuota(context.Background(), &DeleteSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-2"})
			if out == nil || out.Quota != nil || out.Stage != "" || !errors.As(err, &api) || api.Code != "InvalidResponse" || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
