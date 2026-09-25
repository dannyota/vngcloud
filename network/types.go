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
