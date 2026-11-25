//go:build livewrite

package livetest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
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
	"danny.vn/vngcloud/portal"
)

const liveNATPrefix = "vngcloud-live-nat-"

func TestLiveWriteNAT(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_NAT_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_NAT_WRITE=1 for the approved paid NAT run")
	}
	region, err := natRegion(os.Getenv("VNGCLOUD_LIVE_NAT_REGION"))
	if err != nil {
		t.Fatal("invalid NAT region")
	}
	project := os.Getenv("VNGCLOUD_PROJECT_ID")
	if natProjectPreflight(region, project) != nil {
		t.Fatal("HCM requires an explicit path-safe VNGCLOUD_PROJECT_ID")
	}
	hold, err := newNATHoldRegion(os.Getenv("VNGCLOUD_LIVE_NAT_HOLD_DIR"), os.Getenv("VNGCLOUD_LIVE_NAT_HOLD_MAX"), region)
	if err != nil {
		t.Fatal("invalid NAT hold configuration; parent directory must be private and stable")
	}
	defer hold.Close()
	maxPrice, err := natPreflight(region, os.Getenv("VNGCLOUD_LIVE_NAT_MAX_PRICE"), hold.root != nil)
	if err != nil {
		t.Fatal("invalid NAT maximum price or missing HCM hold directory")
	}
	var cleanupDeadline, holdDeadline time.Time
	if hold.root != nil {
		deadline, ok := t.Deadline()
		if !ok {
			t.Fatal("NAT hold requires a test deadline; use -timeout=120m")
		}
		cleanupDeadline, holdDeadline, err = natHoldDeadlines(time.Now(), deadline, hold.max)
		if err != nil {
			t.Fatal("NAT hold requires at least 86 minutes plus the hold maximum; use -timeout=120m")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	natID := ""
	deleteTransport := &natDeleteTransport{base: http.DefaultTransport, natID: &natID}
	options := []vngcloud.LoadOption{vngcloud.WithRegion(region), vngcloud.WithTransport(deleteTransport)}
	if region == "hcm-3" {
		options = append(options, vngcloud.WithProjectID(project))
	}
	cfg := liveWriteConfig(ctx, t, options...)
	if core.ClientOf(cfg).Region() != region || !core.ClientOf(cfg).UsesIAMUserLogin() {
		t.Fatal("live NAT writes require SDK IAM-user login in the selected region")
	}
	if region == "hcm-3" {
		if core.ClientOf(cfg).ConfiguredProjectID() != project {
			t.Fatal("HCM project scope does not match the explicit project")
		}
		if _, err = natReadCreditCap(ctx, billing.New(cfg), maxPrice); err != nil {
			t.Fatal("available NAT credit could not be verified")
		}
		quotas, quotaErr := portal.New(cfg).ListQuotaUsed(ctx, nil)
		if quotaErr != nil || quotas == nil || !natQuotaAvailable(quotas.Items) {
			t.Fatal("VPC quota has no verified free slot")
		}
	}
	client := network.New(cfg)
	zones, err := client.ListVNetworkRegions(ctx, nil)
	if err != nil {
		t.Fatal("NAT region discovery failed")
	}
	zoneID := ""
	matches := 0
	for _, zone := range zones.Items {
		if strings.TrimRight(zone.DashboardURL, "/") == "https://"+region+"-vnetwork.console.greennode.ai" || strings.TrimRight(zone.GatewayURL, "/") == "https://"+region+"-vnetwork.console.greennode.ai/vnetwork-gateway" {
			zoneID = zone.UUID
			matches++
		}
	}
	if matches != 1 || zoneID == "" {
		t.Fatal("selected region needs one region-level NAT scope")
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
	report := map[string]any{"region": region, "name": name, "baselineNATIDs": baselineNATIDs, "baselineVPCIDs": baselineVPCIDs, "transactionReconciliationRequired": true}
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
	expectedPackage := ""
	natAttempted, natGone, vpcGone, cleanupBlocked := false, false, false, false
	if vpcID != "" {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 25*time.Minute)
			if !cleanupDeadline.IsZero() {
				cleanupCancel()
				cleanupCtx, cleanupCancel = context.WithDeadline(context.Background(), cleanupDeadline)
			}
			defer cleanupCancel()
			defer func() {
				report["natID"] = natID
				report["natGone"] = natGone
				report["vpcGone"] = vpcGone
				if !vpcGone {
					t.Error("NAT cleanup left resources; inspect the private NAT report")
				}
				if natAttempted && !deleteTransport.valid() {
					t.Error("NAT DELETE contract was not confirmed")
				}
			}()
			if natAttempted && !natGone && !cleanupBlocked {
				natGone = liveNATCleanupDelete(cleanupCtx, client, report, baseline, name, core.ClientOf(cfg).ProjectID(), zoneID, vpcID, expectedPackage, natID, deleteTransport.sent.Load())
			}
			billingAbsent, routesRestored := false, false
			if !vpcGone && !cleanupBlocked && natAttempted && natGone && natID != "" {
				billingAbsent = liveNATBillingAbsent(cleanupCtx, billing.New(cfg), natID)
				routes, routesErr := liveNATRoutes(cleanupCtx, cfg, vpcID)
				routesRestored = routesErr == nil && len(routes) == 0
				report["billingAbsent"] = billingAbsent
				report["routesRestored"] = routesRestored
			}
			if !vpcGone && !cleanupBlocked && natVPCDeleteAllowed(natAttempted, natGone, billingAbsent, routesRestored) {
				vpcGone = liveNATDeleteVPC(cleanupCtx, client, vpcID)
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
	az, packageID, err := natSelectPlacement(ctx, client, region, zoneID, offers.Items)
	if err != nil {
		t.Fatal("NAT placement or package selection failed")
	}
	report["availabilityZoneID"] = az
	report["packageID"] = packageID
	expectedPackage = packageID
	in := &network.CreateNATInstanceInput{Name: name, ZoneID: zoneID, AvailabilityZoneID: az, PackageID: packageID, VPCID: vpcID, MaxPrice: maxPrice}
	quote, err := client.QuoteCreateNATInstance(ctx, in)
	if err != nil {
		t.Fatal("NAT quote failed")
	}
	if quote.TotalPrice <= 0 || quote.TotalPrice > in.MaxPrice {
		t.Fatal("NAT quote is outside the approved cap")
	}
	t.Logf("catalog zones=%d package=nat.s-standard quote monthly=%.0f total=%.0f VND", len(offers.Items), quote.MonthlyPrice, quote.TotalPrice)
	money := billing.New(cfg)
	beforeCash, cashErr := storageProjectCash(ctx, money)
	if cashErr != nil {
		t.Fatal("NAT baseline cash read failed")
	}
	baselineRoutes, routeErr := liveNATRoutes(ctx, cfg, vpcID)
	if routeErr != nil || len(baselineRoutes) != 0 {
		t.Fatal("disposable VPC must have a complete empty route-table baseline")
	}
	report["baselineCash"] = beforeCash
	report["baselineRoutes"] = baselineRoutes
	report["quotedTotal"] = quote.TotalPrice
	if liveNATSaveReport(report) != nil {
		t.Fatal("private baseline report failed before purchase")
	}
	if region == "hcm-3" {
		in.MaxPrice, err = natReadCreditCap(ctx, money, maxPrice)
		if err != nil {
			t.Fatal("fresh NAT credit could not be verified")
		}
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
	var afterCash float64
	readyForHold := false
	cashRead := false
	if natID != "" && out != nil && out.NATInstance != nil && out.NATInstance.Status == "ACTIVE" {
		defer func() {
			reconcileCtx, cleanupCancel := natReconcileContext(ctx, cleanupDeadline, readyForHold)
			defer cleanupCancel()
			runNATPostActive(reconcileCtx, func() error {
				if !readyForHold {
					return nil
				}
				holdCtx := context.Background()
				if !holdDeadline.IsZero() {
					var cancel context.CancelFunc
					holdCtx, cancel = context.WithDeadline(holdCtx, holdDeadline)
					defer cancel()
				}
				return hold.wait(holdCtx, natHoldIDs{NAT: natID, VPC: vpcID, VNetworkRegion: zoneID, Project: core.ClientOf(cfg).ProjectID()}, func() { t.Log("hold: waiting") }, natHoldSleep)
			}, func(ctx context.Context) {
				state := natReconcileState{cfg: cfg, client: client, money: money, report: report, zone: zoneID, vpc: vpcID, nat: natID, name: name, project: core.ClientOf(cfg).ProjectID(), pkg: expectedPackage, baseline: baseline, before: beforeCash, after: &afterCash, cashErr: &cashErr, cashRead: &cashRead, natGone: &natGone, vpcGone: &vpcGone}
				if err := state.run(ctx); err != nil {
					cleanupBlocked = true
					report["reconciliationError"] = err.Error()
					t.Fatal("NAT reconciliation failed; inspect the private NAT report")
				}
				t.Log("NAT after delete=absent billing=absent")
				t.Logf("NAT debit=%.0f refund=%.0f net cost=%.0f VND", beforeCash-afterCash, report["refund"], report["netCost"])
				t.Logf("NAT absent; disposable VPC removed; total duration=%s", time.Since(started).Round(time.Second))
			}, func(holdErr error) {
				if errors.Is(holdErr, errNATHoldUnlink) {
					t.Error("NAT hold IDs removal failed")
				}
				if errors.Is(holdErr, errNATHoldTimeout) {
					t.Error("NAT hold timed out")
				} else if holdErr != nil {
					t.Error("NAT hold failed")
				}
			})
		}()
	}
	afterCash, cashErr = storageProjectCash(ctx, money)
	cashRead = cashErr == nil
	recordNATPostOrderCash(report, beforeCash, afterCash, cashErr)
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
	activeRoutes, routeErr := liveNATRoutes(ctx, cfg, vpcID)
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
	readyForHold = true
}

func natReconcileContext(ctx context.Context, cleanupDeadline time.Time, ready bool) (context.Context, context.CancelFunc) {
	if !cleanupDeadline.IsZero() {
		return context.WithDeadline(context.Background(), cleanupDeadline)
	}
	if !ready {
		return context.WithTimeout(context.Background(), 25*time.Minute)
	}
	return context.WithCancel(ctx)
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
		if out == nil {
			return false
		}
		absent, valid := natBillingRowsAbsent(out.Items, id)
		if !valid {
			return false
		}
		if absent {
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
func liveNATRoutes(ctx context.Context, cfg vngcloud.Config, vpc string) ([]network.RouteTable, error) {
	items := []network.RouteTable{}
	seen := map[string]bool{}
	count, total, pages := 0, -1, -1
	for page := 1; page <= 50; page++ {
		out, err := liveNATRoutePage(ctx, cfg, page)
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
			if !natRouteIdentityValid(row) || seen[row.UUID] {
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
