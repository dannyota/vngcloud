package vngcloud_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/volume"
)

func TestNewConfigRequiresRegion(t *testing.T) {
	if _, err := vngcloud.NewConfig(vngcloud.WithStaticToken("tok")); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("NewConfig() err = %v, want ErrInvalidConfig", err)
	}
}

func TestNewConfigWithStaticTokenSucceeds(t *testing.T) {
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if cfg.Region() != "hcm-3" {
		t.Fatalf("Region() = %q, want hcm-3", cfg.Region())
	}
}

// TestConfigAuthenticateFailsFastOnBadAuth checks that Authenticate, not
// NewConfig, is what surfaces a bad-credential failure: NewConfig no longer
// logs in eagerly, so a caller that wants to fail fast calls
// cfg.Authenticate(ctx) itself before its first service call.
func TestConfigAuthenticateFailsFastOnBadAuth(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail:     "r",
			Username:      "u",
			Password:      "p",
			SigninBaseURL: signin.URL,
			TokenURL:      signin.URL,
			DashboardURI:  signin.URL + "/",
		}),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if err := cfg.Authenticate(context.Background()); !errors.Is(err, vngcloud.ErrAuth) {
		t.Fatalf("Authenticate() err = %v, want ErrAuth", err)
	}
}

// TestTwoPackagesOneLogin builds a compute and a volume client from one
// Config and calls both concurrently, through the real IAM User login flow
// and real service packages, to confirm they share one login rather than
// each service client logging in on its own.
func TestTwoPackagesOneLogin(t *testing.T) {
	var logins atomic.Int64
	var tokenURL string

	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case http.MethodPost:
			http.Redirect(w, r, tokenURL+"/callback?code=auth-code", http.StatusFound)
		}
	}))
	defer signin.Close()

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := logins.Add(1)
		_, _ = fmt.Fprintf(w, `{"accessToken":"tok-%d","expiresIn":3600}`, n)
	}))
	defer token.Close()
	tokenURL = token.URL

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10,"totalPage":1,"totalItem":0}`))
	}))
	defer api.Close()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithProjectID("project-1"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail:     "root@example.test",
			Username:      "user",
			Password:      "pass",
			SigninBaseURL: signin.URL,
			TokenURL:      token.URL,
			DashboardURI:  token.URL + "/",
		}),
		vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: api.URL}),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := compute.New(cfg).ListServers(context.Background(), &compute.ListServersInput{Page: 1, Size: 5})
		errs[0] = err
	}()
	go func() {
		defer wg.Done()
		_, err := volume.New(cfg).ListVolumes(context.Background(), &volume.ListVolumesInput{Page: 1, Size: 5})
		errs[1] = err
	}()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d error = %v", i, err)
		}
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

// clearVNGCloudEnvAndHome points HOME and USERPROFILE at a fresh temp
// directory and clears every VNGCLOUD_* variable, so LoadConfig never reads
// the real home directory or a value left over from the host environment. It
// returns the temp directory, so a caller that needs to write a file under
// it uses this value directly rather than reading HOME back through
// os.Getenv, which a path-traversal linter treats as tainted input.
func clearVNGCloudEnvAndHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	for _, key := range []string{
		"VNGCLOUD_PROFILE", "VNGCLOUD_REGION", "VNGCLOUD_PROJECT_ID",
		"VNGCLOUD_ROOT_EMAIL", "VNGCLOUD_USERNAME", "VNGCLOUD_PASSWORD", "VNGCLOUD_TOTP_SECRET",
		"VNGCLOUD_ACCESS_TOKEN", "VNGCLOUD_CONFIG_FILE", "VNGCLOUD_SHARED_CREDENTIALS_FILE",
	} {
		t.Setenv(key, "")
	}
	return dir
}

func TestLoadConfigFromOptionsOnly(t *testing.T) {
	clearVNGCloudEnvAndHome(t)

	cfg, err := vngcloud.LoadConfig(context.Background(),
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithStaticToken("tok"),
	)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Region() != "hcm-3" {
		t.Fatalf("Region() = %q, want hcm-3", cfg.Region())
	}
}

func TestLoadConfigMissingRegionIsInvalidConfig(t *testing.T) {
	clearVNGCloudEnvAndHome(t)

	if _, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithStaticToken("tok")); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("LoadConfig() err = %v, want ErrInvalidConfig", err)
	}
}

func TestLoadConfigNoCredentialsIsErrNoCredentials(t *testing.T) {
	clearVNGCloudEnvAndHome(t)

	if _, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithRegion("hcm-3")); !errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatalf("LoadConfig() err = %v, want ErrNoCredentials", err)
	}
}

// TestAuthenticateReturnsLoginError checks that a login failure surfaces as
// *vngcloud.LoginError through the public API, with ErrAuth in its chain and
// no status-500 body text.
func TestAuthenticateReturnsLoginError(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail:     "r",
			Username:      "u",
			Password:      "p",
			SigninBaseURL: signin.URL,
			TokenURL:      signin.URL,
			DashboardURI:  signin.URL + "/",
		}),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}

	authErr := cfg.Authenticate(context.Background())
	var loginErr *vngcloud.LoginError
	if !errors.As(authErr, &loginErr) {
		t.Fatalf("error is not a *vngcloud.LoginError: %v", authErr)
	}
	if loginErr.Status != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want 500", loginErr.Status)
	}
	if !errors.Is(authErr, vngcloud.ErrAuth) {
		t.Fatalf("errors.Is(authErr, ErrAuth) = false")
	}
}

// TestLoadConfigProfileSetting checks Config.ProfileSetting through the
// public API: it returns the resolved profile's config-file value, and
// NewConfig configs (no profile) always return "".
func TestLoadConfigProfileSetting(t *testing.T) {
	home := clearVNGCloudEnvAndHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".vngcloud"), 0o700); err != nil {
		t.Fatal(err)
	}
	configContent := "[default]\nregion = hcm-3\noutput = table\n"
	if err := os.WriteFile(filepath.Join(home, ".vngcloud", "config"), []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := vngcloud.LoadConfig(context.Background(), vngcloud.WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := cfg.ProfileSetting("output"); got != "table" {
		t.Fatalf(`ProfileSetting("output") = %q, want "table"`, got)
	}

	staticCfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	if got := staticCfg.ProfileSetting("output"); got != "" {
		t.Fatalf(`ProfileSetting("output") = %q, want "" for NewConfig`, got)
	}
}

func TestPtr(t *testing.T) {
	p := vngcloud.Ptr(false)
	if *p != false {
		t.Fatalf("*Ptr(false) = %v, want false", *p)
	}
	q := vngcloud.Ptr(false)
	if p == q {
		t.Fatal("Ptr returned the same pointer for two calls")
	}
}
