package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
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
	env, err := c.exchange(ctx, call{
		op: op, method: http.MethodGet, ok: []int{http.StatusOK},
		url: c.route([]string{"regions"}, nil),
		validate: func(raw json.RawMessage) error {
			if err := jsonresponse.Validate(raw); err != nil {
				return &core.APIError{Operation: op, StatusCode: http.StatusOK, Err: fmt.Errorf("decode list: %w", err)}
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	items, err := decodeList[Region](op, 200, env)
	if err != nil {
		return nil, err
	}
	return &ListRegionsOutput{Items: items}, nil
}
