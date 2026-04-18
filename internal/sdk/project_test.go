package sdk

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/compute"
	"danny.vn/vngcloud/internal/containerregistry"
	"danny.vn/vngcloud/internal/dns"
	"danny.vn/vngcloud/internal/glb"
	"danny.vn/vngcloud/internal/loadbalancer"
	"danny.vn/vngcloud/internal/network"
	"danny.vn/vngcloud/internal/portal"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/volume"
)

func TestListProjects(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/project/list_projects.json")
	}))

	projects, err := client.ListProjects(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != "project-1" || projects[0].Region != "hcm-3" {
		t.Fatalf("unexpected projects: %+v", projects)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	c := &Client{Client: testutil.NewCoreClient(t, handler)}
	c.Compute = compute.New(c.Client)
	c.Network = network.New(c.Client)
	c.Volume = volume.New(c.Client)
	c.LoadBalancer = loadbalancer.New(c.Client)
	c.GlobalLoadBalancer = glb.New(c.Client)
	c.DNS = dns.New(c.Client)
	c.ContainerRegistry = containerregistry.New(c.Client)
	c.Portal = portal.New(c.Client)
	return c
}
