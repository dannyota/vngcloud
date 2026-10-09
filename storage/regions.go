package storage

import (
	"context"

	"danny.vn/vngcloud/internal/core"
)

// Region is a vStorage region.
type Region struct {
	ID             string `json:"regionId"`
	Name           string `json:"regionName"`
	DisplayingName string `json:"regionDisplayingName"`
	Description    string `json:"description"`
	BackendType    string `json:"backendType"`
	S3Host         string `json:"s3Host"`
	VOSAPIHost     string `json:"vosApiHost"`
	AccountURL     string `json:"accountUrl"`
	AuthHost       string `json:"authHost"`
	Status         int    `json:"status"`
}

type ListRegionsInput struct{}

type ListRegionsOutput = core.List[Region]

// ListRegions lists the vStorage regions.
func (c *Client) ListRegions(ctx context.Context, _ *ListRegionsInput) (*ListRegionsOutput, error) {
	const op = "storage.ListRegions"
	env, err := c.do(ctx, op, c.route([]string{"regions"}, nil), "")
	if err != nil {
		return nil, err
	}
	items, err := decodeList[Region](op, 200, env)
	if err != nil {
		return nil, err
	}
	return &ListRegionsOutput{Items: items}, nil
}
