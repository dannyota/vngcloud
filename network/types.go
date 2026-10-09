package network

import (
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/core"
)

type ListVNetworkRegionsInput struct{}
type ListVNetworkRegionsOutput = core.List[VNetworkRegion]

type ListVPCsInput struct {
	Name string
	Page int
	Size int
}
type ListVPCsOutput = core.PagedList[VPC]

type ListWANIPsInput struct {
	Name string
	Page int
	Size int
}
type ListWANIPsOutput = core.PagedList[WANIP]

type ListNetworkInterfacesInput struct {
	Name string
	Page int
	Size int
}
type ListNetworkInterfacesOutput = core.PagedList[ElasticNetworkInterface]

type ListSecurityGroupsInput struct {
	Name string
	Page int
	Size int
}
type ListSecurityGroupsOutput = core.PagedList[SecurityGroup]

type ListVirtualIPAddressesInput struct {
	Name string
	Page int
	Size int
}
type ListVirtualIPAddressesOutput = core.PagedList[VirtualIPAddress]

type ListRouteTablesInput struct {
	Name string
	Page int
	Size int
}
type ListRouteTablesOutput = core.PagedList[RouteTable]

type ListPeeringsInput struct {
	Name string
	Page int
	Size int
}
type ListPeeringsOutput = core.PagedList[Peering]

type ListNetworkACLsInput struct {
	Name string
	Page int
	Size int
}
type ListNetworkACLsOutput = core.PagedList[ACL]

type ListInterconnectsInput struct {
	Name string
	Page int
	Size int
}
type ListInterconnectsOutput = core.PagedList[Interconnect]

type GetVPCInput struct {
	VPCID string `vngcloud:"required"`
}
type GetVPCOutput struct {
	VPC VPC
}

type ListDHCPOptionsInput struct {
	Name string
	Page int
	Size int
}
type ListDHCPOptionsOutput = core.PagedList[DHCPOptions]

type GetDHCPOptionsInput struct {
	DHCPOptionsID string `vngcloud:"required"`
}
type GetDHCPOptionsOutput struct {
	DHCPOptions DHCPOptions
}

// CreateDHCPOptionsInput creates a DHCP options set. DNSServers must hold at
// least one IPv4 address; the server enforces the four-server limit. MTU is
// sent only when set; the server's default is 1450.
type CreateDHCPOptionsInput struct {
	Name       string   `vngcloud:"required"`
	DNSServers []string `vngcloud:"required"`

	MTU *int
}
type CreateDHCPOptionsOutput struct {
	DHCPOptions DHCPOptions
}

type DeleteDHCPOptionsInput struct {
	DHCPOptionsID string `vngcloud:"required"`
}
type DeleteDHCPOptionsOutput struct{}

// SetVPCDHCPOptionsInput moves VPCID onto DHCPOptionsID's set.
// ClearVPCDHCPOptionsInput removes a VPC's set instead of moving it to
// another one; see SetVPCDHCPOptions's doc comment for the guards the two
// calls share.
type SetVPCDHCPOptionsInput struct {
	VPCID         string `vngcloud:"required"`
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
	VPCID string `vngcloud:"required"`
}

// ClearVPCDHCPOptionsOutput is the VPC after the call, and whether the call
// itself changed anything. Changed is false only when the VPC already had
// no DHCP options set.
type ClearVPCDHCPOptionsOutput struct {
	VPC     VPC
	Changed bool
}

type GetSubnetInput struct {
	VPCID    string `vngcloud:"required"`
	SubnetID string `vngcloud:"required"`
}
type GetSubnetOutput struct {
	Subnet Subnet
}

type ListSubnetsInput struct{}
type ListSubnetsOutput = core.List[Subnet]

type ListSubnetsByVPCInput struct {
	VPCID string `vngcloud:"required"`
}
type ListSubnetsByVPCOutput = core.List[Subnet]

type GetSecurityGroupInput struct {
	SecurityGroupID string `vngcloud:"required"`
}
type GetSecurityGroupOutput struct {
	SecurityGroup SecurityGroup
}

type ListServersBySecurityGroupInput struct {
	SecurityGroupID string `vngcloud:"required"`
}
type ListServersBySecurityGroupOutput = core.List[compute.Server]

type ListSecurityGroupRulesInput struct {
	SecurityGroupID string `vngcloud:"required"`
}
type ListSecurityGroupRulesOutput = core.List[SecurityGroupRule]

type ListAllSecurityGroupRulesInput struct{}
type ListAllSecurityGroupRulesOutput = core.List[SecurityGroupRule]

type ListRouteTableRoutesInput struct{}
type ListRouteTableRoutesOutput = core.List[RouteTableRoute]

type GetVirtualIPAddressInput struct {
	VirtualIPAddressID string `vngcloud:"required"`
}
type GetVirtualIPAddressOutput struct {
	VirtualIPAddress VirtualIPAddress
}

type ListAddressPairsByVirtualIPAddressInput struct {
	VirtualIPAddressID string `vngcloud:"required"`
}
type ListAddressPairsByVirtualIPAddressOutput = core.List[AddressPair]

type ListAddressPairsByVirtualSubnetInput struct {
	VirtualSubnetID string `vngcloud:"required"`
}
type ListAddressPairsByVirtualSubnetOutput = core.List[AddressPair]

type ListAllVirtualIPAddressAddressPairsInput struct{}
type ListAllVirtualIPAddressAddressPairsOutput = core.List[AddressPair]

type ListEndpointsInput struct {
	ZoneID string
	VPCID  string
	UUID   string
	Page   int
	Size   int
}
type ListEndpointsOutput = core.PagedList[Endpoint]

type GetEndpointInput struct {
	EndpointID string `vngcloud:"required"`
}
type GetEndpointOutput struct {
	Endpoint EndpointDetail
}

type ListEndpointTagsInput struct {
	EndpointID string `vngcloud:"required"`
}
type ListEndpointTagsOutput = core.List[Tag]
