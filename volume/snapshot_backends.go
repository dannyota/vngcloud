package volume

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

type ListSnapshotBackendsInput struct {
	Name string `vngcloud:"required"`
}

type ListSnapshotBackendsOutput = core.List[SnapshotBackend]

func (c *Client) ListSnapshotBackends(ctx context.Context, in *ListSnapshotBackendsInput) (*ListSnapshotBackendsOutput, error) {
	const op = "volume.ListSnapshotBackends"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: %s requires Name", core.ErrInvalidInput, op)
	}
	if err := c.checkSnapshotRegion(op); err != nil {
		return nil, err
	}
	var resp snapshotBackendsResponse
	if err := c.c.DoJSON(ctx, transport.Request{Operation: op, Method: "GET", URL: c.snapshotBackupURL("backends", url.Values{"backend": {in.Name}}), OK: []int{200}}, &resp); err != nil {
		return nil, err
	}
	if resp.Items == nil {
		return nil, snapshotShapeError(op)
	}
	return &ListSnapshotBackendsOutput{Items: resp.Items}, nil
}

type SnapshotBackend struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type snapshotBackendsResponse struct {
	Items []SnapshotBackend `json:"items"`
}

func (r *snapshotBackendsResponse) UnmarshalJSON(data []byte) error {
	type wire snapshotBackendsResponse
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return snapshotShapeError("volume.ListSnapshotBackends")
	}
	*r = snapshotBackendsResponse(decoded)
	return nil
}
