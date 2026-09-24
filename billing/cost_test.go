package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestGetCostOverview(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v2/cost-explorer/overview" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("startDate") != "2026-09-01" || q.Get("endDate") != "2026-09-02" {
			t.Fatalf("date query = %s", r.URL.RawQuery)
		}
		if q.Get("groupBy") != "product" || q.Get("interval") != "daily" {
			t.Fatalf("expected defaults, got query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetCostOverview.json")
	}))

	out, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{
		StartDate: "2026-09-01",
		EndDate:   "2026-09-02",
	})
	if err != nil {
		t.Fatalf("GetCostOverview() error = %v", err)
	}
	if out.Summary.CurrentCost != 4200000 || len(out.Summary.Breakdown) != 2 {
		t.Fatalf("unexpected summary: %+v", out.Summary)
	}
	if len(out.Series) != 2 || out.Series[0].Date != "2026-09-01" || out.Series[0].Cost != 165000 {
		t.Fatalf("unexpected series: %+v", out.Series)
	}
	if out.Interval != "daily" || out.GroupBy != "product" {
		t.Fatalf("unexpected fields: interval=%q groupBy=%q", out.Interval, out.GroupBy)
	}
}

func TestGetCostOverviewCustomGroupByAndInterval(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("groupBy") != "resourceType" || q.Get("interval") != "hourly" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		if q.Get("product") != "compute" || q.Get("resourceType") != "server" || q.Get("resourceId") != "server-1" || q.Get("q") != "web" {
			t.Fatalf("filter query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetCostOverview.json")
	}))

	_, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{
		StartDate:    "2026-09-01",
		EndDate:      "2026-09-02",
		Product:      "compute",
		ResourceType: "server",
		ResourceID:   "server-1",
		Query:        "web",
		GroupBy:      "resourceType",
		Interval:     "hourly",
	})
	if err != nil {
		t.Fatalf("GetCostOverview() error = %v", err)
	}
}

func TestListCostResources(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/api/v2/cost-explorer/resources" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("startDate") != "2026-09-01" || q.Get("endDate") != "2026-09-02" {
			t.Fatalf("date query = %s", r.URL.RawQuery)
		}
		if q.Get("page") != "1" || q.Get("size") != "50" || q.Get("sort") != "cost" || q.Get("order") != "desc" {
			t.Fatalf("page query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/ListCostResources.json")
	}))

	out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
		StartDate: "2026-09-01",
		EndDate:   "2026-09-02",
		Page:      1,
		Size:      50,
		Sort:      "cost",
		Order:     "desc",
	})
	if err != nil {
		t.Fatalf("ListCostResources() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].ResourceID != "server-1" {
		t.Fatalf("unexpected items: %+v", out.Items)
	}
	if out.Page != 1 || out.PageSize != 200 || out.TotalItem != 2 || out.TotalPage != 1 {
		t.Fatalf("unexpected paging: %+v", out.PagedList)
	}
	if out.Summary.TotalAll != 340000 || out.Summary.ActiveCount != 2 {
		t.Fatalf("unexpected summary: %+v", out.Summary)
	}
}

func TestCostResourcesSizeCap(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"zero", 0},
		{"above cap", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("size"); got != "200" {
					t.Fatalf("size = %q, want 200", got)
				}
				testutil.WriteFixture(t, w, "../testdata/billing/ListCostResources.json")
			}))
			_, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
				StartDate: "2026-09-01",
				EndDate:   "2026-09-02",
				Size:      tc.size,
			})
			if err != nil {
				t.Fatalf("ListCostResources() error = %v", err)
			}
		})
	}
}

func TestGetBalances(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/navbar/balances/v1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetBalances.json")
	}))

	out, err := client.GetBalances(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalances() error = %v", err)
	}
	if out.Balances.Cash == nil || *out.Balances.Cash != 1000000 {
		t.Fatalf("unexpected Cash: %v", out.Balances.Cash)
	}
	if out.Balances.POC != nil {
		t.Fatalf("expected nil POC, got %v", *out.Balances.POC)
	}
	if out.Balances.CashAvailable == nil || *out.Balances.CashAvailable != 950000 {
		t.Fatalf("unexpected CashAvailable: %v", out.Balances.CashAvailable)
	}
	if out.Balances.CashHolding == nil || *out.Balances.CashHolding != 50000 {
		t.Fatalf("unexpected CashHolding: %v", out.Balances.CashHolding)
	}
	if out.Balances.POCHolding != nil {
		t.Fatalf("expected nil POCHolding, got %v", *out.Balances.POCHolding)
	}
}

func TestBalancesNumericString(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/billing/GetBalances.json")
	}))

	out, err := client.GetBalances(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalances() error = %v", err)
	}
	if out.Balances.Cash == nil || *out.Balances.Cash != 1000000 {
		t.Fatalf("expected numeric string cash to decode to 1000000, got %v", out.Balances.Cash)
	}
}

func TestBalancesUnenveloped(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/billing/GetBalancesUnenveloped.json")
	}))

	out, err := client.GetBalances(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetBalances() error = %v", err)
	}
	if out.Balances.Cash == nil || *out.Balances.Cash != 1000000 {
		t.Fatalf("unexpected Cash: %v", out.Balances.Cash)
	}
	if out.Balances.CashAvailable == nil || *out.Balances.CashAvailable != 950000 {
		t.Fatalf("unexpected CashAvailable: %v", out.Balances.CashAvailable)
	}
}

func TestCostRequiredAndDates(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	t.Run("missing dates", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.GetCostOverview(context.Background(), nil)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("bad start date", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{
			StartDate: "09-01-2026",
			EndDate:   "2026-09-02",
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("bad end date for resources", func(t *testing.T) {
		client := newTestClient(t, failIfCalled)
		_, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
			StartDate: "2026-09-01",
			EndDate:   "not-a-date",
		})
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}
