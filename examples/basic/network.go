package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/network"
)

func showNetwork(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	networkClient := network.New(cfg)

	vnetRegionsOut, err := networkClient.ListVNetworkRegions(ctx, nil)
	vnetRegions := []network.VNetworkRegion(nil)
	if vnetRegionsOut != nil {
		vnetRegions = vnetRegionsOut.Items
	}
	record(outputs, cfg, "network/vnetwork_region", "vnetwork regions", vnetRegions, err)

	vpcs, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.VPC, int, error) {
		out, err := networkClient.ListVPCs(ctx, &network.ListVPCsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/vpc", "vpcs", vpcs, err)

	vpcDetails, vpcDetailErr := collectDetails(vpcs,
		func(vpc network.VPC) string { return vpc.UUID },
		func(id string) (*network.VPC, error) {
			out, err := networkClient.GetVPC(ctx, &network.GetVPCInput{VPCID: id})
			if err != nil {
				return nil, err
			}
			return &out.VPC, nil
		},
	)
	if err != nil {
		vpcDetailErr = err
	}
	record(outputs, cfg, "network/vpc_detail", "vpc details", vpcDetails, vpcDetailErr)

	subnetsOut, err := networkClient.ListSubnets(ctx, nil)
	subnets := []network.Subnet(nil)
	if subnetsOut != nil {
		subnets = subnetsOut.Items
	}
	record(outputs, cfg, "network/subnet", "subnets", subnets, err)

	subnetDetails, subnetDetailErr := collectDetails2(subnets,
		func(subnet network.Subnet) (string, string) { return subnet.NetworkID, subnet.UUID },
		func(networkID, subnetID string) (*network.Subnet, error) {
			out, err := networkClient.GetSubnet(ctx, &network.GetSubnetInput{VPCID: networkID, SubnetID: subnetID})
			if err != nil {
				return nil, err
			}
			return &out.Subnet, nil
		},
	)
	if err != nil {
		subnetDetailErr = err
	}
	record(outputs, cfg, "network/subnet_detail", "subnet details", subnetDetails, subnetDetailErr)

	wanIPs, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.WANIP, int, error) {
		out, err := networkClient.ListWANIPs(ctx, &network.ListWANIPsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/floating_ip", "floating ips", wanIPs, err)

	interfaces, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.ElasticNetworkInterface, int, error) {
		out, err := networkClient.ListNetworkInterfaces(ctx, &network.ListNetworkInterfacesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/interface", "network interfaces", interfaces, err)

	securityGroups, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.SecurityGroup, int, error) {
		out, err := networkClient.ListSecurityGroups(ctx, &network.ListSecurityGroupsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/security_group", "security groups", securityGroups, err)

	securityGroupDetails := make([]*network.SecurityGroup, 0, len(securityGroups))
	securityGroupServers := make([]compute.Server, 0)
	securityGroupDetailErr := err
	if securityGroupDetailErr == nil {
		for _, securityGroup := range securityGroups {
			if securityGroup.ID == "" {
				continue
			}
			detailOut, detailErr := networkClient.GetSecurityGroup(ctx, &network.GetSecurityGroupInput{SecurityGroupID: securityGroup.ID})
			if detailErr != nil {
				securityGroupDetailErr = detailErr
				break
			}
			securityGroupDetails = append(securityGroupDetails, &detailOut.SecurityGroup)

			serversOut, serversErr := networkClient.ListServersBySecurityGroup(ctx, &network.ListServersBySecurityGroupInput{SecurityGroupID: securityGroup.ID})
			if serversErr != nil {
				securityGroupDetailErr = serversErr
				break
			}
			securityGroupServers = append(securityGroupServers, serversOut.Items...)
		}
	}
	record(outputs, cfg, "network/security_group_detail", "security group details", securityGroupDetails, securityGroupDetailErr)
	record(outputs, cfg, "network/security_group_server", "security group servers", securityGroupServers, securityGroupDetailErr)

	securityGroupRulesOut, err := networkClient.ListAllSecurityGroupRules(ctx, nil)
	securityGroupRules := []network.SecurityGroupRule(nil)
	if securityGroupRulesOut != nil {
		securityGroupRules = securityGroupRulesOut.Items
	}
	record(outputs, cfg, "network/security_group_rule", "security group rules", securityGroupRules, err)

	virtualIPs, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.VirtualIPAddress, int, error) {
		out, err := networkClient.ListVirtualIPAddresses(ctx, &network.ListVirtualIPAddressesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/virtual_ip", "virtual ips", virtualIPs, err)

	virtualIPDetails := make([]*network.VirtualIPAddress, 0, len(virtualIPs))
	addressPairs := make([]network.AddressPair, 0)
	subnetAddressPairs := make([]network.AddressPair, 0)
	if err == nil {
		for _, virtualIP := range virtualIPs {
			detailOut, detailErr := networkClient.GetVirtualIPAddress(ctx, &network.GetVirtualIPAddressInput{VirtualIPAddressID: virtualIP.UUID})
			if detailErr != nil {
				err = detailErr
				break
			}
			virtualIPDetails = append(virtualIPDetails, &detailOut.VirtualIPAddress)

			pairsOut, pairErr := networkClient.ListAddressPairsByVirtualIPAddress(ctx, &network.ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: virtualIP.UUID})
			if pairErr != nil {
				err = pairErr
				break
			}
			addressPairs = append(addressPairs, pairsOut.Items...)

			if virtualIP.SubnetID != "" {
				subnetPairsOut, pairErr := networkClient.ListAddressPairsByVirtualSubnet(ctx, &network.ListAddressPairsByVirtualSubnetInput{VirtualSubnetID: virtualIP.SubnetID})
				if pairErr != nil {
					err = pairErr
					break
				}
				subnetAddressPairs = append(subnetAddressPairs, subnetPairsOut.Items...)
			}
		}
	}
	record(outputs, cfg, "network/virtual_ip_detail", "virtual ip details", virtualIPDetails, err)
	record(outputs, cfg, "network/virtual_ip_address_pair", "virtual ip address pairs", addressPairs, err)
	record(outputs, cfg, "network/virtual_subnet_address_pair", "virtual subnet address pairs", subnetAddressPairs, err)

	routeTables, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.RouteTable, int, error) {
		out, err := networkClient.ListRouteTables(ctx, &network.ListRouteTablesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/route_table", "route tables", routeTables, err)

	routeTableRoutesOut, err := networkClient.ListRouteTableRoutes(ctx, nil)
	routeTableRoutes := []network.RouteTableRoute(nil)
	if routeTableRoutesOut != nil {
		routeTableRoutes = routeTableRoutesOut.Items
	}
	record(outputs, cfg, "network/route_table_route", "route table routes", routeTableRoutes, err)

	peerings, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.Peering, int, error) {
		out, err := networkClient.ListPeerings(ctx, &network.ListPeeringsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/peering", "peerings", peerings, err)

	acls, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.ACL, int, error) {
		out, err := networkClient.ListNetworkACLs(ctx, &network.ListNetworkACLsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/network_acl", "network acls", acls, err)

	interconnects, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.Interconnect, int, error) {
		out, err := networkClient.ListInterconnects(ctx, &network.ListInterconnectsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/interconnect", "interconnects", interconnects, err)

	endpoints, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.Endpoint, int, error) {
		out, err := networkClient.ListEndpoints(ctx, &network.ListEndpointsInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "network/endpoint", "endpoints", endpoints, err)

	endpointDetails, endpointErr := collectDetails(endpoints,
		func(endpoint network.Endpoint) string { return endpoint.UUID },
		func(id string) (*network.EndpointDetail, error) {
			out, err := networkClient.GetEndpoint(ctx, &network.GetEndpointInput{EndpointID: id})
			if err != nil {
				return nil, err
			}
			return &out.Endpoint, nil
		},
	)
	if err != nil {
		endpointErr = err
	}
	record(outputs, cfg, "network/endpoint_detail", "endpoint details", endpointDetails, endpointErr)

	endpointTags := make([]network.Tag, 0)
	endpointTagErr := err
	if endpointTagErr == nil {
		for _, endpoint := range endpoints {
			tagsOut, tagErr := networkClient.ListEndpointTags(ctx, &network.ListEndpointTagsInput{EndpointID: endpoint.UUID})
			if tagErr != nil {
				endpointTagErr = tagErr
				break
			}
			endpointTags = append(endpointTags, tagsOut.Items...)
		}
	}
	record(outputs, cfg, "network/endpoint_tag", "endpoint tags", endpointTags, endpointTagErr)
}
