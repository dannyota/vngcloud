package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestLogProjectOrderSentOnce(t *testing.T) {
	for _, status := range []int{307, 308, 401, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var orders atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/log-api/v1/projects":
					_, _ = w.Write([]byte(noExistingLogProjectsPage))
				case "/billing-api/v2/log/quota-class":
					_, _ = w.Write([]byte(logProjectClassesBasicOnly("pkg-1", 10)))
				case "/billing-api/v2/log/prices/created-price":
					_, _ = w.Write([]byte(freeQuoteBody))
				case "/billing-api/v2/log/quotas", "/redirected-order":
					orders.Add(1)
					w.Header().Set("Location", "/redirected-order")
					w.WriteHeader(status)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			source := &fakeTokenSource{}
			client := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{
				HTTPClient: server.Client(), TokenSource: source, RetryCount: 3, RetryInterval: time.Millisecond,
			})))
			out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
			if err == nil || out != nil {
				t.Fatalf("output = %+v, error = %v; want refusal", out, err)
			}
			if orders.Load() != 1 {
				t.Fatalf("order dispatches = %d, want 1", orders.Load())
			}
			if source.count.Load() != 1 {
				t.Fatalf("token acquisitions = %d, want 1", source.count.Load())
			}
			if status == http.StatusUnauthorized && len(source.invalidated) != 1 {
				t.Fatalf("invalidations = %d, want 1", len(source.invalidated))
			}
		})
	}
}

func TestLogProjectCreateRejectsInvalidPrices(t *testing.T) {
	for _, price := range []string{"-1", "1e999", "-1e999", `"NaN"`, `"Infinity"`, "NaN", "Infinity"} {
		t.Run(price, func(t *testing.T) {
			var orders atomic.Int64
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/log-api/v1/projects":
					_, _ = w.Write([]byte(noExistingLogProjectsPage))
				case "/billing-api/v2/log/quota-class":
					_, _ = w.Write([]byte(logProjectClassesBasicOnly("pkg-1", 10)))
				case "/billing-api/v2/log/prices/created-price":
					_, _ = fmt.Fprintf(w, `{"optimumPrice":%s}`, price)
				case "/billing-api/v2/log/quotas":
					orders.Add(1)
					_, _ = w.Write([]byte(`{"orderId":"order-1"}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
			var api *core.APIError
			if !errors.As(err, &api) || out != nil || orders.Load() != 0 {
				t.Fatalf("output = %+v, error = %v, orders = %d; want invalid quote and no order", out, err, orders.Load())
			}
		})
	}
}

func TestLogProjectAmbiguousQuoteSendsNoOrder(t *testing.T) {
	for name, body := range map[string]string{
		"alias":     `{"optimumPrice":1000,"OptimumPrice":0}`,
		"duplicate": `{"optimumPrice":1000,"optimumPrice":0}`,
		"property":  `{"optimumPrice":0,"propertiesPrice":[{"name":"plan","optimumPrice":1000,"OptimumPrice":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var orders atomic.Int64
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/log-api/v1/projects":
					_, _ = w.Write([]byte(noExistingLogProjectsPage))
				case "/billing-api/v2/log/quota-class":
					_, _ = w.Write([]byte(logProjectClassesBasicOnly("pkg-1", 10)))
				case "/billing-api/v2/log/prices/created-price":
					_, _ = w.Write([]byte(body))
				case "/billing-api/v2/log/quotas":
					orders.Add(1)
					_, _ = w.Write([]byte(`{"orderId":"order-1"}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			out, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
			if out != nil || err == nil || orders.Load() != 0 {
				t.Fatalf("output=%+v error=%v orders=%d; want refusal with no order", out, err, orders.Load())
			}
		})
	}
}
