//go:build livewrite

package livetest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/network"
)

const liveNATPrefix = "vngcloud-live-nat-"

func TestLiveWriteNAT(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_NAT_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_NAT_WRITE=1 for the approved paid HAN run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cfg := liveWriteConfig(ctx, t, vngcloud.WithRegion("han-1"))
	if core.ClientOf(cfg).Region() != "han-1" || !core.ClientOf(cfg).UsesIAMUserLogin() {
		t.Fatal("live NAT writes require SDK IAM-user login in han-1")
	}
	client := network.New(cfg)
	zones, err := client.ListVNetworkRegions(ctx, nil)
	if err != nil {
		t.Fatal("NAT region discovery failed")
	}
	zoneID := ""
	matches := 0
	for _, zone := range zones.Items {
		if strings.TrimRight(zone.DashboardURL, "/") == "https://han-1-vnetwork.console.greennode.ai" || strings.TrimRight(zone.GatewayURL, "/") == "https://han-1-vnetwork.console.greennode.ai/vnetwork-gateway" {
			zoneID = zone.UUID
			matches++
		}
	}
	if matches != 1 || zoneID == "" {
		t.Fatal("HAN needs one region-level NAT scope")
	}
	baseline, err := liveNATInventory(ctx, client, zoneID)
	if err != nil {
		t.Fatal("NAT baseline scan failed")
	}
	for _, row := range baseline {
		if strings.HasPrefix(row.NATName, liveNATPrefix) {
			t.Fatal("existing NAT uses the live NAT prefix; resolve leftovers before running")
		}
	}
	vpcs, err := liveNATVPCs(ctx, client)
	if err != nil {
		t.Fatal("VPC baseline scan failed")
	}
	used := make([]netip.Prefix, 0, len(vpcs))
	for _, vpc := range vpcs {
		if strings.HasPrefix(vpc.Name, liveNATPrefix) {
			t.Fatal("existing VPC uses the live NAT prefix; resolve leftovers before running")
		}
		prefix, parseErr := netip.ParsePrefix(vpc.CIDR)
		if parseErr != nil {
			t.Fatal("VPC baseline has an invalid CIDR")
		}
		used = append(used, prefix.Masked())
	}
	cidr := ""
	for second := byte(250); second >= 200; second-- {
		candidate := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, second, 0, 0}), 16)
		free := true
		for _, prefix := range used {
			if candidate.Overlaps(prefix) {
				free = false
				break
			}
		}
		if free {
			cidr = candidate.String()
			break
		}
	}
	if cidr == "" {
		t.Fatal("no unused /16 is available for the disposable VPC")
	}
	suffix := make([]byte, 4)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal("unique name generation failed")
	}
	name := liveNATPrefix + hex.EncodeToString(suffix)
	baselineNATIDs := []string{}
	for _, row := range baseline {
		baselineNATIDs = append(baselineNATIDs, row.UUID)
	}
	baselineVPCIDs := []string{}
	for _, row := range vpcs {
		baselineVPCIDs = append(baselineVPCIDs, row.UUID)
	}
	report := map[string]any{"region": "han-1", "name": name, "baselineNATIDs": baselineNATIDs, "baselineVPCIDs": baselineVPCIDs, "transactionReconciliationRequired": true}
	if liveNATSaveReport(report) != nil {
		t.Fatal("private NAT report could not be saved")
	}
	t.Cleanup(func() {
		if liveNATSaveReport(report) != nil {
			t.Error("private NAT report could not be saved")
		}
	})
	t.Logf("baseline VPC count=%d NAT count=%d", len(vpcs), len(baseline))
	vpcOut, createVPCErr := client.CreateVPC(ctx, &network.CreateVPCInput{Name: name, CIDR: cidr})
	vpcID := ""
	if vpcOut != nil {
		report["returnedVPCID"] = vpcOut.VPC.UUID
	}
	scopedVPCs, scopeErr := liveNATVPCs(ctx, client)
	if scopeErr != nil || !liveNATOwnedVPC(vpcOut, vpcs, scopedVPCs, name, cidr) {
		report["vpcOwnershipConfirmed"] = false
		t.Fatal("disposable VPC identity is unconfirmed; inspect the private NAT report; no cleanup authorized")
	}
	vpcID = vpcOut.VPC.UUID
	report["vpcOwnershipConfirmed"] = true
	report["vpcID"] = vpcID
	natID := ""
	expectedPackage := ""
	natAttempted, natDeleteAttempted, natGone, vpcGone := false, false, false, false
	if vpcID != "" {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 25*time.Minute)
			defer cleanupCancel()
			defer func() { report["natID"] = natID; report["natGone"] = natGone; report["vpcGone"] = vpcGone }()
			if natAttempted && !natGone {
				inventory, listErr := liveNATInventory(cleanupCtx, client, zoneID)
				if listErr != nil {
					t.Error("NAT cleanup cannot confirm inventory; inspect the private NAT report")
					return
				}
				candidates := []network.NATInstance{}
				for _, row := range inventory {
					if row.NATName == name {
						candidates = append(candidates, row)
					}
				}
				if natID == "" {
					if len(candidates) != 1 || candidates[0].VPC.UUID != vpcID || candidates[0].NATPackage.UUID != expectedPackage || candidates[0].ProjectUUID != core.ClientOf(cfg).ProjectID() || candidates[0].ZoneUUID == "" || candidates[0].VPC.RegionID != zoneID {
						t.Error("purchase identity is unresolved; inspect the private NAT report")
						return
					}
					natID = candidates[0].UUID
				}
				present := false
				for _, row := range inventory {
					if row.UUID == natID {
						present = true
					}
				}
				if present {
					if natDeleteAttempted {
						t.Error("NAT delete is unresolved; inspect the private NAT report")
						return
					}
					natDeleteAttempted = true
					_, deleteErr := client.DeleteNATInstance(cleanupCtx, &network.DeleteNATInstanceInput{ZoneID: zoneID, VPCID: vpcID, NATID: natID})
					if deleteErr != nil {
						t.Error("NAT cleanup failed; inspect the private NAT report")
						return
					}
				}
				natGone = true
			}
			if !vpcGone {
				vpcGone = liveNATDeleteVPC(cleanupCtx, client, vpcID)
				if !vpcGone {
					t.Error("VPC cleanup failed; inspect the private NAT report")
				}
			}
		})
	}
	if createVPCErr != nil || vpcID == "" {
		t.Fatal("disposable VPC create failed; inspect the private NAT report")
	}
	offers, err := client.ListNATZones(ctx, &network.ListNATZonesInput{ZoneID: zoneID})
	if err != nil {
		t.Fatal("NAT zone catalog failed")
	}
	az := ""
	for _, zone := range offers.Items {
		if zone.ZoneType == "AVAILABILITY" && zone.IsEnabled {
			az = zone.UUID
			break
		}
	}
	if az == "" {
		t.Fatal("no enabled NAT availability zone")
	}
	packages, err := client.ListNATPackages(ctx, &network.ListNATPackagesInput{ZoneID: zoneID, AvailabilityZoneID: az})
	if err != nil {
		t.Fatal("NAT package catalog failed")
	}
	packageID := ""
	for _, offer := range packages.Items {
		if offer.BillingSKU == "nat.s-standard" && offer.CurrencyUnit == "VND" {
			if packageID != "" {
				t.Fatal("ambiguous Standard NAT package")
			}
			packageID = offer.UUID
		}
	}
	if packageID == "" {
		t.Fatal("no Standard NAT package")
	}
	expectedPackage = packageID
	in := &network.CreateNATInstanceInput{Name: name, ZoneID: zoneID, AvailabilityZoneID: az, PackageID: packageID, VPCID: vpcID, MaxPrice: 1000000}
	quote, err := client.QuoteCreateNATInstance(ctx, in)
	if err != nil {
		t.Fatal("NAT quote failed")
	}
	if quote.TotalPrice <= 0 || quote.TotalPrice > in.MaxPrice {
		t.Fatal("NAT quote is outside the approved cap")
	}
	t.Logf("catalog zones=%d packages=%d quote monthly=%.0f total=%.0f VND", len(offers.Items), len(packages.Items), quote.MonthlyPrice, quote.TotalPrice)
	money := billing.New(cfg)
	beforeCash, cashErr := storageProjectCash(ctx, money)
	if cashErr != nil {
		t.Fatal("NAT baseline cash read failed")
	}
	baselineRoutes, routeErr := liveNATRoutes(ctx, client, vpcID)
	if routeErr != nil || len(baselineRoutes) != 0 {
		t.Fatal("disposable VPC must have a complete empty route-table baseline")
	}
	report["baselineCash"] = beforeCash
	report["baselineRoutes"] = baselineRoutes
	report["quotedTotal"] = quote.TotalPrice
	if liveNATSaveReport(report) != nil {
		t.Fatal("private baseline report failed before purchase")
	}
	started := time.Now()
	natAttempted = true
	out, err := client.CreateNATInstance(ctx, in)
	if out != nil && out.NATInstance != nil {
		natID = out.NATInstance.UUID
		report["natID"] = natID
		report["lastState"] = out.NATInstance.Status
		report["autoRenew"] = out.AutoRenew
	}
	afterCash, cashErr := storageProjectCash(ctx, money)
	report["afterOrderCashAvailable"] = cashErr == nil
	if cashErr == nil {
		report["afterOrderCash"] = afterCash
		report["debit"] = beforeCash - afterCash
	}
	if err != nil {
		t.Fatal("NAT create failed; cleanup will use confirmed identity only")
	}
	if natID == "" || out.NATInstance.Status != "ACTIVE" || out.AutoRenew == nil || *out.AutoRenew {
		t.Fatal("NAT create did not confirm ACTIVE with renewal off")
	}
	if cashErr != nil {
		t.Fatal("NAT debit read failed")
	}
	report["afterOrderCash"] = afterCash
	report["debit"] = beforeCash - afterCash
	report["quotedTotal"] = out.TotalPrice
	if math.Abs((beforeCash-afterCash)-out.TotalPrice) > 1 {
		t.Fatal("NAT cash debit differs from the fresh create quote; reconcile unrelated transactions privately")
	}
	activeRoutes, routeErr := liveNATRoutes(ctx, client, vpcID)
	if routeErr != nil {
		t.Fatal("ACTIVE NAT route read failed")
	}
	report["activeRoutes"] = activeRoutes
	defaultRoutes := 0
	for _, table := range activeRoutes {
		for _, route := range table.Routes {
			if route.DestinationCIDRBlock == "0.0.0.0/0" {
				defaultRoutes++
			}
		}
	}
	if defaultRoutes != 1 {
		t.Fatal("ACTIVE NAT did not establish one default route")
	}
	resources, err := money.ListResources(ctx, nil)
	if err != nil {
		t.Fatal("NAT billing confirmation failed")
	}
	count := 0
	for _, row := range resources.Items {
		if row.Product == "vserver" && row.ArtifactType == "nat" && row.ArtifactID == natID {
			count++
			if row.RenewType != billing.RenewTypeManual || row.RenewPeriod != nil {
				t.Fatal("NAT billing renewal is not MANUAL/null")
			}
		}
	}
	if count != 1 {
		t.Fatal("NAT billing row is not unique")
	}
	t.Logf("NAT state=ACTIVE renewal=MANUAL create duration=%s monthly=%.0f total=%.0f VND", time.Since(started).Round(time.Second), out.MonthlyPrice, out.TotalPrice)
	natDeleteAttempted = true
	if _, err = client.DeleteNATInstance(ctx, &network.DeleteNATInstanceInput{ZoneID: zoneID, VPCID: vpcID, NATID: natID}); err != nil {
		t.Fatal("NAT delete failed; inspect leftovers before another action")
	}
	after, err := liveNATInventory(ctx, client, zoneID)
	if err != nil {
		t.Fatal("NAT absence confirmation failed")
	}
	for _, row := range after {
		if row.UUID == natID {
			t.Fatal("NAT remains after delete")
		}
	}
	natGone = true
	afterRoutes, routeErr := liveNATRoutes(ctx, client, vpcID)
	if routeErr != nil || len(afterRoutes) != 0 {
		t.Fatal("NAT route removal was not confirmed")
	}
	report["afterDeleteRoutes"] = afterRoutes
	if !liveNATBillingAbsent(ctx, money, natID) {
		t.Fatal("NAT billing row remains after delete")
	}
	report["billingAbsent"] = true
	if !liveNATDeleteVPC(ctx, client, vpcID) {
		t.Fatal("VPC delete did not settle within 10 minutes")
	}
	vpcGone = true
	finalCash, refundErr := liveNATRefund(ctx, money, beforeCash, afterCash)
	report["finalCash"] = finalCash
	report["refund"] = finalCash - afterCash
	report["netCost"] = beforeCash - finalCash
	if refundErr != nil {
		t.Fatal("NAT refund remains unresolved; reconcile the private report and transactions")
	}
	t.Logf("NAT debit=%.0f refund=%.0f net cost=%.0f VND", beforeCash-afterCash, finalCash-afterCash, beforeCash-finalCash)
	t.Logf("NAT absent; disposable VPC removed; total duration=%s", time.Since(started).Round(time.Second))
}

func liveNATDeleteVPC(ctx context.Context, client *network.Client, id string) bool {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		_, err := client.DeleteVPC(bounded, &network.DeleteVPCInput{VPCID: id})
		if err == nil || vngcloud.IsNotFound(err) {
			return true
		}
		var api *vngcloud.APIError
		inUse := errors.Is(err, network.ErrInUse)
		if errors.As(err, &api) && api.StatusCode == 400 {
			message := strings.ToLower(api.Message)
			inUse = inUse || (strings.Contains(message, "currently being used") && strings.Contains(message, "vnetwork"))
		}
		if !inUse {
			return false
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-bounded.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}
func liveNATInventory(ctx context.Context, client *network.Client, zone string) ([]network.NATInstance, error) {
	all := []network.NATInstance{}
	seen := map[string]bool{}
	total, pages := -1, -1
	for page := 1; page <= 50; page++ {
		out, err := client.ListNATInstances(ctx, &network.ListNATInstancesInput{ZoneID: zone, Page: page, Size: 1000})
		if err != nil {
			return nil, err
		}
		if out.Page != page || out.PageSize <= 0 || out.TotalItem < 0 || out.TotalPage < 0 || (out.TotalPage == 0 && (out.TotalItem != 0 || len(out.Items) != 0)) {
			return nil, errors.New("invalid NAT page")
		}
		if page == 1 {
			total, pages = out.TotalItem, out.TotalPage
		} else if total != out.TotalItem || pages != out.TotalPage {
			return nil, errors.New("changing NAT inventory")
		}
		for _, row := range out.Items {
			if seen[row.UUID] {
				return nil, errors.New("duplicate NAT identity")
			}
			seen[row.UUID] = true
			all = append(all, row)
		}
		if page >= pages {
			if len(all) != total {
				return nil, errors.New("incomplete NAT inventory")
			}
			return all, nil
		}
		if len(out.Items) == 0 {
			return nil, errors.New("NAT inventory made no progress")
		}
	}
	return nil, errors.New("NAT inventory exceeds scan bound")
}
func liveNATVPCs(ctx context.Context, client *network.Client) ([]network.VPC, error) {
	all := []network.VPC{}
	seen := map[string]bool{}
	total, pages := -1, -1
	for page := 1; page <= 50; page++ {
		out, err := client.ListVPCs(ctx, &network.ListVPCsInput{Page: page, Size: 1000})
		if err != nil {
			return nil, err
		}
		if out.Page != page || out.PageSize <= 0 || out.TotalItem < 0 || out.TotalPage < 0 || (out.TotalPage == 0 && (out.TotalItem != 0 || len(out.Items) != 0)) {
			return nil, errors.New("invalid VPC page")
		}
		if page == 1 {
			total, pages = out.TotalItem, out.TotalPage
		} else if total != out.TotalItem || pages != out.TotalPage {
			return nil, errors.New("changing VPC inventory")
		}
		for _, row := range out.Items {
			if row.UUID == "" || seen[row.UUID] {
				return nil, errors.New("invalid VPC identity")
			}
			seen[row.UUID] = true
			all = append(all, row)
		}
		if page >= pages {
			if len(all) != total {
				return nil, errors.New("incomplete VPC inventory")
			}
			return all, nil
		}
		if len(out.Items) == 0 {
			return nil, errors.New("VPC inventory made no progress")
		}
	}
	return nil, errors.New("VPC inventory exceeds scan bound")
}

// The SDK has no transaction-history read. Cash evidence cannot rule out
// offsetting unrelated credits or spending; the manager reconciles those.
func liveNATRefund(ctx context.Context, money *billing.Client, before, after float64) (float64, error) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	final := after
	for {
		observed, err := storageProjectCash(bounded, money)
		if err != nil {
			return final, errors.New("refund cash read failed")
		}
		final = observed
		if final > before+1 {
			return final, errors.New("cash exceeds the baseline")
		}
		if final > after {
			return final, nil
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-bounded.Done():
			timer.Stop()
			return final, errors.New("refund confirmation timed out")
		case <-timer.C:
		}
	}
}
func liveNATBillingAbsent(ctx context.Context, money *billing.Client, id string) bool {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		out, err := money.ListResources(bounded, nil)
		if err != nil {
			return false
		}
		present := false
		for _, row := range out.Items {
			if row.Product == "vserver" && row.ArtifactType == "nat" && row.ArtifactID == id {
				present = true
			}
		}
		if !present {
			return true
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-bounded.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}
func liveNATSaveReport(report map[string]any) error {
	dir := repoPath("examples/basic/output/raw/network")
	if os.MkdirAll(dir, 0o700) != nil {
		return errors.New("private report directory failed")
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return errors.New("private report encoding failed")
	}
	if os.Chmod(dir, 0o700) != nil {
		return errors.New("private report directory mode failed")
	}
	file, err := os.OpenFile(filepath.Join(dir, "nat-write-report.json"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.New("private report write failed")
	}
	defer func() { _ = file.Close() }()
	if file.Chmod(0o600) != nil {
		return errors.New("private report mode failed")
	}
	if _, err := file.Write(raw); err != nil {
		return errors.New("private report write failed")
	}
	if file.Close() != nil {
		return errors.New("private report close failed")
	}
	return nil
}
func liveNATRoutes(ctx context.Context, client *network.Client, vpc string) ([]network.RouteTable, error) {
	items := []network.RouteTable{}
	seen := map[string]bool{}
	count, total, pages := 0, -1, -1
	for page := 1; page <= 50; page++ {
		out, err := client.ListRouteTables(ctx, &network.ListRouteTablesInput{Page: page, Size: 1000})
		if err != nil {
			return nil, err
		}
		if out.Page != page || out.PageSize <= 0 || out.TotalItem < 0 || out.TotalPage < 0 || (out.TotalPage == 0 && (out.TotalItem != 0 || len(out.Items) != 0)) {
			return nil, errors.New("invalid route page")
		}
		if page == 1 {
			total, pages = out.TotalItem, out.TotalPage
		} else if total != out.TotalItem || pages != out.TotalPage {
			return nil, errors.New("changing route inventory")
		}
		for _, row := range out.Items {
			if row.UUID == "" || seen[row.UUID] {
				return nil, errors.New("invalid route identity")
			}
			seen[row.UUID] = true
			count++
			if row.NetworkID == vpc {
				items = append(items, row)
			}
		}
		if page >= pages {
			if count != total {
				return nil, errors.New("incomplete route inventory")
			}
			return items, nil
		}
		if len(out.Items) == 0 {
			return nil, errors.New("route inventory made no progress")
		}
	}
	return nil, errors.New("route inventory exceeds scan bound")
}

func TestNATHarnessOwnership(t *testing.T) {
	name, cidr := "vngcloud-live-nat-synthetic", "10.250.0.0/16"
	owned := network.VPC{UUID: "new-vpc", Name: name, CIDR: cidr}
	for _, tc := range []struct {
		name             string
		returned         network.VPC
		baseline, scoped []network.VPC
		valid            bool
	}{
		{"owned", owned, nil, []network.VPC{owned}, true},
		{"baseline ID", owned, []network.VPC{owned}, []network.VPC{owned}, false},
		{"wrong returned name", network.VPC{UUID: owned.UUID, Name: "other", CIDR: cidr}, nil, []network.VPC{owned}, false},
		{"wrong returned CIDR", network.VPC{UUID: owned.UUID, Name: name, CIDR: "10.251.0.0/16"}, nil, []network.VPC{owned}, false},
		{"absent from scope", owned, nil, nil, false},
		{"wrong scoped name", owned, nil, []network.VPC{{UUID: owned.UUID, Name: "other", CIDR: cidr}}, false},
		{"ambiguous identity", owned, nil, []network.VPC{owned, owned}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if liveNATOwnedVPC(&network.CreateVPCOutput{VPC: tc.returned}, tc.baseline, tc.scoped, name, cidr) != tc.valid {
				t.Fatal("VPC ownership guard failed")
			}
		})
	}
}

// The scoped inventory comes from the selected project's regional VPC route.
func liveNATOwnedVPC(created *network.CreateVPCOutput, baseline, scoped []network.VPC, name, cidr string) bool {
	if created == nil || created.VPC.UUID == "" || created.VPC.Name != name || created.VPC.CIDR != cidr {
		return false
	}
	for _, row := range baseline {
		if row.UUID == created.VPC.UUID {
			return false
		}
	}
	matches := 0
	for _, row := range scoped {
		if row.UUID == created.VPC.UUID {
			if row.Name != name || row.CIDR != cidr {
				return false
			}
			matches++
		} else if row.Name == name {
			return false
		}
	}
	return matches == 1
}
