package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
		{name: "missing_raw_sdk_only", sdk: sdk, wantSDKOnly: true},
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
			if got := printSmokeCheck(reports); got != tc.wantPass {
				t.Errorf("pass = %t, want %t", got, tc.wantPass)
			}
			if reports[0].sdkWithoutRaw != tc.wantSDKOnly {
				t.Errorf("sdkWithoutRaw = %t, want %t", reports[0].sdkWithoutRaw, tc.wantSDKOnly)
			}
		})
	}
}
