package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// networkOps is network's operation table. Every Get and List operation
// reads. CreateSecurityGroup, UpdateSecurityGroup, CreateSecurityGroupRule,
// and CreateRouteTable are Write; DeleteSecurityGroup, DeleteSecurityGroupRule,
// and DeleteRouteTable are Write and Destructive, since a deleted group,
// rule, or table cannot be restored by one more command, so each needs
// --yes. CreateSecurityGroupRule also carries a Guard,
// refuseWorldOpenIngressWithoutYes (svc_network_write.go), that needs --yes
// for an ingress rule whose remote prefix is 0.0.0.0/0 or ::/0: such a rule
// opens every port it names to the whole internet. AddRoute and RemoveRoute
// are Write, each carrying the Guard requireYesToChangeRoutes
// (svc_network_write.go): unlike delete-route-table, neither is Destructive,
// since running the other of the pair undoes it with one more command, but
// each still needs --yes on every call, since either can redirect or cut
// traffic for every server behind the table and the CLI cannot tell cheaply
// whether the table is in use. A read-only profile refuses every one of
// these Write operations, before any request.
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
	Write[network.Client, network.CreateSecurityGroupInput, network.CreateSecurityGroupOutput](
		kebab("CreateSecurityGroup"), (*network.Client).CreateSecurityGroup),
	Write[network.Client, network.UpdateSecurityGroupInput, network.UpdateSecurityGroupOutput](
		kebab("UpdateSecurityGroup"), (*network.Client).UpdateSecurityGroup),
	Write[network.Client, network.DeleteSecurityGroupInput, network.DeleteSecurityGroupOutput](
		kebab("DeleteSecurityGroup"), (*network.Client).DeleteSecurityGroup, Destructive()),
	Read[network.Client, network.ListServersBySecurityGroupInput, network.ListServersBySecurityGroupOutput](
		kebab("ListServersBySecurityGroup"), (*network.Client).ListServersBySecurityGroup),
	Read[network.Client, network.ListVirtualIPAddressesInput, network.ListVirtualIPAddressesOutput](
		kebab("ListVirtualIPAddresses"), (*network.Client).ListVirtualIPAddresses),
	Read[network.Client, network.ListRouteTablesInput, network.ListRouteTablesOutput](
		kebab("ListRouteTables"), (*network.Client).ListRouteTables),
	Read[network.Client, network.GetRouteTableInput, network.GetRouteTableOutput](
		kebab("GetRouteTable"), (*network.Client).GetRouteTable),
	Write[network.Client, network.CreateRouteTableInput, network.CreateRouteTableOutput](
		kebab("CreateRouteTable"), (*network.Client).CreateRouteTable),
	Write[network.Client, network.DeleteRouteTableInput, network.DeleteRouteTableOutput](
		kebab("DeleteRouteTable"), (*network.Client).DeleteRouteTable, Destructive()),
	Write[network.Client, network.AddRouteInput, network.AddRouteOutput](
		kebab("AddRoute"), (*network.Client).AddRoute, Guard(requireYesToChangeRoutes("add-route"))),
	Write[network.Client, network.RemoveRouteInput, network.RemoveRouteOutput](
		kebab("RemoveRoute"), (*network.Client).RemoveRoute, Guard(requireYesToChangeRoutes("remove-route"))),
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
	Write[network.Client, network.CreateSecurityGroupRuleInput, network.CreateSecurityGroupRuleOutput](
		kebab("CreateSecurityGroupRule"), (*network.Client).CreateSecurityGroupRule,
		Guard(refuseWorldOpenIngressWithoutYes)),
	Write[network.Client, network.DeleteSecurityGroupRuleInput, network.DeleteSecurityGroupRuleOutput](
		kebab("DeleteSecurityGroupRule"), (*network.Client).DeleteSecurityGroupRule, Destructive()),
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
