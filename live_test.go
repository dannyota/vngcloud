//go:build live

package vngcloud_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/globalloadbalancer"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/project"
	"danny.vn/vngcloud/volume"
)

// liveHome is one temp directory, created once per test binary run, that
// every live-tagged test in this package may use as HOME. TestLiveCLI (in
// live_cli_test.go) points HOME at it so the CLI's fixed ~/.vngcloud/cache
// path resolves to the same directory as cacheDir below: TestLive and
// TestLiveCLI then share one cached token for the same credentials instead
// of each performing its own IAM login inside one 30-second TOTP window.
var liveHome string

// TestMain creates liveHome before any test runs and removes it once every
// test in the binary (TestLive and, under the same build tag, TestLiveCLI)
// has finished, so the shared cache directory outlives whichever test
// creates it first regardless of run order.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vngcloud-live-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "vngcloud: create live home dir: %v\n", err)
		os.Exit(1)
	}
	liveHome = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestLive(t *testing.T) {
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	regions := []string{"hcm-3", "han-1"}
	if raw := os.Getenv("VNGCLOUD_REGIONS"); raw != "" {
		regions = regions[:0]
		for _, region := range strings.Split(raw, ",") {
			if region = strings.TrimSpace(region); region != "" {
				regions = append(regions, region)
			}
		}
	}
	if len(regions) == 0 {
		t.Fatal("no regions resolved from VNGCLOUD_REGIONS")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// cacheDir is under liveHome (see TestMain and its doc comment above), so
	// a CLI command TestLiveCLI runs in this process reuses the token this
	// login caches. It is shared by every Config this run builds, and the two
	// files below are empty, so LoadConfig resolves credentials from .env's
	// environment variables and never reads the real ~/.vngcloud.
	cacheDir := filepath.Join(liveHome, ".vngcloud", "cache")
	emptyConfigFile := emptyFile(t, "config")
	emptyCredentialsFile := emptyFile(t, "credentials")

	buildConfig := func(region string) (vngcloud.Config, error) {
		return vngcloud.LoadConfig(ctx,
			vngcloud.WithRegion(region),
			vngcloud.WithTokenCache(cacheDir),
			vngcloud.WithConfigFile(emptyConfigFile),
			vngcloud.WithSharedCredentialsFile(emptyCredentialsFile),
		)
	}

	firstCfg, err := buildConfig(regions[0])
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Skip("set VNGCLOUD_ROOT_EMAIL/VNGCLOUD_USERNAME/VNGCLOUD_PASSWORD or VNGCLOUD_ACCESS_TOKEN in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := firstCfg.Authenticate(ctx); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	t.Log("login ok")

	// Billing and cdn both ignore the configured region, so they run once
	// here instead of once per region inside testLiveRegion.
	t.Run("billing", func(t *testing.T) { testLiveBilling(ctx, t, firstCfg) })
	t.Run("cdn", func(t *testing.T) { testLiveCDN(ctx, t, firstCfg) })
	t.Run("monitor", func(t *testing.T) { testLiveMonitor(ctx, t, firstCfg) })

	for i, region := range regions {
		cfg := firstCfg
		if i > 0 {
			cfg, err = buildConfig(region)
			if err != nil {
				t.Fatalf("LoadConfig(%s): %v", region, err)
			}
		}
		t.Run(region, func(t *testing.T) { testLiveRegion(ctx, t, cfg) })
	}

	// A second Config built from the same credentials and cache directory
	// must reuse the cached token rather than log in again. There is no
	// public hook to observe a login directly, so this checks wall-clock
	// time instead: a fresh login is a multi-step web flow that takes
	// noticeably longer than reading a local file.
	t.Run("second-config-reuses-cached-token", func(t *testing.T) {
		cfg, err := buildConfig(regions[0])
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		start := time.Now()
		if err := cfg.Authenticate(ctx); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		elapsed := time.Since(start)
		t.Logf("authenticate with a cached token took %s", elapsed)
		const maxCachedAuthenticate = 3 * time.Second
		if elapsed > maxCachedAuthenticate {
			t.Fatalf("authenticate took %s, want under %s: a fresh login likely ran instead of reusing the cache",
				elapsed, maxCachedAuthenticate)
		}
	})
}

// emptyFile creates an empty, mode-0600 file named name in a fresh temp
// directory, for a LoadConfig file option that must point at a file which
// exists but has no sections to resolve from.
func emptyFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create empty %s: %v", name, err)
	}
	return path
}

// testLiveBilling reads budgets, the current period cost, and balances. It
// logs counts and field presence only, never amounts or account values.
func testLiveBilling(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := billing.New(cfg)

	t.Run("budgets", func(t *testing.T) {
		res, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
		if err != nil {
			t.Fatalf("ListBudgets: %v", err)
		}
		t.Logf("budgets: %d", len(res.Items))
	})
	t.Run("current-period-cost", func(t *testing.T) {
		if _, err := client.GetCurrentPeriodCost(ctx, &billing.GetCurrentPeriodCostInput{}); err != nil {
			t.Fatalf("GetCurrentPeriodCost: %v", err)
		}
		t.Log("ok")
	})
	t.Run("balances", func(t *testing.T) {
		res, err := client.GetBalances(ctx, &billing.GetBalancesInput{})
		if err != nil {
			t.Fatalf("GetBalances: %v", err)
		}
		t.Logf("balances set fields: %s", setBalanceFields(res.Balances))
	})
}

// setBalanceFields names which Balances fields are non-nil, never their
// values.
func setBalanceFields(b billing.Balances) string {
	var fields []string
	if b.Cash != nil {
		fields = append(fields, "Cash")
	}
	if b.POC != nil {
		fields = append(fields, "POC")
	}
	if b.CashAvailable != nil {
		fields = append(fields, "CashAvailable")
	}
	if b.CashHolding != nil {
		fields = append(fields, "CashHolding")
	}
	if b.POCHolding != nil {
		fields = append(fields, "POCHolding")
	}
	if len(fields) == 0 {
		return "none"
	}
	return strings.Join(fields, ",")
}

// testLiveCDN reads the published CDN IP ranges. It logs only the count: the
// design does not pin it, since a list change must not fail CI, but every
// item must still parse as a CIDR.
func testLiveCDN(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	out, err := cdn.New(cfg).ListIPRanges(ctx, nil)
	if err != nil {
		t.Fatalf("ListIPRanges: %v", err)
	}
	t.Logf("cdn ip ranges: %d", len(out.Items))
	if len(out.Items) == 0 {
		t.Fatal("expected at least one CDN IP range")
	}
	for _, item := range out.Items {
		if _, err := netip.ParsePrefix(item); err != nil {
			t.Fatalf("item %q did not parse as a CIDR: %v", item, err)
		}
	}
}

// testLiveMonitor lists vMonitor checks and, when the account has at least
// one, reads the first by ID. It never pins the count: the test account's
// checks change over time, and a count change must not fail CI. It also
// lists probe locations, which every account can read regardless of quota.
func testLiveMonitor(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := monitor.New(cfg)

	out, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	t.Logf("checks: %d", len(out.Items))
	if len(out.Items) > 0 {
		first := out.Items[0]
		detail, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: first.ID})
		if err != nil {
			t.Fatalf("GetCheck: %v", err)
		}
		if detail.Check.ID != first.ID {
			t.Fatalf("GetCheck returned id %q, want %q", detail.Check.ID, first.ID)
		}
	}

	locations, err := client.ListLocations(ctx, nil)
	if err != nil {
		t.Fatalf("ListLocations: %v", err)
	}
	t.Logf("locations: %d", len(locations.Items))
	if len(locations.Items) == 0 {
		t.Fatal("expected at least one probe location")
	}
}

func testLiveRegion(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	projects, err := project.New(cfg).ListProjects(ctx, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	t.Logf("projects: %d", len(projects.Items))
	if len(projects.Items) != 1 {
		t.Fatalf("region %s has %d projects, want exactly 1: set VNGCLOUD_PROJECT_ID to disambiguate", cfg.Region(), len(projects.Items))
	}

	t.Run("servers", func(t *testing.T) {
		res, err := compute.New(cfg).ListServers(ctx, &compute.ListServersInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListServers: %v", err)
		}
		t.Logf("servers: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("volumes", func(t *testing.T) {
		res, err := volume.New(cfg).ListVolumes(ctx, &volume.ListVolumesInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVolumes: %v", err)
		}
		t.Logf("volumes: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("vpcs", func(t *testing.T) {
		res, err := network.New(cfg).ListVPCs(ctx, &network.ListVPCsInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVPCs: %v", err)
		}
		t.Logf("vpcs: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("load-balancers", func(t *testing.T) {
		res, err := loadbalancer.New(cfg).ListLoadBalancers(ctx, &loadbalancer.ListLoadBalancersInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListLoadBalancers: %v", err)
		}
		t.Logf("load balancers: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("global-load-balancers", func(t *testing.T) {
		res, err := globalloadbalancer.New(cfg).ListLoadBalancers(ctx, &globalloadbalancer.ListLoadBalancersInput{Limit: 5})
		if err != nil {
			t.Fatalf("GLB ListLoadBalancers: %v", err)
		}
		t.Logf("global load balancers: %d of %d", len(res.Items), res.Total)
	})
	t.Run("dns-zones", func(t *testing.T) {
		dnsClient := dns.New(cfg)
		res, err := dnsClient.ListHostedZones(ctx, &dns.ListHostedZonesInput{})
		if err != nil {
			t.Fatalf("ListHostedZones: %v", err)
		}
		t.Logf("hosted zones: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("container-repositories", func(t *testing.T) {
		res, err := containerregistry.New(cfg).ListRepositories(ctx, &containerregistry.ListRepositoriesInput{})
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}
		t.Logf("repositories: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("portal-user", func(t *testing.T) {
		info, err := portal.New(cfg).GetUserInfo(ctx, nil)
		if err != nil {
			t.Fatalf("GetUserInfo: %v", err)
		}
		t.Logf("portal user info retrieved: %+v", info.UserInfo)
	})
	t.Run("pricing-quote", func(t *testing.T) {
		quoteClient := pricing.New(cfg)
		if _, err := quoteClient.GetQuote(ctx, &pricing.GetQuoteInput{ResourceType: pricing.ResourceSnapshot}); err != nil {
			t.Fatalf("GetQuote: %v", err)
		}
		t.Log("ok")
	})
}
