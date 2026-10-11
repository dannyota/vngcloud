package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func showNetworkVPN(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	out, err := network.New(cfg).ListVPNConnections(ctx, nil)
	var items []network.VPNConnection
	if out != nil {
		items = out.Items
	}
	record(outputs, cfg, "network/vpn_connection", "VPN connections (first page)", items, err)
}
