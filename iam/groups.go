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

type ListGroupsOutput = core.List[GroupSummary]

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

type GetGroupInput struct {
	GroupID string `vngcloud:"required"`
}

type GetGroupOutput struct {
	Group Group
}

func (c *Client) GetGroup(ctx context.Context, in *GetGroupInput) (*GetGroupOutput, error) {
	const op = "iam.GetGroup"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "GroupID", in.GroupID); err != nil {
		return nil, err
	}

	var group Group
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.policiesURL([]string{"groups", in.GroupID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &group); err != nil {
		return nil, err
	}
	return &GetGroupOutput{Group: group}, nil
}

// ListGroupPoliciesInput pages GroupID's attached policies. A non-positive
// Size sends core.DefaultPageSize, and Page 0 is the first page.
type ListGroupPoliciesInput struct {
	GroupID string `vngcloud:"required"`
	Page    int
	Size    int
}

type ListGroupPoliciesOutput = core.List[PolicySummary]

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
