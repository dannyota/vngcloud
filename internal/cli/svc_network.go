package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// networkOps is network's operation table. Every operation reads; network
// writes are not part of this release.
var networkOps = []Op[network.Client]{
	Read[network.Client, network.ListVNetworkRegionsInput, network.ListVNetworkRegionsOutput](
		kebab("ListVNetworkRegions"), (*network.Client).ListVNetworkRegions),
	Read[network.Client, network.ListVPCsInput, network.ListVPCsOutput](
		kebab("ListVPCs"), (*network.Client).ListVPCs),
	Read[network.Client, network.ListWANIPsInput, network.ListWANIPsOutput](
		kebab("ListWANIPs"), (*network.Client).ListWANIPs),
	Read[network.Client, network.ListNetworkInterfacesInput, network.ListNetworkInterfacesOutput](
		kebab("ListNetworkInterfaces"), (*network.Client).ListNetworkInterfaces),
	Read[network.Client, network.ListSecurityGroupsInput, network.ListSecurityGroupsOutput](
		kebab("ListSecurityGroups"), (*network.Client).ListSecurityGroups),
	Read[network.Client, network.GetSecurityGroupInput, network.GetSecurityGroupOutput](
		kebab("GetSecurityGroup"), (*network.Client).GetSecurityGroup),
	Read[network.Client, network.ListServersBySecurityGroupInput, network.ListServersBySecurityGroupOutput](
		kebab("ListServersBySecurityGroup"), (*network.Client).ListServersBySecurityGroup),
	Read[network.Client, network.ListVirtualIPAddressesInput, network.ListVirtualIPAddressesOutput](
		kebab("ListVirtualIPAddresses"), (*network.Client).ListVirtualIPAddresses),
	Read[network.Client, network.ListRouteTablesInput, network.ListRouteTablesOutput](
		kebab("ListRouteTables"), (*network.Client).ListRouteTables),
	Read[network.Client, network.ListPeeringsInput, network.ListPeeringsOutput](
		kebab("ListPeerings"), (*network.Client).ListPeerings),
	Read[network.Client, network.ListNetworkACLsInput, network.ListNetworkACLsOutput](
		kebab("ListNetworkACLs"), (*network.Client).ListNetworkACLs),
	Read[network.Client, network.ListSubnetsInput, network.ListSubnetsOutput](
		kebab("ListSubnets"), (*network.Client).ListSubnets),
	Read[network.Client, network.ListSubnetsByVPCInput, network.ListSubnetsByVPCOutput](
		kebab("ListSubnetsByVPC"), (*network.Client).ListSubnetsByVPC),
	Read[network.Client, network.GetVPCInput, network.GetVPCOutput](
		kebab("GetVPC"), (*network.Client).GetVPC),
	Read[network.Client, network.GetSubnetInput, network.GetSubnetOutput](
		kebab("GetSubnet"), (*network.Client).GetSubnet),
	Read[network.Client, network.ListSecurityGroupRulesInput, network.ListSecurityGroupRulesOutput](
		kebab("ListSecurityGroupRules"), (*network.Client).ListSecurityGroupRules),
	Read[network.Client, network.ListAllSecurityGroupRulesInput, network.ListAllSecurityGroupRulesOutput](
		kebab("ListAllSecurityGroupRules"), (*network.Client).ListAllSecurityGroupRules),
	Read[network.Client, network.ListRouteTableRoutesInput, network.ListRouteTableRoutesOutput](
		kebab("ListRouteTableRoutes"), (*network.Client).ListRouteTableRoutes),
	Read[network.Client, network.GetVirtualIPAddressInput, network.GetVirtualIPAddressOutput](
		kebab("GetVirtualIPAddress"), (*network.Client).GetVirtualIPAddress),
	Read[network.Client, network.ListAddressPairsByVirtualIPAddressInput, network.ListAddressPairsByVirtualIPAddressOutput](
		kebab("ListAddressPairsByVirtualIPAddress"), (*network.Client).ListAddressPairsByVirtualIPAddress),
	Read[network.Client, network.ListAddressPairsByVirtualSubnetInput, network.ListAddressPairsByVirtualSubnetOutput](
		kebab("ListAddressPairsByVirtualSubnet"), (*network.Client).ListAddressPairsByVirtualSubnet),
	Read[network.Client, network.ListAllVirtualIPAddressAddressPairsInput, network.ListAllVirtualIPAddressAddressPairsOutput](
		kebab("ListAllVirtualIPAddressAddressPairs"), (*network.Client).ListAllVirtualIPAddressAddressPairs),
	Read[network.Client, network.ListInterconnectsInput, network.ListInterconnectsOutput](
		kebab("ListInterconnects"), (*network.Client).ListInterconnects),
	Read[network.Client, network.ListEndpointsInput, network.ListEndpointsOutput](
		kebab("ListEndpoints"), (*network.Client).ListEndpoints),
	Read[network.Client, network.GetEndpointInput, network.GetEndpointOutput](
		kebab("GetEndpoint"), (*network.Client).GetEndpoint),
	Read[network.Client, network.ListEndpointTagsInput, network.ListEndpointTagsOutput](
		kebab("ListEndpointTags"), (*network.Client).ListEndpointTags),
}

func newNetworkCmd(e *env) *cobra.Command {
	return Service(e, "network", "VPCs, security groups, and network interfaces", network.New, networkOps...)
}
