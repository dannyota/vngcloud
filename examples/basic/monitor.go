package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

// showMonitor records vMonitor checks: the list, and one check's detail
// when the account has at least one. Monitor ignores the configured
// region, like billing, so main calls this once per config.
func showMonitor(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := monitor.New(cfg)

	checks, err := client.ListChecks(ctx, nil)
	recordAccount(outputs, "monitor/check", "monitor checks", checkItems(checks), err)
	if err != nil || checks == nil || len(checks.Items) == 0 {
		return
	}

	detail, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checks.Items[0].ID})
	recordAccountOne(outputs, "monitor/check_detail", "monitor check detail", detail, err)
}

// showMonitorLocations records vMonitor probe locations. It runs alongside
// showMonitor rather than inside it, so a location fetch failure does not
// stop the check calls above it from being recorded.
func showMonitorLocations(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := monitor.New(cfg)

	locations, err := client.ListLocations(ctx, nil)
	recordAccount(outputs, "monitor/location", "monitor locations", locationItems(locations), err)
}

func locationItems(result *monitor.ListLocationsOutput) []monitor.Location {
	if result == nil {
		return nil
	}
	return result.Items
}

func checkItems(result *monitor.ListChecksOutput) []monitor.Check {
	if result == nil {
		return nil
	}
	return result.Items
}
