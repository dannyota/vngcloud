package glb

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestGLBListPackages(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/packages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/glb/list_packages.json")
	}))

	packages, err := service.ListPackages(context.Background())
	if err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}
	if len(packages) != 1 || packages[0].ID != "pkg-1" || !packages[0].Enabled {
		t.Fatalf("unexpected packages: %+v", packages)
	}
}

func TestGLBListRegions(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/regions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/glb/list_regions.json")
	}))

	regions, err := service.ListRegions(context.Background())
	if err != nil {
		t.Fatalf("ListRegions() error = %v", err)
	}
	if len(regions) != 1 || regions[0].ID != "region-1" {
		t.Fatalf("unexpected regions: %+v", regions)
	}
}

func TestGLBNestedRoutes(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		body  string
		call  func(*Service) error
	}{
		{
			name:  "list load balancers",
			path:  "/v1/global-load-balancers",
			query: "limit=10000&name=&offset=0",
			body:  testutil.FixtureBody(t, "../../testdata/glb/list_load_balancers.json"),
			call: func(s *Service) error {
				items, err := s.ListLoadBalancers(context.Background(), nil)
				if err == nil && (len(items.Items) != 1 || items.Items[0].ID != "glb-1") {
					t.Fatalf("unexpected glbs: %+v", items)
				}
				return err
			},
		},
		{
			name: "get load balancer",
			path: "/v1/global-load-balancers/glb-1",
			body: testutil.FixtureBody(t, "../../testdata/glb/get_load_balancer.json"),
			call: func(s *Service) error {
				item, err := s.GetLoadBalancer(context.Background(), "glb-1")
				if err == nil && item.ID != "glb-1" {
					t.Fatalf("unexpected glb: %+v", item)
				}
				return err
			},
		},
		{
			name: "pools",
			path: "/v1/global-load-balancers/glb-1/global-pools",
			body: testutil.FixtureBody(t, "../../testdata/glb/list_pools.json"),
			call: func(s *Service) error {
				items, err := s.ListPools(context.Background(), "glb-1")
				if err == nil && (len(items) != 1 || items[0].ID != "pool-1") {
					t.Fatalf("unexpected pools: %+v", items)
				}
				return err
			},
		},
		{
			name: "listeners",
			path: "/v1/global-load-balancers/glb-1/global-listeners",
			body: testutil.FixtureBody(t, "../../testdata/glb/list_listeners.json"),
			call: func(s *Service) error {
				items, err := s.ListListeners(context.Background(), "glb-1")
				if err == nil && (len(items) != 1 || items[0].ID != "listener-1") {
					t.Fatalf("unexpected listeners: %+v", items)
				}
				return err
			},
		},
		{
			name: "get listener",
			path: "/v1/global-load-balancers/glb-1/global-listeners/listener-1",
			body: testutil.FixtureBody(t, "../../testdata/glb/get_listener.json"),
			call: func(s *Service) error {
				item, err := s.GetListener(context.Background(), "glb-1", "listener-1")
				if err == nil && item.ID != "listener-1" {
					t.Fatalf("unexpected listener: %+v", item)
				}
				return err
			},
		},
		{
			name: "pool members",
			path: "/v1/global-load-balancers/glb-1/global-pools/pool-1/pool-members",
			body: testutil.FixtureBody(t, "../../testdata/glb/list_pool_members.json"),
			call: func(s *Service) error {
				items, err := s.ListPoolMembers(context.Background(), "glb-1", "pool-1")
				if err == nil && (len(items) != 1 || items[0].ID != "member-1") {
					t.Fatalf("unexpected members: %+v", items)
				}
				return err
			},
		},
		{
			name: "get pool member",
			path: "/v1/global-load-balancers/glb-1/global-pools/pool-1/pool-members/member-1",
			body: testutil.FixtureBody(t, "../../testdata/glb/get_pool_member.json"),
			call: func(s *Service) error {
				item, err := s.GetPoolMember(context.Background(), "glb-1", "pool-1", "member-1")
				if err == nil && item.ID != "member-1" {
					t.Fatalf("unexpected member: %+v", item)
				}
				return err
			},
		},
		{
			name: "usage histories",
			path: "/v1/global-load-balancers/glb-1/usage-histories",
			body: testutil.FixtureBody(t, "../../testdata/glb/list_usage_histories.json"),
			call: func(s *Service) error {
				items, err := s.ListUsageHistories(context.Background(), "glb-1", nil)
				if err == nil && len(items.Items) != 1 {
					t.Fatalf("unexpected histories: %+v", items)
				}
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				if tt.query != "" && r.URL.RawQuery != tt.query {
					t.Fatalf("unexpected query: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			if err := tt.call(service); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
