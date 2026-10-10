//go:build live

package vngcloud_test

import (
	"context"
	"os"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/internal/envfile"
)

// TestLiveCDNWebAccelerators reads the account's Web Accelerator CDNs with
// the vCDN API key in VNGCLOUD_VCDN_API_KEY. It skips without a key. When a
// CDN exists it reads the first one and runs the analytics reads on its
// generated domain over 24 hours and over yesterday to today; on an account
// with none it logs that they were skipped. It logs counts and statuses
// only, never a domain, ID, or key.
func TestLiveCDNWebAccelerators(t *testing.T) {
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("VNGCLOUD_VCDN_API_KEY")
	if key == "" {
		t.Skip("set VNGCLOUD_VCDN_API_KEY to read the vCDN API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("unused"), vngcloud.WithCDNAPIKey(key))
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	client := cdn.New(cfg)

	list, err := client.ListWebAccelerators(ctx, nil)
	if err != nil {
		t.Fatalf("ListWebAccelerators: %v", err)
	}
	t.Logf("web accelerators: %d", len(list.Items))
	if len(list.Items) == 0 {
		t.Log("no CDN: GetWebAccelerator and the analytics reads skipped")
		return
	}
	first := list.Items[0]
	got, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: first.CDNID})
	if err != nil {
		t.Fatalf("GetWebAccelerator: %v", err)
	}
	if got.WebAccelerator.CDNID != first.CDNID || got.WebAccelerator.Type != "webacc" || got.WebAccelerator.CDNDomain == "" {
		t.Fatalf("GetWebAccelerator: unexpected shape: type %q, matches list: %v", got.WebAccelerator.Type, got.WebAccelerator.CDNID == first.CDNID)
	}
	t.Logf("web accelerator: status %s, actions %d, origins %d", got.WebAccelerator.StatusName,
		len(got.WebAccelerator.DefaultRuleActions), len(got.WebAccelerator.Upstreams))
	liveCDNAnalytics(ctx, t, client, got.WebAccelerator.CDNDomain)
}
