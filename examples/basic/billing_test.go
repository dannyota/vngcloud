package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestShowBillingHistoryOffline(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "error"}[fail], func(t *testing.T) {
			raw := newRawCaptureStore()
			seen := map[string]string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("billing example issued a write")
				}
				if strings.HasSuffix(r.URL.Path, "/transactions") || strings.HasSuffix(r.URL.Path, "/payments") {
					q := r.URL.Query()
					start, e1 := time.Parse("02/01/2006", q.Get("dateStart"))
					end, e2 := time.Parse("02/01/2006", q.Get("dateEnd"))
					if e1 != nil || e2 != nil || !start.AddDate(0, 0, 30).Equal(end) || q.Get("pageNumber") != "1" || q.Get("pageSize") != "10" || len(q) != 4 {
						t.Error("example history range or page unsafe")
					}
					seen[r.URL.Path] = q.Get("dateStart") + q.Get("dateEnd")
					if fail {
						w.WriteHeader(403)
						_, _ = w.Write([]byte(`{"message":"synthetic-account"}`))
						return
					}
					_, _ = w.Write([]byte(`{"code":200,"data":{"content":[],"pageable":{"number":1,"size":10,"totalPages":0,"totalElements":0}}}`))
					return
				}
				_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
			}))
			defer server.Close()
			tc := transport.New(transport.Config{HTTPClient: server.Client(), Capture: func(c transport.Capture) {
				raw.add("synthetic", "", vngcloud.ResponseCapture{Operation: c.Operation, Method: c.Method, URL: c.URL, StatusCode: c.StatusCode, Body: c.Body})
			}})
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Billing: server.URL + "/"}, tc)
			store := newSDKOutputStore()
			showBilling(context.Background(), cfg, store)
			if len(seen) != 2 || seen["/gateway/api/v1/transactions"] != seen["/gateway/api/v1/payments"] {
				t.Fatal("example omitted history reads or changed range")
			}
			for _, path := range []string{"billing/transaction", "billing/payment"} {
				if raw.resources[path] == nil || len(raw.resources[path].Regions) != 1 {
					t.Fatal("example raw history capture missing")
				}
				output := store.resources[path]
				if output == nil || len(output.Regions) != 1 {
					t.Fatal("example SDK history output missing")
				}
				item := output.Regions[0]
				if item.Scope != "account" || item.Region != "" || item.ProjectSelected || strings.Contains(item.Error, "synthetic-account") {
					t.Fatal("example history scope or error unsafe")
				}
				if fail && (item.Error == "" || item.Items != nil || item.Count != 0) {
					t.Fatal("example retained an error result")
				}
				if !fail && item.Error != "" {
					t.Fatal("empty history example failed")
				}
			}
		})
	}
}
