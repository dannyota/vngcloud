package loadbalancer

import (
	"context"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestLoadBalancerListLoadBalancers(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/loadBalancers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/loadbalancer/list_load_balancers.json")
	}))

	lbs, err := service.ListLoadBalancers(context.Background(), &ListLoadBalancersOptions{Name: "main", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListLoadBalancers() error = %v", err)
	}
	if len(lbs.Items) != 1 || lbs.Items[0].UUID != "lb-1" || len(lbs.Items[0].Nodes) != 1 {
		t.Fatalf("unexpected load balancers: %+v", lbs)
	}
}

func TestLoadBalancerListDefaultPageSize(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/loadbalancer/list_load_balancers.json")
	}))

	if _, err := service.ListLoadBalancers(context.Background(), nil); err != nil {
		t.Fatalf("ListLoadBalancers() error = %v", err)
	}
}

func TestLoadBalancerListPackages(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/loadBalancers/packages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/loadbalancer/list_packages.json")
	}))

	packages, err := service.ListLoadBalancerPackages(context.Background(), &ListLoadBalancerPackagesOptions{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListLoadBalancerPackages() error = %v", err)
	}
	if len(packages) != 1 || packages[0].UUID != "pkg-1" || packages[0].ConnectionNumber != 1000 {
		t.Fatalf("unexpected packages: %+v", packages)
	}
}

func TestLoadBalancerListCertificates(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/cas" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/loadbalancer/list_certificates.json")
	}))

	certs, err := service.ListCertificates(context.Background(), &ListCertificatesOptions{Name: "main"})
	if err != nil {
		t.Fatalf("ListCertificates() error = %v", err)
	}
	if len(certs.Items) != 1 || certs.Items[0].UUID != "cert-1" || !certs.Items[0].InUse {
		t.Fatalf("unexpected certificates: %+v", certs)
	}
}

func TestLoadBalancerNestedRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Service) error
	}{
		{
			name: "get load balancer",
			path: "/v2/project-1/loadBalancers/lb-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_load_balancer.json"),
			call: func(s *Service) error {
				lb, err := s.GetLoadBalancer(context.Background(), "lb-1")
				if err == nil && lb.UUID != "lb-1" {
					t.Fatalf("unexpected load balancer: %+v", lb)
				}
				return err
			},
		},
		{
			name: "listeners",
			path: "/v2/project-1/loadBalancers/lb-1/listeners",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/list_listeners.json"),
			call: func(s *Service) error {
				items, err := s.ListListeners(context.Background(), "lb-1")
				if err == nil && (len(items) != 1 || items[0].UUID != "listener-1") {
					t.Fatalf("unexpected listeners: %+v", items)
				}
				return err
			},
		},
		{
			name: "get listener",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_listener.json"),
			call: func(s *Service) error {
				item, err := s.GetListener(context.Background(), "lb-1", "listener-1")
				if err == nil && item.UUID != "listener-1" {
					t.Fatalf("unexpected listener: %+v", item)
				}
				return err
			},
		},
		{
			name: "pools",
			path: "/v2/project-1/loadBalancers/lb-1/pools",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/list_pools.json"),
			call: func(s *Service) error {
				items, err := s.ListPools(context.Background(), "lb-1")
				if err == nil && (len(items) != 1 || len(items[0].Members) != 1) {
					t.Fatalf("unexpected pools: %+v", items)
				}
				return err
			},
		},
		{
			name: "get pool",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_pool.json"),
			call: func(s *Service) error {
				item, err := s.GetPool(context.Background(), "lb-1", "pool-1")
				if err == nil && (item.UUID != "pool-1" || len(item.Members) != 1) {
					t.Fatalf("unexpected pool: %+v", item)
				}
				return err
			},
		},
		{
			name: "get pool health monitor",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1/healthMonitor",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_pool_health_monitor.json"),
			call: func(s *Service) error {
				item, err := s.GetPoolHealthMonitor(context.Background(), "lb-1", "pool-1")
				if err == nil && item.HealthCheckProtocol != "HTTP" {
					t.Fatalf("unexpected health monitor: %+v", item)
				}
				return err
			},
		},
		{
			name: "pool members",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1/members",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/list_pool_members.json"),
			call: func(s *Service) error {
				items, err := s.ListPoolMembers(context.Background(), "lb-1", "pool-1")
				if err == nil && (len(items) != 1 || items[0].UUID != "member-1") {
					t.Fatalf("unexpected members: %+v", items)
				}
				return err
			},
		},
		{
			name: "policies",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1/l7policies",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/list_policies.json"),
			call: func(s *Service) error {
				items, err := s.ListPolicies(context.Background(), "lb-1", "listener-1")
				if err == nil && (len(items) != 1 || items[0].UUID != "policy-1") {
					t.Fatalf("unexpected policies: %+v", items)
				}
				return err
			},
		},
		{
			name: "get policy",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1/l7policies/policy-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_policy.json"),
			call: func(s *Service) error {
				item, err := s.GetPolicy(context.Background(), "lb-1", "listener-1", "policy-1")
				if err == nil && (item.UUID != "<policy-id>" || len(item.L7Rules) != 1) {
					t.Fatalf("unexpected policy: %+v", item)
				}
				return err
			},
		},
		{
			name: "tags",
			path: "/v2/project-1/tag/resource/lb-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/list_tags.json"),
			call: func(s *Service) error {
				items, err := s.ListTags(context.Background(), "lb-1")
				if err == nil && (len(items) != 1 || items[0].Key != "env") {
					t.Fatalf("unexpected tags: %+v", items)
				}
				return err
			},
		},
		{
			name: "get certificate",
			path: "/v2/project-1/cas/cert-1",
			body: testutil.FixtureBody(t, "../../testdata/loadbalancer/get_certificate.json"),
			call: func(s *Service) error {
				item, err := s.GetCertificate(context.Background(), "cert-1")
				if err == nil && item.UUID != "cert-1" {
					t.Fatalf("unexpected certificate: %+v", item)
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
