package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/vks"
)

func showVKS(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := vks.New(cfg)
	clusters, err := client.ListClusters(ctx, nil)
	var items []vks.Cluster
	if clusters != nil {
		items = clusters.Items
	}
	outputs.addPricing("vks/cluster", cfg.Region(), items, err)
	printResult("vks clusters", len(items), err)
	versions, err := client.ListClusterVersions(ctx, nil)
	var catalog []vks.ClusterVersion
	if versions != nil {
		catalog = versions.Items
	}
	outputs.addPricing("vks/cluster_version", cfg.Region(), catalog, err)
	printResult("vks cluster versions", len(catalog), err)
	quota, err := client.GetQuota(ctx, nil)
	recordPricing(outputs, cfg.Region(), "vks/quota", "vks quota", quota, err)
}
