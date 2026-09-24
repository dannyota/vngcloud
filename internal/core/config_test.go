package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestNewConfigRequiresRegion(t *testing.T) {
	_, err := NewConfig(WithStaticToken("tok"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestNewConfigRequiresCredentials(t *testing.T) {
	_, err := NewConfig(WithRegion("hcm-3"))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestNewConfigStaticTokenNeedsNoIAMUser(t *testing.T) {
	cfg, err := NewConfig(WithRegion("hcm-3"), WithStaticToken("tok"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() = %v", err)
	}
}

func TestZeroConfigFailsWithoutPanic(t *testing.T) {
	c := ClientOf(Config{})
	err := c.DoJSON(context.Background(), transport.Request{Operation: "x.Y", Method: "GET", URL: "http://127.0.0.1/", OK: []int{200}}, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if _, err := c.RequireProjectID(context.Background()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("RequireProjectID err = %v", err)
	}
}

func TestConfigSharesProjectDiscovery(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"projectId": "p-1", "region": "hcm-3"}}})
	}))
	defer srv.Close()
	cfg := NewTestConfig("hcm-3", "", endpoints.Set{Region: "hcm-3", VServer: srv.URL + "/"}, transport.New(transport.Config{HTTPClient: srv.Client()}))
	a, b := ClientOf(cfg), ClientOf(cfg)
	var wg sync.WaitGroup
	for _, c := range []*Client{a, b, a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.RequireProjectID(context.Background()) }()
	}
	wg.Wait()
	if a != b || calls.Load() != 1 {
		t.Fatalf("same client = %v, discovery calls = %d, want true and 1", a == b, calls.Load())
	}
}
