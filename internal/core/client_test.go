package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientDoesNotMutateAuthConfig(t *testing.T) {
	auth := &IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "pass"}
	if _, err := NewClient(Config{Region: "hcm-3", IAMUser: auth}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if auth.SigninBaseURL != "" || auth.TokenURL != "" || auth.DashboardURI != "" {
		t.Fatalf("NewClient mutated the caller's IAMUserAuth: %+v", auth)
	}
}

func TestBuildHTTPClientRejectsCrossHostRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusMovedPermanently)
	}))
	defer source.Close()

	client := buildHTTPClient(clientConfig{timeout: 5 * time.Second})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := client.Do(req) //nolint:bodyclose // err path has no body
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("expected cross-host redirect error, got nil")
	}
	if !strings.Contains(err.Error(), "endpoints have moved") {
		t.Fatalf("expected cross-host redirect error, got %v", err)
	}
}
