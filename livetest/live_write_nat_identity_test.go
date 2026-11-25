//go:build livewrite

package livetest_test

import (
	"testing"

	"danny.vn/vngcloud/network"
)

func TestNATHarnessDeleteIdentity(t *testing.T) {
	row := network.NATInstance{UUID: "nat-new", NATName: "generated", ProjectUUID: "project-1", ZoneUUID: "az-1", NATPackage: network.NATPackage{UUID: "package-1"}, VPC: network.NATVPC{UUID: "vpc-1", RegionID: "region-1"}}
	other := row
	other.UUID = "nat-other"
	wrong := row
	wrong.ProjectUUID = "project-2"
	wrongVPC := row
	wrongVPC.VPC.UUID = "vpc-2"
	wrongRegion := row
	wrongRegion.VPC.RegionID = "region-2"
	wrongPackage := row
	wrongPackage.NATPackage.UUID = "package-2"
	for _, tc := range []struct {
		name           string
		rows, baseline []network.NATInstance
		claimed        string
		valid          bool
	}{
		{"unique", []network.NATInstance{row}, nil, "nat-new", true},
		{"ambiguous", []network.NATInstance{row, other}, nil, "nat-new", false},
		{"baseline", []network.NATInstance{row}, []network.NATInstance{row}, "nat-new", false},
		{"scope mismatch", []network.NATInstance{wrong}, nil, "nat-new", false},
		{"wrong VPC", []network.NATInstance{wrongVPC}, nil, "nat-new", false},
		{"wrong region", []network.NATInstance{wrongRegion}, nil, "nat-new", false},
		{"wrong package", []network.NATInstance{wrongPackage}, nil, "nat-new", false},
		{"subtract baseline", []network.NATInstance{row, other}, []network.NATInstance{other}, "nat-new", true},
		{"partial output mismatch", []network.NATInstance{row}, nil, "nat-other", false},
		{"fresh discovery", []network.NATInstance{row}, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := liveNATDeleteIdentity(tc.rows, tc.baseline, "generated", "project-1", "region-1", "vpc-1", "package-1", tc.claimed)
			if ok != tc.valid || (ok && got != "nat-new") {
				t.Fatalf("identity %q valid %v", got, ok)
			}
		})
	}
}
