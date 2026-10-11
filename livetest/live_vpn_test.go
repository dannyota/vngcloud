//go:build live

package livetest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/network"
)

func TestLiveVPN(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_VPN") != "1" {
		t.Skip("set VNGCLOUD_LIVE_VPN=1 to run")
	}
	if err := envfile.Load(repoPath(".env")); err != nil {
		t.Fatal("could not load live credentials")
	}
	auth := &vngcloud.IAMUserAuth{RootEmail: os.Getenv("VNGCLOUD_ROOT_EMAIL"), Username: os.Getenv("VNGCLOUD_USERNAME"), Password: os.Getenv("VNGCLOUD_PASSWORD")}
	if secret := os.Getenv("VNGCLOUD_TOTP_SECRET"); secret != "" {
		auth.TOTP = &vngcloud.SecretTOTP{Secret: secret}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	configFile := emptyFile(t, "config")
	credentialsFile := emptyFile(t, "credentials")
	for _, region := range []string{"hcm-3", "han-1"} {
		t.Run(region, func(t *testing.T) {
			cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion(region), vngcloud.WithIAMUser(auth), vngcloud.WithTokenCache(filepath.Join(liveHome, ".vngcloud", "cache")), vngcloud.WithConfigFile(configFile), vngcloud.WithSharedCredentialsFile(credentialsFile))
			if err != nil {
				t.Fatal("VPN login configuration failed")
			}
			out, err := network.New(cfg).ListVPNConnections(ctx, nil)
			if err != nil {
				t.Fatalf("VPN read failed (code %s)", safeVPNLiveCode(err))
			}
			if out == nil || out.Items == nil || out.Page <= 0 || out.PageSize <= 0 || out.TotalPage < 0 || out.TotalItem < 0 {
				t.Fatal("invalid VPN envelope")
			}
			active, provisioning, failed, other := 0, 0, 0, 0
			for _, item := range out.Items {
				switch item.Status {
				case "ACTIVE":
					active++
				case "PROVISIONING":
					provisioning++
				case "ERROR":
					failed++
				default:
					other++
				}
			}
			t.Logf("count=%d ACTIVE=%d PROVISIONING=%d ERROR=%d other=%d", len(out.Items), active, provisioning, failed, other)
		})
	}
}

func safeVPNLiveCode(err error) string {
	switch code := vngcloud.ErrorCode(err); code {
	case "Unauthorized", "Forbidden", "NotFound", "Throttled", "ServerError", "ClientError", "InvalidResponse":
		return code
	default:
		return "withheld"
	}
}
