package vks

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"

	"danny.vn/vngcloud/internal/core"
)

func (c *Client) ListClusters(ctx context.Context, in *ListClustersInput) (*ListClustersOutput, error) {
	const op = "vks.ListClusters"
	if err := c.validate(); err != nil {
		return nil, err
	}
	page, size := 0, 10
	if in != nil {
		page = in.Page
		if in.Size != 0 {
			size = in.Size
		}
	}
	if page < 0 || page > math.MaxInt32 {
		return nil, fmt.Errorf("%w: %s: Page must be between 0 and 2147483647", core.ErrInvalidInput, op)
	}
	if size <= 0 || size > math.MaxInt32 {
		return nil, fmt.Errorf("%w: %s: Size must be between 1 and 2147483647, or 0 for the default", core.ErrInvalidInput, op)
	}
	var resp struct {
		Items    *[]*Cluster `json:"items"`
		Total    *int64      `json:"total"`
		Page     *int32      `json:"page"`
		PageSize *int32      `json:"pageSize"`
	}
	if err := c.read(ctx, op, "clusters", url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}}, &resp); err != nil {
		return nil, err
	}
	if resp.Items == nil || resp.Total == nil || resp.Page == nil || resp.PageSize == nil || *resp.Total < 0 || *resp.Total > int64(maxInt()) || *resp.Page < 0 || *resp.PageSize <= 0 {
		return nil, malformed(op)
	}
	items := make([]Cluster, 0, len(*resp.Items))
	for _, item := range *resp.Items {
		if item == nil {
			return nil, malformed(op)
		}
		items = append(items, *item)
	}
	total := int(*resp.Total)
	returnedSize := int(*resp.PageSize)
	pages := total / returnedSize
	if total%returnedSize != 0 {
		pages++
	}
	return core.NewPagedList(items, int(*resp.Page), returnedSize, pages, total), nil
}

func maxInt() int { return int(^uint(0) >> 1) }

func (c *Client) ListClusterVersions(ctx context.Context, _ *ListClusterVersionsInput) (*ListClusterVersionsOutput, error) {
	const op = "vks.ListClusterVersions"
	var resp []*struct {
		Version      *string `json:"version"`
		Enable       *bool   `json:"enable"`
		Stage        *string `json:"stage"`
		DeprecatedAt *string `json:"deprecatedAt"`
	}
	if err := c.read(ctx, op, "cluster-versions", nil, &resp); err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, malformed(op)
	}
	items := make([]ClusterVersion, 0, len(resp))
	for _, v := range resp {
		if v == nil || v.Version == nil || v.Enable == nil || v.Stage == nil {
			return nil, malformed(op)
		}
		deprecatedAt := ""
		if v.DeprecatedAt != nil {
			deprecatedAt = *v.DeprecatedAt
		}
		items = append(items, ClusterVersion{Version: *v.Version, Enable: *v.Enable, Stage: *v.Stage, DeprecatedAt: deprecatedAt})
	}
	return &ListClusterVersionsOutput{Items: items}, nil
}

func (c *Client) GetQuota(ctx context.Context, _ *GetQuotaInput) (*GetQuotaOutput, error) {
	const op = "vks.GetQuota"
	var resp struct {
		MaxClusters             *int32 `json:"maxClusters"`
		NumClusters             *int32 `json:"numClusters"`
		MaxNodeGroupsPerCluster *int32 `json:"maxNodeGroupsPerCluster"`
		MaxNodesPerNodeGroup    *int32 `json:"maxNodesPerNodeGroup"`
	}
	if err := c.read(ctx, op, "quota", nil, &resp); err != nil {
		return nil, err
	}
	if resp.MaxClusters == nil || resp.NumClusters == nil || resp.MaxNodeGroupsPerCluster == nil || resp.MaxNodesPerNodeGroup == nil {
		return nil, malformed(op)
	}
	return &GetQuotaOutput{Quota: Quota{MaxClusters: int(*resp.MaxClusters), NumClusters: int(*resp.NumClusters), MaxNodeGroupsPerCluster: int(*resp.MaxNodeGroupsPerCluster), MaxNodesPerNodeGroup: int(*resp.MaxNodesPerNodeGroup)}}, nil
}
