package iam

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListGroupsInput takes no fields. GET groups returns a bare array with no
// paging.
type ListGroupsInput struct{}

// ListGroupsOutput holds GroupSummary items.
type ListGroupsOutput = core.List[GroupSummary]

// ListGroups reads groups.
func (c *Client) ListGroups(ctx context.Context, in *ListGroupsInput) (*ListGroupsOutput, error) {
	const op = "iam.ListGroups"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	groups := []GroupSummary{}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"groups"}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &groups); err != nil {
		return nil, err
	}
	return &ListGroupsOutput{Items: groups}, nil
}

// GetGroupInput requires GroupID.
type GetGroupInput struct {
	// GroupID is required.
	GroupID string `vngcloud:"required"`
}

// GetGroupOutput holds the result of GetGroup.
type GetGroupOutput struct {
	Group Group
}

// GetGroup reads group.
func (c *Client) GetGroup(ctx context.Context, in *GetGroupInput) (*GetGroupOutput, error) {
	const op = "iam.GetGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}

	type fields Group
	var group fields
	resp := struct {
		Root      flexibleRoot `json:"root"`
		CreatedAt epochMillis  `json:"createdAt"`
		*fields
	}{fields: &group}
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodGet,
		URL:        c.policiesURL([]string{"groups", in.GroupID}, nil),
		OK:         []int{200},
		StrictJSON: true,
		MaxBody:    guardObjectMaxBody,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	group.Root = string(resp.Root)
	group.CreatedAt = int64(resp.CreatedAt)
	return &GetGroupOutput{Group: Group(group)}, nil
}

// ListGroupPoliciesInput pages GroupID's attached policies. A non-positive
// Size sends core.DefaultPageSize, and Page 0 is the first page.
type ListGroupPoliciesInput struct {
	// GroupID is required.
	GroupID string `vngcloud:"required"`
	// Page starts at zero; negative values select zero.
	Page int
	// Size uses the default when not positive.
	Size int
}

// ListGroupPoliciesOutput holds PolicySummary items.
type ListGroupPoliciesOutput = core.List[PolicySummary]

// ListGroupPolicies reads group policies.
func (c *Client) ListGroupPolicies(ctx context.Context, in *ListGroupPoliciesInput) (*ListGroupPoliciesOutput, error) {
	const op = "iam.ListGroupPolicies"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}

	var resp struct {
		Data []PolicySummary `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"groups", in.GroupID, "policies"}, pageQuery(in.Page, in.Size)),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListGroupPoliciesOutput{Items: resp.Data}, nil
}
