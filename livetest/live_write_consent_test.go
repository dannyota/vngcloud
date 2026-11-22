//go:build livewrite

package livetest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyWriteRequiresOwnGate(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		gate string
	}{
		{"TestLiveWrite", "VNGCLOUD_LIVE_BILLING"},
		{"TestLiveWriteMonitor", "VNGCLOUD_LIVE_MONITOR"},
		{"TestLiveWriteDNS", "VNGCLOUD_LIVE_DNS"},
		{"TestLiveWriteMonitorChannel", "VNGCLOUD_LIVE_MONITOR_CHANNEL"},
		{"TestLiveWriteMonitorCheckNotifications", "VNGCLOUD_LIVE_MONITOR_CHECK_NOTIFICATIONS"},
		{"TestLiveWriteMonitorChannelOTP", "VNGCLOUD_LIVE_MONITOR_CHANNEL_OTP"},
		{"TestLiveWriteContainerRegistryRepository", "VNGCLOUD_LIVE_VCR_REPOSITORY"},
		{"TestLiveWriteContainerRegistryUser", "VNGCLOUD_LIVE_VCR_USER"},
		{"TestLiveWriteStorageBucket", "VNGCLOUD_LIVE_STORAGE_BUCKET"},
		{"TestLiveWriteStorageS3Key", "VNGCLOUD_LIVE_STORAGE_S3_KEY"},
		{"TestLiveWriteStorageServiceAccountKey", "VNGCLOUD_LIVE_STORAGE_SERVICE_ACCOUNT_KEY"},
		{"TestLiveWriteStorageBucketPolicy", "VNGCLOUD_LIVE_STORAGE_BUCKET_POLICY"},
		{"TestLiveWriteStorageBucketSettings", "VNGCLOUD_LIVE_STORAGE_BUCKET_SETTINGS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module synthetic.invalid/consent\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"", "true", " 1"} {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				command := exec.CommandContext(ctx, binary, "-test.run=^"+tc.name+"$", "-test.v", "-test.count=1")
				command.Dir = dir
				command.Env = []string{"HOME=" + dir, "VNGCLOUD_LIVE_WRITE=1", "VNGCLOUD_LIVE_VCR=1", "VNGCLOUD_LIVE_STORAGE_PROJECT_ID=synthetic-project", tc.gate + "=" + value}
				output, err := command.CombinedOutput()
				cancel()
				if err != nil || !strings.Contains(string(output), "--- SKIP: "+tc.name) || !strings.Contains(string(output), tc.gate+"=1") {
					t.Fatalf("operation gate did not refuse before credential loading: %v\n%s", err, output)
				}
			}
		})
	}
}
