package cdn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// periods are the values the server accepts for Period.
var periods = []string{"30m", "1h", "3h", "6h", "12h", "24h", "3d", "7d", "14d", "30d", "90d", "180d", "360d"}

// Range selects the CDNs and the time window of an analytics read. Set
// exactly one of Period or the pair From and To.
//
// CDNDomains holds generated CDN domain names (WebAccelerator.CDNDomain),
// not customer domain names. Period is one of 30m, 1h, 3h, 6h, 12h, 24h,
// 3d, 7d, 14d, 30d, 90d, 180d, or 360d. From and To are calendar dates as
// YYYY-MM-DD, read in UTC+7 from 00:00 on From to the end of To.
type Range struct {
	CDNDomains []string `vngcloud:"required"`
	Period     string
	From       string
	To         string
}

type (
	GetTrafficInput     Range
	GetRequestRateInput Range
	GetCacheStatusInput Range
	GetHTTPCodesInput   Range
)

// CacheSample is one point of a traffic or request series.
type CacheSample struct {
	Time     time.Time
	Cached   float64
	Uncached float64
}

type GetTrafficOutput struct{ Points []CacheSample }
type GetRequestRateOutput struct{ Points []CacheSample }

// GetCacheStatusOutput and GetHTTPCodesOutput hold counts by name. With no
// data in the window the map is empty. No call has returned a non-zero
// value, so no unit is stated.
type GetCacheStatusOutput struct{ Counts map[string]float64 }
type GetHTTPCodesOutput struct{ Counts map[string]float64 }

// GetTraffic reads the traffic series of the CDNs. Points are not evenly
// spaced: with no traffic only the two window edges appear.
func (c *Client) GetTraffic(ctx context.Context, in *GetTrafficInput) (*GetTrafficOutput, error) {
	pts, err := c.series(ctx, "cdn.GetTraffic", "traffic-consuming", (*Range)(in))
	if err != nil {
		return nil, err
	}
	return &GetTrafficOutput{Points: pts}, nil
}

// GetRequestRate reads the request-rate series of the CDNs.
func (c *Client) GetRequestRate(ctx context.Context, in *GetRequestRateInput) (*GetRequestRateOutput, error) {
	pts, err := c.series(ctx, "cdn.GetRequestRate", "cdn-requestsps", (*Range)(in))
	if err != nil {
		return nil, err
	}
	return &GetRequestRateOutput{Points: pts}, nil
}

// GetCacheStatus reads the cache hit counts of the CDNs.
func (c *Client) GetCacheStatus(ctx context.Context, in *GetCacheStatusInput) (*GetCacheStatusOutput, error) {
	counts, err := c.counts(ctx, "cdn.GetCacheStatus", "cache-status", (*Range)(in))
	if err != nil {
		return nil, err
	}
	return &GetCacheStatusOutput{Counts: counts}, nil
}

// GetHTTPCodes reads the HTTP status code counts of the CDNs.
func (c *Client) GetHTTPCodes(ctx context.Context, in *GetHTTPCodesInput) (*GetHTTPCodesOutput, error) {
	counts, err := c.counts(ctx, "cdn.GetHTTPCodes", "cdn-http-codes", (*Range)(in))
	if err != nil {
		return nil, err
	}
	return &GetHTTPCodesOutput{Counts: counts}, nil
}

// GetTrafficReportInput selects CDNs and a date window. The report takes no
// Period.
type GetTrafficReportInput struct {
	CDNDomains []string `vngcloud:"required"`
	From       string   `vngcloud:"required"`
	To         string   `vngcloud:"required"`
}

// Sample is one daily value of a traffic report.
type Sample struct {
	Time  time.Time
	Value float64
}

// DomainTraffic is the report of one CDN. The server buckets the report at
// UTC midnight, unlike the series reads, which use UTC+7.
type DomainTraffic struct {
	DomainName  string
	CDNDomain   string
	TrafficType string
	GroupType   string
	Points      []Sample
}

type GetTrafficReportOutput = core.List[DomainTraffic]

// GetTrafficReport reads the daily traffic report of the CDNs between two
// dates.
func (c *Client) GetTrafficReport(ctx context.Context, in *GetTrafficReportInput) (*GetTrafficReportOutput, error) {
	const op = "cdn.GetTrafficReport"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	body, err := rangeBody(op, &Range{CDNDomains: in.CDNDomains, From: in.From, To: in.To})
	if err != nil {
		return nil, err
	}
	r := analyticsCall(op, "traffic-report", body)
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	var wire []struct {
		Data        map[string]float64 `json:"data"`
		DomainName  string             `json:"domainName"`
		CDNDomain   string             `json:"cdnDomain"`
		TrafficType string             `json:"trafficType"`
		GroupType   string             `json:"groupType"`
	}
	if emptyData(data) {
		return &GetTrafficReportOutput{}, nil
	}
	if json.Unmarshal(data, &wire) != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	items := make([]DomainTraffic, len(wire))
	for i, w := range wire {
		pts, err := timeSeries(w.Data, func(t time.Time, v float64) Sample { return Sample{Time: t, Value: v} })
		if err != nil {
			return nil, r.unexpected(http.StatusOK)
		}
		items[i] = DomainTraffic{DomainName: w.DomainName, CDNDomain: w.CDNDomain, TrafficType: w.TrafficType, GroupType: w.GroupType, Points: pts}
	}
	return &GetTrafficReportOutput{Items: items}, nil
}

type sampleWire struct {
	Cached   float64 `json:"cached"`
	Uncached float64 `json:"uncached"`
}

func analyticsCall(op, endpoint string, body map[string]any) call {
	return call{op: op, method: http.MethodPost, parts: []string{"analytic", endpoint}, body: body, idempotent: true}
}

// rangeBody checks rg and builds the request body. The server reads dates
// as dd/mm/yyyy and lets period win when both are sent, so the body holds
// one or the other.
func rangeBody(op string, rg *Range) (map[string]any, error) {
	if rg == nil || len(rg.CDNDomains) == 0 {
		return nil, fmt.Errorf("%w: %s requires CDNDomains", core.ErrInvalidInput, op)
	}
	for _, d := range rg.CDNDomains {
		if strings.TrimSpace(d) == "" {
			return nil, fmt.Errorf("%w: %s requires every CDNDomains entry to be non-empty", core.ErrInvalidInput, op)
		}
	}
	body := map[string]any{"domains": rg.CDNDomains}
	hasDates := rg.From != "" || rg.To != ""
	switch {
	case rg.Period != "" && hasDates:
		return nil, fmt.Errorf("%w: %s takes Period or From and To, not both", core.ErrInvalidInput, op)
	case rg.Period != "":
		for _, p := range periods {
			if rg.Period == p {
				body["period"] = p
				return body, nil
			}
		}
		return nil, fmt.Errorf("%w: %s Period must be one of %s", core.ErrInvalidInput, op, strings.Join(periods, ", "))
	case rg.From == "" || rg.To == "":
		return nil, fmt.Errorf("%w: %s requires Period, or both From and To", core.ErrInvalidInput, op)
	}
	from, err := time.Parse("2006-01-02", rg.From)
	if err != nil {
		return nil, fmt.Errorf("%w: %s requires From to be a YYYY-MM-DD date, got %q", core.ErrInvalidInput, op, limitMessage(rg.From))
	}
	to, err := time.Parse("2006-01-02", rg.To)
	if err != nil {
		return nil, fmt.Errorf("%w: %s requires To to be a YYYY-MM-DD date, got %q", core.ErrInvalidInput, op, limitMessage(rg.To))
	}
	if to.Before(from) {
		return nil, fmt.Errorf("%w: %s requires To not before From", core.ErrInvalidInput, op)
	}
	body["fromTime"] = from.Format("02/01/2006")
	body["toTime"] = to.Format("02/01/2006")
	return body, nil
}

func (c *Client) series(ctx context.Context, op, endpoint string, rg *Range) ([]CacheSample, error) {
	if err := core.CheckRequired(op, rg); err != nil {
		return nil, err
	}
	body, err := rangeBody(op, rg)
	if err != nil {
		return nil, err
	}
	r := analyticsCall(op, endpoint, body)
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if emptyData(data) {
		return nil, nil
	}
	var wire map[string]sampleWire
	if json.Unmarshal(data, &wire) != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	points, err := timeSeries(wire, func(t time.Time, v sampleWire) CacheSample {
		return CacheSample{Time: t, Cached: v.Cached, Uncached: v.Uncached}
	})
	if err != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	return points, nil
}

// timeSeries turns a map from epoch-millisecond keys into points sorted by
// time. A key that is not an integer is an error.
func timeSeries[V, P any](m map[string]V, build func(time.Time, V) P) ([]P, error) {
	type entry struct {
		ms int64
		v  V
	}
	entries := make([]entry, 0, len(m))
	for key, v := range m {
		ms, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{ms, v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ms < entries[j].ms })
	points := make([]P, len(entries))
	for i, e := range entries {
		points[i] = build(time.UnixMilli(e.ms).UTC(), e.v)
	}
	return points, nil
}

func (c *Client) counts(ctx context.Context, op, endpoint string, rg *Range) (map[string]float64, error) {
	if err := core.CheckRequired(op, rg); err != nil {
		return nil, err
	}
	body, err := rangeBody(op, rg)
	if err != nil {
		return nil, err
	}
	r := analyticsCall(op, endpoint, body)
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	var text string
	if json.Unmarshal(data, &text) != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	counts := map[string]float64{}
	if strings.TrimSpace(text) == "[]" {
		return counts, nil
	}
	if json.Unmarshal([]byte(text), &counts) != nil {
		return nil, r.unexpected(http.StatusOK)
	}
	return counts, nil
}
