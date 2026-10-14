package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
		if q.Get("page") != "0" || q.Get("size") != "50" || q.Get("sort") != "cost" || q.Get("order") != "desc" {
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
	if out.Items[0].Cost != 250000 || out.Items[1].Cost != 90000 {
		t.Fatalf("resource costs = [%v %v], want [250000 90000]", out.Items[0].Cost, out.Items[1].Cost)
	}
	if out.Items[0].DailyAverage != 125000.5 || out.Items[1].DailyAverage != 45000.25 {
		t.Fatalf("daily averages = [%v %v], want [125000.5 45000.25]", out.Items[0].DailyAverage, out.Items[1].DailyAverage)
	}
	if out.Items[0].StartDate != "2026-09-01" || out.Items[1].StartDate != "2026-09-02" {
		t.Fatalf("resource start dates = [%q %q], want [2026-09-01 2026-09-02]", out.Items[0].StartDate, out.Items[1].StartDate)
	}
	if out.Page != 1 || out.PageSize != 200 || out.TotalItem != 2 || out.TotalPage != 1 {
		t.Fatalf("unexpected paging: %+v", out.PagedList)
	}
	if out.Summary.TotalAll != 340000 || out.Summary.ActiveCount != 2 {
		t.Fatalf("unexpected summary: %+v", out.Summary)
	}
}

func TestListCostResourcesIgnoresLegacyCost(t *testing.T) {
	cases := []struct {
		name       string
		costFields string
		wantCost   float64
	}{
		{"totalCost wins", `"totalCost":12.5,"cost":99`, 12.5},
		{"zero totalCost", `"totalCost":0,"cost":99`, 0},
		{"missing totalCost", `"cost":99`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":200,"message":"success","data":{"items":[{"dailyAvg":1,"product":"compute","region":"global","resourceId":"server-1","resourceName":"web-1","resourceType":"server","startDate":"2026-09-01","userId":123456789,%s}],"pagination":{"page":0,"size":200,"total":1,"totalPages":1},"summary":{"totalAll":%g,"activeCount":1,"avgPerResource":%g}}}`, tc.costFields, tc.wantCost, tc.wantCost)
			}))
			out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
				StartDate: "2026-09-01",
				EndDate:   "2026-09-02",
			})
			if err != nil {
				t.Fatalf("ListCostResources() error = %v", err)
			}
			if len(out.Items) != 1 {
				t.Fatalf("items = %d, want 1", len(out.Items))
			}
			if got := out.Items[0].Cost; got != tc.wantCost {
				t.Fatalf("Cost = %v, want %v from totalCost only", got, tc.wantCost)
			}
		})
	}
}

func TestListCostResourcesDoesNotExposeAccountData(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/billing/ListCostResources.json")
	}))
	out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
		StartDate: "2026-09-01",
		EndDate:   "2026-09-02",
	})
	if err != nil {
		t.Fatalf("ListCostResources() error = %v", err)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, value := range []string{"123456789", `"userId"`, `"UserID"`, `"region"`, `"Region"`, `"global"`} {
		if strings.Contains(string(encoded), value) {
			t.Errorf("output exposes excluded value %q", value)
		}
	}
}

func TestListCostResourcesSinglePageContainsItems(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Errorf("invalid page query: %s", r.URL.RawQuery)
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		if page == 0 {
			testutil.WriteFixture(t, w, "../testdata/billing/ListCostResources.json")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"code":200,"message":"success","data":{"items":[],"pagination":{"page":%d,"size":200,"total":2,"totalPages":1},"summary":{"totalAll":340000,"activeCount":2,"avgPerResource":170000}}}`, page)
	}))

	out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
		StartDate: "2026-09-01",
		EndDate:   "2026-09-02",
	})
	if err != nil {
		t.Fatalf("ListCostResources() error = %v", err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d, want 2 on the only page; TotalItem = %d, TotalPage = %d", len(out.Items), out.TotalItem, out.TotalPage)
	}
	if out.Page != 1 || out.PageSize != 200 || out.TotalItem != 2 || out.TotalPage != 1 {
		t.Fatalf("unexpected paging: %+v", out.PagedList)
	}
}

func TestListCostResourcesPageConversion(t *testing.T) {
	cases := []struct {
		name         string
		page         int
		wantWirePage string
		responsePage int
		wantPage     int
	}{
		{"negative defaults to first page", -3, "0", 0, 1},
		{"zero defaults to first page", 0, "0", 0, 1},
		{"first page", 1, "0", 0, 1},
		{"second page", 2, "1", 1, 2},
		{"returned page metadata", 3, "2", 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.URL.Query().Get("page"); got != tc.wantWirePage {
					t.Errorf("wire page = %q, want %q", got, tc.wantWirePage)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":200,"message":"success","data":{"items":[],"pagination":{"page":%d,"size":2,"total":5,"totalPages":3},"summary":{"totalAll":15,"activeCount":5,"avgPerResource":3}}}`, tc.responsePage)
			}))

			out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
				StartDate: "2026-09-01",
				EndDate:   "2026-09-02",
				Page:      tc.page,
				Size:      50,
			})
			if err != nil {
				t.Fatalf("ListCostResources() error = %v", err)
			}
			if out.Page != tc.wantPage || out.PageSize != 2 || out.TotalItem != 5 || out.TotalPage != 3 {
				t.Fatalf("paging = %+v, want Page %d, PageSize 2, TotalItem 5, TotalPage 3", out.PagedList, tc.wantPage)
			}
			if out.Summary.TotalAll != 15 || out.Summary.ActiveCount != 5 || out.Summary.AvgPerResource != 3 {
				t.Fatalf("unexpected summary: %+v", out.Summary)
			}
		})
	}
}

func TestListCostResourcesWirePageBounds(t *testing.T) {
	cases := []struct {
		name     string
		wirePage int
		wantPage int
		wantErr  bool
	}{
		{"negative wire page", -1, 0, true},
		{"first wire page", 0, 1, false},
		{"overflowing wire page", math.MaxInt, 0, true},
		{"last representable wire page", math.MaxInt - 1, math.MaxInt, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"code":200,"message":"response-body-marker","data":{"items":[{"dailyAvg":617,"product":"compute","region":"region-body-marker","resourceId":"resource-body-marker","resourceName":"name-body-marker","resourceType":"server","startDate":"2026-09-01","totalCost":1234,"userId":123456789}],"pagination":{"page":%d,"size":1,"total":1,"totalPages":1},"summary":{"totalAll":1234,"activeCount":1,"avgPerResource":1234}}}`, tc.wirePage)
			}))
			out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
				StartDate: "2026-09-01",
				EndDate:   "2026-09-02",
			})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("ListCostResources() error = %v", err)
				}
				if out == nil || out.Page != tc.wantPage || len(out.Items) != 1 {
					t.Fatalf("output = %+v, want public page %d with one item", out, tc.wantPage)
				}
				return
			}
			if err == nil {
				if out == nil {
					t.Fatal("ListCostResources() returned no error, want InvalidResponse")
				}
				t.Fatalf("wire page %d returned public page %d with %d items, want InvalidResponse and no output", tc.wirePage, out.Page, len(out.Items))
			}
			if out != nil {
				t.Errorf("output = %+v, want nil for an invalid wire page", out)
			}
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *vngcloud.APIError", err)
			}
			if apiErr.Operation != "billing.ListCostResources" || apiErr.StatusCode != http.StatusOK || apiErr.Code != "InvalidResponse" {
				t.Errorf("APIError = %+v, want ListCostResources InvalidResponse with status 200", apiErr)
			}
			if apiErr.Err != nil || apiErr.Retryable {
				t.Errorf("invalid-response error has a cause or is retryable: %+v", apiErr)
			}
			for _, marker := range []string{"response-body-marker", "region-body-marker", "resource-body-marker", "name-body-marker", "123456789", "1234"} {
				if strings.Contains(apiErr.Message, marker) || strings.Contains(err.Error(), marker) {
					t.Errorf("invalid-response error exposes response value %q", marker)
				}
			}
		})
	}
}

func TestListCostResourcesAllPages(t *testing.T) {
	pages := []string{
		`{"code":200,"message":"success","data":{"items":[{"dailyAvg":0.5,"product":"compute","region":"global","resourceId":"server-1","resourceName":"web-1","resourceType":"server","startDate":"2026-09-01","totalCost":1,"userId":123456789},{"dailyAvg":1,"product":"volume","region":"global","resourceId":"vol-1","resourceName":"data-1","resourceType":"volume","startDate":"2026-09-01","totalCost":2,"userId":123456789}],"pagination":{"page":0,"size":2,"total":5,"totalPages":3},"summary":{"totalAll":15,"activeCount":5,"avgPerResource":3}}}`,
		`{"code":200,"message":"success","data":{"items":[{"dailyAvg":1.5,"product":"compute","region":"global","resourceId":"server-2","resourceName":"web-2","resourceType":"server","startDate":"2026-09-01","totalCost":3,"userId":123456789},{"dailyAvg":2,"product":"volume","region":"global","resourceId":"vol-2","resourceName":"data-2","resourceType":"volume","startDate":"2026-09-01","totalCost":4,"userId":123456789}],"pagination":{"page":1,"size":2,"total":5,"totalPages":3},"summary":{"totalAll":15,"activeCount":5,"avgPerResource":3}}}`,
		`{"code":200,"message":"success","data":{"items":[{"dailyAvg":2.5,"product":"compute","region":"global","resourceId":"server-3","resourceName":"web-3","resourceType":"server","startDate":"2026-09-01","totalCost":5,"userId":123456789}],"pagination":{"page":2,"size":2,"total":5,"totalPages":3},"summary":{"totalAll":15,"activeCount":5,"avgPerResource":3}}}`,
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("size"); got != "2" {
			t.Errorf("size = %q, want 2", got)
		}
		page, err := strconv.Atoi(q.Get("page"))
		if err != nil || page < 0 {
			t.Errorf("invalid page query: %s", r.URL.RawQuery)
			http.Error(w, "invalid page", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if page >= len(pages) {
			_, _ = fmt.Fprintf(w, `{"code":200,"message":"success","data":{"items":[],"pagination":{"page":%d,"size":2,"total":5,"totalPages":3},"summary":{"totalAll":15,"activeCount":5,"avgPerResource":3}}}`, page)
			return
		}
		_, _ = w.Write([]byte(pages[page]))
	}))

	var ids []string
	for page := 1; ; page++ {
		out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{
			StartDate: "2026-09-01",
			EndDate:   "2026-09-02",
			Page:      page,
			Size:      2,
		})
		if err != nil {
			t.Fatalf("ListCostResources() page %d error = %v", page, err)
		}
		if out.Page != page || out.PageSize != 2 || out.TotalItem != 5 || out.TotalPage != 3 {
			t.Fatalf("page %d returned unexpected paging: %+v", page, out.PagedList)
		}
		for _, item := range out.Items {
			ids = append(ids, item.ResourceID)
		}
		if page >= out.TotalPage {
			break
		}
	}
	if want := []string{"server-1", "vol-1", "server-2", "vol-2", "server-3"}; !slices.Equal(ids, want) {
		t.Fatalf("resource IDs = %v, want every row exactly once: %v", ids, want)
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
