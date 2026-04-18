package main

import (
	"context"
	"fmt"

	"danny.vn/vngcloud"
)

func showProjects(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	projects, err := client.ListProjects(ctx, nil)
	record(outputs, client, "project/project", "visible projects in region", projects, err)
}

func showPortal(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	userInfo, err := client.Portal.GetUserInfo(ctx)
	recordOne(outputs, client, "portal/user_info", "portal user info", userInfo, err)

	zones, err := client.Portal.ListZones(ctx)
	record(outputs, client, "portal/zone", "portal zones", zones, err)

	quotas, err := client.Portal.ListQuotaUsed(ctx)
	if err != nil {
		fmt.Printf("portal quota used: error\n")
		outputs.add("portal/quota_used", client, nil, err)
	} else {
		fmt.Printf("portal quota used: %d\n", len(quotas))
		outputs.add("portal/quota_used", client, quotas, nil)
		quotaDetails := make([]vngcloud.PortalQuota, 0, len(quotas))
		for _, quota := range quotas {
			name, ok := quotaName(quota)
			if !ok {
				continue
			}
			detail, detailErr := client.Portal.GetQuota(ctx, name)
			if detailErr != nil {
				fmt.Printf("portal quota detail: error\n")
				continue
			}
			quotaDetails = append(quotaDetails, detail)
		}
		fmt.Printf("portal quota details: %d\n", len(quotaDetails))
		outputs.add("portal/quota_detail", client, quotaDetails, nil)
	}

	tagQuota, err := client.Portal.GetTagQuota(ctx)
	recordOne(outputs, client, "portal/tag_quota", "portal tag quota", tagQuota, err)
}

func quotaName(quota vngcloud.PortalQuota) (string, bool) {
	for _, key := range []string{"name", "quotaName", "resourceName", "resource", "key", "type"} {
		value, ok := quota[key]
		if !ok {
			continue
		}
		name, ok := value.(string)
		if ok && name != "" {
			return name, true
		}
	}
	return "", false
}

func showCompute(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	servers, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListServersResult, error) {
		return client.Compute.ListServers(ctx, &vngcloud.ListServersOptions{Page: page, Size: size})
	})
	record(outputs, client, "server/instance", "servers", servers, err)

	keys, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListSSHKeysResult, error) {
		return client.Compute.ListSSHKeys(ctx, &vngcloud.ListSSHKeysOptions{Page: page, Size: size})
	})
	record(outputs, client, "server/ssh_key", "ssh keys", keys, err)

	groups, err := client.Compute.ListServerGroups(ctx, &vngcloud.ListServerGroupsOptions{Page: 0, Size: vngcloud.DefaultPageSize})
	record(outputs, client, "server/placement_group", "placement groups", itemsOf(groups), err)

	serverSecgroups, err := client.Compute.ListServerSecurityGroups(ctx)
	record(outputs, client, "server/instance_security_group", "server security groups", serverSecgroups, err)

	groupMembers, err := client.Compute.ListServerGroupMembers(ctx)
	record(outputs, client, "server/placement_group_member", "placement group members", groupMembers, err)

	policies, err := client.Compute.ListServerGroupPolicies(ctx)
	record(outputs, client, "server/placement_group_policy", "placement group policies", policies, err)

	osImages, err := client.Compute.ListOSImages(ctx, nil)
	record(outputs, client, "server/system_image_os", "os images", osImages, err)

	gpuImages, err := client.Compute.ListGPUImages(ctx)
	record(outputs, client, "server/system_image_gpu", "gpu images", gpuImages, err)

	userImages, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListUserImagesResult, error) {
		return client.Compute.ListUserImages(ctx, &vngcloud.ListUserImagesOptions{Page: page, Size: size})
	})
	record(outputs, client, "server/user_image", "user images", userImages, err)
}

func showVolume(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	volumes, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListVolumesResult, error) {
		return client.Volume.ListVolumes(ctx, &vngcloud.ListVolumesOptions{Page: page, Size: size})
	})
	record(outputs, client, "volume/volume", "volumes", volumes, err)

	underlyingVolumes, underlyingErr := collectDetails(volumes,
		func(volume vngcloud.Volume) string { return volume.UUID },
		func(id string) (*vngcloud.Volume, error) { return client.Volume.GetUnderlyingVolume(ctx, id) },
	)
	if err != nil {
		underlyingErr = err
	}
	record(outputs, client, "volume/underlying_volume", "underlying volumes", underlyingVolumes, underlyingErr)

	defaultType, err := client.Volume.GetDefaultVolumeType(ctx)
	recordOne(outputs, client, "volume/default_type", "default volume type", defaultType, err)

	typeZones, err := client.Volume.ListVolumeTypeZones(ctx, nil)
	record(outputs, client, "volume/type_zone", "volume type zones", typeZones, err)

	types, err := client.Volume.ListVolumeTypes(ctx, nil)
	record(outputs, client, "volume/type", "volume types", types, err)

	typeDetails, typeDetailErr := collectDetails(types,
		func(volumeType vngcloud.VolumeType) string { return volumeType.ID },
		func(id string) (*vngcloud.VolumeType, error) { return client.Volume.GetVolumeType(ctx, id) },
	)
	if err != nil {
		typeDetailErr = err
	}
	record(outputs, client, "volume/type_detail", "volume type details", typeDetails, typeDetailErr)

	encryptionTypes, err := client.Volume.ListEncryptionTypes(ctx)
	record(outputs, client, "volume/encryption_type", "encryption types", encryptionTypes, err)

	snapshots, err := client.Volume.ListAllSnapshots(ctx)
	record(outputs, client, "volume/snapshot", "snapshots", snapshots, err)
}

func showNetwork(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	vnetRegions, err := client.Network.ListVNetworkRegions(ctx)
	record(outputs, client, "network/vnetwork_region", "vnetwork regions", vnetRegions, err)

	vpcs, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListVPCsResult, error) {
		return client.Network.ListVPCs(ctx, &vngcloud.ListVPCsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/vpc", "vpcs", vpcs, err)

	vpcDetails, vpcDetailErr := collectDetails(vpcs,
		func(vpc vngcloud.VPC) string { return vpc.UUID },
		func(id string) (*vngcloud.VPC, error) { return client.Network.GetVPC(ctx, id) },
	)
	if err != nil {
		vpcDetailErr = err
	}
	record(outputs, client, "network/vpc_detail", "vpc details", vpcDetails, vpcDetailErr)

	subnets, err := client.Network.ListSubnets(ctx)
	record(outputs, client, "network/subnet", "subnets", subnets, err)

	subnetDetails, subnetDetailErr := collectDetails2(subnets,
		func(subnet vngcloud.Subnet) (string, string) { return subnet.NetworkID, subnet.UUID },
		func(networkID, subnetID string) (*vngcloud.Subnet, error) {
			return client.Network.GetSubnet(ctx, networkID, subnetID)
		},
	)
	if err != nil {
		subnetDetailErr = err
	}
	record(outputs, client, "network/subnet_detail", "subnet details", subnetDetails, subnetDetailErr)

	wanIPs, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListWANIPsResult, error) {
		return client.Network.ListWANIPs(ctx, &vngcloud.ListWANIPsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/floating_ip", "floating ips", wanIPs, err)

	interfaces, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListNetworkInterfacesResult, error) {
		return client.Network.ListNetworkInterfaces(ctx, &vngcloud.ListNetworkInterfacesOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/interface", "network interfaces", interfaces, err)

	securityGroups, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListSecurityGroupsResult, error) {
		return client.Network.ListSecurityGroups(ctx, &vngcloud.ListSecurityGroupsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/security_group", "security groups", securityGroups, err)

	securityGroupDetails := make([]*vngcloud.SecurityGroup, 0, len(securityGroups))
	securityGroupServers := make([]vngcloud.Server, 0)
	securityGroupDetailErr := err
	if securityGroupDetailErr == nil {
		for _, securityGroup := range securityGroups {
			if securityGroup.ID == "" {
				continue
			}
			detail, detailErr := client.Network.GetSecurityGroup(ctx, securityGroup.ID)
			if detailErr != nil {
				securityGroupDetailErr = detailErr
				break
			}
			securityGroupDetails = append(securityGroupDetails, detail)

			servers, serversErr := client.Network.ListServersBySecurityGroup(ctx, securityGroup.ID)
			if serversErr != nil {
				securityGroupDetailErr = serversErr
				break
			}
			securityGroupServers = append(securityGroupServers, servers...)
		}
	}
	record(outputs, client, "network/security_group_detail", "security group details", securityGroupDetails, securityGroupDetailErr)
	record(outputs, client, "network/security_group_server", "security group servers", securityGroupServers, securityGroupDetailErr)

	securityGroupRules, err := client.Network.ListAllSecurityGroupRules(ctx)
	record(outputs, client, "network/security_group_rule", "security group rules", securityGroupRules, err)

	virtualIPs, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListVirtualIPAddressesResult, error) {
		return client.Network.ListVirtualIPAddresses(ctx, &vngcloud.ListVirtualIPAddressesOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/virtual_ip", "virtual ips", virtualIPs, err)

	virtualIPDetails := make([]*vngcloud.VirtualIPAddress, 0, len(virtualIPs))
	addressPairs := make([]vngcloud.AddressPair, 0)
	subnetAddressPairs := make([]vngcloud.AddressPair, 0)
	if err == nil {
		for _, virtualIP := range virtualIPs {
			detail, detailErr := client.Network.GetVirtualIPAddress(ctx, virtualIP.UUID)
			if detailErr != nil {
				err = detailErr
				break
			}
			virtualIPDetails = append(virtualIPDetails, detail)

			pairs, pairErr := client.Network.ListAddressPairsByVirtualIPAddress(ctx, virtualIP.UUID)
			if pairErr != nil {
				err = pairErr
				break
			}
			addressPairs = append(addressPairs, pairs...)

			if virtualIP.SubnetID != "" {
				pairs, pairErr := client.Network.ListAddressPairsByVirtualSubnet(ctx, virtualIP.SubnetID)
				if pairErr != nil {
					err = pairErr
					break
				}
				subnetAddressPairs = append(subnetAddressPairs, pairs...)
			}
		}
	}
	record(outputs, client, "network/virtual_ip_detail", "virtual ip details", virtualIPDetails, err)
	record(outputs, client, "network/virtual_ip_address_pair", "virtual ip address pairs", addressPairs, err)
	record(outputs, client, "network/virtual_subnet_address_pair", "virtual subnet address pairs", subnetAddressPairs, err)

	routeTables, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListRouteTablesResult, error) {
		return client.Network.ListRouteTables(ctx, &vngcloud.ListRouteTablesOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/route_table", "route tables", routeTables, err)

	routeTableRoutes, err := client.Network.ListRouteTableRoutes(ctx)
	record(outputs, client, "network/route_table_route", "route table routes", routeTableRoutes, err)

	peerings, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListPeeringsResult, error) {
		return client.Network.ListPeerings(ctx, &vngcloud.ListPeeringsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/peering", "peerings", peerings, err)

	acls, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListNetworkACLsResult, error) {
		return client.Network.ListNetworkACLs(ctx, &vngcloud.ListNetworkACLsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/network_acl", "network acls", acls, err)

	interconnects, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListInterconnectsResult, error) {
		return client.Network.ListInterconnects(ctx, &vngcloud.ListInterconnectsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/interconnect", "interconnects", interconnects, err)

	endpoints, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListEndpointsResult, error) {
		return client.Network.ListEndpoints(ctx, &vngcloud.ListEndpointsOptions{Page: page, Size: size})
	})
	record(outputs, client, "network/endpoint", "endpoints", endpoints, err)

	endpointDetails, endpointErr := collectDetails(endpoints,
		func(endpoint vngcloud.NetworkEndpoint) string { return endpoint.UUID },
		func(id string) (*vngcloud.NetworkEndpointDetail, error) { return client.Network.GetEndpoint(ctx, id) },
	)
	if err != nil {
		endpointErr = err
	}
	record(outputs, client, "network/endpoint_detail", "endpoint details", endpointDetails, endpointErr)

	endpointTags := make([]vngcloud.Tag, 0)
	endpointTagErr := err
	if endpointTagErr == nil {
		for _, endpoint := range endpoints {
			tags, tagErr := client.Network.ListEndpointTags(ctx, endpoint.UUID)
			if tagErr != nil {
				endpointTagErr = tagErr
				break
			}
			endpointTags = append(endpointTags, tags...)
		}
	}
	record(outputs, client, "network/endpoint_tag", "endpoint tags", endpointTags, endpointTagErr)
}

func showLoadBalancer(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	loadBalancers, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListLoadBalancersResult, error) {
		return client.LoadBalancer.ListLoadBalancers(ctx, &vngcloud.ListLoadBalancersOptions{Page: page, Size: size})
	})
	record(outputs, client, "loadbalancer/load_balancer", "load balancers", loadBalancers, err)

	var children loadBalancerChildren
	nestedErr := err
	if nestedErr == nil {
		children, nestedErr = collectLoadBalancerChildren(ctx, client, loadBalancers)
	}
	recordLoadBalancerChildren(outputs, client, children, nestedErr)

	packages, err := client.LoadBalancer.ListLoadBalancerPackages(ctx, nil)
	record(outputs, client, "loadbalancer/package", "load balancer packages", packages, err)

	certificates, err := collectPages(vngcloud.DefaultPageSize, func(page, size int) (*vngcloud.ListCertificatesResult, error) {
		return client.LoadBalancer.ListCertificates(ctx, &vngcloud.ListCertificatesOptions{Page: page, Size: size})
	})
	record(outputs, client, "loadbalancer/certificate", "certificates", certificates, err)

	certificateDetails, certificateErr := collectDetails(certificates,
		func(certificate vngcloud.Certificate) string { return certificate.UUID },
		func(id string) (*vngcloud.Certificate, error) { return client.LoadBalancer.GetCertificate(ctx, id) },
	)
	if err != nil {
		certificateErr = err
	}
	record(outputs, client, "loadbalancer/certificate_detail", "certificate details", certificateDetails, certificateErr)
}

type loadBalancerChildren struct {
	details         []*vngcloud.LoadBalancer
	listeners       []vngcloud.Listener
	listenerDetails []*vngcloud.Listener
	pools           []vngcloud.Pool
	poolDetails     []*vngcloud.Pool
	healthMonitors  []*vngcloud.HealthMonitor
	poolMembers     []vngcloud.PoolMember
	policies        []vngcloud.Policy
	policyDetails   []*vngcloud.Policy
	tags            []vngcloud.LoadBalancerTag
}

func recordLoadBalancerChildren(outputs *sdkOutputStore, client *vngcloud.Client, children loadBalancerChildren, err error) {
	if err != nil {
		record(outputs, client, "loadbalancer/load_balancer_detail", "load balancer details", []*vngcloud.LoadBalancer(nil), err)
		record(outputs, client, "loadbalancer/listener", "load balancer listeners", []vngcloud.Listener(nil), err)
		record(outputs, client, "loadbalancer/listener_detail", "load balancer listener details", []*vngcloud.Listener(nil), err)
		record(outputs, client, "loadbalancer/pool", "load balancer pools", []vngcloud.Pool(nil), err)
		record(outputs, client, "loadbalancer/pool_detail", "load balancer pool details", []*vngcloud.Pool(nil), err)
		record(outputs, client, "loadbalancer/pool_health_monitor", "load balancer pool health monitors", []*vngcloud.HealthMonitor(nil), err)
		record(outputs, client, "loadbalancer/pool_member", "load balancer pool members", []vngcloud.PoolMember(nil), err)
		record(outputs, client, "loadbalancer/policy", "load balancer policies", []vngcloud.Policy(nil), err)
		record(outputs, client, "loadbalancer/policy_detail", "load balancer policy details", []*vngcloud.Policy(nil), err)
		record(outputs, client, "loadbalancer/tag", "load balancer tags", []vngcloud.LoadBalancerTag(nil), err)
		return
	}
	record(outputs, client, "loadbalancer/load_balancer_detail", "load balancer details", children.details, nil)
	record(outputs, client, "loadbalancer/listener", "load balancer listeners", children.listeners, nil)
	record(outputs, client, "loadbalancer/listener_detail", "load balancer listener details", children.listenerDetails, nil)
	record(outputs, client, "loadbalancer/pool", "load balancer pools", children.pools, nil)
	record(outputs, client, "loadbalancer/pool_detail", "load balancer pool details", children.poolDetails, nil)
	record(outputs, client, "loadbalancer/pool_health_monitor", "load balancer pool health monitors", children.healthMonitors, nil)
	record(outputs, client, "loadbalancer/pool_member", "load balancer pool members", children.poolMembers, nil)
	record(outputs, client, "loadbalancer/policy", "load balancer policies", children.policies, nil)
	record(outputs, client, "loadbalancer/policy_detail", "load balancer policy details", children.policyDetails, nil)
	record(outputs, client, "loadbalancer/tag", "load balancer tags", children.tags, nil)
}

func collectLoadBalancerChildren(ctx context.Context, client *vngcloud.Client, loadBalancers []vngcloud.LoadBalancer) (loadBalancerChildren, error) {
	children := loadBalancerChildren{
		details:         make([]*vngcloud.LoadBalancer, 0, len(loadBalancers)),
		listeners:       []vngcloud.Listener{},
		listenerDetails: []*vngcloud.Listener{},
		pools:           []vngcloud.Pool{},
		poolDetails:     []*vngcloud.Pool{},
		healthMonitors:  []*vngcloud.HealthMonitor{},
		poolMembers:     []vngcloud.PoolMember{},
		policies:        []vngcloud.Policy{},
		policyDetails:   []*vngcloud.Policy{},
		tags:            []vngcloud.LoadBalancerTag{},
	}
	for _, loadBalancer := range loadBalancers {
		detail, err := client.LoadBalancer.GetLoadBalancer(ctx, loadBalancer.UUID)
		if err != nil {
			return children, err
		}
		children.details = append(children.details, detail)

		tags, err := client.LoadBalancer.ListTags(ctx, loadBalancer.UUID)
		if err != nil {
			return children, err
		}
		children.tags = append(children.tags, tags...)

		listeners, err := client.LoadBalancer.ListListeners(ctx, loadBalancer.UUID)
		if err != nil {
			return children, err
		}
		children.listeners = append(children.listeners, listeners...)
		for _, listener := range listeners {
			if err := collectLoadBalancerListener(ctx, client, loadBalancer.UUID, listener.UUID, &children); err != nil {
				return children, err
			}
		}

		pools, err := client.LoadBalancer.ListPools(ctx, loadBalancer.UUID)
		if err != nil {
			return children, err
		}
		children.pools = append(children.pools, pools...)
		for _, pool := range pools {
			if err := collectLoadBalancerPool(ctx, client, loadBalancer.UUID, pool.UUID, &children); err != nil {
				return children, err
			}
		}
	}
	return children, nil
}

func collectLoadBalancerListener(ctx context.Context, client *vngcloud.Client, loadBalancerID, listenerID string, children *loadBalancerChildren) error {
	detail, err := client.LoadBalancer.GetListener(ctx, loadBalancerID, listenerID)
	if err != nil {
		return err
	}
	children.listenerDetails = append(children.listenerDetails, detail)

	policies, err := client.LoadBalancer.ListPolicies(ctx, loadBalancerID, listenerID)
	if err != nil {
		return err
	}
	children.policies = append(children.policies, policies...)
	for _, policy := range policies {
		policyDetail, err := client.LoadBalancer.GetPolicy(ctx, loadBalancerID, listenerID, policy.UUID)
		if err != nil {
			return err
		}
		children.policyDetails = append(children.policyDetails, policyDetail)
	}
	return nil
}

func collectLoadBalancerPool(ctx context.Context, client *vngcloud.Client, loadBalancerID, poolID string, children *loadBalancerChildren) error {
	detail, err := client.LoadBalancer.GetPool(ctx, loadBalancerID, poolID)
	if err != nil {
		return err
	}
	children.poolDetails = append(children.poolDetails, detail)

	healthMonitor, err := client.LoadBalancer.GetPoolHealthMonitor(ctx, loadBalancerID, poolID)
	if err != nil {
		return err
	}
	children.healthMonitors = append(children.healthMonitors, healthMonitor)

	members, err := client.LoadBalancer.ListPoolMembers(ctx, loadBalancerID, poolID)
	if err != nil {
		return err
	}
	children.poolMembers = append(children.poolMembers, members...)
	return nil
}

func showGlobalLoadBalancer(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	loadBalancersResult, err := client.GlobalLoadBalancer.ListLoadBalancers(ctx, nil)
	loadBalancers := []vngcloud.GlobalLoadBalancer(nil)
	if loadBalancersResult != nil {
		loadBalancers = loadBalancersResult.Items
	}
	record(outputs, client, "glb/load_balancer", "glb load balancers", loadBalancers, err)

	var children globalLoadBalancerChildren
	nestedErr := err
	if nestedErr == nil {
		children, nestedErr = collectGlobalLoadBalancerChildren(ctx, client, loadBalancers)
	}
	recordGlobalLoadBalancerChildren(outputs, client, children, nestedErr)
}

func showGlobalLoadBalancerCatalog(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	packages, err := client.GlobalLoadBalancer.ListPackages(ctx)
	recordGlobal(outputs, client, "glb/package", "glb packages", packages, err)

	regions, err := client.GlobalLoadBalancer.ListRegions(ctx)
	recordGlobal(outputs, client, "glb/region", "glb regions", regions, err)
}

type globalLoadBalancerChildren struct {
	details           []*vngcloud.GlobalLoadBalancer
	listeners         []vngcloud.GlobalListener
	listenerDetails   []*vngcloud.GlobalListener
	pools             []vngcloud.GlobalPool
	poolMembers       []vngcloud.GlobalPoolMember
	poolMemberDetails []*vngcloud.GlobalPoolMember
	usageHistories    []*vngcloud.ListGlobalLoadBalancerUsageHistoriesResult
}

func recordGlobalLoadBalancerChildren(outputs *sdkOutputStore, client *vngcloud.Client, children globalLoadBalancerChildren, err error) {
	if err != nil {
		record(outputs, client, "glb/load_balancer_detail", "glb load balancer details", []*vngcloud.GlobalLoadBalancer(nil), err)
		record(outputs, client, "glb/listener", "glb listeners", []vngcloud.GlobalListener(nil), err)
		record(outputs, client, "glb/listener_detail", "glb listener details", []*vngcloud.GlobalListener(nil), err)
		record(outputs, client, "glb/pool", "glb pools", []vngcloud.GlobalPool(nil), err)
		record(outputs, client, "glb/pool_member", "glb pool members", []vngcloud.GlobalPoolMember(nil), err)
		record(outputs, client, "glb/pool_member_detail", "glb pool member details", []*vngcloud.GlobalPoolMember(nil), err)
		record(outputs, client, "glb/usage_history", "glb usage histories", []*vngcloud.ListGlobalLoadBalancerUsageHistoriesResult(nil), err)
		return
	}
	record(outputs, client, "glb/load_balancer_detail", "glb load balancer details", children.details, nil)
	record(outputs, client, "glb/listener", "glb listeners", children.listeners, nil)
	record(outputs, client, "glb/listener_detail", "glb listener details", children.listenerDetails, nil)
	record(outputs, client, "glb/pool", "glb pools", children.pools, nil)
	record(outputs, client, "glb/pool_member", "glb pool members", children.poolMembers, nil)
	record(outputs, client, "glb/pool_member_detail", "glb pool member details", children.poolMemberDetails, nil)
	record(outputs, client, "glb/usage_history", "glb usage histories", children.usageHistories, nil)
}

func collectGlobalLoadBalancerChildren(ctx context.Context, client *vngcloud.Client, loadBalancers []vngcloud.GlobalLoadBalancer) (globalLoadBalancerChildren, error) {
	children := globalLoadBalancerChildren{
		details:        make([]*vngcloud.GlobalLoadBalancer, 0, len(loadBalancers)),
		usageHistories: make([]*vngcloud.ListGlobalLoadBalancerUsageHistoriesResult, 0, len(loadBalancers)),
	}
	for _, loadBalancer := range loadBalancers {
		detail, err := client.GlobalLoadBalancer.GetLoadBalancer(ctx, loadBalancer.ID)
		if err != nil {
			return children, err
		}
		children.details = append(children.details, detail)

		listeners, err := client.GlobalLoadBalancer.ListListeners(ctx, loadBalancer.ID)
		if err != nil {
			return children, err
		}
		children.listeners = append(children.listeners, listeners...)
		for _, listener := range listeners {
			detail, err := client.GlobalLoadBalancer.GetListener(ctx, loadBalancer.ID, listener.ID)
			if err != nil {
				return children, err
			}
			children.listenerDetails = append(children.listenerDetails, detail)
		}

		pools, err := client.GlobalLoadBalancer.ListPools(ctx, loadBalancer.ID)
		if err != nil {
			return children, err
		}
		children.pools = append(children.pools, pools...)
		for _, pool := range pools {
			if err := collectGlobalLoadBalancerPool(ctx, client, loadBalancer.ID, pool.ID, &children); err != nil {
				return children, err
			}
		}

		history, err := client.GlobalLoadBalancer.ListUsageHistories(ctx, loadBalancer.ID, nil)
		if err != nil {
			return children, err
		}
		children.usageHistories = append(children.usageHistories, history)
	}
	return children, nil
}

func collectGlobalLoadBalancerPool(ctx context.Context, client *vngcloud.Client, loadBalancerID, poolID string, children *globalLoadBalancerChildren) error {
	members, err := client.GlobalLoadBalancer.ListPoolMembers(ctx, loadBalancerID, poolID)
	if err != nil {
		return err
	}
	children.poolMembers = append(children.poolMembers, members...)
	for _, member := range members {
		detail, err := client.GlobalLoadBalancer.GetPoolMember(ctx, loadBalancerID, poolID, member.ID)
		if err != nil {
			return err
		}
		children.poolMemberDetails = append(children.poolMemberDetails, detail)
	}
	return nil
}

func showDNS(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	zonesResult, err := client.DNS.ListHostedZones(ctx, nil)
	if err != nil {
		printError("dns hosted zones", err)
		outputs.add("dns/hosted_zone", client, nil, err)
		return
	}
	zones := zonesResult.Items
	record(outputs, client, "dns/hosted_zone", "dns hosted zones", zones, nil)

	zoneDetails := make([]*vngcloud.HostedZone, 0, len(zones))
	records := make([]vngcloud.DNSRecord, 0)
	recordDetails := make([]*vngcloud.DNSRecord, 0)
	for _, zone := range zones {
		if zone.ID == "" {
			continue
		}
		detail, detailErr := client.DNS.GetHostedZone(ctx, zone.ID)
		if detailErr != nil {
			printError("dns hosted zone detail", detailErr)
			continue
		}
		zoneDetails = append(zoneDetails, detail)

		zoneRecords, recordErr := client.DNS.ListRecords(ctx, zone.ID, nil)
		if recordErr != nil {
			printError("dns records", recordErr)
			continue
		}
		records = append(records, zoneRecords.Items...)
		for _, record := range zoneRecords.Items {
			if record.ID == "" {
				continue
			}
			recordDetail, recordDetailErr := client.DNS.GetRecord(ctx, zone.ID, record.ID)
			if recordDetailErr != nil {
				printError("dns record detail", recordDetailErr)
				continue
			}
			recordDetails = append(recordDetails, recordDetail)
		}
	}
	outputs.add("dns/hosted_zone_detail", client, zoneDetails, nil)
	outputs.add("dns/record", client, records, nil)
	outputs.add("dns/record_detail", client, recordDetails, nil)
}

func showContainerRegistry(ctx context.Context, client *vngcloud.Client, outputs *sdkOutputStore) {
	repositories, err := client.ContainerRegistry.ListRepositories(ctx, nil)
	if err != nil {
		printError("container registry repositories", err)
		outputs.add("containerregistry/repository", client, nil, err)
	} else {
		record(outputs, client, "containerregistry/repository", "container registry repositories", repositories.Items, nil)
	}

	users, err := client.ContainerRegistry.ListUsers(ctx, nil)
	if err != nil {
		printError("container registry users", err)
		outputs.add("containerregistry/user", client, nil, err)
		return
	}
	record(outputs, client, "containerregistry/user", "container registry users", users.Items, nil)
}
