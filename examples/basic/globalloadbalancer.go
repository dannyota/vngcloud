package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/globalloadbalancer"
)

func showGlobalLoadBalancer(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	glbClient := globalloadbalancer.New(cfg)

	loadBalancersOut, err := glbClient.ListLoadBalancers(ctx, nil)
	loadBalancers := []globalloadbalancer.LoadBalancer(nil)
	if loadBalancersOut != nil {
		loadBalancers = loadBalancersOut.Items
	}
	record(outputs, cfg, "glb/load_balancer", "glb load balancers", loadBalancers, err)

	var children globalLoadBalancerChildren
	nestedErr := err
	if nestedErr == nil {
		children, nestedErr = collectGlobalLoadBalancerChildren(ctx, glbClient, loadBalancers)
	}
	recordGlobalLoadBalancerChildren(outputs, cfg, children, nestedErr)
}

func showGlobalLoadBalancerCatalog(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	glbClient := globalloadbalancer.New(cfg)

	packagesOut, err := glbClient.ListPackages(ctx, nil)
	packages := []globalloadbalancer.Package(nil)
	if packagesOut != nil {
		packages = packagesOut.Items
	}
	recordGlobal(outputs, cfg, "glb/package", "glb packages", packages, err)

	regionsOut, err := glbClient.ListRegions(ctx, nil)
	regions := []globalloadbalancer.Region(nil)
	if regionsOut != nil {
		regions = regionsOut.Items
	}
	recordGlobal(outputs, cfg, "glb/region", "glb regions", regions, err)
}

type globalLoadBalancerChildren struct {
	details           []*globalloadbalancer.LoadBalancer
	listeners         []globalloadbalancer.Listener
	listenerDetails   []*globalloadbalancer.Listener
	pools             []globalloadbalancer.Pool
	poolMembers       []globalloadbalancer.PoolMember
	poolMemberDetails []*globalloadbalancer.PoolMember
	usageHistories    []*globalloadbalancer.ListUsageHistoriesOutput
}

func recordGlobalLoadBalancerChildren(outputs *sdkOutputStore, cfg vngcloud.Config, children globalLoadBalancerChildren, err error) {
	if err != nil {
		record(outputs, cfg, "glb/load_balancer_detail", "glb load balancer details", []*globalloadbalancer.LoadBalancer(nil), err)
		record(outputs, cfg, "glb/listener", "glb listeners", []globalloadbalancer.Listener(nil), err)
		record(outputs, cfg, "glb/listener_detail", "glb listener details", []*globalloadbalancer.Listener(nil), err)
		record(outputs, cfg, "glb/pool", "glb pools", []globalloadbalancer.Pool(nil), err)
		record(outputs, cfg, "glb/pool_member", "glb pool members", []globalloadbalancer.PoolMember(nil), err)
		record(outputs, cfg, "glb/pool_member_detail", "glb pool member details", []*globalloadbalancer.PoolMember(nil), err)
		record(outputs, cfg, "glb/usage_history", "glb usage histories", []*globalloadbalancer.ListUsageHistoriesOutput(nil), err)
		return
	}
	record(outputs, cfg, "glb/load_balancer_detail", "glb load balancer details", children.details, nil)
	record(outputs, cfg, "glb/listener", "glb listeners", children.listeners, nil)
	record(outputs, cfg, "glb/listener_detail", "glb listener details", children.listenerDetails, nil)
	record(outputs, cfg, "glb/pool", "glb pools", children.pools, nil)
	record(outputs, cfg, "glb/pool_member", "glb pool members", children.poolMembers, nil)
	record(outputs, cfg, "glb/pool_member_detail", "glb pool member details", children.poolMemberDetails, nil)
	record(outputs, cfg, "glb/usage_history", "glb usage histories", children.usageHistories, nil)
}

func collectGlobalLoadBalancerChildren(ctx context.Context, glbClient *globalloadbalancer.Client, loadBalancers []globalloadbalancer.LoadBalancer) (globalLoadBalancerChildren, error) {
	children := globalLoadBalancerChildren{
		details:        make([]*globalloadbalancer.LoadBalancer, 0, len(loadBalancers)),
		usageHistories: make([]*globalloadbalancer.ListUsageHistoriesOutput, 0, len(loadBalancers)),
	}
	for _, lb := range loadBalancers {
		detailOut, err := glbClient.GetLoadBalancer(ctx, &globalloadbalancer.GetLoadBalancerInput{LoadBalancerID: lb.ID})
		if err != nil {
			return children, err
		}
		children.details = append(children.details, &detailOut.LoadBalancer)

		listenersOut, err := glbClient.ListListeners(ctx, &globalloadbalancer.ListListenersInput{LoadBalancerID: lb.ID})
		if err != nil {
			return children, err
		}
		children.listeners = append(children.listeners, listenersOut.Items...)
		for _, listener := range listenersOut.Items {
			listenerDetailOut, err := glbClient.GetListener(ctx, &globalloadbalancer.GetListenerInput{LoadBalancerID: lb.ID, ListenerID: listener.ID})
			if err != nil {
				return children, err
			}
			children.listenerDetails = append(children.listenerDetails, &listenerDetailOut.Listener)
		}

		poolsOut, err := glbClient.ListPools(ctx, &globalloadbalancer.ListPoolsInput{LoadBalancerID: lb.ID})
		if err != nil {
			return children, err
		}
		children.pools = append(children.pools, poolsOut.Items...)
		for _, pool := range poolsOut.Items {
			if err := collectGlobalLoadBalancerPool(ctx, glbClient, lb.ID, pool.ID, &children); err != nil {
				return children, err
			}
		}

		history, err := glbClient.ListUsageHistories(ctx, &globalloadbalancer.ListUsageHistoriesInput{LoadBalancerID: lb.ID})
		if err != nil {
			return children, err
		}
		children.usageHistories = append(children.usageHistories, history)
	}
	return children, nil
}

func collectGlobalLoadBalancerPool(ctx context.Context, glbClient *globalloadbalancer.Client, loadBalancerID, poolID string, children *globalLoadBalancerChildren) error {
	membersOut, err := glbClient.ListPoolMembers(ctx, &globalloadbalancer.ListPoolMembersInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.poolMembers = append(children.poolMembers, membersOut.Items...)
	for _, member := range membersOut.Items {
		detailOut, err := glbClient.GetPoolMember(ctx, &globalloadbalancer.GetPoolMemberInput{LoadBalancerID: loadBalancerID, PoolID: poolID, PoolMemberID: member.ID})
		if err != nil {
			return err
		}
		children.poolMemberDetails = append(children.poolMemberDetails, &detailOut.PoolMember)
	}
	return nil
}
