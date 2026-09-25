package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/testutil"
)

// TestZoneAliasMatchesComputeZone checks that network.Zone and compute.Zone
// name the same underlying type, so either package's alias decodes and
// assigns interchangeably with compute.Server.Zone.
func TestZoneAliasMatchesComputeZone(t *testing.T) {
	acceptsComputeZone := func(compute.Zone) {}
	var z Zone
	acceptsComputeZone(z)
}

func TestNetworkListVPCs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/networks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "prod" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_vpcs.json")
	}))

	out, err := c.ListVPCs(context.Background(), &ListVPCsInput{Name: "prod", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListVPCs() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "vpc-1" {
		t.Fatalf("unexpected vpcs: %+v", out)
	}
}

func TestNetworkListDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := c.ListSecurityGroups(context.Background(), nil); err != nil {
		t.Fatalf("ListSecurityGroups() error = %v", err)
	}
}

func TestNetworkListVNetworkRegions(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vnetwork/v1/regions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_vnetwork_regions.json")
	}))

	out, err := c.ListVNetworkRegions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListVNetworkRegions() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "zone-a" || !out.Items[0].matches("hcm-3") {
		t.Fatalf("unexpected regions: %+v", out)
	}
}

func TestNetworkTopLevelRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) error
	}{
		{
			name: "wan ips",
			path: "/v2/project-1/wanIps",
			call: func(c *Client) error {
				_, err := c.ListWANIPs(context.Background(), nil)
				return err
			},
		},
		{
			name: "interfaces",
			path: "/v2/project-1/network-interfaces-elastic",
			call: func(c *Client) error {
				_, err := c.ListNetworkInterfaces(context.Background(), nil)
				return err
			},
		},
		{
			name: "security groups",
			path: "/v2/project-1/secgroups",
			call: func(c *Client) error {
				_, err := c.ListSecurityGroups(context.Background(), nil)
				return err
			},
		},
		{
			name: "virtual ips",
			path: "/v2/project-1/virtualIpAddress",
			call: func(c *Client) error {
				_, err := c.ListVirtualIPAddresses(context.Background(), nil)
				return err
			},
		},
		{
			name: "route tables",
			path: "/v2/project-1/route-table",
			call: func(c *Client) error {
				_, err := c.ListRouteTables(context.Background(), nil)
				return err
			},
		},
		{
			name: "peerings",
			path: "/v2/project-1/peering",
			call: func(c *Client) error {
				_, err := c.ListPeerings(context.Background(), nil)
				return err
			},
		},
		{
			name: "network acls",
			path: "/v2/project-1/network-acl/list",
			call: func(c *Client) error {
				_, err := c.ListNetworkACLs(context.Background(), nil)
				return err
			},
		},
		{
			name: "interconnects",
			path: "/v2/project-1/interconnects",
			call: func(c *Client) error {
				_, err := c.ListInterconnects(context.Background(), nil)
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
				_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10,"totalPage":0,"totalItem":0}`))
			}))
			if err := tt.call(c); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func TestNetworkListSubnetsByVPC(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/networks/vpc-1/subnets" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_subnets_by_vpc.json")
	}))

	out, err := c.ListSubnetsByVPC(context.Background(), &ListSubnetsByVPCInput{VPCID: "vpc-1"})
	if err != nil {
		t.Fatalf("ListSubnetsByVPC() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "subnet-1" || out.Items[0].ZoneID != "zone-a" {
		t.Fatalf("unexpected subnets: %+v", out)
	}
}

func TestNetworkListSecurityGroupRules(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/secgroups/secgroup-1/secGroupRules" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_security_group_rules.json")
	}))

	out, err := c.ListSecurityGroupRules(context.Background(), &ListSecurityGroupRulesInput{SecurityGroupID: "secgroup-1"})
	if err != nil {
		t.Fatalf("ListSecurityGroupRules() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].RuleID != "rule-1" || out.Items[0].SecurityGroupID != "secgroup-1" {
		t.Fatalf("unexpected rules: %+v", out)
	}
}

func TestNetworkSecurityGroupHelpers(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "get security group",
			path: "/v2/project-1/secgroups/secgroup-1",
			body: testutil.FixtureBody(t, "../testdata/network/get_security_group.json"),
			call: func(c *Client) error {
				out, err := c.GetSecurityGroup(context.Background(), &GetSecurityGroupInput{SecurityGroupID: "secgroup-1"})
				if err == nil && out.SecurityGroup.ID != "secgroup-1" {
					t.Fatalf("unexpected security group: %+v", out.SecurityGroup)
				}
				return err
			},
		},
		{
			name: "list servers by security group",
			path: "/v2/project-1/secgroups/secgroup-1/servers",
			body: testutil.FixtureBody(t, "../testdata/network/list_servers_by_security_group.json"),
			call: func(c *Client) error {
				out, err := c.ListServersBySecurityGroup(context.Background(), &ListServersBySecurityGroupInput{SecurityGroupID: "secgroup-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].UUID != "server-1") {
					t.Fatalf("unexpected servers: %+v", out)
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

func TestNetworkListEndpoints(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vnetwork/v1/zone-a/project-1/endpoints" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var params struct {
			Page   int `json:"page"`
			Size   int `json:"size"`
			Search []struct {
				Field string `json:"field"`
				Value string `json:"value"`
			} `json:"search"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("params")), &params); err != nil {
			t.Fatalf("invalid params query: %v", err)
		}
		if params.Page != 2 || params.Size != 25 || len(params.Search) != 2 {
			t.Fatalf("unexpected params: %+v", params)
		}
		testutil.WriteFixture(t, w, "../testdata/network/list_endpoints.json")
	}))

	out, err := c.ListEndpoints(context.Background(), &ListEndpointsInput{ZoneID: "zone-a", VPCID: "vpc-1", UUID: "endpoint-1", Page: 2, Size: 25})
	if err != nil {
		t.Fatalf("ListEndpoints() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "endpoint-1" || out.PageSize != 25 {
		t.Fatalf("unexpected endpoints: %+v", out)
	}
}

func TestNetworkGetEndpoint(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vnetwork/v1/regions":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"uuid": "zone-a", "name": "hcm-3", "vnetworkDashboard": "http://" + r.Host},
			}})
		case "/vnetwork-gateway/vnetwork/v1/zone-a/project-1/endpoints/endpoint-1":
			testutil.WriteFixture(t, w, "../testdata/network/get_endpoint.json")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	out, err := c.GetEndpoint(context.Background(), &GetEndpointInput{EndpointID: "endpoint-1"})
	if err != nil {
		t.Fatalf("GetEndpoint() error = %v", err)
	}
	if out.Endpoint.UUID != "endpoint-1" || out.Endpoint.EndpointName != "<name>" {
		t.Fatalf("unexpected endpoint: %+v", out.Endpoint)
	}
}

func TestNetworkListEndpointTags(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/projects":
			_, _ = w.Write([]byte(`{"projects":[{"projectId":"project-1","region":"hcm-3","userId":123}]}`))
		case "/vnetwork/v1/project-1/tags":
			if r.URL.Query().Get("resourceUuid") != "endpoint-1" {
				t.Fatalf("unexpected query: %s", r.URL.RawQuery)
			}
			if r.Header.Get("portal-user-id") != "123" {
				t.Fatalf("unexpected user header: %s", r.Header.Get("portal-user-id"))
			}
			testutil.WriteFixture(t, w, "../testdata/network/list_endpoint_tags.json")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	out, err := c.ListEndpointTags(context.Background(), &ListEndpointTagsInput{EndpointID: "endpoint-1"})
	if err != nil {
		t.Fatalf("ListEndpointTags() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "tag-1" || out.Items[0].Key != "env" {
		t.Fatalf("unexpected tags: %+v", out)
	}
}

func TestNetworkDetailRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Client) error
	}{
		{
			name: "get vpc",
			path: "/v2/project-1/networks/vpc-1",
			body: testutil.FixtureBody(t, "../testdata/network/get_vpc.json"),
			call: func(c *Client) error {
				out, err := c.GetVPC(context.Background(), &GetVPCInput{VPCID: "vpc-1"})
				if err == nil && out.VPC.UUID != "vpc-1" {
					t.Fatalf("unexpected vpc: %+v", out.VPC)
				}
				return err
			},
		},
		{
			name: "get subnet",
			path: "/v2/project-1/networks/vpc-1/subnets/subnet-1",
			body: testutil.FixtureBody(t, "../testdata/network/get_subnet.json"),
			call: func(c *Client) error {
				out, err := c.GetSubnet(context.Background(), &GetSubnetInput{VPCID: "vpc-1", SubnetID: "subnet-1"})
				if err == nil && out.Subnet.UUID != "subnet-1" {
					t.Fatalf("unexpected subnet: %+v", out.Subnet)
				}
				return err
			},
		},
		{
			name: "get virtual ip",
			path: "/v2/project-1/virtualIpAddress/vip-1",
			body: testutil.FixtureBody(t, "../testdata/network/get_virtual_ip_address.json"),
			call: func(c *Client) error {
				out, err := c.GetVirtualIPAddress(context.Background(), &GetVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
				if err == nil && out.VirtualIPAddress.UUID != "vip-1" {
					t.Fatalf("unexpected virtual ip: %+v", out.VirtualIPAddress)
				}
				return err
			},
		},
		{
			name: "list virtual ip address pairs",
			path: "/v2/project-1/virtualIpAddress/vip-1/addressPairs",
			body: testutil.FixtureBody(t, "../testdata/network/list_address_pairs_by_virtual_ip_address.json"),
			call: func(c *Client) error {
				out, err := c.ListAddressPairsByVirtualIPAddress(context.Background(), &ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: "vip-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].VirtualIPAddressID != "vip-1") {
					t.Fatalf("unexpected address pairs: %+v", out)
				}
				return err
			},
		},
		{
			name: "list virtual subnet address pairs",
			path: "/v2/project-1/virtual-subnets/subnet-1/addressPairs",
			body: testutil.FixtureBody(t, "../testdata/network/list_virtual_subnet_address_pairs.json"),
			call: func(c *Client) error {
				out, err := c.ListAddressPairsByVirtualSubnet(context.Background(), &ListAddressPairsByVirtualSubnetInput{VirtualSubnetID: "subnet-1"})
				if err == nil && (len(out.Items) != 1 || out.Items[0].VirtualSubnetID != "subnet-1") {
					t.Fatalf("unexpected address pairs: %+v", out)
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

func TestNetworkRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetSubnet(context.Background(), &GetSubnetInput{VPCID: "vpc-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || err.Error() == "" {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetSubnet(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func TestNetworkZeroConfig(t *testing.T) {
	c := New(vngcloud.Config{})

	if _, err := c.ListVNetworkRegions(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListVNetworkRegions() err = %v, want ErrInvalidConfig", err)
	}
	if _, err := c.ListVPCs(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("ListVPCs() err = %v, want ErrInvalidConfig", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
