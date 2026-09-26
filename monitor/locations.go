package monitor

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListLocationsInput has no fields today; a nil Input is valid. Fields may
// be added later without breaking callers.
type ListLocationsInput struct{}

type ListLocationsOutput = core.List[Location]

// ListLocations lists every probe location the account may use in a
// CreateCheck call's Locations field. The uptime manager returns the whole
// list in one response; there is no paging.
func (c *Client) ListLocations(ctx context.Context, in *ListLocationsInput) (*ListLocationsOutput, error) {
	const op = "monitor.ListLocations"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var locations []Location
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"locations"}),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &locations); err != nil {
		return nil, err
	}
	return &ListLocationsOutput{Items: locations}, nil
}
