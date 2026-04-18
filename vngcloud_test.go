package vngcloud_test

import (
	"testing"

	"danny.vn/vngcloud"
)

func TestPublicFacadeCompile(t *testing.T) {
	_ = vngcloud.Config{}
	_ = &vngcloud.IAMUserAuth{}
	_ = vngcloud.ListOptions{}
	_ = vngcloud.ListServersOptions{}
	_ = vngcloud.Server{}
	_ = vngcloud.Volume{}
	_ = vngcloud.VPC{}
	_ = vngcloud.LoadBalancer{}
	_ = vngcloud.GlobalLoadBalancer{}
	_ = (*vngcloud.GlobalLoadBalancerService)(nil)
	_ = vngcloud.GlobalLoadBalancerPackage{}
	_ = vngcloud.GlobalLoadBalancerRegion{}
	_ = vngcloud.HostedZone{}
	_ = vngcloud.ContainerRepository{}
	_ = (*vngcloud.ContainerRegistryService)(nil)
	_ = vngcloud.PortalUserInfo{}

	if vngcloud.NewClient == nil {
		t.Fatal("NewClient is nil")
	}
}
