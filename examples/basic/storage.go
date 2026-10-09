package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

// showStorage records the vStorage regions, the projects in the configured
// region's vStorage region, and, for the first project, its buckets and the
// first bucket's detail. Storage is account-wide, so main calls this once
// per config.
func showStorage(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	client := storage.New(cfg)

	regions, err := client.ListRegions(ctx, nil)
	recordAccount(outputs, "storage/region", "storage regions", storageRegionItems(regions), err)

	projects, err := client.ListProjects(ctx, nil)
	recordAccount(outputs, "storage/project", "storage projects", storageProjectItems(projects), err)
	if err != nil || projects == nil || len(projects.Items) == 0 {
		return
	}

	buckets, err := client.ListBuckets(ctx, &storage.ListBucketsInput{ProjectID: projects.Items[0].ID})
	recordAccount(outputs, "storage/bucket", "storage buckets", storageBucketItems(buckets), err)
	if err != nil || buckets == nil || len(buckets.Items) == 0 {
		return
	}

	detail, err := client.GetBucket(ctx, &storage.GetBucketInput{ProjectID: projects.Items[0].ID, Bucket: buckets.Items[0].Name})
	recordAccountOne(outputs, "storage/bucket_detail", "storage bucket detail", detail, err)
}

func storageRegionItems(result *storage.ListRegionsOutput) []storage.Region {
	if result == nil {
		return nil
	}
	return result.Items
}

func storageProjectItems(result *storage.ListProjectsOutput) []storage.Project {
	if result == nil {
		return nil
	}
	return result.Items
}

func storageBucketItems(result *storage.ListBucketsOutput) []storage.Bucket {
	if result == nil {
		return nil
	}
	return result.Items
}
