//go:build livewrite

package livetest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"danny.vn/vngcloud/globalloadbalancer"
)

func TestLiveWriteGLB(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_GLB") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_GLB=1 to enable the approved run")
	}
	packageID := os.Getenv("VNGCLOUD_LIVE_GLB_PACKAGE_ID")
	if packageID == "" {
		t.Skip("set VNGCLOUD_LIVE_GLB_PACKAGE_ID to the approved enabled package")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client := globalloadbalancer.New(liveWriteConfig(ctx, t))
	packages, err := client.ListPackages(ctx, nil)
	if err != nil {
		t.Fatal("package discovery failed")
	}
	enabled := false
	for _, p := range packages.Items {
		if p.ID == packageID && p.Enabled {
			enabled = true
		}
	}
	if !enabled {
		t.Fatal("approved package is not enabled")
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal("name generation failed")
	}
	name := "vngcloud-live-" + hex.EncodeToString(suffix)
	start := time.Now()
	out, createErr := client.CreateLoadBalancer(ctx, &globalloadbalancer.CreateLoadBalancerInput{Name: name, PackageID: packageID})
	id := ""
	if out != nil {
		id = out.LoadBalancer.ID
	}
	deleted := false
	t.Cleanup(func() {
		if id == "" || deleted {
			return
		}
		cleanupCtx, done := context.WithTimeout(context.Background(), 11*time.Minute)
		defer done()
		result, err := client.DeleteLoadBalancer(cleanupCtx, &globalloadbalancer.DeleteLoadBalancerInput{LoadBalancerID: id})
		if err != nil || result == nil || !result.Deleted {
			t.Error("cleanup could not confirm deletion of the created resource")
		}
	})
	if createErr != nil || id == "" {
		t.Fatal("create did not settle; inspect inventory and billing before another write")
	}
	t.Logf("create state=active elapsed=%s", time.Since(start))
	start = time.Now()
	deleted = true
	result, err := client.DeleteLoadBalancer(ctx, &globalloadbalancer.DeleteLoadBalancerInput{LoadBalancerID: id})
	if err != nil || result == nil || !result.Deleted {
		t.Fatal("delete did not confirm absence")
	}
	deleted = true
	t.Logf("delete state=absent elapsed=%s", time.Since(start))
}
