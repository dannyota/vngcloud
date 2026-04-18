package network

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestNetworkListVPCs(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/networks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "prod" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/network/list_vpcs.json")
	}))

	vpcs, err := service.ListVPCs(context.Background(), &ListVPCsOptions{Name: "prod", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListVPCs() error = %v", err)
	}
	if len(vpcs.Items) != 1 || vpcs.Items[0].UUID != "vpc-1" {
		t.Fatalf("unexpected vpcs: %+v", vpcs)
	}
}

func TestNetworkListDefaultPageSize(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := service.ListSecurityGroups(context.Background(), nil); err != nil {
		t.Fatalf("ListSecurityGroups() error = %v", err)
	}
}

func TestNetworkListVNetworkRegions(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vnetwork/v1/regions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/network/list_vnetwork_regions.json")
	}))

	regions, err := service.ListVNetworkRegions(context.Background())
	if err != nil {
		t.Fatalf("ListVNetworkRegions() error = %v", err)
	}
	if len(regions) != 1 || regions[0].UUID != "zone-a" || !regions[0].matches("hcm-3") {
		t.Fatalf("unexpected regions: %+v", regions)
	}
}

func TestNetworkTopLevelRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Service) error
	}{
		{
			name: "wan ips",
			path: "/v2/project-1/wanIps",
			call: func(s *Service) error {
				_, err := s.ListWANIPs(context.Background(), nil)
				return err
			},
		},
		{
			name: "interfaces",
			path: "/v2/project-1/network-interfaces-elastic",
			call: func(s *Service) error {
				_, err := s.ListNetworkInterfaces(context.Background(), nil)
				return err
			},
		},
		{
			name: "security groups",
			path: "/v2/project-1/secgroups",
			call: func(s *Service) error {
				_, err := s.ListSecurityGroups(context.Background(), nil)
				return err
			},
		},
		{
			name: "virtual ips",
			path: "/v2/project-1/virtualIpAddress",
			call: func(s *Service) error {
				_, err := s.ListVirtualIPAddresses(context.Background(), nil)
				return err
			},
		},
		{
			name: "route tables",
			path: "/v2/project-1/route-table",
			call: func(s *Service) error {
				_, err := s.ListRouteTables(context.Background(), nil)
				return err
			},
		},
		{
			name: "peerings",
			path: "/v2/project-1/peering",
			call: func(s *Service) error {
				_, err := s.ListPeerings(context.Background(), nil)
				return err
			},
		},
		{
			name: "network acls",
			path: "/v2/project-1/network-acl/list",
			call: func(s *Service) error {
				_, err := s.ListNetworkACLs(context.Background(), nil)
				return err
			},
		},
		{
			name: "interconnects",
			path: "/v2/project-1/interconnects",
			call: func(s *Service) error {
				_, err := s.ListInterconnects(context.Background(), nil)
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
				_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10,"totalPage":0,"totalItem":0}`))
			}))
			if err := tt.call(service); err != nil {
				t.Fatalf("call error = %v", err)
			}
		})
	}
}

func TestNetworkListSubnetsByVPC(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/networks/vpc-1/subnets" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/network/list_subnets_by_vpc.json")
	}))

	subnets, err := service.ListSubnetsByVPC(context.Background(), "vpc-1")
	if err != nil {
		t.Fatalf("ListSubnetsByVPC() error = %v", err)
	}
	if len(subnets) != 1 || subnets[0].UUID != "subnet-1" || subnets[0].ZoneID != "zone-a" {
		t.Fatalf("unexpected subnets: %+v", subnets)
	}
}

func TestNetworkListSecurityGroupRules(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/secgroups/secgroup-1/secGroupRules" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/network/list_security_group_rules.json")
	}))

	rules, err := service.ListSecurityGroupRules(context.Background(), "secgroup-1")
	if err != nil {
		t.Fatalf("ListSecurityGroupRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].RuleID != "rule-1" || rules[0].SecurityGroupID != "secgroup-1" {
		t.Fatalf("unexpected rules: %+v", rules)
	}
}

func TestNetworkSecurityGroupHelpers(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Service) error
	}{
		{
			name: "get security group",
			path: "/v2/project-1/secgroups/secgroup-1",
			body: testutil.FixtureBody(t, "../../testdata/network/get_security_group.json"),
			call: func(s *Service) error {
				securityGroup, err := s.GetSecurityGroup(context.Background(), "secgroup-1")
				if err == nil && securityGroup.ID != "secgroup-1" {
					t.Fatalf("unexpected security group: %+v", securityGroup)
				}
				return err
			},
		},
		{
			name: "list servers by security group",
			path: "/v2/project-1/secgroups/secgroup-1/servers",
			body: testutil.FixtureBody(t, "../../testdata/network/list_servers_by_security_group.json"),
			call: func(s *Service) error {
				servers, err := s.ListServersBySecurityGroup(context.Background(), "secgroup-1")
				if err == nil && (len(servers) != 1 || servers[0].UUID != "server-1") {
					t.Fatalf("unexpected servers: %+v", servers)
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

func TestNetworkListEndpoints(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		testutil.WriteFixture(t, w, "../../testdata/network/list_endpoints.json")
	}))

	endpoints, err := service.ListEndpoints(context.Background(), &ListEndpointsOptions{ZoneID: "zone-a", VPCID: "vpc-1", UUID: "endpoint-1", Page: 2, Size: 25})
	if err != nil {
		t.Fatalf("ListEndpoints() error = %v", err)
	}
	if len(endpoints.Items) != 1 || endpoints.Items[0].UUID != "endpoint-1" || endpoints.Page.PageSize != 25 {
		t.Fatalf("unexpected endpoints: %+v", endpoints)
	}
}

func TestNetworkGetEndpoint(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vnetwork/v1/regions":
			_, _ = w.Write([]byte(`{"data":[{"uuid":"zone-a","name":"hcm-3","vnetworkDashboard":"` + "http://" + r.Host + `" }]}`))
		case "/vnetwork-gateway/vnetwork/v1/zone-a/project-1/endpoints/endpoint-1":
			testutil.WriteFixture(t, w, "../../testdata/network/get_endpoint.json")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	endpoint, err := service.GetEndpoint(context.Background(), "endpoint-1")
	if err != nil {
		t.Fatalf("GetEndpoint() error = %v", err)
	}
	if endpoint.UUID != "endpoint-1" || endpoint.EndpointName != "<name>" {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
}

func TestNetworkListEndpointTags(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			testutil.WriteFixture(t, w, "../../testdata/network/list_endpoint_tags.json")
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))

	tags, err := service.ListEndpointTags(context.Background(), "endpoint-1")
	if err != nil {
		t.Fatalf("ListEndpointTags() error = %v", err)
	}
	if len(tags) != 1 || tags[0].UUID != "tag-1" || tags[0].Key != "env" {
		t.Fatalf("unexpected tags: %+v", tags)
	}
}

func TestNetworkDetailRoutes(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*Service) error
	}{
		{
			name: "get vpc",
			path: "/v2/project-1/networks/vpc-1",
			body: testutil.FixtureBody(t, "../../testdata/network/get_vpc.json"),
			call: func(s *Service) error {
				vpc, err := s.GetVPC(context.Background(), "vpc-1")
				if err == nil && vpc.UUID != "vpc-1" {
					t.Fatalf("unexpected vpc: %+v", vpc)
				}
				return err
			},
		},
		{
			name: "get subnet",
			path: "/v2/project-1/networks/vpc-1/subnets/subnet-1",
			body: testutil.FixtureBody(t, "../../testdata/network/get_subnet.json"),
			call: func(s *Service) error {
				subnet, err := s.GetSubnet(context.Background(), "vpc-1", "subnet-1")
				if err == nil && subnet.UUID != "subnet-1" {
					t.Fatalf("unexpected subnet: %+v", subnet)
				}
				return err
			},
		},
		{
			name: "get virtual ip",
			path: "/v2/project-1/virtualIpAddress/vip-1",
			body: testutil.FixtureBody(t, "../../testdata/network/get_virtual_ip_address.json"),
			call: func(s *Service) error {
				vip, err := s.GetVirtualIPAddress(context.Background(), "vip-1")
				if err == nil && vip.UUID != "vip-1" {
					t.Fatalf("unexpected virtual ip: %+v", vip)
				}
				return err
			},
		},
		{
			name: "list virtual ip address pairs",
			path: "/v2/project-1/virtualIpAddress/vip-1/addressPairs",
			body: testutil.FixtureBody(t, "../../testdata/network/list_address_pairs_by_virtual_ip_address.json"),
			call: func(s *Service) error {
				pairs, err := s.ListAddressPairsByVirtualIPAddress(context.Background(), "vip-1")
				if err == nil && (len(pairs) != 1 || pairs[0].VirtualIPAddressID != "vip-1") {
					t.Fatalf("unexpected address pairs: %+v", pairs)
				}
				return err
			},
		},
		{
			name: "list virtual subnet address pairs",
			path: "/v2/project-1/virtual-subnets/subnet-1/addressPairs",
			body: testutil.FixtureBody(t, "../../testdata/network/list_virtual_subnet_address_pairs.json"),
			call: func(s *Service) error {
				pairs, err := s.ListAddressPairsByVirtualSubnet(context.Background(), "subnet-1")
				if err == nil && (len(pairs) != 1 || pairs[0].VirtualSubnetID != "subnet-1") {
					t.Fatalf("unexpected address pairs: %+v", pairs)
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
