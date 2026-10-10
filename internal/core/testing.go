package core

import (
	"log/slog"
	"time"

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

// NewTestConfigWithCDNAPIKey is NewTestConfig for a Config that also holds a
// vCDN API key.
func NewTestConfigWithCDNAPIKey(region, projectID string, endpointSet endpoints.Set, tc *transport.Client, key string) Config {
	c := NewTestClient(region, projectID, endpointSet, tc)
	k := cdnKey(key)
	c.cdnAPIKey = &k
	return Config{client: c}
}

// WithStorageTestClock injects a per-config clock without a public SDK option.
func WithStorageTestClock(now func() time.Time) ClientOption {
	return clientOptionFunc(func(cfg *clientConfig) { cfg.storageTestClock = now })
}

func StorageClock(cfg Config) func() time.Time {
	if now := ClientOf(cfg).storageTestClock; now != nil {
		return now
	}
	return time.Now
}
