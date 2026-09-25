package globalloadbalancer

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestGLBListPackages(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/packages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/glb/list_packages.json")
	}))

	out, err := c.ListPackages(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListPackages() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "pkg-1" || !out.Items[0].Enabled {
		t.Fatalf("unexpected packages: %+v", out)
	}
}

func TestGLBListRegions(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/regions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/glb/list_regions.json")
	}))

	out, err := c.ListRegions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListRegions() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "region-1" {
		t.Fatalf("unexpected regions: %+v", out)
	}
}

func TestGLBNestedRoutes(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		body  string
		call  func(*Client) error
	}{
		{
			name:  "list load balancers",
			path:  "/v1/global-load-balancers",
			query: "limit=10000&name=&offset=0",
			body:  testutil.FixtureBody(t, "../testdata/glb/list_load_balancers.json"),
			call: func(c *Client) error {
				out, err := c.ListLoadBalancers(context.Background(), nil)
				if err == nil && (len(out.Items) != 1 || out.Items[0].ID != "glb-1") {
					t.Fatalf("unexpected glbs: %+v", out)
				}
				return err
			},
		},
		{
			name: "get load balancer",
			path: "/v1/global-load-balancers/glb-1",
			body: testutil.FixtureBody(t, "../testdata/glb/get_load_balancer.json"),
			call: func(c *Client) error {
				out, err := c.GetLoadBalancer(context.Background(), &GetLoadBalancerInput{LoadBalancerID: "glb-1"})
				if err == nil && out.LoadBalancer.ID != "glb-1" {
					t.Fatalf("unexpected glb: %+v", out.LoadBalancer)
				}
				return err
			},
		},
		{
			name: "pools",
			path: "/v1/global-load-balancers/glb-1/global-pools",
			body: testutil.FixtureBody(t, "../testdata/glb/list_pools.json"),
			call: func(c *Client) error {
				out, err := c.ListPools(context.Background(), &ListPoolsInput{LoadBalancerID: "glb-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].ID != "pool-1") {
					t.Fatalf("unexpected pools: %+v", out)
				}
				return err
			},
		},
		{
			name: "listeners",
			path: "/v1/global-load-balancers/glb-1/global-listeners",
			body: testutil.FixtureBody(t, "../testdata/glb/list_listeners.json"),
			call: func(c *Client) error {
				out, err := c.ListListeners(context.Background(), &ListListenersInput{LoadBalancerID: "glb-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].ID != "listener-1") {
					t.Fatalf("unexpected listeners: %+v", out)
				}
				return err
			},
		},
		{
			name: "get listener",
			path: "/v1/global-load-balancers/glb-1/global-listeners/listener-1",
			body: testutil.FixtureBody(t, "../testdata/glb/get_listener.json"),
			call: func(c *Client) error {
				out, err := c.GetListener(context.Background(), &GetListenerInput{LoadBalancerID: "glb-1", ListenerID: "listener-1"})
				if err == nil && out.Listener.ID != "listener-1" {
					t.Fatalf("unexpected listener: %+v", out.Listener)
				}
				return err
			},
		},
		{
			name: "pool members",
			path: "/v1/global-load-balancers/glb-1/global-pools/pool-1/pool-members",
			body: testutil.FixtureBody(t, "../testdata/glb/list_pool_members.json"),
			call: func(c *Client) error {
				out, err := c.ListPoolMembers(context.Background(), &ListPoolMembersInput{LoadBalancerID: "glb-1", PoolID: "pool-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].ID != "member-1") {
					t.Fatalf("unexpected members: %+v", out)
				}
				return err
			},
		},
		{
			name: "get pool member",
			path: "/v1/global-load-balancers/glb-1/global-pools/pool-1/pool-members/member-1",
			body: testutil.FixtureBody(t, "../testdata/glb/get_pool_member.json"),
			call: func(c *Client) error {
				out, err := c.GetPoolMember(context.Background(), &GetPoolMemberInput{LoadBalancerID: "glb-1", PoolID: "pool-1", PoolMemberID: "member-1"})
				if err == nil && out.PoolMember.ID != "member-1" {
					t.Fatalf("unexpected member: %+v", out.PoolMember)
				}
				return err
			},
		},
		{
			name: "usage histories",
			path: "/v1/global-load-balancers/glb-1/usage-histories",
			body: testutil.FixtureBody(t, "../testdata/glb/list_usage_histories.json"),
			call: func(c *Client) error {
				out, err := c.ListUsageHistories(context.Background(), &ListUsageHistoriesInput{LoadBalancerID: "glb-1"})
				if err == nil && len(out.Items) != 1 {
					t.Fatalf("unexpected histories: %+v", out)
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
				if tt.query != "" && r.URL.RawQuery != tt.query {
					t.Fatalf("unexpected query: %s", r.URL.RawQuery)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			if err := tt.call(c); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func TestGLBRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetPoolMember(context.Background(), &GetPoolMemberInput{LoadBalancerID: "glb-1", PoolID: "pool-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetPoolMember(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestGLBZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})
	if _, err := c.ListPackages(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListPackages() err = %v, want ErrInvalidConfig", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
