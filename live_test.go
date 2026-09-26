//go:build live

package vngcloud_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
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

	// Billing, cdn, and globalloadbalancer all ignore the configured region
	// (Global scope, per the CLI reads design's scope table for
	// globalloadbalancer), so they run once here instead of once per region
	// inside testLiveRegion.
	t.Run("billing", func(t *testing.T) { testLiveBilling(ctx, t, firstCfg) })
	t.Run("cdn", func(t *testing.T) { testLiveCDN(ctx, t, firstCfg) })
	t.Run("monitor", func(t *testing.T) { testLiveMonitor(ctx, t, firstCfg) })
	t.Run("monitor-alarms", func(t *testing.T) { testLiveMonitorAlarms(ctx, t, firstCfg) })
	t.Run("globalloadbalancer", func(t *testing.T) { testLiveGlobalLoadBalancer(ctx, t, firstCfg) })

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
// lists probe locations, which every account can read regardless of quota,
// and notification channel types and channels.
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

	types, err := client.ListChannelTypes(ctx, nil)
	if err != nil {
		t.Fatalf("ListChannelTypes: %v", err)
	}
	t.Logf("channel types: %d", len(types.Items))
	if len(types.Items) == 0 {
		t.Fatal("expected at least one channel type")
	}

	channels, err := client.ListChannels(ctx, nil)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	t.Logf("channels: %d", len(channels.Items))
	if len(channels.Items) > 0 {
		first := channels.Items[0]
		detail, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: first.ID})
		if err != nil {
			t.Fatalf("GetChannel: %v", err)
		}
		if detail.Channel.ID != first.ID {
			t.Fatalf("GetChannel returned id %q, want %q", detail.Channel.ID, first.ID)
		}
	} else {
		// The test account has no channels today (see the design's live
		// facts). GetChannel's page-walk-then-miss path is otherwise unread
		// by any live test, so exercise it here against a channel ID no
		// account has.
		if _, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: "vngcloud-live-missing"}); !vngcloud.IsNotFound(err) {
			t.Fatalf("GetChannel(missing): %v, want IsNotFound", err)
		}
	}
}

// testLiveMonitorAlarms lists Metric and Log alarms and, for a kind with at
// least one, reads the first by ID. The test account has neither kind
// today. Unlike GetCheck and GetChannel, a live GetAlarm call for an ID
// with no matching alarm returns a 500 (message "Get alarm by id is failed"),
// not a 404, so this only confirms an error comes back rather than asserting
// vngcloud.IsNotFound. It logs counts only: GetAlarm's output shape is
// unconfirmed against a live alarm (see the design), so no field beyond ID
// is checked here.
func testLiveMonitorAlarms(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := monitor.New(cfg)

	for _, kind := range []string{monitor.AlarmKindMetric, monitor.AlarmKindLog} {
		t.Run(kind, func(t *testing.T) {
			out, err := client.ListAlarms(ctx, &monitor.ListAlarmsInput{Kind: kind})
			if err != nil {
				t.Fatalf("ListAlarms(%s): %v", kind, err)
			}
			t.Logf("%s alarms: %d", kind, len(out.Items))
			if len(out.Items) > 0 {
				first := out.Items[0]
				detail, err := client.GetAlarm(ctx, &monitor.GetAlarmInput{AlarmID: first.ID})
				if err != nil {
					t.Fatalf("GetAlarm: %v", err)
				}
				if detail.Alarm.ID != first.ID {
					t.Fatalf("GetAlarm returned id %q, want %q", detail.Alarm.ID, first.ID)
				}
				return
			}
			if _, err := client.GetAlarm(ctx, &monitor.GetAlarmInput{AlarmID: "vngcloud-live-missing"}); err == nil {
				t.Fatal("GetAlarm(missing): got nil error, want an error")
			}
		})
	}
}

// testLiveGlobalLoadBalancer reads GLB packages, regions, and load
// balancers, per the CLI reads design's globalloadbalancer live checks.
// globalloadbalancer ignores the configured region (Global scope), so this
// runs once, not per region (see its caller in TestLive). When the account
// holds a global load balancer, it also reads GetLoadBalancer, each child
// list and get, and ListUsageHistories on the first one; it skips a Get or
// child list whose resource is absent, since the test account has no global
// load balancer today. It logs counts and field presence only, never
// values.
func testLiveGlobalLoadBalancer(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := globalloadbalancer.New(cfg)

	t.Run("packages", func(t *testing.T) {
		res, err := client.ListPackages(ctx, nil)
		if err != nil {
			t.Fatalf("ListPackages: %v", err)
		}
		if len(res.Items) == 0 {
			t.Fatal("ListPackages returned no packages")
		}
		set, total := nonZeroFieldCount(res.Items[0])
		t.Logf("packages: %d, first fields set %d/%d", len(res.Items), set, total)
	})

	t.Run("regions", func(t *testing.T) {
		res, err := client.ListRegions(ctx, nil)
		if err != nil {
			t.Fatalf("ListRegions: %v", err)
		}
		if len(res.Items) == 0 {
			t.Fatal("ListRegions returned no regions")
		}
		set, total := nonZeroFieldCount(res.Items[0])
		t.Logf("glb regions: %d, first fields set %d/%d", len(res.Items), set, total)
	})

	lbs, err := client.ListLoadBalancers(ctx, &globalloadbalancer.ListLoadBalancersInput{Limit: 5})
	if err != nil {
		t.Fatalf("ListLoadBalancers: %v", err)
	}
	t.Logf("global load balancers: %d of %d", len(lbs.Items), lbs.Total)
	if len(lbs.Items) == 0 {
		t.Log("skipped get-load-balancer, list-pools, list-listeners, get-listener, list-pool-members, get-pool-member, list-usage-histories: none")
		return
	}
	first := lbs.Items[0]

	t.Run("load-balancer", func(t *testing.T) {
		detail, err := client.GetLoadBalancer(ctx, &globalloadbalancer.GetLoadBalancerInput{LoadBalancerID: first.ID})
		if err != nil {
			t.Fatalf("GetLoadBalancer: %v", err)
		}
		if detail.LoadBalancer.ID != first.ID {
			t.Fatalf("GetLoadBalancer returned id %q, want %q", detail.LoadBalancer.ID, first.ID)
		}
	})

	var poolID, listenerID string
	t.Run("pools", func(t *testing.T) {
		res, err := client.ListPools(ctx, &globalloadbalancer.ListPoolsInput{LoadBalancerID: first.ID})
		if err != nil {
			t.Fatalf("ListPools: %v", err)
		}
		t.Logf("pools: %d", len(res.Items))
		if len(res.Items) > 0 {
			poolID = res.Items[0].ID
		}
	})
	t.Run("listeners", func(t *testing.T) {
		res, err := client.ListListeners(ctx, &globalloadbalancer.ListListenersInput{LoadBalancerID: first.ID})
		if err != nil {
			t.Fatalf("ListListeners: %v", err)
		}
		t.Logf("listeners: %d", len(res.Items))
		if len(res.Items) > 0 {
			listenerID = res.Items[0].ID
		}
	})

	if listenerID == "" {
		t.Log("skipped get-listener: none")
	} else {
		t.Run("listener", func(t *testing.T) {
			detail, err := client.GetListener(ctx, &globalloadbalancer.GetListenerInput{LoadBalancerID: first.ID, ListenerID: listenerID})
			if err != nil {
				t.Fatalf("GetListener: %v", err)
			}
			if detail.Listener.ID != listenerID {
				t.Fatalf("GetListener returned id %q, want %q", detail.Listener.ID, listenerID)
			}
		})
	}

	if poolID == "" {
		t.Log("skipped list-pool-members, get-pool-member: none")
	} else {
		var memberID string
		t.Run("pool-members", func(t *testing.T) {
			res, err := client.ListPoolMembers(ctx, &globalloadbalancer.ListPoolMembersInput{LoadBalancerID: first.ID, PoolID: poolID})
			if err != nil {
				t.Fatalf("ListPoolMembers: %v", err)
			}
			t.Logf("pool members: %d", len(res.Items))
			if len(res.Items) > 0 {
				memberID = res.Items[0].ID
			}
		})
		if memberID == "" {
			t.Log("skipped get-pool-member: none")
		} else {
			t.Run("pool-member", func(t *testing.T) {
				detail, err := client.GetPoolMember(ctx, &globalloadbalancer.GetPoolMemberInput{LoadBalancerID: first.ID, PoolID: poolID, PoolMemberID: memberID})
				if err != nil {
					t.Fatalf("GetPoolMember: %v", err)
				}
				if detail.PoolMember.ID != memberID {
					t.Fatalf("GetPoolMember returned id %q, want %q", detail.PoolMember.ID, memberID)
				}
			})
		}
	}

	t.Run("usage-histories", func(t *testing.T) {
		res, err := client.ListUsageHistories(ctx, &globalloadbalancer.ListUsageHistoriesInput{LoadBalancerID: first.ID})
		if err != nil {
			t.Fatalf("ListUsageHistories: %v", err)
		}
		t.Logf("usage histories: %d", len(res.Items))
	})
}

// testLivePortal reads the portal's zones, quotas, and tag quota, and calls
// GetQuota on the first quota ListQuotaUsed returns (its "quotaName" key,
// the field the live rows carry, per the CLI reads design's "portal"). A
// map-backed Output has no ID field to compare against the list's the way
// compute or monitor's typed Gets do, so this checks the returned Quota is
// not empty instead, which still fails a GetQuota that decodes empty. It
// logs counts and key counts only, never values.
func testLivePortal(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := portal.New(cfg)

	t.Run("user-info", func(t *testing.T) {
		info, err := client.GetUserInfo(ctx, nil)
		if err != nil {
			t.Fatalf("GetUserInfo: %v", err)
		}
		t.Logf("portal user info keys: %d", len(info.UserInfo))
	})

	t.Run("zones", func(t *testing.T) {
		res, err := client.ListZones(ctx, nil)
		if err != nil {
			t.Fatalf("ListZones: %v", err)
		}
		t.Logf("zones: %d", len(res.Items))
	})

	quotas, err := client.ListQuotaUsed(ctx, nil)
	if err != nil {
		t.Fatalf("ListQuotaUsed: %v", err)
	}
	t.Logf("quotas used: %d", len(quotas.Items))
	if len(quotas.Items) == 0 {
		t.Log("skipped: none")
	} else {
		t.Run("quota", func(t *testing.T) {
			name := fmt.Sprint(quotas.Items[0]["quotaName"])
			quota, err := client.GetQuota(ctx, &portal.GetQuotaInput{Name: name})
			if err != nil {
				t.Fatalf("GetQuota: %v", err)
			}
			if len(quota.Quota) == 0 {
				t.Fatal("GetQuota decoded empty")
			}
		})
	}

	t.Run("tag-quota", func(t *testing.T) {
		res, err := client.GetTagQuota(ctx, nil)
		if err != nil {
			t.Fatalf("GetTagQuota: %v", err)
		}
		t.Logf("tag quota keys: %d", len(res.TagQuota))
	})
}

// testLiveLoadBalancer reads vLB packages and certificates, and, when the
// account has a load balancer or a certificate, reads it and each child
// resource on its first child found, per the CLI reads design's
// live-checks table for loadbalancer. It logs counts and field presence
// only, never values.
func testLiveLoadBalancer(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := loadbalancer.New(cfg)

	t.Run("packages", func(t *testing.T) {
		res, err := client.ListPackages(ctx, nil)
		if err != nil {
			t.Fatalf("ListPackages: %v", err)
		}
		if len(res.Items) == 0 {
			t.Fatal("ListPackages returned no packages")
		}
		set, total := nonZeroFieldCount(res.Items[0])
		t.Logf("packages: %d, first fields set %d/%d", len(res.Items), set, total)
	})

	lbs, err := client.ListLoadBalancers(ctx, &loadbalancer.ListLoadBalancersInput{Page: 1, Size: 5})
	if err != nil {
		t.Fatalf("ListLoadBalancers: %v", err)
	}
	t.Logf("load balancers: %d of %d", len(lbs.Items), lbs.TotalItem)
	if len(lbs.Items) == 0 {
		t.Log("skipped: none")
	} else {
		t.Run("load-balancer", func(t *testing.T) {
			testLiveLoadBalancerDetail(ctx, t, client, lbs.Items[0].UUID)
		})
	}

	certs, err := client.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Page: 1, Size: 5})
	if err != nil {
		t.Fatalf("ListCertificates: %v", err)
	}
	t.Logf("certificates: %d of %d", len(certs.Items), certs.TotalItem)
	if len(certs.Items) == 0 {
		t.Log("skipped: none")
	} else {
		t.Run("certificate", func(t *testing.T) {
			certID := certs.Items[0].UUID
			cert, err := client.GetCertificate(ctx, &loadbalancer.GetCertificateInput{CertificateID: certID})
			if err != nil {
				t.Fatalf("GetCertificate: %v", err)
			}
			if cert.Certificate.UUID != certID {
				t.Fatalf("GetCertificate returned id %q, want %q", cert.Certificate.UUID, certID)
			}
		})
	}
}

// testLiveLoadBalancerDetail reads lbID itself, then its tags, its first
// listener, and its first pool. testLiveLoadBalancer only calls this when
// the account has at least one load balancer.
func testLiveLoadBalancerDetail(ctx context.Context, t *testing.T, client *loadbalancer.Client, lbID string) {
	lb, err := client.GetLoadBalancer(ctx, &loadbalancer.GetLoadBalancerInput{LoadBalancerID: lbID})
	if err != nil {
		t.Fatalf("GetLoadBalancer: %v", err)
	}
	if lb.LoadBalancer.UUID != lbID {
		t.Fatalf("GetLoadBalancer returned id %q, want %q", lb.LoadBalancer.UUID, lbID)
	}

	t.Run("tags", func(t *testing.T) {
		res, err := client.ListTags(ctx, &loadbalancer.ListTagsInput{LoadBalancerID: lbID})
		if err != nil {
			t.Fatalf("ListTags: %v", err)
		}
		t.Logf("tags: %d", len(res.Items))
	})

	listeners, err := client.ListListeners(ctx, &loadbalancer.ListListenersInput{LoadBalancerID: lbID})
	if err != nil {
		t.Fatalf("ListListeners: %v", err)
	}
	t.Logf("listeners: %d", len(listeners.Items))
	if len(listeners.Items) == 0 {
		t.Log("skipped: none")
	} else {
		t.Run("listener", func(t *testing.T) {
			testLiveLoadBalancerListener(ctx, t, client, lbID, listeners.Items[0].UUID)
		})
	}

	pools, err := client.ListPools(ctx, &loadbalancer.ListPoolsInput{LoadBalancerID: lbID})
	if err != nil {
		t.Fatalf("ListPools: %v", err)
	}
	t.Logf("pools: %d", len(pools.Items))
	if len(pools.Items) == 0 {
		t.Log("skipped: none")
	} else {
		t.Run("pool", func(t *testing.T) {
			testLiveLoadBalancerPool(ctx, t, client, lbID, pools.Items[0].UUID)
		})
	}
}

// testLiveLoadBalancerListener reads listenerID itself and, when it has at
// least one policy, the first policy.
func testLiveLoadBalancerListener(ctx context.Context, t *testing.T, client *loadbalancer.Client, lbID, listenerID string) {
	listener, err := client.GetListener(ctx, &loadbalancer.GetListenerInput{LoadBalancerID: lbID, ListenerID: listenerID})
	if err != nil {
		t.Fatalf("GetListener: %v", err)
	}
	if listener.Listener.UUID != listenerID {
		t.Fatalf("GetListener returned id %q, want %q", listener.Listener.UUID, listenerID)
	}

	policies, err := client.ListPolicies(ctx, &loadbalancer.ListPoliciesInput{LoadBalancerID: lbID, ListenerID: listenerID})
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	t.Logf("policies: %d", len(policies.Items))
	if len(policies.Items) == 0 {
		t.Log("skipped: none")
		return
	}
	policyID := policies.Items[0].UUID
	policy, err := client.GetPolicy(ctx, &loadbalancer.GetPolicyInput{LoadBalancerID: lbID, ListenerID: listenerID, PolicyID: policyID})
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if policy.Policy.UUID != policyID {
		t.Fatalf("GetPolicy returned id %q, want %q", policy.Policy.UUID, policyID)
	}
}

// testLiveLoadBalancerPool reads poolID itself, its health monitor, and its
// members. HealthMonitor carries no ID field to compare against a list the
// way the other Gets here do, so this checks its HealthCheckProtocol is set
// instead, which still fails a decode that comes back empty.
func testLiveLoadBalancerPool(ctx context.Context, t *testing.T, client *loadbalancer.Client, lbID, poolID string) {
	pool, err := client.GetPool(ctx, &loadbalancer.GetPoolInput{LoadBalancerID: lbID, PoolID: poolID})
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if pool.Pool.UUID != poolID {
		t.Fatalf("GetPool returned id %q, want %q", pool.Pool.UUID, poolID)
	}

	hm, err := client.GetPoolHealthMonitor(ctx, &loadbalancer.GetPoolHealthMonitorInput{LoadBalancerID: lbID, PoolID: poolID})
	if err != nil {
		t.Fatalf("GetPoolHealthMonitor: %v", err)
	}
	if hm.HealthMonitor.HealthCheckProtocol == "" {
		t.Fatal("GetPoolHealthMonitor decoded empty")
	}

	members, err := client.ListPoolMembers(ctx, &loadbalancer.ListPoolMembersInput{LoadBalancerID: lbID, PoolID: poolID})
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	t.Logf("pool members: %d", len(members.Items))
}

// nonZeroFieldCount reports how many top-level fields of the struct v hold a
// non-zero value, out of the total field count. A live Get test uses it to
// confirm decoding filled in real fields without logging any of their
// values: a struct that decodes with every field empty (set == 0) is a
// decoding bug.
func nonZeroFieldCount(v any) (set, total int) {
	rv := reflect.ValueOf(v)
	total = rv.NumField()
	for i := 0; i < total; i++ {
		if !rv.Field(i).IsZero() {
			set++
		}
	}
	return set, total
}

// testLiveVolume reads volume type zones, volume types, and encryption
// types, and calls GetVolumeType on the first volume type, per the CLI
// reads design's "volume" live checks. When the account holds a volume (the
// list the "volumes" subtest above already fetched), it also reads
// GetVolume, GetUnderlyingVolume, and ListSnapshots on the first one, and
// skips those three when the account has none. It logs counts and field
// presence only, never values.
func testLiveVolume(ctx context.Context, t *testing.T, cfg vngcloud.Config, volumes []volume.Volume) {
	client := volume.New(cfg)

	t.Run("volume-type-zones", func(t *testing.T) {
		res, err := client.ListVolumeTypeZones(ctx, nil)
		if err != nil {
			t.Fatalf("ListVolumeTypeZones: %v", err)
		}
		t.Logf("volume type zones: %d", len(res.Items))
		if len(res.Items) > 0 {
			first := res.Items[0]
			set, total := nonZeroFieldCount(first)
			t.Logf("volume type zone fields set: %d/%d", set, total)
			if first.ID == "" || first.Name == "" || first.Zone.UUID == "" {
				t.Fatal("ListVolumeTypeZones item missing id, name, or zone uuid")
			}
		}
	})

	types, err := client.ListVolumeTypes(ctx, nil)
	if err != nil {
		t.Fatalf("ListVolumeTypes: %v", err)
	}
	t.Logf("volume types: %d", len(types.Items))
	if len(types.Items) == 0 {
		t.Log("skipped get-volume-type: none")
	} else {
		t.Run("volume-type", func(t *testing.T) {
			detail, err := client.GetVolumeType(ctx, &volume.GetVolumeTypeInput{VolumeTypeID: types.Items[0].ID})
			if err != nil {
				t.Fatalf("GetVolumeType: %v", err)
			}
			if detail.VolumeType.ID != types.Items[0].ID {
				t.Fatalf("GetVolumeType returned id %q, want %q", detail.VolumeType.ID, types.Items[0].ID)
			}
		})
	}

	t.Run("encryption-types", func(t *testing.T) {
		res, err := client.ListEncryptionTypes(ctx, nil)
		if err != nil {
			t.Fatalf("ListEncryptionTypes: %v", err)
		}
		t.Logf("encryption types: %d", len(res.Items))
	})

	if len(volumes) == 0 {
		t.Log("skipped get-volume, get-underlying-volume, list-snapshots: none")
		return
	}
	first := volumes[0]

	t.Run("get-volume", func(t *testing.T) {
		detail, err := client.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: first.UUID})
		if err != nil {
			t.Fatalf("GetVolume: %v", err)
		}
		if detail.Volume.UUID != first.UUID {
			t.Fatalf("GetVolume returned uuid %q, want %q", detail.Volume.UUID, first.UUID)
		}
	})

	t.Run("underlying-volume", func(t *testing.T) {
		detail, err := client.GetUnderlyingVolume(ctx, &volume.GetUnderlyingVolumeInput{VolumeID: first.UUID})
		if err != nil {
			t.Fatalf("GetUnderlyingVolume: %v", err)
		}
		set, total := nonZeroFieldCount(detail.Volume)
		t.Logf("underlying volume fields set: %d/%d", set, total)
		if set == 0 {
			t.Fatal("GetUnderlyingVolume decoded empty")
		}
	})

	t.Run("snapshots", func(t *testing.T) {
		res, err := client.ListSnapshots(ctx, &volume.ListSnapshotsInput{VolumeID: first.UUID, Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListSnapshots: %v", err)
		}
		t.Logf("snapshots: %d of %d", len(res.Items), res.TotalItem)
	})
}

// testLiveContainerRegistry reads repositories and users, per the CLI reads
// design's live-checks table for containerregistry. It logs counts and,
// when a list returns at least one row, that row's key count only, never a
// key name or value: list-users is held from the CLI until the SDK types
// User from a live capture, and a registry user row may carry a password or
// robot token. It skips the key-count log for an empty list.
func testLiveContainerRegistry(ctx context.Context, t *testing.T, cfg vngcloud.Config) {
	client := containerregistry.New(cfg)

	t.Run("repositories", func(t *testing.T) {
		res, err := client.ListRepositories(ctx, &containerregistry.ListRepositoriesInput{})
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}
		t.Logf("repositories: %d of %d", len(res.Items), res.TotalItem)
		if len(res.Items) == 0 {
			t.Log("skipped key count: none")
			return
		}
		t.Logf("first repository keys: %d", len(res.Items[0]))
	})

	t.Run("users", func(t *testing.T) {
		res, err := client.ListUsers(ctx, &containerregistry.ListUsersInput{})
		if err != nil {
			t.Fatalf("ListUsers: %v", err)
		}
		t.Logf("users: %d of %d", len(res.Items), res.TotalItem)
		if len(res.Items) == 0 {
			t.Log("skipped key count: none")
			return
		}
		t.Logf("first user keys: %d", len(res.Items[0]))
	})
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
	var volumes []volume.Volume
	t.Run("volumes", func(t *testing.T) {
		res, err := volume.New(cfg).ListVolumes(ctx, &volume.ListVolumesInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVolumes: %v", err)
		}
		t.Logf("volumes: %d of %d", len(res.Items), res.TotalItem)
		volumes = res.Items
	})
	t.Run("volume", func(t *testing.T) { testLiveVolume(ctx, t, cfg, volumes) })
	t.Run("vpcs", func(t *testing.T) {
		res, err := network.New(cfg).ListVPCs(ctx, &network.ListVPCsInput{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVPCs: %v", err)
		}
		t.Logf("vpcs: %d of %d", len(res.Items), res.TotalItem)
	})
	t.Run("loadbalancer", func(t *testing.T) { testLiveLoadBalancer(ctx, t, cfg) })
	t.Run("dns-zones", func(t *testing.T) {
		dnsClient := dns.New(cfg)
		res, err := dnsClient.ListHostedZones(ctx, &dns.ListHostedZonesInput{})
		if err != nil {
			t.Fatalf("ListHostedZones: %v", err)
		}
		t.Logf("hosted zones: %d of %d", len(res.Items), res.TotalItem)
		if len(res.Items) == 0 {
			return
		}
		records, err := dnsClient.ListRecords(ctx, &dns.ListRecordsInput{HostedZoneID: res.Items[0].ID})
		if err != nil {
			t.Fatalf("ListRecords: %v", err)
		}
		t.Logf("records in first zone: %d of %d", len(records.Items), records.TotalItem)
	})
	t.Run("containerregistry", func(t *testing.T) { testLiveContainerRegistry(ctx, t, cfg) })
	t.Run("portal", func(t *testing.T) { testLivePortal(ctx, t, cfg) })
	t.Run("pricing-quote", func(t *testing.T) {
		quoteClient := pricing.New(cfg)
		if _, err := quoteClient.GetQuote(ctx, &pricing.GetQuoteInput{ResourceType: pricing.ResourceSnapshot}); err != nil {
			t.Fatalf("GetQuote: %v", err)
		}
		t.Log("ok")
	})
}
