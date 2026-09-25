package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"sync"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Service struct {
	client *core.Client

	mu           sync.Mutex
	vnetZoneID   string
	vnetEndpoint string
}

func New(client *core.Client) *Service {
	return &Service{client: client}
}

type NetworkZone = core.NetworkZone

type NetworkListOptions struct {
	Name string
	Page int
	Size int
}

type ListVPCsOptions = NetworkListOptions
type ListWANIPsOptions = NetworkListOptions
type ListNetworkInterfacesOptions = NetworkListOptions
type ListSecurityGroupsOptions = NetworkListOptions
type ListVirtualIPAddressesOptions = NetworkListOptions
type ListRouteTablesOptions = NetworkListOptions
type ListPeeringsOptions = NetworkListOptions
type ListNetworkACLsOptions = NetworkListOptions
type ListInterconnectsOptions = NetworkListOptions

type ListEndpointsOptions struct {
	ZoneID string
	VPCID  string
	UUID   string
	Page   int
	Size   int
}

type ListVPCsResult = core.ListResult[VPC]
type ListWANIPsResult = core.ListResult[WANIP]
type ListNetworkInterfacesResult = core.ListResult[ElasticNetworkInterface]
type ListSecurityGroupsResult = core.ListResult[SecurityGroup]
type ListVirtualIPAddressesResult = core.ListResult[VirtualIPAddress]
type ListRouteTablesResult = core.ListResult[RouteTable]
type ListPeeringsResult = core.ListResult[Peering]
type ListNetworkACLsResult = core.ListResult[NetworkACL]
type ListInterconnectsResult = core.ListResult[Interconnect]
type ListEndpointsResult = core.ListResult[NetworkEndpoint]

func (s *Service) ListVNetworkRegions(ctx context.Context) ([]VNetworkRegion, error) {
	bases := []string{
		s.client.Endpoint(routes.ProductVNet),
		endpoints.VNetworkRegionalGateway(s.client.Region()),
	}
	var lastErr error
	for _, base := range uniqueNonEmptyStrings(bases) {
		regions, err := s.listVNetworkRegions(ctx, base)
		if err == nil {
			return regions, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *Service) listVNetworkRegions(ctx context.Context, baseURL string) ([]VNetworkRegion, error) {
	var resp struct {
		Data []VNetworkRegion `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListVNetworkRegions",
		Method:    "GET",
		URL: routes.URL(fixedVNetEndpoint{base: baseURL, next: s}, routes.Route{
			Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{"regions"},
		}),
		OK: []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// fixedVNetEndpoint pins the vNetwork base URL for a single request without
// mutating shared Service state.
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

func (s *Service) ListVPCs(ctx context.Context, opts *ListVPCsOptions) (*ListVPCsResult, error) {
	var resp listVPCsResponse
	if err := s.list(ctx, "Network.ListVPCs", []string{"networks"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListWANIPs(ctx context.Context, opts *ListWANIPsOptions) (*ListWANIPsResult, error) {
	var resp listWANIPsResponse
	if err := s.list(ctx, "Network.ListWANIPs", []string{"wanIps"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListNetworkInterfaces(ctx context.Context, opts *ListNetworkInterfacesOptions) (*ListNetworkInterfacesResult, error) {
	var resp listNetworkInterfacesResponse
	if err := s.list(ctx, "Network.ListNetworkInterfaces", []string{"network-interfaces-elastic"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListSecurityGroups(ctx context.Context, opts *ListSecurityGroupsOptions) (*ListSecurityGroupsResult, error) {
	var resp listSecurityGroupsResponse
	if err := s.list(ctx, "Network.ListSecurityGroups", []string{"secgroups"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetSecurityGroup(ctx context.Context, id string) (*SecurityGroup, error) {
	if id == "" {
		return nil, errors.New("vngcloud: security group id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data SecurityGroup `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.GetSecurityGroup",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "secgroups", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListServersBySecurityGroup(ctx context.Context, securityGroupID string) ([]Server, error) {
	if securityGroupID == "" {
		return nil, errors.New("vngcloud: security group id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []Server `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListServersBySecurityGroup",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "secgroups", securityGroupID, "servers"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (s *Service) ListVirtualIPAddresses(ctx context.Context, opts *ListVirtualIPAddressesOptions) (*ListVirtualIPAddressesResult, error) {
	var resp listVirtualIPAddressesResponse
	if err := s.list(ctx, "Network.ListVirtualIPAddresses", []string{"virtualIpAddress"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListRouteTables(ctx context.Context, opts *ListRouteTablesOptions) (*ListRouteTablesResult, error) {
	var resp listRouteTablesResponse
	if err := s.list(ctx, "Network.ListRouteTables", []string{"route-table"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListPeerings(ctx context.Context, opts *ListPeeringsOptions) (*ListPeeringsResult, error) {
	var resp listPeeringsResponse
	if err := s.list(ctx, "Network.ListPeerings", []string{"peering"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListNetworkACLs(ctx context.Context, opts *ListNetworkACLsOptions) (*ListNetworkACLsResult, error) {
	var resp listNetworkACLsResponse
	if err := s.list(ctx, "Network.ListNetworkACLs", []string{"network-acl", "list"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListSubnets(ctx context.Context) ([]Subnet, error) {
	vpcs, err := s.ListVPCs(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]Subnet, 0)
	for _, vpc := range vpcs.Items {
		subnets, err := s.ListSubnetsByVPC(ctx, vpc.UUID)
		if err != nil {
			return nil, err
		}
		items = append(items, subnets...)
	}
	return items, nil
}

func (s *Service) ListSubnetsByVPC(ctx context.Context, vpcID string) ([]Subnet, error) {
	if vpcID == "" {
		return nil, errors.New("vngcloud: vpc id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp []subnetResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListSubnetsByVPC",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "networks", vpcID, "subnets"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	items := make([]Subnet, 0, len(resp))
	for _, item := range resp {
		items = append(items, item.toSubnet())
	}
	return items, nil
}

func (s *Service) GetVPC(ctx context.Context, id string) (*VPC, error) {
	if id == "" {
		return nil, errors.New("vngcloud: vpc id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data VPC `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.GetVPC",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "networks", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) GetSubnet(ctx context.Context, vpcID, subnetID string) (*Subnet, error) {
	if vpcID == "" {
		return nil, errors.New("vngcloud: vpc id is required")
	}
	if subnetID == "" {
		return nil, errors.New("vngcloud: subnet id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data subnetResponse `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.GetSubnet",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "networks", vpcID, "subnets", subnetID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	subnet := resp.Data.toSubnet()
	return &subnet, nil
}

func (s *Service) ListSecurityGroupRules(ctx context.Context, securityGroupID string) ([]SecurityGroupRule, error) {
	if securityGroupID == "" {
		return nil, errors.New("vngcloud: security group id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []SecurityGroupRule `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListSecurityGroupRules",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "secgroups", securityGroupID, "secGroupRules"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].SecurityGroupID = securityGroupID
	}
	return resp.Data, nil
}

func (s *Service) ListAllSecurityGroupRules(ctx context.Context) ([]SecurityGroupRule, error) {
	secgroups, err := s.ListSecurityGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]SecurityGroupRule, 0)
	for _, secgroup := range secgroups.Items {
		rules, err := s.ListSecurityGroupRules(ctx, secgroup.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, rules...)
	}
	return items, nil
}

func (s *Service) ListRouteTableRoutes(ctx context.Context) ([]RouteTableRoute, error) {
	tables, err := s.ListRouteTables(ctx, nil)
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
	return items, nil
}

func (s *Service) GetVirtualIPAddress(ctx context.Context, id string) (*VirtualIPAddress, error) {
	if id == "" {
		return nil, errors.New("vngcloud: virtual ip address id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data VirtualIPAddress `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.GetVirtualIPAddress",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "virtualIpAddress", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListAddressPairsByVirtualIPAddress(ctx context.Context, virtualIPAddressID string) ([]AddressPair, error) {
	if virtualIPAddressID == "" {
		return nil, errors.New("vngcloud: virtual ip address id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []AddressPair `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListAddressPairsByVirtualIPAddress",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "virtualIpAddress", virtualIPAddressID, "addressPairs"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].VirtualIPAddressID = virtualIPAddressID
	}
	return resp.Data, nil
}

func (s *Service) ListAddressPairsByVirtualSubnet(ctx context.Context, virtualSubnetID string) ([]AddressPair, error) {
	if virtualSubnetID == "" {
		return nil, errors.New("vngcloud: virtual subnet id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []AddressPair `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListAddressPairsByVirtualSubnet",
		Method:    "GET",
		URL:       s.networkURL([]string{projectID, "virtual-subnets", virtualSubnetID, "addressPairs"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		resp.Data[i].VirtualSubnetID = virtualSubnetID
	}
	return resp.Data, nil
}

func (s *Service) ListAllVirtualIPAddressAddressPairs(ctx context.Context) ([]AddressPair, error) {
	virtualIPs, err := s.ListVirtualIPAddresses(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]AddressPair, 0)
	for _, virtualIP := range virtualIPs.Items {
		pairs, err := s.ListAddressPairsByVirtualIPAddress(ctx, virtualIP.UUID)
		if err != nil {
			return nil, err
		}
		items = append(items, pairs...)
	}
	return items, nil
}

func (s *Service) ListInterconnects(ctx context.Context, opts *ListInterconnectsOptions) (*ListInterconnectsResult, error) {
	var resp listInterconnectsResponse
	if err := s.list(ctx, "Network.ListInterconnects", []string{"interconnects"}, opts, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListEndpoints(ctx context.Context, opts *ListEndpointsOptions) (*ListEndpointsResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	zoneID := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	q := url.Values{}
	if opts != nil {
		if opts.ZoneID != "" {
			zoneID = opts.ZoneID
		}
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	if zoneID == "" {
		zoneID = s.requireVNetworkZoneID(ctx)
	}
	params := map[string]any{"page": page, "size": size}
	search := make([]map[string]string, 0, 2)
	if opts != nil && opts.VPCID != "" {
		search = append(search, map[string]string{"field": "vpcId", "value": opts.VPCID})
	}
	if opts != nil && opts.UUID != "" {
		search = append(search, map[string]string{"field": "uuid", "value": opts.UUID})
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
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListEndpoints",
		Method:    "GET",
		URL:       s.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{zoneID, projectID, "endpoints"}, Query: q}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.Data, resp.Page, resp.Size, resp.TotalPage, resp.Total), nil
}

func (s *Service) GetEndpoint(ctx context.Context, id string) (*NetworkEndpointDetail, error) {
	if id == "" {
		return nil, errors.New("vngcloud: endpoint id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	zoneID := s.requireVNetworkZoneID(ctx)
	var resp struct {
		Data NetworkEndpointDetail `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.GetEndpoint",
		Method:    "GET",
		URL:       s.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{zoneID, projectID, "endpoints", id}}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) ListEndpointTags(ctx context.Context, endpointID string) ([]Tag, error) {
	if endpointID == "" {
		return nil, errors.New("vngcloud: endpoint id is required")
	}
	project, err := s.client.RequireProject(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("resourceUuid", endpointID)
	headers := map[string]string{}
	if project.UserID > 0 {
		headers["portal-user-id"] = strconv.Itoa(project.UserID)
	}
	var resp struct {
		Data []Tag `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Network.ListEndpointTags",
		Method:    "GET",
		URL:       s.routeURL(routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{project.ID, "tags"}, Query: q}),
		Headers:   headers,
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (s *Service) list(ctx context.Context, operation string, parts []string, opts *NetworkListOptions, out any) error {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	routeParts := append([]string{projectID}, parts...)
	return s.client.DoJSON(ctx, transport.Request{
		Operation: operation,
		Method:    "GET",
		URL:       s.networkURL(routeParts, networkListQuery(opts)),
		OK:        []int{200},
	}, out)
}

func (s *Service) networkURL(parts []string, q url.Values) string {
	return s.routeURL(routes.Route{
		Product: routes.ProductVServer,
		Version: "v2",
		Parts:   parts,
		Query:   q,
	})
}

func (s *Service) routeURL(route routes.Route) string {
	return routes.URL(s, route)
}

func (s *Service) Endpoint(product routes.Product) string {
	if product == routes.ProductVNet {
		s.mu.Lock()
		endpoint := s.vnetEndpoint
		s.mu.Unlock()
		if endpoint != "" {
			return endpoint
		}
	}
	return s.client.Endpoint(product)
}

func (s *Service) requireVNetworkZoneID(ctx context.Context) string {
	s.mu.Lock()
	if s.vnetZoneID != "" {
		id := s.vnetZoneID
		s.mu.Unlock()
		return id
	}
	s.mu.Unlock()

	regions, err := s.ListVNetworkRegions(ctx)
	if err != nil {
		return s.client.Region()
	}
	for _, region := range regions {
		if region.matches(s.client.Region()) {
			s.mu.Lock()
			s.vnetZoneID = region.UUID
			if endpoint := region.endpoint(); endpoint != "" {
				s.vnetEndpoint = endpoint
			}
			id := s.vnetZoneID
			s.mu.Unlock()
			return id
		}
	}
	return s.client.Region()
}

func networkListQuery(opts *NetworkListOptions) url.Values {
	name := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	if opts != nil {
		name = opts.Name
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}
