//go:build livewrite

package livetest_test

import (
	"math"
	"testing"

	"danny.vn/vngcloud/portal"
)

func TestNATMaxPrice(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  float64
		valid bool
	}{{"", 1000000, true}, {"123", 123, true}, {"1000000", 1000000, true}, {"0", 0, false}, {"1000001", 0, false}, {"1.5", 0, false}, {"-1", 0, false}} {
		got, err := natMaxPrice(tc.value)
		if (err == nil) != tc.valid || (tc.valid && got != tc.want) {
			t.Fatalf("%q: %v %v", tc.value, got, err)
		}
	}
}

func TestNATHarnessRegion(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		valid       bool
	}{{"", "han-1", true}, {"han-1", "han-1", true}, {"hcm-3", "hcm-3", true}, {"hcm-1", "", false}} {
		got, err := natRegion(tc.input)
		if got != tc.want || (err == nil) != tc.valid {
			t.Errorf("region %q = %q, %v", tc.input, got, err)
		}
	}
}

func TestNATHarnessHCMPreflight(t *testing.T) {
	for _, tc := range []struct {
		region, cap string
		hold        bool
		valid       bool
	}{
		{"han-1", "", false, true}, {"hcm-3", "", true, false}, {"hcm-3", "720001", true, false}, {"hcm-3", "720000", false, false}, {"hcm-3", "720000", true, true}, {"hcm-3", "1", true, true}, {"hcm-3", "0", true, false},
	} {
		_, err := natPreflight(tc.region, tc.cap, tc.hold)
		if (err == nil) != tc.valid {
			t.Errorf("preflight %q %q %v: %v", tc.region, tc.cap, tc.hold, err)
		}
	}
}

func TestNATHarnessCreditCap(t *testing.T) {
	for _, tc := range []struct {
		cap, credit, want float64
		valid             bool
	}{{720000, 800000, 720000, true}, {500000, 800000, 500000, true}, {500000, 200000, 200000, true}, {720000, 0, 0, false}, {720000, math.NaN(), 0, false}, {720000, math.Inf(1), 0, false}} {
		got, err := natCreditCap(tc.cap, tc.credit)
		if got != tc.want || (err == nil) != tc.valid {
			t.Errorf("credit cap %v %v = %v, %v", tc.cap, tc.credit, got, err)
		}
	}
}

func TestNATHarnessQuota(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  []portal.Quota
		valid bool
	}{
		{"available", []portal.Quota{{"quotaName": "VPC", "used": "1", "limit": float64(2)}}, true},
		{"numeric", []portal.Quota{{"quotaName": "VPC", "used": float64(0), "limit": float64(2)}}, true},
		{"full", []portal.Quota{{"quotaName": "VPC", "used": float64(2), "limit": float64(2)}}, false},
		{"missing", nil, false},
		{"duplicate", []portal.Quota{{"quotaName": "VPC", "used": float64(0), "limit": float64(2)}, {"quotaName": "VPC", "used": float64(2), "limit": float64(2)}}, false},
		{"malformed", []portal.Quota{{"quotaName": "VPC", "used": "x", "limit": float64(2)}}, false},
		{"fractional", []portal.Quota{{"quotaName": "VPC", "used": float64(0.5), "limit": float64(2)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if natQuotaAvailable(tc.rows) != tc.valid {
				t.Fatal("unexpected quota decision")
			}
		})
	}
}
