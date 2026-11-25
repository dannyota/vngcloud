//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"testing"

	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/network"
)

func TestNATHarnessExplicitProject(t *testing.T) {
	for _, tc := range []struct {
		region, project string
		valid           bool
	}{{"hcm-3", "", false}, {"hcm-3", "../bad", false}, {"hcm-3", "pro-123", true}, {"han-1", "", true}} {
		if (natProjectPreflight(tc.region, tc.project) == nil) != tc.valid {
			t.Errorf("project %q region %q", tc.project, tc.region)
		}
	}
}

type natBalanceStub struct {
	cash  *float64
	err   error
	calls int
}

func (s *natBalanceStub) GetBalances(context.Context, *billing.GetBalancesInput) (*billing.GetBalancesOutput, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &billing.GetBalancesOutput{Balances: billing.Balances{CashAvailable: s.cash}}, nil
}
func TestNATHarnessFreshCredit(t *testing.T) {
	high, low := float64(800000), float64(120000)
	s := &natBalanceStub{cash: &high}
	if _, err := natReadCreditCap(context.Background(), s, 700000); err != nil {
		t.Fatal(err)
	}
	s.cash = &low
	got, err := natReadCreditCap(context.Background(), s, 700000)
	if err != nil || got != low || s.calls != 2 {
		t.Fatalf("fresh cap %v, %v, calls %d", got, err, s.calls)
	}
	s.err = errors.New("unavailable")
	if _, err := natReadCreditCap(context.Background(), s, 700000); err == nil {
		t.Fatal("missing credit accepted")
	}
}

func TestNATHarnessHCMSelection(t *testing.T) {
	zones := []network.NATAvailabilityZone{{UUID: "az-1", ZoneType: "AVAILABILITY", IsEnabled: true}, {UUID: "az-2", ZoneType: "AVAILABILITY", IsEnabled: true}}
	offer := network.NATPackageOffer{UUID: "offer-1", BillingSKU: "nat.s-standard", CurrencyUnit: "VND"}
	fetch := func(_ context.Context, az string) ([]network.NATPackageOffer, error) {
		if az == "az-1" {
			return []network.NATPackageOffer{offer}, nil
		}
		return nil, nil
	}
	az, pkg, err := natSelectHCM(context.Background(), zones, "", fetch)
	if err != nil || az != "az-1" || pkg != "offer-1" {
		t.Fatalf("selection %q %q %v", az, pkg, err)
	}
	_, _, err = natSelectHCM(context.Background(), zones, "", func(context.Context, string) ([]network.NATPackageOffer, error) {
		return []network.NATPackageOffer{offer}, nil
	})
	if err == nil {
		t.Fatal("multiple AZ candidates accepted")
	}
	_, _, err = natSelectHCM(context.Background(), zones[:1], "", func(context.Context, string) ([]network.NATPackageOffer, error) {
		return []network.NATPackageOffer{offer, offer}, nil
	})
	if err == nil {
		t.Fatal("multiple offers accepted")
	}
}
