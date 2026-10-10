//go:build live

package vngcloud_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/envfile"
)

func TestLiveConsoleLog(t *testing.T) {
	serverID := os.Getenv("VNGCLOUD_LIVE_CONSOLE_LOG_SERVER_ID")
	if serverID == "" {
		t.Skip("VNGCLOUD_LIVE_CONSOLE_LOG_SERVER_ID is unset")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatal("fail: environment config load")
	}
	region := os.Getenv("VNGCLOUD_LIVE_CONSOLE_LOG_REGION")
	if region == "" {
		region = "hcm-3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion(region),
		vngcloud.WithTokenCache(filepath.Join(liveHome, ".vngcloud", "cache")),
		vngcloud.WithConfigFile(emptyFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyFile(t, "credentials")),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Skip("set VNGCLOUD_ROOT_EMAIL/VNGCLOUD_USERNAME/VNGCLOUD_PASSWORD or VNGCLOUD_ACCESS_TOKEN in .env")
	}
	if err != nil {
		t.Fatal("fail: config load")
	}
	out, err := compute.New(cfg).GetServerConsoleLog(ctx, &compute.GetServerConsoleLogInput{ServerID: serverID})
	if err != nil {
		var apiErr *vngcloud.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("fail: console log read; code=%q status=%d", apiErr.Code, apiErr.StatusCode)
		}
		t.Fatal("fail: console log read")
	}
	if out == nil {
		t.Fatal("fail: nil console log output")
	}
	size := len(out.Log.Reveal())
	if size == 0 {
		t.Fatal("fail: empty console log; log length 0 bytes")
	}
	t.Logf("pass: log length %d bytes", size)
}
