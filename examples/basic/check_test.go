package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSmokeOutputNestedAndBlocked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blocked bool
	}{
		{name: "nested_sdk_only"},
		{name: "blocked_raw_parent", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "sdk", "network", "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "sdk", "network", "nested", "resource.json"), []byte(`{"regions":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "raw", "other"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "raw", "other", "resource.json"), []byte(`{"regions":[]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.blocked {
				if err := os.WriteFile(filepath.Join(root, "raw", "network"), []byte("block"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			reports, err := checkSmokeOutput(root)
			if err != nil {
				return
			}
			for _, report := range reports {
				if report.resource == "network/nested/resource" && report.sdkWithoutRaw {
					return
				}
			}
			t.Fatal("unmatched nested SDK artifact was not reported")
		})
	}
}

func TestCheckSmokeOutputIntegrity(t *testing.T) {
	const raw = `{"regions":[{"statusCode":200,"body":[]}]}`
	const sdk = `{"regions":[{"count":0,"items":[]}]}`
	tests := []struct {
		name        string
		raw         string
		sdk         string
		wantPass    bool
		wantSDKOnly bool
	}{
		{name: "sdk_error", raw: raw, sdk: `{"regions":[{"error":"decode failed"}]}`},
		{name: "missing_sdk", raw: raw},
		{name: "missing_raw_sdk_only", sdk: sdk, wantSDKOnly: true, wantPass: true},
		{name: "sdk_only_error", sdk: `{"regions":[{"error":"decode failed"}]}`, wantSDKOnly: true},
		{name: "sdk_only_malformed", sdk: sdk + ` garbage`, wantSDKOnly: true},
		{name: "malformed_raw", raw: raw + ` garbage`, sdk: sdk},
		{name: "malformed_sdk", raw: raw, sdk: sdk + ` garbage`},
		{name: "explicit_empty", raw: raw, sdk: sdk, wantPass: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for kind, content := range map[string]string{"raw": tc.raw, "sdk": tc.sdk} {
				if content == "" {
					continue
				}
				dir := filepath.Join(root, kind, "service")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "resource.json"), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			reports, err := checkSmokeOutput(root)
			if err != nil {
				t.Fatalf("checkSmokeOutput: %v", err)
			}
			if len(reports) != 1 {
				t.Fatalf("got %d reports, want 1", len(reports))
			}
			var out bytes.Buffer
			if got := writeSmokeCheck(&out, reports); got != tc.wantPass {
				t.Errorf("pass = %t, want %t", got, tc.wantPass)
			}
			if tc.wantSDKOnly && !strings.Contains(out.String(), "sdk_only=1 paths=[service/resource]") {
				t.Errorf("missing SDK-only information: %q", out.String())
			}
			if reports[0].sdkWithoutRaw != tc.wantSDKOnly {
				t.Errorf("sdkWithoutRaw = %t, want %t", reports[0].sdkWithoutRaw, tc.wantSDKOnly)
			}
		})
	}
}
