//go:build livewrite

package livetest_test

import (
	"context"
	"testing"

	"danny.vn/vngcloud/network"
)

func TestNATHarnessHCMExplicitAZ(t *testing.T) {
	zones := []network.NATAvailabilityZone{{UUID: "HCM03-1B", ZoneType: "AVAILABILITY", IsEnabled: true}, {UUID: "HCM03-1C", ZoneType: "AVAILABILITY", IsEnabled: true}}
	fetch := func(_ context.Context, az string) ([]network.NATPackageOffer, error) {
		return []network.NATPackageOffer{{UUID: "offer-1", BillingSKU: "nat.s-standard", CurrencyUnit: "VND"}}, nil
	}
	for _, tc := range []struct {
		name, requested, want string
		zones                 []network.NATAvailabilityZone
		valid                 bool
	}{
		{"two without selector", "", "", zones, false},
		{"two with selector", "HCM03-1C", "HCM03-1C", zones, true},
		{"unmatched", "HCM03-1D", "", zones, false},
		{"single mismatched", "HCM03-1C", "", zones[:1], false},
		{"single no selector", "", "HCM03-1B", zones[:1], true},
		{"single matched", "HCM03-1B", "HCM03-1B", zones[:1], true},
		{"unsafe", "../bad", "", zones, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			az, offer, err := natSelectHCM(context.Background(), tc.zones, tc.requested, fetch)
			if (err == nil) != tc.valid || (tc.valid && (az != tc.want || offer != "offer-1")) {
				t.Fatalf("selection %q %q: %v", az, offer, err)
			}
		})
	}
}
