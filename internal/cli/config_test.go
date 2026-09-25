package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
)

func TestLoadConfigAppliesGlobalFlags(t *testing.T) {
	withCleanEnv(t)
	opts := newFakeServer(t, http.NewServeMux())
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("tok"))...)

	flags := &globalFlags{region: "hcm-3", projectID: "proj-1"}
	e := &env{flags: flags, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	cfg, err := loadConfig(context.Background(), e, nil)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Region() != "hcm-3" {
		t.Fatalf("Region() = %q, want hcm-3", cfg.Region())
	}
}

func TestLoadConfigNoHomeMeansNoCache(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	dir, err := tokenCacheDir()
	if err != nil {
		t.Fatalf("tokenCacheDir: %v", err)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want empty when the home directory cannot be found", dir)
	}
}

func TestTokenCacheDirMissingIsFine(t *testing.T) {
	home := withCleanEnv(t)
	dir, err := tokenCacheDir()
	if err != nil {
		t.Fatalf("tokenCacheDir: %v", err)
	}
	want := filepath.Join(home, ".vngcloud", "cache")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
}

func TestTokenCacheDirUnsafePermissionsIsAConfigError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not checked on windows")
	}
	home := withCleanEnv(t)
	dir := filepath.Join(home, ".vngcloud", "cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_, err := tokenCacheDir()
	if err == nil {
		t.Fatalf("expected an error for a world-readable cache directory")
	}
	if !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("error = %v, want it to name the fix", err)
	}
	if exitCode(err) != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode(err))
	}
}

func TestResolveOutput(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile withoutput]\nregion = hcm-3\noutput = table\n\n[profile noOutput]\nregion = hcm-3\n")
	writeCredentialsFile(t, home, "[withoutput]\nusername = u\npassword = p\nroot_email = e@example.com\n\n[noOutput]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	cfgWith, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile("withoutput"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := resolveOutput(&globalFlags{}, cfgWith); got != "table" {
		t.Fatalf("got %q, want table (from the profile)", got)
	}
	if got := resolveOutput(&globalFlags{output: "text"}, cfgWith); got != "text" {
		t.Fatalf("got %q, want text (the flag wins)", got)
	}

	cfgWithout, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithProfile("noOutput"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := resolveOutput(&globalFlags{}, cfgWithout); got != "json" {
		t.Fatalf("got %q, want the json default", got)
	}
}

// secretMarker is a value that must never reach --debug output.
const secretMarker = "marker-secret-do-not-log"

func TestDebugLoggerLogsRequestWithNoSecrets(t *testing.T) {
	withCleanEnv(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/navbar/balances/v1", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secretMarker {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"cash":null,"poc":null,"cashAvailable":null,"cashHolding":null,"pocHolding":null}`)
	})
	opts := newFakeServer(t, mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken(secretMarker))...)

	var stderr bytes.Buffer
	flags := &globalFlags{region: "hcm-3", debug: true}
	e := &env{flags: flags, stdout: &bytes.Buffer{}, stderr: &stderr}
	logger := debugLogger(e)
	if logger == nil {
		t.Fatalf("debugLogger returned nil with --debug set")
	}
	cfg, err := loadConfig(context.Background(), e, logger)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if _, err := billing.New(cfg).GetBalances(context.Background(), nil); err != nil {
		t.Fatalf("GetBalances: %v", err)
	}

	out := stderr.String()
	if !strings.Contains(out, "msg=request") {
		t.Fatalf("expected a request log line, got %q", out)
	}
	if !strings.Contains(out, "method=GET") {
		t.Fatalf("missing method in %q", out)
	}
	if !strings.Contains(out, "path=/navbar/balances/v1") {
		t.Fatalf("missing path in %q", out)
	}
	if !strings.Contains(out, "status=200") {
		t.Fatalf("missing status in %q", out)
	}
	if strings.Contains(out, secretMarker) {
		t.Fatalf("debug output leaked the token: %q", out)
	}
	if strings.Contains(out, "Authorization") || strings.Contains(out, "Bearer") {
		t.Fatalf("debug output leaked the auth header: %q", out)
	}
	if strings.Contains(out, "?") {
		t.Fatalf("debug output has a query string: %q", out)
	}
}

func TestDebugLoggerNilWithoutFlag(t *testing.T) {
	e := &env{flags: &globalFlags{debug: false}, stderr: &bytes.Buffer{}}
	if got := debugLogger(e); got != nil {
		t.Fatalf("debugLogger() = %v, want nil without --debug", got)
	}
}
