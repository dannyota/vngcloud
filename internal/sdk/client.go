package sdk

import (
	"context"

	"danny.vn/vngcloud/internal/compute"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/glb"
	"danny.vn/vngcloud/internal/loadbalancer"
	"danny.vn/vngcloud/internal/network"
	"danny.vn/vngcloud/internal/volume"
)

type Client struct {
	*core.Client

	Compute            *compute.Service
	Network            *network.Service
	Volume             *volume.Service
	LoadBalancer       *loadbalancer.Service
	GlobalLoadBalancer *glb.Service
}

func NewClient(ctx context.Context, cfg core.Config) (*Client, error) {
	base := core.ClientOf(cfg)
	c := &Client{Client: base}
	c.Compute = compute.New(base)
	c.Network = network.New(base)
	c.Volume = volume.New(base)
	c.LoadBalancer = loadbalancer.New(base)
	c.GlobalLoadBalancer = glb.New(base)
	if err := c.Authenticate(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
