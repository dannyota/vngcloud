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

	_, err := NewClient(context.Background(), core.Config{
		Region:  "hcm-3",
		IAMUser: &core.IAMUserAuth{RootEmail: "r", Username: "u", Password: "p", SigninBaseURL: signin.URL, TokenURL: signin.URL, DashboardURI: signin.URL + "/"},
	})
	if err == nil {
		t.Fatal("expected eager authentication failure")
	}
}

func TestNewClientStaticTokenSkipsLogin(t *testing.T) {
	c, err := NewClient(context.Background(), core.Config{Region: "hcm-3"}, core.WithStaticToken("tok"))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if c.Compute == nil || c.DNS == nil {
		t.Fatal("services not wired")
	}
}
