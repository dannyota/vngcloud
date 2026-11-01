package monitor

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// Status combinations and synthetic absence below are synthetic mocks, not
// new live evidence. Successful components use sanitized raw fixtures.
func statusClient(t *testing.T, overrides map[string]func(http.ResponseWriter, *http.Request), calls *[]string) *Client {
	t.Helper()
	fixtures := map[string]string{
		"/billing-api/v1/synthetic/quota":                    "GetSyntheticQuota",
		"/billing-api/v2/synthetic/quota-syn-1/quota-detail": "GetSyntheticQuotaDetailNullable",
		"/billing-api/v1/email/quota":                        "GetNotificationQuotaEmail",
		"/billing-api/v1/email/packages":                     "ListNotificationPackagesEmail",
		"/billing-api/v1/sms/quota":                          "GetNotificationQuotaSMS",
		"/billing-api/v1/sms/packages":                       "ListNotificationPackagesSMS",
	}
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, r.URL.Path)
		if r.Method != "GET" {
			t.Errorf("mutation: %s", r.Method)
		}
		if handler := overrides[r.URL.Path]; handler != nil {
			handler(w, r)
			return
		}
		if fixture := fixtures[r.URL.Path]; fixture != "" {
			testutil.WriteFixture(t, w, "../testdata/monitor/"+fixture+".json")
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
		w.WriteHeader(400)
	}))
	c.now = func() time.Time { return time.Date(2026, 10, 20, 7, 0, 0, 0, time.FixedZone("offset", 7*3600)) }
	return c
}

func TestQuotaStatusSnapshotAndFilter(t *testing.T) {
	for _, kind := range []string{"", "sYnThEtIc", "EMAIL", "sms"} {
		t.Run(kind, func(t *testing.T) {
			var calls []string
			c := statusClient(t, nil, &calls)
			clockCalls := 0
			clock := c.now
			c.now = func() time.Time { clockCalls++; return clock() }
			out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: kind})
			if err != nil {
				t.Fatal(err)
			}
			if !out.Complete || out.AsOf.Location() != time.UTC || clockCalls != 1 {
				t.Fatalf("snapshot=%+v clock=%d", out, clockCalls)
			}
			if kind == "" {
				if len(out.Items) != 3 || out.Items[0].Kind != "Synthetic" || out.Items[1].Kind != "Email" || out.Items[2].Kind != "SMS" || len(calls) != 6 {
					t.Fatalf("out=%+v calls=%v", out, calls)
				}
			} else if len(out.Items) != 1 || len(calls) != 2 {
				t.Fatalf("out=%+v calls=%v", out, calls)
			}
			if kind == "sYnThEtIc" || kind == "" {
				row := out.Items[0]
				if row.ClassName != "Basic" || row.PackageName != "" || row.Quota.PackageID != "32b3743d-94b8-49ab-984d-bd7ec99b46f6" || row.Price == nil || *row.Price != 0 || row.TimeLeftSeconds == nil || *row.TimeLeftSeconds != 510180 || row.AutoRenew == nil || *row.AutoRenew || row.RenewWindowOpen == nil || !*row.RenewWindowOpen || row.NextSafeCommand != "monitor get-synthetic-quota" {
					t.Fatalf("row=%+v", row)
				}
				if !reflect.DeepEqual(calls[:2], []string{"/billing-api/v1/synthetic/quota", "/billing-api/v2/synthetic/quota-syn-1/quota-detail"}) {
					t.Fatalf("calls=%v", calls)
				}
			}
			for _, row := range out.Items {
				if row.StartTime != nil && row.StartTime.Location() != time.UTC {
					t.Fatal("time not UTC")
				}
				if row.Kind != "Synthetic" && (row.Price == nil || row.PackageName == "" || row.RenewWindowOpen != nil) {
					t.Fatalf("notification=%+v", row)
				}
			}
		})
	}
}

func TestQuotaStatusInvalidKind(t *testing.T) {
	var calls []string
	c := statusClient(t, nil, &calls)
	for _, kind := range []string{"Slack", " synthetic", "../sms"} {
		out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: kind})
		if out != nil || !errors.Is(err, core.ErrInvalidInput) || len(calls) != 0 {
			t.Fatalf("out=%+v err=%v calls=%v", out, err, calls)
		}
	}
}

func TestQuotaStatusMissingAndPartial(t *testing.T) {
	for _, mode := range []string{"none", "forbidden", "detail-404", "list-404", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			handlers := map[string]func(http.ResponseWriter, *http.Request){}
			for _, kind := range []string{"synthetic", "email", "sms"} {
				if mode == "none" {
					handlers["/billing-api/v1/"+kind+"/quota"] = func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(404)
						_, _ = w.Write([]byte(`{"message":"synthetic absence mock"}`))
					}
				}
				if mode == "forbidden" && kind != "synthetic" {
					handlers["/billing-api/v1/"+kind+"/quota"] = func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(403)
						_, _ = w.Write([]byte(`{"message":"unsafe peer text"}`))
					}
				}
			}
			if mode == "detail-404" {
				handlers["/billing-api/v2/synthetic/quota-syn-1/quota-detail"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); _, _ = w.Write([]byte(`{}`)) }
			}
			if mode == "list-404" {
				handlers["/billing-api/v1/email/packages"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); _, _ = w.Write([]byte(`{}`)) }
			}
			var calls []string
			c := statusClient(t, handlers, &calls)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			out, err := c.GetQuotaStatus(ctx, nil)
			if out == nil || len(out.Items) != 3 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "none" {
				if err != nil || !out.Complete || len(calls) != 3 {
					t.Fatalf("out=%+v err=%v calls=%v", out, err, calls)
				}
				for _, row := range out.Items {
					if row.Status != "none" || row.Quota != nil || row.Price != nil || row.StartTime != nil || row.TimeLeftSeconds != nil || row.AutoRenew != nil || row.RenewWindowOpen != nil || row.Error != nil {
						t.Fatalf("none=%+v", row)
					}
				}
				return
			}
			if err == nil || !errors.Is(err, ErrQuotaStatusIncomplete) || out.Complete || strings.Contains(err.Error(), "unsafe") {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if mode == "forbidden" {
				if out.Items[0].Status != "ACTIVE" || out.Items[1].Status != "unavailable" || out.Items[2].Error.Code != "Forbidden" || !errors.Is(err, core.ErrPermission) {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			}
			if mode == "detail-404" && out.Items[0].Status != "unavailable" {
				t.Fatal("detail failure hidden as missing quota")
			}
			if mode == "list-404" && out.Items[1].Status != "unavailable" {
				t.Fatal("list failure hidden as missing quota")
			}
			if mode == "canceled" {
				if len(calls) != 0 || !errors.Is(err, context.Canceled) || out.Items[2].Error.Code != "RequestFailed" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.Operation != "monitor.GetQuotaStatus" {
				t.Fatalf("API error=%+v", api)
			}
		})
	}
}

func TestQuotaStatusRenewalBoundaryAndOverflow(t *testing.T) {
	for _, tc := range []struct {
		name, end, status, price string
		seconds                  int64
		known, open              bool
	}{
		{"seven-days", "2026-10-27T00:00:00Z", "ACTIVE", "0", 604800, true, true},
		{"outside", "2026-10-27T00:00:01Z", "ACTIVE", "0", 604801, true, false},
		{"seven-days-plus-half-second", "2026-10-27T00:00:00.5Z", "ACTIVE", "0", 604800, true, false},
		{"seven-days-minus-half-second", "2026-10-26T23:59:59.5Z", "ACTIVE", "0", 604799, true, true},
		{"half-second-before-expiry", "2026-10-20T00:00:00.5Z", "ACTIVE", "0", 0, true, true},
		{"now", "2026-10-20T00:00:00Z", "ACTIVE", "0", 0, true, false},
		{"expired", "2026-10-19T23:59:59Z", "IN-TRASH", "0", -1, true, false},
		{"future-status", "2026-10-21T00:00:00Z", "FUTURE", "0", 86400, true, false},
		{"paid", "2026-10-21T00:00:00Z", "ACTIVE", "1", 86400, false, false},
		{"unknown-price", "2026-10-21T00:00:00Z", "ACTIVE", "null", 86400, false, false},
		{"far-future", "9999-01-01T00:00:00Z", "ACTIVE", "0", 251578310400, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuota.json")
			quota = strings.ReplaceAll(quota, "2026-10-25T21:43:00.000+00:00", tc.end)
			quota = strings.ReplaceAll(quota, `"status": "ACTIVE"`, `"status": "`+tc.status+`"`)
			detail := strings.ReplaceAll(testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json"), `"price": 0.0`, `"price": `+tc.price)
			var calls []string
			c := statusClient(t, map[string]func(http.ResponseWriter, *http.Request){
				"/billing-api/v1/synthetic/quota":                    func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(quota)) },
				"/billing-api/v2/synthetic/quota-syn-1/quota-detail": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(detail)) },
			}, &calls)
			out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: "synthetic"})
			if err != nil {
				t.Fatal(err)
			}
			row := out.Items[0]
			if *row.TimeLeftSeconds != tc.seconds || (row.RenewWindowOpen != nil) != tc.known || (tc.known && *row.RenewWindowOpen != tc.open) || row.Status != tc.status || row.NextSafeCommand != "monitor get-synthetic-quota" {
				t.Fatalf("row=%+v seconds=%d", row, *row.TimeLeftSeconds)
			}
		})
	}
}

func TestQuotaStatusNotificationJoin(t *testing.T) {
	for _, mode := range []string{"unmatched", "duplicate", "unknown-price"} {
		t.Run(mode, func(t *testing.T) {
			body := testutil.FixtureBody(t, "../testdata/monitor/ListNotificationPackagesEmail.json")
			switch mode {
			case "unmatched":
				body = `[]`
			case "duplicate":
				body = strings.TrimSuffix(strings.TrimSpace(body), "]") + "," + strings.TrimSpace(body)[1:]
			case "unknown-price":
				body = strings.ReplaceAll(body, `"price": 0.0`, `"price": null`)
			}
			var calls []string
			c := statusClient(t, map[string]func(http.ResponseWriter, *http.Request){"/billing-api/v1/email/packages": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }}, &calls)
			out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: "email"})
			row := out.Items[0]
			if mode == "duplicate" {
				if !errors.Is(err, ErrQuotaStatusIncomplete) || row.Status != "unavailable" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				return
			}
			if err != nil || row.Price != nil || (mode == "unmatched" && row.PackageName != "") {
				t.Fatalf("row=%+v err=%v", row, err)
			}
		})
	}
}

func TestQuotaStatusResponseLimits(t *testing.T) {
	for _, oversize := range []bool{false, true} {
		t.Run(map[bool]string{false: "six-bounded-components", true: "oversize-detail"}[oversize], func(t *testing.T) {
			handlers := map[string]func(http.ResponseWriter, *http.Request){}
			fixtures := map[string]string{"/billing-api/v1/synthetic/quota": "GetSyntheticQuota", "/billing-api/v2/synthetic/quota-syn-1/quota-detail": "GetSyntheticQuotaDetailNullable", "/billing-api/v1/email/quota": "GetNotificationQuotaEmail", "/billing-api/v1/email/packages": "ListNotificationPackagesEmail", "/billing-api/v1/sms/quota": "GetNotificationQuotaSMS", "/billing-api/v1/sms/packages": "ListNotificationPackagesSMS"}
			for path, name := range fixtures {
				body := testutil.FixtureBody(t, "../testdata/monitor/"+name+".json")
				size := quotaMaxBody
				if oversize && strings.HasSuffix(path, "/quota-detail") {
					size++
				}
				body += strings.Repeat(" ", size-len(body))
				handlers[path] = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }
			}
			var calls []string
			c := statusClient(t, handlers, &calls)
			out, err := c.GetQuotaStatus(context.Background(), nil)
			if len(calls) != 6 {
				t.Fatalf("calls=%v", calls)
			}
			if oversize {
				if !errors.Is(err, ErrQuotaStatusIncomplete) || out.Items[0].Error.Code != "ResponseTooLarge" || out.Items[1].Status != "ACTIVE" {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			} else if err != nil || !out.Complete {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestQuotaStatusFractionalSeconds(t *testing.T) {
	// Independent expected values check truncation on either side of zero.
	for _, tc := range []struct {
		end, now string
		want     int64
	}{
		{"2026-01-01T00:00:01.1Z", "2026-01-01T00:00:00.9Z", 0},
		{"2026-01-01T00:00:00.9Z", "2026-01-01T00:00:01.1Z", 0},
		{"2026-01-01T00:00:02.1Z", "2026-01-01T00:00:00.9Z", 1},
		{"2026-01-01T00:00:00.9Z", "2026-01-01T00:00:02.1Z", -1},
	} {
		end, err := time.Parse(time.RFC3339, tc.end)
		if err != nil {
			t.Fatal(err)
		}
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if got := quotaSecondsLeft(end, now); got != tc.want {
			t.Fatalf("end=%s now=%s got=%d want=%d", tc.end, tc.now, got, tc.want)
		}
	}
}

func TestQuotaStatusCancellationBetweenComponents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []string
	c := statusClient(t, map[string]func(http.ResponseWriter, *http.Request){"/billing-api/v1/synthetic/quota": func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/monitor/GetSyntheticQuota.json")
		cancel()
	}}, &calls)
	out, err := c.GetQuotaStatus(ctx, nil)
	if !errors.Is(err, ErrQuotaStatusIncomplete) || !errors.Is(err, context.Canceled) || len(calls) != 1 || out.Items[1].Error.Code != "RequestFailed" || out.Items[2].Error.Status != 0 {
		t.Fatalf("out=%+v err=%v calls=%v", out, err, calls)
	}
}

func TestQuotaStatusReadRetries(t *testing.T) {
	calls := 0
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"message":"peer"}`))
	})))
	out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: "synthetic"})
	if !errors.Is(err, ErrQuotaStatusIncomplete) || out.Items[0].Error.Code != "ServerError" || calls != 4 {
		t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
	}
}

func TestQuotaStatusDetailValidation(t *testing.T) {
	for _, mode := range []string{"resource", "package", "missing-tier", "negative-limit"} {
		t.Run(mode, func(t *testing.T) {
			body := testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
			switch mode {
			case "resource":
				body = strings.ReplaceAll(body, `"resourceId": "quota-syn-1"`, `"resourceId": "other"`)
			case "package":
				body = strings.ReplaceAll(body, `"packageId": "32b3743d-94b8-49ab-984d-bd7ec99b46f6"`, `"packageId": "other"`)
			case "missing-tier":
				body = strings.ReplaceAll(body, `"tierName": null,`, ``)
			case "negative-limit":
				body = strings.ReplaceAll(body, `"email": 0`, `"email": -1`)
			}
			var calls []string
			c := statusClient(t, map[string]func(http.ResponseWriter, *http.Request){"/billing-api/v2/synthetic/quota-syn-1/quota-detail": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }}, &calls)
			out, err := c.GetQuotaStatus(context.Background(), &GetQuotaStatusInput{Kind: "synthetic"})
			if !errors.Is(err, ErrQuotaStatusIncomplete) || out.Items[0].Status != "unavailable" || out.Items[0].Quota != nil || out.Items[0].Error.Code != "InvalidResponse" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
