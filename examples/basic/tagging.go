package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/tagging"
)

func showTagging(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	networkClient := network.New(cfg)
	taggingClient := tagging.New(cfg)

	virtualIPs, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]network.VirtualIPAddress, int, error) {
		out, err := networkClient.ListVirtualIPAddresses(ctx, &network.ListVirtualIPAddressesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	if err != nil {
		record(outputs, cfg, "tagging/resource_tag", "virtual IP resource tags", []tagging.Tag(nil), err)
		return
	}

	tags := make([]tagging.Tag, 0)
	for _, virtualIP := range virtualIPs {
		if virtualIP.UUID == "" {
			continue
		}
		out, err := taggingClient.ListResourceTags(ctx, &tagging.ListResourceTagsInput{ResourceID: virtualIP.UUID})
		if err != nil {
			record(outputs, cfg, "tagging/resource_tag", "virtual IP resource tags", []tagging.Tag(nil), err)
			return
		}
		tags = append(tags, out.Items...)
	}
	record(outputs, cfg, "tagging/resource_tag", "virtual IP resource tags", tags, nil)
}
