package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestGetCostOverviewCaptured(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/gateway/api/v2/cost-explorer/overview" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("startDate") != "2026-11-01" || q.Get("endDate") != "2026-11-09" || q.Get("groupBy") != "product" || q.Get("interval") != "daily" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/billing/GetCostOverviewCaptured.json")
	}))
	out, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{
		StartDate: "2026-11-01", EndDate: "2026-11-09", GroupBy: "product", Interval: "daily",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.CurrentCost != 271980 || out.Summary.ForecastCost != 928081 || out.Summary.ActiveCount != 3 {
		t.Fatalf("unexpected summary: %+v", out.Summary)
	}
	var costs []float64
	for _, row := range out.Summary.Breakdown {
		costs = append(costs, row.Cost)
	}
	if want := []float64{263118, 8862, 0}; !reflect.DeepEqual(costs, want) {
		t.Errorf("breakdown costs = %v, want %v from value", costs, want)
	}
	for _, point := range out.Series {
		if point.Date == "" {
			t.Errorf("grouped response decoded as a flat point: %+v", point)
		}
	}
	if out.Series != nil {
		t.Errorf("legacy Series = %+v, want nil for a grouped response", out.Series)
	}
	wantBreakdown := []CostBreakdown{
		{Key: "vserver", Cost: 263118, Percent: 96.74},
		{Key: "vstorage", Cost: 8862, Percent: 3.26},
		{Key: "vmonitor", Cost: 0, Percent: 0},
	}
	if !reflect.DeepEqual(out.Summary.Breakdown, wantBreakdown) {
		t.Errorf("breakdown = %+v, want %+v", out.Summary.Breakdown, wantBreakdown)
	}
	wantGroups := []CostSeriesGroup{
		{Key: "vmonitor", Color: "#005AAB", Points: []CostSeriesPoint{
			{Date: "2026-11-01", Cost: 0}, {Date: "2026-11-02", Cost: 0},
			{Date: "2026-11-03", Cost: 0}, {Date: "2026-11-04", Cost: 0},
			{Date: "2026-11-05", Cost: 0}, {Date: "2026-11-06", Cost: 0},
			{Date: "2026-11-07", Cost: 0}, {Date: "2026-11-08", Cost: 0},
			{Date: "2026-11-09", Cost: 0},
		}},
		{Key: "vserver", Color: "#11B895", Points: []CostSeriesPoint{
			{Date: "2026-11-01", Cost: 29928}, {Date: "2026-11-02", Cost: 29928},
			{Date: "2026-11-03", Cost: 29928}, {Date: "2026-11-04", Cost: 29928},
			{Date: "2026-11-05", Cost: 29928}, {Date: "2026-11-06", Cost: 29928},
			{Date: "2026-11-07", Cost: 29928}, {Date: "2026-11-08", Cost: 29929},
			{Date: "2026-11-09", Cost: 23693},
		}},
		{Key: "vstorage", Color: "#5B5AA8", Points: []CostSeriesPoint{
			{Date: "2026-11-01", Cost: 1008}, {Date: "2026-11-02", Cost: 1008},
			{Date: "2026-11-03", Cost: 1008}, {Date: "2026-11-04", Cost: 1008},
			{Date: "2026-11-05", Cost: 1008}, {Date: "2026-11-06", Cost: 1008},
			{Date: "2026-11-07", Cost: 1008}, {Date: "2026-11-08", Cost: 1008},
			{Date: "2026-11-09", Cost: 798},
		}},
	}
	if !reflect.DeepEqual(out.GroupedSeries, wantGroups) {
		t.Errorf("grouped series = %+v, want %+v", out.GroupedSeries, wantGroups)
	}
	if out.Interval != "daily" || out.GroupBy != "product" || out.StartDate != "2026-11-01" || out.EndDate != "2026-11-09" {
		t.Errorf("unexpected overview metadata: %+v", out)
	}
}

func TestGetCostOverviewBreakdownValuePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		wantCost     float64
	}{
		{"value", `"value":12.5`, 12.5},
		{"value wins", `"value":12.5,"cost":99`, 12.5},
		{"zero value wins", `"value":0,"cost":99`, 0},
		{"null value wins", `"value":null,"cost":99`, 0},
		{"legacy cost", `"cost":99`, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"summary":{"breakdown":[{"key":"vserver","percent":50,%s}]}}}`, tc.fields)
			}))
			out, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})
			if err != nil {
				t.Fatal(err)
			}
			want := []CostBreakdown{{Key: "vserver", Cost: tc.wantCost, Percent: 50}}
			if !reflect.DeepEqual(out.Summary.Breakdown, want) {
				t.Fatalf("breakdown = %+v, want %+v", out.Summary.Breakdown, want)
			}
			encoded, err := json.Marshal(out.Summary.Breakdown[0])
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["cost"] != tc.wantCost {
				t.Fatalf("encoded breakdown = %s, want the existing cost field", encoded)
			}
		})
	}
}

func TestGetCostOverviewGroupedIntervals(t *testing.T) {
	for _, groupBy := range []string{"product", "resourceType"} {
		for _, interval := range []string{"daily", "weekly", "monthly"} {
			t.Run(groupBy+"/"+interval, func(t *testing.T) {
				// Synthetic values exercise the common shape in the diagnostic;
				// sparse resource-type groups must keep their own points.
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("groupBy") != groupBy || r.URL.Query().Get("interval") != interval {
						t.Errorf("query = %s", r.URL.RawQuery)
					}
					_, _ = fmt.Fprintf(w, `{"code":200,"data":{"series":[{"key":"first","color":"#005AAB","points":[{"x":"bucket-b","y":0},{"x":"bucket-a","y":12.5}]},{"key":"sparse","color":"#11B895","points":[{"x":"bucket-a","y":2.5}]},{"key":"empty","points":[]}],"groupBy":%q,"interval":%q}}`, groupBy, interval)
				}))
				out, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09", GroupBy: groupBy, Interval: interval})
				if err != nil {
					t.Fatal(err)
				}
				want := []CostSeriesGroup{
					{Key: "first", Color: "#005AAB", Points: []CostSeriesPoint{{Date: "bucket-b", Cost: 0}, {Date: "bucket-a", Cost: 12.5}}},
					{Key: "sparse", Color: "#11B895", Points: []CostSeriesPoint{{Date: "bucket-a", Cost: 2.5}}},
					{Key: "empty", Points: []CostSeriesPoint{}},
				}
				if out.Series != nil || !reflect.DeepEqual(out.GroupedSeries, want) || out.Interval != interval || out.GroupBy != groupBy {
					t.Fatalf("overview = %+v, want grouped series %+v", out, want)
				}
			})
		}
	}
}

func TestGetCostOverviewDecodeErrorWithholdsValues(t *testing.T) {
	for _, data := range []string{
		`{"summary":{"breakdown":[{"key":"vserver","value":"private-value-marker"}]}}`,
		`{"summary":{"breakdown":[{"key":"vserver","percent":"private-value-marker"}]}}`,
		`{"series":[{"key":"vserver","points":"private-value-marker"}]}`,
		`{"series":[{"key":"vserver","points":[{"x":"bucket","y":"private-value-marker"}]}]}`,
	} {
		t.Run(data, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"code":200,"data":%s}`, data)
			}))
			out, err := client.GetCostOverview(context.Background(), &GetCostOverviewInput{StartDate: "2026-11-01", EndDate: "2026-11-09"})
			var apiErr *vngcloud.APIError
			if out != nil || !errors.As(err, &apiErr) {
				t.Fatalf("output = %+v, error = %v, want nil output and APIError", out, err)
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(cause.Error(), "private-value-marker") {
					t.Fatal("decode error exposed a response value")
				}
			}
		})
	}
}
