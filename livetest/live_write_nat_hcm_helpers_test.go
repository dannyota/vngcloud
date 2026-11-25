//go:build livewrite

package livetest_test

import (
	"context"
	"errors"

	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/network"
)

func natProjectPreflight(region, project string) error {
	if region == "hcm-3" && (project == "" || core.CheckPathID("NAT preflight", "ProjectID", project) != nil) {
		return errors.New("HCM requires an explicit path-safe project ID")
	}
	return nil
}

type natBalanceReader interface {
	GetBalances(context.Context, *billing.GetBalancesInput) (*billing.GetBalancesOutput, error)
}

func natReadCreditCap(ctx context.Context, reader natBalanceReader, cap float64) (float64, error) {
	out, err := reader.GetBalances(ctx, nil)
	if err != nil || out == nil || out.Balances.CashAvailable == nil {
		return 0, errors.New("available NAT credit could not be verified")
	}
	return natCreditCap(cap, *out.Balances.CashAvailable)
}

func natSelectPlacement(ctx context.Context, client *network.Client, region, scope string, zones []network.NATAvailabilityZone) (string, string, error) {
	fetch := func(ctx context.Context, az string) ([]network.NATPackageOffer, error) {
		out, err := client.ListNATPackages(ctx, &network.ListNATPackagesInput{ZoneID: scope, AvailabilityZoneID: az})
		if err != nil {
			return nil, err
		}
		if out == nil {
			return nil, errors.New("missing NAT catalog")
		}
		return out.Items, nil
	}
	if region == "hcm-3" {
		return natSelectHCM(ctx, zones, fetch)
	}
	for _, zone := range zones {
		if zone.ZoneType == "AVAILABILITY" && zone.IsEnabled {
			return natSelectHCM(ctx, []network.NATAvailabilityZone{zone}, fetch)
		}
	}
	return "", "", errors.New("no enabled NAT availability zone")
}

func natSelectHCM(ctx context.Context, zones []network.NATAvailabilityZone, fetch func(context.Context, string) ([]network.NATPackageOffer, error)) (string, string, error) {
	selectedAZ, selectedOffer := "", ""
	seen := map[string]bool{}
	for _, zone := range zones {
		if !zone.IsEnabled || zone.ZoneType != "AVAILABILITY" {
			continue
		}
		if core.CheckPathID("NAT selection", "AZ", zone.UUID) != nil || seen[zone.UUID] {
			return "", "", errors.New("invalid or duplicate NAT AZ")
		}
		seen[zone.UUID] = true
		offers, err := fetch(ctx, zone.UUID)
		if err != nil {
			return "", "", errors.New("NAT package catalog failed")
		}
		for _, offer := range offers {
			if offer.BillingSKU != "nat.s-standard" || offer.CurrencyUnit != "VND" {
				continue
			}
			if selectedAZ != "" || core.CheckPathID("NAT selection", "offer", offer.UUID) != nil {
				return "", "", errors.New("ambiguous NAT offer")
			}
			selectedAZ, selectedOffer = zone.UUID, offer.UUID
		}
	}
	if selectedAZ == "" {
		return "", "", errors.New("no eligible NAT offer")
	}
	return selectedAZ, selectedOffer, nil
}
