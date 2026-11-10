//go:build livewrite

package livetest_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/volume"
)

const floatingIPCap = 500000

// TestLiveWriteFloatingIP needs fresh manager approval and verified console
// release permissions. Credit-history access, checkpoint captures, and billing
// attribution remain manager work under docs/design/vserver-floating-ips-checks.md.
// Run alone, with -timeout 60m. No guest access or SDK floating IP write runs.
func TestLiveWriteFloatingIP(t *testing.T) {
	run, err := floatingIPGates(os.Getenv("VNGCLOUD_LIVE_WRITE"), os.Getenv("VNGCLOUD_LIVE_FLOATING_IP"), os.Getenv("VNGCLOUD_LIVE_MAX_VND"))
	if err != nil {
		t.Fatal("VNGCLOUD_LIVE_MAX_VND must equal 500000")
	}
	if !run {
		t.Skip("requires VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_FLOATING_IP=1")
	}
	region, err := floatingIPRegion(os.Getenv("VNGCLOUD_LIVE_FLOATING_IP_REGION"))
	if err != nil {
		t.Fatal(err)
	}

	// Reserve ten minutes of the sixty-minute envelope for fresh cleanup.
	deadline := time.Now().Add(60 * time.Minute)
	if testDeadline, ok := t.Deadline(); ok && testDeadline.Before(deadline) {
		deadline = testDeadline
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.Add(-10*time.Minute))
	defer cancel()
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatal("run name generation failed")
	}
	name := "vngcloud-live-fip-" + suffix
	capture := &floatingIPCapture{dir: repoPath("examples", "basic", "output", "raw", "compute", "floating-ip", name)}
	if err := os.MkdirAll(filepath.Dir(capture.dir), 0o700); err != nil {
		t.Fatal("private capture parent directory creation failed")
	}
	if err := os.Mkdir(capture.dir, 0o700); err != nil {
		t.Fatal("private capture directory creation failed")
	}
	guard := newFloatingIPTransport(region)
	cfg := liveWriteConfig(ctx, t, vngcloud.WithRegion(region), vngcloud.WithTransport(guard), vngcloud.WithResponseCapture(capture.hook))
	if cfg.Region() != region || !core.ClientOf(cfg).UsesIAMUserLogin() {
		t.Fatal("requires IAM user login in the selected region")
	}
	t.Logf("selected region: %s", region)
	r := newFloatingIPRun(t, cfg, guard, capture, name)
	before := r.balance(ctx)
	t.Cleanup(func() {
		cleanupDeadline := time.Now().Add(10 * time.Minute)
		if limit := deadline.Add(-time.Second); limit.Before(cleanupDeadline) {
			cleanupDeadline = limit
		}
		cleanupCtx, cleanupCancel := context.WithDeadline(context.Background(), cleanupDeadline)
		defer cleanupCancel()
		defer r.endingBalance(cleanupCtx, before)
		if !r.cleanup(cleanupCtx) {
			r.leftovers()
		}
	})
	r.inventory(ctx)
	zoneRead, err := portal.New(cfg).ListZones(ctx, nil)
	if err != nil {
		t.Fatal("enabled-zone read failed")
	}
	zone, err := floatingIPZone(zoneRead.Items, os.Getenv("VNGCLOUD_LIVE_FLOATING_IP_ZONE_ID"))
	if err != nil {
		t.Fatal("selected zone is missing, disabled, or ambiguous")
	}
	input, err := r.spec(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	// Quote before creating parents, so a refusal alone triggers no deletes.
	r.quote(ctx, input)
	if capture.failed {
		t.Fatal("baseline capture failed; no writes allowed")
	}
	r.parents(ctx, input)
	r.quote(ctx, input)
	if capture.failed {
		r.ledger.blocked = true
		t.Fatal("pre-order capture failed; cleanup needs reconciliation")
	}
	if err := r.purchase(ctx, input); err != nil {
		t.Fatal(err)
	}
	r.checkDebit(ctx, before)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	if err := r.waitForConsoleRelease(ctx, ticker.C); err != nil {
		t.Fatal(err)
	}
}

func newFloatingIPRun(t *testing.T, cfg vngcloud.Config, guard *floatingIPTransport, capture *floatingIPCapture, name string) *floatingIPRun {
	return &floatingIPRun{
		t: t, compute: compute.New(cfg), network: network.New(cfg), volume: volume.New(cfg),
		billing: billing.New(cfg), guard: guard, capture: capture, name: name, region: cfg.Region(),
		ledger: &floatingIPLedger{ids: map[string]string{}, seen: map[string]map[string]bool{}, baseline: map[string]map[string]bool{}, attempted: map[string]bool{}},
	}
}

func (r *floatingIPRun) purchase(ctx context.Context, input *compute.CreateServerInput) error {
	r.serverAttempted = true
	for _, kind := range []string{"server", "volume", "floating IP"} {
		r.ledger.attempted[kind] = true
	}
	r.guard.arm(http.MethodPost, "/servers")
	created, createErr := r.compute.CreateServer(ctx, input)
	r.guard.disarm()
	if created != nil {
		r.rememberServer(created.Server)
		if err := r.capture.saveSDK("compute.CreateServer", created); err != nil {
			return err
		}
	}
	if createErr != nil || created == nil || r.ledger.blocked {
		r.ledger.blocked = true
		return errors.New("server create failed or is uncertain; do not repeat the order")
	}
	if !floatingIPPriceOK(created.MonthlyPrice) || created.MonthlyPrice > input.MaxPrice {
		r.ledger.blocked = true
		return errors.New("server estimate failed the approved price guard")
	}
	read, err := r.getServer(ctx, r.ledger.ids["server"])
	if err != nil || read == nil || r.ledger.blocked {
		return errors.New("created-server read or ownership confirmation failed")
	}
	s := read.Server
	rows, err := r.wan(ctx)
	if err != nil || s.Status != "ACTIVE" || s.Name != r.name || r.ledger.ids["volume"] == "" ||
		!floatingIPLinked(s, rows, r.ledger.ids["floating IP"], r.interfaceID, input.VPCID, input.SubnetID) || r.capture.failed ||
		r.capture.decoded["compute.CreateServer"] == 0 || r.capture.decoded["compute.GetServer"] == 0 || r.capture.saved["network.ListWANIPs"] == 0 {
		r.ledger.blocked = true
		return errors.New("server/WAN linkage or private evidence failed; manager reconciliation required")
	}
	r.t.Log("step 2: server/WAN linkage passed")
	return nil
}

func (r *floatingIPRun) waitForConsoleRelease(ctx context.Context, ticks <-chan time.Time) error {
	r.t.Logf("Release the run-owned floating IP in the %s console now; this test waits for its absence.", r.region)
	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer waitCancel()
	for {
		select {
		case <-waitCtx.Done():
			return errors.New("console release timed out or was canceled; server deletion refused")
		case <-ticks:
		}
		rows, err := r.wan(waitCtx)
		if err != nil || r.capture.failed {
			return errors.New("console release inventory failed; server deletion refused")
		}
		if !slices.ContainsFunc(rows, func(row network.WANIP) bool { return row.UUID == r.ledger.ids["floating IP"] }) {
			r.released = true
			delete(r.ledger.ids, "floating IP")
			r.t.Log("console release: absence passed")
			return nil
		}
	}
}

func floatingIPRegion(value string) (string, error) {
	switch value {
	case "":
		return "hcm-3", nil
	case "hcm-3", "han-1":
		return value, nil
	default:
		return "", errors.New("VNGCLOUD_LIVE_FLOATING_IP_REGION must be hcm-3 or han-1")
	}
}

func newFloatingIPTransport(region string) *floatingIPTransport {
	return &floatingIPTransport{base: http.DefaultTransport, host: region + ".console.greennode.ai"}
}

func floatingIPGates(write, floating, cap string) (bool, error) {
	if write != "1" || floating != "1" {
		return false, nil
	}
	if cap != "500000" {
		return false, errors.New("unapproved cap")
	}
	return true, nil
}

func floatingIPPriceOK(price float64) bool {
	return !math.IsNaN(price) && !math.IsInf(price, 0) && price > 0 && price <= floatingIPCap
}

func floatingIPZone(zones []portal.Zone, requested string) (string, error) {
	selected := ""
	seen := map[string]bool{}
	for _, zone := range zones {
		if zone.UUID == nil || *zone.UUID == "" || seen[*zone.UUID] {
			return "", errors.New("invalid zone inventory")
		}
		seen[*zone.UUID] = true
		if requested != "" && *zone.UUID != requested {
			continue
		}
		if zone.IsEnabled != nil && *zone.IsEnabled && floatingIPID.MatchString(*zone.UUID) && selected == "" {
			selected = *zone.UUID
		}
	}
	if selected == "" {
		return "", errors.New("no enabled selected zone")
	}
	return selected, nil
}

var floatingIPID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

type floatingIPLedger struct {
	ids       map[string]string
	seen      map[string]map[string]bool
	baseline  map[string]map[string]bool
	attempted map[string]bool
	blocked   bool
}

func (l *floatingIPLedger) remember(kind, id string) {
	if id == "" {
		return
	}
	if l.seen[kind] == nil {
		l.seen[kind] = map[string]bool{}
	}
	l.seen[kind][id] = true
	if !floatingIPID.MatchString(id) || id == "auto" || l.baseline[kind][id] || (l.ids[kind] != "" && l.ids[kind] != id) {
		l.blocked = true
		return
	}
	l.ids[kind] = id
}

type floatingIPCleanupStep struct {
	kind   string
	remove func(context.Context, string) error
}

func (l *floatingIPLedger) clean(ctx context.Context, steps []floatingIPCleanupStep) bool {
	if l.blocked {
		return false
	}
	for _, step := range steps {
		if l.blocked {
			return false
		}
		if id := l.ids[step.kind]; id != "" {
			if ctx.Err() != nil || step.remove(ctx, id) != nil {
				l.blocked = true
				return false
			}
			delete(l.ids, step.kind)
			delete(l.seen, step.kind)
			delete(l.attempted, step.kind)
		}
	}
	return !l.blocked
}

// The permit is consumed before sending, including a failed dial or redirect.
// SDK retries cannot send a second create or an unapproved cleanup write.
type floatingIPTransport struct {
	mu             sync.Mutex
	base           http.RoundTripper
	host           string
	method, suffix string
}

func (g *floatingIPTransport) arm(method, suffix string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.method, g.suffix = method, suffix
}

func (g *floatingIPTransport) disarm() { g.arm("", "") }

func (g *floatingIPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	allowedHost := req.URL.Scheme == "https" && req.URL.User == nil &&
		(req.URL.Host == g.host || req.URL.Host == "dashboard.console.greennode.ai")
	allowed := allowedHost && (req.Method == http.MethodGet ||
		(req.Method == http.MethodPost && req.URL.Host == g.host && strings.HasSuffix(req.URL.Path, "/v1/price")))
	if allowedHost && req.URL.Host == g.host && g.method != "" && req.Method == g.method && strings.HasSuffix(req.URL.Path, g.suffix) {
		allowed = true
		g.method, g.suffix = "", ""
	}
	g.mu.Unlock()
	if !allowed {
		return nil, errors.New("floating IP harness refused an unapproved request")
	}
	response, err := g.base.RoundTrip(req)
	if response != nil && response.Body != nil {
		response.Body = http.MaxBytesReader(nil, response.Body, 8<<20)
	}
	return response, err
}

type floatingIPCapture struct {
	mu          sync.Mutex
	dir         string
	count       int
	failed      bool
	wanComplete bool
	saved       map[string]int
	decoded     map[string]int
}

func (c *floatingIPCapture) hook(response vngcloud.ResponseCapture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	if response.Operation == "network.ListWANIPs" {
		var page struct {
			Rows  json.RawMessage `json:"listData"`
			Page  *int            `json:"page"`
			Size  *int            `json:"pageSize"`
			Pages *int            `json:"totalPage"`
			Total *int            `json:"totalItem"`
		}
		c.wanComplete = json.Unmarshal(response.Body, &page) == nil && page.Page != nil && page.Size != nil &&
			page.Pages != nil && page.Total != nil && len(page.Rows) > 0 && page.Rows[0] == '['
	}
	body := json.RawMessage(response.Body)
	if len(body) == 0 {
		body = nil
	}
	data, err := json.Marshal(struct {
		Operation string          `json:"operation"`
		Method    string          `json:"method"`
		Status    int             `json:"status"`
		Body      json.RawMessage `json:"body"`
	}{response.Operation, response.Method, response.StatusCode, body})
	if err == nil {
		path := filepath.Join(c.dir, fmt.Sprintf("%04d-%s.json", c.count, response.Operation))
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		c.failed = true
	} else {
		if c.saved == nil {
			c.saved = map[string]int{}
		}
		c.saved[response.Operation]++
	}
}

// Sensitive SDK operations suppress raw captures. Keep their safe returned
// models separate from raw evidence; neither file format establishes ownership.
func (c *floatingIPCapture) saveSDK(operation string, output any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	data, err := json.Marshal(struct {
		Operation string `json:"operation"`
		SDK       any    `json:"sdk"`
	}{operation, output})
	if err == nil {
		path := filepath.Join(c.dir, fmt.Sprintf("%04d-%s-sdk.json", c.count, operation))
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		c.failed = true
		return errors.New("private decoded SDK output save failed")
	}
	if c.decoded == nil {
		c.decoded = map[string]int{}
	}
	c.decoded[operation]++
	return nil
}

type floatingIPRun struct {
	t                 *testing.T
	compute           *compute.Client
	network           *network.Client
	volume            *volume.Client
	billing           *billing.Client
	guard             *floatingIPTransport
	capture           *floatingIPCapture
	ledger            *floatingIPLedger
	name, interfaceID string
	region            string
	serverAttempted   bool
	released          bool
	vpcs              []network.VPC
}

func (r *floatingIPRun) getServer(ctx context.Context, id string) (*compute.GetServerOutput, error) {
	out, err := r.compute.GetServer(ctx, &compute.GetServerInput{ServerID: id})
	if out != nil {
		r.rememberServer(out.Server)
	}
	return out, errors.Join(err, r.capture.saveSDK("compute.GetServer", out))
}

func (r *floatingIPRun) quote(ctx context.Context, input *compute.CreateServerInput) {
	r.t.Helper()
	quote, err := r.compute.QuoteCreateServer(ctx, input)
	if err != nil || quote == nil || !floatingIPPriceOK(quote.OptimumPrice) {
		// A failed quote never authorizes a delete of already created parents.
		r.ledger.blocked = true
		r.t.Fatal("quote failed or exceeds the approved cap; no server order sent")
	}
	input.MaxPrice = quote.OptimumPrice
	r.t.Log("quote: cap check passed")
}

func (r *floatingIPRun) balance(ctx context.Context) float64 {
	r.t.Helper()
	out, err := r.billing.GetBalances(ctx, &billing.GetBalancesInput{})
	if err != nil || out == nil || out.Balances.Cash == nil || math.IsNaN(*out.Balances.Cash) || math.IsInf(*out.Balances.Cash, 0) {
		r.t.Fatal("balance read failed")
	}
	if _, err := r.billing.GetCurrentPeriodCost(ctx, &billing.GetCurrentPeriodCostInput{}); err != nil {
		r.t.Fatal("current-period cost read failed")
	}
	r.t.Log("billing baseline read: passed")
	return *out.Balances.Cash
}

func (r *floatingIPRun) checkDebit(ctx context.Context, before float64) {
	r.t.Helper()
	out, err := r.billing.GetBalances(ctx, &billing.GetBalancesInput{})
	if err != nil || out == nil || out.Balances.Cash == nil || !floatingIPBalanceOK(before, *out.Balances.Cash) {
		r.t.Error("post-create balance check failed; manager reconciliation required")
		return
	}
	r.t.Log("post-create balance cap check: passed")
}

func floatingIPBalanceOK(before, after float64) bool {
	return !math.IsNaN(after) && !math.IsInf(after, 0) && before-after <= floatingIPCap
}

func (r *floatingIPRun) endingBalance(ctx context.Context, before float64) {
	out, err := r.billing.GetBalances(ctx, &billing.GetBalancesInput{})
	passed := err == nil && out != nil && out.Balances.Cash != nil && floatingIPBalanceOK(before, *out.Balances.Cash)
	r.t.Logf("ending balance cap check: %v", passed)
	if !passed {
		r.t.Error("ending balance read or cap check failed")
	}
	if _, err := r.billing.GetCurrentPeriodCost(ctx, &billing.GetCurrentPeriodCostInput{}); err != nil || r.capture.failed {
		r.t.Error("ending cost read or private capture failed")
	}
}

// Only a complete, stable, bounded walk can prove that an owned ID is absent.
func floatingIPWalk[T any](ctx context.Context, fetch func(context.Context, int) (*core.PagedList[T], error), id func(T) string) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var rows []T
	seen := map[string]bool{}
	total, pages := -1, -1
	for page := 1; page <= 50; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, err := fetch(ctx, page)
		if err != nil {
			return nil, err
		}
		if out == nil || out.Page != page || out.PageSize != 1000 || out.TotalItem < 0 ||
			out.TotalPage < 0 || out.TotalPage > 50 || len(out.Items) > 1000 {
			return nil, errors.New("invalid inventory metadata")
		}
		if total == -1 {
			total, pages = out.TotalItem, out.TotalPage
		}
		if total != out.TotalItem || pages != out.TotalPage || total > 50000 ||
			(total > 0 && pages != (total+999)/1000) || (total == 0 && pages > 1) {
			return nil, errors.New("inconsistent inventory metadata")
		}
		for _, row := range out.Items {
			key := id(row)
			if !floatingIPID.MatchString(key) || seen[key] {
				return nil, errors.New("invalid inventory identity")
			}
			seen[key] = true
			rows = append(rows, row)
		}
		if page >= pages {
			if len(rows) != total {
				return nil, errors.New("incomplete inventory")
			}
			return rows, nil
		}
		if len(out.Items) != 1000 {
			return nil, errors.New("inventory made incomplete progress")
		}
	}
	return nil, errors.New("inventory page bound exceeded")
}

func (r *floatingIPRun) wan(ctx context.Context) ([]network.WANIP, error) {
	return floatingIPWalk(ctx, func(ctx context.Context, page int) (*network.ListWANIPsOutput, error) {
		r.capture.wanComplete = false
		out, err := r.network.ListWANIPs(ctx, &network.ListWANIPsInput{Page: page, Size: 1000})
		if err == nil && !r.capture.wanComplete {
			return nil, errors.New("WAN envelope lacks complete metadata")
		}
		return out, err
	}, func(row network.WANIP) string { return row.UUID })
}

func (r *floatingIPRun) inventory(ctx context.Context) {
	r.t.Helper()
	var err error
	r.vpcs, err = floatingIPWalk(ctx, func(ctx context.Context, page int) (*network.ListVPCsOutput, error) {
		return r.network.ListVPCs(ctx, &network.ListVPCsInput{Page: page, Size: 1000})
	}, func(v network.VPC) string { return v.UUID })
	if err == nil {
		r.ledger.baseline["vpc"] = map[string]bool{}
		for _, vpc := range r.vpcs {
			r.ledger.baseline["vpc"][vpc.UUID] = true
		}
		r.ledger.baseline["subnet"] = map[string]bool{}
		for id := range r.ledger.baseline["vpc"] {
			out, readErr := r.network.ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{VPCID: id})
			if readErr != nil {
				err = readErr
				break
			}
			for _, subnet := range out.Items {
				r.ledger.baseline["subnet"][subnet.UUID] = true
			}
		}
	}
	if err != nil {
		r.t.Fatal("baseline network inventory failed")
	}
	r.ledger.baseline["server"], err = floatingIPInventory(ctx, func(ctx context.Context, page int) (*compute.ListServersOutput, error) {
		return r.compute.ListServers(ctx, &compute.ListServersInput{Page: page, Size: 1000})
	}, func(s compute.Server) string { return s.UUID })
	if err != nil {
		r.t.Fatal("baseline server inventory failed")
	}
	r.ledger.baseline["volume"], err = floatingIPInventory(ctx, func(ctx context.Context, page int) (*volume.ListVolumesOutput, error) {
		return r.volume.ListVolumes(ctx, &volume.ListVolumesInput{Page: page, Size: 1000})
	}, func(v volume.Volume) string { return v.UUID })
	if err != nil {
		r.t.Fatal("baseline volume inventory failed")
	}
	r.ledger.baseline["group"], err = floatingIPInventory(ctx, func(ctx context.Context, page int) (*network.ListSecurityGroupsOutput, error) {
		return r.network.ListSecurityGroups(ctx, &network.ListSecurityGroupsInput{Page: page, Size: 1000})
	}, func(g network.SecurityGroup) string { return g.ID })
	if err != nil {
		r.t.Fatal("baseline group inventory failed")
	}
	r.ledger.baseline["key"], err = floatingIPInventory(ctx, func(ctx context.Context, page int) (*compute.ListSSHKeysOutput, error) {
		return r.compute.ListSSHKeys(ctx, &compute.ListSSHKeysInput{Page: page, Size: 1000})
	}, func(k compute.SSHKey) string { return k.ID })
	if err != nil {
		r.t.Fatal("baseline key inventory failed")
	}
	rows, err := r.wan(ctx)
	if err != nil {
		r.t.Fatal("baseline WAN inventory failed")
	}
	r.ledger.baseline["floating IP"] = map[string]bool{}
	for _, row := range rows {
		r.ledger.baseline["floating IP"][row.UUID] = true
	}
	for _, kind := range []string{"vpc", "subnet", "server", "volume", "group", "key", "floating IP"} {
		r.t.Logf("baseline %s count: %d", kind, len(r.ledger.baseline[kind]))
	}
}

func floatingIPInventory[T any](ctx context.Context, fetch func(context.Context, int) (*core.PagedList[T], error), id func(T) string) (map[string]bool, error) {
	rows, err := floatingIPWalk(ctx, fetch, id)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, row := range rows {
		ids[id(row)] = true
	}
	return ids, nil
}

func (r *floatingIPRun) spec(ctx context.Context, zone string) (*compute.CreateServerInput, error) {
	r.t.Helper()
	input := &compute.CreateServerInput{Name: r.name, ZoneID: zone, RootDiskSize: 20, AttachFloatingIP: true, AllowOpenLogin: false}
	flavorZones, err := r.compute.ListFlavorZones(ctx, &compute.ListFlavorZonesInput{ZoneID: zone})
	if err != nil {
		return nil, errors.New("flavor-zone read failed")
	}
	for _, fz := range flavorZones.Items {
		flavors, err := r.compute.ListFlavors(ctx, &compute.ListFlavorsInput{FlavorZoneID: fz.ID})
		if err != nil {
			return nil, errors.New("flavor read failed")
		}
		for _, f := range flavors.Items {
			if f.Name == "s2-general-1x2" && !f.IsSoldOut {
				if input.FlavorID != "" && input.FlavorID != f.FlavorID {
					return nil, errors.New("flavor selection is ambiguous")
				}
				input.FlavorID = f.FlavorID
			}
		}
	}
	if input.FlavorID == "" {
		return nil, errors.New("s2-general-1x2 missing in selected region or zone")
	}
	images, err := r.compute.ListOSImages(ctx, &compute.ListOSImagesInput{ZoneID: zone})
	if err != nil {
		return nil, errors.New("image read failed")
	}
	for _, img := range images.Items {
		if strings.Contains(strings.ToLower(img.ImageVersion), "ubuntu") && strings.Contains(img.ImageVersion, "24.04") &&
			img.Licence != nil && !*img.Licence && img.PackageLimit.DiskSize <= 20 {
			input.ImageID = img.ID
			break
		}
	}
	if input.ImageID == "" {
		return nil, errors.New("Ubuntu 24.04 image unavailable in selected region or zone")
	}
	// Catalog parents can name a disabled zone. Read the chosen class directly;
	// the successful server quote verifies this type in the selected zone.
	typeZones, err := r.volume.ListVolumeTypeZones(ctx, nil)
	if err != nil {
		return nil, errors.New("disk catalog read failed")
	}
	class := "NVME"
	for _, tz := range typeZones.Items {
		if strings.EqualFold(tz.Name, "SSD") {
			class = "SSD"
			break
		}
	}
	requested := os.Getenv("VNGCLOUD_LIVE_FLOATING_IP_ROOT_DISK_TYPE_ID")
	for _, tz := range typeZones.Items {
		if !strings.EqualFold(tz.Name, class) {
			continue
		}
		types, err := r.volume.ListVolumeTypes(ctx, &volume.ListVolumeTypesInput{VolumeTypeZoneID: tz.ID})
		if err != nil {
			return nil, fmt.Errorf("%s type read failed", class)
		}
		for _, vt := range types.Items {
			if vt.IOPS != 3000 || vt.MinSize > 20 || vt.MaxSize < 20 || (requested != "" && requested != vt.ID) {
				continue
			}
			if input.RootDiskTypeID != "" && input.RootDiskTypeID != vt.ID {
				return nil, fmt.Errorf("%s selection is ambiguous; set VNGCLOUD_LIVE_FLOATING_IP_ROOT_DISK_TYPE_ID", class)
			}
			input.RootDiskTypeID = vt.ID
		}
	}
	if input.RootDiskTypeID == "" {
		return nil, fmt.Errorf("3000 IOPS %s root type missing in selected region or zone", class)
	}
	r.t.Logf("root disk: %s, 3000 IOPS", class)
	return input, nil
}

func (r *floatingIPRun) parents(ctx context.Context, input *compute.CreateServerInput) {
	r.t.Helper()
	cidr, err := floatingIPCIDR(r.vpcs)
	if err != nil {
		r.t.Fatal("free VPC CIDR selection failed")
	}
	subnetCIDR, err := vpcBlockCIDR(cidr, 0, 24)
	if err != nil {
		r.t.Fatal("subnet CIDR selection failed")
	}
	r.guard.arm(http.MethodPost, "/networks")
	r.ledger.attempted["vpc"] = true
	vpc, err := r.network.CreateVPC(ctx, &network.CreateVPCInput{Name: r.name, CIDR: cidr})
	r.guard.disarm()
	if vpc != nil {
		r.ledger.remember("vpc", vpc.VPC.UUID)
	}
	if err != nil || vpc == nil || vpc.VPC.Name != r.name || r.ledger.ids["vpc"] == "" || r.ledger.blocked {
		r.ledger.blocked = true
		r.t.Fatal("VPC create or ownership failed")
	}
	input.VPCID = r.ledger.ids["vpc"]
	r.ledger.attempted["subnet"] = true
	r.guard.arm(http.MethodPost, "/networks/"+input.VPCID+"/subnets")
	subnet, err := r.network.CreateSubnet(ctx, &network.CreateSubnetInput{VPCID: input.VPCID, ZoneID: input.ZoneID, Name: r.name, CIDR: subnetCIDR})
	r.guard.disarm()
	if subnet != nil {
		r.ledger.remember("subnet", subnet.Subnet.UUID)
	}
	if err != nil || subnet == nil || subnet.Subnet.Name != r.name || subnet.Subnet.NetworkID != input.VPCID ||
		subnet.Subnet.ZoneID != input.ZoneID || r.ledger.ids["subnet"] == "" || r.ledger.blocked {
		r.ledger.blocked = true
		r.t.Fatal("subnet create or ownership failed")
	}
	input.SubnetID = r.ledger.ids["subnet"]
	r.ledger.attempted["group"] = true
	r.guard.arm(http.MethodPost, "/secgroups")
	group, err := r.network.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{Name: r.name, Description: "Floating IP live test with no ingress"})
	r.guard.disarm()
	if group != nil {
		r.ledger.remember("group", group.SecurityGroup.ID)
	}
	if err != nil || group == nil || group.SecurityGroup.Name != r.name || group.SecurityGroup.IsSystem || group.SecurityGroup.System ||
		r.ledger.ids["group"] == "" || r.ledger.blocked {
		r.ledger.blocked = true
		r.t.Fatal("group create or ownership failed")
	}
	input.SecurityGroupIDs = []string{r.ledger.ids["group"]}
	rules, err := r.network.ListSecurityGroupRules(ctx, &network.ListSecurityGroupRulesInput{SecurityGroupID: r.ledger.ids["group"]})
	if err != nil || rules == nil {
		r.t.Fatal("group rule read failed")
	}
	for _, rule := range rules.Items {
		if !strings.EqualFold(rule.Direction, "egress") {
			r.t.Fatal("group contains ingress or unknown rules; server order refused")
		}
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		r.t.Fatal("throwaway key generation failed")
	}
	r.guard.arm(http.MethodPost, "/sshKeys/import")
	r.ledger.attempted["key"] = true
	imported, err := r.compute.ImportSSHKey(ctx, &compute.ImportSSHKeyInput{Name: r.name, PublicKey: sshRSAPublicKeyLine(&key.PublicKey, r.name)})
	r.guard.disarm()
	if imported != nil {
		r.ledger.remember("key", imported.SSHKey.ID)
	}
	if err != nil || imported == nil || imported.SSHKey.Name != r.name || r.ledger.ids["key"] == "" || r.ledger.blocked {
		r.ledger.blocked = true
		r.t.Fatal("key import or ownership failed")
	}
	input.SSHKeyID = r.ledger.ids["key"]
	r.t.Log("parents: created 1 VPC, 1 subnet, 1 group, 1 imported key; no ingress passed")
}

func (r *floatingIPRun) rememberServer(s compute.Server) {
	r.ledger.remember("server", s.UUID)
	r.ledger.remember("volume", s.BootVolumeID)
	for _, nic := range s.InternalInterfaces {
		r.ledger.remember("floating IP", nic.FloatingIPID)
		if nic.FloatingIPID != "" {
			if r.interfaceID != "" && r.interfaceID != nic.UUID {
				r.ledger.blocked = true
			}
			r.interfaceID = nic.UUID
		}
	}
}

func floatingIPLinked(s compute.Server, rows []network.WANIP, ipID, interfaceID, vpcID, subnetID string) bool {
	if len(s.ExternalInterfaces) != 0 || len(s.InternalInterfaces) != 1 || s.PublicIP == "" ||
		!floatingIPID.MatchString(ipID) || !floatingIPID.MatchString(interfaceID) {
		return false
	}
	nic := s.InternalInterfaces[0]
	addr, err := netip.ParseAddr(nic.FloatingIP)
	if err != nil || addr.Unmap().String() != s.PublicIP || nic.UUID != interfaceID || nic.FloatingIPID != ipID ||
		nic.NetworkUUID != vpcID || nic.SubnetUUID != subnetID || (nic.ServerUUID != "" && nic.ServerUUID != s.UUID) {
		return false
	}
	matches := 0
	for _, row := range rows {
		if row.UUID == ipID {
			if row.NetworkInterfaceID != interfaceID || (row.ServerID != "" && row.ServerID != s.UUID) {
				return false
			}
			matches++
		} else if row.NetworkInterfaceID == interfaceID {
			return false
		}
	}
	return matches == 1
}

func floatingIPCIDR(vpcs []network.VPC) (string, error) {
	var used []netip.Prefix
	for _, vpc := range vpcs {
		prefix, err := netip.ParsePrefix(vpc.CIDR)
		if err != nil {
			return "", errors.New("invalid VPC CIDR inventory")
		}
		used = append(used, prefix.Masked())
	}
	for second := 250; second >= 200; second-- {
		candidate := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(second), 0, 0}), 16)
		if !slices.ContainsFunc(used, func(prefix netip.Prefix) bool { return prefix.Overlaps(candidate) }) {
			return candidate.String(), nil
		}
	}
	return "", errors.New("no free VPC CIDR")
}

func (r *floatingIPRun) cleanup(ctx context.Context) bool {
	if len(r.ledger.attempted) == 0 {
		return true
	}
	if r.ledger.blocked || (r.serverAttempted && !r.released) {
		return false
	}
	steps := []floatingIPCleanupStep{
		{"server", r.deleteServer},
		{"volume", r.deleteBootVolume},
		{"group", func(ctx context.Context, id string) error {
			r.guard.arm(http.MethodDelete, "/secgroups/"+id)
			defer r.guard.disarm()
			_, err := r.network.DeleteSecurityGroup(ctx, &network.DeleteSecurityGroupInput{SecurityGroupID: id})
			if err != nil {
				return err
			}
			_, err = r.network.GetSecurityGroup(ctx, &network.GetSecurityGroupInput{SecurityGroupID: id})
			return floatingIPAbsent(err)
		}},
		{"key", func(ctx context.Context, id string) error {
			r.guard.arm(http.MethodDelete, "/sshKeys/"+id)
			defer r.guard.disarm()
			_, err := r.compute.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: id})
			if err != nil {
				return err
			}
			_, err = r.compute.GetSSHKey(ctx, &compute.GetSSHKeyInput{SSHKeyID: id})
			return floatingIPAbsent(err)
		}},
		{"subnet", func(ctx context.Context, id string) error {
			vpcID := r.ledger.ids["vpc"]
			r.guard.arm(http.MethodDelete, "/networks/"+vpcID+"/subnets/"+id)
			defer r.guard.disarm()
			_, err := r.network.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: id})
			if err != nil {
				return err
			}
			out, err := r.network.ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{VPCID: vpcID})
			if err != nil {
				return err
			}
			if slices.ContainsFunc(out.Items, func(s network.Subnet) bool { return s.UUID == id }) {
				return errors.New("subnet still present")
			}
			return nil
		}},
		{"vpc", func(ctx context.Context, id string) error {
			r.guard.arm(http.MethodDelete, "/networks/"+id)
			defer r.guard.disarm()
			_, err := r.network.DeleteVPC(ctx, &network.DeleteVPCInput{VPCID: id})
			if err != nil {
				return err
			}
			_, err = r.network.GetVPC(ctx, &network.GetVPCInput{VPCID: id})
			return floatingIPAbsent(err)
		}},
	}
	if !r.ledger.clean(ctx, steps) {
		return false
	}
	r.t.Log("cleanup: server, boot volume, group, key, subnet, VPC absence passed")
	return true
}

func (r *floatingIPRun) deleteServer(ctx context.Context, id string) error {
	// Recheck complete WAN absence and server identity immediately before delete.
	rows, err := r.wan(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if r.ledger.seen["floating IP"][row.UUID] || row.NetworkInterfaceID == r.interfaceID {
			return errors.New("floating IP still present")
		}
	}
	volumes, err := floatingIPWalk(ctx, func(ctx context.Context, page int) (*volume.ListVolumesOutput, error) {
		return r.volume.ListVolumes(ctx, &volume.ListVolumesInput{Page: page, Size: 1000})
	}, func(v volume.Volume) string { return v.UUID })
	if err != nil {
		return err
	}
	for _, v := range volumes {
		if (v.ServerID == id || slices.Contains(v.ServerIDList, id)) && v.UUID != r.ledger.ids["volume"] {
			r.ledger.remember("volume", v.UUID)
			return errors.New("unexpected server volume; ownership uncertain")
		}
	}
	read, err := r.getServer(ctx, id)
	if err != nil {
		return err
	}
	s := read.Server
	if s.UUID != id || s.Name != r.name || s.BootVolumeID != r.ledger.ids["volume"] || len(s.ExternalInterfaces) != 0 || len(s.InternalInterfaces) != 1 {
		return errors.New("server ownership changed")
	}
	nic := s.InternalInterfaces[0]
	if nic.UUID != r.interfaceID || nic.NetworkUUID != r.ledger.ids["vpc"] || nic.SubnetUUID != r.ledger.ids["subnet"] ||
		nic.FloatingIPID != "" || nic.FloatingIP != "" {
		return errors.New("server attachment is not cleared")
	}
	r.guard.arm(http.MethodDelete, "/servers/"+id)
	defer r.guard.disarm()
	// The SDK reads GetServer again before DELETE. State can change between
	// our pre-delete check, that SDK read, and DELETE; this window is accepted.
	deleted, err := r.compute.DeleteServer(ctx, &compute.DeleteServerInput{ServerID: id, DeleteVolumes: false})
	if deleted != nil {
		for _, volumeID := range append(slices.Clone(deleted.KeptVolumeIDs), deleted.DeletedVolumeIDs...) {
			r.ledger.remember("volume", volumeID)
		}
		if len(deleted.KeptVolumeIDs) != 0 || len(deleted.DeletedVolumeIDs) != 0 {
			return errors.New("unexpected volumes in delete output")
		}
	}
	if err != nil {
		return err
	}
	_, err = r.getServer(ctx, id)
	return floatingIPAbsent(err)
}

func (r *floatingIPRun) deleteBootVolume(ctx context.Context, id string) error {
	out, err := r.volume.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: id})
	if vngcloud.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if out.Volume.UUID != id || !out.Volume.Bootable || out.Volume.ServerID != "" || len(out.Volume.ServerIDList) != 0 {
		return errors.New("boot volume ownership or detachment uncertain")
	}
	r.guard.arm(http.MethodDelete, "/volumes/"+id)
	defer r.guard.disarm()
	if _, err := r.volume.DeleteVolume(ctx, &volume.DeleteVolumeInput{VolumeID: id}); err != nil {
		return err
	}
	_, err = r.volume.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: id})
	return floatingIPAbsent(err)
}

func floatingIPAbsent(err error) error {
	if vngcloud.IsNotFound(err) {
		return nil
	}
	return errors.New("resource absence unconfirmed")
}

func (r *floatingIPRun) leftovers() {
	r.t.Error("cleanup stopped; manager must reconcile private captures and ledger ownership")
	for _, kind := range []string{"floating IP", "server", "volume", "group", "key", "subnet", "vpc"} {
		if kind == "floating IP" && r.released {
			continue
		}
		count := len(r.ledger.seen[kind])
		if count == 0 && (r.ledger.ids[kind] != "" || r.ledger.attempted[kind]) {
			r.t.Logf("possible leftovers: %s count=1", kind)
			continue
		}
		if count > 0 {
			r.t.Logf("leftovers: %s count=%d", kind, count)
		}
	}
}
