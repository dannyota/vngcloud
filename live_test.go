//go:build live

package vngcloud_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/internal/iamuser"
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

	for _, region := range regions {
		t.Run(region, func(t *testing.T) { testLiveRegion(ctx, t, region, token) })
	}
}

func testLiveRegion(ctx context.Context, t *testing.T, region, token string) {
	client, err := vngcloud.NewClient(ctx,
		vngcloud.Config{Region: region, ProjectID: os.Getenv("VNGCLOUD_PROJECT_ID")},
		vngcloud.WithStaticToken(token))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	project, err := client.RequireProject(ctx)
	if err != nil {
		t.Fatalf("RequireProject: %v", err)
	}
	t.Logf("project %s in %s", project.ID, project.Region)

	t.Run("servers", func(t *testing.T) {
		res, err := client.Compute.ListServers(ctx, &vngcloud.ListServersOptions{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListServers: %v", err)
		}
		t.Logf("servers: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("volumes", func(t *testing.T) {
		res, err := client.Volume.ListVolumes(ctx, &vngcloud.ListVolumesOptions{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVolumes: %v", err)
		}
		t.Logf("volumes: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("vpcs", func(t *testing.T) {
		res, err := client.Network.ListVPCs(ctx, &vngcloud.ListVPCsOptions{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListVPCs: %v", err)
		}
		t.Logf("vpcs: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("load-balancers", func(t *testing.T) {
		res, err := client.LoadBalancer.ListLoadBalancers(ctx, &vngcloud.ListLoadBalancersOptions{Page: 1, Size: 5})
		if err != nil {
			t.Fatalf("ListLoadBalancers: %v", err)
		}
		t.Logf("load balancers: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("global-load-balancers", func(t *testing.T) {
		res, err := client.GlobalLoadBalancer.ListLoadBalancers(ctx, &vngcloud.ListGlobalLoadBalancersOptions{Limit: 5})
		if err != nil {
			t.Fatalf("GLB ListLoadBalancers: %v", err)
		}
		t.Logf("global load balancers: %d of %d", len(res.Items), res.Total)
	})
	t.Run("dns-zones", func(t *testing.T) {
		res, err := client.DNS.ListHostedZones(ctx, &vngcloud.ListHostedZonesOptions{})
		if err != nil {
			t.Fatalf("ListHostedZones: %v", err)
		}
		t.Logf("hosted zones: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("container-repositories", func(t *testing.T) {
		res, err := client.ContainerRegistry.ListRepositories(ctx, &vngcloud.ListContainerRepositoriesOptions{})
		if err != nil {
			t.Fatalf("ListRepositories: %v", err)
		}
		t.Logf("repositories: %d of %d", len(res.Items), res.Page.TotalItem)
	})
	t.Run("portal-user", func(t *testing.T) {
		info, err := client.Portal.GetUserInfo(ctx)
		if err != nil {
			t.Fatalf("GetUserInfo: %v", err)
		}
		t.Logf("portal user info retrieved: %+v", info)
	})
}
