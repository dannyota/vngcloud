// Package network lists and reads vNetwork VPCs, subnets, security groups,
// virtual IPs, route tables, peerings, ACLs, interconnects, and endpoints.
package network

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"sync"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the network service client.
type Client struct {
	c *core.Client

	mu           sync.Mutex
	vnetZoneID   string
	vnetEndpoint string
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

func (c *Client) ListVNetworkRegions(ctx context.Context, _ *ListVNetworkRegionsInput) (*ListVNetworkRegionsOutput, error) {
	bases := uniqueNonEmptyStrings([]string{
		c.c.Endpoint(routes.ProductVNet),
		endpoints.VNetworkRegionalGateway(c.c.Region()),
	})
	if len(bases) == 0 {
		// An invalid Config resolves no endpoint at all; try one empty base so
		// the DoJSON call below surfaces ErrInvalidConfig instead of the loop
		// silently returning (nil, nil).
		bases = []string{""}
	}
	var lastErr error
	for _, base := range bases {
		regions, err := c.listVNetworkRegions(ctx, base)
		if err == nil {
			return &ListVNetworkRegionsOutput{Items: regions}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) listVNetworkRegions(ctx context.Context, baseURL string) ([]VNetworkRegion, error) {
	var resp struct {
		Data []VNetworkRegion `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListVNetworkRegions",
		Method:    "GET",
		URL: routes.URL(fixedVNetEndpoint{base: baseURL, next: vnetEndpoints{c: c}}, routes.Route{
			Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{"regions"},
		}),
		OK: []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// fixedVNetEndpoint pins the vNetwork base URL for a single request without
// mutating shared Client state.
type fixedVNetEndpoint struct {
	base string
	next routes.Endpoints
}

func (f fixedVNetEndpoint) Endpoint(product routes.Product) string {
	if product == routes.ProductVNet {
		return f.base
	}
	return f.next.Endpoint(product)
}

// vnetEndpoints resolves the vNetwork endpoint pinned by a prior
// ListVNetworkRegions call, falling back to the shared client's default for
// every other product. It exists because routes.URL needs a routes.Endpoints
// value, and that pinning is Client-specific state that the public Client
// type does not expose as a method.
type vnetEndpoints struct{ c *Client }

func (e vnetEndpoints) Endpoint(product routes.Product) string {
	if product == routes.ProductVNet {
		e.c.mu.Lock()
		endpoint := e.c.vnetEndpoint
		e.c.mu.Unlock()
		if endpoint != "" {
			return endpoint
		}
	}
	return e.c.c.Endpoint(product)
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (c *Client) ListVPCs(ctx context.Context, in *ListVPCsInput) (*ListVPCsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listVPCsResponse
	if err := c.list(ctx, "network.ListVPCs", []string{"networks"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListWANIPs(ctx context.Context, in *ListWANIPsInput) (*ListWANIPsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listWANIPsResponse
	if err := c.list(ctx, "network.ListWANIPs", []string{"wanIps"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListNetworkInterfaces(ctx context.Context, in *ListNetworkInterfacesInput) (*ListNetworkInterfacesOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listNetworkInterfacesResponse
	if err := c.list(ctx, "network.ListNetworkInterfaces", []string{"network-interfaces-elastic"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListSecurityGroups(ctx context.Context, in *ListSecurityGroupsInput) (*ListSecurityGroupsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listSecurityGroupsResponse
	if err := c.list(ctx, "network.ListSecurityGroups", []string{"secgroups"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) GetSecurityGroup(ctx context.Context, in *GetSecurityGroupInput) (*GetSecurityGroupOutput, error) {
	if err := core.CheckRequired("network.GetSecurityGroup", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data SecurityGroup `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetSecurityGroup",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetSecurityGroupOutput{SecurityGroup: resp.Data}, nil
}

func (c *Client) ListServersBySecurityGroup(ctx context.Context, in *ListServersBySecurityGroupInput) (*ListServersBySecurityGroupOutput, error) {
	if err := core.CheckRequired("network.ListServersBySecurityGroup", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []compute.Server `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListServersBySecurityGroup",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID, "servers"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListServersBySecurityGroupOutput{Items: resp.Data}, nil
}

func (c *Client) ListVirtualIPAddresses(ctx context.Context, in *ListVirtualIPAddressesInput) (*ListVirtualIPAddressesOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listVirtualIPAddressesResponse
	if err := c.list(ctx, "network.ListVirtualIPAddresses", []string{"virtualIpAddress"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListRouteTables(ctx context.Context, in *ListRouteTablesInput) (*ListRouteTablesOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listRouteTablesResponse
	if err := c.list(ctx, "network.ListRouteTables", []string{"route-table"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListPeerings(ctx context.Context, in *ListPeeringsInput) (*ListPeeringsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listPeeringsResponse
	if err := c.list(ctx, "network.ListPeerings", []string{"peering"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListNetworkACLs(ctx context.Context, in *ListNetworkACLsInput) (*ListNetworkACLsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listNetworkACLsResponse
	if err := c.list(ctx, "network.ListNetworkACLs", []string{"network-acl", "list"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListSubnets(ctx context.Context, _ *ListSubnetsInput) (*ListSubnetsOutput, error) {
	vpcs, err := c.ListVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]Subnet, 0)
	for _, vpc := range vpcs.Items {
		subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: vpc.UUID})
		if err != nil {
			return nil, err
		}
		items = append(items, subnets.Items...)
	}
	return &ListSubnetsOutput{Items: items}, nil
}

func (c *Client) ListSubnetsByVPC(ctx context.Context, in *ListSubnetsByVPCInput) (*ListSubnetsByVPCOutput, error) {
	if err := core.CheckRequired("network.ListSubnetsByVPC", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp []subnetResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListSubnetsByVPC",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	items := make([]Subnet, 0, len(resp))
	for _, item := range resp {
		items = append(items, item.toSubnet())
	}
	return &ListSubnetsByVPCOutput{Items: items}, nil
}

func (c *Client) GetVPC(ctx context.Context, in *GetVPCInput) (*GetVPCOutput, error) {
	if err := core.CheckRequired("network.GetVPC", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	// Unlike GetSecurityGroup, this endpoint returns the VPC fields at the
	// top level, not wrapped in a "data" object.
	var resp VPC
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetVPC",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetVPCOutput{VPC: resp}, nil
}

func (c *Client) GetSubnet(ctx context.Context, in *GetSubnetInput) (*GetSubnetOutput, error) {
	if err := core.CheckRequired("network.GetSubnet", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	// Like GetVPC, and unlike GetSecurityGroup, this endpoint returns the
	// subnet fields at the top level, not wrapped in a "data" object.
	var resp subnetResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetSubnet",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "networks", in.VPCID, "subnets", in.SubnetID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetSubnetOutput{Subnet: resp.toSubnet()}, nil
}

func (c *Client) ListSecurityGroupRules(ctx context.Context, in *ListSecurityGroupRulesInput) (*ListSecurityGroupRulesOutput, error) {
	if err := core.CheckRequired("network.ListSecurityGroupRules", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []SecurityGroupRule `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListSecurityGroupRules",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID, "secGroupRules"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].SecurityGroupID = in.SecurityGroupID
	}
	return &ListSecurityGroupRulesOutput{Items: resp.Data}, nil
}

func (c *Client) ListAllSecurityGroupRules(ctx context.Context, _ *ListAllSecurityGroupRulesInput) (*ListAllSecurityGroupRulesOutput, error) {
	secgroups, err := c.ListSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]SecurityGroupRule, 0)
	for _, secgroup := range secgroups.Items {
		rules, err := c.ListSecurityGroupRules(ctx, &ListSecurityGroupRulesInput{SecurityGroupID: secgroup.ID})
		if err != nil {
			return nil, err
		}
		items = append(items, rules.Items...)
	}
	return &ListAllSecurityGroupRulesOutput{Items: items}, nil
}

func (c *Client) ListRouteTableRoutes(ctx context.Context, _ *ListRouteTableRoutesInput) (*ListRouteTableRoutesOutput, error) {
	tables, err := c.ListRouteTables(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]RouteTableRoute, 0)
	for _, table := range tables.Items {
		for _, route := range table.Routes {
			items = append(items, RouteTableRoute{
				RouteTableID:         table.UUID,
				UUID:                 route.UUID,
				RoutingType:          route.RoutingType,
				DestinationCIDRBlock: route.DestinationCIDRBlock,
				Target:               route.Target,
				Status:               route.Status,
			})
		}
	}
	return &ListRouteTableRoutesOutput{Items: items}, nil
}

func (c *Client) GetVirtualIPAddress(ctx context.Context, in *GetVirtualIPAddressInput) (*GetVirtualIPAddressOutput, error) {
	if err := core.CheckRequired("network.GetVirtualIPAddress", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data VirtualIPAddress `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetVirtualIPAddress",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "virtualIpAddress", in.VirtualIPAddressID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetVirtualIPAddressOutput{VirtualIPAddress: resp.Data}, nil
}

func (c *Client) ListAddressPairsByVirtualIPAddress(ctx context.Context, in *ListAddressPairsByVirtualIPAddressInput) (*ListAddressPairsByVirtualIPAddressOutput, error) {
	if err := core.CheckRequired("network.ListAddressPairsByVirtualIPAddress", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []AddressPair `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListAddressPairsByVirtualIPAddress",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "virtualIpAddress", in.VirtualIPAddressID, "addressPairs"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].VirtualIPAddressID = in.VirtualIPAddressID
	}
	return &ListAddressPairsByVirtualIPAddressOutput{Items: resp.Data}, nil
}

func (c *Client) ListAddressPairsByVirtualSubnet(ctx context.Context, in *ListAddressPairsByVirtualSubnetInput) (*ListAddressPairsByVirtualSubnetOutput, error) {
	if err := core.CheckRequired("network.ListAddressPairsByVirtualSubnet", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []AddressPair `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListAddressPairsByVirtualSubnet",
		Method:    "GET",
		URL:       c.networkURL([]string{projectID, "virtual-subnets", in.VirtualSubnetID, "addressPairs"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].VirtualSubnetID = in.VirtualSubnetID
	}
	return &ListAddressPairsByVirtualSubnetOutput{Items: resp.Data}, nil
}

func (c *Client) ListAllVirtualIPAddressAddressPairs(ctx context.Context, _ *ListAllVirtualIPAddressAddressPairsInput) (*ListAllVirtualIPAddressAddressPairsOutput, error) {
	virtualIPs, err := c.ListVirtualIPAddresses(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]AddressPair, 0)
	for _, virtualIP := range virtualIPs.Items {
		pairs, err := c.ListAddressPairsByVirtualIPAddress(ctx, &ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: virtualIP.UUID})
		if err != nil {
			return nil, err
		}
		items = append(items, pairs.Items...)
	}
	return &ListAllVirtualIPAddressAddressPairsOutput{Items: items}, nil
}

func (c *Client) ListInterconnects(ctx context.Context, in *ListInterconnectsInput) (*ListInterconnectsOutput, error) {
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	var resp listInterconnectsResponse
	if err := c.list(ctx, "network.ListInterconnects", []string{"interconnects"}, name, page, size, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) ListEndpoints(ctx context.Context, in *ListEndpointsInput) (*ListEndpointsOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	zoneID := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	q := url.Values{}
	if in != nil {
		if in.ZoneID != "" {
			zoneID = in.ZoneID
		}
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	if zoneID == "" {
		zoneID = c.requireVNetworkZoneID(ctx)
	}
	params := map[string]any{"page": page, "size": size}
	search := make([]map[string]string, 0, 2)
	if in != nil && in.VPCID != "" {
		search = append(search, map[string]string{"field": "vpcId", "value": in.VPCID})
	}
	if in != nil && in.UUID != "" {
		search = append(search, map[string]string{"field": "uuid", "value": in.UUID})
	}
	if len(search) > 0 {
		params["search"] = search
	}
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	q.Set("params", string(rawParams))
	var resp listEndpointsResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListEndpoints",
		Method:    "GET",
		URL:       c.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{zoneID, projectID, "endpoints"}, Query: q}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Data, resp.Page, resp.Size, resp.TotalPage, resp.Total), nil
}

func (c *Client) GetEndpoint(ctx context.Context, in *GetEndpointInput) (*GetEndpointOutput, error) {
	if err := core.CheckRequired("network.GetEndpoint", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	zoneID := c.requireVNetworkZoneID(ctx)
	var resp struct {
		Data EndpointDetail `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.GetEndpoint",
		Method:    "GET",
		URL:       c.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{zoneID, projectID, "endpoints", in.EndpointID}}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetEndpointOutput{Endpoint: resp.Data}, nil
}

func (c *Client) ListEndpointTags(ctx context.Context, in *ListEndpointTagsInput) (*ListEndpointTagsOutput, error) {
	if err := core.CheckRequired("network.ListEndpointTags", in); err != nil {
		return nil, err
	}
	project, err := c.c.RequireProject(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("resourceUuid", in.EndpointID)
	headers := map[string]string{}
	if project.UserID > 0 {
		headers["portal-user-id"] = strconv.Itoa(project.UserID)
	}
	var resp struct {
		Data []Tag `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "network.ListEndpointTags",
		Method:    "GET",
		URL:       c.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{project.ID, "tags"}, Query: q}),
		Headers:   headers,
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListEndpointTagsOutput{Items: resp.Data}, nil
}

func (c *Client) list(ctx context.Context, operation string, parts []string, name string, page, size int, out any) error {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	routeParts := append([]string{projectID}, parts...)
	return c.c.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       c.networkURL(routeParts, networkListQuery(name, page, size)),
		OK:        []int{200},
	}, out)
}

func (c *Client) networkURL(parts []string, q url.Values) string {
	return c.routeURL(routes.Route{
		Product: routes.ProductVServer,
		Version: "v2",
		Parts:   parts,
		Query:   q,
	})
}

func (c *Client) routeURL(route routes.Route) string {
	return routes.URL(vnetEndpoints{c: c}, route)
}

func (c *Client) requireVNetworkZoneID(ctx context.Context) string {
	c.mu.Lock()
	if c.vnetZoneID != "" {
		id := c.vnetZoneID
		c.mu.Unlock()
		return id
	}
	c.mu.Unlock()

	out, err := c.ListVNetworkRegions(ctx, nil)
	if err != nil {
		return c.c.Region()
	}
	for _, region := range out.Items {
		if region.matches(c.c.Region()) {
			c.mu.Lock()
			c.vnetZoneID = region.UUID
			if endpoint := region.endpoint(); endpoint != "" {
				c.vnetEndpoint = endpoint
			}
			id := c.vnetZoneID
			c.mu.Unlock()
			return id
		}
	}
	return c.c.Region()
}

func networkListQuery(name string, page, size int) url.Values {
	if page <= 0 {
		page = core.DefaultPage
	}
	if size <= 0 {
		size = core.DefaultPageSize
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}
