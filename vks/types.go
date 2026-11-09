package vks

import "danny.vn/vngcloud/internal/core"

// ListClustersInput selects a page of cluster inventory.
type ListClustersInput struct {
	// Page is zero-based and must be between 0 and 2147483647.
	Page int
	// Size is the page size; zero selects 10 and other values must be positive.
	Size int
}

// ListClustersOutput holds a page of clusters.
type ListClustersOutput = core.PagedList[Cluster]

// ListClusterVersionsInput has no filters.
type ListClusterVersionsInput struct{}

// ListClusterVersionsOutput holds the available cluster versions.
type ListClusterVersionsOutput = core.List[ClusterVersion]

// GetQuotaInput has no filters.
type GetQuotaInput struct{}

// GetQuotaOutput holds the account quota.
type GetQuotaOutput struct{ Quota Quota }
