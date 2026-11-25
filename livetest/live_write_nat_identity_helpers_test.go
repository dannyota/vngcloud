//go:build livewrite

package livetest_test

import (
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

func liveNATDeleteIdentity(rows, baseline []network.NATInstance, name, project, region, vpc, pkg, claimed string) (string, bool) {
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
