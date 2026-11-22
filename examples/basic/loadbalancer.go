package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

func showLoadBalancer(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	lbClient := loadbalancer.New(cfg)

	loadBalancers, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]loadbalancer.LoadBalancer, int, error) {
		out, err := lbClient.ListLoadBalancers(ctx, &loadbalancer.ListLoadBalancersInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "loadbalancer/load_balancer", "load balancers", loadBalancers, err)

	var children loadBalancerChildren
	nestedErr := err
	if nestedErr == nil {
		children, nestedErr = collectLoadBalancerChildren(ctx, lbClient, loadBalancers)
	}
	recordLoadBalancerChildren(outputs, cfg, children, nestedErr)

	packagesOut, err := lbClient.ListPackages(ctx, nil)
	packages := []loadbalancer.Package(nil)
	if packagesOut != nil {
		packages = packagesOut.Items
	}
	record(outputs, cfg, "loadbalancer/package", "load balancer packages", packages, err)

	certificates, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]loadbalancer.Certificate, int, error) {
		out, err := lbClient.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "loadbalancer/certificate", "certificates", certificates, err)

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
	record(outputs, cfg, "loadbalancer/certificate_detail", "certificate details", certificateDetails, certificateErr)
}

type loadBalancerChildren struct {
	called          map[string]bool
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

func recordLoadBalancerChildren(outputs *sdkOutputStore, cfg vngcloud.Config, children loadBalancerChildren, err error) {
	if err != nil {
		recordLoadBalancerChild(outputs, cfg, children.called, "load_balancer_detail", "load balancer details", []*loadbalancer.LoadBalancer(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "listener", "load balancer listeners", []loadbalancer.Listener(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "listener_detail", "load balancer listener details", []*loadbalancer.Listener(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "pool", "load balancer pools", []loadbalancer.Pool(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "pool_detail", "load balancer pool details", []*loadbalancer.Pool(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "pool_health_monitor", "load balancer pool health monitors", []*loadbalancer.HealthMonitor(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "pool_member", "load balancer pool members", []loadbalancer.PoolMember(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "policy", "load balancer policies", []loadbalancer.Policy(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "policy_detail", "load balancer policy details", []*loadbalancer.Policy(nil), err)
		recordLoadBalancerChild(outputs, cfg, children.called, "tag", "load balancer tags", []loadbalancer.Tag(nil), err)
		return
	}
	recordLoadBalancerChild(outputs, cfg, children.called, "load_balancer_detail", "load balancer details", children.details, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "listener", "load balancer listeners", children.listeners, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "listener_detail", "load balancer listener details", children.listenerDetails, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "pool", "load balancer pools", children.pools, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "pool_detail", "load balancer pool details", children.poolDetails, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "pool_health_monitor", "load balancer pool health monitors", children.healthMonitors, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "pool_member", "load balancer pool members", children.poolMembers, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "policy", "load balancer policies", children.policies, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "policy_detail", "load balancer policy details", children.policyDetails, nil)
	recordLoadBalancerChild(outputs, cfg, children.called, "tag", "load balancer tags", children.tags, nil)
}

func recordLoadBalancerChild[T any](outputs *sdkOutputStore, cfg vngcloud.Config, called map[string]bool, name, label string, items []T, err error) {
	if called[name] {
		record(outputs, cfg, "loadbalancer/"+name, label, items, err)
	}
}

func collectLoadBalancerChildren(ctx context.Context, lbClient *loadbalancer.Client, loadBalancers []loadbalancer.LoadBalancer) (loadBalancerChildren, error) {
	children := loadBalancerChildren{
		called:          map[string]bool{},
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
		children.called["load_balancer_detail"] = true
		detailOut, err := lbClient.GetLoadBalancer(ctx, &loadbalancer.GetLoadBalancerInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.details = append(children.details, &detailOut.LoadBalancer)

		children.called["tag"] = true

		tagsOut, err := lbClient.ListTags(ctx, &loadbalancer.ListTagsInput{LoadBalancerID: lb.UUID})
		if err != nil {
			return children, err
		}
		children.tags = append(children.tags, tagsOut.Items...)

		children.called["listener"] = true

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

		children.called["pool"] = true

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
	children.called["listener_detail"] = true
	detailOut, err := lbClient.GetListener(ctx, &loadbalancer.GetListenerInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID})
	if err != nil {
		return err
	}
	children.listenerDetails = append(children.listenerDetails, &detailOut.Listener)

	children.called["policy"] = true

	policiesOut, err := lbClient.ListPolicies(ctx, &loadbalancer.ListPoliciesInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID})
	if err != nil {
		return err
	}
	children.policies = append(children.policies, policiesOut.Items...)
	for _, policy := range policiesOut.Items {
		children.called["policy_detail"] = true
		policyDetailOut, err := lbClient.GetPolicy(ctx, &loadbalancer.GetPolicyInput{LoadBalancerID: loadBalancerID, ListenerID: listenerID, PolicyID: policy.UUID})
		if err != nil {
			return err
		}
		children.policyDetails = append(children.policyDetails, &policyDetailOut.Policy)
	}
	return nil
}

func collectLoadBalancerPool(ctx context.Context, lbClient *loadbalancer.Client, loadBalancerID, poolID string, children *loadBalancerChildren) error {
	children.called["pool_detail"] = true
	detailOut, err := lbClient.GetPool(ctx, &loadbalancer.GetPoolInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.poolDetails = append(children.poolDetails, &detailOut.Pool)

	children.called["pool_health_monitor"] = true

	healthMonitorOut, err := lbClient.GetPoolHealthMonitor(ctx, &loadbalancer.GetPoolHealthMonitorInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.healthMonitors = append(children.healthMonitors, &healthMonitorOut.HealthMonitor)

	children.called["pool_member"] = true

	membersOut, err := lbClient.ListPoolMembers(ctx, &loadbalancer.ListPoolMembersInput{LoadBalancerID: loadBalancerID, PoolID: poolID})
	if err != nil {
		return err
	}
	children.poolMembers = append(children.poolMembers, membersOut.Items...)
	return nil
}
