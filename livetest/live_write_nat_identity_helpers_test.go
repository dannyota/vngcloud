//go:build livewrite

package livetest_test

import (
	"context"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/network"
)

func liveNATRecordCleanupCandidates(report map[string]any, rows []network.NATInstance, name string) {
	ids := []string{}
	for _, row := range rows {
		if row.NATName == name {
			ids = append(ids, row.UUID)
		}
	}
	report["cleanupCandidateIDs"] = ids
}

func liveNATCleanupDelete(ctx context.Context, client *network.Client, report map[string]any, baseline []network.NATInstance, name, project, region, vpc, pkg, claimed string, alreadySent bool) bool {
	report["cleanupIdentityConfirmed"] = false
	inventory, err := liveNATInventory(ctx, client, region)
	if err != nil {
		return false
	}
	liveNATRecordCleanupCandidates(report, inventory, name)
	id, verified := liveNATDeleteIdentity(inventory, baseline, name, project, region, vpc, pkg, claimed)
	if !verified || alreadySent {
		return false
	}
	report["cleanupIdentityConfirmed"] = true
	if _, err := client.DeleteNATInstance(ctx, &network.DeleteNATInstanceInput{ZoneID: region, VPCID: vpc, NATID: id}); err != nil {
		return false
	}
	after, err := liveNATInventory(ctx, client, region)
	if err != nil {
		return false
	}
	for _, row := range after {
		if row.UUID == id {
			return false
		}
	}
	return true
}

func liveNATDeleteIdentity(rows, baseline []network.NATInstance, name, project, region, vpc, pkg, claimed string) (string, bool) {
	if claimed == "" || core.CheckPathID("NAT cleanup", "claimed NAT ID", claimed) != nil {
		return "", false
	}
	baselineIDs := make(map[string]bool, len(baseline))
	for _, row := range baseline {
		baselineIDs[row.UUID] = true
	}
	found := ""
	for _, row := range rows {
		if row.NATName != name || baselineIDs[row.UUID] {
			continue
		}
		if found != "" || core.CheckPathID("NAT cleanup", "NAT ID", row.UUID) != nil || row.ProjectUUID != project || row.ZoneUUID == "" || row.VPC.UUID != vpc || row.VPC.RegionID != region || row.NATPackage.UUID != pkg {
			return "", false
		}
		found = row.UUID
	}
	if found == "" || (claimed != "" && claimed != found) {
		return "", false
	}
	return found, true
}
