package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// networkOps is network's operation table. Every Get and List operation
// reads. CreateSecurityGroup, UpdateSecurityGroup, CreateSecurityGroupRule,
// CreateVPC, UpdateVPC, CreateSubnet, UpdateSubnet, CreateRouteTable,
// CreateNetworkACL, CreateVirtualIPAddress, and UpdateVirtualIPAddress are
// Write. DeleteSecurityGroup, DeleteSecurityGroupRule, DeleteVPC,
// DeleteSubnet, DeleteRouteTable, DeleteNetworkACL, and
// DeleteVirtualIPAddress are Write and Destructive, since a deleted group,
// rule, VPC, subnet, table, ACL, or virtual IP cannot be restored by one
// more command, so each needs --yes.
// EnableVPCPrivateDNS is also Write and Destructive: the API has no call
// that disables Private DNS again, so enabling it is not undoable by one
// more command either. CreateSecurityGroupRule also carries a Guard,
// refuseWorldOpenIngressWithoutYes (svc_network_write.go), that needs --yes
// for an ingress rule whose remote prefix is 0.0.0.0/0 or ::/0: such a rule
// opens every port it names to the whole internet. AddRoute and RemoveRoute
// are Write, each carrying the Guard requireYesToChangeRoutes
// (svc_network_write.go): unlike delete-route-table, neither is Destructive,
// since running the other of the pair undoes it with one more command, but
// each still needs --yes on every call, since either can redirect or cut
// traffic for every server behind the table and the CLI cannot tell cheaply
// whether the table is in use. AddNetworkACLRule, AssociateNetworkACLSubnet,
// and DisassociateNetworkACLSubnet are the same shape, over an ACL instead
// of a route table, each carrying requireYesForACLChange
// (svc_network_acl.go). RemoveNetworkACLRule carries
// requireYesAndPriorityToRemoveACLRule (svc_network_acl.go) instead: the
// same --yes requirement, plus a --priority requirement the SDK's own Input
// does not carry, since Priority 0 is both CheckRequired's zero value and
// the priority of the ACL's own pass-all rules, which a caller may remove,
// so the SDK cannot use IsZero to tell "not given" from "naming priority 0
// on purpose" the way the CLI can. CreateDHCPOptions is Write;
// DeleteDHCPOptions is Write and Destructive, since a deleted set cannot be
// restored by one more command; list-dhcp-options and get-dhcp-options are
// Read. set-vpc-dhcp-options carries the Guard requireYesToSetVPCDHCPOptions
// (svc_network_dhcp.go), and clear-vpc-dhcp-options (ClearVPCDHCPOptions,
// under the rename table's override for its own mechanical kebab-case)
// carries requireYesToClearVPCDHCPOptions (svc_network_dhcp.go): moving or
// clearing a VPC's set changes DNS for every server behind it, and the
// servers pick up the new resolvers only once they renew, so each needs
// --yes on every call the same way AddRoute and RemoveRoute need
// requireYesToChangeRoutes on theirs, rather than being registered
// Destructive. A read-only profile refuses every one of these Write
// operations, before any request.
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
	Write[network.Client, network.CreateVPCInput, network.CreateVPCOutput](
		kebab("CreateVPC"), (*network.Client).CreateVPC),
	Write[network.Client, network.UpdateVPCInput, network.UpdateVPCOutput](
		kebab("UpdateVPC"), (*network.Client).UpdateVPC),
	Write[network.Client, network.DeleteVPCInput, network.DeleteVPCOutput](
		kebab("DeleteVPC"), (*network.Client).DeleteVPC, Destructive()),
	Write[network.Client, network.EnableVPCPrivateDNSInput, network.EnableVPCPrivateDNSOutput](
		kebab("EnableVPCPrivateDNS"), (*network.Client).EnableVPCPrivateDNS, Destructive()),
	Write[network.Client, network.CreateSubnetInput, network.CreateSubnetOutput](
		kebab("CreateSubnet"), (*network.Client).CreateSubnet),
	Write[network.Client, network.UpdateSubnetInput, network.UpdateSubnetOutput](
		kebab("UpdateSubnet"), (*network.Client).UpdateSubnet),
	Write[network.Client, network.DeleteSubnetInput, network.DeleteSubnetOutput](
		kebab("DeleteSubnet"), (*network.Client).DeleteSubnet, Destructive()),
	Read[network.Client, network.ListServersBySubnetInput, network.ListServersBySubnetOutput](
		kebab("ListServersBySubnet"), (*network.Client).ListServersBySubnet),
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
	Write[network.Client, network.CreateVirtualIPAddressInput, network.CreateVirtualIPAddressOutput](
		kebab("CreateVirtualIPAddress"), (*network.Client).CreateVirtualIPAddress),
	Write[network.Client, network.UpdateVirtualIPAddressInput, network.UpdateVirtualIPAddressOutput](
		kebab("UpdateVirtualIPAddress"), (*network.Client).UpdateVirtualIPAddress),
	Write[network.Client, network.DeleteVirtualIPAddressInput, network.DeleteVirtualIPAddressOutput](
		kebab("DeleteVirtualIPAddress"), (*network.Client).DeleteVirtualIPAddress, Destructive()),
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
	Read[network.Client, network.GetNetworkACLInput, network.GetNetworkACLOutput](
		kebab("GetNetworkACL"), (*network.Client).GetNetworkACL),
	Write[network.Client, network.CreateNetworkACLInput, network.CreateNetworkACLOutput](
		kebab("CreateNetworkACL"), (*network.Client).CreateNetworkACL),
	Write[network.Client, network.DeleteNetworkACLInput, network.DeleteNetworkACLOutput](
		kebab("DeleteNetworkACL"), (*network.Client).DeleteNetworkACL, Destructive()),
	Write[network.Client, network.AddNetworkACLRuleInput, network.AddNetworkACLRuleOutput](
		kebab("AddNetworkACLRule"), (*network.Client).AddNetworkACLRule,
		Guard(requireYesForACLChange("add-network-acl-rule"))),
	Write[network.Client, network.RemoveNetworkACLRuleInput, network.RemoveNetworkACLRuleOutput](
		kebab("RemoveNetworkACLRule"), (*network.Client).RemoveNetworkACLRule,
		Guard(requireYesAndPriorityToRemoveACLRule)),
	Write[network.Client, network.AssociateNetworkACLSubnetInput, network.AssociateNetworkACLSubnetOutput](
		kebab("AssociateNetworkACLSubnet"), (*network.Client).AssociateNetworkACLSubnet,
		Guard(requireYesForACLChange("associate-network-acl-subnet"))),
	Write[network.Client, network.DisassociateNetworkACLSubnetInput, network.DisassociateNetworkACLSubnetOutput](
		kebab("DisassociateNetworkACLSubnet"), (*network.Client).DisassociateNetworkACLSubnet,
		Guard(requireYesForACLChange("disassociate-network-acl-subnet"))),
	Read[network.Client, network.ListDHCPOptionsInput, network.ListDHCPOptionsOutput](
		kebab("ListDHCPOptions"), (*network.Client).ListDHCPOptions),
	Read[network.Client, network.GetDHCPOptionsInput, network.GetDHCPOptionsOutput](
		kebab("GetDHCPOptions"), (*network.Client).GetDHCPOptions),
	Write[network.Client, network.CreateDHCPOptionsInput, network.CreateDHCPOptionsOutput](
		kebab("CreateDHCPOptions"), (*network.Client).CreateDHCPOptions),
	Write[network.Client, network.DeleteDHCPOptionsInput, network.DeleteDHCPOptionsOutput](
		kebab("DeleteDHCPOptions"), (*network.Client).DeleteDHCPOptions, Destructive()),
	Write[network.Client, network.SetVPCDHCPOptionsInput, network.SetVPCDHCPOptionsOutput](
		"set-vpc-dhcp-options", (*network.Client).SetVPCDHCPOptions, Guard(requireYesToSetVPCDHCPOptions)),
	Write[network.Client, network.ClearVPCDHCPOptionsInput, network.ClearVPCDHCPOptionsOutput](
		"clear-vpc-dhcp-options", (*network.Client).ClearVPCDHCPOptions, Guard(requireYesToClearVPCDHCPOptions)),
}

func newNetworkCmd(e *env) *cobra.Command {
	return Service(e, "network", "VPCs, security groups, and network interfaces", network.New, networkOps...)
}
