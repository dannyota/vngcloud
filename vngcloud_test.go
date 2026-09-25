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
	_ = vngcloud.ContainerRepository{}
	_ = (*vngcloud.ContainerRegistryService)(nil)

	if vngcloud.NewClient == nil {
		t.Fatal("NewClient is nil")
	}
}

func TestPtr(t *testing.T) {
	p := vngcloud.Ptr(false)
	if *p != false {
		t.Fatalf("*Ptr(false) = %v, want false", *p)
	}
	q := vngcloud.Ptr(false)
	if p == q {
		t.Fatal("Ptr returned the same pointer for two calls")
	}
}
