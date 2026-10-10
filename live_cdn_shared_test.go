//go:build live || livewrite

package vngcloud_test

import (
	"context"
	"testing"
	"time"

	"danny.vn/vngcloud/cdn"
)

// liveCDNAnalytics runs the five analytics reads on one generated CDN domain
// over a period and over a date range, and logs counts only.
func liveCDNAnalytics(ctx context.Context, t *testing.T, client *cdn.Client, domain string) {
	t.Helper()
	utc7 := time.FixedZone("UTC+7", 7*60*60)
	today := time.Now().In(utc7)
	from, to := today.AddDate(0, 0, -1).Format("2006-01-02"), today.Format("2006-01-02")
	domains := []string{domain}

	for _, window := range []struct {
		name     string
		rg       cdn.Range
		reportOK bool
	}{
		{"24h", cdn.Range{CDNDomains: domains, Period: "24h"}, false},
		{"yesterday to today", cdn.Range{CDNDomains: domains, From: from, To: to}, true},
	} {
		traffic, err := client.GetTraffic(ctx, (*cdn.GetTrafficInput)(&window.rg))
		if err != nil {
			t.Fatalf("GetTraffic %s: %v", window.name, err)
		}
		rate, err := client.GetRequestRate(ctx, (*cdn.GetRequestRateInput)(&window.rg))
		if err != nil {
			t.Fatalf("GetRequestRate %s: %v", window.name, err)
		}
		cache, err := client.GetCacheStatus(ctx, (*cdn.GetCacheStatusInput)(&window.rg))
		if err != nil {
			t.Fatalf("GetCacheStatus %s: %v", window.name, err)
		}
		codes, err := client.GetHTTPCodes(ctx, (*cdn.GetHTTPCodesInput)(&window.rg))
		if err != nil {
			t.Fatalf("GetHTTPCodes %s: %v", window.name, err)
		}
		t.Logf("analytics %s: traffic points %d, request points %d, cache counts %d, http code counts %d",
			window.name, len(traffic.Points), len(rate.Points), len(cache.Counts), len(codes.Counts))
		if window.reportOK {
			report, err := client.GetTrafficReport(ctx, &cdn.GetTrafficReportInput{CDNDomains: domains, From: from, To: to})
			if err != nil {
				t.Fatalf("GetTrafficReport: %v", err)
			}
			points := 0
			for _, item := range report.Items {
				points += len(item.Points)
			}
			t.Logf("traffic report: domains %d, points %d", len(report.Items), points)
		}
	}
}
