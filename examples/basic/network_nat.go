package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func showNetworkNAT(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	out, err := network.New(cfg).ListNATInstances(ctx, nil)
	var items []network.NATInstance
	if out != nil {
		items = out.Items
	}
	record(outputs, cfg, "network/nat_instance", "NAT instances (first page)", items, err)
}
