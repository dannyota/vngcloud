package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

var domains = []string{"gen1.vcdn.example"}

type seriesCall struct {
	name, path, fixture string
	run                 func(h *vcdnHarness, rg Range) error
}

func seriesCalls() []seriesCall {
	ctx := context.Background()
	return []seriesCall{
		{"GetTraffic", "analytic/traffic-consuming", "analytics-traffic.json", func(h *vcdnHarness, rg Range) error {
			in := GetTrafficInput(rg)
			_, err := h.GetTraffic(ctx, &in)
			return err
		}},
		{"GetRequestRate", "analytic/cdn-requestsps", "analytics-request-rate.json", func(h *vcdnHarness, rg Range) error {
			in := GetRequestRateInput(rg)
			_, err := h.GetRequestRate(ctx, &in)
			return err
		}},
		{"GetCacheStatus", "analytic/cache-status", "analytics-cache-status.json", func(h *vcdnHarness, rg Range) error {
			in := GetCacheStatusInput(rg)
			_, err := h.GetCacheStatus(ctx, &in)
			return err
		}},
		{"GetHTTPCodes", "analytic/cdn-http-codes", "analytics-http-codes.json", func(h *vcdnHarness, rg Range) error {
			in := GetHTTPCodesInput(rg)
			_, err := h.GetHTTPCodes(ctx, &in)
			return err
		}},
	}
}

func TestAnalyticsRangeRules(t *testing.T) {
	good := []Range{
		{CDNDomains: domains, Period: "24h"},
		{CDNDomains: domains, From: "2026-10-09", To: "2026-10-10"},
		{CDNDomains: domains, From: "2026-10-10", To: "2026-10-10"},
	}
	bad := map[string]Range{
		"no window":          {CDNDomains: domains},
		"period and dates":   {CDNDomains: domains, Period: "24h", From: "2026-10-09", To: "2026-10-10"},
		"period and from":    {CDNDomains: domains, Period: "24h", From: "2026-10-09"},
		"only from":          {CDNDomains: domains, From: "2026-10-09"},
		"only to":            {CDNDomains: domains, To: "2026-10-09"},
		"unknown period":     {CDNDomains: domains, Period: "2h"},
		"period case":        {CDNDomains: domains, Period: "24H"},
		"bad date":           {CDNDomains: domains, From: "2026-13-01", To: "2026-13-02"},
		"slash date":         {CDNDomains: domains, From: "09/10/2026", To: "10/10/2026"},
		"time of day":        {CDNDomains: domains, From: "2026-10-09T00:00:00", To: "2026-10-10"},
		"to before from":     {CDNDomains: domains, From: "2026-10-10", To: "2026-10-09"},
		"no domains":         {Period: "24h"},
		"empty domains":      {CDNDomains: []string{}, Period: "24h"},
		"empty domain entry": {CDNDomains: []string{"a.example", ""}, Period: "24h"},
		"blank domain entry": {CDNDomains: []string{"  "}, Period: "24h"},
	}
	for _, c := range seriesCalls() {
		for i, rg := range good {
			h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, c.fixture)))
			if err := c.run(h, rg); err != nil {
				t.Errorf("%s good %d: %v", c.name, i, err)
			}
		}
		for name, rg := range bad {
			h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, c.fixture)))
			if err := c.run(h, rg); !errors.Is(err, vngcloud.ErrInvalidInput) || h.requests.Load() != 0 {
				t.Errorf("%s %s: err = %v, requests = %d", c.name, name, err, h.requests.Load())
			}
		}
	}
	h := newVCDN(t, testKey, reply(200, "application/json", `{}`))
	for _, c := range seriesCalls() {
		if err := (func() error {
			switch c.name {
			case "GetTraffic":
				_, err := h.GetTraffic(context.Background(), nil)
				return err
			case "GetRequestRate":
				_, err := h.GetRequestRate(context.Background(), nil)
				return err
			case "GetCacheStatus":
				_, err := h.GetCacheStatus(context.Background(), nil)
				return err
			}
			_, err := h.GetHTTPCodes(context.Background(), nil)
			return err
		})(); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("%s nil: err = %v", c.name, err)
		}
	}
	if h.requests.Load() != 0 {
		t.Fatalf("requests = %d", h.requests.Load())
	}
}

func TestAnalyticsEveryAllowedPeriodIsSent(t *testing.T) {
	for _, p := range []string{"30m", "1h", "3h", "6h", "12h", "24h", "3d", "7d", "14d", "30d", "90d", "180d", "360d"} {
		var body map[string]any
		h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			jsonReply(w, readFixture(t, "analytics-traffic.json"))
		})
		if _, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: p}); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if body["period"] != p || body["fromTime"] != nil || body["toTime"] != nil {
			t.Errorf("%s: body = %v, want period only", p, body)
		}
	}
}

func TestAnalyticsRequestShape(t *testing.T) {
	for _, c := range seriesCalls() {
		t.Run(c.name, func(t *testing.T) {
			var gotPath, gotType string
			var body map[string]any
			h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotType = r.Method+" "+r.URL.Path, r.Header.Get("Content-Type")
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &body)
				if r.Header.Get("Authorization") != "Bearer "+testKey {
					t.Error("no key")
				}
				jsonReply(w, readFixture(t, c.fixture))
			})
			if err := c.run(h, Range{CDNDomains: domains, From: "2026-10-09", To: "2026-10-10"}); err != nil {
				t.Fatal(err)
			}
			if gotPath != "POST /vcdn-api/v1/"+c.path || !strings.HasPrefix(gotType, "application/json") {
				t.Errorf("request = %s %s", gotPath, gotType)
			}
			want := map[string]any{"domains": []any{domains[0]}, "fromTime": "09/10/2026", "toTime": "10/10/2026"}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("body = %v, want %v", body, want)
			}
		})
	}
}

// An analytics POST only reads, so the transport retries it.
func TestAnalyticsRetriesLikeARead(t *testing.T) {
	n := 0
	h := newVCDN(t, testKey, func(w http.ResponseWriter, _ *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		jsonReply(w, readFixture(t, "analytics-traffic.json"))
	})
	if _, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: "24h"}); err != nil || n != 2 {
		t.Fatalf("err = %v, requests = %d", err, n)
	}
}

func TestSeriesDecodesTwoEdgePoints(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, "analytics-traffic.json")))
	out, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheSample{
		{Time: time.UnixMilli(1791511200000).UTC()},
		{Time: time.UnixMilli(1791597599999).UTC()},
	}
	if !reflect.DeepEqual(out.Points, want) || out.Points[0].Time.Location() != time.UTC {
		t.Fatalf("points = %+v", out.Points)
	}
}

func TestSeriesSortsAndKeepsValues(t *testing.T) {
	body := `{"success":true,"code":200,"data":{"1791600000000":{"cached":3.5,"uncached":1},"1791500000000":{"cached":2,"uncached":0.25},"1791550000000":{"cached":0,"uncached":9}}}`
	h := newVCDN(t, testKey, reply(200, "application/json", body))
	out, err := h.GetRequestRate(context.Background(), &GetRequestRateInput{CDNDomains: domains, Period: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheSample{
		{Time: time.UnixMilli(1791500000000).UTC(), Cached: 2, Uncached: 0.25},
		{Time: time.UnixMilli(1791550000000).UTC(), Uncached: 9},
		{Time: time.UnixMilli(1791600000000).UTC(), Cached: 3.5, Uncached: 1},
	}
	if !reflect.DeepEqual(out.Points, want) {
		t.Fatalf("points = %+v", out.Points)
	}
}

func TestSeriesBadShapes(t *testing.T) {
	for name, data := range map[string]string{
		"non-integer key": `{"yesterday":{"cached":1,"uncached":1}}`,
		"float key":       `{"1.5":{"cached":1,"uncached":1}}`,
		"list":            `[]`,
		"string":          `"x"`,
		"bad value":       `{"1791500000000":"x"}`,
	} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":`+data+`}`))
		_, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: "24h"})
		if apiError(t, err).Code != codeEmptyResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, data := range []string{`null`, `{}`} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":`+data+`}`))
		out, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: "24h"})
		if err != nil || len(out.Points) != 0 {
			t.Errorf("data %s: out = %+v err = %v", data, out, err)
		}
	}
}

func TestCountsNoDataIsEmpty(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", readFixture(t, "analytics-cache-status.json")))
	out, err := h.GetCacheStatus(context.Background(), &GetCacheStatusInput{CDNDomains: domains, Period: "24h"})
	if err != nil || out.Counts == nil || len(out.Counts) != 0 {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	h = newVCDN(t, testKey, reply(200, "application/json", readFixture(t, "analytics-http-codes.json")))
	out2, err := h.GetHTTPCodes(context.Background(), &GetHTTPCodesInput{CDNDomains: domains, Period: "24h"})
	if err != nil || out2.Counts == nil || len(out2.Counts) != 0 {
		t.Fatalf("out = %+v err = %v", out2, err)
	}
}

func TestCountsDecodeAnObjectString(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":"{\"200\":12,\"404\":1.5}"}`))
	out, err := h.GetHTTPCodes(context.Background(), &GetHTTPCodesInput{CDNDomains: domains, Period: "24h"})
	if err != nil || !reflect.DeepEqual(out.Counts, map[string]float64{"200": 12, "404": 1.5}) {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	for name, data := range map[string]string{
		"object not string": `{"200":1}`, "list string": `"[1]"`, "text": `"hello"`, "null": `null`, "number": `5`, "bad value": `"{\"200\":\"x\"}"`,
	} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":`+data+`}`))
		if _, err := h.GetCacheStatus(context.Background(), &GetCacheStatusInput{CDNDomains: domains, Period: "24h"}); apiError(t, err).Code != codeEmptyResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestTrafficReport(t *testing.T) {
	var body map[string]any
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		if r.URL.Path != "/vcdn-api/v1/analytic/traffic-report" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		jsonReply(w, readFixture(t, "analytics-traffic-report.json"))
	})
	out, err := h.GetTrafficReport(context.Background(), &GetTrafficReportInput{CDNDomains: domains, From: "2026-10-08", To: "2026-10-09"})
	if err != nil {
		t.Fatal(err)
	}
	want := []DomainTraffic{{
		DomainName: "<domain>", CDNDomain: "<cdn-domain>", TrafficType: "Domestic", GroupType: "Standard",
		Points: []Sample{{Time: time.UnixMilli(1791504000000).UTC()}, {Time: time.UnixMilli(1791590400000).UTC()}},
	}}
	if !reflect.DeepEqual(out.Items, want) {
		t.Fatalf("items = %+v", out.Items)
	}
	if body["fromTime"] != "08/10/2026" || body["toTime"] != "09/10/2026" || body["period"] != nil {
		t.Fatalf("body = %v", body)
	}
}

func TestTrafficReportRefusals(t *testing.T) {
	for name, in := range map[string]GetTrafficReportInput{
		"no domains":  {From: "2026-10-08", To: "2026-10-09"},
		"no from":     {CDNDomains: domains, To: "2026-10-09"},
		"no to":       {CDNDomains: domains, From: "2026-10-09"},
		"reversed":    {CDNDomains: domains, From: "2026-10-10", To: "2026-10-09"},
		"bad date":    {CDNDomains: domains, From: "x", To: "2026-10-09"},
		"empty entry": {CDNDomains: []string{""}, From: "2026-10-08", To: "2026-10-09"},
	} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{}`))
		if _, err := h.GetTrafficReport(context.Background(), &in); !errors.Is(err, vngcloud.ErrInvalidInput) || h.requests.Load() != 0 {
			t.Errorf("%s: err = %v, requests = %d", name, err, h.requests.Load())
		}
	}
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":[{"data":{"x":1},"domainName":"a"}]}`))
	if _, err := h.GetTrafficReport(context.Background(), &GetTrafficReportInput{CDNDomains: domains, From: "2026-10-08", To: "2026-10-09"}); apiError(t, err).Code != codeEmptyResponse {
		t.Errorf("bad key: err = %v", err)
	}
	h = newVCDN(t, testKey, reply(200, "application/json", `{"success":true,"code":200,"data":[]}`))
	out, err := h.GetTrafficReport(context.Background(), &GetTrafficReportInput{CDNDomains: domains, From: "2026-10-08", To: "2026-10-09"})
	if err != nil || len(out.Items) != 0 {
		t.Errorf("empty: out = %+v err = %v", out, err)
	}
}

// A domain the account does not own names the account user; the SDK never
// returns that text.
func TestAnalyticsOwnerMessageIsWithheld(t *testing.T) {
	body := `{"success":false,"code":500,"message":"User someone@example.test is not the owner of all the request CDN.","data":""}`
	h := newVCDN(t, testKey, reply(200, "application/json", body))
	_, err := h.GetTraffic(context.Background(), &GetTrafficInput{CDNDomains: domains, Period: "24h"})
	if strings.Contains(err.Error(), "someone") || !strings.Contains(err.Error(), "withheld") {
		t.Fatalf("err = %v", err)
	}
}
