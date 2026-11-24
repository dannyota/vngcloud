package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/backup"
)

func showBackup(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	if cfg.Region() != "hcm-3" {
		return
	}
	client := backup.New(cfg)
	backends, err := client.ListBackends(ctx, nil)
	recordPricing(outputs, cfg.Region(), "backup/backends", "backup backends", backends, err)
	policies, err := client.ListPolicies(ctx, nil)
	recordPricing(outputs, cfg.Region(), "backup/policies", "backup policies", policies, err)
	if err != nil || policies == nil || len(policies.Items) == 0 || policies.Items[0].ID == "" {
		return
	}
	policy, err := client.GetPolicy(ctx, &backup.GetPolicyInput{PolicyID: policies.Items[0].ID})
	recordPricing(outputs, cfg.Region(), "backup/get-policy", "backup policy", policy, err)
}
