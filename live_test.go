//go:build live

package vngcloud_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/globalloadbalancer"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/internal/iamuser"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/pricing"
	"danny.vn/vngcloud/volume"
)

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

	token := os.Getenv("VNGCLOUD_ACCESS_TOKEN")
	if token == "" {
		if os.Getenv("VNGCLOUD_ROOT_EMAIL") == "" {
			t.Skip("set VNGCLOUD_ROOT_EMAIL/VNGCLOUD_USERNAME/VNGCLOUD_PASSWORD or VNGCLOUD_ACCESS_TOKEN in .env")
		}
		req := iamuser.LoginRequest{
			RootEmail: os.Getenv("VNGCLOUD_ROOT_EMAIL"),
			Username:  os.Getenv("VNGCLOUD_USERNAME"),
			Password:  os.Getenv("VNGCLOUD_PASSWORD"),
		}
		if secret := os.Getenv("VNGCLOUD_TOTP_SECRET"); secret != "" {
			req.TOTP = &vngcloud.SecretTOTP{Secret: secret}
		}
		result, err := iamuser.Login(ctx, req)
		if err != nil {
			t.Fatalf("IAM login against default endpoints failed: %v", err)
		}
		t.Logf("login ok; token expires %s; refresh token present: %v", result.ExpiresAt.Format(time.RFC3339), result.RefreshToken != "")
		token = result.AccessToken
	}

	// Billing ignores the configured region, so it runs once here instead of
	// once per region inside testLiveRegion.
	t.Run("billing", func(t *testing.T) { testLiveBilling(ctx, t, regions[0], token) })

	for _, region := range regions {
		t.Run(region, func(t *testing.T) { testLiveRegion(ctx, t, region, token) })
	}
}

// testLiveBilling reads budgets, the current period cost, and balances. It
// logs counts and field presence only, never amounts or account values.
func testLiveBilling(ctx context.Context, t *testing.T, region, token string) {
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion(region),
		vngcloud.WithStaticToken(token),
	)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
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

func testLiveRegion(ctx context.Context, t *testing.T, region, token string) {
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion(region),
		vngcloud.WithProjectID(os.Getenv("VNGCLOUD_PROJECT_ID")),
		vngcloud.WithStaticToken(token),
	)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	client, err := vngcloud.NewClient(ctx, cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	project, err := client.RequireProject(ctx)
	if err != nil {
		t.Fatalf("RequireProject: %v", err)
	}
	t.Logf("project %s in %s", project.ID, project.Region)

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
