package core

import (
	"log/slog"

	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func NewTestClient(region, projectID string, endpointSet endpoints.Set, tc *transport.Client) *Client {
	logger := slog.New(nopHandler{})
	return &Client{
		region:    region,
		projectID: projectID,
		endpoints: endpointSet,
		transport: tc,
		logger:    logger,
	}
}
