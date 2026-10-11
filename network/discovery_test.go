package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func networkDiscoveryContext() context.Context {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	return discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
}

func TestListNetworkPricesFixturesAndBodies(t *testing.T) {
	calls := []string{}
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/price" || r.URL.RawQuery != "" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL)
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		info, _ := body["resourceInfo"].(map[string]any)
		kind, _ := info["type"].(string)
		want := map[string]any{"resourceType": "public-vip", "action": "create", "resourceInfo": map[string]any{"type": kind, "isBuyMorePo": false, "isPoc": false}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body %+v", body)
		}
		calls = append(calls, kind)
		file := map[string]string{"public": "discovery_quote_public.json", "public-vm": "discovery_quote_public_vm.json", "public-mkp": "discovery_quote_public_mkp.json"}[kind]
		testutil.WriteFixture(t, w, "../testdata/network/"+file)
	})))
	out, err := c.ListNetworkPrices(networkDiscoveryContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 6 || out.Metadata.Requests != 3 || !out.Metadata.Complete || out.Metadata.QuotedAt.Location() != time.UTC {
		t.Fatalf("output %+v", out)
	}
	if !reflect.DeepEqual(calls, []string{"public", "public-vm", "public-mkp"}) {
		t.Fatalf("quotes %v", calls)
	}
	types := []string{}
	for _, row := range out.Items {
		if row.Unit != "IP-month" {
			t.Fatalf("row %+v", row)
		}
		if row.Kind == "public-vip" {
			types = append(types, row.Type)
			if row.Price.MonthlyPrice == nil || *row.Price.MonthlyPrice != 120000 || row.Price.DiscountPercent == nil {
				t.Fatalf("price %+v", row)
			}
		} else if row.Price.Status != "unknown" || row.Price.MonthlyPrice != nil {
			t.Fatalf("unknown %+v", row)
		}
	}
	if !reflect.DeepEqual(types, []string{"public", "public-mkp", "public-vm"}) {
		t.Fatalf("order %v", types)
	}
}

func TestListNetworkPricesValidationBeforeReads(t *testing.T) {
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })))
	for _, max := range []int{-1, 2001} {
		out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{MaxRequests: max})
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("output %v error %v", out, err)
		}
	}
}

func TestListNetworkPricesMalformedPrices(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"optimumPrice":null}`, `{"optimumPrice":0}`, `{"optimumPrice":-1}`, `{"optimumPrice":1e400}`, `{"optimumPrice":"1"}`, `{"optimumPrice":1,"discountPercent":-1}`, `{"data":{"optimumPrice":1}}`, `{`} {
		t.Run(body, func(t *testing.T) {
			c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })))
			for _, partial := range []bool{false, true} {
				out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{AllowPartial: partial})
				var api *vngcloud.APIError
				if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 {
					t.Fatalf("output %v error %v", out, err)
				}
			}
		})
	}
}

func TestListNetworkPricesOptionalPricePresence(t *testing.T) {
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"optimumPrice":1}`)) })))
	out, err := c.ListNetworkPrices(networkDiscoveryContext(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range out.Items {
		if row.Kind == "public-vip" && (row.Price.OriginalPrice != nil || row.Price.DiscountPrice != nil || row.Price.DiscountPercent != nil) {
			t.Fatalf("row %+v", row)
		}
	}
}

func TestListNetworkPricesBudgetAndFreshCalls(t *testing.T) {
	calls := 0
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
	})))
	for _, partial := range []bool{false, true} {
		out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{MaxRequests: 2, AllowPartial: partial})
		if out != nil || !errors.Is(err, discovery.ErrLimit) || calls != 0 {
			t.Fatalf("output %v error %v calls %d", out, err, calls)
		}
	}
	for _, max := range []int{0, 3, 2000} {
		out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{MaxRequests: max})
		if err != nil || out.Metadata.Requests != 3 {
			t.Fatalf("output %v error %v", out, err)
		}
	}
	if calls != 9 {
		t.Fatalf("persistent quote cache: %d", calls)
	}
}

func TestListNetworkPricesPartialIdentityAndSDKOrder(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502, 503, 504} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", status, partial), func(t *testing.T) {
				calls := 0
				c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if calls == 2 {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"code":"synthetic","message":"refused"}`))
						return
					}
					testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
				})))
				out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{AllowPartial: partial})
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("error %v", err)
				}
				if !partial {
					if out != nil || calls != 2 || errors.Is(err, pricing.ErrDiscoveryIncomplete) {
						t.Fatalf("output %v error %v calls %d", out, err, calls)
					}
					return
				}
				if out == nil || out.Metadata.Complete || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || out.Metadata.Failures[0].ItemKey != "public-vm" {
					t.Fatalf("output %+v error %v", out, err)
				}
				want := []string{"public", "public-mkp", "public-vm"}
				if status == 401 || status == 429 {
					want = []string{"public", "public-vm"}
				}
				got := []string{}
				for _, row := range out.Items {
					if row.Kind == "public-vip" {
						got = append(got, row.Type)
						if row.Type == "public-vm" && (row.Price.Status != "failed" || row.Price.MonthlyPrice != nil) {
							t.Fatalf("row %+v", row)
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("order %v want %v", got, want)
				}
			})
		}
	}
}

func TestListNetworkPricesProjectBudgetAndCache(t *testing.T) {
	calls, quotes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/v1/projects" {
			testutil.WriteFixture(t, w, "../testdata/project/list_projects.json")
			return
		}
		if r.URL.Path != "/v1/price" {
			t.Errorf("route %s", r.URL.Path)
		}
		quotes++
		testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, Portal: server.URL}), vngcloud.WithRetry(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{MaxRequests: 3})
	if out != nil || !errors.Is(err, discovery.ErrLimit) || calls != 1 || quotes != 0 {
		t.Fatalf("output %v error %v calls %d", out, err, calls)
	}
	out, err = c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{MaxRequests: 3})
	if err != nil || out.Metadata.Requests != 3 || calls != 4 {
		t.Fatalf("output %v error %v calls %d", out, err, calls)
	}
}

func TestListNetworkPricesPortalOverrideNoFallback(t *testing.T) {
	for _, status := range []int{200, 404} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/custom/base/v1/price" {
					t.Errorf("route %s", r.URL.Path)
				}
				w.WriteHeader(status)
				testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
			}))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithStaticToken("synthetic"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Portal: server.URL + "/custom/base", VServer: server.URL + "/forbidden"}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			out, err := New(cfg).ListNetworkPrices(networkDiscoveryContext(), nil)
			if status == 200 {
				if err != nil || out.Metadata.Requests != 3 || calls != 3 {
					t.Fatalf("output %v error %v calls %d", out, err, calls)
				}
			} else if out != nil || err == nil || calls != 1 {
				t.Fatalf("output %v error %v calls %d", out, err, calls)
			}
		})
	}
}

func TestListNetworkPricesRetryExhaustion(t *testing.T) {
	for _, status := range []int{429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":"failure"}`))
			})))
			out, err := c.ListNetworkPrices(networkDiscoveryContext(), &ListNetworkPricesInput{AllowPartial: true})
			var api *vngcloud.APIError
			want := 12
			if status == 429 {
				want = 4
			}
			if status == 500 {
				want = 3
			}
			if out != nil || !errors.As(err, &api) || api.StatusCode != status || calls != want {
				t.Fatalf("output %v error %v calls %d want %d", out, err, calls, want)
			}
		})
	}
}

func TestListNetworkPricesPacingAndDeadline(t *testing.T) {
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.FixedZone("test", 3600))
	starts := []time.Time{}
	ctx := discovery.WithTestClock(context.Background(), func() time.Time { return now }, func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() })
	ctx = discovery.WithTestContextObserver(ctx, func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Minute || time.Until(deadline) < 9*time.Minute {
			t.Errorf("deadline %v", deadline)
		}
	})
	c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		starts = append(starts, now)
		testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
	})))
	out, err := c.ListNetworkPrices(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 500*time.Millisecond {
			t.Fatalf("starts %v", starts)
		}
	}
	if out.Metadata.QuotedAt != now.UTC() {
		t.Fatalf("completion %v want %v", out.Metadata.QuotedAt, now.UTC())
	}
}

func TestListNetworkPricesCancellationStopsScheduling(t *testing.T) {
	for _, partial := range []bool{false, true} {
		ctx, cancel := context.WithCancel(networkDiscoveryContext())
		calls := 0
		c := New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 2 {
				cancel()
			}
			testutil.WriteFixture(t, w, "../testdata/network/discovery_quote_public.json")
		})))
		out, err := c.ListNetworkPrices(ctx, &ListNetworkPricesInput{AllowPartial: partial})
		cancel()
		if !errors.Is(err, context.Canceled) || calls != 2 {
			t.Fatalf("output %v error %v calls %d", out, err, calls)
		}
		if partial {
			if out == nil || !errors.Is(err, pricing.ErrDiscoveryIncomplete) {
				t.Fatalf("output %v error %v", out, err)
			}
		} else if out != nil {
			t.Fatalf("output %v", out)
		}
	}
}
