package network

import (
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
)

// ListVNetworkRegionsInput takes no fields.
type ListVNetworkRegionsInput struct{}

// ListVNetworkRegionsOutput holds VNetworkRegion items.
type ListVNetworkRegionsOutput = core.List[VNetworkRegion]

// ListVPCsInput selects parameters for ListVPCs.
type ListVPCsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListVPCsOutput holds VPC items and pagination metadata.
type ListVPCsOutput = core.PagedList[VPC]

// ListWANIPsInput selects parameters for ListWANIPs.
type ListWANIPsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListWANIPsOutput holds WANIP items and pagination metadata.
type ListWANIPsOutput = core.PagedList[WANIP]

// ListNetworkInterfacesInput selects parameters for ListNetworkInterfaces.
type ListNetworkInterfacesInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListNetworkInterfacesOutput holds ElasticNetworkInterface items and pagination metadata.
type ListNetworkInterfacesOutput = core.PagedList[ElasticNetworkInterface]

// ListSecurityGroupsInput selects parameters for ListSecurityGroups.
type ListSecurityGroupsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListSecurityGroupsOutput holds SecurityGroup items and pagination metadata.
type ListSecurityGroupsOutput = core.PagedList[SecurityGroup]

// ListVirtualIPAddressesInput selects parameters for ListVirtualIPAddresses.
type ListVirtualIPAddressesInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListVirtualIPAddressesOutput holds VirtualIPAddress items and pagination metadata.
type ListVirtualIPAddressesOutput = core.PagedList[VirtualIPAddress]

// ListRouteTablesInput selects parameters for ListRouteTables.
type ListRouteTablesInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListRouteTablesOutput holds RouteTable items and pagination metadata.
type ListRouteTablesOutput = core.PagedList[RouteTable]

// ListPeeringsInput selects parameters for ListPeerings.
type ListPeeringsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListPeeringsOutput holds Peering items and pagination metadata.
type ListPeeringsOutput = core.PagedList[Peering]

// ListNetworkACLsInput selects parameters for ListNetworkACLs.
type ListNetworkACLsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListNetworkACLsOutput holds ACL items and pagination metadata.
type ListNetworkACLsOutput = core.PagedList[ACL]

// ListInterconnectsInput selects parameters for ListInterconnects.
type ListInterconnectsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListInterconnectsOutput holds Interconnect items and pagination metadata.
type ListInterconnectsOutput = core.PagedList[Interconnect]

// GetVPCInput requires VPCID.
type GetVPCInput struct {
	// VPCID is required.
	VPCID string `vngcloud:"required"`
}

// GetVPCOutput holds the result of GetVPC.
type GetVPCOutput struct {
	VPC VPC
}

// ListDHCPOptionsInput selects parameters for ListDHCPOptions.
type ListDHCPOptionsInput struct {
	// Name is sent as the name query parameter.
	Name string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListDHCPOptionsOutput holds DHCPOptions items and pagination metadata.
type ListDHCPOptionsOutput = core.PagedList[DHCPOptions]

// GetDHCPOptionsInput requires DHCPOptionsID.
type GetDHCPOptionsInput struct {
	// DHCPOptionsID is required.
	DHCPOptionsID string `vngcloud:"required"`
}

// GetDHCPOptionsOutput holds the result of GetDHCPOptions.
type GetDHCPOptionsOutput struct {
	DHCPOptions DHCPOptions
}

// CreateDHCPOptionsInput creates a DHCP options set. DNSServers must hold at
// least one IPv4 address; the server enforces the four-server limit. MTU is
// sent only when set; the server's default is 1450.
type CreateDHCPOptionsInput struct {
	// Name is required.
	Name string `vngcloud:"required"`
	// DNSServers is required.
	DNSServers []string `vngcloud:"required"`

	// MTU is sent only when set; the server default is 1450.
	MTU *int
}

// CreateDHCPOptionsOutput holds the result of CreateDHCPOptions.
type CreateDHCPOptionsOutput struct {
	DHCPOptions DHCPOptions
}

// DeleteDHCPOptionsInput requires DHCPOptionsID.
type DeleteDHCPOptionsInput struct {
	// DHCPOptionsID is required.
	DHCPOptionsID string `vngcloud:"required"`
}

// DeleteDHCPOptionsOutput carries no result fields.
type DeleteDHCPOptionsOutput struct{}

// SetVPCDHCPOptionsInput moves VPCID onto DHCPOptionsID's set.
// ClearVPCDHCPOptionsInput removes a VPC's set instead of moving it to
// another one; see SetVPCDHCPOptions's doc comment for the guards the two
// calls share.
type SetVPCDHCPOptionsInput struct {
	// VPCID is required.
	VPCID string `vngcloud:"required"`
	// DHCPOptionsID is required.
	DHCPOptionsID string `vngcloud:"required"`
}

// SetVPCDHCPOptionsOutput is the VPC after the call, and whether the call
// itself changed anything. Changed is false only when the VPC's
// DHCPOptionID already equaled DHCPOptionsID.
type SetVPCDHCPOptionsOutput struct {
	VPC     VPC
	Changed bool
}

// ClearVPCDHCPOptionsInput removes VPCID's DHCP options set, returning it to
// none. See ClearVPCDHCPOptions's doc comment for the guards this refuses
// on.
type ClearVPCDHCPOptionsInput struct {
	// VPCID is required.
	VPCID string `vngcloud:"required"`
}

// ClearVPCDHCPOptionsOutput is the VPC after the call, and whether the call
// itself changed anything. Changed is false only when the VPC already had
// no DHCP options set.
type ClearVPCDHCPOptionsOutput struct {
	VPC     VPC
	Changed bool
}

// GetSubnetInput requires VPCID and SubnetID.
type GetSubnetInput struct {
	// VPCID is required.
	VPCID string `vngcloud:"required"`
	// SubnetID is required.
	SubnetID string `vngcloud:"required"`
}

// GetSubnetOutput holds the result of GetSubnet.
type GetSubnetOutput struct {
	Subnet Subnet
}

// ListSubnetsInput takes no fields.
type ListSubnetsInput struct{}

// ListSubnetsOutput holds Subnet items.
type ListSubnetsOutput = core.List[Subnet]

// ListSubnetsByVPCInput requires VPCID.
type ListSubnetsByVPCInput struct {
	// VPCID is required.
	VPCID string `vngcloud:"required"`
}

// ListSubnetsByVPCOutput holds Subnet items.
type ListSubnetsByVPCOutput = core.List[Subnet]

// GetSecurityGroupInput requires SecurityGroupID.
type GetSecurityGroupInput struct {
	// SecurityGroupID is required.
	SecurityGroupID string `vngcloud:"required"`
}

// GetSecurityGroupOutput holds the result of GetSecurityGroup.
type GetSecurityGroupOutput struct {
	SecurityGroup SecurityGroup
}

// ListServersBySecurityGroupInput requires SecurityGroupID.
type ListServersBySecurityGroupInput struct {
	// SecurityGroupID is required.
	SecurityGroupID string `vngcloud:"required"`
}

// ListServersBySecurityGroupOutput holds the result of
// ListServersBySecurityGroup.
type ListServersBySecurityGroupOutput = core.List[compute.Server]

// ListSecurityGroupRulesInput requires SecurityGroupID.
type ListSecurityGroupRulesInput struct {
	// SecurityGroupID is required.
	SecurityGroupID string `vngcloud:"required"`
}

// ListSecurityGroupRulesOutput holds SecurityGroupRule items.
type ListSecurityGroupRulesOutput = core.List[SecurityGroupRule]

// ListAllSecurityGroupRulesInput takes no fields.
type ListAllSecurityGroupRulesInput struct{}

// ListAllSecurityGroupRulesOutput holds SecurityGroupRule items.
type ListAllSecurityGroupRulesOutput = core.List[SecurityGroupRule]

// ListRouteTableRoutesInput takes no fields.
type ListRouteTableRoutesInput struct{}

// ListRouteTableRoutesOutput holds RouteTableRoute items.
type ListRouteTableRoutesOutput = core.List[RouteTableRoute]

// GetVirtualIPAddressInput requires VirtualIPAddressID.
type GetVirtualIPAddressInput struct {
	// VirtualIPAddressID is required.
	VirtualIPAddressID string `vngcloud:"required"`
}

// GetVirtualIPAddressOutput holds the result of GetVirtualIPAddress.
type GetVirtualIPAddressOutput struct {
	VirtualIPAddress VirtualIPAddress
}

// ListAddressPairsByVirtualIPAddressInput requires VirtualIPAddressID.
type ListAddressPairsByVirtualIPAddressInput struct {
	// VirtualIPAddressID is required.
	VirtualIPAddressID string `vngcloud:"required"`
}

// ListAddressPairsByVirtualIPAddressOutput holds AddressPair items.
type ListAddressPairsByVirtualIPAddressOutput = core.List[AddressPair]

// ListAddressPairsByVirtualSubnetInput requires VirtualSubnetID.
type ListAddressPairsByVirtualSubnetInput struct {
	// VirtualSubnetID is required.
	VirtualSubnetID string `vngcloud:"required"`
}

// ListAddressPairsByVirtualSubnetOutput holds AddressPair items.
type ListAddressPairsByVirtualSubnetOutput = core.List[AddressPair]

// ListAllVirtualIPAddressAddressPairsInput takes no fields.
type ListAllVirtualIPAddressAddressPairsInput struct{}

// ListAllVirtualIPAddressAddressPairsOutput holds AddressPair items.
type ListAllVirtualIPAddressAddressPairsOutput = core.List[AddressPair]

// ListEndpointsInput selects parameters for ListEndpoints.
type ListEndpointsInput struct {
	// ZoneID selects the zone; empty uses the discovered zone.
	ZoneID string
	// VPCID adds a vpcId search filter when nonempty.
	VPCID string
	// UUID adds a uuid search filter when nonempty.
	UUID string
	// Page uses the default when not positive.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListEndpointsOutput holds Endpoint items and pagination metadata.
type ListEndpointsOutput = core.PagedList[Endpoint]

// GetEndpointInput requires EndpointID.
type GetEndpointInput struct {
	// EndpointID is required.
	EndpointID string `vngcloud:"required"`
}

// GetEndpointOutput holds the result of GetEndpoint.
type GetEndpointOutput struct {
	Endpoint EndpointDetail
}

// ListEndpointTagsInput requires EndpointID.
type ListEndpointTagsInput struct {
	// EndpointID is required.
	EndpointID string `vngcloud:"required"`
}

// ListEndpointTagsOutput holds Tag items.
type ListEndpointTagsOutput = core.List[Tag]
