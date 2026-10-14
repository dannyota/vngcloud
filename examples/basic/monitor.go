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

	showMonitorQuotas(ctx, client, outputs)

	checks, err := client.ListChecks(ctx, nil)
	recordAccount(outputs, "monitor/check", "monitor checks", checkItems(checks), err)
	if err != nil || checks == nil || len(checks.Items) == 0 {
		return
	}

	detail, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checks.Items[0].ID})
	recordAccountOne(outputs, "monitor/check_detail", "monitor check detail", detail, err)
	if err == nil && detail != nil && detail.Check.HistoryAlarmID != "" {
		alarm, err := client.GetCheckAlarm(ctx, &monitor.GetCheckAlarmInput{CheckID: checks.Items[0].ID})
		recordAccountOne(outputs, "monitor/check_alarm", "monitor check alarm", alarm, err)
		history, err := client.ListCheckAlarmHistory(ctx, &monitor.ListCheckAlarmHistoryInput{CheckID: checks.Items[0].ID})
		recordAccountOne(outputs, "monitor/check_alarm_history", "monitor check alarm history", history, err)
	}
}

// showMonitorLocations records vMonitor probe locations. It runs alongside
// showMonitor rather than inside it, so a location fetch failure does not
// stop the check calls above it from being recorded.
func showMonitorLocations(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := monitor.New(cfg)

	locations, err := client.ListLocations(ctx, nil)
	recordAccount(outputs, "monitor/location", "monitor locations", locationItems(locations), err)
}

// showMonitorChannels records notification channel types and channels, and
// one channel's detail through GetChannel when the account has at least
// one. It runs alongside showMonitor rather than inside it, so a channel
// fetch failure does not stop the check calls above it from being recorded.
func showMonitorChannels(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := monitor.New(cfg)

	types, err := client.ListChannelTypes(ctx, nil)
	recordAccount(outputs, "monitor/channel_type", "monitor channel types", channelTypeItems(types), err)

	channels, err := client.ListChannels(ctx, nil)
	recordAccount(outputs, "monitor/channel", "monitor channels", channelItems(channels), err)
	if err != nil || channels == nil || len(channels.Items) == 0 {
		return
	}

	detail, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: channels.Items[0].ID})
	recordAccountOne(outputs, "monitor/channel_detail", "monitor channel detail", detail, err)
}

func locationItems(result *monitor.ListLocationsOutput) []monitor.Location {
	if result == nil {
		return nil
	}
	return result.Items
}

func channelTypeItems(result *monitor.ListChannelTypesOutput) []monitor.ChannelType {
	if result == nil {
		return nil
	}
	return result.Items
}

func channelItems(result *monitor.ListChannelsOutput) []monitor.Channel {
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

func showMonitorQuotas(ctx context.Context, client *monitor.Client, outputs *sdkOutputStore) {
	usage, err := client.GetQuotaUsage(ctx, nil)
	recordAccountOne(outputs, "monitor/quota_usage", "monitor quota usage", usage, err)
	for _, kind := range []string{monitor.NotificationKindEmail, monitor.NotificationKindSMS} {
		packages, err := client.ListNotificationPackages(ctx, &monitor.ListNotificationPackagesInput{Kind: kind})
		recordAccountOne(outputs, "monitor/notification_packages_"+kind, "monitor notification packages", packages, err)
	}
	emailQuota, err := client.GetNotificationQuota(ctx, &monitor.GetNotificationQuotaInput{Kind: monitor.NotificationKindEmail})
	recordAccountOne(outputs, "monitor/email_quota", "monitor email quota", emailQuota, err)
	emailUsage, err := client.GetNotificationQuotaUsage(ctx, &monitor.GetNotificationQuotaUsageInput{Kind: monitor.NotificationKindEmail})
	recordAccountOne(outputs, "monitor/email_quota_usage", "monitor email quota usage", emailUsage, err)
	emailQuote, err := client.QuoteCreateNotificationQuota(ctx, &monitor.CreateNotificationQuotaInput{Kind: monitor.NotificationKindEmail})
	recordAccountOne(outputs, "monitor/email_quota_quote", "monitor email quota quote", emailQuote, err)
	quota, err := client.GetSyntheticQuota(ctx, nil)
	recordAccountOne(outputs, "monitor/synthetic_quota", "monitor synthetic quota", quota, err)
	syntheticUsage, err := client.GetSyntheticQuotaUsage(ctx, nil)
	recordAccountOne(outputs, "monitor/synthetic_quota_usage", "monitor synthetic quota usage", syntheticUsage, err)
}
