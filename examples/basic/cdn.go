package main

import (
	"context"
	"os"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
)

// showCDN records vCDN reads when the process has a vCDN API key. The vCDN
// API ignores the configured region and has no project, so main calls it once
// per config.
func showCDN(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	if os.Getenv("VNGCLOUD_VCDN_API_KEY") == "" {
		return
	}
	client := cdn.New(cfg)

	list, err := client.ListWebAccelerators(ctx, nil)
	items := []cdn.WebAcceleratorSummary(nil)
	if list != nil {
		items = list.Items
	}
	recordAccount(outputs, "cdn/web_accelerator", "cdn web accelerators", items, err)
	if err != nil || len(items) == 0 {
		return
	}

	first := items[0]
	detail, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: first.CDNID})
	var accelerator cdn.WebAccelerator
	if detail != nil {
		accelerator = detail.WebAccelerator
	}
	recordAccountOne(outputs, "cdn/web_accelerator_detail", "cdn web accelerator detail", accelerator, err)
	if err != nil || accelerator.CDNDomain == "" {
		return
	}

	period := cdn.Range{CDNDomains: []string{accelerator.CDNDomain}, Period: "24h"}
	traffic, err := client.GetTraffic(ctx, (*cdn.GetTrafficInput)(&period))
	recordAccountOne(outputs, "cdn/traffic", "cdn traffic", traffic, err)
	requests, err := client.GetRequestRate(ctx, (*cdn.GetRequestRateInput)(&period))
	recordAccountOne(outputs, "cdn/request_rate", "cdn request rate", requests, err)
	cache, err := client.GetCacheStatus(ctx, (*cdn.GetCacheStatusInput)(&period))
	recordAccountOne(outputs, "cdn/cache_status", "cdn cache status", cache, err)
	codes, err := client.GetHTTPCodes(ctx, (*cdn.GetHTTPCodesInput)(&period))
	recordAccountOne(outputs, "cdn/http_codes", "cdn HTTP codes", codes, err)

	now := time.Now().In(time.FixedZone("UTC+7", 7*60*60))
	report, err := client.GetTrafficReport(ctx, &cdn.GetTrafficReportInput{
		CDNDomains: []string{accelerator.CDNDomain},
		From:       now.AddDate(0, 0, -1).Format("2006-01-02"),
		To:         now.Format("2006-01-02"),
	})
	reportItems := []cdn.DomainTraffic(nil)
	if report != nil {
		reportItems = report.Items
	}
	recordAccount(outputs, "cdn/traffic_report", "cdn traffic report", reportItems, err)
}
