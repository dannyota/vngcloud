package vngcloud_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"danny.vn/vngcloud"
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
