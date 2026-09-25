package main

import (
	"context"
	"fmt"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/portal"
)

func showProjects(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	projects, err := client.ListProjects(ctx, nil)
	record(outputs, client, "project/project", "visible projects in region", projects, err)
}

func showPortal(ctx context.Context, client *vngcloud.Client, cfg vngcloud.Config, outputs *sdkOutputStore) {
	portalClient := portal.New(cfg)

	userInfoOut, err := portalClient.GetUserInfo(ctx, nil)
	var userInfo portal.UserInfo
	if userInfoOut != nil {
		userInfo = userInfoOut.UserInfo
	}
	recordOne(outputs, client, "portal/user_info", "portal user info", userInfo, err)

	zonesOut, err := portalClient.ListZones(ctx, nil)
	zones := []portal.Zone(nil)
	if zonesOut != nil {
		zones = zonesOut.Items
	}
	record(outputs, client, "portal/zone", "portal zones", zones, err)

	quotasOut, err := portalClient.ListQuotaUsed(ctx, nil)
	if err != nil {
		fmt.Printf("portal quota used: error\n")
		outputs.add("portal/quota_used", client, nil, err)
	} else {
		quotas := quotasOut.Items
		fmt.Printf("portal quota used: %d\n", len(quotas))
		outputs.add("portal/quota_used", client, quotas, nil)
		quotaDetails := make([]portal.Quota, 0, len(quotas))
		for _, quota := range quotas {
			name, ok := quotaName(quota)
			if !ok {
				continue
			}
			detailOut, detailErr := portalClient.GetQuota(ctx, &portal.GetQuotaInput{Name: name})
			if detailErr != nil {
				fmt.Printf("portal quota detail: error\n")
				continue
			}
			quotaDetails = append(quotaDetails, detailOut.Quota)
		}
		fmt.Printf("portal quota details: %d\n", len(quotaDetails))
		outputs.add("portal/quota_detail", client, quotaDetails, nil)
	}

	tagQuotaOut, err := portalClient.GetTagQuota(ctx, nil)
	var tagQuota portal.TagQuota
	if tagQuotaOut != nil {
		tagQuota = tagQuotaOut.TagQuota
	}
	recordOne(outputs, client, "portal/tag_quota", "portal tag quota", tagQuota, err)
}

func quotaName(quota portal.Quota) (string, bool) {
	for _, key := range []string{"name", "quotaName", "resourceName", "resource", "key", "type"} {
		value, ok := quota[key]
		if !ok {
			continue
		}
		name, ok := value.(string)
		if ok && name != "" {
			return name, true
		}
	}
	return "", false
}

func showDNS(ctx context.Context, client *vngcloud.Client, cfg vngcloud.Config, outputs *sdkOutputStore) {
	dnsClient := dns.New(cfg)

	zonesOut, err := dnsClient.ListHostedZones(ctx, nil)
	if err != nil {
		printError("dns hosted zones", err)
		outputs.add("dns/hosted_zone", client, nil, err)
		return
	}
	zones := zonesOut.Items
	record(outputs, client, "dns/hosted_zone", "dns hosted zones", zones, nil)

	zoneDetails := make([]dns.HostedZone, 0, len(zones))
	records := make([]dns.Record, 0)
	recordDetails := make([]dns.Record, 0)
	for _, zone := range zones {
		if zone.ID == "" {
			continue
		}
		detail, detailErr := dnsClient.GetHostedZone(ctx, &dns.GetHostedZoneInput{HostedZoneID: zone.ID})
		if detailErr != nil {
			printError("dns hosted zone detail", detailErr)
			continue
		}
		zoneDetails = append(zoneDetails, detail.HostedZone)

		zoneRecords, recordErr := dnsClient.ListRecords(ctx, &dns.ListRecordsInput{HostedZoneID: zone.ID})
		if recordErr != nil {
			printError("dns records", recordErr)
			continue
		}
		records = append(records, zoneRecords.Items...)
		for _, rec := range zoneRecords.Items {
			if rec.ID == "" {
				continue
			}
			recordDetail, recordDetailErr := dnsClient.GetRecord(ctx, &dns.GetRecordInput{HostedZoneID: zone.ID, RecordID: rec.ID})
			if recordDetailErr != nil {
				printError("dns record detail", recordDetailErr)
				continue
			}
			recordDetails = append(recordDetails, recordDetail.Record)
		}
	}
	outputs.add("dns/hosted_zone_detail", client, zoneDetails, nil)
	outputs.add("dns/record", client, records, nil)
	outputs.add("dns/record_detail", client, recordDetails, nil)
}

func showContainerRegistry(ctx context.Context, client *vngcloud.Client, cfg vngcloud.Config, outputs *sdkOutputStore) {
	vcrClient := containerregistry.New(cfg)

	repositories, err := vcrClient.ListRepositories(ctx, nil)
	if err != nil {
		printError("container registry repositories", err)
		outputs.add("containerregistry/repository", client, nil, err)
	} else {
		record(outputs, client, "containerregistry/repository", "container registry repositories", repositories.Items, nil)
	}

	users, err := vcrClient.ListUsers(ctx, nil)
	if err != nil {
		printError("container registry users", err)
		outputs.add("containerregistry/user", client, nil, err)
		return
	}
	record(outputs, client, "containerregistry/user", "container registry users", users.Items, nil)
}
