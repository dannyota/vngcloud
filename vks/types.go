package vks

import "danny.vn/vngcloud/internal/core"

type ListClustersInput struct {
	Page int
	Size int
}

type ListClustersOutput = core.PagedList[Cluster]
type ListClusterVersionsInput struct{}
type ListClusterVersionsOutput = core.List[ClusterVersion]
type GetQuotaInput struct{}
type GetQuotaOutput struct{ Quota Quota }
