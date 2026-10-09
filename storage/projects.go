package storage

import (
	"context"

	"danny.vn/vngcloud/internal/core"
)

// Project is a vStorage project, a paid storage package in one region. The
// fields follow the vStorage API specification; no live project was
// available to confirm them.
type Project struct {
	ID         string  `json:"projectId"`
	Name       string  `json:"projectName"`
	RegionID   string  `json:"regionId"`
	RegionName string  `json:"regionName"`
	Status     int     `json:"status"`
	TotalQuota float64 `json:"totalQuota"`
	StartTime  string  `json:"startTime"`
	EndTime    string  `json:"endTime"`
	Period     int     `json:"period"`
}

type ListProjectsInput struct {
	Region string
}

type ListProjectsOutput = core.List[Project]

// ListProjects lists the vStorage projects in a region.
func (c *Client) ListProjects(ctx context.Context, in *ListProjectsInput) (*ListProjectsOutput, error) {
	const op = "storage.ListProjects"
	region := ""
	if in != nil {
		region = in.Region
	}
	id, err := c.regionID(ctx, op, region)
	if err != nil {
		return nil, err
	}
	env, err := c.do(ctx, op, c.route([]string{"projects"}, nil), id)
	if err != nil {
		return nil, err
	}
	items, err := decodeList[Project](op, 200, env)
	if err != nil {
		return nil, err
	}
	return &ListProjectsOutput{Items: items}, nil
}
