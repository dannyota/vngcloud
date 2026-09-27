package iam

import (
	"context"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListActionsInput takes no fields; the SDK always asks for product "iam".
type ListActionsInput struct{}

type ListActionsOutput = core.List[Action]

// ListActions lists the account's IAM actions (GET actions?product=iam).
// The guards in guard.go call this once per Client and reuse the result to
// decide which policy statements are IAM write actions.
func (c *Client) ListActions(ctx context.Context, in *ListActionsInput) (*ListActionsOutput, error) {
	const op = "iam.ListActions"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	q := url.Values{}
	q.Set("product", "iam")
	actions := []Action{}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"actions"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &actions); err != nil {
		return nil, err
	}
	return &ListActionsOutput{Items: actions}, nil
}
