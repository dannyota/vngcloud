package sdk

import (
	"context"

	"danny.vn/vngcloud/internal/compute"
	"danny.vn/vngcloud/internal/containerregistry"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/dns"
	"danny.vn/vngcloud/internal/glb"
	"danny.vn/vngcloud/internal/loadbalancer"
	"danny.vn/vngcloud/internal/network"
	"danny.vn/vngcloud/internal/portal"
	"danny.vn/vngcloud/internal/volume"
)

type Client struct {
	*core.Client

	Compute            *compute.Service
	Network            *network.Service
	Volume             *volume.Service
	LoadBalancer       *loadbalancer.Service
	GlobalLoadBalancer *glb.Service
	DNS                *dns.Service
	ContainerRegistry  *containerregistry.Service
	Portal             *portal.Service
}

func NewClient(ctx context.Context, cfg core.Config, opts ...core.ClientOption) (*Client, error) {
	base, err := core.NewClient(cfg, opts...)
	if err != nil {
		return nil, err
	}
	c := &Client{Client: base}
	c.Compute = compute.New(base)
	c.Network = network.New(base)
	c.Volume = volume.New(base)
	c.LoadBalancer = loadbalancer.New(base)
	c.GlobalLoadBalancer = glb.New(base)
	c.DNS = dns.New(base)
	c.ContainerRegistry = containerregistry.New(base)
	c.Portal = portal.New(base)
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
