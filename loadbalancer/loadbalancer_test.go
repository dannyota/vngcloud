package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestLoadBalancerListLoadBalancers(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/loadBalancers" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/list_load_balancers.json")
	}))

	out, err := c.ListLoadBalancers(context.Background(), &ListLoadBalancersInput{Name: "main", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListLoadBalancers() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "lb-1" || len(out.Items[0].Nodes) != 1 {
		t.Fatalf("unexpected load balancers: %+v", out)
	}
}

func TestLoadBalancerListDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/list_load_balancers.json")
	}))

	if _, err := c.ListLoadBalancers(context.Background(), nil); err != nil {
		t.Fatalf("ListLoadBalancers() error = %v", err)
	}
}

func TestLoadBalancerListPackages(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/loadBalancers/packages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/list_packages.json")
	}))

	out, err := c.ListPackages(context.Background(), &ListPackagesInput{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "pkg-1" || out.Items[0].ConnectionNumber != 1000 {
		t.Fatalf("unexpected packages: %+v", out)
	}
}

func TestLoadBalancerListCertificates(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/cas" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "main" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/loadbalancer/list_certificates.json")
	}))

	out, err := c.ListCertificates(context.Background(), &ListCertificatesInput{Name: "main"})
	if err != nil {
		t.Fatalf("ListCertificates() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "cert-1" || !out.Items[0].InUse {
		t.Fatalf("unexpected certificates: %+v", out)
	}
}

func TestLoadBalancerNestedRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "get load balancer",
			path: "/v2/project-1/loadBalancers/lb-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_load_balancer.json"),
			call: func(c *Client) error {
				out, err := c.GetLoadBalancer(context.Background(), &GetLoadBalancerInput{LoadBalancerID: "lb-1"})
				if err == nil && out.LoadBalancer.UUID != "lb-1" {
					t.Fatalf("unexpected load balancer: %+v", out.LoadBalancer)
				}
				return err
			},
		},
		{
			name: "listeners",
			path: "/v2/project-1/loadBalancers/lb-1/listeners",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/list_listeners.json"),
			call: func(c *Client) error {
				out, err := c.ListListeners(context.Background(), &ListListenersInput{LoadBalancerID: "lb-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].UUID != "listener-1") {
					t.Fatalf("unexpected listeners: %+v", out)
				}
				return err
			},
		},
		{
			name: "get listener",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_listener.json"),
			call: func(c *Client) error {
				out, err := c.GetListener(context.Background(), &GetListenerInput{LoadBalancerID: "lb-1", ListenerID: "listener-1"})
				if err == nil && out.Listener.UUID != "listener-1" {
					t.Fatalf("unexpected listener: %+v", out.Listener)
				}
				return err
			},
		},
		{
			name: "pools",
			path: "/v2/project-1/loadBalancers/lb-1/pools",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/list_pools.json"),
			call: func(c *Client) error {
				out, err := c.ListPools(context.Background(), &ListPoolsInput{LoadBalancerID: "lb-1"})
				if err == nil && (len(out.Items) != 1 || len(out.Items[0].Members) != 1) {
					t.Fatalf("unexpected pools: %+v", out)
				}
				return err
			},
		},
		{
			name: "get pool",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_pool.json"),
			call: func(c *Client) error {
				out, err := c.GetPool(context.Background(), &GetPoolInput{LoadBalancerID: "lb-1", PoolID: "pool-1"})
				if err == nil && (out.Pool.UUID != "pool-1" || len(out.Pool.Members) != 1) {
					t.Fatalf("unexpected pool: %+v", out.Pool)
				}
				return err
			},
		},
		{
			name: "get pool health monitor",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1/healthMonitor",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_pool_health_monitor.json"),
			call: func(c *Client) error {
				out, err := c.GetPoolHealthMonitor(context.Background(), &GetPoolHealthMonitorInput{LoadBalancerID: "lb-1", PoolID: "pool-1"})
				if err == nil && out.HealthMonitor.HealthCheckProtocol != "HTTP" {
					t.Fatalf("unexpected health monitor: %+v", out.HealthMonitor)
				}
				return err
			},
		},
		{
			name: "pool members",
			path: "/v2/project-1/loadBalancers/lb-1/pools/pool-1/members",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/list_pool_members.json"),
			call: func(c *Client) error {
				out, err := c.ListPoolMembers(context.Background(), &ListPoolMembersInput{LoadBalancerID: "lb-1", PoolID: "pool-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].UUID != "member-1") {
					t.Fatalf("unexpected members: %+v", out)
				}
				return err
			},
		},
		{
			name: "policies",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1/l7policies",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/list_policies.json"),
			call: func(c *Client) error {
				out, err := c.ListPolicies(context.Background(), &ListPoliciesInput{LoadBalancerID: "lb-1", ListenerID: "listener-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].UUID != "policy-1") {
					t.Fatalf("unexpected policies: %+v", out)
				}
				return err
			},
		},
		{
			name: "get policy",
			path: "/v2/project-1/loadBalancers/lb-1/listeners/listener-1/l7policies/policy-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_policy.json"),
			call: func(c *Client) error {
				out, err := c.GetPolicy(context.Background(), &GetPolicyInput{LoadBalancerID: "lb-1", ListenerID: "listener-1", PolicyID: "policy-1"})
				if err == nil && (out.Policy.UUID != "<policy-id>" || len(out.Policy.L7Rules) != 1) {
					t.Fatalf("unexpected policy: %+v", out.Policy)
				}
				return err
			},
		},
		{
			name: "tags",
			path: "/v2/project-1/tag/resource/lb-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/list_tags.json"),
			call: func(c *Client) error {
				out, err := c.ListTags(context.Background(), &ListTagsInput{LoadBalancerID: "lb-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].Key != "env") {
					t.Fatalf("unexpected tags: %+v", out)
				}
				return err
			},
		},
		{
			name: "get certificate",
			path: "/v2/project-1/cas/cert-1",
			body: testutil.FixtureBody(t, "../testdata/loadbalancer/get_certificate.json"),
			call: func(c *Client) error {
				out, err := c.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "cert-1"})
				if err == nil && out.Certificate.UUID != "cert-1" {
					t.Fatalf("unexpected certificate: %+v", out.Certificate)
				}
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			if err := tt.call(c); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func TestLoadBalancerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetPolicy(context.Background(), &GetPolicyInput{LoadBalancerID: "lb-1", ListenerID: "listener-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "PolicyID") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetPolicy(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestLoadBalancerZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListLoadBalancers(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListLoadBalancers() err = %v, want ErrInvalidConfig", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}

// sequentialTokenSource hands out a new access token on every call, so a
// newOnceTestClient can exercise the transport's real 401 invalidate-and-
// refresh path, which only runs when a TokenSource is configured; the
// zero-value TokenSource newTestClient leaves in place never triggers it.
type sequentialTokenSource struct {
	n atomic.Int64
}

func (s *sequentialTokenSource) Token(context.Context) (transport.Token, error) {
	n := s.n.Add(1)
	return transport.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *sequentialTokenSource) Invalidate(string) {}

// newOnceTestClient builds a Client whose transport has a real TokenSource,
// so a test can prove that Once keeps a write from being resent after a 401
// that would otherwise be retried with a refreshed token.
func newOnceTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &sequentialTokenSource{}})
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Region: "hcm-3", Portal: server.URL + "/", VLB: server.URL + "/"}, tc)
	return New(cfg)
}

// TestLoadBalancerRejectsBadPathIDs checks that every read operation with an
// ID in its request path refuses "..", ".", "/", and "?" before sending any
// request, the same guard GetCertificate already has. routes.URL escapes "/"
// in a path segment but not "..", so this is what stops a caller-supplied ID
// from reaching a different path than the one it named.
func TestLoadBalancerRejectsBadPathIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))

	cases := []struct {
		name string
		call func(bad string) error
	}{
		{"GetLoadBalancer.LoadBalancerID", func(bad string) error {
			_, err := c.GetLoadBalancer(context.Background(), &GetLoadBalancerInput{LoadBalancerID: bad})
			return err
		}},
		{"ListListeners.LoadBalancerID", func(bad string) error {
			_, err := c.ListListeners(context.Background(), &ListListenersInput{LoadBalancerID: bad})
			return err
		}},
		{"GetListener.LoadBalancerID", func(bad string) error {
			_, err := c.GetListener(context.Background(), &GetListenerInput{LoadBalancerID: bad, ListenerID: "listener-1"})
			return err
		}},
		{"GetListener.ListenerID", func(bad string) error {
			_, err := c.GetListener(context.Background(), &GetListenerInput{LoadBalancerID: "lb-1", ListenerID: bad})
			return err
		}},
		{"ListPools.LoadBalancerID", func(bad string) error {
			_, err := c.ListPools(context.Background(), &ListPoolsInput{LoadBalancerID: bad})
			return err
		}},
		{"GetPool.LoadBalancerID", func(bad string) error {
			_, err := c.GetPool(context.Background(), &GetPoolInput{LoadBalancerID: bad, PoolID: "pool-1"})
			return err
		}},
		{"GetPool.PoolID", func(bad string) error {
			_, err := c.GetPool(context.Background(), &GetPoolInput{LoadBalancerID: "lb-1", PoolID: bad})
			return err
		}},
		{"GetPoolHealthMonitor.LoadBalancerID", func(bad string) error {
			_, err := c.GetPoolHealthMonitor(context.Background(), &GetPoolHealthMonitorInput{LoadBalancerID: bad, PoolID: "pool-1"})
			return err
		}},
		{"GetPoolHealthMonitor.PoolID", func(bad string) error {
			_, err := c.GetPoolHealthMonitor(context.Background(), &GetPoolHealthMonitorInput{LoadBalancerID: "lb-1", PoolID: bad})
			return err
		}},
		{"ListPoolMembers.LoadBalancerID", func(bad string) error {
			_, err := c.ListPoolMembers(context.Background(), &ListPoolMembersInput{LoadBalancerID: bad, PoolID: "pool-1"})
			return err
		}},
		{"ListPoolMembers.PoolID", func(bad string) error {
			_, err := c.ListPoolMembers(context.Background(), &ListPoolMembersInput{LoadBalancerID: "lb-1", PoolID: bad})
			return err
		}},
		{"ListPolicies.LoadBalancerID", func(bad string) error {
			_, err := c.ListPolicies(context.Background(), &ListPoliciesInput{LoadBalancerID: bad, ListenerID: "listener-1"})
			return err
		}},
		{"ListPolicies.ListenerID", func(bad string) error {
			_, err := c.ListPolicies(context.Background(), &ListPoliciesInput{LoadBalancerID: "lb-1", ListenerID: bad})
			return err
		}},
		{"GetPolicy.LoadBalancerID", func(bad string) error {
			_, err := c.GetPolicy(context.Background(), &GetPolicyInput{LoadBalancerID: bad, ListenerID: "listener-1", PolicyID: "policy-1"})
			return err
		}},
		{"GetPolicy.ListenerID", func(bad string) error {
			_, err := c.GetPolicy(context.Background(), &GetPolicyInput{LoadBalancerID: "lb-1", ListenerID: bad, PolicyID: "policy-1"})
			return err
		}},
		{"GetPolicy.PolicyID", func(bad string) error {
			_, err := c.GetPolicy(context.Background(), &GetPolicyInput{LoadBalancerID: "lb-1", ListenerID: "listener-1", PolicyID: bad})
			return err
		}},
		{"ListTags.LoadBalancerID", func(bad string) error {
			_, err := c.ListTags(context.Background(), &ListTagsInput{LoadBalancerID: bad})
			return err
		}},
	}

	for _, tc := range cases {
		for _, bad := range []string{"..", ".", "/", "?"} {
			t.Run(tc.name+"/"+bad, func(t *testing.T) {
				if err := tc.call(bad); !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
			})
		}
	}
}
