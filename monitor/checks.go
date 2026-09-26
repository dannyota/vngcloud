package monitor

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListChecksInput has no fields today; a nil Input is valid. Fields may be
// added later without breaking callers.
type ListChecksInput struct{}

type ListChecksOutput = core.List[Check]

// ListChecks lists every check on the account. The uptime manager returns
// the whole list in one response; there is no paging.
func (c *Client) ListChecks(ctx context.Context, in *ListChecksInput) (*ListChecksOutput, error) {
	const op = "monitor.ListChecks"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var checks []Check
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"uptimes"}),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &checks); err != nil {
		return nil, err
	}
	return &ListChecksOutput{Items: checks}, nil
}

// GetCheckInput identifies the check to read.
type GetCheckInput struct {
	CheckID string `vngcloud:"required"`
}

type GetCheckOutput struct {
	Check Check
}

func (c *Client) GetCheck(ctx context.Context, in *GetCheckInput) (*GetCheckOutput, error) {
	const op = "monitor.GetCheck"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CheckID", in.CheckID); err != nil {
		return nil, err
	}

	var check Check
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.route([]string{"uptimes", in.CheckID}),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &check); err != nil {
		return nil, err
	}
	return &GetCheckOutput{Check: check}, nil
}
