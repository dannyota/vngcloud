package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"danny.vn/vngcloud/internal/core"
)

func TestNewClientFailsFastOnBadAuth(t *testing.T) {
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer signin.Close()

	cfg, err := core.NewConfig(
		core.WithRegion("hcm-3"),
		core.WithIAMUser(&core.IAMUserAuth{RootEmail: "r", Username: "u", Password: "p", SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/"}),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	_, err = NewClient(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected eager authentication failure")
	}
}

func TestNewClientStaticTokenSkipsLogin(t *testing.T) {
	cfg, err := core.NewConfig(core.WithRegion("hcm-3"), core.WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	c, err := NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if c.Network == nil || c.LoadBalancer == nil {
		t.Fatal("services not wired")
	}
}
