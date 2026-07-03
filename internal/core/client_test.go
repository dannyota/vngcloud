package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
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

func TestRequireProjectIDConcurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3","userId":7}]}`))
	}))
	defer server.Close()

	c := NewTestClient("hcm-3", "", endpoints.Set{VServer: server.URL + "/"},
		transport.New(transport.Config{HTTPClient: server.Client()}))

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.RequireProjectID(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.ProjectID() != "project-1" {
		t.Fatalf("unexpected project: %s", c.ProjectID())
	}
}
