//go:build livewrite

package livetest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATHoldRegionRelease(t *testing.T) {
	requireNATHoldPlatform(t)
	for _, tc := range []struct {
		region string
		valid  bool
	}{{"han-1", true}, {"hcm-3", false}} {
		t.Run(tc.region, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			h, err := newNATHoldRegion(dir, "2s", tc.region)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			if err = os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			err = h.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error { return nil })
			if (err == nil) != tc.valid {
				t.Fatalf("release error %v", err)
			}
		})
	}
}

func TestNATHarnessRoutePagePresence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"complete", `{"listData":[],"page":1,"pageSize":1000,"totalPage":0,"totalItem":0}`, true},
		{"no array", `{"page":1,"pageSize":1000,"totalPage":0,"totalItem":0}`, false},
		{"wrong case", `{"ListData":[],"page":1,"pageSize":1000,"totalPage":0,"totalItem":0}`, false},
		{"null array", `{"listData":null,"page":1,"pageSize":1000,"totalPage":0,"totalItem":0}`, false},
		{"no totals", `{"listData":[],"page":1,"pageSize":1000}`, false},
		{"bad total", `{"listData":[],"page":1,"pageSize":1000,"totalPage":"0","totalItem":0}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer server.Close()
			cfg, err := vngcloud.NewConfig(vngcloud.WithStaticToken("synthetic"), vngcloud.WithProjectID("project-1"), vngcloud.WithRegion("han-1"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL}), vngcloud.WithRetry(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			_, err = liveNATRoutes(context.Background(), cfg, "vpc-1")
			if (err == nil) != tc.valid {
				t.Fatalf("route page accepted=%v error=%v", err == nil, err)
			}
		})
	}
}
