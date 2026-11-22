package network

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestVPNWaiterCancelsWhileProjectDiscoveryHeld(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" {
			t.Errorf("unexpected request %s", r.URL.Path)
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3"}]}`))
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(0, 0), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: server.URL, VNetwork: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	holder := make(chan error, 1)
	go func() { _, err := c.c.RequireProjectID(context.Background()); holder <- err }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waiter := make(chan error, 1)
	go func() { _, err := c.ListVPNConnections(ctx, nil); waiter <- err }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			close(release)
			t.Fatalf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("waiter blocked behind discovery")
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
}
