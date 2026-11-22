package main

import (
	"context"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/pricing"
)

func showEstimate(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := pricing.New(cfg).Estimate(ctx, &pricing.EstimateInput{
		PublicIPs: []pricing.EstimatePublicIP{
			{Key: "web-ips", Kind: "public-vip", Type: "public-vm", Quantity: 2},
			{Key: "reserved", Kind: "reservation", Quantity: 1},
		},
		Storage: []pricing.EstimateStorage{
			{Key: "objects", Type: "Gold", QuotaGB: 30, Quantity: 2},
		},
		MaxRequests:  20,
		AllowPartial: true,
	})
	recordPricing(outputs, cfg.Region(), "pricing/estimate", "monthly setup estimate", out, err)
}
