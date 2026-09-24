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

// NewTestConfig builds a Config around a client wired for a test server,
// bypassing NewConfig's option parsing and login.
func NewTestConfig(region, projectID string, endpointSet endpoints.Set, tc *transport.Client) Config {
	return Config{client: NewTestClient(region, projectID, endpointSet, tc)}
}
