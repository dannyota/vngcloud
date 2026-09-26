//go:build live

package vngcloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/cli"
	"danny.vn/vngcloud/internal/envfile"
)

// TestLiveCLI runs one read command per service through internal/cli.Main,
// in-process against the real API, and checks that each exits 0 and prints
// valid JSON. It points HOME (and USERPROFILE, for a Windows run) at
// liveHome, the same directory TestLive (live_test.go) builds its token
// cache dir under, so the CLI's fixed ~/.vngcloud/cache resolves to the
// identical directory: whichever of the two tests logs in first, the other
// reuses its cached token instead of logging in again inside one 30-second
// TOTP window.
func TestLiveCLI(t *testing.T) {
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	if os.Getenv("VNGCLOUD_ACCESS_TOKEN") == "" &&
		(os.Getenv("VNGCLOUD_ROOT_EMAIL") == "" || os.Getenv("VNGCLOUD_USERNAME") == "" || os.Getenv("VNGCLOUD_PASSWORD") == "") {
		t.Skip("set VNGCLOUD_ROOT_EMAIL/VNGCLOUD_USERNAME/VNGCLOUD_PASSWORD or VNGCLOUD_ACCESS_TOKEN in .env")
	}

	t.Setenv("HOME", liveHome)
	t.Setenv("USERPROFILE", liveHome)

	// An empty, explicit config and credentials file, both mode 0600, keep
	// the CLI's own LoadConfig call from reading the real ~/.vngcloud, which
	// could hold a different profile than the account named in .env;
	// credentials come from .env's environment variables instead.
	fileDir := t.TempDir()
	configFile := filepath.Join(fileDir, "config")
	credentialsFile := filepath.Join(fileDir, "credentials")
	if err := os.WriteFile(configFile, nil, 0o600); err != nil {
		t.Fatalf("create empty config file: %v", err)
	}
	if err := os.WriteFile(credentialsFile, nil, 0o600); err != nil {
		t.Fatalf("create empty credentials file: %v", err)
	}
	t.Setenv("VNGCLOUD_CONFIG_FILE", configFile)
	t.Setenv("VNGCLOUD_SHARED_CREDENTIALS_FILE", credentialsFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Run("billing", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "billing", "list-budgets")
	})
	t.Run("pricing", func(t *testing.T) {
		stdout, _ := runLiveCLI(ctx, t, "pricing", "get-quote", "--resource-type", "snapshot")
		if !json.Valid(stdout) {
			t.Fatal("stdout is not valid JSON")
		}
		t.Log("ok")
	})
	t.Run("compute", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "compute", "list-servers")
	})
	t.Run("network", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "network", "list-vpcs")
	})
	t.Run("dns", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "dns", "list-hosted-zones")
	})
	t.Run("cdn", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "cdn", "list-ip-ranges")
	})
	t.Run("monitor", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "monitor", "list-checks")
		testLiveCLIItems(ctx, t, "monitor", "list-locations")
		testLiveCLIItems(ctx, t, "monitor", "list-channel-types")
		testLiveCLIItems(ctx, t, "monitor", "list-channels")
	})
	t.Run("project", func(t *testing.T) {
		testLiveCLIItemsAtLeastOne(ctx, t, "project", "list-projects")
	})
	t.Run("portal", func(t *testing.T) {
		testLiveCLIItems(ctx, t, "portal", "list-zones")
	})
}

// testLiveCLIItems runs a list command through the CLI and logs the length
// of its decoded Items field, never any item's values.
func testLiveCLIItems(ctx context.Context, t *testing.T, args ...string) {
	t.Helper()
	stdout, _ := runLiveCLI(ctx, t, args...)

	var decoded struct {
		Items []json.RawMessage
	}
	if err := json.Unmarshal(stdout, &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	t.Logf("items: %d", len(decoded.Items))
}

// testLiveCLIItemsAtLeastOne runs a list command through the CLI like
// testLiveCLIItems, and additionally fails when the decoded Items field is
// empty. list-projects is the only caller today: LoadConfig's project
// discovery (the CLI reads design's "Scope rules") needs exactly one project
// in the configured region, so an empty result here means the read itself
// is broken, not that the account happens to have none. It never logs an
// item's value, only the count.
func testLiveCLIItemsAtLeastOne(ctx context.Context, t *testing.T, args ...string) {
	t.Helper()
	stdout, _ := runLiveCLI(ctx, t, args...)

	var decoded struct {
		Items []json.RawMessage
	}
	if err := json.Unmarshal(stdout, &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	t.Logf("items: %d", len(decoded.Items))
	if len(decoded.Items) == 0 {
		t.Fatal("expected at least one item")
	}
}

// runLiveCLI runs args through cli.Main with --output json and --region
// hcm-3 appended, and fails the test unless it exits 0. On failure it logs
// only the error envelope's Code field (see errors.go's errorEnvelope in
// internal/cli), never Message, which may name account data.
func runLiveCLI(ctx context.Context, t *testing.T, args ...string) (stdout, stderr []byte) {
	t.Helper()
	fullArgs := append(append([]string{}, args...), "--output", "json", "--region", "hcm-3")

	var outBuf, errBuf bytes.Buffer
	code := cli.Main(ctx, fullArgs, strings.NewReader(""), &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("%s: exit code %d, error code %s", strings.Join(args, " "), code, liveCLIErrorCode(errBuf.Bytes()))
	}
	return outBuf.Bytes(), errBuf.Bytes()
}

// liveCLIErrorCode extracts only the "code" field from a failed command's
// one-line stderr JSON envelope, so a test failure never logs the
// accompanying message, which may name account or resource data.
func liveCLIErrorCode(stderr []byte) string {
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &decoded); err != nil {
		return "unknown"
	}
	return decoded.Error.Code
}
