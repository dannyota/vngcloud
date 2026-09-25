package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

func showLoadBalancer(ctx context.Context, client *vngcloud.Client, cfg vngcloud.Config, outputs *sdkOutputStore) {
	lbClient := loadbalancer.New(cfg)

	loadBalancers, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]loadbalancer.LoadBalancer, int, error) {
		out, err := lbClient.ListLoadBalancers(ctx, &loadbalancer.ListLoadBalancersInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, client, "loadbalancer/load_balancer", "load balancers", loadBalancers, err)

	var children loadBalancerChildren
	nestedErr := err
	if nestedErr == nil {
		children, nestedErr = collectLoadBalancerChildren(ctx, lbClient, loadBalancers)
	}
	recordLoadBalancerChildren(outputs, client, children, nestedErr)

	packagesOut, err := lbClient.ListPackages(ctx, nil)
	packages := []loadbalancer.Package(nil)
	if packagesOut != nil {
		packages = packagesOut.Items
	}
	record(outputs, client, "loadbalancer/package", "load balancer packages", packages, err)

	certificates, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]loadbalancer.Certificate, int, error) {
		out, err := lbClient.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, client, "loadbalancer/certificate", "certificates", certificates, err)

	certificateDetails, certificateErr := collectDetails(certificates,
		func(certificate loadbalancer.Certificate) string { return certificate.UUID },
		func(id string) (*loadbalancer.Certificate, error) {
			out, err := lbClient.GetCertificate(ctx, &loadbalancer.GetCertificateInput{CertificateID: id})
			if err != nil {
				return nil, err
			}
			return &out.Certificate, nil
		},
	)
	if err != nil {
		certificateErr = err
	}
	record(outputs, client, "loadbalancer/certificate_detail", "certificate details", certificateDetails, certificateErr)
}

type loadBalancerChildren struct {
	details         []*loadbalancer.LoadBalancer
	listeners       []loadbalancer.Listener
	listenerDetails []*loadbalancer.Listener
	pools           []loadbalancer.Pool
	poolDetails     []*loadbalancer.Pool
	healthMonitors  []*loadbalancer.HealthMonitor
	poolMembers     []loadbalancer.PoolMember
	policies        []loadbalancer.Policy
	policyDetails   []*loadbalancer.Policy
	tags            []loadbalancer.Tag
}

func recordLoadBalancerChildren(outputs *sdkOutputStore, client *vngcloud.Client, children loadBalancerChildren, err error) {
	if err != nil {
		record(outputs, client, "loadbalancer/load_balancer_detail", "load balancer details", []*loadbalancer.LoadBalancer(nil), err)
		record(outputs, client, "loadbalancer/listener", "load balancer listeners", []loadbalancer.Listener(nil), err)
		record(outputs, client, "loadbalancer/listener_detail", "load balancer listener details", []*loadbalancer.Listener(nil), err)
		record(outputs, client, "loadbalancer/pool", "load balancer pools", []loadbalancer.Pool(nil), err)
		record(outputs, client, "loadbalancer/pool_detail", "load balancer pool details", []*loadbalancer.Pool(nil), err)
		record(outputs, client, "loadbalancer/pool_health_monitor", "load balancer pool health monitors", []*loadbalancer.HealthMonitor(nil), err)
		record(outputs, client, "loadbalancer/pool_member", "load balancer pool members", []loadbalancer.PoolMember(nil), err)
		record(outputs, client, "loadbalancer/policy", "load balancer policies", []loadbalancer.Policy(nil), err)
		record(outputs, client, "loadbalancer/policy_detail", "load balancer policy details", []*loadbalancer.Policy(nil), err)
		record(outputs, client, "loadbalancer/tag", "load balancer tags", []loadbalancer.Tag(nil), err)
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

func collectLoadBalancerChildren(ctx context.Context, lbClient *loadbalancer.Client, loadBalancers []loadbalancer.LoadBalancer) (loadBalancerChildren, error) {
	children := loadBalancerChildren{
		details:         make([]*loadbalancer.LoadBalancer, 0, len(loadBalancers)),
		listeners:       []loadbalancer.Listener{},
		listenerDetails: []*loadbalancer.Listener{},
		pools:           []loadbalancer.Pool{},
		poolDetails:     []*loadbalancer.Pool{},
		healthMonitors:  []*loadbalancer.HealthMonitor{},
		poolMembers:     []loadbalancer.PoolMember{},
		policies:        []loadbalancer.Policy{},
		policyDetails:   []*loadbalancer.Policy{},
		tags:            []loadbalancer.Tag{},
	}
	for _, lb := range loadBalancers {
		detailOut, err := lbClient.GetLoadBalancer(ctx, &loadbalancer.GetLoadBalancerInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.details = append(children.details, &detailOut.LoadBalancer)

		tagsOut, err := lbClient.ListTags(ctx, &loadbalancer.ListTagsInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.tags = append(children.tags, tagsOut.Items...)

		listenersOut, err := lbClient.ListListeners(ctx, &loadbalancer.ListListenersInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.listeners = append(children.listeners, listenersOut.Items...)
		for _, listener := range listenersOut.Items {
			if err := collectLoadBalancerListener(ctx, lbClient, lb.UUID, listener.UUID, &children); err != nil {
				return children, err
			}
		}

		poolsOut, err := lbClient.ListPools(ctx, &loadbalancer.ListPoolsInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.pools = append(children.pools, poolsOut.Items...)
		for _, pool := range poolsOut.Items {
			if err := collectLoadBalancerPool(ctx, lbClient, lb.UUID, pool.UUID, &children); err != nil {
				return children, err
			}
		}
	}
	return children, nil
}

func collectLoadBalancerListener(ctx context.Context, lbClient *loadbalancer.Client, loadBalancerID, listenerID string, children *loadBalancerChildren) error {
	detailOut, err := lbClient.GetListener(ctx, &loadbalancer.GetListenerInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID})
	if err != nil {
		return err
	}
	children.listenerDetails = append(children.listenerDetails, &detailOut.Listener)

	policiesOut, err := lbClient.ListPolicies(ctx, &loadbalancer.ListPoliciesInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID})
	if err != nil {
		return err
	}
	children.policies = append(children.policies, policiesOut.Items...)
	for _, policy := range policiesOut.Items {
		policyDetailOut, err := lbClient.GetPolicy(ctx, &loadbalancer.GetPolicyInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID, PolicyID: policy.UUID})
		if err != nil {
			return err
		}
		children.policyDetails = append(children.policyDetails, &policyDetailOut.Policy)
	}
	return nil
}

func collectLoadBalancerPool(ctx context.Context, lbClient *loadbalancer.Client, loadBalancerID, poolID string, children *loadBalancerChildren) error {
	detailOut, err := lbClient.GetPool(ctx, &loadbalancer.GetPoolInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.poolDetails = append(children.poolDetails, &detailOut.Pool)

	healthMonitorOut, err := lbClient.GetPoolHealthMonitor(ctx, &loadbalancer.GetPoolHealthMonitorInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.healthMonitors = append(children.healthMonitors, &healthMonitorOut.HealthMonitor)

	membersOut, err := lbClient.ListPoolMembers(ctx, &loadbalancer.ListPoolMembersInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.poolMembers = append(children.poolMembers, membersOut.Items...)
	return nil
}
