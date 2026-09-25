package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
)

func showCompute(ctx context.Context, client *vngcloud.Client, cfg vngcloud.Config, outputs *sdkOutputStore) {
	computeClient := compute.New(cfg)

	servers, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]compute.Server, int, error) {
		out, err := computeClient.ListServers(ctx, &compute.ListServersInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, client, "server/instance", "servers", servers, err)

	keys, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]compute.SSHKey, int, error) {
		out, err := computeClient.ListSSHKeys(ctx, &compute.ListSSHKeysInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, client, "server/ssh_key", "ssh keys", keys, err)

	groups, err := computeClient.ListServerGroups(ctx, &compute.ListServerGroupsInput{Page: 0, Size: vngcloud.DefaultPageSize})
	groupItems := []compute.ServerGroup(nil)
	if groups != nil {
		groupItems = groups.Items
	}
	record(outputs, client, "server/placement_group", "placement groups", groupItems, err)

	serverSecgroups, err := computeClient.ListServerSecurityGroups(ctx, nil)
	secgroupItems := []compute.ServerSecurityGroup(nil)
	if serverSecgroups != nil {
		secgroupItems = serverSecgroups.Items
	}
	record(outputs, client, "server/instance_security_group", "server security groups", secgroupItems, err)

	groupMembers, err := computeClient.ListServerGroupMembers(ctx, nil)
	memberItems := []compute.ServerGroupMembership(nil)
	if groupMembers != nil {
		memberItems = groupMembers.Items
	}
	record(outputs, client, "server/placement_group_member", "placement group members", memberItems, err)

	policies, err := computeClient.ListServerGroupPolicies(ctx, nil)
	policyItems := []compute.ServerGroupPolicy(nil)
	if policies != nil {
		policyItems = policies.Items
	}
	record(outputs, client, "server/placement_group_policy", "placement group policies", policyItems, err)

	osImages, err := computeClient.ListOSImages(ctx, nil)
	osImageItems := []compute.OSImage(nil)
	if osImages != nil {
		osImageItems = osImages.Items
	}
	record(outputs, client, "server/system_image_os", "os images", osImageItems, err)

	gpuImages, err := computeClient.ListGPUImages(ctx, nil)
	gpuImageItems := []compute.OSImage(nil)
	if gpuImages != nil {
		gpuImageItems = gpuImages.Items
	}
	record(outputs, client, "server/system_image_gpu", "gpu images", gpuImageItems, err)

	userImages, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]compute.UserImage, int, error) {
		out, err := computeClient.ListUserImages(ctx, &compute.ListUserImagesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, client, "server/user_image", "user images", userImages, err)
}
