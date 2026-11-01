package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestRecoverSyntheticQuotaOnceOutcomes(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504, 301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var sends atomic.Int32
			reads := &lifecycleServer{kind: "synthetic", quota: func(int32) string {
				return testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaInTrash.json")
			}}
			handler := reads.handler(t)
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/billing-api/v2/trash/synthetic/quota" {
					sends.Add(1)
					raw, _ := io.ReadAll(r.Body)
					if string(raw) != `{"redirectUrl":"https://vmonitor.console.vngcloud.vn/quota-usages/synthetic-test","monthPeriod":1,"pay":true}` {
						t.Errorf("body=%s", raw)
					}
					w.Header().Set("Location", "/must-not-follow")
					w.WriteHeader(status)
					if status != 200 {
						_, _ = io.WriteString(w, `{"message":"private-canary"}`)
					}
					return
				}
				handler(w, r)
			})))
			c.now = func() time.Time { return time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC) }
			out, err := c.RecoverSyntheticQuota(context.Background(), &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1", NoWait: true})
			if out == nil || out.Sent != "yes" || out.Recovered || out.Quota != nil || sends.Load() != 1 {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, sends.Load())
			}
			if status == 200 {
				if err != nil || out.Stage != "accepted" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.StatusCode != status || strings.Contains(err.Error(), "private-canary") {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			uncertain := status < 400 || status >= 500
			if errors.Is(err, dns.ErrNotSettled) != uncertain {
				t.Fatalf("err=%v", err)
			}
			if uncertain && (out.Stage != "unconfirmed" || out.Error.Code != "NotSettled") {
				t.Fatalf("out=%+v", out)
			}
		})
	}
}

func TestRecoverSyntheticQuotaEligibilityAndFinalTerms(t *testing.T) {
	for _, tc := range []struct {
		name        string
		quotaField  string
		value       any
		detailField string
		detailValue any
		final       bool
		code        string
	}{
		{name: "exact now", quotaField: "endTime", value: "2026-11-01T06:00:00Z", code: "InvalidUsage"},
		{name: "active", quotaField: "status", value: "ACTIVE", code: "InvalidUsage"},
		{name: "unknown", quotaField: "status", value: "UNKNOWN", code: "UnexpectedStatus"},
		{name: "poc", quotaField: "poc", value: 1, code: "InvalidUsage"},
		{name: "auto renew", quotaField: "autoRenew", value: 1, code: "InvalidUsage"},
		{name: "wrong package", detailField: "packageId", detailValue: "other", code: "InvalidResponse"},
		{name: "wrong class", detailField: "className", detailValue: "Pro", code: "InvalidUsage"},
		{name: "wrong location", detailField: "location", detailValue: 2, code: "InvalidUsage"},
		{name: "wrong capacity", detailField: "api", detailValue: 20, code: "InvalidUsage"},
		{name: "missing price", detailField: "price", detailValue: nil, code: "InvalidResponse"},
		{name: "unpriced", detailField: "price", detailValue: 10, code: "Unpriced"},
		{name: "final class", detailField: "className", detailValue: "Pro", final: true, code: "QuotaChanged"},
		{name: "final location", detailField: "location", detailValue: 2, final: true, code: "QuotaChanged"},
		{name: "final capacity", detailField: "api", detailValue: 20, final: true, code: "QuotaChanged"},
		{name: "final missing price", detailField: "price", detailValue: nil, final: true, code: "InvalidResponse"},
		{name: "final price", detailField: "price", detailValue: 1, final: true, code: "QuotaChanged"},
		{name: "final flags", quotaField: "autoRenew", value: 1, final: true, code: "QuotaChanged"},
		{name: "final expiry", quotaField: "endTime", value: "2026-10-26T06:00:00Z", final: true, code: "QuotaChanged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutate := func(raw string, field string, value any) string {
				var obj map[string]any
				if err := json.Unmarshal([]byte(raw), &obj); err != nil {
					t.Fatal(err)
				}
				obj[field] = value
				b, err := json.Marshal(obj)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			s := &syntheticOrderServer{recovery: true, send: func(http.ResponseWriter, *http.Request) { t.Fatal("unexpected mutation") }}
			s.quota = func(n int32) string {
				raw := syntheticFixture(t, true)
				if tc.quotaField != "" && (!tc.final || n == 2) {
					return mutate(raw, tc.quotaField, tc.value)
				}
				return raw
			}
			s.detail = func(n int32) string {
				raw := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
				if tc.detailField != "" && (!tc.final || n == 2) {
					return mutate(raw, tc.detailField, tc.detailValue)
				}
				return raw
			}
			c := syntheticTestClient(t, s)
			out, err := c.RecoverSyntheticQuota(context.Background(), &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1", MaxPrice: 100})
			if err == nil || out.Sent != "no" || out.Stage != "preflight" || out.Error.Code != tc.code || s.sends.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestSyntheticAutoRenewReconcilesMismatchedOpenRecovery(t *testing.T) {
	s := &syntheticOrderServer{recovery: true, quota: func(int32) string { return syntheticFixture(t, true) }, send: func(http.ResponseWriter, *http.Request) {}}
	c := syntheticTestClient(t, s)
	_, err := c.RecoverSyntheticQuota(context.Background(), &RecoverSyntheticQuotaInput{ExpectedQuotaID: "quota-syn-1", NoWait: true})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	_, err = c.SetSyntheticQuotaAutoRenew(context.Background(), &SetSyntheticQuotaAutoRenewInput{Enabled: &on, ExpectedQuotaID: "other"})
	if !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, ErrQuotaChanged) || s.sends.Load() != 1 || len(c.quotaAttempts) != 1 {
		t.Fatalf("err=%v sends=%d", err, s.sends.Load())
	}
}
